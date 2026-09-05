package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"lazyai/internal/activity"
	"lazyai/internal/fuzzy"
	"lazyai/internal/git"
	"lazyai/internal/notes"
)

// Workstream lifecycle -------------------------------------------------------

// addStream launches an OpenCode child in root and makes it current.
func (m *Model) addStream(root, name string) (*stream, error) {
	w, h := m.rightInner()
	if m.width == 0 { // size unknown until the first WindowSizeMsg
		w, h = 80, 24
	}
	term, token, err := m.cfg.Launch(root, w, h)
	if err != nil {
		return nil, err
	}
	s := &stream{
		root:     root,
		token:    token,
		term:     term,
		ledger:   activity.New(root),
		mode:     ModeInteractive,
		focus:    FocusContent,
		diffView: viewport.New(w, h),
		showView: viewport.New(w, h),
	}
	if info, err := git.Inspect(root); err == nil {
		s.repo = info
	}
	if name == "" {
		name = s.repo.Branch
	}
	if name == "" {
		name = filepath.Base(root)
	}
	s.name = name
	// The repository's main checkout is pinned at the top; everything else is
	// an append-only list, newest last.
	idx := len(m.streams)
	if s.repo.Top != "" && !s.repo.Linked {
		idx = 0
		m.streams = append([]*stream{s}, m.streams...)
		if m.stream != nil {
			m.cur++
			m.last++
		}
	} else {
		m.streams = append(m.streams, s)
	}
	m.switchTo(idx)
	m.focus = FocusContent // a fresh workstream is there to be typed into
	m.syncKeyboard()
	m.registerWorktree()
	return s, nil
}

// registerWorktree records the current stream's worktree as active.
func (m *Model) registerWorktree() {
	if m.cfg.Notes == nil || m.repo.Main == "" {
		return
	}
	_ = m.cfg.Notes.UpsertWorktree(m.repo.Main, m.name, m.root, m.repo.Linked)
}

// archiveStream sends the current workstream dormant: OpenCode and its shell
// stop, the worktree stays on disk and is listed under w for waking.
func (m *Model) archiveStream() tea.Cmd {
	if len(m.streams) < 2 {
		m.notice = "cannot archive the last workstream"
		return nil
	}
	name := m.name
	if m.cfg.Notes != nil && m.repo.Main != "" {
		_ = m.cfg.Notes.SetDormant(m.repo.Main, name, true)
	}
	cmd := m.closeStream(m.cur)
	m.notice = fmt.Sprintf("archived %s (dormant) · w %s wakes it", name, name)
	return cmd
}

// dormantWorktrees lists the repo's registered worktrees that have no
// running workstream: archived ones, and ones left behind by a previous
// LazyAI run. Either way they are one w away.
func (m Model) dormantWorktrees() []notes.Worktree {
	if m.cfg.Notes == nil || m.repo.Main == "" {
		return nil
	}
	list, err := m.cfg.Notes.Worktrees(m.repo.Main)
	if err != nil {
		return nil
	}
	open := map[string]bool{}
	for _, s := range m.streams {
		open[s.name] = true
	}
	var out []notes.Worktree
	for _, w := range list {
		if !open[w.Branch] {
			out = append(out, w)
		}
	}
	return out
}

// switchTo makes streams[i] current. Every switch lands on the agent pane
// in normal: OpenCode on screen, LazyAI keeps the keys until i / Enter. The
// stream's viewers (diff, show set, shell) stay available under d / s / t.
func (m *Model) switchTo(i int) {
	if i < 0 || i >= len(m.streams) {
		return
	}
	if m.stream != nil && i != m.cur {
		m.last = m.cur
	}
	m.cur = i
	m.stream = m.streams[i]
	m.mode = ModeInteractive
	m.focus = FocusSidebar
	if m.cfg.Notes != nil && m.repo.Main != "" {
		_ = m.cfg.Notes.SetState(m.repo.Main, "last_branch", m.name)
	}
	m.stream.attention = false
	m.stream.showPending = false
	m.help = false
	m.syncKeyboard()
	m.refreshDiff()
	m.refreshShow(true)
}

// streamByToken finds the stream whose child sent an event.
func (m Model) streamByToken(token string) *stream {
	for _, s := range m.streams {
		if s.token == token {
			return s
		}
	}
	return nil
}

// closeStream terminates a workstream's child and removes it. Closing the
// last one quits LazyAI.
func (m *Model) closeStream(i int) tea.Cmd {
	if i < 0 || i >= len(m.streams) {
		return nil
	}
	s := m.streams[i]
	_ = s.term.Close()
	if s.shell != nil {
		_ = s.shell.Close()
	}
	return m.removeStream(i)
}

func (m *Model) removeStream(i int) tea.Cmd {
	m.streams = append(m.streams[:i], m.streams[i+1:]...)
	if len(m.streams) == 0 {
		return tea.Quit
	}
	if m.last > i {
		m.last--
	}
	if m.last >= len(m.streams) {
		m.last = 0
	}
	next := m.cur
	if i < m.cur {
		next--
	} else if i == m.cur {
		next = m.last
		if next == i || next >= len(m.streams) {
			next = min(i, len(m.streams)-1)
		}
	}
	m.stream = nil
	m.cur = -1
	m.switchTo(next)
	return nil
}

// childExited handles an OpenCode process going away.
func (m Model) childExited(token string) (tea.Model, tea.Cmd) {
	for i, s := range m.streams {
		if s.token == token {
			m.notice = fmt.Sprintf("workstream %s exited", s.name)
			cmd := m.removeStream(i)
			return m, cmd
		}
	}
	return m, nil
}

// Navigation -----------------------------------------------------------------

// cycleStream moves to the next/previous workstream, wrapping around.
func (m *Model) cycleStream(delta int) {
	n := len(m.streams)
	if n < 2 {
		return
	}
	m.switchTo(((m.cur+delta)%n + n) % n)
}

// leaderKey interprets the key following Ctrl+Space.
func (m Model) leaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		i := int(key[0] - '1')
		if i >= len(m.streams) {
			m.notice = fmt.Sprintf("no workstream %d", i+1)
			return m, nil
		}
		m.switchTo(i)
	case "l", "right":
		m.cycleStream(1)
	case "h", "left":
		m.cycleStream(-1)
	case "ctrl+@", "ctrl+space":
		if m.last < len(m.streams) && m.last != m.cur {
			m.switchTo(m.last)
		}
	case "w":
		m.openPrompt()
	case "a":
		cmd := m.archiveStream()
		return m, cmd
	case "x":
		return m.requestClose()
	case "z":
		m.zoom = !m.zoom
		m.relayout()
	case "?":
		m.help = !m.help
	}
	return m, nil
}

// requestClose asks for confirmation on the first x and closes on the second.
func (m Model) requestClose() (tea.Model, tea.Cmd) {
	if m.pendingClose == m.name && m.pendingClose != "" {
		m.pendingClose = ""
		cmd := m.closeStream(m.cur)
		return m, cmd
	}
	m.pendingClose = m.name
	m.notice = fmt.Sprintf("close workstream %s and stop its OpenCode? press x again", m.name)
	return m, nil
}

// Worktree prompt ------------------------------------------------------------

// promptStage is where the new-worktree prompt is: typing the branch name,
// or choosing what a brand-new branch starts from.
type promptStage int

const (
	stageName promptStage = iota
	stageBase
)

// openPrompt shows the new-worktree prompt.
func (m *Model) openPrompt() {
	if m.repo.Top == "" {
		m.notice = "not a git repository"
		return
	}
	m.prompting = true
	m.promptStage = stageName
	m.prompt.SetValue("")
	m.prompt.Focus()
	m.refilter()
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(false) // the prompt owns the keyboard, even from interactive
	}
}

// closePrompt hides the prompt and hands the keyboard back per the mode.
func (m *Model) closePrompt() {
	m.prompting = false
	m.syncKeyboard()
}

// mainBranch is the branch checked out in the repository's main worktree.
func (m Model) mainBranch() string {
	if info, err := git.Inspect(m.repo.Main); err == nil && info.Branch != "" {
		return info.Branch
	}
	return "main"
}

// promptKey routes keys to the worktree prompt while it is open.
func (m Model) promptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.promptStage == stageBase {
		switch key {
		case "esc", "ctrl+c":
			m.promptStage = stageName // back to the name, keep what was typed
			return m, nil
		case "m", "1", "enter":
			return m.createWorktree(strings.TrimSpace(m.prompt.Value()), m.mainBranch())
		case "c", "2":
			return m.createWorktree(strings.TrimSpace(m.prompt.Value()), m.repo.Branch)
		}
		return m, nil
	}
	switch key {
	case "esc", "ctrl+c":
		m.closePrompt()
		return m, nil
	case "down", "ctrl+n", "tab":
		if len(m.matches) > 0 {
			m.matchSel = (m.matchSel + 1) % len(m.matches)
		}
		return m, nil
	case "up", "ctrl+p", "shift+tab":
		if m.matchSel >= 0 {
			m.matchSel--
		}
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.prompt.Value())
		if m.matchSel >= 0 && m.matchSel < len(m.matches) {
			name = m.matches[m.matchSel].name
		} else if len(m.matches) == 1 {
			name = m.matches[0].name
		}
		if err := git.ValidateBranch(name); err != nil {
			m.notice = err.Error()
			return m, nil
		}
		// An existing workstream on that branch: just go there.
		for i, s := range m.streams {
			if s.name == name {
				m.closePrompt()
				m.switchTo(i)
				return m, nil
			}
		}
		// Existing branch (dormant worktree or plain branch): nothing to base
		// it on, open it. A brand-new branch asks what to start from.
		if git.BranchExists(m.root, name) {
			return m.createWorktree(name, "")
		}
		m.promptStage = stageBase
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	m.refilter()
	return m, cmd
}

// candidate is one branch the prompt can complete to.
type candidate struct {
	name string
	kind string // "open" workstream · "dormant" worktree · "branch"
}

// candidates gathers everything the typed name could refer to, in the order
// shown when the query is empty: running workstreams, dormant worktrees,
// then the remaining local branches.
func (m Model) candidates() []candidate {
	seen := map[string]bool{}
	var out []candidate
	add := func(name, kind string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, candidate{name, kind})
	}
	for _, s := range m.streams {
		add(s.name, "open")
	}
	for _, d := range m.dormantWorktrees() {
		add(d.Branch, "dormant")
	}
	if m.repo.Main != "" {
		if branches, err := git.Branches(m.repo.Main); err == nil {
			for _, b := range branches {
				add(b, "branch")
			}
		}
	}
	return out
}

// refilter recomputes the live match list from the typed text.
func (m *Model) refilter() {
	all := m.candidates()
	names := make([]string, len(all))
	byName := map[string]candidate{}
	for i, c := range all {
		names[i] = c.name
		byName[c.name] = c
	}
	m.matches = m.matches[:0]
	for _, n := range fuzzy.Rank(strings.TrimSpace(m.prompt.Value()), names) {
		m.matches = append(m.matches, byName[n])
	}
	if m.matchSel >= len(m.matches) {
		m.matchSel = len(m.matches) - 1
	}
	if strings.TrimSpace(m.prompt.Value()) == "" {
		m.matchSel = -1 // nothing typed: list only, no preselection
	}
}

// createWorktree makes (or reuses) the worktree for name, starting a new
// branch from base, and opens a workstream in it.
func (m Model) createWorktree(name, base string) (tea.Model, tea.Cmd) {
	path, created, err := git.EnsureWorktree(m.root, name, base)
	if err != nil {
		m.notice = err.Error()
		m.promptStage = stageName
		return m, nil
	}
	m.closePrompt()
	if _, err := m.addStream(path, name); err != nil {
		m.notice = "worktree ready at " + path + " but OpenCode failed to start: " + err.Error()
		return m, nil
	}
	verb := "opened"
	if created {
		verb = "created"
		if base != "" {
			verb += " (from " + base + ")"
		}
	}
	m.notice = fmt.Sprintf("%s worktree %s · workstream %d", verb, name, m.cur+1)
	return m, nil
}
