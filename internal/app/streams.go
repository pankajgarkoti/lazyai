package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lazyai/internal/activity"
	"lazyai/internal/fuzzy"
	"lazyai/internal/git"
	"lazyai/internal/hooks"
	"lazyai/internal/notes"
)

// Workstream identity limits (packet: nickname 60 display cells, description
// 240 characters).
const (
	nicknameMax    = 60
	descriptionMax = 240
	setupBatchMax  = 10
)

var errUnknownWorkstream = errors.New("unknown workstream")

// WorkstreamResult is what OpenWorkstream reports for one specification. It
// is also the per-item JSON result returned to the setup_workstreams tool.
type WorkstreamResult struct {
	Branch   string `json:"branch"`
	Nickname string `json:"nickname"`
	Root     string `json:"root,omitempty"`
	Created  bool   `json:"created"`  // a new worktree (and maybe branch) was made
	Launched bool   `json:"launched"` // OpenCode is running there now
	Error    string `json:"error,omitempty"`
}

// Workstream lifecycle -------------------------------------------------------

// addStream launches an OpenCode child in root and makes it current.
func (m *Model) addStream(root, name string) (*stream, error) {
	return m.addStreamOpts(root, name, "", "", true)
}

// addStreamOpts launches an OpenCode child in root. With activate it becomes
// current and, unless strict entry intervenes, focused: a fresh workstream is
// there to be typed into. Without activate it is appended in the background.
func (m *Model) addStreamOpts(root, name, nickname, description string, activate bool) (*stream, error) {
	w, h := m.rightInner()
	if m.width == 0 { // size unknown until the first WindowSizeMsg
		w, h = 80, 24
	}
	term, token, err := m.cfg.Launch(root, w, h)
	if err != nil {
		return nil, err
	}
	s := &stream{
		root:       root,
		token:      token,
		term:       term,
		ledger:     activity.New(root),
		mode:       ModeInteractive,
		focus:      FocusSidebar,
		diffView:   viewport.New(w, h),
		showView:   viewport.New(w, h),
		showTarget: -1,
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
	s.nickname, s.description = strings.TrimSpace(nickname), strings.TrimSpace(description)
	if s.nickname == "" || s.description == "" {
		if stored, ok := m.storedIdentity(s.repo.Main, name); ok {
			if s.nickname == "" {
				s.nickname = stored.Nickname
			}
			if s.description == "" {
				s.description = stored.Description
			}
		}
	}
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
	if !activate && m.stream != nil {
		m.registerWorktreeFor(s)
		return s, nil
	}
	m.switchTo(idx)
	m.registerWorktree()
	m.focusAgent()
	return s, nil
}

// storedIdentity looks up the persisted nickname/description for a branch.
func (m Model) storedIdentity(repo, branch string) (notes.Worktree, bool) {
	if m.cfg.Notes == nil || repo == "" {
		return notes.Worktree{}, false
	}
	list, err := m.cfg.Notes.Worktrees(repo)
	if err != nil {
		return notes.Worktree{}, false
	}
	for _, w := range list {
		if w.Branch == branch {
			return w, true
		}
	}
	return notes.Worktree{}, false
}

// registerWorktree records the current stream's worktree as active.
func (m *Model) registerWorktree() { m.registerWorktreeFor(m.stream) }

func (m *Model) registerWorktreeFor(s *stream) {
	if m.cfg.Notes == nil || s == nil || s.repo.Main == "" {
		return
	}
	_ = m.cfg.Notes.UpsertWorktree(s.repo.Main, s.name, s.root, s.repo.Linked)
}

// setIdentity updates a stream's nickname/description in memory and on disk.
func (m *Model) setIdentity(s *stream, nickname, description string) error {
	nickname, description = strings.TrimSpace(nickname), strings.TrimSpace(description)
	if err := validateIdentity(nickname, description); err != nil {
		return err
	}
	s.nickname, s.description = nickname, description
	if m.cfg.Notes != nil && s.repo.Main != "" {
		return m.cfg.Notes.SetWorktreeIdentity(s.repo.Main, s.name, nickname, description)
	}
	return nil
}

func validateIdentity(nickname, description string) error {
	if strings.TrimSpace(nickname) == "" {
		return errors.New("nickname is required")
	}
	if lipgloss.Width(nickname) > nicknameMax {
		return fmt.Errorf("nickname is longer than %d cells", nicknameMax)
	}
	if utf8.RuneCountInString(description) > descriptionMax {
		return fmt.Errorf("description is longer than %d characters", descriptionMax)
	}
	if strings.ContainsAny(nickname+description, "\n\r\t\x1b") {
		return errors.New("identity must be a single line of text")
	}
	return nil
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
// Visiting clears "unseen"; attention stays until OpenCode resolves it.
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
	m.stream.unseen = false
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

// streamByBranch finds a running workstream on a branch.
func (m Model) streamByBranch(branch string) (int, *stream) {
	for i, s := range m.streams {
		if s.name == branch {
			return i, s
		}
	}
	return -1, nil
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
			m.notice = fmt.Sprintf("workstream %s exited", s.displayName())
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
	case "e":
		m.openIdentityEdit()
	case "a":
		cmd := m.archiveStream()
		return m, cmd
	case "x":
		return m.requestClose()
	case "z":
		m.zoom = !m.zoom
		m.relayout()
	case "f":
		m.toggleFreestyle()
	case "c":
		m.reloadConfig()
		switch {
		case m.configErr != "":
			m.notice = "config: " + m.configErr
		case m.project.Loaded:
			m.notice = "config reloaded from " + m.project.Path
		default:
			m.notice = "no .lazyai/config.yaml; defaults apply"
		}
	case "?":
		m.help = !m.help
	}
	return m, nil
}

// toggleFreestyle bypasses (or re-enables) strict entry for this stream only.
func (m *Model) toggleFreestyle() {
	if !m.project.Interactive.Strict || m.configErr != "" {
		m.notice = "strict mode is not enabled in .lazyai/config.yaml"
		return
	}
	m.freestyle = !m.freestyle
	if m.freestyle {
		m.notice = "freestyle: this workstream accepts free-form input · ctrl+space f re-enables contracts"
	} else {
		m.notice = "strict: i opens the contract form"
	}
}

// requestClose asks for confirmation on the first x and closes on the second.
func (m Model) requestClose() (tea.Model, tea.Cmd) {
	if m.pendingClose == m.name && m.pendingClose != "" {
		m.pendingClose = ""
		cmd := m.closeStream(m.cur)
		return m, cmd
	}
	m.pendingClose = m.name
	m.notice = fmt.Sprintf("close workstream %s and stop its OpenCode? press x again", m.displayName())
	return m, nil
}

// Shared workstream command -------------------------------------------------

// OpenWorkstream is the one path that turns a specification into a running
// workstream: it validates identity, resolves or creates the worktree,
// persists nickname/description, launches OpenCode and reports what happened.
// Both the w form and the agent's setup_workstreams tool call it. With
// activate the new workstream becomes current; otherwise it stays in the
// background and the user's focus is untouched.
func (m *Model) OpenWorkstream(spec hooks.WorkstreamSpec, activate bool) (WorkstreamResult, error) {
	res := WorkstreamResult{Branch: spec.Branch}
	fail := func(err error) (WorkstreamResult, error) {
		res.Error = err.Error()
		return res, err
	}
	if err := git.ValidateBranch(spec.Branch); err != nil {
		return fail(err)
	}
	nickname := strings.TrimSpace(spec.Nickname)
	description := strings.TrimSpace(spec.Description)
	if nickname == "" || description == "" {
		if stored, ok := m.storedIdentity(m.repo.Main, spec.Branch); ok {
			if nickname == "" {
				nickname = stored.Nickname
			}
			if description == "" {
				description = stored.Description
			}
		}
	}
	if nickname == "" {
		nickname = spec.Branch
	}
	if err := validateIdentity(nickname, description); err != nil {
		return fail(err)
	}
	res.Nickname = nickname
	// Already running: just go there. Metadata is not overwritten.
	if i, s := m.streamByBranch(spec.Branch); s != nil {
		res.Root, res.Launched = s.root, true
		if activate {
			m.switchTo(i)
		}
		return res, nil
	}
	if spec.Base != "" && !git.BranchExists(m.root, spec.Base) {
		return fail(fmt.Errorf("base branch %q does not exist", spec.Base))
	}
	path, created, err := git.EnsureWorktree(m.root, spec.Branch, spec.Base)
	if err != nil {
		return fail(err)
	}
	res.Root, res.Created = path, created
	// Identity is recorded before launch so it survives a failed start.
	if m.cfg.Notes != nil && m.repo.Main != "" {
		_ = m.cfg.Notes.SetWorktreeIdentity(m.repo.Main, spec.Branch, nickname, description)
	}
	if _, err := m.addStreamOpts(path, spec.Branch, nickname, description, activate); err != nil {
		return fail(fmt.Errorf("worktree ready at %s but OpenCode failed to start: %w", path, err))
	}
	res.Launched = true
	return res, nil
}

// Agent setup ---------------------------------------------------------------

// setupRequest is a validated setup_workstreams batch waiting for the user's
// confirmation (more than one new branch would be created).
type setupRequest struct {
	ev       hooks.Event
	specs    []hooks.WorkstreamSpec
	newCount int
	deadline time.Time
	stream   *stream // the requesting workstream (repository scope)
}

// applySetup validates a setup_workstreams request against the requesting
// stream's repository and either runs it or asks the user first. Nothing is
// mutated unless the whole batch is valid.
func (m *Model) applySetup(ev hooks.Event) {
	reply := func(r hooks.Reply) {
		if ev.Reply != nil {
			select {
			case ev.Reply <- r:
			default:
			}
		}
	}
	if m.repo.Top == "" {
		reply(hooks.Reply{Err: errors.New("the workstream is not inside a git repository")})
		return
	}
	specs, newCount, err := m.validateSetup(ev.Workstreams)
	if err != nil {
		reply(hooks.Reply{Err: err})
		return
	}
	req := &setupRequest{ev: ev, specs: specs, newCount: newCount, stream: m.stream,
		deadline: time.Now().Add(hooks.DefaultRequestTimeout - 10*time.Second)}
	if newCount > 1 {
		if m.setup != nil {
			reply(hooks.Reply{Err: errors.New("another setup request is waiting for confirmation")})
			return
		}
		m.setup = req
		if m.cfg.SetForward != nil {
			m.cfg.SetForward(false)
		}
		return
	}
	m.runSetup(req)
}

func (m Model) validateSetup(in []hooks.WorkstreamSpec) ([]hooks.WorkstreamSpec, int, error) {
	if len(in) == 0 {
		return nil, 0, errors.New("no workstreams requested")
	}
	if len(in) > setupBatchMax {
		return nil, 0, fmt.Errorf("at most %d workstreams per request", setupBatchMax)
	}
	seen := map[string]bool{}
	newCount := 0
	out := make([]hooks.WorkstreamSpec, 0, len(in))
	for i, spec := range in {
		spec.Branch = strings.TrimSpace(spec.Branch)
		spec.Nickname = strings.TrimSpace(spec.Nickname)
		spec.Description = strings.TrimSpace(spec.Description)
		spec.Base = strings.TrimSpace(spec.Base)
		if err := git.ValidateBranch(spec.Branch); err != nil {
			return nil, 0, fmt.Errorf("workstream %d: %w", i+1, err)
		}
		if seen[spec.Branch] {
			return nil, 0, fmt.Errorf("workstream %d: branch %q is listed twice", i+1, spec.Branch)
		}
		seen[spec.Branch] = true
		if spec.Nickname == "" {
			return nil, 0, fmt.Errorf("workstream %d (%s): nickname is required", i+1, spec.Branch)
		}
		if err := validateIdentity(spec.Nickname, spec.Description); err != nil {
			return nil, 0, fmt.Errorf("workstream %d (%s): %w", i+1, spec.Branch, err)
		}
		if spec.Base != "" && !git.BranchExists(m.root, spec.Base) {
			return nil, 0, fmt.Errorf("workstream %d (%s): base branch %q does not exist", i+1, spec.Branch, spec.Base)
		}
		if _, running := m.streamByBranch(spec.Branch); running == nil && !git.BranchExists(m.root, spec.Branch) {
			newCount++
		}
		out = append(out, spec)
	}
	return out, newCount, nil
}

// runSetup opens every workstream in request order, in the background, and
// answers the tool call with per-item results.
func (m *Model) runSetup(req *setupRequest) {
	saved := m.stream
	m.stream = req.stream
	results := make([]WorkstreamResult, 0, len(req.specs))
	for _, spec := range req.specs {
		r, _ := m.OpenWorkstream(spec, false)
		results = append(results, r)
	}
	m.stream = saved
	if req.ev.Reply != nil {
		select {
		case req.ev.Reply <- hooks.Reply{Result: map[string]any{"workstreams": results}}:
		default:
		}
	}
	ok := 0
	for _, r := range results {
		if r.Launched {
			ok++
		}
	}
	m.notice = fmt.Sprintf("agent set up %d/%d workstreams", ok, len(results))
}

// setupKey answers the confirmation overlay.
func (m Model) setupKey(key string) (tea.Model, tea.Cmd) {
	req := m.setup
	if req == nil {
		return m, nil
	}
	switch key {
	case "y", "Y", "enter":
		m.setup = nil
		if time.Now().After(req.deadline) {
			m.notice = "setup request expired; ask the agent again"
			m.syncKeyboard()
			return m, nil
		}
		m.runSetup(req)
		m.syncKeyboard()
	case "n", "N", "esc", "ctrl+c":
		m.setup = nil
		if req.ev.Reply != nil {
			select {
			case req.ev.Reply <- hooks.Reply{Err: errors.New("the user declined to create the workstreams")}:
			default:
			}
		}
		m.notice = "declined agent setup"
		m.syncKeyboard()
	}
	return m, nil
}

// Workstream form -----------------------------------------------------------

// promptStage is where the workstream form is: typing the branch name,
// naming it (nickname + description), or choosing what a brand-new branch
// starts from.
type promptStage int

const (
	stageName promptStage = iota
	stageIdentity
	stageBase
)

// openPrompt shows the workstream form.
func (m *Model) openPrompt() {
	if m.repo.Top == "" {
		m.notice = "not a git repository"
		return
	}
	m.prompting = true
	m.editing = false
	m.promptStage = stageName
	m.prompt.SetValue("")
	m.prompt.Focus()
	m.nick.Blur()
	m.desc.Blur()
	m.refilter()
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(false) // the prompt owns the keyboard, even from interactive
	}
}

// openIdentityEdit opens the identity stage for the current workstream so its
// nickname/description can be changed without touching git.
func (m *Model) openIdentityEdit() {
	if m.stream == nil {
		return
	}
	m.prompting = true
	m.editing = true
	m.prompt.SetValue(m.name)
	m.enterIdentity(m.displayName(), m.description)
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(false)
	}
}

// enterIdentity moves the form to the identity stage with prefilled values.
func (m *Model) enterIdentity(nickname, description string) {
	m.promptStage = stageIdentity
	m.field = 0
	m.nick.SetValue(nickname)
	m.nick.CursorEnd()
	m.desc.SetValue(description)
	m.desc.CursorEnd()
	m.prompt.Blur()
	m.nick.Focus()
	m.desc.Blur()
}

// closePrompt hides the form and hands the keyboard back per the mode.
func (m *Model) closePrompt() {
	m.prompting = false
	m.editing = false
	m.nick.Blur()
	m.desc.Blur()
	m.syncKeyboard()
}

// mainBranch is the branch checked out in the repository's main worktree.
func (m Model) mainBranch() string {
	if info, err := git.Inspect(m.repo.Main); err == nil && info.Branch != "" {
		return info.Branch
	}
	return "main"
}

// promptKey routes keys to the workstream form while it is open.
func (m Model) promptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch m.promptStage {
	case stageBase:
		switch key {
		case "esc", "ctrl+c":
			m.enterIdentity(m.nick.Value(), m.desc.Value()) // back to naming, keep what was typed
			return m, nil
		case "m", "1", "enter":
			return m.createWorktree(strings.TrimSpace(m.prompt.Value()), m.mainBranch())
		case "c", "2":
			return m.createWorktree(strings.TrimSpace(m.prompt.Value()), m.repo.Branch)
		}
		return m, nil
	case stageIdentity:
		return m.identityKey(msg)
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
		m.prompt.SetValue(name)
		// An existing workstream on that branch: just go there.
		if i, s := m.streamByBranch(name); s != nil {
			m.closePrompt()
			m.switchTo(i)
			return m, nil
		}
		// Existing branch (dormant worktree or plain branch): its identity is
		// already known (or defaults to the branch); open it. A brand-new
		// branch is named first, then asks what to start from.
		if git.BranchExists(m.root, name) {
			return m.createWorktree(name, "")
		}
		m.enterIdentity(name, "")
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	m.refilter()
	return m, cmd
}

// identityKey handles the nickname/description stage.
func (m Model) identityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		if m.editing {
			m.closePrompt()
			return m, nil
		}
		m.promptStage = stageName
		m.nick.Blur()
		m.desc.Blur()
		m.prompt.Focus()
		m.refilter()
		return m, nil
	case "tab", "down":
		m.setField(1)
		return m, nil
	case "shift+tab", "up":
		m.setField(0)
		return m, nil
	case "enter":
		if err := validateIdentity(m.nick.Value(), m.desc.Value()); err != nil {
			m.notice = err.Error()
			m.setField(0)
			return m, nil
		}
		if m.editing {
			if err := m.setIdentity(m.stream, m.nick.Value(), m.desc.Value()); err != nil {
				m.notice = err.Error()
				return m, nil
			}
			m.closePrompt()
			m.notice = "renamed workstream to " + m.displayName()
			return m, nil
		}
		m.promptStage = stageBase
		return m, nil
	}
	var cmd tea.Cmd
	if m.field == 0 {
		m.nick, cmd = m.nick.Update(msg)
	} else {
		m.desc, cmd = m.desc.Update(msg)
	}
	return m, cmd
}

func (m *Model) setField(i int) {
	m.field = i
	if i == 0 {
		m.nick.Focus()
		m.desc.Blur()
	} else {
		m.nick.Blur()
		m.desc.Focus()
	}
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
// branch from base, and opens a workstream in it with the identity typed in
// the form. On failure the form stays open, populated, with the error.
func (m Model) createWorktree(name, base string) (tea.Model, tea.Cmd) {
	spec := hooks.WorkstreamSpec{Branch: name, Base: base}
	if m.promptStage != stageName {
		spec.Nickname, spec.Description = m.nick.Value(), m.desc.Value()
	}
	res, err := m.OpenWorkstream(spec, true)
	if err != nil {
		m.notice = err.Error()
		if m.promptStage == stageBase {
			m.enterIdentity(m.nick.Value(), m.desc.Value())
		}
		return m, nil
	}
	m.closePrompt()
	verb := "opened"
	if res.Created {
		verb = "created"
		if base != "" {
			verb += " (from " + base + ")"
		}
	}
	m.notice = fmt.Sprintf("%s worktree %s · workstream %d", verb, name, m.cur+1)
	return m, nil
}
