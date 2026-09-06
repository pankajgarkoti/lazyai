import { stat } from "node:fs/promises"
import { resolve } from "node:path"
import { type Plugin, tool } from "@opencode-ai/plugin"

// LazyAI integration plugin.
//
// OpenCode runs unchanged; this plugin reports what the agent touches to the
// LazyAI host (file reads / writes, tool activity) and adds two tools:
// `show_locations`, which points the user at exact code locations with a
// note, and `setup_workstreams`, which opens workstreams exactly like the
// user's `w` key does.

const url = process.env.LAZYAI_HOOK_URL
const token = process.env.LAZYAI_HOOK_TOKEN

async function send(event: Record<string, unknown>): Promise<Response> {
  const res = await fetch(`${url}/event`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
    body: JSON.stringify(event),
  })
  if (!res.ok) {
    throw new Error((await res.text().catch(() => "")) || `LazyAI rejected event (${res.status})`)
  }
  return res
}

async function post(event: Record<string, unknown>): Promise<void> {
  if (!url || !token) return
  await send(event)
}

// request sends an event LazyAI answers with a JSON body (setup_workstreams).
async function request<T>(event: Record<string, unknown>): Promise<T> {
  const res = await send(event)
  return (await res.json()) as T
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
      // callID pairs this with tool.after so overlapping calls are counted,
      // not collapsed into one boolean.
      report({ type: "tool.before", tool: input.tool, sessionID: input.sessionID, callID: input.callID })
      if (input.tool !== "edit" && input.tool !== "write") return
      const p = pathArg(output.args)
      if (!p) return
      report({ type: "file.before", tool: input.tool, sessionID: input.sessionID, path: resolve(ctx.directory, p) })
    },

    "tool.execute.after": async (input, output) => {
      report({ type: "tool.after", tool: input.tool, sessionID: input.sessionID, callID: input.callID })
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

      setup_workstreams: tool({
        description:
          "Open LazyAI workstreams (one OpenCode per git worktree) exactly as the user's `w` key does: each entry gets a branch, a short nickname the user will see in the sidebar, and an optional one-line description reminding them what it is for. Existing branches and dormant worktrees are reused; new branches start from `base` (default: the repository's main branch). Use only when the user asks to set up workstreams. The whole batch is validated first; when it would create more than one new branch LazyAI asks the user to confirm. The user's current workstream keeps focus.",
        args: {
          workstreams: tool.schema
            .array(
              tool.schema.object({
                branch: tool.schema.string().min(1).max(200).describe("Git branch name (created if missing)"),
                nickname: tool.schema.string().min(1).max(60).describe("Short human name shown in the sidebar"),
                description: tool.schema.string().max(240).optional().describe("One line: what this workstream is for"),
                base: tool.schema.string().min(1).max(200).optional().describe("Start point for a new branch; must exist"),
              }),
            )
            .min(1)
            .max(10),
        },
        async execute(args, context) {
          if (!url || !token) {
            throw new Error("LazyAI is not connected (LAZYAI_HOOK_URL unset). Ask the user to press w instead.")
          }
          type Result = {
            branch: string
            nickname: string
            root?: string
            created: boolean
            launched: boolean
            error?: string
          }
          const reply = await request<{ workstreams: Result[] }>({
            type: "setup",
            sessionID: context.sessionID,
            workstreams: args.workstreams.map((w) => ({
              branch: w.branch,
              nickname: w.nickname,
              description: w.description ?? "",
              base: w.base ?? "",
            })),
          })
          const results = reply.workstreams ?? []
          const ok = results.filter((r) => r.launched).length
          context.metadata({ title: `Set up ${ok}/${results.length} workstream${results.length === 1 ? "" : "s"}` })
          const lines = results.map((r) => {
            const state = r.error ? `failed: ${r.error}` : r.created ? "created" : "opened"
            return `- ${r.branch} (${r.nickname}): ${state}${r.root ? ` at ${r.root}` : ""}`
          })
          return [
            `${ok} of ${results.length} workstreams are running in LazyAI. The user switches with h / l or Ctrl+Space 1-9; each has its own OpenCode.`,
            ...lines,
          ].join("\n")
        },
      }),
    },
  }
}

export default LazyAIPlugin
