// Command lazyai wraps a real OpenCode terminal session in a lazygit-style
// interface for browsing the files the agent touches.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"lazyai/internal/app"
	"lazyai/internal/git"
	"lazyai/internal/hooks"
	"lazyai/internal/input"
	"lazyai/internal/integration"
	"lazyai/internal/notes"
	"lazyai/internal/terminal"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lazyai:", err)
		os.Exit(1)
	}
}

type launchOptions struct {
	dir      string
	bin      string
	worktree string
	base     string
	child    []string
}

func parseLaunchOptions(args []string) (launchOptions, error) {
	var opts launchOptions
	fs := flag.NewFlagSet("lazyai", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.dir, "dir", ".", "project directory to open OpenCode in")
	fs.StringVar(&opts.bin, "opencode", "opencode", "opencode executable")
	fs.StringVar(&opts.worktree, "worktree", "", "run in a git worktree for this branch under <repo>/"+git.WorktreeDir+" (created if needed)")
	fs.StringVar(&opts.base, "base", "", "start point for a new --worktree branch (default: HEAD)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: lazyai [--dir DIR] [--worktree BRANCH [--base REF]] [--opencode BIN] [-- opencode args...]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return launchOptions{}, err
	}
	opts.child = fs.Args()
	return opts, nil
}

func prepareRoot(opts launchOptions) (string, error) {
	absDir, err := filepath.Abs(opts.dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	if opts.worktree != "" {
		path, created, err := git.EnsureWorktree(absDir, opts.worktree, opts.base)
		if err != nil {
			return "", err
		}
		if created {
			fmt.Fprintf(os.Stderr, "lazyai: created worktree %s at %s\n", opts.worktree, path)
		}
		absDir = path
	}
	return absDir, nil
}

func runDirect(args []string) error {
	opts, err := parseLaunchOptions(args)
	if err != nil {
		return err
	}
	childArgs := opts.child

	absDir, err := prepareRoot(opts)
	if err != nil {
		return err
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("lazyai must run in an interactive terminal")
	}

	// Initial geometry so the child starts at the right size.
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}

	// Hook channel + bundled plugin/skill handed to OpenCode as an extra
	// config directory (additive to the user's own configuration).
	hookSrv, err := hooks.Listen()
	if err != nil {
		return fmt.Errorf("hook listener: %w", err)
	}
	defer hookSrv.Close()

	cfgDir, err := integration.DefaultDir()
	if err != nil {
		return err
	}
	if _, err := integration.Materialize(cfgDir); err != nil {
		return err
	}

	baseEnv := append(os.Environ(),
		"TERM=xterm-256color",
		"LAZYAI=1",
		"OPENCODE_CONFIG_DIR="+cfgDir,
	)

	// We own raw mode on the real terminal because Bubble Tea reads from a
	// pipe that the input router feeds.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck

	hostR, hostW := io.Pipe()
	router := input.New(os.Stdin, discardSink{}, hostW)

	// Child processes can emit output while app.New is still constructing the
	// model. Buffer those messages until the Bubble Tea program exists.
	var p *tea.Program
	programMessages := make(chan tea.Msg, 64)
	var children []*terminal.Terminal
	var roots sync.Map // hook token -> workstream root, for show validation
	hookSrv.Validate = func(ev hooks.Event) error {
		root, ok := roots.Load(ev.Token)
		if !ok {
			return fmt.Errorf("unknown workstream")
		}
		return app.ValidateShow(root.(string), ev)
	}
	defer func() {
		for _, c := range children {
			c.Close()
		}
	}()
	launch := func(dir string, w, h int) (*terminal.Terminal, string, error) {
		token := hookSrv.Register()
		roots.Store(token, dir)
		child, err := terminal.Start(terminal.Options{
			Command: opts.bin,
			Args:    childArgs,
			Dir:     dir,
			Env:     append(append([]string{}, baseEnv...), hookSrv.EnvFor(token)...),
			Width:   w,
			Height:  h,
		})
		if err != nil {
			hookSrv.Unregister(token)
			return nil, "", fmt.Errorf("start opencode: %w", err)
		}
		children = append(children, child)
		go func() {
			for {
				select {
				case <-child.Dirty:
					programMessages <- app.ScreenDirtyMsg{}
				case <-child.Exited:
					hookSrv.Unregister(token)
					roots.Delete(token)
					programMessages <- app.ChildExitedMsg{Token: token, Err: child.Err()}
					return
				}
			}
		}()
		return child, token, nil
	}

	// Per-workstream shell for t mode.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	launchShell := func(dir, token string, w, h int) (*terminal.Terminal, error) {
		child, err := terminal.Start(terminal.Options{
			Command: shell,
			Args:    []string{"-l"},
			Dir:     dir,
			Env:     append(append([]string{}, baseEnv...), "LAZYAI_WORKTREE="+dir),
			Width:   w,
			Height:  h,
		})
		if err != nil {
			return nil, err
		}
		children = append(children, child)
		go func() {
			for {
				select {
				case <-child.Dirty:
					programMessages <- app.ScreenDirtyMsg{}
				case <-child.Exited:
					programMessages <- app.ChildExitedMsg{Token: token, Shell: true, Err: child.Err()}
					return
				}
			}
		}()
		return child, nil
	}

	// Durable per-repo state: show notes and worktrees.
	dbPath, err := notes.DefaultPath()
	if err != nil {
		return err
	}
	store, err := notes.Open(dbPath)
	if err != nil {
		return fmt.Errorf("notes db: %w", err)
	}
	defer store.Close()

	model, err := app.New(app.Config{
		Root:        absDir,
		Width:       cols,
		Height:      rows,
		Launch:      launch,
		LaunchShell: launchShell,
		Notes:       store,
		SetForward:  router.SetForward,
		SetChild:    router.SetChild,
	})
	if err != nil {
		return err
	}
	p = tea.NewProgram(model, tea.WithInput(hostR), tea.WithOutput(os.Stdout), tea.WithAltScreen(), tea.WithMouseCellMotion())
	go func() {
		for msg := range programMessages {
			p.Send(msg)
		}
	}()
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, syscall.SIGTERM)
	defer signal.Stop(terminate)
	programDone := make(chan struct{})
	go func() {
		select {
		case <-terminate:
			p.Quit()
		case <-programDone:
		}
	}()

	router.OnEscape = func() { p.Send(app.EscapeMsg{}) }
	router.OnQuit = func() { p.Send(app.QuitMsg{}) }
	router.OnZoom = func() { p.Send(app.ZoomMsg{}) }
	router.OnLeader = func() { p.Send(app.LeaderMsg{}) }
	go func() { _ = router.Run() }()

	go func() {
		for ev := range hookSrv.Events {
			programMessages <- app.HookMsg{Event: ev}
		}
	}()

	_, err = p.Run()
	close(programDone)
	_ = hostW.Close()
	return err
}

// discardSink swallows raw input until the first workstream takes over.
type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }
