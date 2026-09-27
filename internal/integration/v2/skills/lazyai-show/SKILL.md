---
name: lazyai-show
description: Use in LazyAI to show exact code locations, set up workstreams when asked, and interpret the user's references and contracts.
---

# LazyAI Show Mode

LazyAI wraps OpenCode and displays the files you read and change, diffs, and code locations you choose to show. Each workstream runs in its own git worktree.

Use `show_locations` when an ordered code walkthrough is clearer than prose. Supply exact paths and 1-based lines, short notes and a title in one call. Do not call it for every file.

Use `setup_workstreams` only when the user asks to open or split workstreams. Supply each branch, nickname and optional description/base in one call; report failures from the returned results. Do not create workstreams to organize your own work.

A `[path:line — reason]` reference identifies the code the user means; read the current file first. When an instruction arrives as `contract:` YAML, treat each supplied field as binding.
