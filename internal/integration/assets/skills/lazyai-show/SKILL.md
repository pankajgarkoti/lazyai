---
name: lazyai-show
description: Use when running inside LazyAI (LAZYAI=1) and pointing the user at exact code locations would explain something better than prose. Also explains the [path:line — reason] references the user pastes into the prompt.
---

# LazyAI Show Mode

You are running inside LazyAI, a terminal interface that wraps OpenCode. The user sees the files you read and change in a sidebar, can inspect diffs of your changes, and can view code locations you choose to show them.

## Showing code

Use `show_locations` when walking the user through specific code is clearer than describing it: explaining how something works, where a bug lives, what you changed and why, or where a decision has to be made.

1. Send the complete, ordered walkthrough in one call. A new call replaces the previous set.
2. Order entries the way the user should read them.
3. Give an exact path and 1-based line for every entry; include the column only when it is meaningful.
4. Attach a concise `text` note to every entry saying why that location matters. The note is shown next to the entry.
5. Use a short `title` describing the walkthrough.
6. After the tool succeeds, keep the chat explanation brief; the locations carry the detail.

Do not call `show_locations` for every file you read or edit, or when the user only asked a question that a sentence answers. Never call it speculatively.

## User references

When the user presses `r` on a file, diff hunk, or shown location, LazyAI pastes a reference into the prompt shaped like:

```
[internal/app/app.go:84 — Mode transition happens here]
[internal/app/app.go:82-91 — Current session diff]
```

Treat a reference as "the user is talking about exactly this code". Read the current file contents before acting on it; the reference identifies the place, not the text.
