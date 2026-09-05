package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"lazyai/internal/terminal"
)

const mouseWheelRows = 3

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirmQuit {
		return m.quitMouse(msg)
	}
	if m.prompting {
		return m.promptMouse(msg)
	}
	if m.help {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.help = false
		}
		return m, nil
	}

	rw, rh := m.rightInner()
	contentX := 1
	if !m.zoom {
		contentX = m.sidebarWidth + 1
	}
	if msg.X >= contentX && msg.X < contentX+rw && msg.Y >= 1 && msg.Y < 1+rh {
		if m.mode.live() {
			m.focus = FocusContent
			m.syncKeyboard()
			m.forwardMouse(msg, msg.X-contentX, msg.Y-1)
			return m, nil
		}
		m.focus = FocusContent
		vp := &m.diffView
		if m.mode == ModeShow {
			vp = &m.showView
		}
		var cmd tea.Cmd
		*vp, cmd = vp.Update(msg)
		return m, cmd
	}

	if m.zoom || msg.X < 1 || msg.X >= m.sidebarWidth-1 || msg.Y < 1 || msg.Y >= 1+rh {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	m.focus = FocusSidebar
	m.syncKeyboard()

	if msg.Y >= 2 && msg.Y < 2+len(m.streams) && msg.Button == tea.MouseButtonLeft {
		m.switchTo(msg.Y - 2)
		return m, nil
	}

	firstRow := len(m.streams) + 4
	if msg.Y < firstRow {
		return m, nil
	}
	rows := m.sidebarRows()
	if rows == 0 || msg.Y >= firstRow+rows {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.moveSelection(-mouseWheelRows)
	case tea.MouseButtonWheelDown:
		m.moveSelection(mouseWheelRows)
	case tea.MouseButtonLeft:
		offset := m.fileOffset
		if m.mode == ModeShow {
			offset = m.showOffset
		}
		m.selectIndex(offset + msg.Y - firstRow)
	}
	return m, nil
}

func (m Model) forwardMouse(msg tea.MouseMsg, x, y int) {
	m.liveTerm().SendMouse(mouseEvent(msg, x, y))
}

func mouseEvent(msg tea.MouseMsg, x, y int) terminal.Mouse {
	action := terminal.MousePress
	if msg.Action == tea.MouseActionRelease {
		action = terminal.MouseRelease
	} else if msg.Action == tea.MouseActionMotion {
		action = terminal.MouseMotion
	}
	return terminal.Mouse{
		X: x, Y: y, Button: vt.MouseButton(msg.Button), Action: action,
		Shift: msg.Shift, Alt: msg.Alt, Ctrl: msg.Ctrl,
	}
}

func (m Model) quitMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	contentX := 1
	if !m.zoom {
		contentX = m.sidebarWidth + 1
	}
	rw, rh := m.rightInner()
	if msg.X < contentX || msg.X >= contentX+rw || msg.Y < 1 || msg.Y >= 1+rh {
		return m, nil
	}
	rows := m.renderQuitPrompt(rw)
	if msg.Y-1 >= len(rows) {
		return m, nil
	}
	line := ansi.Strip(rows[msg.Y-1])
	for _, choice := range []struct {
		label string
		quit  bool
	}{{"y yes", true}, {"n no", false}} {
		start, end, ok := labelCellBounds(line, choice.label)
		if ok && msg.X-contentX >= start && msg.X-contentX < end {
			if choice.quit {
				return m, tea.Quit
			}
			m.closeQuit()
			break
		}
	}
	return m, nil
}

func (m Model) promptMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button == tea.MouseButtonRight {
		if m.promptStage == stageBase {
			m.promptStage = stageName
		} else {
			m.closePrompt()
		}
		return m, nil
	}
	rw, rh := m.rightInner()
	contentX := 1
	if !m.zoom {
		contentX = m.sidebarWidth + 1
	}
	if msg.X < contentX || msg.X >= contentX+rw || msg.Y < 1 || msg.Y >= 1+rh {
		return m, nil
	}
	if m.promptStage == stageBase {
		return m.baseChoiceMouse(msg, contentX, rw)
	}
	if len(m.matches) == 0 {
		return m, nil
	}
	matchRows := m.renderMatches(rw)
	firstRow := 1 + len(m.renderPrompt(rw)) - len(matchRows)
	if msg.Y < firstRow || msg.Y >= firstRow+min(len(m.matches), 8) {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.matchSel = max(-1, m.matchSel-1)
	case tea.MouseButtonWheelDown:
		m.matchSel = min(len(m.matches)-1, m.matchSel+1)
	case tea.MouseButtonLeft:
		m.matchSel = msg.Y - firstRow
		return m.promptKey(tea.KeyMsg{Type: tea.KeyEnter})
	}
	return m, nil
}

func (m Model) baseChoiceMouse(msg tea.MouseMsg, contentX, width int) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	name := strings.TrimSpace(m.prompt.Value())
	choices := []struct {
		label string
		base  string
	}{
		{"m " + m.mainBranch() + " (main)", m.mainBranch()},
		{"c " + m.repo.Branch + " (current)", m.repo.Branch},
	}
	rows := m.renderPrompt(width)
	if msg.Y-1 >= len(rows) {
		return m, nil
	}
	line := ansi.Strip(rows[msg.Y-1])
	for _, choice := range choices {
		start, end, ok := labelCellBounds(line, choice.label)
		if ok && msg.X-contentX >= start && msg.X-contentX < end {
			return m.createWorktree(name, choice.base)
		}
	}
	return m, nil
}

func labelCellBounds(line, label string) (start, end int, ok bool) {
	i := strings.Index(line, label)
	if i < 0 {
		return 0, 0, false
	}
	start = ansi.StringWidth(line[:i])
	return start, start + ansi.StringWidth(label), true
}

func (m Model) sidebarRows() int {
	_, h := m.rightInner()
	return max(0, h-len(m.streams)-3)
}

func (m *Model) ensureSidebarSelectionVisible() {
	rows := m.sidebarRows()
	if rows == 0 {
		m.fileOffset = 0
		m.showOffset = 0
		return
	}
	ensureVisible := func(selected, count int, offset *int) {
		if selected < *offset {
			*offset = selected
		}
		if selected >= *offset+rows {
			*offset = selected - rows + 1
		}
		*offset = max(0, min(*offset, max(0, count-rows)))
	}
	ensureVisible(m.fileSel, m.ledger.Len(), &m.fileOffset)
	showCount := 0
	if m.showSet != nil {
		showCount = len(m.showSet.Locations)
	}
	ensureVisible(m.showSel, showCount, &m.showOffset)
}
