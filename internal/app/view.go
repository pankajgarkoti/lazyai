package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazyai/internal/diff"
	"lazyai/internal/fuzzy"
	"lazyai/internal/git"
	"lazyai/internal/highlight"
	"lazyai/internal/show"
	"lazyai/internal/theme"
)

// View --------------------------------------------------------------------

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	rw, rh := m.rightInner()
	sw := m.sidebarWidth - borderCells

	var left string
	if !m.zoom {
		var sidebar string
		switch m.mode {
		case ModeShow:
			sidebar = m.renderShowList(sw, rh)
		default:
			sidebar = m.renderFileList(sw, rh)
		}
		sbStyle := theme.BorderUnfocused
		if m.focus == FocusSidebar {
			sbStyle = theme.BorderFocused
		}
		left = sbStyle.Render(lipgloss.NewStyle().Width(sw).Height(rh).MaxHeight(rh).Render(sidebar))
	}

	var body string
	switch {
	case m.confirmQuit:
		body = strings.Join(m.renderQuitPrompt(rw), "\n")
	case m.setup != nil:
		body = strings.Join(m.renderSetupPrompt(rw), "\n")
	case m.prompting:
		body = strings.Join(m.renderPrompt(rw), "\n")
	case m.help:
		body = strings.Join(renderHelp(rw), "\n")
	case m.mode.live():
		// In normal the pane stays fully visible; only the cursor goes and
		// the border tells you input is not routed there.
		rows := m.liveTerm().Snapshot(!m.normal())
		if len(rows) > rh {
			rows = rows[:rh]
		}
		for len(rows) < rh {
			rows = append(rows, "")
		}
		if m.contract != nil {
			rows = m.renderContract(rows, rw, rh)
		}
		if m.info {
			rows = m.renderInfo(rows, rw, rh)
		}
		body = strings.Join(rows, "\n")
	case m.mode == ModeDiff:
		body = m.diffView.View()
	case m.mode == ModeShow:
		body = m.showView.View()
	}
	if m.info && !m.mode.live() {
		body = strings.Join(m.renderInfo(strings.Split(body, "\n"), rw, rh), "\n")
	}
	rpStyle := theme.BorderUnfocused
	if m.focus == FocusContent {
		rpStyle = theme.BorderFocused
	}
	right := rpStyle.Width(rw).Height(rh).MaxHeight(rh + borderCells).Render(body)

	main := right
	if !m.zoom {
		main = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, main, m.renderStatus())
}

// Status bar ---------------------------------------------------------------

func (m Model) renderStatus() string {
	left := theme.BadgeBlock.Render(" "+theme.Badge+" ") + theme.StatusBar.Render(" ")
	left += m.renderMode() + theme.StatusBar.Render(" ")

	right := m.renderStatusRight()

	// Middle: a transient notice, otherwise key hints. (Show notes live in
	// the diagnostic float next to the code, not here.) A configuration
	// error stays visible until it is fixed and reloaded.
	mid := " " + m.renderHint()
	switch {
	case m.notice != "":
		mid = theme.Notice.Render(" " + theme.IconWarn + " " + m.notice)
	case m.configErr != "":
		mid = theme.Notice.Render(" "+theme.IconWarn+" config error") + theme.StatusDim.Render(" · ctrl+space c reloads · ") + theme.Notice.Render(m.configErr)
	}

	avail := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if avail < 8 {
		right = ""
		avail = m.width - lipgloss.Width(left)
	}
	if avail < 0 {
		avail = 0
	}
	mid = ansi.Truncate(mid, avail, "…")
	mid = theme.StatusBar.Width(avail).MaxWidth(avail).Render(mid)
	return theme.StatusBar.MaxWidth(m.width).Render(left + mid + right)
}

// renderMode is a single Neovim-style mode block. Switching keys belong in
// help, not in front of the mode's label.
func (m Model) renderMode() string {
	if m.contract != nil {
		return theme.ModeInsert.Render(" CONTRACT ")
	}
	freestyle := m.stream != nil && m.project.Interactive.Strict && m.configErr == "" && m.freestyle
	if m.normal() {
		if freestyle {
			return theme.ModeNormal.Render(" NORMAL·FREE ")
		}
		return theme.ModeNormal.Render(" NORMAL ")
	}
	switch m.mode {
	case ModeInteractive:
		switch {
		case freestyle:
			return theme.ModeInsert.Render(" INTERACTIVE·FREE ")
		case m.strictActive():
			return theme.ModeInsert.Render(" INTERACTIVE·STRICT ")
		}
		return theme.ModeInsert.Render(" INTERACTIVE ")
	case ModeTerminal:
		return theme.ModeTerm.Render(" TERMINAL ")
	case ModeDiff:
		return theme.ModeDiff.Render(fmt.Sprintf(" DIFF·%d ", m.changedCount()))
	case ModeShow:
		n := 0
		if m.showSet != nil {
			n = len(m.showSet.Locations)
		}
		return theme.ModeShow.Render(fmt.Sprintf(" SHOW·%d ", n))
	default:
		return theme.ModeNormal.Render(" NORMAL ")
	}
}

// renderStatusRight mirrors tmux's status-right plugin blocks.
func (m Model) renderStatusRight() string {
	var plug string
	if m.pluginOK {
		plug = theme.SepOnBG.Render(theme.Sep) + theme.AccentBlock.Render(" "+theme.Dot+" plugin ")
	} else {
		plug = theme.StatusDim.Render(" "+theme.Ring+" plugin ") + theme.SepOnBG.Render(theme.Sep)
	}
	n := m.ledger.Len()
	files := fmt.Sprintf(" %s %d ", theme.IconFiles, n)
	if m.zoom {
		files = " " + theme.IconZoom + files
	}
	proj := " " + theme.IconFold + " " + filepath.Base(m.root) + " "
	if m.repo.Linked {
		proj = " " + theme.IconWorktree + " " + filepath.Base(m.repo.Top) + " "
	}
	if len(m.streams) > 1 {
		proj = fmt.Sprintf(" %d/%d", m.cur+1, len(m.streams)) + proj
	}
	out := plug +
		theme.SepOnAccent.Render(theme.Sep) + theme.TabActive.Render(files) +
		theme.SepOnAccent.Render(theme.Sep) + theme.TabActive.Render(proj)
	if m.repo.Branch != "" {
		out += theme.SepOnAccent.Render(theme.Sep) + theme.TabActive.Render(" "+theme.IconBranch+" "+m.repo.Branch+" ")
	}
	return out
}

// renderHint shows the keys that matter on the current screen, with the key
// itself accented so the eye can scan the bar. Every state has its own set.
func (m Model) renderHint() string {
	type kv struct{ k, v string }
	var hints []kv
	switch {
	case m.confirmQuit, m.setup != nil, m.contract != nil:
		// The choices and controls are displayed inside the float.
	case m.prompting:
		// Suggestion / field controls are already inside the form.
	case m.leader:
		hints = []kv{{"1-9", "workstream"}, {"ctrl+space", "last"}, {"w", "new"}, {"R", "restore"}, {"K", "details"}, {"e", "rename"}, {"a", "archive"}, {"x", "close"}, {"q", "quit session"}, {"f", "freestyle"}, {"c", "reload config"}, {"z", "zoom"}}
	case m.help:
		hints = []kv{{"?", "close"}, {"esc", "close"}}
	case m.mode == ModeInteractive && m.focus == FocusContent:
		hints = []kv{{"esc", "normal"}, {"ctrl+]", "esc→opencode"}, {"ctrl+space", "workstreams"}, {"ctrl+z", "zoom"}}
	case m.mode == ModeTerminal && m.focus == FocusContent:
		hints = []kv{{"esc", "normal"}, {"ctrl+]", "esc→shell"}, {"ctrl+space", "workstreams"}, {"ctrl+z", "zoom"}}
	case m.info:
		hints = []kv{{"any key", "close"}}
	case m.normal():
		hints = []kv{{"j/k", "workstream"}, {"w", "worktree"}, {"K", "details"}, {"a", "archive"}, {"?", "help"}}
		if n := len(m.restorable); n > 0 {
			hints = append([]kv{{"R", fmt.Sprintf("restore %d", n)}}, hints...)
		}
		if m.strictActive() {
			hints = append([]kv{{"i", "contract"}}, hints...)
		}
	case m.mode == ModeDiff && m.focus == FocusSidebar:
		hints = []kv{{"j/k", "file"}, {"enter", "hunks"}, {"r", "reference"}}
		hints = append(hints, kv{"esc", "normal"}, kv{"i", "opencode"})
	case m.mode == ModeDiff:
		hints = []kv{{"j/k", "scroll"}, {"[ ]", "hunk"}, {"r", "reference hunk"}, {"esc", "files"}}
	case m.mode == ModeShow && m.focus == FocusSidebar:
		hints = []kv{{"j/k", "location"}, {"[ ]", "location"}, {"enter", "source"}, {"r", "reference"}}
		hints = append(hints, kv{"esc", "normal"}, kv{"i", "opencode"})
	case m.mode == ModeShow:
		hints = []kv{{"j/k", "scroll"}, {"[ ]", "location"}, {"r", "reference"}, {"esc", "list"}}
	}
	hints = append(hints, kv{"ctrl+q", "detach"})
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, theme.StatusKey.Render(h.k)+theme.StatusDim.Render(":"+h.v))
	}
	return strings.Join(parts, theme.StatusDim.Render("  "))
}

func (m Model) renderQuitPrompt(w int) []string {
	rows := []string{theme.DiffHeader.Render(theme.IconWarn + " Quit LazyAI?")}
	body := theme.PromptText.Render("Quit LazyAI and stop all workstreams? (ctrl+q only detaches)")
	choices := theme.StatusKey.Render("y") + theme.PromptText.Render(" yes") + "   " +
		theme.StatusKey.Render("n") + theme.PromptText.Render(" no")
	rows = append(rows, renderFloat(theme.IconWarn, theme.DiagWarn, body, choices+theme.Dim.Render(" · esc cancel"), 1, w)...)
	return rows
}

// Sidebar -----------------------------------------------------------------

// streamGlyph is the activity indicator for a stream, by precedence:
// attention (OpenCode waits on you) > working (tool calls in flight) >
// unseen (background output you have not looked at) > idle.
func (m Model) streamGlyph(s *stream) (string, lipgloss.Style) {
	switch {
	case s.attention:
		return theme.Attention, theme.Warn
	case s.working():
		return theme.Frame(m.spin), theme.Icon
	case s.unseen:
		return theme.Unseen, theme.Warn
	}
	return theme.Dot, theme.Dim
}

// stripRows is the number of rows the workstream strip uses under its title.
// It is always one row per workstream so browsing never shifts the layout;
// branch and description live in the status bar and the K float.
func (m Model) stripRows() int { return len(m.streams) }

// renderStreams draws the workstream strip that tops the sidebar in every
// mode: number, nickname and the activity glyph.
func (m Model) renderStreams(w int) string {
	var b strings.Builder
	b.WriteString(sidebarTitle(theme.IconWorktree+" Workstreams", fmt.Sprint(len(m.streams)), w))
	b.WriteString("\n")
	for i, s := range m.streams {
		glyph, gs := m.streamGlyph(s)
		idx := fmt.Sprintf("%d", i+1)
		name := ansi.Truncate(s.displayName(), w-len(idx)-4, "…")
		gap := w - len(idx) - 1 - lipgloss.Width(name) - 1
		if gap < 1 {
			gap = 1
		}
		switch {
		case i == m.cur && m.mode == ModeInteractive:
			b.WriteString(theme.SelLine.Width(w).Render(idx + " " + name + strings.Repeat(" ", gap) + glyph))
		case i == m.cur:
			b.WriteString(theme.SelDim.Width(w).Render(idx + " " + name + strings.Repeat(" ", gap) + glyph))
		default:
			b.WriteString(theme.Index.Render(idx) + " " + name + strings.Repeat(" ", gap) + gs.Render(glyph))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderInfo lays the workstream float over the content rows: at the pane's
// left edge, aligned with the current strip row, so it reads as a hover on
// that entry. It shows what the strip deliberately does not: branch,
// description, worktree and activity.
func (m Model) renderInfo(rows []string, w, h int) []string {
	s := m.stream
	state := "idle"
	switch {
	case s.attention:
		state = "waiting on you"
	case s.working():
		state = fmt.Sprintf("working (%d tool call%s)", len(s.active), map[bool]string{true: "", false: "s"}[len(s.active) == 1])
	case s.unseen:
		state = "unseen output"
	}
	// Plain text only: renderFloat wraps and styles the body itself, and
	// inline escapes would confuse its width accounting.
	var b strings.Builder
	b.WriteString(theme.IconBranch + " " + s.name + "\n")
	if s.description != "" {
		b.WriteString(s.description + "\n")
	} else {
		b.WriteString("no description · e to add one\n")
	}
	b.WriteString(theme.IconWorktree + " " + s.root)
	footer := state + " · any key closes"
	box := renderFloat(theme.IconInfo, theme.DiagInfo, s.displayName()+"\n"+strings.TrimRight(b.String(), "\n"), footer, 0, w)
	for len(rows) < h {
		rows = append(rows, "")
	}
	out := make([]string, len(rows))
	copy(out, rows)
	top := m.cur // strip row of the current stream, in pane coordinates
	if top+len(box) > len(out) {
		top = max(0, len(out)-len(box))
	}
	for i, r := range box {
		if y := top + i; y >= 0 && y < len(out) {
			out[y] = ansi.Truncate(r, w, "")
		}
	}
	return out
}

func (m Model) renderFileList(w, h int) string {
	var b strings.Builder
	b.WriteString(m.renderStreams(w))
	b.WriteString("\n")
	h -= m.stripRows() + 2
	entries := m.ledger.Entries()
	b.WriteString(sidebarTitle(theme.IconFiles+" Files", fmt.Sprint(len(entries)), w))
	b.WriteString("\n")
	if len(entries) == 0 {
		b.WriteString(theme.Dim.Render("  nothing touched yet"))
	}
	active := m.mode == ModeDiff || m.normal()
	for i := m.fileOffset; i < len(entries); i++ {
		if i-m.fileOffset >= h-1 {
			break
		}
		e := entries[i]
		mark := e.Marker()
		icon := theme.FileIcon(e.Path)
		name := ansi.Truncate(e.Path, w-5, "…")
		plain := mark + " " + icon + " " + name
		var line string
		switch {
		case active && i == m.fileSel && m.focus == FocusSidebar:
			line = theme.SelLine.Width(w).Render(plain)
		case active && i == m.fileSel:
			line = theme.SelDim.Width(w).Render(plain)
		default:
			line = theme.Marks[mark].Render(mark) + " " + theme.Icon.Render(icon) + " " + name
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderShowList(w, h int) string {
	var b strings.Builder
	b.WriteString(m.renderStreams(w))
	b.WriteString("\n")
	h -= m.stripRows() + 2
	title := "Show"
	count := ""
	if m.showSet != nil {
		title = m.showSet.Title
		count = fmt.Sprint(len(m.showSet.Locations))
	}
	b.WriteString(sidebarTitle(theme.IconShow+" "+title, count, w))
	b.WriteString("\n")
	if m.showSet == nil {
		b.WriteString(theme.Dim.Render("  nothing shown yet"))
		return b.String()
	}
	for i := m.showOffset; i < len(m.showSet.Locations); i++ {
		if i-m.showOffset >= h-1 {
			break
		}
		loc := m.showSet.Locations[i]
		// The note itself is shown inline next to the code (diagnostic
		// float), so the list row is the location: "n  path:line[:col]".
		idx := fmt.Sprintf("%d", i+1)
		icon := theme.FileIcon(loc.Path)
		pos := fmt.Sprintf("%s:%d", loc.Path, loc.Line)
		if loc.Column > 1 {
			pos += fmt.Sprintf(":%d", loc.Column)
		}
		pos = ansi.Truncate(pos, w-len(idx)-lipgloss.Width(icon)-2, "…")
		switch {
		case i == m.showSel && m.focus == FocusSidebar:
			b.WriteString(theme.SelLine.Width(w).Render(idx + " " + icon + " " + pos))
		case i == m.showSel:
			b.WriteString(theme.SelDim.Width(w).Render(idx + " " + icon + " " + pos))
		default:
			b.WriteString(theme.Index.Render(idx) + " " + theme.Icon.Render(icon) + " " + pos)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// sidebarTitle renders "title ……… count" across the sidebar width.
func sidebarTitle(title, count string, w int) string {
	title = ansi.Truncate(title, w-len(count)-1, "…")
	gap := w - lipgloss.Width(title) - lipgloss.Width(count)
	if gap < 1 {
		gap = 1
	}
	return theme.Title.Render(title) + strings.Repeat(" ", gap) + theme.Dim.Render(count)
}

// Diagnostic floats --------------------------------------------------------

// renderFloat draws an agent explanation the way Neovim shows a diagnostic
// hover: a single-line bordered box, severity icon, word-wrapped message and
// a dim footer, indented to a column and never wider than width. Returns one
// row per line; nil when the message is empty.
func renderFloat(icon string, iconStyle lipgloss.Style, msg, footer string, indent, width int) []string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	avail := width - indent
	if avail < 12 {
		indent = 0
		avail = width
	}
	if avail < 12 {
		return nil
	}
	iconW := lipgloss.Width(icon) + 1
	inner := avail - 4 // border + padding on each side
	// Fit the box to the text when the note is short.
	wrapped := strings.Split(ansi.Wrap(msg, inner-iconW, ""), "\n")
	maxW := lipgloss.Width(footer)
	for _, l := range wrapped {
		if w := lipgloss.Width(l) + iconW; w > maxW {
			maxW = w
		}
	}
	if maxW < inner {
		inner = maxW
	}
	pad := strings.Repeat(" ", indent)
	line := func(content string) string {
		gap := inner - lipgloss.Width(content)
		if gap < 0 {
			gap = 0
		}
		return pad + theme.FloatBorder.Render(theme.FloatV) + " " + content + strings.Repeat(" ", gap) + " " + theme.FloatBorder.Render(theme.FloatV)
	}
	rows := []string{pad + theme.FloatBorder.Render(theme.FloatTL+strings.Repeat(theme.FloatH, inner+2)+theme.FloatTR)}
	for i, l := range wrapped {
		lead := strings.Repeat(" ", iconW)
		if i == 0 {
			lead = iconStyle.Render(icon) + " "
		}
		rows = append(rows, line(lead+theme.FloatText.Render(l)))
	}
	if footer != "" {
		rows = append(rows, line(theme.FloatFooter.Render(ansi.Truncate(footer, inner, "…"))))
	}
	rows = append(rows, pad+theme.FloatBorder.Render(theme.FloatBL+strings.Repeat(theme.FloatH, inner+2)+theme.FloatBR))
	return rows
}

// Content renderers -------------------------------------------------------

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// renderDiff draws a unified diff delta-style: syntax-highlighted content from
// the matching side of the file laid over a green/red tint, a coloured sign
// column, and accent hunk headers. old/cur may be nil, in which case the raw
// diff text is used.
func renderDiff(r diff.Result, old, cur []highlight.Line, reason string, w int) string {
	title := theme.DiffHeader.Render(theme.FileIcon(r.Path) + " " + r.Path)
	if len(r.Lines) == 0 {
		// No change to explain, so the agent's Show note (reason) would only
		// confuse here; it already lives next to the code in Show mode.
		rows := append([]string{ansi.Truncate(title, w, "…")}, renderFloat(theme.IconWarn, theme.DiagWarn, r.Note, "", 1, w)...)
		if len(cur) > 0 {
			rows = append(rows, "")
			rows = append(rows, renderGutteredSource(cur, w)...)
		}
		return strings.Join(rows, "\n")
	}
	refs := diff.Annotate(r.Lines)
	body := w - 1
	if body < 1 {
		body = 1
	}
	pick := func(side []highlight.Line, n int, raw string) (highlight.Line, bool) {
		if n >= 1 && n <= len(side) {
			return side[n-1], true
		}
		return highlight.Line{{Text: expandTabs(raw)}}, false
	}
	var b strings.Builder
	for i, l := range r.Lines {
		ref := refs[i]
		var out string
		switch ref.Kind {
		case diff.KindHeader:
			if i == 0 {
				out = ansi.Truncate(title, w, "…")
			} else {
				out = theme.Dim.Render(ansi.Truncate(r.Lines[0]+" → "+l, w, "…"))
				if fl := renderFloat(theme.IconInfo, theme.DiagInfo, reason, "", 1, w); fl != nil {
					out += "\n" + strings.Join(fl, "\n")
				}
			}
		case diff.KindHunk:
			out = theme.DiffHunk.Render(ansi.Truncate(theme.Sep+theme.Sep+" "+l, w, "…"))
		case diff.KindAdd:
			hl, _ := pick(cur, ref.New, l[1:])
			out = theme.DiffAddSign.Render("+") + tintRow(hl.Render(theme.DiffAddBG), theme.DiffAddLine, body)
		case diff.KindDel:
			hl, _ := pick(old, ref.Old, l[1:])
			out = theme.DiffDelSign.Render("-") + tintRow(hl.Render(theme.DiffDelBG), theme.DiffDelLine, body)
		case diff.KindContext:
			raw := ""
			if len(l) > 0 {
				raw = l[1:]
			}
			hl, _ := pick(cur, ref.New, raw)
			out = theme.DiffCtxSign.Render(" ") + ansi.Truncate(hl.Render(nil), body, "…")
		default:
			out = theme.Dim.Render(ansi.Truncate(expandTabs(l), w, "…"))
		}
		b.WriteString(out)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderGutteredSource draws highlighted lines with a "NN │ " gutter (the
// Show view's look without a target).
func renderGutteredSource(lines []highlight.Line, w int) []string {
	digits := len(fmt.Sprint(len(lines)))
	body := w - digits - 3
	if body < 1 {
		body = 1
	}
	rows := make([]string, 0, len(lines))
	for i, l := range lines {
		rows = append(rows, theme.LineNo.Render(fmt.Sprintf("%*d │ ", digits, i+1))+ansi.Truncate(l.Render(nil), body, "…"))
	}
	return rows
}

// diffPadRows counts the rows renderDiff inserts before the first hunk beyond
// the diff's own header lines, so hunk indices can be mapped to view rows.
func diffPadRows(reason string, w int) int {
	return len(renderFloat(theme.IconInfo, theme.DiagInfo, reason, "", 1, w))
}

// tintRow truncates styled content to width and pads it with the row tint.
func tintRow(content string, tint lipgloss.Style, width int) string {
	content = ansi.Truncate(content, width, "…")
	return tint.Width(width).MaxWidth(width).Render(content)
}

// renderSource draws a Show-mode preview: file header, line-number gutter
// with a thin rule, syntax-highlighted lines, and the target line raised on a
// surface tint with its column marked.
func renderSource(lines []highlight.Line, err error, loc show.Location, idx, total, w int) string {
	header := theme.DiffHeader.Render(theme.FileIcon(loc.Path)+" "+loc.Path) +
		theme.Dim.Render(fmt.Sprintf(":%d:%d", loc.Line, loc.Column))
	if err != nil {
		return header + "\n\n" + theme.Warn.Render("  "+theme.IconWarn+" cannot read file: "+err.Error())
	}
	digits := len(fmt.Sprint(len(lines)))
	gutterW := digits + 3 // "NN │ "
	body := w - gutterW
	if body < 1 {
		body = 1
	}
	var b strings.Builder
	b.WriteString(ansi.Truncate(header, w, "…"))
	b.WriteString("\n")
	for i, l := range lines {
		n := i + 1
		if n != loc.Line {
			num := theme.LineNo.Render(fmt.Sprintf("%*d │ ", digits, n))
			b.WriteString(num)
			b.WriteString(ansi.Truncate(l.Render(nil), body, "…"))
			b.WriteString("\n")
			continue
		}
		num := theme.LineNoTarget.Render(fmt.Sprintf("%*d ", digits, n)) + theme.Title.Render("┃ ")
		col := loc.Column - 1
		head, tail := l.Cut(col)
		var text string
		if len(tail) > 0 {
			at, rest := tail.Cut(1)
			text = head.Render(theme.Surface0) + theme.TargetCol.Render(at.Plain()) + rest.Render(theme.Surface0)
		} else {
			text = head.Render(theme.Surface0) + theme.TargetCol.Render(" ")
		}
		b.WriteString(num)
		b.WriteString(tintRow(text, theme.TargetLine, body))
		b.WriteString("\n")
		footer := fmt.Sprintf("%s:%d:%d · %d/%d", loc.Path, loc.Line, loc.Column, idx, total)
		for _, row := range renderFloat(theme.IconInfo, theme.DiagInfo, loc.Note, footer, gutterW, w) {
			b.WriteString(row)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// changedCount is the number of files with a session diff.
func (m Model) changedCount() int {
	n := 0
	for _, e := range m.ledger.Entries() {
		if e.Changed() {
			n++
		}
	}
	return n
}

// renderHelp draws the keymap as one diagnostic-style float.
func renderHelp(w int) []string {
	sections := []struct{ title, keys string }{
		{"session lifecycle (available on every screen)", "ctrl+q: detach and keep all work running · reattach: run lazyai for the project · ctrl+space q: quit the session (stops every workstream, after confirming) · lazyai list: inspect sessions · lazyai stop --dir DIR: stop a project and all workstreams"},
		{"interactive / terminal (the pane owns the keys)", "esc: normal (pane remains visible, no input) · ctrl+]: send a real ESC into the pane · ctrl+space: workstream leader · ctrl+z: zoom"},
		{"normal (pane focused out)", "i: opencode · t: terminal · d: diff (when there are changes) · s: show (when the agent pointed at code) · j/k: previous / next workstream · h/l: pick a file for d · enter: back into the pane"},
		{"workstreams (one OpenCode per git worktree)", "j / k in normal, h / l in diff / show: previous / next · w: new or wake a dormant worktree (branch, nickname, optional description) · K: details float (branch, description, worktree, activity; any key closes) · R: restore the workstreams the previous session left open · e: rename the current one · a: archive (dormant: stops OpenCode, keeps the worktree) · x x: close · from a pane: ctrl+space then h / l / 1-9 / ctrl+space (last) / w / R / K / e / a / x"},
		{"strip glyphs", "! OpenCode waits on you · spinner: tool calls running · " + theme.Unseen + " output you have not looked at · " + theme.Dot + " idle"},
		{"strict contracts (.lazyai/config.yaml)", "i / enter: fill the contract form instead of typing · tab: next field · ctrl+s: send · esc: keep draft and close · ctrl+space f: freestyle for this workstream · ctrl+space c: reload config · agents: setup_workstreams tool opens workstreams like w"},
		{"sidebar (diff / show)", "j/k: select · h/l: workstream · 1-9: jump · enter: focus content · esc: normal · tab: focus"},
		{"content", "j/k: scroll · ctrl+d/u: half page · g/G: top/bottom · esc/h: back to sidebar"},
		{"diff", "[ ]: previous/next hunk · r: reference hunk in prompt"},
		{"show", "[ ]: previous/next location (either focus) · r: reference location"},
	}
	var b strings.Builder
	for i, sct := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sct.title + "\n  " + sct.keys + "\n")
	}
	rows := []string{theme.DiffHeader.Render(theme.IconHelp + " keys")}
	// renderFloat wraps long lines; keep each section on its own lines.
	for _, block := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n\n") {
		rows = append(rows, renderFloat(theme.IconInfo, theme.DiagInfo, block, "", 1, w)...)
	}
	return rows
}

// renderMatches lists the live fuzzy matches under the prompt, matched runes
// accented, the selected row raised, each tagged with what it is.
func (m Model) renderMatches(w int) []string {
	if len(m.matches) == 0 {
		return nil
	}
	const maxRows = 8
	query := strings.TrimSpace(m.prompt.Value())
	var rows []string
	for i, c := range m.matches {
		if i >= maxRows {
			rows = append(rows, "   "+theme.Dim.Render(fmt.Sprintf("… %d more", len(m.matches)-maxRows)))
			break
		}
		tag := map[string]string{"open": theme.Dot + " open", "dormant": theme.IconWorktree + " dormant", "branch": theme.IconBranch + " branch"}[c.kind]
		name := highlightMatch(query, c.name)
		line := "   " + name + "  " + theme.Dim.Render(tag)
		if i == m.matchSel {
			line = " " + theme.SelLine.Render(" "+ansi.Strip(name)+" ") + "  " + theme.Dim.Render(tag)
		}
		rows = append(rows, ansi.Truncate(line, w, "…"))
	}
	return rows
}

// highlightMatch accents the runes of name matched by query.
func highlightMatch(query, name string) string {
	pos, ok := fuzzy.Positions(query, name)
	if !ok || len(pos) == 0 {
		return theme.PromptText.Render(name)
	}
	set := map[int]bool{}
	for _, p := range pos {
		set[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(name) {
		if set[i] {
			b.WriteString(theme.StatusKey.Render(string(r)))
		} else {
			b.WriteString(theme.PromptText.Render(string(r)))
		}
	}
	return b.String()
}

// renderPrompt draws the workstream form as floats: branch, then identity
// (nickname + description), then the base choice for a new branch.
func (m Model) renderPrompt(w int) []string {
	where := filepath.Join(m.repo.Main, git.WorktreeDir, "<branch>")
	title := " new workstream"
	if m.editing {
		title = " rename workstream"
	}
	rows := []string{theme.DiffHeader.Render(theme.IconWorktree + title)}
	name := strings.TrimSpace(m.prompt.Value())
	if m.promptStage == stageName {
		body := theme.PromptLabel.Render("branch ›") + " " + theme.PromptText.Render(m.prompt.View())
		rows = append(rows, renderFloat(theme.IconBranch, theme.DiagInfo, body,
			fmt.Sprintf("worktree %s from %s · type filters · ↑/↓ pick · enter open/create · esc cancel", where, m.repo.Branch), 1, w)...)
		rows = append(rows, m.renderMatches(w)...)
		return rows
	}
	// Identity (also shown, settled, above the base choice).
	mark := func(i int) string {
		if m.promptStage == stageIdentity && m.field == i {
			return theme.StatusKey.Render("›")
		}
		return theme.Dim.Render("·")
	}
	body := theme.PromptLabel.Render("branch") + "      " + theme.PromptText.Render(name) + "\n" +
		mark(0) + " " + theme.PromptLabel.Render("nickname") + "    " + theme.PromptText.Render(m.nick.View()) + theme.Dim.Render(" *") + "\n" +
		mark(1) + " " + theme.PromptLabel.Render("description") + " " + theme.PromptText.Render(m.desc.View())
	footer := "tab: next field · enter: continue · esc: back"
	if m.editing {
		footer = "tab: next field · enter: save · esc: cancel"
	}
	if m.promptStage == stageBase {
		footer = "named " + strings.TrimSpace(m.nick.Value())
	}
	rows = append(rows, renderFloat(theme.IconWorktree, theme.DiagInfo, body, footer, 1, w)...)
	if m.promptStage == stageBase {
		choice := theme.PromptLabel.Render("branch off ›") + " " +
			theme.StatusKey.Render("m") + theme.PromptText.Render(" "+m.mainBranch()+" (main)") + "   " +
			theme.StatusKey.Render("c") + theme.PromptText.Render(" "+m.repo.Branch+" (current)")
		rows = append(rows, renderFloat(theme.IconBranch, theme.DiagInfo, choice,
			fmt.Sprintf("new branch %s · enter: main · esc: back", name), 1, w)...)
	}
	return rows
}
