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

## Versioning and release builds

Current version: **0.2.0**: workstream identities, agent-driven workstream
setup, strict contract entry, accurate activity indicators.
LazyAI uses Semantic Versioning (`MAJOR.MINOR.PATCH`). During `0.x`
development, new features and breaking changes increment the minor version;
compatible fixes increment the patch version. Version `1.0.0` will mark a stable
public interface. Release tags use `vMAJOR.MINOR.PATCH`.

The application version is defined in `cmd/lazyai/version.go` and is included in
both regular and release builds. Run `lazyai --version` to inspect it.

```sh
make release          # optimized Go build with paths and debug symbols stripped
./bin/lazyai --version
```

Pushing a `vX.Y.Z` tag that matches `version.go` runs the gates
(`.github/workflows/release.yml`: vet, race tests) and publishes a GitHub
release with `darwin`/`linux` × `arm64`/`amd64` archives and `SHA256SUMS`.
Pull requests run `.github/workflows/ci.yml` (gofmt, vet, race tests, build).
The tmux drive (`scripts/test-sessions-tmux.py`, see
`docs/persistent-sessions.md`) needs tmux 3.7+ and an installed OpenCode, so
it stays a documented local pre-release gate.

## Sessions

LazyAI keeps one session alive per project when its terminal client detaches.
Starting LazyAI again from the repository or any linked worktree reattaches to
the same OpenCode processes and restores their current screen.

```sh
lazyai                         # start or reattach this project
lazyai list                    # show known running, stopped, exited, and stale sessions
lazyai stop --dir ~/code/app   # terminate one project's session
lazyai --help                  # show launch options and session controls
```

`Ctrl+Q` detaches from any screen without stopping OpenCode, shells, hooks, or
workstreams. Closing the terminal client has the same persistence behavior. A
new attachment takes over from an older client. Launch options apply when a
session starts; reattaching keeps the already-running session and its original
options. Reattaching with `--worktree` does not create an unused branch or
worktree; use `w` inside the session to open another workstream. Stop the session
first when you need to relaunch with different options.

Session and workstream controls have different scopes:

| Control | Scope |
|---|---|
| `Ctrl+Q` | Detach this terminal client; leave the project session running |
| `a` | Archive the current workstream; stop its processes but keep its worktree |
| `x x` | Close the current workstream and its processes |
| `lazyai stop --dir DIR` | Stop the entire project session and every workstream |

Git worktrees share the session for their canonical main checkout; independent
clones and non-Git directories remain isolated. `lazyai list` reports `running`
for an attachable session, `stopped` after an explicit stop, `exited` when the
supervised LazyAI process ends, and `stale` when the recorded supervisor is no
longer reachable. A reboot or supervisor failure loses live PTYs; LazyAI reports
that state instead of pretending to reconstruct those processes.

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
| `Ctrl+Q`  | Detach; leave LazyAI and all child processes running |

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
| `e`                | Rename workstream (nickname / description) | Rename     |
| `a`                | Archive workstream (dormant) | Archive workstream       |
| `t`                | Terminal in the worktree     | Terminal in the worktree |
| `x x`              | Close workstream             | Close workstream         |
| `z` / `?`          | Zoom / help                  | Zoom / help              |
| `1`–`9`            | Jump to entry                | –                        |
| `[` / `]` (Show)   | Previous / next location     | Previous / next location |

Mouse clicks focus panes and select workstreams, files, locations, prompt
choices and form fields. The wheel scrolls the pane, list or field under the
pointer; right-click backs out of the workstream form one stage at a time and
closes the contract form. Mouse input inside OpenCode or a terminal is
forwarded with coordinates adjusted to that pane (press, release, motion,
wheel and modifiers).

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
(`n  nickname  glyph`) and the status bar shows `n/N`. The current workstream
adds a dim detail row with its branch and description. The glyph is the
stream's activity, by precedence:

| Glyph | Meaning |
|---|---|
| `!` | OpenCode is waiting on you (permission / question); stays until it is answered |
| spinner | one or more tool calls are running (animated; counted per call, so overlapping calls never show idle early) |
| `◆` | something happened while you were elsewhere: edits or a `show_locations` set you have not looked at; clears when you visit |
| `●` | idle |

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

`w` opens the workstream form. The first field (`branch ›`) has a live fuzzy
list underneath: running workstreams, dormant / previously used worktrees and
every local branch, narrowing as you type, matched letters accented. `↑`/`↓`
(or `Ctrl+P`/`Ctrl+N`, `Tab`) pick one, `Enter` opens it; with nothing picked
`Enter` takes what you typed. A brand-new branch then gets an identity:
a **nickname** (required, prefilled with the branch, ≤ 60 cells) — what the
strip shows — and an optional one-line **description** (≤ 240 characters)
reminding you what the workstream is for when you juggle several. `Tab` moves
between the two, `Enter` continues, `Esc` steps back. The last step asks what
to start from — `m` the main branch or `c` the current worktree's branch.
Existing branches and dormant worktrees keep the identity they were given and
skip both questions. The worktree is created or reused, OpenCode starts there
and the workstream becomes current. `e` renames the current workstream
(nickname and description only; the branch never changes). A `show_locations`
call from a background workstream does not steal focus: it flags the stream
with `◆`; after you switch there in Normal, `s` opens those locations.
Closing a workstream stops its OpenCode; when the last one exits LazyAI quits.

**Agents can set up workstreams too.** The bundled plugin adds a
`setup_workstreams` tool; when you ask the agent to split work into
workstreams it passes a list of `{branch, nickname, description?, base?}` and
LazyAI runs exactly the same command the `w` form uses. The whole batch is
validated first (branch names, nicknames, bases, duplicates, at most 10) and
rejected as a whole on any error; if it would create more than one new branch
a float asks you to confirm (`y` / `n`). New workstreams are appended in
request order and your current workstream keeps focus; the tool call returns
per-branch results (created / opened / failed). Requests are scoped to the
repository of the workstream whose agent made them.

## Strict contracts (`.lazyai/config.yaml`)

Optionally, a project can force instructions to the agent into a structured
shape. Create `<repo>/.lazyai/config.yaml` in the main checkout (it is shared
by every worktree):

```yaml
version: 1
interactive:
  strict: true
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
          type: text
```

With `strict: true`, `i`, `Enter` and a click on the agent pane open the
contract form centred over OpenCode instead of handing it the keyboard. `Tab`
/ `Shift+Tab` move between fields, `Enter` on a single-line field moves on
(and submits from the last one), `Ctrl+S` submits, `Esc` closes and keeps your
draft, right-click closes. Missing required fields are flagged in place and
nothing is sent. Submitting sends one deterministic YAML document as a single
paste (`contract: task`, then every non-empty field in template order as a
literal block) followed by Enter, and then focuses OpenCode as usual. The
bundled skill tells the agent to treat every field as binding.

`Ctrl+Space` `f` toggles **freestyle** for the current workstream only (the
mode block shows `INTERACTIVE·FREE` / `NORMAL·FREE`); it resets when that
workstream is restarted and never edits the file. `Ctrl+Space` `c` reloads the
configuration. Without the file nothing changes. A malformed or unsupported
file (bad YAML, `version` ≠ 1, duplicate field keys, unknown field types,
missing labels, undefined `default_contract`) disables strict entry and shows
a persistent `config error` in the status bar rather than enforcing anything
else; unknown top-level keys only warn. Field types are `text` (one line) and
`multiline`. Strict mode governs what *you* type; OpenCode's own dialogs
(permissions, palette) are untouched.

## Durable state (SQLite)

`$XDG_DATA_HOME/lazyai/lazyai.db` (macOS: `~/Library/Application Support/lazyai/`,
override with `LAZYAI_DB`) keeps, per repository:

- `show_sets` / `show_locations` — every accepted `show_locations` set with
  its notes, branch and OpenCode session id.
- `worktrees` — every worktree LazyAI ran a workstream in, with created /
  last-opened times, a `dormant` flag, and (schema v1) its `nickname` and
  `description`. `a` archives the current workstream (stops its OpenCode and
  shell, keeps the worktree); the `w` form lists dormant worktrees and typing
  one wakes it with the identity it had. The schema is versioned with SQLite
  `PRAGMA user_version`; migrations are additive, so an older LazyAI keeps
  working on a newer database and rows from before 0.2.0 read back with the
  branch as their name.
- `repo_state` — small key/value state such as `last_branch`.
- `runtime_sessions` — project supervisor discovery and lifecycle metadata.
  The Unix socket, rather than the recorded PID, is authoritative for liveness.

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
  `NORMAL`, `INTERACTIVE`, `INTERACTIVE·STRICT`, `CONTRACT`, `TERMINAL`,
  `DIFF·3`, or `SHOW·2`. Less-frequent navigation and mode-switch keys live in
  `?` help instead of the status line.
- Every form and float is usable from the keyboard alone and fits a 60×18
  terminal: fields share the available rows, focus is always marked with `›`,
  errors appear next to the field they concern, and `Esc` always backs out
  without losing what you typed.
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
cmd/lazyai            attach client, supervisor lifecycle, and direct TUI wiring
internal/supervisor   project identity, Unix-socket protocol, outer PTY ownership
internal/terminal     child process in a PTY + VT emulator + screen renderer
internal/input        raw byte router: child vs host, jk / Ctrl+Space / Ctrl+Z / Ctrl+Q chords
internal/hooks        loopback HTTP receiver for plugin events (one token per workstream); setup requests get a reply
internal/integration  embedded OpenCode plugin (show_locations, setup_workstreams) + skill, materialized on start
internal/config       optional .lazyai/config.yaml: strict contract templates, validation, deterministic rendering
internal/activity     file ledger (read/modified/shown, reasons, baselines)
internal/diff         unified diff + hunk parsing
internal/show         quickfix-style location set validation and source loading
internal/git          checkout facts + worktree create/reuse
internal/theme        palette, glyphs and Lip Gloss styles (single place to retheme)
internal/highlight    Chroma tokeniser → per-line styled spans, background-safe
internal/notes        SQLite state with versioned, additive migrations
internal/app          Bubble Tea model: workstreams (OpenWorkstream shared by w and agents), modes, focus, keys, forms, views
```

## Debugging

`LAZYAI_INPUT_LOG=/path/to/log` appends every raw stdin chunk with a timestamp
and whether it went to OpenCode (`child`) or to LazyAI (`host`).

## Known limitations

- Files changed via shell commands (not `edit`/`write`) are not tracked yet.
- `opencode --pure` disables external plugins, including LazyAI's.
