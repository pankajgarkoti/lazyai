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

	"lazyai/internal/config"
	"lazyai/internal/git"
	"lazyai/internal/hooks"
	"lazyai/internal/notes"
	"lazyai/internal/terminal"
	"lazyai/internal/theme"
)

// --- G1: mouse boundaries not covered before ---------------------------------

func TestMouseWheelScrollsDiffContentAndPromptList(t *testing.T) {
	h := newHarness(t)
	h.update(tea.WindowSizeMsg{Width: 120, Height: 14})
	p := h.writeFile(t, "big.go", 200)
	h.hook(hooks.Event{Type: "file.before", Path: p})
	data, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.ReplaceAll(string(data), "line xxx", "CHANGED")), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.update(EscapeMsg{})
	h.key("d")
	if h.m.mode != ModeDiff {
		t.Fatal("expected diff")
	}
	// Wheel over the diff pane scrolls the diff viewport and focuses it.
	before := h.m.diffView.YOffset
	h.mouse(h.m.sidebarWidth+3, 3, tea.MouseButtonWheelDown)
	if h.m.focus != FocusContent || h.m.diffView.YOffset <= before {
		t.Fatalf("diff wheel: focus=%v before=%d after=%d", h.m.focus, before, h.m.diffView.YOffset)
	}
	h.mouse(h.m.sidebarWidth+3, 3, tea.MouseButtonWheelUp)
	if h.m.diffView.YOffset != before {
		t.Fatalf("wheel up should return: %d", h.m.diffView.YOffset)
	}

	// The prompt's match list follows the wheel and right click backs out.
	gitRepo(t, h.root)
	for _, b := range []string{"feat/a", "feat/b", "feat/c"} {
		if err := exec.Command("git", "-C", h.root, "branch", b).Run(); err != nil {
			t.Fatal(err)
		}
	}
	h.m.refreshRepo()
	h.key("esc")
	h.key("w")
	rw, _ := h.m.rightInner()
	matchRows := h.m.renderMatches(rw)
	firstRow := 1 + len(h.m.renderPrompt(rw)) - len(matchRows)
	h.mouse(h.m.sidebarWidth+4, firstRow, tea.MouseButtonWheelDown)
	h.mouse(h.m.sidebarWidth+4, firstRow, tea.MouseButtonWheelDown)
	if h.m.matchSel != 1 {
		t.Fatalf("prompt wheel sel=%d", h.m.matchSel)
	}
	h.mouse(h.m.sidebarWidth+4, firstRow, tea.MouseButtonWheelUp)
	if h.m.matchSel != 0 {
		t.Fatalf("prompt wheel up sel=%d", h.m.matchSel)
	}
	// Right click: base -> identity -> name -> closed, one stage per click.
	for _, r := range "feat/new" {
		h.key(string(r))
	}
	h.key("enter")
	h.key("enter")
	if h.m.promptStage != stageBase {
		t.Fatalf("stage=%v", h.m.promptStage)
	}
	h.mouse(h.m.sidebarWidth+4, 3, tea.MouseButtonRight)
	if h.m.promptStage != stageIdentity {
		t.Fatalf("right click from base -> %v", h.m.promptStage)
	}
	h.mouse(h.m.sidebarWidth+4, 3, tea.MouseButtonRight)
	if h.m.promptStage != stageName || !h.m.prompting {
		t.Fatalf("right click from identity -> %v prompting=%v", h.m.promptStage, h.m.prompting)
	}
	h.mouse(h.m.sidebarWidth+4, 3, tea.MouseButtonRight)
	if h.m.prompting {
		t.Fatal("right click from name should close")
	}
}

func TestMouseYesClickQuitsAndReleaseMotionModifiersReachChild(t *testing.T) {
	h := newHarness(t)
	h.update(QuitMsg{})
	rw, _ := h.m.rightInner()
	contentX := h.m.sidebarWidth + 1
	var cmd tea.Cmd
	for y, row := range h.m.renderQuitPrompt(rw) {
		if x, _, ok := labelCellBounds(stripANSI(row), "y yes"); ok {
			var mm tea.Model
			mm, cmd = h.m.Update(tea.MouseMsg{X: contentX + x, Y: y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			h.m = mm.(Model)
			break
		}
	}
	if cmd == nil {
		t.Fatal("clicking yes should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("yes should emit tea.QuitMsg")
	}

	// Release and motion keep their action; modifiers survive translation.
	for _, tc := range []struct {
		action tea.MouseAction
		want   terminal.MouseAction
	}{{tea.MouseActionPress, terminal.MousePress}, {tea.MouseActionRelease, terminal.MouseRelease}, {tea.MouseActionMotion, terminal.MouseMotion}} {
		msg := tea.MouseMsg{X: 40, Y: 5, Action: tc.action, Button: tea.MouseButtonLeft, Shift: true, Alt: true, Ctrl: true}
		got := mouseEvent(msg, msg.X-contentX, msg.Y-1)
		if got.Action != tc.want || !got.Shift || !got.Alt || !got.Ctrl || got.X != 40-contentX || got.Y != 4 {
			t.Fatalf("action %v -> %+v", tc.action, got)
		}
	}
	wheel := mouseEvent(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}, 1, 1)
	if int(wheel.Button) != int(tea.MouseButtonWheelDown) {
		t.Fatalf("wheel button lost: %+v", wheel)
	}
}

// --- Normal-mode navigation: j/k workstreams, h/l files ----------------------

func TestNormalUsesJKForWorkstreamsAndHLForFiles(t *testing.T) {
	h := twoStreams(t) // cur = 2
	for i := 1; i <= 3; i++ {
		p := filepath.Join(h.m.root, fmt.Sprintf("f%d.go", i))
		os.WriteFile(p, []byte(goFile), 0o644)
		h.hook(hooks.Event{Type: "file.read", Path: p})
	}
	h.update(EscapeMsg{})
	if !h.m.normal() {
		t.Fatal("expected normal")
	}
	// j / k cycle workstreams (wrapping) and land in normal.
	h.key("j")
	if h.m.cur != 0 || !h.m.normal() {
		t.Fatalf("j -> cur %d normal=%v", h.m.cur, h.m.normal())
	}
	h.key("k")
	if h.m.cur != 1 {
		t.Fatalf("k -> cur %d", h.m.cur)
	}
	// h / l move the file selection exactly as j / k used to.
	h.key("l")
	h.key("l")
	if h.m.fileSel != 2 || h.m.cur != 1 {
		t.Fatalf("l l -> fileSel %d cur %d", h.m.fileSel, h.m.cur)
	}
	h.key("h")
	if h.m.fileSel != 1 {
		t.Fatalf("h -> fileSel %d", h.m.fileSel)
	}
	h.key("l")
	h.key("l") // clamps at the last file
	if h.m.fileSel != 2 {
		t.Fatalf("clamp -> fileSel %d", h.m.fileSel)
	}
	// Diff mode keeps j / k for files and h / l for workstreams.
	p := filepath.Join(h.m.root, "f1.go")
	h.hook(hooks.Event{Type: "file.before", Path: p})
	os.WriteFile(p, []byte("changed\n"), 0o644)
	h.hook(hooks.Event{Type: "file.write", Path: p})
	h.key("d")
	if h.m.mode != ModeDiff {
		t.Fatal("expected diff")
	}
	h.key("k")
	if h.m.fileSel != 1 || h.m.cur != 1 {
		t.Fatalf("diff k -> fileSel %d cur %d", h.m.fileSel, h.m.cur)
	}
	h.key("h")
	if h.m.cur != 0 || !h.m.normal() {
		t.Fatalf("diff h -> cur %d normal=%v", h.m.cur, h.m.normal())
	}
	// Show mode likewise (j in normal first returns to workstream 2).
	h.key("j")
	if h.m.cur != 1 {
		t.Fatalf("normal j -> cur %d", h.m.cur)
	}
	q := filepath.Join(h.m.root, "s.go")
	os.WriteFile(q, []byte(strings.Repeat("x\n", 20)), 0o644)
	h.hook(hooks.Event{Type: "show", Locations: []hooks.Location{{Path: q, Line: 2}, {Path: q, Line: 5}}})
	h.key("j")
	if h.m.showSel != 1 || h.m.cur != 1 {
		t.Fatalf("show j -> showSel %d cur %d", h.m.showSel, h.m.cur)
	}
	h.key("l")
	if h.m.cur != 0 {
		t.Fatalf("show l -> cur %d", h.m.cur)
	}
	// The hint tells you about j/k in normal.
	if s := stripANSI(h.m.renderHint()); !strings.Contains(s, "j/k:workstream") {
		t.Fatalf("normal hint: %q", s)
	}
}

func TestLeaderQQuitsTheSessionAfterConfirmation(t *testing.T) {
	h := twoStreams(t) // interactive, OpenCode owns the keys
	h.update(LeaderMsg{})
	h.key("q")
	if !h.m.confirmQuit || h.forward[len(h.forward)-1] {
		t.Fatalf("leader q should open the quit confirmation and take the keys: confirm=%v", h.m.confirmQuit)
	}
	if v := stripANSI(h.m.View()); !strings.Contains(v, "stop all workstreams") || !strings.Contains(v, "y yes") {
		t.Fatalf("quit float missing:\n%s", v)
	}
	h.key("n")
	if h.m.confirmQuit || !h.forward[len(h.forward)-1] || len(h.m.streams) != 2 {
		t.Fatal("n should cancel and return the keys")
	}
	// From normal it works the same and y quits.
	h.update(EscapeMsg{})
	h.update(tea.KeyMsg{Type: tea.KeyCtrlAt})
	h.key("q")
	if !h.m.confirmQuit {
		t.Fatal("ctrl+@ q from normal should confirm quit")
	}
	mm, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	h.m = mm.(Model)
	if cmd == nil {
		t.Fatal("y should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("y should emit tea.QuitMsg")
	}
	if !strings.Contains(strings.Join(renderHelp(220), "\n"), "ctrl+space q") {
		t.Fatal("help should document ctrl+space q")
	}
}

// --- Restore the previous session's workstreams ------------------------------

func TestRestoreReopensWorkstreamsLeftOpenByThePreviousSession(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte(goFile), 0o644)
	gitRepo(t, root)
	// Previous session: two worktrees left open, one archived, one closed, one
	// whose directory is gone.
	st := newMemStore()
	mk := func(branch string, i int) string {
		p, _, err := gitEnsure(root, branch)
		if err != nil {
			t.Fatal(err)
		}
		st.wts[branch] = &notes.Worktree{Repo: realpath(root), Branch: branch, Path: p, Linked: true, CreatedAt: time.Unix(int64(1000+i), 0)}
		return p
	}
	mk("feat/second", 2)
	mk("feat/first", 1)
	st.wts["feat/second"].Nickname = "Second lane"
	mk("feat/archived", 3)
	st.wts["feat/archived"].Dormant = true
	gone := mk("feat/gone", 4)
	os.RemoveAll(gone)
	st.wts["main"] = &notes.Worktree{Repo: realpath(root), Branch: "main", Path: realpath(root)}

	h := &harness{root: root}
	m, err := New(Config{Root: root, Launch: h.launch, Notes: st,
		SetForward: func(v bool) { h.forward = append(h.forward, v) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range h.children {
			c.Close()
		}
	})
	h.m = m
	h.update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Only the two left-open worktrees that still exist are restorable.
	if got := branches(h.m.restorable); got != "feat/first,feat/second" {
		t.Fatalf("restorable=%q", got)
	}
	if !strings.Contains(h.m.notice, "2 workstreams") || !strings.Contains(h.m.notice, "R") {
		t.Fatalf("startup notice=%q", h.m.notice)
	}
	h.update(EscapeMsg{})
	if s := stripANSI(h.m.renderHint()); !strings.Contains(s, "R:restore 2") {
		t.Fatalf("hint=%q", s)
	}
	// R reopens them in original order, in the background: focus stays put.
	h.key("R")
	names := []string{}
	for _, s := range h.m.streams {
		names = append(names, s.name)
	}
	if strings.Join(names, ",") != "main,feat/first,feat/second" || h.m.cur != 0 || !h.m.normal() {
		t.Fatalf("after R: %v cur=%d", names, h.m.cur)
	}
	if h.m.streams[2].nickname != "Second lane" {
		t.Fatal("restored workstream should keep its identity")
	}
	if len(h.m.restorable) != 0 || !strings.Contains(h.m.notice, "restored 2") || strings.Contains(stripANSI(h.m.renderHint()), "restore") {
		t.Fatalf("restorable=%d notice=%q", len(h.m.restorable), h.m.notice)
	}
	h.key("R")
	if !strings.Contains(h.m.notice, "nothing") {
		t.Fatalf("second R: %q", h.m.notice)
	}
	// An explicit close parks the worktree (dormant) so it is not offered
	// again; archive already did. A session that simply ends leaves rows open.
	h.key("j") // -> feat/first
	h.key("x")
	h.key("x")
	if !st.wts["feat/first"].Dormant || st.wts["feat/second"].Dormant {
		t.Fatalf("close should mark dormant: first=%v second=%v", st.wts["feat/first"].Dormant, st.wts["feat/second"].Dormant)
	}
}

func branches(list []notes.Worktree) string {
	var out []string
	for _, w := range list {
		out = append(out, w.Branch)
	}
	return strings.Join(out, ",")
}

func gitEnsure(root, branch string) (string, bool, error) {
	return git.EnsureWorktree(root, branch, "")
}

// --- Workstream hover --------------------------------------------------------

func TestWorkstreamHoverShowsDetailsAndAnyKeyDismisses(t *testing.T) {
	h := twoStreams(t)
	h.m.nickname, h.m.description = "Search", "rebuild the index nightly"
	h.update(EscapeMsg{})
	rowsBefore := strings.Count(h.m.View(), "\n")
	h.key("K")
	if !h.m.info {
		t.Fatal("K should open the workstream float")
	}
	view := h.m.View()
	plain := stripANSI(view)
	for _, want := range []string{"Search", "second", "rebuild the index nightly", theme.FloatTL} {
		if !strings.Contains(plain, want) {
			t.Fatalf("hover missing %q:\n%s", want, plain)
		}
	}
	if strings.Count(view, "\n") != rowsBefore {
		t.Fatal("the float must overlay the pane, not shift the layout")
	}
	// The float sits at the left edge of the content pane, aligned with the
	// current workstream's strip row.
	rows := strings.Split(plain, "\n")
	row := 1 + h.m.cur
	if !strings.Contains(rows[row], theme.FloatTL) {
		t.Fatalf("float should start on strip row %d: %q", row, rows[row])
	}
	// Any key dismisses it and is consumed; state is otherwise untouched.
	h.key("j")
	if h.m.info || !strings.Contains(stripANSI(h.m.View()), "Workstreams") || strings.Contains(stripANSI(h.m.View()), "nightly") {
		t.Fatal("a key should close the float and do nothing else")
	}
	if h.m.fileSel != 0 {
		t.Fatal("the dismissing key must not act")
	}
	// Esc and mouse clicks dismiss too; it also works from the leader.
	h.key("K")
	h.update(EscapeMsg{})
	if h.m.info || !h.m.normal() {
		t.Fatal("esc should only close the float")
	}
	h.key("K")
	h.mouse(2, 2, tea.MouseButtonLeft)
	if h.m.info {
		t.Fatal("click should close the float")
	}
	h.key("i")
	h.update(LeaderMsg{})
	h.key("K")
	if !h.m.info || h.forward[len(h.forward)-1] {
		t.Fatal("leader K opens the float and takes the keyboard")
	}
	h.key("x")
	if h.m.info || !h.forward[len(h.forward)-1] || h.m.pendingClose != "" {
		t.Fatal("dismissing from interactive returns the keys to OpenCode without acting")
	}
	// Narrow terminals: no overflow.
	h.update(EscapeMsg{})
	h.update(tea.WindowSizeMsg{Width: 60, Height: 18})
	h.key("K")
	if w := widestRow(h.m.View()); w > 60 {
		t.Fatalf("hover overflows: %d", w)
	}
}

// --- G5: activity model ------------------------------------------------------

func TestOverlappingAndOutOfOrderToolEventsKeepWorkingAccurate(t *testing.T) {
	h := twoStreams(t)
	first := h.m.streams[0]
	send := func(typ, id string) tea.Cmd {
		mm, cmd := h.m.Update(HookMsg{Event: hooks.Event{Token: first.token, Type: typ, Tool: "bash", CallID: id}})
		h.m = mm.(Model)
		return cmd
	}
	if cmd := send("tool.before", "a"); cmd == nil || !h.m.ticking {
		t.Fatal("first tool call should start the spinner")
	}
	if cmd := send("tool.before", "b"); cmd != nil {
		t.Fatal("a second call must not schedule a second ticker")
	}
	send("tool.after", "a")
	if !first.working() {
		t.Fatal("finishing one of two calls must not show idle")
	}
	send("tool.after", "a")   // duplicate finish is harmless
	send("tool.after", "zzz") // unknown finish is harmless
	if !first.working() {
		t.Fatal("stray after-events must not clear other calls")
	}
	// The strip animates while working; frames advance on ticks.
	view := func() string { return stripANSI(h.m.View()) }
	f0 := theme.Frame(h.m.spin)
	if !strings.Contains(view(), f0) {
		t.Fatalf("spinner frame %q missing:\n%s", f0, view())
	}
	mm, cmd := h.m.Update(TickMsg{})
	h.m = mm.(Model)
	if cmd == nil || theme.Frame(h.m.spin) == f0 {
		t.Fatal("tick should advance the frame and reschedule while working")
	}
	send("tool.after", "b")
	if first.working() {
		t.Fatal("all calls finished -> idle")
	}
	mm, cmd = h.m.Update(TickMsg{})
	h.m = mm.(Model)
	if cmd != nil || h.m.ticking {
		t.Fatal("ticks stop when nothing is working")
	}
	if !strings.Contains(view(), theme.Dot) {
		t.Fatal("idle glyph missing")
	}
	// A lost after-event is reconciled by the stream-level idle event.
	send("tool.before", "orphan")
	send("idle", "")
	if first.working() {
		t.Fatal("idle must clear orphaned calls")
	}
	// Precedence: attention beats working beats unseen.
	send("tool.before", "c")
	send("attention", "")
	glyph, _ := h.m.streamGlyph(first)
	if glyph != theme.Attention {
		t.Fatalf("attention should win: %q", glyph)
	}
	// Visiting the stream clears unseen only; attention stays until OpenCode
	// resolves it.
	first.unseen = true
	h.update(LeaderMsg{})
	h.key("1")
	if h.m.unseen || !h.m.attention {
		t.Fatalf("switch: unseen=%v attention=%v", h.m.unseen, h.m.attention)
	}
	// Background file writes flag unseen output on that stream.
	h.update(LeaderMsg{})
	h.key("2")
	p := filepath.Join(first.root, "w.go")
	os.WriteFile(p, []byte(goFile), 0o644)
	h.update(HookMsg{Event: hooks.Event{Token: first.token, Type: "file.write", Path: p}})
	if !first.unseen {
		t.Fatal("background write should mark unseen")
	}
}

// --- G3: strict contracts ----------------------------------------------------

const strictYAML = `
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
          label: Acceptance
          type: text
          required: true
        - key: notes
          label: Notes
          type: text
`

func strictHarness(t *testing.T, yaml string) *harness {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, config.Dir), 0o755)
	os.WriteFile(config.Path(root), []byte(yaml), 0o644)
	h := &harness{root: root}
	m, err := New(Config{
		Root:       root,
		Launch:     h.launch,
		SetForward: func(v bool) { h.forward = append(h.forward, v) },
		LoadConfig: func() (config.Config, []string, error) { return config.Load(root) },
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
	h.update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return h
}

func childScreen(h *harness) string { return stripANSI(strings.Join(h.m.term.Snapshot(false), "\n")) }

func waitScreen(t *testing.T, h *harness, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(childScreen(h), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q never reached the child; screen:\n%s", want, childScreen(h))
}

func TestStrictModeGatesEveryEntryThroughTheContractForm(t *testing.T) {
	h := strictHarness(t, strictYAML)
	// A fresh workstream in strict mode opens the form instead of focusing.
	if h.m.contract == nil || h.m.mode != ModeInteractive {
		t.Fatalf("startup should open the contract: contract=%v mode=%v", h.m.contract != nil, h.m.mode)
	}
	if last := h.forward[len(h.forward)-1]; last {
		t.Fatal("keys must not be forwarded while the form is open")
	}
	if !strings.Contains(stripANSI(h.m.renderStatus()), " CONTRACT ") {
		t.Fatalf("mode indicator: %q", stripANSI(h.m.renderStatus()))
	}
	view := stripANSI(h.m.View())
	for _, want := range []string{"Task contract", "Outcome", "Acceptance", "Notes", "ctrl+s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form missing %q:\n%s", want, view)
		}
	}
	// Esc keeps the draft and lands in normal; i, enter and a pane click all
	// reopen the form rather than handing OpenCode the keys.
	for _, r := range "ship it" {
		h.key(string(r))
	}
	h.key("esc")
	if h.m.contract != nil || !h.m.normal() || h.m.draft["outcome"] != "ship it" {
		t.Fatalf("esc: contract=%v normal=%v draft=%v", h.m.contract != nil, h.m.normal(), h.m.draft)
	}
	if s := stripANSI(h.m.renderStatus()); !strings.Contains(s, "i:contract") {
		t.Fatalf("normal hint should advertise the contract: %q", s)
	}
	h.key("i")
	if h.m.contract == nil || h.m.contract.inputs[0].value() != "ship it" {
		t.Fatal("i should reopen the form with the draft")
	}
	h.key("esc")
	h.key("enter")
	if h.m.contract == nil {
		t.Fatal("enter should reopen the form")
	}
	h.key("esc")
	h.mouse(h.m.sidebarWidth+5, 5, tea.MouseButtonLeft)
	if h.m.contract == nil || h.forward[len(h.forward)-1] {
		t.Fatal("clicking the pane must open the form, not focus OpenCode")
	}
	// Submit with a missing required field: the form stays, the field is
	// flagged, values are kept.
	h.key("ctrl+s")
	if h.m.contract == nil || !h.m.contract.invalid["acceptance"] || h.m.contract.focus != 1 {
		t.Fatalf("missing required: contract=%v invalid=%v focus=%d", h.m.contract != nil, h.m.contract.invalid, h.m.contract.focus)
	}
	if !strings.Contains(stripANSI(h.m.View()), "required") || h.m.contract.inputs[0].value() != "ship it" {
		t.Fatal("validation must flag the field and keep values")
	}
	// Fill it via tab navigation and enter on the last text field submits.
	for _, r := range "tests pass" {
		h.key(string(r))
	}
	h.key("tab") // notes (optional, left empty)
	h.key("enter")
	if h.m.contract != nil || h.m.mode != ModeInteractive || h.m.focus != FocusContent {
		t.Fatalf("submit should hand over to OpenCode: contract=%v mode=%v focus=%v", h.m.contract != nil, h.m.mode, h.m.focus)
	}
	if !h.forward[len(h.forward)-1] {
		t.Fatal("after submit OpenCode owns the keys")
	}
	// /bin/cat echoes the paste: deterministic YAML, one document, no notes.
	waitScreen(t, h, "contract: task")
	screen := childScreen(h)
	for _, want := range []string{"outcome: |", "  ship it", "acceptance: |", "  tests pass"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q in\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "notes:") {
		t.Fatal("empty optional field must be omitted")
	}
	if h.m.draft != nil {
		t.Fatal("a sent contract clears the draft")
	}
	// Freestyle for this stream only: i focuses OpenCode directly, the mode
	// says so, and toggling back restores the gate.
	h.update(EscapeMsg{})
	h.update(LeaderMsg{})
	h.key("f")
	if !h.m.freestyle || !strings.Contains(stripANSI(h.m.renderStatus()), "FREE") {
		t.Fatalf("freestyle: %v %q", h.m.freestyle, stripANSI(h.m.renderStatus()))
	}
	h.key("i")
	if h.m.contract != nil || h.m.focus != FocusContent {
		t.Fatal("freestyle i should focus OpenCode")
	}
	h.update(EscapeMsg{})
	h.update(LeaderMsg{})
	h.key("f")
	h.key("i")
	if h.m.contract == nil {
		t.Fatal("re-enabling strict should gate i again")
	}
	h.key("esc")
	// Freestyle is per workstream: a second stream is still strict.
	second := t.TempDir()
	h.m.streams[0].freestyle = true
	if _, err := h.m.addStream(second, "second"); err != nil {
		t.Fatal(err)
	}
	if h.m.contract == nil || h.m.freestyle {
		t.Fatal("new stream must start strict")
	}
}

func TestInvalidOrMissingConfigNeverEnforcesStrictMode(t *testing.T) {
	h := strictHarness(t, "version: 1\ninteractive: [\n")
	if h.m.contract != nil || h.m.configErr == "" || h.m.strictActive() {
		t.Fatalf("malformed config: contract=%v err=%q", h.m.contract != nil, h.m.configErr)
	}
	if s := stripANSI(h.m.renderStatus()); !strings.Contains(s, "config error") || !strings.Contains(s, "reload") {
		t.Fatalf("status should show the persistent config error: %q", s)
	}
	h.update(EscapeMsg{})
	h.key("i")
	if h.m.contract != nil || h.m.focus != FocusContent {
		t.Fatal("with a broken config i must behave as before")
	}
	h.update(EscapeMsg{})
	h.update(LeaderMsg{})
	h.key("f")
	if !strings.Contains(h.m.notice, "not enabled") {
		t.Fatalf("freestyle without strict: %q", h.m.notice)
	}
	// Fixing the file and reloading (leader c) enables strict entry.
	os.WriteFile(config.Path(h.root), []byte(strictYAML), 0o644)
	h.update(LeaderMsg{})
	h.key("c")
	if h.m.configErr != "" || !h.m.strictActive() || !strings.Contains(h.m.notice, "reloaded") {
		t.Fatalf("reload: err=%q strict=%v notice=%q", h.m.configErr, h.m.strictActive(), h.m.notice)
	}
	// Unknown keys warn but load.
	os.WriteFile(config.Path(h.root), []byte(strictYAML+"\nsomething_new: 1\n"), 0o644)
	h.update(LeaderMsg{})
	h.key("c")
	if h.m.configErr != "" || !strings.Contains(h.m.configWarn, "something_new") {
		t.Fatalf("warn: err=%q warn=%q", h.m.configErr, h.m.configWarn)
	}
	// No file at all: plain defaults.
	plain := newHarness(t)
	if plain.m.strictActive() || plain.m.configErr != "" {
		t.Fatal("no config -> no strict, no error")
	}
}

// --- G6: ergonomic gate: narrow terminals ------------------------------------

func widestRow(s string) int {
	w := 0
	for _, row := range strings.Split(s, "\n") {
		if rw := lipgloss.Width(row); rw > w {
			w = rw
		}
	}
	return w
}

func TestFormsFitA60x18TerminalWithoutOverflow(t *testing.T) {
	h := strictHarness(t, strictYAML)
	h.update(tea.WindowSizeMsg{Width: 60, Height: 18})
	if h.m.contract == nil {
		t.Fatal("contract open")
	}
	view := h.m.View()
	if w := widestRow(view); w > 60 {
		t.Fatalf("contract view overflows: %d > 60", w)
	}
	plain := stripANSI(view)
	for _, want := range []string{"Outcome", "Acceptance", "Notes", "ctrl+s"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("narrow contract form lost %q:\n%s", want, plain)
		}
	}
	if rows := strings.Count(view, "\n") + 1; rows != 18 {
		t.Fatalf("view has %d rows, want 18", rows)
	}
	// The form is centred over the pane, not pinned to the top.
	if h.m.contract.boxTop == 0 {
		t.Fatal("form should be vertically centred")
	}
	h.key("esc")

	// The workstream form on the same screen.
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.key("w")
	for _, r := range "feat/narrow" {
		h.key(string(r))
	}
	h.key("enter")
	view = h.m.View()
	if w := widestRow(view); w > 60 {
		t.Fatalf("identity form overflows: %d > 60", w)
	}
	plain = stripANSI(view)
	if !strings.Contains(plain, "nickname") || !strings.Contains(plain, "description") {
		t.Fatalf("identity form missing fields at 60x18:\n%s", plain)
	}
	h.key("enter")
	if w := widestRow(h.m.View()); w > 60 {
		t.Fatalf("base stage overflows: %d > 60", w)
	}
	// Keyboard-only path completes without any mouse.
	h.key("m")
	if h.m.prompting || len(h.m.streams) != 2 {
		t.Fatalf("keyboard-only creation failed: prompting=%v streams=%d", h.m.prompting, len(h.m.streams))
	}
	if w := widestRow(h.m.View()); w > 60 {
		t.Fatalf("view overflows after creation: %d", w)
	}
}

// --- G4: agent setup ---------------------------------------------------------

func setupRequestFor(h *harness, specs []hooks.WorkstreamSpec) (chan hooks.Reply, hooks.Event) {
	reply := make(chan hooks.Reply, 1)
	ev := hooks.Event{Token: h.m.token, Type: "setup", Workstreams: specs, Reply: reply}
	return reply, ev
}

func TestAgentSetupOpensWorkstreamsThroughTheSharedCommandWithoutStealingFocus(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	st := newMemStore()
	h.m.cfg.Notes = st
	h.m.refreshRepo()
	h.m.name = "main"
	if err := exec.Command("git", "-C", h.root, "branch", "feat/existing").Run(); err != nil {
		t.Fatal(err)
	}
	// One new branch: no confirmation needed. Current stream stays focused.
	reply, ev := setupRequestFor(h, []hooks.WorkstreamSpec{
		{Branch: "feat/existing", Nickname: "Existing"},
		{Branch: "feat/agent", Nickname: "Agent lane", Description: "opened by the agent", Base: "main"},
	})
	h.update(HookMsg{Event: ev})
	var r hooks.Reply
	select {
	case r = <-reply:
	default:
		t.Fatal("setup must reply synchronously")
	}
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	results := r.Result.(map[string]any)["workstreams"].([]WorkstreamResult)
	// Both get a fresh worktree (Created); only feat/agent is a new branch,
	// which is why no confirmation was needed above.
	if len(results) != 2 || !results[0].Launched || !results[0].Created || !results[1].Launched || !results[1].Created {
		t.Fatalf("results=%+v", results)
	}
	if results[1].Nickname != "Agent lane" || !strings.HasSuffix(results[1].Root, filepath.Join(".worktrees", "feat-agent")) {
		t.Fatalf("result 1 = %+v", results[1])
	}
	if h.m.name != "main" || h.m.mode != ModeInteractive || h.m.focus != FocusContent {
		t.Fatalf("setup stole focus: name=%q mode=%v focus=%v", h.m.name, h.m.mode, h.m.focus)
	}
	names := []string{}
	for _, s := range h.m.streams {
		names = append(names, s.name)
	}
	if strings.Join(names, ",") != "main,feat/existing,feat/agent" {
		t.Fatalf("order %v", names)
	}
	if st.wts["feat/agent"] == nil || st.wts["feat/agent"].Nickname != "Agent lane" || st.wts["feat/agent"].Description != "opened by the agent" {
		t.Fatalf("identity not persisted: %+v", st.wts["feat/agent"])
	}
	if !strings.Contains(h.m.notice, "2/2") {
		t.Fatalf("notice=%q", h.m.notice)
	}
	// The same branch through the w form is equivalent: already running ->
	// switch, identity untouched.
	h.update(EscapeMsg{})
	openWorktreePrompt(h, "feat/agent")
	if h.m.name != "feat/agent" || h.m.nickname != "Agent lane" || len(h.m.streams) != 3 {
		t.Fatalf("w should reuse: name=%q nick=%q streams=%d", h.m.name, h.m.nickname, len(h.m.streams))
	}

	// Batch validation rejects the whole request before any mutation.
	before := len(h.m.streams)
	for name, specs := range map[string][]hooks.WorkstreamSpec{
		"empty":       {},
		"bad branch":  {{Branch: "bad name", Nickname: "x"}},
		"dup":         {{Branch: "feat/d1", Nickname: "a"}, {Branch: "feat/d1", Nickname: "b"}},
		"no nickname": {{Branch: "feat/d2"}},
		"bad base":    {{Branch: "feat/d3", Nickname: "c", Base: "nope"}},
		"long nick":   {{Branch: "feat/d4", Nickname: strings.Repeat("n", 61)}},
		"too many": func() []hooks.WorkstreamSpec {
			var out []hooks.WorkstreamSpec
			for i := 0; i < 11; i++ {
				out = append(out, hooks.WorkstreamSpec{Branch: "feat/many" + string(rune('a'+i)), Nickname: "m"})
			}
			return out
		}(),
	} {
		reply, ev := setupRequestFor(h, specs)
		h.update(HookMsg{Event: ev})
		r := <-reply
		if r.Err == nil {
			t.Errorf("%s: expected rejection", name)
		}
		if len(h.m.streams) != before || h.m.setup != nil {
			t.Fatalf("%s: rejected batch mutated state", name)
		}
	}
	// Unknown tokens are refused rather than acted on.
	reply2 := make(chan hooks.Reply, 1)
	h.update(HookMsg{Event: hooks.Event{Token: "nope", Type: "setup", Workstreams: []hooks.WorkstreamSpec{{Branch: "feat/x", Nickname: "x"}}, Reply: reply2}})
	if r := <-reply2; r.Err == nil {
		t.Fatal("unknown token must be rejected")
	}
}

func TestAgentSetupWithSeveralNewBranchesAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.writeFile(t, "a.go", 3)
	gitRepo(t, h.root)
	h.m.refreshRepo()
	h.m.name = "main"
	specs := []hooks.WorkstreamSpec{
		{Branch: "feat/one", Nickname: "One", Description: "first"},
		{Branch: "feat/two", Nickname: "Two"},
	}
	reply, ev := setupRequestFor(h, specs)
	h.update(HookMsg{Event: ev})
	if h.m.setup == nil || len(h.m.streams) != 1 || h.forward[len(h.forward)-1] {
		t.Fatalf("two new branches should wait for confirmation: setup=%v streams=%d", h.m.setup != nil, len(h.m.streams))
	}
	select {
	case <-reply:
		t.Fatal("must not reply before the user decides")
	default:
	}
	view := stripANSI(h.m.View())
	for _, want := range []string{"agent wants", "feat/one", "One", "first", "feat/two", "y create", "n decline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay missing %q:\n%s", want, view)
		}
	}
	if hintsOf(h) != "ctrl+q:detach" {
		t.Fatalf("hints during setup prompt: %q", hintsOf(h))
	}
	// Declining fails the tool call and creates nothing.
	h.key("n")
	if r := <-reply; r.Err == nil || !strings.Contains(r.Err.Error(), "declined") {
		t.Fatalf("decline reply: %+v", r)
	}
	if h.m.setup != nil || len(h.m.streams) != 1 || !h.forward[len(h.forward)-1] {
		t.Fatal("decline should restore state")
	}
	// Accepting (by clicking y) creates them in order, in the background.
	reply, ev = setupRequestFor(h, specs)
	h.update(HookMsg{Event: ev})
	rw, _ := h.m.rightInner()
	contentX := h.m.sidebarWidth + 1
	clicked := false
	for y, row := range h.m.renderSetupPrompt(rw) {
		if x, _, ok := labelCellBounds(stripANSI(row), "y create"); ok {
			h.mouse(contentX+x, y+1, tea.MouseButtonLeft)
			clicked = true
			break
		}
	}
	if !clicked {
		t.Fatal("y create not rendered")
	}
	r := <-reply
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(h.m.streams) != 3 || h.m.name != "main" || h.m.streams[1].name != "feat/one" || h.m.streams[2].nickname != "Two" {
		t.Fatalf("streams after accept: %d cur=%q", len(h.m.streams), h.m.name)
	}
	// An expired confirmation is refused rather than acted on late.
	reply, ev = setupRequestFor(h, []hooks.WorkstreamSpec{{Branch: "feat/l1", Nickname: "l1"}, {Branch: "feat/l2", Nickname: "l2"}})
	h.update(HookMsg{Event: ev})
	h.m.setup.deadline = time.Now().Add(-time.Second)
	h.key("y")
	if len(h.m.streams) != 3 || !strings.Contains(h.m.notice, "expired") {
		t.Fatalf("expired: streams=%d notice=%q", len(h.m.streams), h.m.notice)
	}
	select {
	case <-reply:
		t.Fatal("expired request must not be answered with a result")
	default:
	}
}
