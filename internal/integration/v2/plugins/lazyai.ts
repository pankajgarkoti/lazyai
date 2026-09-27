import { stat } from "node:fs/promises"
import { resolve } from "node:path"
import { Plugin } from "@opencode/plugin"

// V2 has a separate plugin API from OpenCode 1. The private --standalone
// server inherits these workstream-scoped credentials from LazyAI's child.
const url = process.env.LAZYAI_HOOK_URL
const token = process.env.LAZYAI_HOOK_TOKEN
const root = process.env.LAZYAI_WORKTREE ?? ""

async function send(event: Record<string, unknown>): Promise<Response> {
  if (!url || !token) throw new Error("LazyAI is not connected")
  const res = await fetch(`${url}/event`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
    body: JSON.stringify(event),
    signal: event.type === "file.snapshot" ? AbortSignal.timeout(3000) : undefined,
  })
  if (!res.ok) throw new Error((await res.text().catch(() => "")) || `LazyAI rejected event (${res.status})`)
  return res
}

function report(event: Record<string, unknown>): void {
  if (url && token) void send(event).catch(() => {})
}

function pathArg(args: unknown): string | undefined {
  if (!args || typeof args !== "object") return undefined
  const a = args as Record<string, unknown>
  const path = a.filePath ?? a.path ?? a.file
  return typeof path === "string" && path.length ? resolve(root, path) : undefined
}

function identity(event: object): { sessionID?: string; callID?: string } {
  const data = event as Record<string, unknown>
  return {
    sessionID: typeof data.sessionID === "string" ? data.sessionID : undefined,
    callID: typeof data.id === "string" ? data.id : undefined,
  }
}

function pathsFor(tool: string, input: unknown): string[] {
  if (tool === "patch" || tool === "apply_patch") {
    if (!input || typeof input !== "object") return []
    const value = input as Record<string, unknown>
    const patch = value.patch ?? value.command
    if (typeof patch !== "string") return []
    const paths = new Set<string>()
    for (const line of patch.split("\n")) {
      const match = line.match(/^\*\*\* (?:Add File|Update File|Delete File|Move to): (.+)$/)
      if (match) paths.add(resolve(root, match[1]))
    }
    return [...paths]
  }
  const path = pathArg(input)
  return path ? [path] : []
}

export default Plugin.define({
  id: "lazyai",
  async setup(ctx) {
    await ctx.session.hook("prompt", (event) => {
      report({ type: "session", sessionID: event.sessionID })
    })

    await ctx.tool.hook("execute.before", async (event) => {
      const info = identity(event)
      report({ type: "tool.before", tool: event.tool, ...info })
      if (!["edit", "write", "patch", "apply_patch"].includes(event.tool)) return
      for (const path of pathsFor(event.tool, event.input)) {
        try {
          // Wait for the host's pre-image before allowing the write to proceed.
          await send({ type: "file.snapshot", tool: event.tool, path, ...info })
        } catch (err) {
          report({ type: "integration.error", title: `Pre-edit snapshot failed: ${String(err)}` })
        }
      }
    })

    await ctx.tool.hook("execute.after", (event) => {
      const info = identity(event)
      report({ type: "tool.after", tool: event.tool, ...info })
      if (event.status !== "completed") return
      if (!["read", "edit", "write", "patch", "apply_patch"].includes(event.tool)) return
      for (const path of pathsFor(event.tool, event.input)) {
        report({ type: event.tool === "read" ? "file.read" : "file.write", tool: event.tool, path, ...info })
      }
    })

    const controller = new AbortController()
    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          if (event.type === "session.idle") report({ type: "idle" })
          if (event.type === "permission.asked") report({ type: "attention" })
        }
      } catch {
        // Unloading aborts the subscription.
      }
    })()

    await ctx.tool.transform((editor) => {
      editor.add({
        name: "show_locations",
        description: "Show exact code locations in LazyAI's Show panel. Send the complete ordered set in one call.",
        input: {
          type: "object",
          properties: {
            title: { type: "string" },
            locations: { type: "array", minItems: 1, maxItems: 200, items: {
              type: "object", properties: {
                path: { type: "string" }, line: { type: "integer", minimum: 1 },
                column: { type: "integer", minimum: 1 }, text: { type: "string" },
              }, required: ["path", "line"], additionalProperties: false,
            } },
          }, required: ["locations"], additionalProperties: false,
        },
        async execute(input, context) {
          const args = input as { title?: string; locations: { path: string; line: number; column?: number; text?: string }[] }
          const checked = await Promise.all(args.locations.map(async (location) => {
            const path = resolve(root, location.path)
            const info = await stat(path).catch(() => undefined)
            return { location, path, valid: info?.isFile() === true }
          }))
          const invalid = checked.filter((e) => !e.valid).map((e) => e.path)
          if (invalid.length) throw new Error(`Cannot show missing or non-file paths:\n${invalid.join("\n")}`)
          const seen = new Set<string>()
          const locations = checked.flatMap(({ location, path }) => {
            const column = location.column ?? 1
            const key = `${path}\0${location.line}\0${column}`
            if (seen.has(key)) return []
            seen.add(key)
            return [{ path, line: location.line, column, text: location.text ?? "" }]
          })
          await send({ type: "show", sessionID: context.sessionID, title: args.title || "Locations", locations })
          return { content: `Showing ${locations.length} locations in LazyAI.` }
        },
      })

      editor.add({
        name: "setup_workstreams",
        description: "Open LazyAI workstreams when the user asks. Existing branches/worktrees are reused; multiple new branches require confirmation.",
        input: {
          type: "object", properties: { workstreams: { type: "array", minItems: 1, maxItems: 10, items: {
            type: "object", properties: {
              branch: { type: "string" }, nickname: { type: "string" },
              description: { type: "string" }, base: { type: "string" },
            }, required: ["branch", "nickname"], additionalProperties: false,
          } } }, required: ["workstreams"], additionalProperties: false,
        },
        async execute(input, context) {
          const args = input as { workstreams: { branch: string; nickname: string; description?: string; base?: string }[] }
          const res = await send({ type: "setup", sessionID: context.sessionID, workstreams: args.workstreams })
          const reply = await res.json() as { workstreams?: { branch: string; nickname: string; root?: string; created: boolean; launched: boolean; error?: string }[] }
          const results = reply.workstreams ?? []
          const ok = results.filter((r) => r.launched).length
          return { content: [
            `${ok} of ${results.length} workstreams are running in LazyAI.`,
            ...results.map((r) => `- ${r.branch} (${r.nickname}): ${r.error ? `failed: ${r.error}` : r.created ? "created" : "opened"}${r.root ? ` at ${r.root}` : ""}`),
          ].join("\n") }
        },
      })
    })
    report({ type: "hello", version: 2 })
    return () => controller.abort()
  },
})
