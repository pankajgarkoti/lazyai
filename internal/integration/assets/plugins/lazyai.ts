import { stat } from "node:fs/promises"
import { resolve } from "node:path"
import { type Plugin, tool } from "@opencode-ai/plugin"

// LazyAI integration plugin.
//
// OpenCode runs unchanged; this plugin only reports what the agent touches to
// the LazyAI host (file reads / writes) and adds one tool, `show_locations`,
// that lets the agent point the user at exact code locations with a note.

const url = process.env.LAZYAI_HOOK_URL
const token = process.env.LAZYAI_HOOK_TOKEN

async function post(event: Record<string, unknown>): Promise<void> {
  if (!url || !token) return
  const res = await fetch(`${url}/event`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
    body: JSON.stringify(event),
  })
  if (!res.ok) {
    throw new Error((await res.text().catch(() => "")) || `LazyAI rejected event (${res.status})`)
  }
}

// Fire-and-forget for observational hooks: never let reporting break a tool.
function report(event: Record<string, unknown>): void {
  post(event).catch(() => {})
}

const FILE_TOOLS = new Set(["read", "edit", "write"])

function pathArg(args: unknown): string | undefined {
  if (!args || typeof args !== "object") return undefined
  const a = args as Record<string, unknown>
  const p = a.filePath ?? a.path ?? a.file
  return typeof p === "string" && p.length > 0 ? p : undefined
}

const LazyAIPlugin: Plugin = async (ctx) => {
  report({ type: "hello", version: 1 })

  return {
    // Activity: LazyAI shows a spinner on the workstream while a tool runs
    // and an attention flag when OpenCode waits on the user.
    event: async ({ event }) => {
      const type = (event as { type?: string }).type
      if (type === "session.idle") report({ type: "idle" })
      else if (type === "permission.updated" || type === "permission.asked") report({ type: "attention" })
    },

    "tool.execute.before": async (input, output) => {
      report({ type: "tool.before", tool: input.tool, sessionID: input.sessionID })
      if (input.tool !== "edit" && input.tool !== "write") return
      const p = pathArg(output.args)
      if (!p) return
      report({ type: "file.before", tool: input.tool, sessionID: input.sessionID, path: resolve(ctx.directory, p) })
    },

    "tool.execute.after": async (input, output) => {
      report({ type: "tool.after", tool: input.tool, sessionID: input.sessionID })
      if (!FILE_TOOLS.has(input.tool)) return
      const p = pathArg(input.args)
      if (!p) return
      report({
        type: input.tool === "read" ? "file.read" : "file.write",
        tool: input.tool,
        sessionID: input.sessionID,
        path: resolve(ctx.directory, p),
      })
    },

    tool: {
      show_locations: tool({
        description:
          "Show the user exact code locations in LazyAI's Show panel, each with a short note explaining why it matters. Use when pointing at specific code is clearer than describing it. Sends the complete ordered set in one call; a new call replaces the previous set.",
        args: {
          locations: tool.schema
            .array(
              tool.schema.object({
                path: tool.schema.string().min(1).describe("Absolute path or path relative to the project"),
                line: tool.schema.number().int().positive().describe("Exact 1-based line number"),
                column: tool.schema.number().int().positive().optional().describe("Exact 1-based column; defaults to 1"),
                text: tool.schema.string().optional().describe("Short note: why this location matters"),
              }),
            )
            .min(1)
            .max(200),
          title: tool.schema.string().min(1).max(120).optional().describe("Title for the set of locations"),
        },
        async execute(args, context) {
          if (!url || !token) {
            throw new Error("LazyAI is not connected (LAZYAI_HOOK_URL unset). Describe the locations in text instead.")
          }

          const checked = await Promise.all(
            args.locations.map(async (location) => {
              const path = resolve(context.directory, location.path)
              const info = await stat(path).catch(() => undefined)
              return { location, path, valid: info?.isFile() === true }
            }),
          )
          const invalid = checked.filter((e) => !e.valid).map((e) => e.path)
          if (invalid.length > 0) {
            throw new Error(`Cannot show missing or non-file paths:\n${invalid.join("\n")}`)
          }

          const seen = new Set<string>()
          const locations = checked.flatMap(({ location, path }) => {
            const column = location.column ?? 1
            const key = `${path}\0${location.line}\0${column}`
            if (seen.has(key)) return []
            seen.add(key)
            return [{ path, line: location.line, column, text: location.text ?? "" }]
          })

          await post({
            type: "show",
            sessionID: context.sessionID,
            title: args.title || "Locations",
            locations,
          })

          const n = locations.length
          context.metadata({ title: `Show ${n} location${n === 1 ? "" : "s"}` })
          return `Showing ${n} location${n === 1 ? "" : "s"} in LazyAI. The user can press r on any entry to reference it in the conversation.`
        },
      }),
    },
  }
}

export default LazyAIPlugin
