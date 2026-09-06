package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"lazyai/internal/diff"
	"lazyai/internal/highlight"
	"lazyai/internal/hooks"
	"lazyai/internal/input"
	"lazyai/internal/notes"
	"lazyai/internal/show"
	"lazyai/internal/terminal"
	"lazyai/internal/theme"
)

type harness struct {
	m        Model
	root     string
	forward  []bool
	children []*terminal.Terminal
	tokens   int
	sinks    []input.Sink
}

// launch starts /bin/cat as a stand-in child: it echoes whatever is pasted,
// so references become visible on its screen.
func (h *harness) launch(dir string, w, hgt int) (*terminal.Terminal, string, error) {
	term, err := terminal.Start(terminal.Options{Command: "/bin/cat", Dir: dir, Env: os.Environ(), Width: w, Height: hgt})
	if err != nil {
		return nil, "", err
	}
	h.children = append(h.children, term)
	h.tokens++
	return term, fmt.Sprintf("tok%d", h.tokens), nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{root: root}
	m, err := New(Config{
		Root:   root,
		Launch: h.launch,
		LaunchShell: func(dir, token string, w, hgt int) (*terminal.Terminal, error) {
			term, err := terminal.Start(terminal.Options{Command: "/bin/cat", Dir: dir, Env: os.Environ(), Width: w, Height: hgt})
			if err == nil {
				h.children = append(h.children, term)
			}
			return term, err
		},
		SetForward: func(v bool) { h.forward = append(h.forward, v) },
		SetChild:   func(s input.Sink) { h.sinks = append(h.sinks, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range h.children {
			c.Close()
		}
	})
	h.m = m
	h.forward = nil
	h.update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return h
}

// hook delivers an event as if the current stream's plugin sent it.
func (h *harness) hook(ev hooks.Event) {
	ev.Token = h.m.token
	h.update(HookMsg{Event: ev})
}

func (h *harness) update(msg tea.Msg) {
	mm, _ := h.m.Update(msg)
	h.m = mm.(Model)
}

func (h *harness) key(k string) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+n":
		msg = tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+p":
		msg = tea.KeyMsg{Type: tea.KeyCtrlP}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	h.update(msg)
}

func (h *harness) mouse(x, y int, button tea.MouseButton) {
	h.update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: button})
}

func (h *harness) writeFile(t *testing.T, rel string, lines int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	p := filepath.Join(h.root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(b.String()), 0o644)
	return p
}

func TestEscapeGoesToNormalAndIReturnsToOpenCode(t *testing.T) {
	h := newHarness(t)
	if h.m.mode != ModeInteractive || h.m.focus != FocusContent {
		t.Fatal("initial state should be interactive with OpenCode focused")
	}
	h.update(EscapeMsg{})
	if !h.m.normal() {
		t.Fatalf("mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	h.key("i")
	if h.m.mode != ModeInteractive || h.m.focus != FocusContent {
		t.Fatal("i should return to OpenCode")
	}
	if len(h.forward) != 2 || h.forward[0] != false || h.forward[1] != true {
		t.Fatalf("forward toggles = %v", h.forward)
	}
}

func TestQuitRequiresYConfirmationFromChildAndHost(t *testing.T) {
	h := newHarness(t)

	// The router sends QuitMsg while the child owns the keyboard.
	mm, cmd := h.m.Update(QuitMsg{})
	h.m = mm.(Model)
	if cmd != nil || !h.m.confirmQuit || h.forward[len(h.forward)-1] {
		t.Fatalf("quit request should capture input, confirm=%v forward=%v", h.m.confirmQuit, h.forward)
	}
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "Quit LazyAI") || !strings.Contains(view, "y yes") || !strings.Contains(view, "n no") {
		t.Fatalf("quit confirmation missing:\n%s", view)
	}
	h.key("n")
	if h.m.confirmQuit || !h.forward[len(h.forward)-1] {
		t.Fatalf("n should restore child input: confirm=%v forward=%v", h.m.confirmQuit, h.forward)
	}

	// If another modal was already open, cancelling quit must not route keys
	// past it to the child.
	h.m.prompting = true
	h.update(QuitMsg{})
	h.key("n")
	if !h.m.prompting || h.forward[len(h.forward)-1] {
		t.Fatalf("n should restore prompt ownership: prompting=%v forward=%v", h.m.prompting, h.forward)
	}
	h.m.closePrompt()

	// Host-owned Ctrl+Q follows the same path; Esc cancels without changing
	// the pre-existing Normal state.
	h.update(EscapeMsg{})
	mm, cmd = h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	h.m = mm.(Model)
	if cmd != nil || !h.m.confirmQuit || h.forward[len(h.forward)-1] {
		t.Fatalf("host ctrl+q should confirm in normal: confirm=%v forward=%v", h.m.confirmQuit, h.forward)
	}
	h.key("esc")
	if h.m.confirmQuit || h.forward[len(h.forward)-1] {
		t.Fatalf("esc should return to normal: confirm=%v forward=%v", h.m.confirmQuit, h.forward)
	}

	// Only y returns Bubble Tea's quit command.
	h.update(QuitMsg{})
	mm, cmd = h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	h.m = mm.(Model)
	if cmd == nil {
		t.Fatal("y should return a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("y should emit tea.QuitMsg")
	}
}

func TestShowEventOpensShowModeWithQuickfixSemantics(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "a/b.go", 200)

	h.hook(hooks.Event{Type: "show", Title: "Walkthrough", Locations: []hooks.Location{
		{Path: p, Line: 40, Column: 3, Text: "first"},
		{Path: "a/b.go", Line: 40, Column: 3, Text: "dup"},
		{Path: "a/b.go", Line: 70, Column: 1, Text: "second"},
	}})

	if h.m.mode != ModeShow || h.m.focus != FocusSidebar {
		t.Fatalf("mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	if got := len(h.m.showSet.Locations); got != 2 {
		t.Fatalf("locations=%d want 2 (dedupe)", got)
	}
	// First location previewed and centred.
	_, rh := h.m.rightInner()
	if off := h.m.showView.YOffset; off != 40-rh/2 {
		t.Fatalf("YOffset=%d want %d", off, 40-rh/2)
	}
	if e, _ := h.m.ledger.Get("a/b.go"); e.Marker() != "S" || e.Reason != "second" {
		t.Fatalf("ledger entry %+v", e)
	}

	// j moves selection while sidebar is focused; preview follows.
	h.key("j")
	if h.m.showSel != 1 {
		t.Fatalf("showSel=%d", h.m.showSel)
	}
	if off := h.m.showView.YOffset; off != 70-rh/2 {
		t.Fatalf("YOffset=%d want %d", off, 70-rh/2)
	}

	// Enter focuses content; now j scrolls instead of selecting.
	h.key("enter")
	if h.m.focus != FocusContent {
		t.Fatal("enter should focus content")
	}
	before := h.m.showView.YOffset
	h.key("j")
	if h.m.showSel != 1 || h.m.showView.YOffset != before+1 {
		t.Fatalf("sel=%d off=%d (before %d)", h.m.showSel, h.m.showView.YOffset, before)
	}
	// Esc returns to the list without leaving show mode.
	h.key("esc")
	if h.m.focus != FocusSidebar || h.m.mode != ModeShow {
		t.Fatalf("focus=%v mode=%v", h.m.focus, h.m.mode)
	}
}

func TestMouseSelectsSidebarRowsAndScrollsContent(t *testing.T) {
	h := newHarness(t)
	h.update(tea.WindowSizeMsg{Width: 120, Height: 12})
	p := h.writeFile(t, "a.go", 100)
	var locs []hooks.Location
	for i := 1; i <= 20; i++ {
		locs = append(locs, hooks.Location{Path: p, Line: i * 4, Text: fmt.Sprint(i)})
	}
	h.hook(hooks.Event{Type: "show", Locations: locs})

	// Sidebar rows begin below the workstream strip and Show heading.
	h.mouse(2, len(h.m.streams)+5, tea.MouseButtonLeft)
	if h.m.showSel != 1 || h.m.focus != FocusSidebar {
		t.Fatalf("sidebar click: sel=%d focus=%v", h.m.showSel, h.m.focus)
	}

	// Wheel movement follows the list and keeps the selected row visible.
	for range 8 {
		h.mouse(2, len(h.m.streams)+5, tea.MouseButtonWheelDown)
	}
	if h.m.showSel != 19 || h.m.showOffset == 0 {
		t.Fatalf("sidebar wheel: sel=%d offset=%d", h.m.showSel, h.m.showOffset)
	}
	if !strings.Contains(stripANSI(h.m.View()), "20 ") {
		t.Fatal("selected location should remain visible after scrolling")
	}

	// Clicking and wheeling the content pane focuses and scrolls that viewport.
	h.mouse(h.m.sidebarWidth+2, 2, tea.MouseButtonLeft)
	if h.m.focus != FocusContent {
		t.Fatal("content click should focus content")
	}
	before := h.m.showView.YOffset
	h.mouse(h.m.sidebarWidth+2, 2, tea.MouseButtonWheelDown)
	if h.m.showView.YOffset <= before {
		t.Fatalf("content wheel did not scroll: before=%d after=%d", before, h.m.showView.YOffset)
	}
	after := h.m.showView.YOffset
	h.hook(hooks.Event{Type: "idle"})
	if h.m.showView.YOffset != after {
		t.Fatalf("unrelated refresh reset scroll: before=%d after=%d", after, h.m.showView.YOffset)
	}
}

func TestMouseControlsPromptAndQuitDialog(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	if err := exec.Command("git", "-C", h.root, "branch", "feat/mouse").Run(); err != nil {
		t.Fatal(err)
	}
	h.m.refreshRepo()
	h.update(EscapeMsg{})
	h.key("w")
	rw, _ := h.m.rightInner()
	matchRows := h.m.renderMatches(rw)
	firstRow := 1 + len(h.m.renderPrompt(rw)) - len(matchRows)
	h.mouse(h.m.sidebarWidth+4, firstRow+1, tea.MouseButtonLeft)
	if h.m.prompting || h.m.name != "feat/mouse" {
		t.Fatalf("prompt match click: prompting=%v name=%q", h.m.prompting, h.m.name)
	}

	h.update(QuitMsg{})
	contentX := h.m.sidebarWidth + 1
	rows := h.m.renderQuitPrompt(rw)
	for y, row := range rows {
		if x, _, ok := labelCellBounds(stripANSI(row), "n no"); ok {
			h.mouse(contentX+x, y+1, tea.MouseButtonLeft)
			break
		}
	}
	if h.m.confirmQuit {
		t.Fatal("clicking no should close quit confirmation")
	}
}

func TestMouseSelectsWorktreeBaseChoice(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.update(EscapeMsg{})
	h.key("w")
	for _, r := range "feat/from-mouse" {
		h.key(string(r))
	}
	h.key("enter")
	if h.m.promptStage != stageIdentity {
		t.Fatal("new branch should be named first")
	}
	// Clicking the description row focuses that field; enter continues.
	rw0, _ := h.m.rightInner()
	for y, row := range h.m.renderPrompt(rw0) {
		if strings.Contains(stripANSI(row), "description") {
			h.mouse(h.m.sidebarWidth+4, y+1, tea.MouseButtonLeft)
			break
		}
	}
	if h.m.field != 1 {
		t.Fatalf("click should focus description, field=%d", h.m.field)
	}
	h.key("enter")
	if h.m.promptStage != stageBase {
		t.Fatal("new branch should reach base selection")
	}

	rw, _ := h.m.rightInner()
	contentX := h.m.sidebarWidth + 1
	label := "c " + h.m.repo.Branch + " (current)"
	clicked := false
	for y, row := range h.m.renderPrompt(rw) {
		if x, _, ok := labelCellBounds(stripANSI(row), label); ok {
			h.mouse(contentX+x, y+1, tea.MouseButtonLeft)
			clicked = true
			break
		}
	}
	if !clicked {
		t.Fatalf("base choice %q was not rendered", label)
	}
	if h.m.prompting || h.m.name != "feat/from-mouse" {
		t.Fatalf("base click: prompting=%v name=%q", h.m.prompting, h.m.name)
	}
}

func TestMouseSwitchesWorkstreams(t *testing.T) {
	h := twoStreams(t)
	h.update(EscapeMsg{})
	h.mouse(2, 2, tea.MouseButtonLeft)
	if h.m.cur != 0 || !h.m.normal() {
		t.Fatalf("workstream click: cur=%d mode=%v focus=%v", h.m.cur, h.m.mode, h.m.focus)
	}
}

func TestMouseInLivePaneIsForwardedWithLocalCoordinates(t *testing.T) {
	h := newHarness(t)
	msg := tea.MouseMsg{X: h.m.sidebarWidth + 4, Y: 6, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	h.update(msg)
	contentX := h.m.sidebarWidth + 1
	got := mouseEvent(msg, msg.X-contentX, msg.Y-1)
	if got.X != 3 || got.Y != 5 || got.Button != 1 || got.Action != terminal.MousePress {
		t.Fatalf("translated mouse event = %+v", got)
	}
	if h.m.focus != FocusContent {
		t.Fatal("live pane mouse event should keep content focused")
	}
}

func TestInvalidShowIsRejectedAndKeepsPreviousSet(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "ok.go", 5)
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 2}}})
	first := h.m.showSet
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: "missing.go", Line: 1}}})
	if h.m.showSet != first || h.m.notice == "" {
		t.Fatalf("set replaced or no notice: %q", h.m.notice)
	}
	if err := ValidateShow(h.root, hooks.Event{Locations: []hooks.Location{{Path: "missing.go", Line: 1}}}); err == nil {
		t.Fatal("ValidateShow should reject missing file")
	}
}

func TestReferencePastesIntoChildAndReturnsToChat(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "ref.go", 10)
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 4, Column: 2, Text: "why"}}})
	h.key("r")
	if h.m.mode != ModeInteractive {
		t.Fatal("r should return to chat")
	}
	want := "[ref.go:4:2 — why]"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		screen := strings.Join(h.m.term.Snapshot(false), "\n")
		if strings.Contains(stripANSI(screen), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("reference %q never reached the child", want)
}

func TestDiffModeShowsBaselineDiffAndHunkReference(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "m.go", 20)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(data), "line xxx\n", "CHANGED\n", 1)), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})

	h.update(EscapeMsg{})
	h.key("d")
	if h.m.diffPath != "m.go" || len(h.m.diffRes.Hunks) != 1 {
		t.Fatalf("diffPath=%q hunks=%d note=%q", h.m.diffPath, len(h.m.diffRes.Hunks), h.m.diffRes.Note)
	}
	if !strings.Contains(stripANSI(h.m.diffView.View()), "+CHANGED") {
		t.Fatal("diff view missing added line")
	}
	h.key("r")
	if h.m.mode != ModeInteractive {
		t.Fatal("r should return to chat")
	}
	want := "[m.go:1-6 — current session diff]" // line 3 changed, 3 lines of context
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stripANSI(strings.Join(h.m.term.Snapshot(false), "\n")), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("reference %q never reached the child; screen:\n%s", want, stripANSI(strings.Join(h.m.term.Snapshot(false), "\n")))
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && (r == 'm' || r == '~'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

const goFile = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := 1\n\tfmt.Println(\"hi\", x)\n}\n"

func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestDiffViewIsSyntaxHighlightedAndTextFaithful(t *testing.T) {
	withTrueColor(t)
	h := newHarness(t)
	p := filepath.Join(h.root, "prog.go")
	os.WriteFile(p, []byte(goFile), 0o644)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	edited := strings.Replace(goFile, "\tx := 1\n", "\tx := 2\n\ty := \"new\"\n", 1)
	os.WriteFile(p, []byte(edited), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.update(EscapeMsg{})
	h.key("d")

	if len(h.m.diffOld) == 0 || len(h.m.diffNew) == 0 {
		t.Fatalf("highlighted sides missing: old=%d new=%d", len(h.m.diffOld), len(h.m.diffNew))
	}
	out := strings.Split(renderDiff(h.m.diffRes, h.m.diffOld, h.m.diffNew, "", 200), "\n")
	if len(out) != len(h.m.diffRes.Lines) {
		t.Fatalf("rendered %d lines for %d diff lines", len(out), len(h.m.diffRes.Lines))
	}
	refs := diff.Annotate(h.m.diffRes.Lines)
	sawColour, sawTint := false, false
	for i, raw := range h.m.diffRes.Lines {
		switch refs[i].Kind {
		case diff.KindAdd, diff.KindDel, diff.KindContext:
			want := strings.TrimRight(raw[:1]+expandTabs(raw[1:]), " ")
			got := strings.TrimRight(stripANSI(out[i]), " ")
			if got != want {
				t.Errorf("line %d: got %q want %q", i, got, want)
			}
			if strings.Contains(out[i], "38;2;") {
				sawColour = true
			}
			if refs[i].Kind != diff.KindContext && strings.Contains(out[i], "48;2;") {
				sawTint = true
			}
		}
	}
	if !sawColour || !sawTint {
		t.Fatalf("colour=%v tint=%v; diff lines were not highlighted", sawColour, sawTint)
	}
	// Hunk navigation indices still line up with the rendered content.
	if !strings.HasPrefix(stripANSI(out[h.m.diffRes.Hunks[0].Index]), theme.Sep+theme.Sep+" @@") {
		t.Fatalf("hunk header row = %q", stripANSI(out[h.m.diffRes.Hunks[0].Index]))
	}
}

func TestShowViewHighlightsSourceAndMarksTarget(t *testing.T) {
	withTrueColor(t)
	h := newHarness(t)
	p := filepath.Join(h.root, "prog.go")
	os.WriteFile(p, []byte(goFile), 0o644)
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 7, Column: 6, Text: "the print"}}})
	if len(h.m.showHL) != len(h.m.showLines) || len(h.m.showLines) != 8 {
		t.Fatalf("showHL=%d showLines=%d", len(h.m.showHL), len(h.m.showLines))
	}
	out := strings.Split(renderSource(h.m.showHL, h.m.showErr, h.m.showSet.Locations[0], 1, 1, 200), "\n")
	if len(out) < 9 { // header + 8 lines (+ the note float after line 7)
		t.Fatalf("rows=%d", len(out))
	}
	for i, src := range h.m.showLines[:7] { // rows after the target are followed by the float
		row := stripANSI(out[i+1])
		if !strings.HasSuffix(strings.TrimRight(row, " "), src) {
			t.Errorf("row %d %q does not end with source %q", i+1, row, src)
		}
	}
	target := out[7]
	if !strings.Contains(target, "\x1b[7m") && !strings.Contains(target, ";7m") {
		t.Fatalf("target column not reverse-highlighted: %q", target)
	}
	if !strings.Contains(target, "38;2;") {
		t.Fatalf("target line lost syntax colour: %q", target)
	}
	// The status bar carries the badge and one Neovim-style mode indicator.
	status := stripANSI(h.m.renderStatus())
	for _, want := range []string{theme.Badge, " SHOW·1 ", "plugin"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q missing %q", status, want)
		}
	}
	for _, unwanted := range []string{" s SHOW", "INTERACTIVE", "DIFF", "h/l"} {
		if strings.Contains(status, unwanted) {
			t.Errorf("status %q contains redundant item %q", status, unwanted)
		}
	}
	if strings.Contains(status, "the print") {
		t.Fatalf("note should live in the float, not the status bar: %q", status)
	}
}

func TestShowNoteRendersAsDiagnosticFloatUnderTarget(t *testing.T) {
	h := newHarness(t)
	p := filepath.Join(h.root, "prog.go")
	os.WriteFile(p, []byte(goFile), 0o644)
	note := "this call prints the greeting and is the only side effect in the program, so it is the natural place to hook logging"
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{
		{Path: p, Line: 7, Column: 6, Text: note},
		{Path: p, Line: 1, Text: "second"},
	}})
	out := strings.Split(stripANSI(renderSource(h.m.showHL, h.m.showErr, h.m.showSet.Locations[0], 1, 2, 80)), "\n")
	// Rows 1..7 are still source lines 1..7 (float is inserted after the target).
	if !strings.Contains(out[7], "fmt.Println") {
		t.Fatalf("row 7 = %q, target line moved", out[7])
	}
	// Float: top border, wrapped note with the info icon, footer, bottom border, then line 8.
	if !strings.Contains(out[8], theme.FloatTL) || !strings.Contains(out[8], theme.FloatTR) {
		t.Fatalf("row 8 should be the float's top border: %q", out[8])
	}
	body := strings.Join(out[9:], "\n")
	if !strings.Contains(body, theme.IconInfo) || !strings.Contains(body, "natural place") || !strings.Contains(body, "prog.go:7:6") || !strings.Contains(body, "1/2") {
		t.Fatalf("float body missing content:\n%s", body)
	}
	end := -1
	for i := 9; i < len(out); i++ {
		if strings.Contains(out[i], theme.FloatBL) {
			end = i
			break
		}
	}
	if end < 0 || !strings.Contains(out[end+1], "}") || end-9 < 3 {
		t.Fatalf("float not closed / long note not wrapped (end=%d):\n%s", end, strings.Join(out, "\n"))
	}
	for i := 8; i <= end; i++ {
		if w := lipgloss.Width(out[i]); w > 80 {
			t.Fatalf("float row %d overflows: %d > 80: %q", i, w, out[i])
		}
	}
	// Centering still lands on the target line, which the float does not shift.
	_, rh := h.m.rightInner()
	if h.m.showView.YOffset != 7-rh/2 && !(7-rh/2 < 0 && h.m.showView.YOffset == 0) {
		t.Fatalf("YOffset=%d", h.m.showView.YOffset)
	}
}

func TestDiffReasonFloatKeepsHunkNavigationAligned(t *testing.T) {
	h := newHarness(t)
	h.update(tea.WindowSizeMsg{Width: 120, Height: 16}) // small enough that the diff scrolls
	p := filepath.Join(h.root, "prog.go")
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("stmt%d := %d", i, i))
	}
	lines = append(lines, "")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	for _, n := range []int{3, 30, 60, 90, 120} {
		lines[n] = "CHANGED"
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	// The agent explains the file via show_locations -> ledger reason.
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 1, Text: "explains the change"}}})
	h.key("d")
	if h.m.diffPad == 0 {
		t.Fatal("expected a reason float to pad the diff")
	}
	rows := strings.Split(stripANSI(h.m.diffView.View()), "\n")
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "explains the change") || !strings.Contains(joined, theme.FloatTL) {
		t.Fatalf("reason float missing:\n%s", joined)
	}
	if len(h.m.diffRes.Hunks) != 5 {
		t.Fatalf("hunks=%d", len(h.m.diffRes.Hunks))
	}
	h.key("enter")
	h.key("]") // top -> hunk 0
	h.key("]") // -> hunk 1
	full := strings.Split(stripANSI(renderDiff(h.m.diffRes, h.m.diffOld, h.m.diffNew, "explains the change", 200)), "\n")
	if !strings.HasPrefix(full[h.m.diffView.YOffset], theme.Sep+theme.Sep+" @@") {
		t.Fatalf("] did not land on a hunk header; row %d = %q", h.m.diffView.YOffset, full[h.m.diffView.YOffset])
	}
	if h.m.diffRes.HunkAt(h.m.diffView.YOffset-h.m.diffPad) != 1 {
		t.Fatalf("expected to be on hunk 1, YOffset=%d pad=%d", h.m.diffView.YOffset, h.m.diffPad)
	}
	// Note-only results (nothing to diff) render as a warn float too.
	note := stripANSI(renderDiff(diff.Result{Path: "x.go", Note: "not modified during this session"}, nil, nil, "show note", 60))
	if !strings.Contains(note, theme.IconWarn) || !strings.Contains(note, theme.FloatBL) {
		t.Fatalf("note float missing: %q", note)
	}
	if strings.Contains(note, "show note") {
		t.Fatalf("unmodified file must not show a change reason: %q", note)
	}
	// With the current source available, it is listed under the float.
	src := stripANSI(renderDiff(diff.Result{Path: "x.go", Note: "not modified during this session"}, nil, highlight.File("x.go", "package x\n\nfunc F() {}\n"), "", 60))
	if !strings.Contains(src, "1 │ package x") || !strings.Contains(src, "3 │ func F() {}") {
		t.Fatalf("source not shown for unmodified file:\n%s", src)
	}
}

func TestZoomHidesSidebarAndGivesChildFullWidth(t *testing.T) {
	h := newHarness(t)
	w0, _ := h.m.rightInner()
	h.update(ZoomMsg{})
	if !h.m.zoom {
		t.Fatal("zoom should toggle on")
	}
	w1, _ := h.m.rightInner()
	if w1 != w0+h.m.sidebarWidth {
		t.Fatalf("zoomed width %d, want %d", w1, w0+h.m.sidebarWidth)
	}
	view := stripANSI(h.m.View())
	if strings.Contains(view, "Files") || strings.Contains(view, "╮╭") {
		t.Fatal("sidebar still visible while zoomed")
	}
	if !strings.Contains(stripANSI(h.m.renderStatus()), theme.IconZoom) {
		t.Fatal("status bar should indicate zoom")
	}
	// z in a LazyAI mode toggles it back.
	h.update(EscapeMsg{})
	h.key("z")
	if h.m.zoom {
		t.Fatal("z should toggle zoom off")
	}
	if w, _ := h.m.rightInner(); w != w0 {
		t.Fatalf("width after unzoom %d, want %d", w, w0)
	}
}

func TestShowBracketKeysAndDigitsJumpLocations(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "a.go", 100)
	var locs []hooks.Location
	for i := 1; i <= 4; i++ {
		locs = append(locs, hooks.Location{Path: p, Line: i * 10, Text: fmt.Sprint("n", i)})
	}
	h.hook(hooks.Event{Type: "show", Locations: locs})
	h.key("]")
	h.key("]")
	if h.m.showSel != 2 {
		t.Fatalf("]] -> sel %d", h.m.showSel)
	}
	h.key("[")
	if h.m.showSel != 1 {
		t.Fatalf("[ -> sel %d", h.m.showSel)
	}
	h.key("enter") // content focus: ] still cycles locations in Show mode
	h.key("]")
	if h.m.showSel != 2 || h.m.focus != FocusContent {
		t.Fatalf("] in content -> sel %d focus %v", h.m.showSel, h.m.focus)
	}
	_, rh := h.m.rightInner()
	if h.m.showView.YOffset != 30-rh/2 {
		t.Fatalf("preview did not follow: YOffset=%d", h.m.showView.YOffset)
	}
	h.key("esc")
	h.key("4")
	if h.m.showSel != 3 {
		t.Fatalf("4 -> sel %d", h.m.showSel)
	}
	h.key("9") // out of range: clamp to last
	if h.m.showSel != 3 {
		t.Fatalf("9 -> sel %d", h.m.showSel)
	}
	h.key("1")
	if h.m.showSel != 0 {
		t.Fatalf("1 -> sel %d", h.m.showSel)
	}
}

func TestHelpFloatAndModeIndicator(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "a.go", 30)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(data), "line xxx\n", "CHANGED\n", 1)), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 2, Text: "x"}, {Path: p, Line: 3, Text: "y"}}})
	status := stripANSI(h.m.renderStatus())
	if !strings.Contains(status, "SHOW·2") || strings.Contains(status, "DIFF·1") || strings.Contains(status, "s SHOW") {
		t.Fatalf("status should show only the active mode without a key prefix: %q", status)
	}
	h.key("?")
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "jk") || !strings.Contains(view, "ctrl+z") || !strings.Contains(view, theme.FloatTL) {
		t.Fatalf("help float missing:\n%s", view)
	}
	h.key("?")
	if strings.Contains(stripANSI(h.m.View()), "ctrl+z") {
		t.Fatal("help should toggle off")
	}
	h.key("esc") // Show -> Normal
	if status := stripANSI(h.m.renderStatus()); !strings.Contains(status, " NORMAL ") || strings.Contains(status, "i INTERACTIVE") {
		t.Fatalf("normal mode indicator: %q", status)
	}
	h.key("?")
	view = stripANSI(h.m.View())
	if !strings.Contains(view, "h / l previous / next") {
		t.Fatalf("normal help should contain hidden navigation keys:\n%s", view)
	}
	h.key("esc")
	if h.m.help {
		t.Fatal("esc should close help")
	}
}

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "init", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

func TestWorktreePromptCreatesWorktreeAndWorkstream(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	if !strings.Contains(stripANSI(h.m.renderStatus()), theme.IconBranch+" main") {
		t.Fatalf("branch missing from status: %q", stripANSI(h.m.renderStatus()))
	}
	h.update(EscapeMsg{}) // diff mode
	h.key("w")
	if !h.m.prompting {
		t.Fatal("w should open the worktree prompt")
	}
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "workstream") || !strings.Contains(view, theme.FloatTL) {
		t.Fatalf("prompt float missing:\n%s", view)
	}
	for _, r := range "feat/x" {
		h.key(string(r))
	}
	h.key("j") // plain letters must go to the input, not navigation
	h.key("enter")
	// Identity stage: nickname prefilled with the branch, description optional.
	if h.m.promptStage != stageIdentity || h.m.nick.Value() != "feat/xj" || h.m.field != 0 {
		t.Fatalf("identity stage: stage=%v nick=%q field=%d", h.m.promptStage, h.m.nick.Value(), h.m.field)
	}
	view = stripANSI(h.m.View())
	if !strings.Contains(view, "nickname") || !strings.Contains(view, "description") {
		t.Fatalf("identity form missing fields:\n%s", view)
	}
	h.key("tab")
	for _, r := range "search index" {
		h.key(string(r))
	}
	h.key("enter")
	h.key("m") // new branch: base it on main
	if h.m.prompting {
		t.Fatal("prompt should close after choosing the base")
	}
	want := filepath.Join(realpath(h.root), ".worktrees", "feat-xj")
	if len(h.m.streams) != 2 || h.m.cur != 1 || h.m.root != want || h.m.name != "feat/xj" {
		t.Fatalf("streams=%d cur=%d root=%q name=%q", len(h.m.streams), h.m.cur, h.m.root, h.m.name)
	}
	if h.m.nickname != "feat/xj" || h.m.description != "search index" {
		t.Fatalf("identity not applied: nick=%q desc=%q", h.m.nickname, h.m.description)
	}
	if v := stripANSI(h.m.View()); !strings.Contains(v, "search index") {
		t.Fatalf("strip detail row should show the description:\n%s", v)
	}
	if _, err := os.Stat(filepath.Join(want, "a.go")); err != nil {
		t.Fatal("worktree not checked out")
	}
	if len(h.children) != 2 || h.sinks[len(h.sinks)-1] != input.Sink(h.children[1]) {
		t.Fatalf("keyboard not retargeted to the new child: children=%d", len(h.children))
	}
	if h.m.mode != ModeInteractive || h.forward[len(h.forward)-1] != true {
		t.Fatalf("new workstream should start interactive, mode=%v fwd=%v", h.m.mode, h.forward)
	}
	if !strings.Contains(h.m.notice, "feat/xj") {
		t.Fatalf("notice=%q", h.m.notice)
	}
	// Prompting the same branch again just switches to it.
	h.update(EscapeMsg{})
	h.key("w")
	for _, r := range "feat/xj" {
		h.key(string(r))
	}
	h.key("enter")
	if len(h.m.streams) != 2 || h.m.cur != 1 {
		t.Fatalf("should reuse the workstream: streams=%d cur=%d", len(h.m.streams), h.m.cur)
	}
	// Esc cancels without side effects; empty enter is rejected.
	h.update(EscapeMsg{})
	h.key("w")
	h.key("esc")
	if h.m.prompting || len(h.m.streams) != 2 {
		t.Fatal("esc should cancel")
	}
	h.key("w")
	h.key("enter")
	if !h.m.prompting || h.m.notice == "" {
		t.Fatalf("empty name should keep prompt open with a notice, prompting=%v notice=%q", h.m.prompting, h.m.notice)
	}
}

// twoStreams builds a harness with two workstreams (plain dirs, no git).
func twoStreams(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	second := t.TempDir()
	if _, err := h.m.addStream(second, "second"); err != nil {
		t.Fatal(err)
	}
	if len(h.m.streams) != 2 || h.m.cur != 1 {
		t.Fatalf("streams=%d cur=%d", len(h.m.streams), h.m.cur)
	}
	return h
}

func TestWorkstreamNavigationLeaderAndHL(t *testing.T) {
	h := twoStreams(t)
	// Leader from interactive mode: Ctrl+Space then 1.
	h.update(LeaderMsg{})
	h.key("1")
	if h.m.cur != 0 {
		t.Fatalf("leader 1 -> cur %d", h.m.cur)
	}
	if last := h.sinks[len(h.sinks)-1]; last != input.Sink(h.children[0]) {
		t.Fatal("keyboard not retargeted to stream 1")
	}
	h.update(LeaderMsg{})
	h.key("l")
	if h.m.cur != 1 {
		t.Fatalf("leader l -> cur %d", h.m.cur)
	}
	h.update(LeaderMsg{})
	h.key("l") // wraps
	if h.m.cur != 0 {
		t.Fatalf("leader l wrap -> cur %d", h.m.cur)
	}
	h.update(LeaderMsg{})
	h.update(LeaderMsg{}) // Ctrl+Space Ctrl+Space -> last
	h.key("ctrl+@")
	if h.m.cur != 1 {
		t.Fatalf("leader leader -> cur %d", h.m.cur)
	}
	h.update(LeaderMsg{})
	h.key("9")
	if h.m.cur != 1 || h.m.notice == "" {
		t.Fatalf("leader 9 should notice, cur=%d notice=%q", h.m.cur, h.m.notice)
	}
	// Each stream remembers its state: stream 2 focused-out (normal), stream
	// 1 still focused; browsing with h lands on 1 in normal (faded, no input).
	h.update(EscapeMsg{})
	if !h.m.normal() {
		t.Fatal("expected normal")
	}
	h.key("h")
	if h.m.cur != 0 || !h.m.normal() || h.forward[len(h.forward)-1] != false {
		t.Fatalf("h -> cur %d mode %v focus %v", h.m.cur, h.m.mode, h.m.focus)
	}
	h.key("l")
	if h.m.cur != 1 || !h.m.normal() {
		t.Fatalf("l -> cur %d mode %v focus %v", h.m.cur, h.m.mode, h.m.focus)
	}
	// Enter focuses OpenCode; in content focus h is typed into it, not a switch.
	h.key("enter")
	if h.m.focus != FocusContent || h.forward[len(h.forward)-1] != true {
		t.Fatal("enter should focus OpenCode")
	}
	h.update(EscapeMsg{})
	// g in normal is a no-op that must not switch streams.
	h.key("g")
	if h.m.cur != 1 {
		t.Fatal("g must not switch streams")
	}
	// Sidebar strip lists both with the current one and numbers.
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "Workstreams") || !strings.Contains(view, "2 second") {
		t.Fatalf("strip missing:\n%s", view)
	}
	if !strings.Contains(stripANSI(h.m.renderStatus()), "2/2") {
		t.Fatalf("status lacks stream position: %q", stripANSI(h.m.renderStatus()))
	}
}

func TestLeaderWFromInteractiveOwnsKeyboardForPrompt(t *testing.T) {
	h := newHarness(t)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	if h.m.mode != ModeInteractive {
		t.Fatal("start interactive")
	}
	h.update(LeaderMsg{})
	h.key("w")
	if !h.m.prompting || h.forward[len(h.forward)-1] != false {
		t.Fatalf("prompt must take the keyboard: prompting=%v fwd=%v", h.m.prompting, h.forward)
	}
	h.key("esc")
	if h.m.prompting || h.forward[len(h.forward)-1] != true {
		t.Fatalf("esc must close and give keys back: prompting=%v fwd=%v", h.m.prompting, h.forward)
	}
	h.update(LeaderMsg{})
	h.key("w")
	h.update(EscapeMsg{}) // a raw ESC also cancels instead of switching modes
	if h.m.prompting || h.m.mode != ModeInteractive {
		t.Fatalf("raw esc: prompting=%v mode=%v", h.m.prompting, h.m.mode)
	}
}

func TestHookEventsRouteToTheirStreamAndFlagAttention(t *testing.T) {
	h := twoStreams(t)
	first := h.m.streams[0]
	p := filepath.Join(first.root, "x.go")
	os.WriteFile(p, []byte(goFile), 0o644)
	// Events from stream 1's plugin while stream 2 is current.
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "tool.before", Tool: "read"}})
	if !first.working() || h.m.working() {
		t.Fatalf("working flag misrouted: first=%v cur=%v", first.working(), h.m.working())
	}
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "file.read", Path: p}})
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "tool.after", Tool: "read"}})
	if first.working() || first.ledger.Len() != 1 || h.m.ledger.Len() != 0 {
		t.Fatalf("ledger misrouted: first=%d cur=%d", first.ledger.Len(), h.m.ledger.Len())
	}
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "show", Locations: []hooks.Location{{Path: p, Line: 2, Text: "look"}}}})
	if h.m.cur != 1 || !first.unseen || first.showSet == nil {
		t.Fatalf("show on a background stream must not steal focus: cur=%d unseen=%v set=%v", h.m.cur, first.unseen, first.showSet != nil)
	}
	if !strings.Contains(stripANSI(h.m.View()), theme.Unseen) {
		t.Fatal("strip should flag the background stream as unseen")
	}
	// Further agent activity must not clear a pending show; only visiting does.
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "tool.before", Tool: "read"}})
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "tool.after", Tool: "read"}})
	if !first.unseen {
		t.Fatal("unseen flag cleared by unrelated tool activity")
	}
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "attention"}})
	if !strings.Contains(stripANSI(h.m.View()), theme.Attention) {
		t.Fatal("attention outranks unseen in the strip")
	}
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "tool.before", Tool: "edit"}})
	if first.attention {
		t.Fatal("a permission attention is resolved once a tool runs")
	}
	h.update(LeaderMsg{})
	h.key("1")
	if !h.m.normal() || h.m.unseen || h.m.showSet == nil {
		t.Fatalf("switching lands in normal with the set ready: mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	h.key("s")
	if h.m.mode != ModeShow {
		t.Fatal("s should open the prepared show set")
	}
	// Unknown tokens are ignored.
	h.update(HookMsg{Event: hooks.Event{Token: "nope", Type: "hello"}})
}

func TestCloseAndExitRemoveStreamsAndLastOneQuits(t *testing.T) {
	h := twoStreams(t)
	h.update(EscapeMsg{})
	h.key("x")
	if len(h.m.streams) != 2 || !strings.Contains(h.m.notice, "press x again") {
		t.Fatalf("first x should only ask: notice=%q", h.m.notice)
	}
	h.key("j") // anything else cancels the pending close
	h.key("x")
	if len(h.m.streams) != 2 {
		t.Fatal("cancelled close should need two x again")
	}
	h.key("x")
	if len(h.m.streams) != 1 || h.m.cur != 0 || h.m.name == "second" {
		t.Fatalf("x x should close: streams=%d cur=%d name=%q", len(h.m.streams), h.m.cur, h.m.name)
	}
	// The remaining child's exit quits the program.
	mm, cmd := h.m.Update(ChildExitedMsg{Token: h.m.token})
	h.m = mm.(Model)
	if cmd == nil {
		t.Fatal("last child exiting should quit")
	}
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func TestEverySwitchLandsOnTheAgentPaneInNormal(t *testing.T) {
	h := twoStreams(t) // cur = 2, focused
	h.update(EscapeMsg{})
	h.key("t") // stream 2 in its terminal, focused
	if h.m.mode != ModeTerminal {
		t.Fatal("expected terminal")
	}
	h.update(LeaderMsg{})
	h.key("h") // from a focused pane too: land in normal on the agent pane
	if h.m.cur != 0 || !h.m.normal() || h.forward[len(h.forward)-1] != false {
		t.Fatalf("cur=%d mode=%v focus=%v", h.m.cur, h.m.mode, h.m.focus)
	}
	h.key("l") // back to stream 2: agent pane, normal (not its terminal)
	if h.m.cur != 1 || !h.m.normal() || h.m.shell == nil {
		t.Fatalf("cur=%d mode=%v focus=%v shell=%v", h.m.cur, h.m.mode, h.m.focus, h.m.shell != nil)
	}
	h.key("t") // the shell is still there
	if h.m.mode != ModeTerminal || h.m.focus != FocusContent {
		t.Fatal("t should refocus the kept shell")
	}
	h.update(EscapeMsg{})
	h.key("i")
	if h.m.mode != ModeInteractive || h.m.focus != FocusContent || h.forward[len(h.forward)-1] != true {
		t.Fatal("i should focus OpenCode")
	}
}

func hintsOf(h *harness) string { return stripANSI(h.m.renderHint()) }

func TestEscGoesToNormalAndDiffIsGatedUntilChanges(t *testing.T) {
	h := newHarness(t)
	h.update(EscapeMsg{})
	if h.m.mode != ModeInteractive || h.m.focus != FocusSidebar || !h.m.normal() {
		t.Fatalf("esc should land in normal (opencode focused out): mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	if h.forward[len(h.forward)-1] != false {
		t.Fatal("normal mode must not forward keys")
	}
	if strings.Contains(hintsOf(h), "i:opencode") || strings.Contains(hintsOf(h), "h/l") {
		t.Fatalf("normal status should leave mode and navigation keys to help: %q", hintsOf(h))
	}
	h.key("d")
	if h.m.mode != ModeInteractive || h.m.notice == "" {
		t.Fatalf("d without changes must stay put with a notice: mode=%v notice=%q", h.m.mode, h.m.notice)
	}
	if !strings.Contains(stripANSI(h.m.renderStatus()), " NORMAL ") {
		t.Fatal("failed mode switch should leave the normal indicator")
	}
	// A change unlocks diff.
	p := h.writeFile(t, "a.go", 5)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	os.WriteFile(p, []byte("changed\n"), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.key("d")
	if h.m.mode != ModeDiff {
		t.Fatal("d should enter diff once there are changes")
	}
	// Esc from the diff sidebar goes to normal, not into OpenCode.
	h.key("esc")
	if !h.m.normal() || h.forward[len(h.forward)-1] != false {
		t.Fatalf("esc from diff -> normal: mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	// i / enter step into OpenCode.
	h.key("i")
	if h.m.mode != ModeInteractive || h.m.focus != FocusContent || h.forward[len(h.forward)-1] != true {
		t.Fatal("i should focus OpenCode")
	}
	h.update(EscapeMsg{})
	h.key("enter")
	if h.m.focus != FocusContent || h.forward[len(h.forward)-1] != true {
		t.Fatal("enter should focus OpenCode")
	}
}

func TestTerminalModeStartsShellPerStreamAndFollowsFocusRules(t *testing.T) {
	h := newHarness(t)
	h.update(EscapeMsg{})
	h.key("t")
	if h.m.mode != ModeTerminal || h.m.focus != FocusContent || h.m.shell == nil {
		t.Fatalf("t should start and focus a shell: mode=%v focus=%v shell=%v", h.m.mode, h.m.focus, h.m.shell != nil)
	}
	if h.sinks[len(h.sinks)-1] != input.Sink(h.m.shell) || h.forward[len(h.forward)-1] != true {
		t.Fatal("keys must forward to the shell")
	}
	if !strings.Contains(hintsOf(h), "esc") || !strings.Contains(hintsOf(h), "jk") {
		t.Fatalf("terminal hints: %q", hintsOf(h))
	}
	shell := h.m.shell
	h.update(EscapeMsg{}) // focus out: faded shell, no input
	if h.m.mode != ModeTerminal || h.m.focus != FocusSidebar || h.forward[len(h.forward)-1] != false {
		t.Fatalf("esc in terminal -> normal-terminal: mode=%v focus=%v", h.m.mode, h.m.focus)
	}
	h.key("t") // back in, same shell
	if h.m.focus != FocusContent || h.m.shell != shell {
		t.Fatal("t should refocus the existing shell")
	}
	h.update(EscapeMsg{})
	h.key("i")
	if h.m.mode != ModeInteractive || h.sinks[len(h.sinks)-1] != input.Sink(h.m.term) {
		t.Fatal("i should return keys to OpenCode")
	}
	// Shell exit while it is on screen drops back to normal.
	h.key("t")
	h.update(ChildExitedMsg{Token: h.m.token, Shell: true})
	if h.m.shell != nil || h.m.mode != ModeInteractive || h.m.focus != FocusSidebar {
		t.Fatalf("shell exit -> normal: shell=%v mode=%v focus=%v", h.m.shell != nil, h.m.mode, h.m.focus)
	}
	if !strings.Contains(stripANSI(h.m.renderStatus()), " NORMAL ") {
		t.Fatal("normal indicator missing after shell exit")
	}
}

type fakeNotes struct{ recs []string }

func (f *fakeNotes) UpsertWorktree(string, string, string, bool) error        { return nil }
func (f *fakeNotes) SetWorktreeIdentity(string, string, string, string) error { return nil }
func (f *fakeNotes) SetDormant(string, string, bool) error                    { return nil }
func (f *fakeNotes) Worktrees(string) ([]notes.Worktree, error)               { return nil, nil }
func (f *fakeNotes) SetState(string, string, string) error                    { return nil }

func (f *fakeNotes) Record(root, branch, session string, set show.Set) error {
	f.recs = append(f.recs, fmt.Sprintf("%s|%s|%s|%d", filepath.Base(root), branch, set.Title, len(set.Locations)))
	return nil
}

func TestShowSetsArePersisted(t *testing.T) {
	h := newHarness(t)
	fn := &fakeNotes{}
	h.m.cfg.Notes = fn
	p := h.writeFile(t, "a.go", 5)
	h.hook(hooks.Event{Type: "show", Title: "tour", SessionID: "s1", Locations: []hooks.Location{{Path: p, Line: 1, Text: "x"}, {Path: p, Line: 2, Text: "y"}}})
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: "missing.go", Line: 1}}}) // rejected: not stored
	if len(fn.recs) != 1 || fn.recs[0] != filepath.Base(h.root)+"||tour|2" {
		t.Fatalf("recorded %v", fn.recs)
	}
}

func TestHintsTrackEveryScreen(t *testing.T) {
	h := newHarness(t)
	p := h.writeFile(t, "a.go", 20)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	os.WriteFile(p, []byte("changed\n"), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: p, Line: 1, Text: "x"}}})
	h.key("i")
	want := map[string][]string{}
	got := map[string]string{}
	got["interactive"] = hintsOf(h)
	want["interactive"] = []string{"esc:normal", "jk", "ctrl+q:detach"}
	h.update(EscapeMsg{})
	got["normal"] = hintsOf(h)
	want["normal"] = []string{"w:worktree", "?:help", "ctrl+q:detach"}
	h.key("d")
	got["diff-sidebar"] = hintsOf(h)
	want["diff-sidebar"] = []string{"j/k:file", "enter", "r:reference", "esc:normal", "ctrl+q:detach"}
	h.key("enter")
	got["diff-content"] = hintsOf(h)
	want["diff-content"] = []string{"[ ]:hunk", "esc:files", "ctrl+q:detach"}
	h.key("esc")
	h.key("s")
	got["show-sidebar"] = hintsOf(h)
	want["show-sidebar"] = []string{"j/k:location", "[ ]", "r:reference", "esc:normal", "ctrl+q:detach"}
	h.key("enter")
	got["show-content"] = hintsOf(h)
	want["show-content"] = []string{"j/k:scroll", "[ ]:location", "esc:list", "ctrl+q:detach"}
	h.key("esc")
	h.key("?")
	got["help"] = hintsOf(h)
	want["help"] = []string{"?", "close", "ctrl+q:detach"}
	h.key("?")
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.key("w")
	got["prompt"] = hintsOf(h)
	want["prompt"] = []string{"ctrl+q:detach"}
	h.key("esc")
	h.update(LeaderMsg{})
	got["leader"] = hintsOf(h)
	want["leader"] = []string{"1-9", "w", "x", "ctrl+q:detach"}
	for screen, needles := range want {
		for _, n := range needles {
			if !strings.Contains(got[screen], n) {
				t.Errorf("%s hints %q missing %q", screen, got[screen], n)
			}
		}
	}
	for _, screen := range []string{"normal", "diff-sidebar", "show-sidebar", "leader"} {
		if strings.Contains(got[screen], "h/l") {
			t.Errorf("%s should leave h/l in help: %q", screen, got[screen])
		}
	}
	// Nothing about hunks in show, nothing about locations in diff.
	if strings.Contains(got["show-content"], "hunk") || strings.Contains(got["diff-content"], "location") {
		t.Errorf("cross-talk: show=%q diff=%q", got["show-content"], got["diff-content"])
	}
}

func TestHelpExplainsSessionLifecycle(t *testing.T) {
	help := strings.Join(renderHelp(220), "\n")
	for _, want := range []string{"session lifecycle", "ctrl+q: detach", "lazyai list", "lazyai stop --dir"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q: %q", want, help)
		}
	}
}

// memStore is an in-memory Store for tests.
type memStore struct {
	fakeNotes
	wts   map[string]*notes.Worktree
	state map[string]string
}

func newMemStore() *memStore {
	return &memStore{wts: map[string]*notes.Worktree{}, state: map[string]string{}}
}
func (m *memStore) UpsertWorktree(repo, branch, path string, linked bool) error {
	if w, ok := m.wts[branch]; ok {
		w.Path, w.Linked, w.Dormant = path, linked, false
		return nil
	}
	m.wts[branch] = &notes.Worktree{Repo: repo, Branch: branch, Path: path, Linked: linked}
	return nil
}
func (m *memStore) SetWorktreeIdentity(repo, branch, nickname, description string) error {
	w, ok := m.wts[branch]
	if !ok {
		w = &notes.Worktree{Repo: repo, Branch: branch, Linked: true}
		m.wts[branch] = w
	}
	w.Nickname, w.Description = nickname, description
	return nil
}
func (m *memStore) SetDormant(repo, branch string, dormant bool) error {
	if w, ok := m.wts[branch]; ok {
		w.Dormant = dormant
	}
	return nil
}
func (m *memStore) Worktrees(repo string) ([]notes.Worktree, error) {
	var out []notes.Worktree
	for _, w := range m.wts {
		out = append(out, *w)
	}
	return out, nil
}
func (m *memStore) SetState(repo, key, value string) error { m.state[key] = value; return nil }

func TestArchiveMakesWorktreeDormantAndPromptWakesIt(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	st := newMemStore()
	h.m.cfg.Notes = st
	h.m.refreshRepo()
	h.m.name = h.m.repo.Branch // the repo was created after New in this test
	h.m.registerWorktree()     // the initial stream predates the store in this test
	h.update(EscapeMsg{})
	h.key("w")
	for _, r := range "feat/z" {
		h.key(string(r))
	}
	h.key("enter")
	// Name it: the nickname is persisted before launch and survives archive/wake.
	for range len("feat/z") {
		h.key("backspace")
	}
	for _, r := range "Zed" {
		h.key(string(r))
	}
	h.key("enter")
	h.key("m")
	if len(h.m.streams) != 2 || st.wts["feat/z"] == nil || !st.wts["feat/z"].Linked || st.wts["main"] == nil {
		t.Fatalf("registry: %+v streams=%d", st.wts, len(h.m.streams))
	}
	if st.state["last_branch"] != "feat/z" {
		t.Fatalf("last_branch=%q", st.state["last_branch"])
	}
	wt := h.m.root
	// a archives: stream gone, OpenCode stopped, worktree kept and marked dormant.
	h.update(EscapeMsg{})
	h.key("a")
	if len(h.m.streams) != 1 || h.m.name != "main" || !st.wts["feat/z"].Dormant {
		t.Fatalf("archive: streams=%d cur=%q dormant=%v", len(h.m.streams), h.m.name, st.wts["feat/z"].Dormant)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.go")); err != nil {
		t.Fatal("archiving must keep the worktree on disk")
	}
	if !strings.Contains(h.m.notice, "feat/z") {
		t.Fatalf("notice=%q", h.m.notice)
	}
	// The last workstream cannot be archived.
	h.key("a")
	if len(h.m.streams) != 1 || !strings.Contains(h.m.notice, "last") {
		t.Fatalf("last stream archived? streams=%d notice=%q", len(h.m.streams), h.m.notice)
	}
	// The prompt lists dormant worktrees; typing one wakes it (same path, no new branch).
	h.key("w")
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "dormant") || !strings.Contains(view, "feat/z") {
		t.Fatalf("prompt should list dormant worktrees:\n%s", view)
	}
	for _, r := range "feat/z" {
		h.key(string(r))
	}
	h.key("enter")
	if len(h.m.streams) != 2 || h.m.root != wt || st.wts["feat/z"].Dormant {
		t.Fatalf("wake: streams=%d root=%q dormant=%v", len(h.m.streams), h.m.root, st.wts["feat/z"].Dormant)
	}
	if h.m.nickname != "Zed" || st.wts["feat/z"].Nickname != "Zed" {
		t.Fatalf("identity lost across archive/wake: mem=%q db=%q", h.m.nickname, st.wts["feat/z"].Nickname)
	}
	if !strings.Contains(stripANSI(h.m.View()), "2 Zed") {
		t.Fatalf("strip should show the nickname:\n%s", stripANSI(h.m.View()))
	}
	// e renames the current workstream without touching git.
	h.update(EscapeMsg{})
	h.key("e")
	if !h.m.prompting || !h.m.editing || h.m.promptStage != stageIdentity || h.m.nick.Value() != "Zed" {
		t.Fatalf("e should open identity edit: prompting=%v editing=%v stage=%v nick=%q", h.m.prompting, h.m.editing, h.m.promptStage, h.m.nick.Value())
	}
	for range 3 {
		h.key("backspace")
	}
	for _, r := range "Zeta" {
		h.key(string(r))
	}
	h.key("tab")
	for _, r := range "greek letters" {
		h.key(string(r))
	}
	h.key("enter")
	if h.m.prompting || h.m.nickname != "Zeta" || h.m.description != "greek letters" || h.m.name != "feat/z" {
		t.Fatalf("rename: prompting=%v nick=%q desc=%q branch=%q", h.m.prompting, h.m.nickname, h.m.description, h.m.name)
	}
	if st.wts["feat/z"].Nickname != "Zeta" || st.wts["feat/z"].Description != "greek letters" {
		t.Fatalf("rename not persisted: %+v", st.wts["feat/z"])
	}
	// An empty nickname is rejected with the value kept for correction.
	h.key("e")
	for range 4 {
		h.key("backspace")
	}
	h.key("enter")
	if !h.m.prompting || h.m.notice == "" || h.m.nickname != "Zeta" {
		t.Fatalf("empty nickname must be rejected: prompting=%v notice=%q", h.m.prompting, h.m.notice)
	}
	h.key("esc")
	if h.m.prompting {
		t.Fatal("esc cancels the rename")
	}
}

func commitIn(t *testing.T, dir, file string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte("x\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", file}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
}

func openWorktreePrompt(h *harness, name string) {
	h.key("w")
	for _, r := range name {
		h.key(string(r))
	}
	h.key("enter")
}

// newWorktree drives the form for a brand-new branch: name, accept the
// prefilled nickname, pick the base.
func newWorktree(h *harness, name, base string) {
	openWorktreePrompt(h, name)
	h.key("enter")
	h.key(base)
}

func TestMainPinnedOnTopAndWorktreesAppendOnly(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.m.name = "main"
	h.update(EscapeMsg{})
	newWorktree(h, "feat/one", "m") // base: main
	newWorktree(h, "feat/two", "m")
	names := func() string {
		var out []string
		for _, s := range h.m.streams {
			out = append(out, s.name)
		}
		return strings.Join(out, ",")
	}
	if names() != "main,feat/one,feat/two" {
		t.Fatalf("order %s", names())
	}
	// Archive main, then wake it: it goes back to the top, others keep order.
	h.update(EscapeMsg{})
	h.key("h")
	h.key("h") // on main
	if h.m.name != "main" {
		t.Fatalf("expected main, on %s", h.m.name)
	}
	h.key("a")
	if names() != "feat/one,feat/two" {
		t.Fatalf("after archive %s", names())
	}
	openWorktreePrompt(h, "main")
	if names() != "main,feat/one,feat/two" || h.m.cur != 0 {
		t.Fatalf("after wake %s cur=%d", names(), h.m.cur)
	}
	// A new one is appended at the end even when created from main.
	h.update(EscapeMsg{})
	newWorktree(h, "feat/three", "m")
	if names() != "main,feat/one,feat/two,feat/three" {
		t.Fatalf("append %s", names())
	}
}

func TestNewWorktreePromptAsksForBaseBranch(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.m.name = "main"
	h.update(EscapeMsg{})
	openWorktreePrompt(h, "feat/one")
	if !h.m.prompting || h.m.promptStage != stageIdentity {
		t.Fatalf("should name the workstream first: prompting=%v stage=%v", h.m.prompting, h.m.promptStage)
	}
	h.key("enter")
	if h.m.promptStage != stageBase {
		t.Fatalf("should ask for the base: stage=%v", h.m.promptStage)
	}
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "m main") || !strings.Contains(view, "c main (current)") || hintsOf(h) != "ctrl+q:detach" {
		t.Fatalf("base prompt missing options:\n%s\n%s", view, hintsOf(h))
	}
	h.key("m")
	if h.m.prompting || h.m.name != "feat/one" {
		t.Fatalf("m should create off main: prompting=%v name=%q", h.m.prompting, h.m.name)
	}
	// Commit something only on feat/one, then branch feat/two off the *current* worktree.
	commitIn(t, h.m.root, "only-on-one.txt")
	h.update(EscapeMsg{})
	newWorktree(h, "feat/two", "c")
	if h.m.name != "feat/two" {
		t.Fatalf("on %q", h.m.name)
	}
	if _, err := os.Stat(filepath.Join(h.m.root, "only-on-one.txt")); err != nil {
		t.Fatal("feat/two should be based on feat/one (current)")
	}
	// And one off main does not have it.
	h.update(EscapeMsg{})
	newWorktree(h, "feat/three", "m")
	if _, err := os.Stat(filepath.Join(h.m.root, "only-on-one.txt")); err == nil {
		t.Fatal("feat/three should be based on main")
	}
	// Existing branches skip naming and the question; esc walks back one
	// stage at a time: base -> identity -> name -> closed.
	h.update(EscapeMsg{})
	openWorktreePrompt(h, "feat/one")
	if h.m.prompting {
		t.Fatal("existing branch/worktree should not ask for a base")
	}
	h.update(EscapeMsg{})
	openWorktreePrompt(h, "feat/four")
	h.key("enter")
	if h.m.promptStage != stageBase {
		t.Fatalf("stage=%v", h.m.promptStage)
	}
	h.key("esc")
	if !h.m.prompting || h.m.promptStage != stageIdentity || h.m.nick.Value() != "feat/four" {
		t.Fatalf("esc should return to identity: prompting=%v stage=%v nick=%q", h.m.prompting, h.m.promptStage, h.m.nick.Value())
	}
	h.key("esc")
	if !h.m.prompting || h.m.promptStage != stageName || h.m.prompt.Value() != "feat/four" {
		t.Fatalf("esc should return to the name step: prompting=%v stage=%v value=%q", h.m.prompting, h.m.promptStage, h.m.prompt.Value())
	}
	h.key("esc")
	if h.m.prompting {
		t.Fatal("third esc cancels")
	}
}

func TestNormalShowsPaneUnfadedButUnfocused(t *testing.T) {
	h := newHarness(t)
	h.update(EscapeMsg{})
	if !h.m.normal() {
		t.Fatal("expected normal")
	}
	// Body must be the plain snapshot (no faint attribute), border unfocused.
	body := h.m.View()
	if strings.Contains(body, "\x1b[2m") || strings.Contains(body, ";2m") {
		t.Fatal("normal must not fade the pane")
	}
}

func TestWorktreePromptFiltersBranchesLiveAndSelects(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	for _, b := range []string{"feat/alpha", "feat/beta", "fix/login"} {
		cmd := exec.Command("git", "branch", b)
		cmd.Dir = h.root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	st := newMemStore()
	h.m.cfg.Notes = st
	h.m.refreshRepo()
	h.m.name = "main"
	h.update(EscapeMsg{})
	h.key("w")
	// Empty query lists everything (no selection).
	if len(h.m.matches) != 4 || h.m.matchSel != -1 {
		t.Fatalf("initial matches=%v sel=%d", h.m.matches, h.m.matchSel)
	}
	for _, r := range "fb" {
		h.key(string(r))
	}
	if len(h.m.matches) != 1 || h.m.matches[0].name != "feat/beta" {
		t.Fatalf("live filter: %v", h.m.matches)
	}
	view := stripANSI(h.m.View())
	if !strings.Contains(view, "feat/beta") || strings.Contains(view, "fix/login") {
		t.Fatalf("prompt should show only matches:\n%s", view)
	}
	// A single remaining match is accepted by Enter without selecting it first.
	if h.m.matchSel != -1 {
		t.Fatalf("single match should remain unselected, sel=%d", h.m.matchSel)
	}
	h.key("enter")
	if h.m.prompting || h.m.name != "feat/beta" {
		t.Fatalf("enter on a match should open it: prompting=%v name=%q", h.m.prompting, h.m.name)
	}
	// A running workstream and a dormant worktree are offered too, tagged.
	h.update(EscapeMsg{})
	h.key("a") // archive feat/beta -> dormant
	h.key("w")
	var kinds []string
	for _, c := range h.m.matches {
		kinds = append(kinds, c.name+":"+c.kind)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "feat/beta:dormant") || !strings.Contains(joined, "main:open") || !strings.Contains(joined, "feat/alpha:branch") {
		t.Fatalf("kinds %s", joined)
	}
	// Typing something new: no matches, Enter goes to the base step.
	for _, r := range "brandnew" {
		h.key(string(r))
	}
	if len(h.m.matches) != 0 {
		t.Fatalf("expected no matches, got %v", h.m.matches)
	}
	h.key("enter")
	if !h.m.prompting || h.m.promptStage != stageIdentity {
		t.Fatal("new name should ask for its identity")
	}
	h.key("esc")
	if h.m.promptStage != stageName {
		t.Fatal("esc from identity returns to the name")
	}
	// Backspace re-filters; ctrl+n / ctrl+p / tab cycle; up past the top clears the selection.
	for i := 0; i < len("brandnew"); i++ {
		h.key("backspace")
	}
	if len(h.m.matches) != 4 {
		t.Fatalf("after clearing: %d matches", len(h.m.matches))
	}
	h.key("ctrl+n")
	h.key("ctrl+n")
	if h.m.matchSel != 1 {
		t.Fatalf("ctrl+n sel=%d", h.m.matchSel)
	}
	h.key("ctrl+p")
	h.key("up")
	if h.m.matchSel != -1 {
		t.Fatalf("up past top should deselect, sel=%d", h.m.matchSel)
	}
	h.key("tab")
	if h.m.matchSel != 0 {
		t.Fatalf("tab sel=%d", h.m.matchSel)
	}
	// Selecting the running workstream just switches to it.
	for h.m.matches[h.m.matchSel].kind != "open" {
		h.key("tab")
	}
	h.key("enter")
	if h.m.prompting || h.m.name != "main" {
		t.Fatalf("selecting a running stream should switch: prompting=%v name=%q", h.m.prompting, h.m.name)
	}
}
