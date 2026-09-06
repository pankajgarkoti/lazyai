package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazyai/internal/config"
	"lazyai/internal/theme"
)

// Strict interactive mode: instead of typing free-form at OpenCode, the user
// fills the project's contract template in a form laid over the pane. Submit
// sends one deterministic YAML document as a single paste, then hands the
// keyboard to OpenCode as usual.

// fieldInput is one form field: a single-line input or a textarea.
type fieldInput struct {
	field config.Field
	text  textinput.Model
	area  textarea.Model
	// rows is the number of screen rows the input occupies in the last render;
	// mouse hit testing uses it.
	top, rows int
}

func (f *fieldInput) multiline() bool { return f.field.Type == config.FieldMultiline }

func (f *fieldInput) value() string {
	if f.multiline() {
		return f.area.Value()
	}
	return f.text.Value()
}

func (f *fieldInput) setValue(v string) {
	if f.multiline() {
		f.area.SetValue(v)
		f.area.CursorEnd()
		return
	}
	f.text.SetValue(v)
	f.text.CursorEnd()
}

func (f *fieldInput) focus() {
	if f.multiline() {
		f.area.Focus()
		return
	}
	f.text.Focus()
}

func (f *fieldInput) blur() {
	if f.multiline() {
		f.area.Blur()
		return
	}
	f.text.Blur()
}

// contractForm is the open strict-entry form.
type contractForm struct {
	contract config.Contract
	inputs   []*fieldInput
	focus    int
	invalid  map[string]bool
	// height/width of the last layout, so field geometry follows the pane.
	boxTop, boxW int
}

func newContractForm(c config.Contract, draft map[string]string) *contractForm {
	f := &contractForm{contract: c, invalid: map[string]bool{}}
	for _, fd := range c.Fields {
		in := &fieldInput{field: fd}
		if fd.Type == config.FieldMultiline {
			ta := textarea.New()
			ta.Prompt = ""
			ta.ShowLineNumbers = false
			ta.Placeholder = fd.Label
			ta.CharLimit = 0
			ta.SetHeight(3)
			in.area = ta
		} else {
			ti := textinput.New()
			ti.Prompt = ""
			ti.Placeholder = fd.Label
			ti.CharLimit = 400
			in.text = ti
		}
		if v, ok := draft[fd.Key]; ok {
			in.setValue(v)
		}
		f.inputs = append(f.inputs, in)
	}
	if len(f.inputs) > 0 {
		f.inputs[0].focus()
	}
	return f
}

func (f *contractForm) values() map[string]string {
	out := map[string]string{}
	for _, in := range f.inputs {
		out[in.field.Key] = in.value()
	}
	return out
}

func (f *contractForm) setFocus(i int) {
	if len(f.inputs) == 0 {
		return
	}
	i = ((i % len(f.inputs)) + len(f.inputs)) % len(f.inputs)
	for j, in := range f.inputs {
		if j == i {
			in.focus()
		} else {
			in.blur()
		}
	}
	f.focus = i
}

// layout sizes the inputs to the pane: fields share the rows left after the
// title and footer so a 60x18 terminal still shows every field.
func (f *contractForm) layout(w, h int) {
	inner := min(w-4, 76)
	if inner < 12 {
		inner = max(1, w-2)
	}
	f.boxW = inner
	avail := h - 3 // title, blank, footer
	perField := 1
	if n := len(f.inputs); n > 0 {
		perField = max(1, avail/n-1) // minus the label row
	}
	areaH := min(3, perField)
	used := 0
	for _, in := range f.inputs {
		if in.multiline() {
			in.area.SetWidth(inner - 2)
			in.area.SetHeight(max(1, areaH))
			in.rows = max(1, areaH)
		} else {
			in.text.Width = inner - 3
			in.rows = 1
		}
		used += 1 + in.rows
	}
	boxH := used + 3
	f.boxTop = max(0, (h-boxH)/2)
}

// openContract shows the form for the project's contract, restoring the
// stream's last draft so a cancelled or rejected submission is not lost.
func (m *Model) openContract() {
	c, ok := m.project.Contract()
	if !ok {
		m.notice = "no contract template defined"
		return
	}
	if m.draft == nil {
		m.draft = make(map[string]map[string]string)
	}
	m.contract = newContractForm(c, m.draft[c.Name])
	w, h := m.rightInner()
	m.contract.layout(w, h)
	m.mode = ModeInteractive
	m.focus = FocusSidebar // LazyAI owns the keys while the form is open
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(false)
	}
}

// closeContract hides the form, keeping the draft, and returns to normal.
func (m *Model) closeContract() {
	if m.contract != nil {
		m.draft[m.contract.contract.Name] = m.contract.values()
	}
	m.contract = nil
	m.syncKeyboard()
}

// submitContract validates required fields, sends the rendered YAML as one
// paste followed by Enter and steps into OpenCode.
func (m *Model) submitContract() {
	f := m.contract
	if f == nil {
		return
	}
	values := f.values()
	m.draft[f.contract.Name] = values
	missing := f.contract.Missing(values)
	f.invalid = map[string]bool{}
	for _, k := range missing {
		f.invalid[k] = true
	}
	if len(missing) > 0 {
		for i, in := range f.inputs {
			if f.invalid[in.field.Key] {
				f.setFocus(i)
				break
			}
		}
		m.notice = fmt.Sprintf("required: %s", strings.Join(missing, ", "))
		return
	}
	doc := f.contract.Render(values)
	if err := m.term.Paste(doc); err != nil {
		m.notice = "paste failed: " + err.Error()
		return
	}
	if _, err := m.term.Write([]byte("\r")); err != nil {
		m.notice = "send failed: " + err.Error()
		return
	}
	delete(m.draft, f.contract.Name) // sent; only this template starts blank next time
	m.contract = nil
	m.enter(ModeInteractive)
}

// contractKey routes keys while the form is open.
func (m Model) contractKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.contract
	key := msg.String()
	switch key {
	case "esc", "ctrl+c":
		m.closeContract()
		return m, nil
	case "ctrl+s":
		m.submitContract()
		return m, nil
	case "tab":
		f.setFocus(f.focus + 1)
		return m, nil
	case "shift+tab":
		f.setFocus(f.focus - 1)
		return m, nil
	}
	if len(f.inputs) == 0 {
		return m, nil
	}
	in := f.inputs[f.focus]
	if !in.multiline() {
		switch key {
		case "enter":
			if f.focus == len(f.inputs)-1 {
				m.submitContract()
			} else {
				f.setFocus(f.focus + 1)
			}
			return m, nil
		case "down":
			f.setFocus(f.focus + 1)
			return m, nil
		case "up":
			f.setFocus(f.focus - 1)
			return m, nil
		}
		var cmd tea.Cmd
		in.text, cmd = in.text.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	in.area, cmd = in.area.Update(msg)
	return m, cmd
}

// contractMouse: click a field to focus it, wheel scrolls the focused
// textarea, right click cancels.
func (m Model) contractMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	f := m.contract
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button == tea.MouseButtonRight {
		m.closeContract()
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
	row := msg.Y - 1
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if in := f.inputs[f.focus]; in.multiline() {
			if msg.Button == tea.MouseButtonWheelUp {
				in.area.CursorUp()
			} else {
				in.area.CursorDown()
			}
		}
	case tea.MouseButtonLeft:
		for i, in := range f.inputs {
			if row >= in.top && row < in.top+in.rows+1 { // label row + input rows
				f.setFocus(i)
				return m, nil
			}
		}
		if strings.Contains(ansi.Strip(m.contractFooter()), "ctrl+s") && row == f.boxTop+f.height()-1 {
			m.submitContract()
		}
	}
	return m, nil
}

func (f *contractForm) height() int {
	h := 3
	for _, in := range f.inputs {
		h += 1 + in.rows
	}
	return h
}

func (m Model) contractFooter() string {
	return theme.StatusKey.Render("ctrl+s") + theme.PromptText.Render(" send") + "   " +
		theme.StatusKey.Render("tab") + theme.PromptText.Render(" next field") + "   " +
		theme.StatusKey.Render("esc") + theme.PromptText.Render(" keep draft & close") +
		theme.Dim.Render("   ctrl+space f: freestyle")
}

// renderContract lays the form over the live pane rows, vertically centred,
// so the OpenCode screen stays visible around it.
func (m Model) renderContract(rows []string, w, h int) []string {
	f := m.contract
	f.layout(w, h)
	inner := f.boxW
	pad := strings.Repeat(" ", max(0, (w-inner-2)/2))
	line := func(content string) string {
		content = ansi.Truncate(content, inner-2, "…")
		gap := inner - 2 - lipgloss.Width(content)
		if gap < 0 {
			gap = 0
		}
		return pad + theme.FloatBorder.Render(theme.FloatV) + " " + content + strings.Repeat(" ", gap) + " " + theme.FloatBorder.Render(theme.FloatV)
	}
	title := theme.DiffHeader.Render(theme.IconInfo+" "+f.contract.Title) + theme.Dim.Render("  contract: "+f.contract.Name)
	box := []string{
		pad + theme.FloatBorder.Render(theme.FloatTL+strings.Repeat(theme.FloatH, inner)+theme.FloatTR),
		line(title),
	}
	for i, in := range f.inputs {
		in.top = f.boxTop + len(box)
		label := theme.PromptLabel.Render(in.field.Label)
		if in.field.Required {
			label += theme.Dim.Render(" *")
		}
		if f.invalid[in.field.Key] {
			label += " " + theme.Warn.Render(theme.IconWarn+" required")
		}
		if i == f.focus {
			label = theme.StatusKey.Render("› ") + label
		} else {
			label = "  " + label
		}
		box = append(box, line(label))
		var view string
		if in.multiline() {
			view = in.area.View()
		} else {
			view = in.text.View()
		}
		for _, r := range strings.Split(view, "\n")[:in.rows] {
			box = append(box, line("  "+r))
		}
	}
	box = append(box, line(m.contractFooter()))
	box = append(box, pad+theme.FloatBorder.Render(theme.FloatBL+strings.Repeat(theme.FloatH, inner)+theme.FloatBR))
	for len(rows) < h {
		rows = append(rows, "")
	}
	out := make([]string, len(rows))
	copy(out, rows)
	for i, b := range box {
		if y := f.boxTop + i; y >= 0 && y < len(out) {
			out[y] = ansi.Truncate(b, w, "")
		}
	}
	return out
}

// renderSetupPrompt asks the user to allow an agent batch that would create
// several new branches.
func (m Model) renderSetupPrompt(w int) []string {
	req := m.setup
	rows := []string{theme.DiffHeader.Render(theme.IconWorktree + " agent wants to set up workstreams")}
	var b strings.Builder
	fmt.Fprintf(&b, "The agent in %s asks to open %d workstreams (%d new branches):\n", req.stream.displayName(), len(req.specs), req.newCount)
	for _, s := range req.specs {
		fmt.Fprintf(&b, "%s — %s", s.Branch, s.Nickname)
		if s.Base != "" {
			fmt.Fprintf(&b, " (from %s)", s.Base)
		}
		if s.Description != "" {
			fmt.Fprintf(&b, ": %s", s.Description)
		}
		b.WriteString("\n")
	}
	choices := theme.StatusKey.Render("y") + theme.PromptText.Render(" create") + "   " +
		theme.StatusKey.Render("n") + theme.PromptText.Render(" decline")
	rows = append(rows, renderFloat(theme.IconInfo, theme.DiagInfo, strings.TrimRight(b.String(), "\n"), choices+theme.Dim.Render(" · esc declines"), 1, w)...)
	return rows
}

// setupMouse clicks y / n on the setup prompt.
func (m Model) setupMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
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
	rows := m.renderSetupPrompt(rw)
	if msg.Y-1 >= len(rows) {
		return m, nil
	}
	line := ansi.Strip(rows[msg.Y-1])
	for _, choice := range []struct{ label, key string }{{"y create", "y"}, {"n decline", "n"}} {
		start, end, ok := labelCellBounds(line, choice.label)
		if ok && msg.X-contentX >= start && msg.X-contentX < end {
			return m.setupKey(choice.key)
		}
	}
	return m, nil
}
