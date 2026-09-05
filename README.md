# lazyai

A lazygit-style, Vim-modal terminal UI around **OpenCode**.

OpenCode runs unchanged, as a real terminal process inside the right pane. LazyAI
adds a sidebar of the files the agent reads and changes, a diff viewer for the
agent's edits, and a "Show" mode where the agent can point you at exact code
locations with a note attached.

```
┌──────────────────┬──────────────────────────────────────────┐
│ Files            │                                          │
│ M internal/x.go  │ OpenCode TUI (interactive) | diff | show │
│ R cmd/main.go    │                                          │
├──────────────────┴──────────────────────────────────────────┤
│ ▼・ᴥ・▼  NORMAL   w:worktree  a:archive  ?:help      ⸗ ● plugin│
└─────────────────────────────────────────────────────────────┘
```

## Run

```sh
go build -o bin/lazyai ./cmd/lazyai
./bin/lazyai --dir /path/to/project [-- opencode args...]
```

Requires `opencode` on `PATH` (tested with 1.18.x). Your existing OpenCode
configuration, providers, sessions, skills and plugins all apply; LazyAI only
adds one extra config directory (`OPENCODE_CONFIG_DIR`) containing its plugin
and skill, materialized under your user cache dir.

## Modes and keys

**Interactive** (default) – the real OpenCode TUI owns the keyboard.
`Esc` focuses out into **Normal**: the same OpenCode window, fully visible,
just with no input routed to it (the border and `NORMAL` indicator show it) until
`i` (or `Enter`). `t` opens a **Terminal** (your `$SHELL` in the
worktree) that follows the same rules (`Esc` out, `jk` sends it a real Esc).
`d` is only available once the agent changed something; `s` once it pointed
at code.

| Key       | Action                                              |
|-----------|-----------------------------------------------------|
| `Esc`     | Normal: focus out of the pane (visible, no input)      |
| `i` / `t` | Into OpenCode / into the terminal                   |
| `jk`      | Send a real Escape into the pane (palette, interrupt)|
| `Ctrl+Q`  | Ask to quit LazyAI (and OpenCode); confirm with `y` |

**Diff** / **Show** – LazyAI owns the keyboard. Each has a sidebar and a
content pane; `Enter` focuses the content, `Esc` returns to the sidebar.

| Key                | Sidebar focus                | Content focus            |
|--------------------|------------------------------|--------------------------|
| `j` / `k`          | Select file / location       | Scroll one line          |
| `Enter`            | Focus content                | –                        |
| `Esc`              | Back to Interactive          | Back to sidebar          |
| `h` / `l`          | Previous / next workstream   | `h`: back to sidebar     |
| `Tab`              | Toggle focus                 | Toggle focus             |
| `Ctrl+D`/`Ctrl+U`  | –                            | Half page                |
| `g` / `G`          | First / last entry           | Top / bottom             |
| `[` / `]`          | –                            | Previous / next hunk     |
| `r`                | Reference selection in prompt| Reference current hunk   |
| `i`                | Back to Interactive          | Back to Interactive      |
| `d` / `s`          | Diff mode / Show mode        | Diff mode / Show mode    |
| `w`                | New / wake workstream        | New / wake workstream    |
| `a`                | Archive workstream (dormant) | Archive workstream       |
| `t`                | Terminal in the worktree     | Terminal in the worktree |
| `x x`              | Close workstream             | Close workstream         |
| `z` / `?`          | Zoom / help                  | Zoom / help              |
| `1`–`9`            | Jump to entry                | –                        |
| `[` / `]` (Show)   | Previous / next location     | Previous / next location |

Mouse clicks focus panes and select workstreams, files, locations, and prompt
choices. The wheel scrolls the pane under the pointer; right-click cancels the
worktree prompt. Mouse input inside OpenCode or a terminal is forwarded with
coordinates adjusted to that pane.

`r` pastes a reference such as `[internal/app/keys.go:25 — r key handled]`
into OpenCode's prompt and returns to Interactive so you can keep typing.

## Workstreams (worktrees)

A workstream is one OpenCode child running in its own git worktree, with its
own file ledger, Diff/Show state and remembered mode. LazyAI hosts any number
of them in the same interface; exactly one is current.

```sh
lazyai --dir ~/code/app                                   # workstream 1: current checkout
lazyai --dir ~/code/app --worktree feat/search            # start on a worktree (branch from HEAD)
lazyai --dir ~/code/app --worktree hotfix/login --base main
```

Worktrees live under `<repo>/.worktrees/<branch>` (slashes become dashes) and
that directory is added to `.git/info/exclude`, so nothing tracked changes.
An existing branch gets a worktree; an existing worktree is reused.

**Navigating** — the sidebar tops every mode with a ` Workstreams` strip
(`n  branch  ●|⟳|!` = idle · tool running · needs you / has something to show)
and the status bar shows `n/N`.

| Where                     | Keys                                                                              |
|---------------------------|-----------------------------------------------------------------------------------|
| Normal / Diff / Show sidebar | `h` / `l` previous / next · `w` new or wake · `a` archive (dormant) · `x x` close |
| inside a pane             | `Ctrl+Space` then `h` / `l` · `1`–`9` go to n · `Ctrl+Space` again = last · `w` · `a` · `x` |

Every switch (`h`/`l`, leader, waking) lands on the agent pane in Normal:
OpenCode on screen, LazyAI keeps the keyboard, so you can keep browsing.
`Enter` or `i` steps into OpenCode; that stream's diff, show set and shell are
still one key away (`d`, `s`, `t`). A freshly created workstream starts focused
since the next thing you do is type its task.

The strip pins the repository's main checkout at the top; every other
workstream is an append-only list, newest last.

`w` opens a prompt (`  branch ›`) with a live fuzzy list underneath: running
workstreams, dormant / previously used worktrees and every local branch,
narrowing as you type, matched letters accented. `↑`/`↓` (or `Ctrl+P`/`Ctrl+N`,
`Tab`) pick one, `Enter` opens it; with nothing picked `Enter` takes what you
typed. For a brand-new branch a second step asks
what to start from — `m` the main branch or `c` the current worktree's branch
(`Esc` goes back to the name). Existing branches and dormant worktrees skip
the question. The worktree is created or reused, OpenCode starts there and
the workstream becomes current. A `show_locations` call from a
background workstream does not steal focus: it flags the stream with `!`; after
you switch there in Normal, `s` opens those locations.
Closing a workstream stops its OpenCode; when the last one exits LazyAI quits.

## Durable state (SQLite)

`$XDG_DATA_HOME/lazyai/lazyai.db` (macOS: `~/Library/Application Support/lazyai/`,
override with `LAZYAI_DB`) keeps, per repository:

- `show_sets` / `show_locations` — every accepted `show_locations` set with
  its notes, branch and OpenCode session id.
- `worktrees` — every worktree LazyAI ran a workstream in, with created /
  last-opened times and a `dormant` flag. `a` archives the current workstream
  (stops its OpenCode and shell, keeps the worktree); the `w` prompt lists
  dormant worktrees and typing one wakes it.
- `repo_state` — small key/value state such as `last_branch`.

## Creature comforts

- `jk` typed quickly in Interactive mode sends a real `Esc` to OpenCode (closes
  its palette, interrupts the agent) — Vim `inoremap jk <Esc>` semantics,
  200 ms timeout; a slow `j` … `k` types normally. A bare `Esc` leaves to LazyAI.
- `Ctrl+Z` (any mode) or `z` (Diff/Show) zooms: the sidebar hides and OpenCode
  gets the full width.
- `]` / `[` in Show mode step through locations from either focus (`]q`/`[q`).
- `1`–`9` jump to the n-th sidebar entry.
- `?` shows the keymap as diagnostic floats.
- The status line has one Neovim-style mode block without a key prefix, such as
  `NORMAL`, `INTERACTIVE`, `TERMINAL`, `DIFF·3`, or `SHOW·2`. Less-frequent
  navigation and mode-switch keys live in `?` help instead of the status line.
- `LAZYAI_INPUT_LOG=<path>` logs raw stdin routing for debugging.

## Sidebar markers

`R` read · `M` modified · `A` added · `D` deleted · `S` shown by the agent.
Changed files sort first; each group is most-recent first.

Diffs are **session-relative**: the baseline is captured right before the
agent's first `edit`/`write` of a file, so pre-existing dirty changes are not
attributed to the agent.

## Show mode

The bundled `show_locations` tool lets the agent send an ordered set of
`{path, line, column?, text}` entries plus a title. Semantics follow the Neovim
quickfix integration: exact 1-based positions, atomic validation, duplicate
locations dropped, a new call replaces the previous set, and the preview is
centered on the selected location.

The agent's explanations are rendered like Neovim diagnostic hovers: a
single-bordered float directly under the target line (with an `path:line:col ·
n/N` footer) in Show mode, and under the file header in Diff mode when the
agent explained that file. The sidebar lists locations as `n path:line`.

## Look

The chrome follows the same three-colour `blue_mist` palette as the author's
tmux and lualine setups (`#1e3a4a` / `#c5d7e5` / `#2a82b5`), with `⸗` notches,
a `▼・ᴥ・▼` badge, and a Neovim-style current-mode block in the status bar.
Content uses Catppuccin Mocha; Diff and Show panes are syntax-highlighted with
Chroma's `catppuccin-mocha` style (added/deleted lines get a green/red tint,
the Show target line a surface tint). Glyphs assume a Nerd Font. Everything
lives in `internal/theme`; Lip Gloss degrades colours on non-truecolor terminals.

## Layout

```
cmd/lazyai            entry point: PTY, raw-mode stdin, wiring
internal/terminal     child process in a PTY + VT emulator + screen renderer
internal/input        raw byte router: child vs host, jk / Ctrl+Space / Ctrl+Z / Ctrl+Q chords
internal/hooks        loopback HTTP receiver for plugin events (one token per workstream)
internal/integration  embedded OpenCode plugin + skill, materialized on start
internal/activity     file ledger (read/modified/shown, reasons, baselines)
internal/diff         unified diff + hunk parsing
internal/show         quickfix-style location set validation and source loading
internal/git          checkout facts + worktree create/reuse
internal/theme        palette, glyphs and Lip Gloss styles (single place to retheme)
internal/highlight    Chroma tokeniser → per-line styled spans, background-safe
internal/app          Bubble Tea model: workstreams, modes, focus, keys, views, references
```

## Debugging

`LAZYAI_INPUT_LOG=/path/to/log` appends every raw stdin chunk with a timestamp
and whether it went to OpenCode (`child`) or to LazyAI (`host`).

## Known limitations

- Files changed via shell commands (not `edit`/`write`) are not tracked yet.
- `opencode --pure` disables external plugins, including LazyAI's.
