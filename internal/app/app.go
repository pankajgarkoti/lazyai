// Package app is the Bubble Tea root model: a lazygit-style layout with a
// sidebar on the left and, on the right, either the live OpenCode terminal
// (interactive mode) or LazyAI's own viewers (diff / show modes).
//
// LazyAI hosts any number of workstreams: one OpenCode child per git worktree,
// each with its own ledger, mode and viewers. Exactly one is current; the
// others keep running underneath.
package app

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"lazyai/internal/activity"
	"lazyai/internal/config"
	"lazyai/internal/diff"
	"lazyai/internal/git"
	"lazyai/internal/highlight"
	"lazyai/internal/hooks"
	"lazyai/internal/input"
	"lazyai/internal/notes"
	"lazyai/internal/show"
	"lazyai/internal/terminal"
)

// Mode is the outer modal state of a workstream.
type Mode int

const (
	ModeInteractive Mode = iota
	ModeTerminal
	ModeDiff
	ModeShow
)

func (m Mode) String() string {
	switch m {
	case ModeInteractive:
		return "INTERACTIVE"
	case ModeTerminal:
		return "TERM"
	case ModeDiff:
		return "DIFF"
	case ModeShow:
		return "SHOW"
	}
	return "?"
}

// live reports whether the mode shows a live child terminal (OpenCode or the
// shell) whose keyboard ownership depends on focus.
func (m Mode) live() bool { return m == ModeInteractive || m == ModeTerminal }

// Focus says which pane owns j/k in non-interactive modes.
type Focus int

const (
	FocusSidebar Focus = iota
	FocusContent
)

// Messages crossing goroutine boundaries.
type (
	// ScreenDirtyMsg says some child's screen may have changed.
	ScreenDirtyMsg struct{}
	// ChildExitedMsg says the child identified by Token has exited.
	ChildExitedMsg struct {
		Token string
		Shell bool // the workstream's terminal, not its OpenCode
		Err   error
	}
	// EscapeMsg is a lone ESC captured while the child owned the keyboard.
	EscapeMsg struct{}
	// QuitMsg requests application exit.
	QuitMsg struct{}
	// HookMsg carries an event from an OpenCode plugin instance.
	HookMsg struct{ Event hooks.Event }
	// ZoomMsg toggles the sidebar (Ctrl+Z from any mode).
	ZoomMsg struct{}
	// LeaderMsg is Ctrl+Space pressed while the child owned the keyboard; the
	// next key arrives as a KeyMsg and is interpreted as a leader command.
	LeaderMsg struct{}
	// TickMsg advances the working spinner while any stream has an active
	// tool call. Ticks stop by themselves when nothing is working.
	TickMsg struct{}
)

const spinnerInterval = 120 * time.Millisecond

func tickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return TickMsg{} })
}

// Launcher starts an OpenCode child rooted at dir with the given screen size
// and returns it together with the hook token that identifies its events.
type Launcher func(dir string, w, h int) (*terminal.Terminal, string, error)

// ShellLauncher starts the user's shell in dir for the t (terminal) mode. The
// token identifies the owning workstream in ChildExitedMsg{Shell: true}.
type ShellLauncher func(dir, token string, w, h int) (*terminal.Terminal, error)

// NotesStore is LazyAI's durable per-repo state: show notes, the worktrees
// it opened (and which are dormant), and small key/value state.
type NotesStore interface {
	Record(root, branch, sessionID string, set show.Set) error
	UpsertWorktree(repo, branch, path string, linked bool) error
	SetWorktreeIdentity(repo, branch, nickname, description string) error
	SetDormant(repo, branch string, dormant bool) error
	Worktrees(repo string) ([]notes.Worktree, error)
	SetState(repo, key, value string) error
}

// ConfigLoader reads the project configuration (.lazyai/config.yaml). It is
// called at startup and on the explicit reload command.
type ConfigLoader func() (config.Config, []string, error)

// Config wires the model to the process.
type Config struct {
	Root          string
	Width, Height int // initial terminal size (0 = unknown yet)
	Launch        Launcher
	LaunchShell   ShellLauncher    // nil disables t
	Notes         NotesStore       // nil disables persistence
	SetForward    func(bool)       // route raw keys to the child (true) or to LazyAI
	SetChild      func(input.Sink) // retarget raw keys to another child
	LoadConfig    ConfigLoader     // nil: no project configuration
}

// working reports whether any tool call is in flight on the stream.
func (s *stream) working() bool { return len(s.active) > 0 }

// stream is one workstream: a worktree, its OpenCode child and all per-stream
// UI state. Model embeds a pointer to the current one so view and key code
// address it as m.mode, m.ledger, ...
type stream struct {
	name        string // branch (or directory name): the identity git knows
	nickname    string // what the user calls it; "" falls back to name
	description string // optional reminder of what the workstream is for
	root        string
	token       string
	term        *terminal.Terminal
	shell       *terminal.Terminal // t mode, started lazily
	ledger      *activity.Ledger
	repo        git.Info // zero when root is not a git checkout

	mode  Mode
	focus Focus

	pluginOK  bool
	active    map[string]bool   // in-flight tool calls by call id
	attention bool              // OpenCode is waiting on the user (permission/question)
	unseen    bool              // background output / show set not yet looked at
	freestyle bool              // strict contracts bypassed for this stream
	draft     map[string]string // last contract values typed for this stream

	// Diff state
	fileSel    int
	fileOffset int
	diffPath   string
	diffRes    diff.Result
	diffView   viewport.Model
	diffOld    []highlight.Line // highlighted baseline, indexed by old line-1
	diffNew    []highlight.Line // highlighted current file, indexed by new line-1
	diffPad    int              // rows inserted before hunks (reason float)

	// Show state
	showSet    *show.Set
	showSel    int
	showOffset int
	showLines  []string
	showHL     []highlight.Line
	showErr    error
	showView   viewport.Model
	showSeq    uint64
	loadedShow string // abs path currently loaded into showLines
	showTarget int    // selected location used for the current viewport position
}

// Model is the root application model.
type Model struct {
	*stream // current workstream

	streams []*stream
	cur     int // index into streams
	last    int // previously current stream, for Ctrl+Space Ctrl+Space

	cfg Config

	zoom        bool // sidebar hidden, content pane takes the full width
	help        bool // keymap float shown instead of the content pane
	confirmQuit bool // quit confirmation owns input until y / n / Esc

	prompting   bool // workstream form open
	promptStage promptStage
	prompt      textinput.Model // branch
	nick        textinput.Model // nickname (identity stage)
	desc        textinput.Model // description (identity stage)
	field       int             // focused identity field: 0 nickname, 1 description
	editing     bool            // identity stage edits the current stream instead of creating
	matches     []candidate     // live fuzzy matches for the typed name
	matchSel    int             // selected match, -1 = none (use the typed text)

	leader       bool   // Ctrl+Space pressed; next key is a workstream command
	pendingClose string // stream name awaiting a second "x"

	project    config.Config // validated .lazyai/config.yaml (zero when absent/invalid)
	configErr  string        // persistent configuration error shown in the status bar
	configWarn string
	contract   *contractForm // open strict-entry form
	setup      *setupRequest // agent setup awaiting confirmation

	spin    int  // spinner frame
	ticking bool // a TickMsg is scheduled

	width, height int
	sidebarWidth  int

	notice string // transient status-bar message
}

// New constructs the root model and launches the first workstream.
func New(cfg Config) (Model, error) {
	m := Model{
		cfg:          cfg,
		sidebarWidth: 34,
		prompt:       newPrompt("branch name", 120),
		nick:         newPrompt("nickname", nicknameMax),
		desc:         newPrompt("what is this workstream for? (optional)", descriptionMax),
		width:        cfg.Width,
		height:       cfg.Height,
	}
	m.reloadConfig()
	if _, err := m.addStream(cfg.Root, ""); err != nil {
		return Model{}, err
	}
	return m, nil
}

func newPrompt(placeholder string, limit int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = limit
	ti.Width = 40
	return ti
}

// reloadConfig (re)reads the project configuration. Failure disables strict
// entry and leaves a persistent error; it never keeps a stale contract.
func (m *Model) reloadConfig() {
	m.project, m.configErr, m.configWarn = config.Config{}, "", ""
	if m.cfg.LoadConfig == nil {
		return
	}
	cfg, warnings, err := m.cfg.LoadConfig()
	if err != nil {
		// The status bar has little room: drop the project prefix from paths.
		m.configErr = strings.ReplaceAll(err.Error(), m.cfg.Root+string(os.PathSeparator), "")
		return
	}
	m.project = cfg
	if len(warnings) > 0 {
		m.configWarn = strings.Join(warnings, "; ")
	}
}

// strictActive reports whether instruction entry for the current stream goes
// through a contract form.
func (m Model) strictActive() bool {
	return m.stream != nil && m.project.Interactive.Strict && m.configErr == "" && !m.freestyle
}

// focusAgent is the single way into typing at OpenCode: in strict mode it
// opens the contract form, otherwise it hands the pane the keyboard.
func (m *Model) focusAgent() {
	if m.strictActive() {
		m.openContract()
		return
	}
	m.enter(ModeInteractive)
}

func (m Model) Init() tea.Cmd { return nil }

// ValidateShow is wired into the hook server so a bad show_locations payload
// fails the tool call inside OpenCode instead of being silently dropped. It
// takes the stream's root explicitly because the server runs off-model: a
// Bubble Tea model is a value and a captured copy would not see later streams.
func ValidateShow(root string, ev hooks.Event) error {
	_, err := show.Validate(root, ev.Title, toInputs(ev.Locations))
	return err
}

func toInputs(locs []hooks.Location) []show.Input {
	in := make([]show.Input, 0, len(locs))
	for _, l := range locs {
		in = append(in, show.Input{Path: l.Path, Line: l.Line, Column: l.Column, Text: l.Text})
	}
	return in
}

// Layout geometry --------------------------------------------------------

const (
	statusHeight = 1
	borderCells  = 2 // left + right (or top + bottom)
)

func (m Model) rightInner() (w, h int) {
	w = m.width - m.sidebarWidth - borderCells
	if m.zoom {
		w = m.width - borderCells
	}
	h = m.height - statusHeight - borderCells
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// Update ------------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		return m, nil

	case ZoomMsg:
		m.zoom = !m.zoom
		m.relayout()
		return m, nil

	case LeaderMsg:
		m.leader = true
		return m, nil

	case TickMsg:
		for _, s := range m.streams {
			if s.working() {
				m.spin++
				return m, tickCmd()
			}
		}
		m.ticking = false
		return m, nil

	case ScreenDirtyMsg:
		return m, nil

	case ChildExitedMsg:
		if msg.Shell {
			m.shellExited(msg.Token)
			return m, nil
		}
		return m.childExited(msg.Token)

	case QuitMsg:
		m.openQuit()
		return m, nil

	case EscapeMsg:
		if m.confirmQuit {
			m.closeQuit()
			return m, nil
		}
		if m.setup != nil {
			return m.setupKey("esc")
		}
		if m.contract != nil {
			m.closeContract()
			return m, nil
		}
		if m.prompting {
			m.closePrompt()
			return m, nil
		}
		// Esc inside OpenCode / the shell focuses out: the pane stays fully
		// visible and LazyAI owns the keys until i / t / Enter.
		if m.mode.live() && m.focus == FocusContent {
			m.focus = FocusSidebar
			m.syncKeyboard()
		}
		return m, nil

	case HookMsg:
		cmd := m.applyHook(msg.Event)
		return m, cmd

	case tea.KeyMsg:
		m.notice = ""
		switch {
		case m.confirmQuit:
			return m.quitKey(msg.String())
		case msg.String() == "ctrl+q":
			m.openQuit()
			return m, nil
		case m.setup != nil:
			return m.setupKey(msg.String())
		case m.contract != nil:
			return m.contractKey(msg)
		case m.prompting:
			return m.promptKey(msg)
		case m.leader:
			m.leader = false
			return m.leaderKey(msg)
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		m.notice = ""
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m *Model) openQuit() {
	m.confirmQuit = true
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(false)
	}
}

func (m *Model) closeQuit() {
	m.confirmQuit = false
	m.syncKeyboard()
}

func (m Model) quitKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "Y":
		return m, tea.Quit
	case "n", "N", "esc", "ctrl+c":
		m.closeQuit()
	}
	return m, nil
}

// relayout pushes the current geometry to every child and the viewers.
func (m *Model) relayout() {
	w, h := m.rightInner()
	for _, s := range m.streams {
		s.term.Resize(w, h)
		if s.shell != nil {
			s.shell.Resize(w, h)
		}
		s.diffView.Width, s.diffView.Height = w, h
		s.showView.Width, s.showView.Height = w, h
		s.diffPath = "" // re-render at the new width
	}
	m.refreshDiff()
	m.refreshShow(true)
	m.ensureSidebarSelectionVisible()
}

// callID identifies a tool call across its before/after events. Old plugins
// send no id; the tool name then stands in so a pair still cancels out.
func callID(ev hooks.Event) string {
	if ev.CallID != "" {
		return ev.CallID
	}
	return "tool:" + ev.Tool
}

// applyHook routes a plugin event to the workstream that sent it. The
// returned command starts the spinner when work begins.
func (m *Model) applyHook(ev hooks.Event) tea.Cmd {
	s := m.streamByToken(ev.Token)
	if s == nil {
		if ev.Reply != nil {
			ev.Reply <- hooks.Reply{Err: errUnknownWorkstream}
		}
		return nil
	}
	// Operate on that stream as if it were current, then restore.
	saved := m.stream
	m.stream = s
	defer func() { m.stream = saved }()
	isCur := s == saved
	var cmd tea.Cmd

	switch ev.Type {
	case "hello":
		s.pluginOK = true
	case "tool.before":
		if s.active == nil {
			s.active = map[string]bool{}
		}
		s.active[callID(ev)] = true
		s.attention = false
		if !m.ticking {
			m.ticking = true
			cmd = tickCmd()
		}
	case "tool.after":
		delete(s.active, callID(ev))
	case "idle":
		s.active = nil // clears calls whose after-event was lost
	case "attention":
		s.attention = true
	case "file.before":
		s.ledger.Snapshot(ev.Path)
	case "file.read":
		s.ledger.MarkRead(ev.Path)
	case "file.write":
		s.ledger.MarkWritten(ev.Path)
		if rel, _, ok := s.ledger.Rel(ev.Path); ok && rel == s.diffPath {
			s.diffPath = "" // force recompute
		}
		if s.loadedShow != "" && sameFile(s.loadedShow, ev.Path) {
			s.loadedShow = ""
		}
		if !isCur {
			s.unseen = true
		}
	case "setup":
		m.applySetup(ev)
	case "show":
		set, err := show.Validate(s.root, ev.Title, toInputs(ev.Locations))
		if err != nil {
			if isCur {
				m.notice = "show rejected: " + err.Error()
			}
			break
		}
		s.showSeq++
		set.Sequence = s.showSeq
		s.showSet = &set
		if m.cfg.Notes != nil {
			if err := m.cfg.Notes.Record(s.root, s.repo.Branch, ev.SessionID, set); err != nil && isCur {
				m.notice = "notes: " + err.Error()
			}
		}
		s.showSel = 0
		s.showOffset = 0
		s.loadedShow = ""
		s.showTarget = -1
		for _, loc := range set.Locations {
			s.ledger.MarkShown(loc.Abs, loc.Note)
		}
		if isCur {
			m.enter(ModeShow)
		} else {
			// Flag it so the strip shows there is something to look at; s
			// opens the set once you switch there.
			s.unseen = true
		}
	}
	m.clampFileSel()
	m.refreshDiff()
	m.refreshShow(false)
	m.refreshRepo()
	return cmd
}

func sameFile(a, b string) bool {
	sa, ea := os.Stat(a)
	sb, eb := os.Stat(b)
	if ea != nil || eb != nil {
		return a == b
	}
	return os.SameFile(sa, sb)
}

// enter switches the current workstream's mode and keyboard ownership.
// Live modes (interactive, terminal) are entered focused: their child gets
// the keys. Diff needs real changes; Show needs a set; t needs a shell.
func (m *Model) enter(mode Mode) {
	switch mode {
	case ModeShow:
		if m.showSet == nil {
			m.notice = "nothing shown yet"
			return
		}
	case ModeDiff:
		if m.changedCount() == 0 {
			m.notice = "no changes to diff yet"
			return
		}
	case ModeTerminal:
		if m.shell == nil {
			if m.cfg.LaunchShell == nil {
				m.notice = "terminal not available"
				return
			}
			w, h := m.rightInner()
			sh, err := m.cfg.LaunchShell(m.root, m.token, w, h)
			if err != nil {
				m.notice = "shell: " + err.Error()
				return
			}
			m.shell = sh
		}
	}
	if mode.live() {
		m.focus = FocusContent
	} else if m.mode.live() {
		m.focus = FocusSidebar
	}
	m.mode = mode
	m.syncKeyboard()
	m.refreshDiff()
	m.refreshShow(false)
}

// normal reports the focused-out state: a live pane (OpenCode or the shell)
// is on screen and LazyAI owns the keys.
func (m Model) normal() bool { return m.mode.live() && m.focus == FocusSidebar }

// liveTerm is the child whose screen the live mode shows.
func (m Model) liveTerm() *terminal.Terminal {
	if m.mode == ModeTerminal && m.shell != nil {
		return m.shell
	}
	return m.term
}

// syncKeyboard points raw input at the current live child when it owns the
// keys (live mode with content focus).
func (m *Model) syncKeyboard() {
	if m.cfg.SetChild != nil {
		m.cfg.SetChild(m.liveTerm())
	}
	if m.cfg.SetForward != nil {
		m.cfg.SetForward(!m.prompting && !m.confirmQuit && m.contract == nil && m.setup == nil && m.mode.live() && m.focus == FocusContent)
	}
}

// displayName is what the strip shows: the nickname, falling back to the
// branch.
func (s *stream) displayName() string {
	if s.nickname != "" {
		return s.nickname
	}
	return s.name
}

// shellExited clears a finished shell and drops its workstream to normal.
func (m *Model) shellExited(token string) {
	s := m.streamByToken(token)
	if s == nil {
		return
	}
	s.shell = nil
	if s.mode == ModeTerminal {
		s.mode = ModeInteractive
		s.focus = FocusSidebar
		if s == m.stream {
			m.syncKeyboard()
		}
	}
}

// refreshRepo re-reads branch / worktree facts for the status bar.
func (m *Model) refreshRepo() {
	if info, err := git.Inspect(m.root); err == nil {
		m.repo = info
	} else {
		m.repo = git.Info{}
	}
}

// Diff helpers ------------------------------------------------------------

func (m *Model) clampFileSel() {
	n := m.ledger.Len()
	if m.fileSel >= n {
		m.fileSel = n - 1
	}
	if m.fileSel < 0 {
		m.fileSel = 0
	}
	m.ensureSidebarSelectionVisible()
}

func (m Model) selectedEntry() (activity.Entry, bool) {
	entries := m.ledger.Entries()
	if len(entries) == 0 || m.fileSel >= len(entries) {
		return activity.Entry{}, false
	}
	return entries[m.fileSel], true
}

// refreshDiff recomputes the diff for the selected file when the selection or
// the file changed. Cheap enough to call liberally.
func (m *Model) refreshDiff() {
	if m.mode != ModeDiff {
		return
	}
	e, ok := m.selectedEntry()
	if !ok {
		m.diffPath = ""
		m.diffRes = diff.Result{}
		m.diffOld, m.diffNew = nil, nil
		m.diffView.SetContent("")
		return
	}
	if e.Path == m.diffPath {
		return
	}
	m.diffPath = e.Path
	base, existed, known := m.ledger.Baseline(e.Path)
	cur, err := os.ReadFile(e.Abs)
	exists := err == nil
	switch {
	case !known:
		m.diffRes = diff.Result{Path: e.Path, Note: "not modified during this session"}
	case known && existed && base == nil:
		m.diffRes = diff.Result{Path: e.Path, Note: "modified, but no pre-edit snapshot was captured"}
	default:
		m.diffRes = diff.Unified(e.Path, base, existed, cur, exists)
	}
	m.diffOld, m.diffNew = nil, nil
	if len(m.diffRes.Lines) > 0 {
		m.diffOld = highlight.File(e.Path, expandTabs(string(base)))
	}
	if exists && !m.diffRes.Binary {
		// Also used to show the current source when there is nothing to diff.
		m.diffNew = highlight.File(e.Path, expandTabs(string(cur)))
	}
	w, _ := m.rightInner()
	reason := e.Reason
	if len(m.diffRes.Lines) == 0 {
		reason = "" // nothing changed; the Show note is not a change reason
	}
	m.diffPad = diffPadRows(reason, w)
	m.diffView.SetContent(renderDiff(m.diffRes, m.diffOld, m.diffNew, reason, w))
	m.diffView.GotoTop()
}

// currentHunk is the hunk under the view (content focus) or the first one.
func (m Model) currentHunk() int {
	if len(m.diffRes.Hunks) == 0 {
		return -1
	}
	if m.focus == FocusContent {
		if h := m.diffRes.HunkAt(m.diffView.YOffset - m.diffPad); h >= 0 {
			return h
		}
	}
	return 0
}

// Show helpers ------------------------------------------------------------

func (m Model) selectedLocation() (show.Location, bool) {
	if m.showSet == nil || m.showSel < 0 || m.showSel >= len(m.showSet.Locations) {
		return show.Location{}, false
	}
	return m.showSet.Locations[m.showSel], true
}

// refreshShow loads the selected location's source and centers on it. When
// force is false it keeps the scroll position if the same file is loaded.
func (m *Model) refreshShow(force bool) {
	if m.mode != ModeShow {
		return
	}
	loc, ok := m.selectedLocation()
	if !ok {
		return
	}
	w, h := m.rightInner()
	reload := force || m.loadedShow != loc.Abs
	recenter := reload || m.showTarget != m.showSel
	if reload {
		m.showLines, m.showErr = show.Source(loc.Abs)
		m.showHL = highlight.File(loc.Path, strings.Join(m.showLines, "\n"))
		m.loadedShow = loc.Abs
	}
	total := 0
	if m.showSet != nil {
		total = len(m.showSet.Locations)
	}
	m.showView.SetContent(renderSource(m.showHL, m.showErr, loc, m.showSel+1, total, w))
	m.showTarget = m.showSel
	if !recenter {
		return
	}
	// Center like `zz` in Vim. Content row 0 is the file header, so source
	// line N sits at row N.
	target := loc.Line - h/2
	if target < 0 {
		target = 0
	}
	m.showView.SetYOffset(target)
}

// Reference ---------------------------------------------------------------

// reference builds the text to paste into OpenCode's prompt for the current
// selection, then returns to interactive mode.
func (m *Model) reference() {
	var ref string
	switch m.mode {
	case ModeShow:
		loc, ok := m.selectedLocation()
		if !ok {
			return
		}
		ref = loc.Reference()
	case ModeDiff:
		e, ok := m.selectedEntry()
		if !ok {
			return
		}
		reason := e.Reason
		if reason == "" {
			reason = "current session diff"
		}
		hunk := m.currentHunk()
		pos := e.Path
		if hunk >= 0 {
			pos = m.diffRes.Reference(hunk)
		}
		ref = "[" + pos + " — " + reason + "]"
	default:
		return
	}
	if err := m.term.Paste(ref + " "); err != nil {
		m.notice = "paste failed: " + err.Error()
		return
	}
	m.focusAgent()
}
