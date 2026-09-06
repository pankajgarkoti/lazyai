package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleKey is only reached when LazyAI owns the keyboard (non-chat modes);
// in chat mode raw bytes go straight to OpenCode.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Bubble Tea batches rapidly typed runes into one message ("jjj"); apply
	// them one at a time so each keystroke keeps its meaning.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
		var cur tea.Model = m
		var cmd tea.Cmd
		for _, r := range msg.Runes {
			cur, cmd = cur.(Model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
		}
		return cur, cmd
	}
	key := msg.String()
	if key != "x" {
		m.pendingClose = ""
	}
	return m.applyKey(key)
}

// applyKey dispatches a resolved key in a LazyAI-owned mode.
func (m Model) applyKey(key string) (tea.Model, tea.Cmd) {
	if m.help {
		if key == "?" || key == "esc" {
			m.help = false
		}
		return m, nil
	}
	// Global (non-interactive) keys.
	switch key {
	case "ctrl+@":
		m.leader = true
		return m, nil
	case "x":
		return m.requestClose()
	case "a":
		cmd := m.archiveStream()
		return m, cmd
	case "ctrl+q":
		m.openQuit()
		return m, nil
	case "i":
		m.focusAgent()
		return m, nil
	case "e":
		m.openIdentityEdit()
		return m, nil
	case "K":
		m.openInfo()
		return m, nil
	case "t":
		m.enter(ModeTerminal)
		return m, nil
	case "d":
		m.enter(ModeDiff)
		return m, nil
	case "s":
		m.enter(ModeShow)
		return m, nil
	case "r":
		m.reference()
		return m, nil
	case "w":
		m.openPrompt()
		return m, nil
	case "z":
		m.zoom = !m.zoom
		m.relayout()
		return m, nil
	case "?":
		m.help = !m.help
		return m, nil
	case "]", "[":
		if m.mode == ModeShow { // quickfix-style ]q / [q from either focus
			if key == "]" {
				m.moveSelection(1)
			} else {
				m.moveSelection(-1)
			}
			return m, nil
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if m.focus == FocusSidebar {
			m.selectIndex(int(key[0] - '1'))
			return m, nil
		}
	case "tab":
		if m.focus == FocusSidebar {
			m.focus = FocusContent
		} else {
			m.focus = FocusSidebar
		}
		m.syncKeyboard()
		return m, nil
	}

	if m.focus == FocusSidebar {
		return m.sidebarKey(key)
	}
	return m.contentKey(key)
}

func (m Model) sidebarKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		// Esc from a viewer's list goes to normal: OpenCode stays visible,
		// still with no input until i.
		if !m.mode.live() {
			m.mode = ModeInteractive
			m.syncKeyboard()
		}
	case "enter":
		if m.mode == ModeInteractive {
			m.focusAgent() // strict mode routes through the contract form
			return m, nil
		}
		m.focus = FocusContent
		m.syncKeyboard() // in normal this hands the live pane the keys
	// In normal (a live pane on screen) the strip is what you browse: j/k
	// move between workstreams and h/l pick a file for d. Inside Diff / Show
	// the list is the subject, so j/k select entries and h/l switch streams.
	case "h", "left":
		if m.mode.live() {
			m.moveSelection(-1)
		} else {
			m.cycleStream(-1)
		}
	case "l", "right":
		if m.mode.live() {
			m.moveSelection(1)
		} else {
			m.cycleStream(1)
		}
	case "j", "down":
		if m.mode.live() {
			m.cycleStream(1)
		} else {
			m.moveSelection(1)
		}
	case "k", "up":
		if m.mode.live() {
			m.cycleStream(-1)
		} else {
			m.moveSelection(-1)
		}
	case "g":
		m.moveSelection(-1 << 30)
	case "G":
		m.moveSelection(1 << 30)
	}
	return m, nil
}

// selectIndex jumps the sidebar selection to an absolute (clamped) index.
func (m *Model) selectIndex(i int) {
	switch m.mode {
	case ModeDiff, ModeInteractive, ModeTerminal:
		m.fileSel = i
		m.clampFileSel()
		m.refreshDiff()
	case ModeShow:
		m.showSel = 0
		m.moveSelection(i)
	}
	m.ensureSidebarSelectionVisible()
}

func (m *Model) moveSelection(delta int) {
	switch m.mode {
	case ModeDiff, ModeInteractive, ModeTerminal:
		m.fileSel += delta
		m.clampFileSel()
		m.refreshDiff()
	case ModeShow:
		if m.showSet == nil {
			return
		}
		m.showSel += delta
		if m.showSel >= len(m.showSet.Locations) {
			m.showSel = len(m.showSet.Locations) - 1
		}
		if m.showSel < 0 {
			m.showSel = 0
		}
		m.ensureSidebarSelectionVisible()
		m.refreshShow(false)
	}
}

func (m Model) contentKey(key string) (tea.Model, tea.Cmd) {
	vp := &m.diffView
	if m.mode == ModeShow {
		vp = &m.showView
	}
	switch key {
	case "esc", "h":
		m.focus = FocusSidebar
	case "j", "down":
		vp.LineDown(1)
	case "k", "up":
		vp.LineUp(1)
	case "ctrl+d":
		vp.HalfViewDown()
	case "ctrl+u":
		vp.HalfViewUp()
	case "g":
		vp.GotoTop()
	case "G":
		vp.GotoBottom()
	case "]":
		if m.mode == ModeDiff {
			m.jumpHunk(1)
		}
	case "[":
		if m.mode == ModeDiff {
			m.jumpHunk(-1)
		}
	}
	return m, nil
}

// jumpHunk scrolls the diff so the next/previous hunk header is at the top.
func (m *Model) jumpHunk(dir int) {
	if len(m.diffRes.Hunks) == 0 {
		return
	}
	row := m.diffView.YOffset - m.diffPad // hunk indices are diff-line based
	cur := m.diffRes.HunkAt(row)
	next := cur + dir
	if dir > 0 && cur >= 0 && m.diffRes.Hunks[cur].Index < row {
		next = cur + 1 // we're past the current header; advance normally
	}
	if next < 0 {
		next = 0
	}
	if next >= len(m.diffRes.Hunks) {
		next = len(m.diffRes.Hunks) - 1
	}
	m.diffView.SetYOffset(m.diffRes.Hunks[next].Index + m.diffPad)
}
