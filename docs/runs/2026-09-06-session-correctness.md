# Session correctness delivery contract

## Outcome and authority

Deliver the five findings from the detach/attach review, test the actual CLI in tmux, and open a PR targeting main. Local implementation, isolated test projects/processes, commits and PR publication are authorized. Stop before merging or deployment. Preserve the existing uncommitted session documentation/help changes on feat/persistent-sessions (starting HEAD a5305da).

The source of truth is the current implementation, docs/persistent-sessions.md, the review reproductions, and observable process/terminal behavior. One implementation lane; progress updates within 60 seconds of active work. Ask only for missing publication destination or a material change in product intent. No remote is configured at the start; destination requested while implementation proceeds.

## Design and blast radius

- Project identity: resolve Git's relative common directory against the queried directory. This affects attach, stop, worktree discovery/creation and note ownership. Main checkout, nested directories, symlinks and linked worktrees must group together; separate repositories must remain separate.
- Foreground input modes: the LazyAI client owns mouse cell-motion/SGR and bracketed-paste modes, enabling them on attach and disabling them on every normal return, takeover and signal exit. Screen snapshots remain bounded cell snapshots. Do not rely on replaying transient child mode sequences. Preserve paste framing through the input router, including split reads and control characters inside a paste.
- Shutdown: explicit stop must terminate the owned process tree before reporting stopped. Reuse process-tree cleanup in Terminal.Close for workstream close/archive and direct TUI cleanup. Freeze ancestors while discovering descendants, terminate descendants before their parent, and surface discovery errors instead of claiming success. Do not wait for a parent to exit before discovering its descendants. Explicit stop may terminate work immediately, consistent with existing terminal close behavior; no graceful-drain guarantee is introduced.
- CLI ordering: reject noninteractive launch before worktree/database/supervisor side effects. Discover an existing project session before applying launch-only worktree options. Reattachment retains the existing screen/options, with no unused worktree creation.
- Preserve: socket startup lock, one active client/takeover, same live PIDs across detach, detached progress, resize, stale recovery, independent project sessions, current help/docs changes.
- Environment: macOS local PTYs, Unix sockets, tmux and real OpenCode; deterministic helper processes cover output/paste/process-tree assertions without model variability. No database migration or new external service.
- Rollback: revert the fix commit; no persistent schema changes. Restart existing supervisors to run the new code.

## Acceptance ledger

- [x] Nested directories/symlinks/worktrees share the correct identity; sibling repositories do not.
- [x] Client enables mouse and bracketed paste and cleans up on detach, takeover, error and signal.
- [x] Paste contents and control bytes survive without accidental host commands/detach.
- [x] Explicit stop and workstream close/archive terminate nested children; unrelated processes survive.
- [x] Reattach with --worktree creates no branch/worktree; fresh startup still applies it.
- [x] Noninteractive launch produces no runtime/worktree/database artifacts.
- [x] Existing suite and race checks pass.
- [x] tmux drive: start, type, paste, mouse, resize, detach, detached progress, reattach, takeover, stop, clean terminal modes; real OpenCode startup and reattach.
- [x] Verified implementation committed as 21368f6 and published to PR #1 targeting main: https://github.com/pankajgarkoti/lazyai/pull/1.

## Evidence and status

Phase: complete. Implementation, regression/race verification, tmux drive and PR publication delivered. Stop before merge.
Original review: isolated tests failed for nested identity, lost terminal input modes and a descendant surviving graceful parent exit. Existing race suite passed with sandbox-external socket/PTY access.
Publication: Git FETCH_HEAD identifies pankajgarkoti/lazyai. Refreshed remote main is 54f768f, matching the local base. Existing PR #1 targets main from this branch; update it using the repository owner account without changing the active CLI account.


## Verification evidence

- Regression red: nested directory identity, graceful-stop orphan worker, noninteractive worktree/runtime mutations, and pasted control sequences reproduced before implementation.
- Terminal red: replaying the original session.go through a Go source overlay with the corrected PTY test harness reports missing enable/disable modes and unused worktree creation for detach, exit and error paths.
- `go vet ./...` and `git diff --check`: passed.
- `go test -race ./...`: passed with local socket/PTY access. Includes identity via absolute/relative/symlink paths, detached progress, takeover, resize/input, stale replacement, startup lock, forced and formerly graceful shutdown, terminal mode cleanup and literal fragmented paste.
- Dedicated tmux drive: mode flags enabled/restored; multiline paste including raw Ctrl+Q and jk reaches helper unchanged; SGR mouse reaches child; resize; same supervisor/worker PIDs and detached progress; reattach with ignored worktree flag; takeover; client SIGTERM; stop from nested directory; fresh worktree creation; archive kills selected descendants and preserves siblings; final workstream close kills nested descendants.
- Real OpenCode: visible prompt, unsent typed draft, detach and reattach preserving draft and supervisor identity. No model request was submitted. Initial smoke predicate matched a host hint; replaced it with a visible OpenCode prompt plus draft restoration before accepting this evidence.
- Reusable drive: scripts/test-sessions-tmux.py. Successful artifacts: /private/tmp/lazyai-drive-pjs8pxl8. Test-server configuration is isolated from user tmux configuration; literal paste disables tmux's own control-byte sanitization.
- Source review: explicit stop no longer gives workers an orphaning grace window. Cleanup errors return from the terminal owner. No schema migration. Existing supervisors need to be stopped/restarted to run the new revision.

## Verification limits

macOS was exercised; Linux process cleanup was not driven here. Process-tree cleanup owns descendants present at close/stop; it cannot retroactively recover workers deliberately daemonized and reparented before discovery. Reboot and supervisor-crash recovery remain stale-session behavior, as documented. Real model execution was not needed to verify the lifecycle boundary and was not exercised.

Publication confirmed: PR #1 is OPEN, base main, with implementation commit 21368f64e186b0611915b6017ecfe81132fbf8ca. The final documentation commit only records this result.
