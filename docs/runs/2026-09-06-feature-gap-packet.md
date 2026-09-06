# Feature gap packet

## Outcome and authority

Close the gaps identified by the 2026-09-06 implementation-state review without regressing current workstream, input-routing, or persistent-session behavior. This packet is an implementation design and verification contract only; it authorizes local branch work, tests, and documentation when an implementation run is started, but not merging, tagging, publishing, or deployment.

The source of truth is the current `main` implementation at `a03e813`, the executable verification completed on 2026-09-06, and the observable behaviors below. The current baseline is green under focused package tests, `go vet ./...`, `go test -race ./...`, and the real-OpenCode tmux drive. Detach/attach is implemented and locally verified; the remaining session work is release publication, not another lifecycle rewrite.

## Scope and priority

| ID | Workstream | Current state | Target |
|---|---|---|---|
| G1 | Mouse and scrolling verification | Unit coverage; click-only tmux coverage | Real wheel and native hit-target coverage |
| G2 | Workstream identity form | Branch-only prompt | Branch, nickname, and optional description |
| G3 | Strict interactive contracts | Absent | Per-project YAML templates and enforced structured input |
| G4 | Agent workstream setup | Absent | Agent-callable setup using the same application command as `w` |
| G5 | Activity indicators | Static boolean idle/busy/attention | Accurate, animated, concurrency-safe activity state |
| G6 | Ergonomic review | Existing controls characterized; new flows absent | Keyboard, mouse, focus, narrow-screen, and recovery acceptance |
| G7 | Release delivery | Merged and versioned; no remote tag or GitHub release | Reproducible tagged release with artifacts and checksums |

Priority order is G1, G2, G4, G3, G5, G6, then G7. G1 protects the existing input boundary before UI expansion. G2 and G4 establish the shared workstream command surface that G3 can use for structured task entry. G6 is a gate applied to every UI-bearing slice rather than a cleanup pass deferred to the end.

## Product decisions

### Project configuration

Use an optional, project-root-relative `.lazyai/config.yaml`. Absence of the file preserves all current behavior: normal interactive entry, the branch-only data model as a migrated display fallback, current keys, and current session startup. The file is user-owned and may be committed; LazyAI reads it but never rewrites it implicitly.

Configuration version 1:

```yaml
version: 1
interactive:
  strict: false
  default_contract: task
  contracts:
    task:
      title: Task contract
      fields:
        - key: outcome
          label: Outcome
          type: multiline
          required: true
        - key: acceptance
          label: Acceptance criteria
          type: multiline
          required: true
        - key: constraints
          label: Constraints
          type: multiline
          required: false
```

Unknown top-level keys are ignored with a visible warning so newer shared configuration does not prevent older LazyAI versions from starting. An unsupported `version`, malformed YAML, duplicate field key, missing required template attribute, or unsupported field type disables strict entry and presents a persistent configuration error; it must never silently enforce a different contract. Configuration is loaded when a project session starts and on an explicit reload action, not continuously while a form is open.

### Workstream identity

Creating a new workstream collects three independent values:

- `branch`: required, Git-valid, and the source of worktree identity.
- `nickname`: required for new workstreams, human-readable, trimmed, and limited to 60 display cells.
- `description`: optional, intended as a short reminder for later reference, trimmed, and limited to 240 characters.

Existing branches and persisted worktrees are migrated lazily with `nickname = branch` and an empty description. Waking, archiving, detaching, and reattaching preserve metadata. Reopening an already running branch switches to it and does not overwrite its metadata; editing identity is a separate explicit action. The compact workstream strip shows nickname first and exposes branch plus description in the selected-row detail area so narrow layouts remain usable.

### Strict interactive mode

When strict mode is disabled, `i` and `Enter` retain current behavior. When strict mode is enabled, `i` or `Enter` opens a centered contract form over the visible OpenCode pane; input is not forwarded to OpenCode until validation succeeds and the user submits. Submission renders a deterministic YAML document headed by the selected template name and sends it as one bracketed paste followed by Enter.

`Ctrl+Space`, then `f`, toggles freestyle entry for the current workstream only. Freestyle state is visible in the mode indicator, resets when the workstream process is restarted, and does not modify project configuration. `Esc` closes the form without sending partial input; submitted values remain in local form history for the current workstream so an accidental validation failure does not destroy them. Strict mode constrains new user instructions, but does not intercept OpenCode-owned permission dialogs, command palettes, or other child interactions after submission.

### Agent workstream setup

Introduce one internal application command, `OpenWorkstream(spec)`, which validates identity, resolves or creates the worktree, persists metadata, launches OpenCode, and returns a structured result. The keyboard `w` flow and agent path must both call this command; neither may duplicate Git or persistence logic.

Expose the agent path as a bundled OpenCode tool named `setup_workstreams`, documented by the bundled LazyAI skill as the action to use when the user asks the agent to set up workstreams. Its input is a bounded list of branch, nickname, optional description, and optional base values. The plugin sends authenticated requests through the existing per-workstream loopback hook channel, and the app converts accepted requests into `OpenWorkstream` commands without terminal keystroke injection. A batch is validated completely before mutation; duplicate branches, invalid bases, or invalid metadata reject the entire batch, while launch failures after successful creation return per-item results and leave reusable worktrees registered rather than deleting user data.

### Activity model

Replace the `busy` boolean with active tool-call identities and derive visible state by precedence:

1. `attention`: permission or explicit user action is required.
2. `working`: one or more tool calls are active.
3. `unseen`: background output or Show locations have not been viewed.
4. `idle`: none of the above.

The plugin includes a stable call identifier in tool start and finish events. Duplicate starts and finishes are idempotent; a stream-level idle event clears orphaned active calls after dropped events. The selected stream may remain working, but selecting it clears only unseen output, never attention or active work. Animate working state only while at least one stream is active and stop ticks when no animation is visible.

## User-visible flows

### New or wake with `w`

1. `w` opens the workstream form with branch, nickname, and description.
2. Selecting a running workstream switches immediately without entering edit mode.
3. Selecting a dormant or existing branch loads persisted metadata and offers Open or Edit Identity.
4. Entering a new branch validates all fields, then asks for main/current/explicit base.
5. Submit calls `OpenWorkstream`; success focuses the new OpenCode pane and failure keeps the populated form open with an inline error.

### Strict instruction entry

1. `i` opens the configured contract form when strict mode is active.
2. Tab and Shift+Tab move fields; arrows and wheel scroll the active multiline field; mouse selects fields and buttons.
3. Submit highlights every invalid field without discarding values.
4. A successful submit sends one deterministic YAML instruction and returns control to OpenCode.
5. Leader `f` visibly enters or exits per-workstream freestyle mode.

### Agent setup

1. The user asks the current agent to create named workstreams.
2. The agent invokes `setup_workstreams` with the requested specifications.
3. LazyAI validates the complete batch and displays a confirmation overlay before creating more than one new branch.
4. Accepted workstreams appear in the same order as the request without stealing focus from the current agent.
5. The tool returns branch, nickname, root, created/reused status, and launch status for each item.

## Compatibility and migration

- Add nullable `nickname` and `description` columns to `worktrees` using an explicit schema migration recorded with SQLite `PRAGMA user_version`.
- Read old rows as branch-name nicknames until they are migrated or edited.
- Do not rename branches, move existing worktree directories, change project identity, or alter runtime-session rows.
- Preserve missing-config behavior and all existing OpenCode configuration, provider, plugin, session, and skill loading.
- Keep the hook endpoint loopback-only and token-authenticated; setup requests may target only the canonical repository that owns the requesting token.
- Existing supervisors continue with their startup configuration until restarted; the CLI reports this when configuration differs on disk.
- Rollback may leave additive database columns and `.lazyai/config.yaml`; old binaries must continue operating because both are ignored safely.

## Blast radius

| Surface | Expected change | Must preserve |
|---|---|---|
| `internal/app` | Forms, shared command, strict mode, activity state | Mode switching, focus, archive/close, diff/show behavior |
| `internal/git` | Base validation reused by batch setup | Existing create/reuse and `.git/info/exclude` behavior |
| `internal/notes` | Versioned metadata migration | Existing show sets, repo state, and runtime sessions |
| `internal/hooks` | Authenticated setup request/event | Token isolation and Show validation |
| Bundled plugin/skill | `setup_workstreams` schema and guidance | Existing `show_locations` behavior |
| Input/router/terminal | Structured paste and expanded mouse tests | Literal paste, leader keys, local mouse coordinates, detach byte |
| Supervisor | No lifecycle redesign; only restart/config reporting if needed | Detach, takeover, snapshot, resize, stop, process cleanup |
| CLI/release | Config diagnostics and release automation | Existing launch, list, stop, and version output |

A focused blast-radius review is required before G3 and G4 implementation because they cross the child/plugin, hook, app-model, persistence, and terminal-input boundaries.

## Delivery slices

### Slice G1: close mouse verification gaps

- Add app tests for wheel behavior in Diff and Show content, prompt list scrolling, right-click back/cancel, dialog confirmation, release/motion events, and modifier preservation.
- Extend the isolated tmux drive with one native sidebar wheel assertion and one child-pane wheel assertion using screen or helper-observed output.
- Preserve current three-row sidebar wheel step unless hands-on evidence demonstrates a problem.

### Slice G2: workstream identity

- Establish red tests for migration, metadata persistence, create/wake/reopen semantics, narrow rendering, and failed-launch recovery.
- Add versioned migration and metadata repository methods.
- Replace the branch prompt with the multi-field form while keeping existing-branch fuzzy selection.
- Verify archive/wake and detach/reattach against real persisted state.

### Slice G4: shared setup command and agent tool

- Extract `OpenWorkstream(spec)` without changing `w` behavior.
- Add authenticated hook request handling and batched validation.
- Add the bundled plugin tool and skill instructions.
- Verify a real OpenCode session can invoke the tool, create multiple workstreams, receive results, and leave current focus unchanged.

### Slice G3: strict contracts

- Add parser and validation tests for version 1 configuration.
- Add form-state tests independently of rendering, then keyboard, mouse, scroll, and narrow-screen rendering tests.
- Send the rendered contract through the existing literal bracketed-paste path.
- Verify strict/freestyle transitions across workstream switches, detach/reattach, permission dialogs, and process restart.

### Slice G5: activity accuracy

- Characterize current idle, busy, attention, and unseen behavior.
- Add overlapping, duplicate, missing, and out-of-order event reproductions before replacing the boolean.
- Add animation lifecycle and rendering assertions for wide and constrained terminals.
- Extend the real plugin path to verify concurrent tool activity and idle reconciliation.

### Slice G6: ergonomic gate

- Run every new flow using keyboard only, mouse only where supported, and a 60x18 terminal.
- Confirm focus is always visible, cancellation is reversible, errors are adjacent to their field, and no action requires remembering an undisclosed key.
- Confirm overlays preserve enough of the OpenCode pane to retain context and that long content scrolls without moving the underlying pane.
- Record observed friction as failed acceptance criteria, not subjective cleanup notes.

### Slice G7: release delivery

- Add a tag-triggered workflow that runs vet, race tests, and the deterministic tmux drive before building supported artifacts.
- Publish versioned archives and SHA-256 checksums in a GitHub release.
- Keep the real-OpenCode smoke as a documented local pre-release gate if credentials or model installation make it unsuitable for CI.
- Require explicit release authority immediately before creating a tag or GitHub release.

## Acceptance ledger

Implemented on `feat/gap-packet` (2026-09-06). Evidence: `go test -race -count=1 ./...` (14 packages ok), `go vet ./...`, `gofmt -l .` clean, and `scripts/test-sessions-tmux.py --real-opencode` (23/23 checks, `ALL TMUX CHECKS PASSED`).

- [x] G1: Real tmux evidence proves native sidebar wheel scrolling and pane-local child wheel forwarding; focused and race suites pass. — drive: `mouse wheel over the agent pane is forwarded to the child`, `mouse wheel over the sidebar scrolls a native LazyAI list`; app: `TestMouseWheelScrollsDiffContentAndPromptList`, `TestMouseYesClickQuitsAndReleaseMotionModifiersReachChild`.
- [x] G2: New workstreams require branch and nickname, accept an optional description, and preserve metadata across wake and session reattachment. — `TestWorktreePromptCreatesWorktreeAndWorkstream`, `TestArchiveMakesWorktreeDormantAndPromptWakesIt` (archive → wake keeps nickname; `e` renames; empty nickname rejected), drive: `workstream form records nickname and description shown in the strip`. Reattachment reads identity from the same persisted row (`storedIdentity`).
- [x] G2: Existing databases and projects with no config open without behavior loss or manual migration. — `notes.TestMigrationAddsIdentityColumnsToVersion0Database` (v0 fixture → `user_version` 1, old row unchanged, reopen idempotent), `TestInvalidOrMissingConfigNeverEnforcesStrictMode` (no file → defaults).
- [x] G3: Strict mode cannot send freestyle instructions through `i` or `Enter`; valid forms produce deterministic YAML; leader `f` provides a visible, workstream-local bypass. — `TestStrictModeGatesEveryEntryThroughTheContractForm` (i, Enter, pane click all open the form; YAML observed in the child; `NORMAL·FREE` / `INTERACTIVE·FREE`; second stream stays strict), `config.TestRenderIsDeterministicAndSkipsEmptyOptionalFields`, drive: `strict contract form sends one deterministic YAML paste and returns to OpenCode` (exact bracketed-paste bytes + `\r` observed by the child through client → supervisor → router).
- [x] G3: Missing config preserves current behavior, while malformed or unsupported config fails visibly and safely. — `config.TestInvalidConfigsFailVisiblyAndNeverEnforce` (9 malformed classes), `TestInvalidOrMissingConfigNeverEnforcesStrictMode` (persistent `config error` in status; leader `c` reload; unknown keys warn).
- [x] G4: `w` and `setup_workstreams` produce equivalent worktree, persistence, and launch results through one shared command. — `OpenWorkstream` is the only caller of `git.EnsureWorktree` from the app; `TestAgentSetupOpensWorkstreamsThroughTheSharedCommandWithoutStealingFocus` reopens an agent-created branch via `w` and finds identity intact.
- [x] G4: Agent batch setup is repository-scoped, authenticated, bounded, fully prevalidated, ordered, and does not steal focus. — same test (7 rejection classes leave state untouched, unknown token refused, order preserved, focus unchanged), `TestAgentSetupWithSeveralNewBranchesAsksFirst` (confirm float, decline fails the tool call, click `y create`, expired confirmation refused), `hooks.TestSetupIsARequestThatWaitsForTheModelReply` (200/422/504, late reply never blocks). Real OpenCode loads the plugin exposing both tools (drive: `real OpenCode loads the bundled plugin with both tools`).
- [x] G5: Overlapping and out-of-order tool events cannot show idle while work is active; attention and unseen states remain distinct. — `TestOverlappingAndOutOfOrderToolEventsKeepWorkingAccurate` (call ids, duplicate/unknown after-events, idle reconciliation, spinner tick lifecycle, precedence, visit clears unseen only, background writes flag unseen), `TestHookEventsRouteToTheirStreamAndFlagAttention`.
- [x] G6: New overlays and forms pass keyboard, mouse, scroll, cancellation, error-recovery, and 60x18 terminal checks. — `TestFormsFitA60x18TerminalWithoutOverflow` (contract + identity + base stages, no row > 60 cells, all fields visible, centred, keyboard-only completion), mouse field focus in `TestMouseSelectsWorktreeBaseChoice`, right-click staging in `TestMouseWheelScrollsDiffContentAndPromptList`, inline `required` flag and draft retention in the strict test.
- [~] G7: A version tag can produce downloadable artifacts and checksums only after required gates pass. — `.github/workflows/release.yml` (tag/version match, vet, race, 4 archives, `SHA256SUMS`) and `ci.yml` are added and reviewed; version bumped to 0.2.0 (`TestVersionWithoutTerminal`). Not executed: no tag was pushed (release authority not granted in this run). The tmux drive stays a documented local gate.
- [x] Preserve: detach/reattach, takeover, literal paste, resize, workstream archive/close, process cleanup, Show isolation, and existing OpenCode configuration remain green. — all 16 pre-existing drive checks pass unchanged; full race suite green.

Deferred (not in this revision): the CLI does not yet report when `.lazyai/config.yaml` on disk differs from a running supervisor's loaded copy; `Ctrl+Space c` reloads inside the session and `docs/persistent-sessions.md` documents the restart path.

## Verification matrix

| Boundary | Required evidence |
|---|---|
| Pure config and form state | Table-driven unit tests including malformed and boundary-size input |
| SQLite migration | Fresh database, version-0 database fixture, repeated migration, and rollback-compatible read |
| Workstream command | Create, reuse, already running, dormant wake, invalid base, launch failure, and batch prevalidation tests |
| Plugin/hook | Token rejection, cross-repository rejection, schema rejection, duplicate request, and successful result tests |
| Input delivery | Exact bytes for deterministic YAML, bracketed paste framing, embedded control bytes, and no host-command leakage |
| Activity | Concurrent IDs, duplicate/out-of-order events, idle reconciliation, attention precedence, and animation stop |
| TUI | Keyboard, mouse, wheel, narrow terminal, resize while open, cancel, and inline errors |
| Sessions | Detach/reattach with open metadata and strict forms; takeover; restart-required config behavior |
| Repository gate | `go vet ./...`, `go test -race ./...`, deterministic tmux drive, and real OpenCode smoke |
| Release | Clean tag, matching embedded version, artifact checksums, and downloadable GitHub release |

## Non-goals

- No general task/project manager, cloud synchronization, or cross-repository workstream creation.
- No arbitrary executable hooks in project YAML.
- No attempt to reconstruct PTYs after reboot or supervisor crash.
- No branch rename or automatic deletion of worktrees after partial setup failure.
- No interception or restructuring of OpenCode responses; strict mode governs user instruction entry only.
- No public release, merge, or deployment as part of an implementation run without separate authority.

## Stop conditions

Stop and return to design if OpenCode cannot expose a bundled tool with authenticated result delivery, if strict input cannot coexist with OpenCode permission interactions without terminal emulation hacks, or if migration requires changing existing worktree identity. Stop before any remote branch publication, merge, tag, GitHub release, package-manager publication, or deployment unless the user explicitly authorizes that phase.

## Appendix: short explanation

LazyAI already handles persistent sessions, mouse clicks, and basic activity state, but several workflows stop at a functional baseline. This packet adds memorable workstream identities, lets an agent create the same workstreams as the `w` flow, and optionally replaces free-form task entry with project-defined structured contracts. It also closes real mouse-wheel test gaps and makes activity indicators accurate when tools overlap or need attention. Existing projects keep their current behavior unless they add configuration, and stored worktrees migrate without renaming branches or moving directories. Release automation is the final gate so a merged version becomes a reproducible, downloadable release rather than only a local binary.
