# Project agent backends

## Contract

One project session selects `agent.backend: opencode | codex` from the canonical
main checkout's `.lazyai/config.yaml`. Every child uses the same frozen backend;
configuration reload cannot create a mixed project. Backend/executable changes
require an explicit session stop and relaunch. OpenCode remains the default.

New projects without `.lazyai/config.yaml` first answer a terminal setup flow:
agent, optional executable, strict mode and default workflow, then a save/start
summary. Setup runs in the attached client before raw mode, supervisors or
worktree creation. Cancellation/EOF is side-effect-free. The canonical main
checkout owns the resulting commented configuration, published atomically without
overwriting an existing or concurrently-created file. Existing projects and
reattachments skip the questions entirely.

The implementation preserves native terminal UIs and routes their telemetry into
the existing LazyAI model. It does not translate conversation histories, models,
permissions or third-party plugins between CLIs. Each CLI retains its own native
configuration and history. Files and worktree identities are shared project state.

## Ownership and flow

1. `cmd/lazyai/session.go` reattaches to an existing supervisor before interpreting
   new launch configuration. New sessions validate configuration and executable
   availability before creating a worktree or supervisor.
2. `internal/agent` prepares the selected backend. OpenCode receives the existing
   additive `OPENCODE_CONFIG_DIR`; Codex receives `-c` session-layer hooks and the
   `lazyai` MCP server. Existing user/project hooks load alongside these hooks.
3. Each workstream has its own hook token. `internal/hooks` authenticates incoming
   events and stamps their workstream identity. Closed children lose their token.
4. `internal/codex` maps released hook payloads into common events, preserving
   session/turn/call identity. The model counts overlapping tool calls and tracks
   pending questions independently of unrelated completions.
5. A `file.snapshot` request waits for the model to read the pre-image. Only then
   does the synchronous hook return. Both adapters use this path. Snapshot
   failures become visible integration errors rather than invented baselines.
6. Codex MCP tools use the existing Show validator and workstream setup command.
   Setup waits for the host's result, including its existing batch confirmation.
   `read_file` provides bounded, line-numbered text reads within the worktree.

LazyAI adds only generic environment settings to shells; it does not inject the
agent's integration configuration or workstream bearer token. MCP children receive explicit scoped
environment entries because Codex may filter inherited environment variables.
Lifecycle hook commands are stable across launches so per-session tokens do not
invalidate Codex's native hook-trust hashes.

## Codex requirements and boundaries

- Minimum version: 0.155.1. The executable is checked before session startup.
- Native hook review is required. No hook-trust or sandbox bypass flags are added.
- `SessionStart` is deferred until the first turn in the tested Codex version.
  MCP readiness alone is not presented as full integration readiness.
- `request_user_input` and permission requests yield attention events. Actual
  answers remain in the native CLI; LazyAI preserves contract drafts and focuses
  the agent when a question arrives.
- Read tracking uses `read_file`. Arbitrary shell reads are not inferred from
  command strings. Patch tracking uses structured `apply_patch` inputs; shell
  mutations and third-party write tools remain outside the tracked-file contract.
- Hook inputs rewritten by another hook after LazyAI's snapshot may touch
  different paths. Such custom rewriting is not a verified baseline boundary.
- Hosted tools that do not emit Codex local-tool hooks are not counted by the
  spinner. Hook failures are bounded and surfaced; they do not authorize actions.
- Show records store Codex IDs as `codex:<id>`, preserving legacy OpenCode IDs.
  Schema v3 adds a separate `codex_session_id` to worktrees without changing their
  existing OpenCode `session_id`. Reopening selects the configured backend's
  saved conversation; Codex uses `codex resume <id>`. Explicit native `resume`
  or `fork` passthrough commands override automatic Codex selection.
  Live diff baselines/drafts have the same
  detach-versus-stop lifetime as before.
- Use native local Codex; remote app-server and Codex-managed worktree launch
  options can change execution/environment roots and are not supported by this
  worktree-scoped adapter.

## Verification

```sh
go vet ./...
go test -race ./...
go build -o bin/lazyai ./cmd/lazyai
python3 scripts/test-codex-bridge.py
python3 scripts/test-sessions-tmux.py --real-opencode --real-codex
```

The Go tests cover setup answers, cancellation, invalid choices, executable
validation, existing-file preservation, configuration selection, separate backend
conversation persistence, reload boundaries, acknowledgment
ordering, dirty-file baselines, failed/reverted writes, multi-file/move parsing,
MCP handshake/tool errors, scoped reads, concurrent attention, and existing
workstream/strict-contract/supervisor behavior.

The Codex compatibility drive uses an isolated `CODEX_HOME`, exact test-hook trust
hashes, and a local deterministic Responses fixture. It makes no model-provider
requests and leaves the user's authentication and hook trust alone. It exercises
real Codex config merging, native hook execution, patch approvals, dirty-file
snapshot ordering, questions, deferred MCP tool discovery, all three MCP tools,
and turn completion. Its HTTP receiver stands in for LazyAI's model; the Go host
tests separately cover actual validation, snapshot storage and workstream setup.

The tmux drive uses real native terminal processes without submitting model
requests. It checks first-run setup, terminal routing, lifecycle, workstreams, strict forms,
OpenCode plugin startup, and Codex draft preservation when reattaching after a
backend config change. It skips native hook review for this terminal-only check;
the isolated compatibility drive proves trusted hook execution separately.

Initial red observations: project configuration had no agent field, and snapshot
requests received no model acknowledgment. Both are now covered by focused tests.
Linux terminal behavior and additional Codex versions require their own native
compatibility runs; local verification is on macOS.
