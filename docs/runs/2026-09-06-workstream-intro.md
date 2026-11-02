# Workstream identity delivery contract

```yaml
workflow: implement
outcome: >-
  Creating a workstream captures a branch name plus an optional human-readable
  nickname and description, then shows that context where it helps the user
  remember the purpose of parallel work without obscuring Git identity.
environment: local
authority: workspace-write
source_of_truth:
  - the current TUI behavior and tests
  - README.md workstream semantics
  - the user's run backlog
deliverable:
  - a tested workstream creation form
  - persisted nickname and optional description for later reference
  - concise workstream-list presentation that keeps the branch identifiable
acceptance:
  - a new workstream can be created with a valid branch and a nickname
  - description is optional and an empty description is accepted
  - existing branches and dormant worktrees remain openable from the prompt
  - nickname and description survive detach, reattach, archive, and wake
  - keyboard-only creation remains complete and mouse interaction is covered
  - scrolling and focus behavior work at constrained terminal sizes
  - existing project configuration and persisted worktrees remain valid
  - focused tests, the repository test suite, race checks, and vet pass
must_preserve:
  - branch names remain the canonical Git and worktree identity
  - main checkout pinning and workstream ordering
  - fuzzy discovery of open, dormant, and local branches
  - current detach/attach process ownership and terminal input cleanup
  - existing databases upgrade without destructive migration
out_of_scope:
  - structured strict-interactive contracts and their configuration UI
  - freestyle-mode enable or disable shortcuts
  - redesigning the supervisor or detach/attach lifecycle
  - skill-driven workstream setup
  - general activity-indicator redesign beyond showing workstream identity
  - release, merge, or deployment
autonomous_decisions:
  - field layout, focus order, validation copy, and reversible key bindings that
    follow existing TUI conventions
  - minimal additive persistence migration and fallback display for old records
  - exact focused tests and local verification commands
ask_when:
  - a required product choice changes branch semantics or existing shortcuts
  - compatibility requires destructive data changes
  - external publication, merge, or release authority is needed
stop_before:
  - commit, pull request, merge, or release
checkpoint:
  interval: at red reproduction, green implementation, and final verification
  stop_if:
    - intended behavior conflicts with existing persisted-data guarantees
    - the same environmental blocker survives two materially different attempts
```

## Acceptance ledger

- [ ] Form captures branch, nickname, and optional description - evidence pending.
- [ ] Existing and dormant workstreams remain directly openable - evidence pending.
- [ ] Metadata persists across lifecycle transitions - evidence pending.
- [ ] Keyboard, mouse, scrolling, focus, and small-terminal behavior are covered - evidence pending.
- [ ] Existing configuration and database state remain compatible - evidence pending.
- [ ] Focused and repository-wide checks pass - evidence pending.

## Program boundaries

- Mouse support and scrolling already exist in `9bb1af6`; this run verifies and repairs them only where the new form exercises them.
- Persistent detach/attach semantics shipped in `0.1.0`; this run treats them as a regression boundary rather than rebuilding the supervisor.
- Strict structured contracts require a separate design contract because template schema, enforcement boundaries, bypass behavior, and configuration ownership change observable product behavior.
- Better activity indicators and skill-driven setup are independent implementation contracts after their intended behavior is specified.

## Status

Current phase: contract established; characterization and design discovery next.

Last verified artifact: clean `feat/intro-banner` at `a03e813`.

Next action: characterize workstream prompt, persistence schema, rendering, mouse behavior, and lifecycle tests before adding the failing form reproduction.
