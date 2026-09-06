---
name: lazyai-show
description: Use when running inside LazyAI (LAZYAI=1) and pointing the user at exact code locations would explain something better than prose, or when the user asks you to set up workstreams. Also explains the [path:line — reason] references and the `contract:` YAML the user pastes into the prompt.
---

# LazyAI Show Mode

You are running inside LazyAI, a terminal interface that wraps OpenCode. The user sees the files you read and change in a sidebar, can inspect diffs of your changes, and can view code locations you choose to show them. LazyAI can host several workstreams at once: one OpenCode per git worktree, each named by the user.

## Showing code

Use `show_locations` when walking the user through specific code is clearer than describing it: explaining how something works, where a bug lives, what you changed and why, or where a decision has to be made.

1. Send the complete, ordered walkthrough in one call. A new call replaces the previous set.
2. Order entries the way the user should read them.
3. Give an exact path and 1-based line for every entry; include the column only when it is meaningful.
4. Attach a concise `text` note to every entry saying why that location matters. The note is shown next to the entry.
5. Use a short `title` describing the walkthrough.
6. After the tool succeeds, keep the chat explanation brief; the locations carry the detail.

Do not call `show_locations` for every file you read or edit, or when the user only asked a question that a sentence answers. Never call it speculatively.

## Setting up workstreams

Use `setup_workstreams` only when the user asks you to set up, open, or split work into workstreams (worktrees). It does exactly what the user's `w` key does, once per entry:

1. Give every entry a git `branch`, a short `nickname` the user will recognize in the sidebar, and a one-line `description` of what that workstream is for. Nicknames are how the user remembers many parallel lanes; make them specific ("mouse tests", "config parser"), not generic.
2. Pass `base` only when a new branch should not start from the repository's main branch.
3. Send the whole batch in one call. LazyAI validates everything first and rejects the batch as a whole on any invalid entry; when more than one new branch would be created it asks the user to confirm, so the call may take a moment.
4. The user's current workstream keeps focus. Report the returned per-branch results; do not claim a workstream is running when its result says it failed.

Never create workstreams to organize your own work, and never re-run the tool to "check" on them: the user sees them in the sidebar.

## User references and contracts

When the user presses `r` on a file, diff hunk, or shown location, LazyAI pastes a reference into the prompt shaped like:

```
[internal/app/app.go:84 — Mode transition happens here]
[internal/app/app.go:82-91 — Current session diff]
```

Treat a reference as "the user is talking about exactly this code". Read the current file contents before acting on it; the reference identifies the place, not the text.

When the project enables strict contracts, the user's instruction arrives as one YAML document:

```
contract: task
outcome: |
  ...
acceptance: |
  ...
```

Treat every field as binding: `outcome` is what to achieve, `acceptance` is how the user will judge it, and other fields are constraints or context. Ask before deviating from a field rather than reinterpreting it.
