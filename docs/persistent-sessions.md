# Persistent project sessions

## Outcome

Detaching the foreground LazyAI client leaves the current project running without
stopping OpenCode, shells, hooks, or workstreams. Starting LazyAI for that
project later reattaches to the same live terminal and processes.

## Product contract

- Each canonical project root has exactly one isolated supervisor session.
- Git worktrees belong to their main checkout's project. Separate clones remain
  separate projects. Non-Git directories use their resolved root.
- `Ctrl+Q` detaches the client. It does not terminate workstreams.
- `lazyai stop --dir <project>` explicitly terminates that project's session.
- `lazyai list` shows all known project sessions and their live/stale state.
- A second client takes over the display connection; the older client detaches.
- Sessions remain alive without a client until stopped or until their supervisor
  exits. Machine reboot and supervisor failure are reported as stale sessions;
  PTYs are not reconstructed or silently replaced.
- Existing in-app workstream close and archive behavior remains unchanged.

## Architecture

The foreground command becomes a thin terminal client. A detached, per-project
supervisor starts the existing LazyAI TUI as an internal child inside an outer
PTY. The existing TUI continues to own OpenCode PTYs, the hook server, activity
state, and all rendering. The supervisor keeps that outer PTY open and exposes
it through a project-scoped Unix socket.

```text
terminal client
  <-> framed Unix-socket protocol
project supervisor
  <-> outer PTY + VT screen
existing LazyAI TUI
  <-> OpenCode and shell PTYs
```

The supervisor sends complete terminal snapshots rather than replaying an
unbounded output log. The foreground client enables mouse cell-motion, SGR mouse encoding, and
bracketed paste on each attachment, and restores the terminal on detach,
takeover, signal, or error. Input, mouse sequences, paste bytes, and resize
events go to the outer PTY. Both input layers preserve bracketed-paste framing
across reads and bypass shortcuts inside pasted text. Disconnecting only drops
the socket client.

Launch rejects noninteractive terminals before creating any runtime or worktree
artifacts. Project discovery happens before applying launch-only options, so
reattachment cannot create an unused worktree.

Explicit stop and workstream close/archive use the terminal owner's process-tree
cleanup. It freezes ancestors while discovering descendants and terminates the
tree before releasing the PTY. Stop does not wait for graceful parent exit,
which could orphan workers before they can be found. Discovery errors are
returned instead of silently treating an incomplete census as an empty tree.

## Identity and storage

Project identity is the symlink-resolved main Git checkout path, or the
symlink-resolved requested directory outside Git. A SHA-256 digest of that path
names the Unix socket; the path itself remains the authoritative identity.

SQLite stores one runtime row per project:

- project root and requested root
- socket path and supervisor PID
- original command arguments
- status, started time, last attachment, and exit time

The live Unix socket is authoritative for liveness. PID reuse is never accepted
as proof. Stale rows remain visible until a new session replaces them or the
user stops them.

## Protocol

Messages are newline-delimited JSON with a type and optional input bytes,
dimensions, screen snapshot, or error. Required operations are `attach`,
`input`, `resize`, `screen`, `stop`, `exit`, and `error`. Only the local user
can access the socket directory and socket.

## Failure behavior

- Client crash or terminal closure: supervisor and children continue.
- Supervisor startup failure: client reports the failure and the path to the
  supervisor log containing startup diagnostics.
- Existing but unreachable socket: mark the registry row stale, remove the
  stale socket, and start a new supervisor for an explicit attach invocation.
- Supervisor crash: child PTYs are lost and the registry row becomes stale.
- Child LazyAI exit: supervisor records exit status, notifies the client, and
  removes its socket.
- Concurrent attach: newest client becomes active and the previous connection
  closes without affecting the PTY.

## Security

- Runtime directory and registry artifacts are user-only (`0700`/`0600`).
- The socket accepts local filesystem clients only.
- Hook bearer tokens remain inside the supervised child environment.
- No command arguments or environment secrets are emitted by `lazyai list`.

## Delivery slices

1. Add project identity, protocol framing, and runtime registry tests.
2. Add the detached supervisor and a single attachable outer PTY.
3. Add the foreground raw-terminal client, resize forwarding, and detach.
4. Add list, stop, stale-session reconciliation, and takeover.
5. Update status hints and user documentation.
6. Verify unit, race, process, and tmux detach/reattach behavior.

## Acceptance matrix

| Scenario | Required evidence |
|---|---|
| Project isolation | Different roots produce different sockets and registry rows |
| Worktree grouping | Main checkout and linked worktree resolve to one project |
| Detach | Client exits while supervisor and child PIDs remain live |
| Reattach | New client sees the existing screen and same supervisor PID |
| Detached progress | Child output produced without a client appears after attach |
| Input and resize | Attached bytes and terminal dimensions reach the child |
| Explicit stop | `lazyai stop` ends the supervisor and removes the socket |
| Stale recovery | Unreachable registered session is reported stale and replaceable |
| Takeover | New attachment disconnects the old client without killing children |
| Regression | Existing app, input, terminal, hook, notes, diff, and show tests pass |

## Rollout and rollback

The supervisor is the default entry path. `lazyai __direct` is an internal
escape hatch that runs the previous foreground architecture and supports fast
rollback during development. Removing the runtime session row and socket does
not affect existing worktree or Show history.

## Reproducible terminal drive

Build with `make build`, then run:

```sh
python3 scripts/test-sessions-tmux.py --binary ./bin/lazyai --real-opencode
```
 The drive uses a dedicated tmux server
and disposable Git projects. It checks terminal modes, literal multiline paste,
mouse input, resize, same-process detach/reattach, takeover, signal cleanup,
worktree options and nested process cleanup. With `--real-opencode`, it also
checks that real OpenCode displays a prompt and retains an unsent typed draft
across reattachment; it does not submit a model request.
