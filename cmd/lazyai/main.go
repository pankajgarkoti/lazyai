// Command lazyai wraps a real OpenCode terminal session in a lazygit-style
// interface for browsing the files the agent touches.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyai:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("dir", ".", "project directory to open OpenCode in")
	bin := flag.String("opencode", "opencode", "opencode executable")
	worktree := flag.String("worktree", "", "run in a git worktree for this branch under <repo>/"+git.WorktreeDir+" (created if needed)")
	base := flag.String("base", "", "start point for a new --worktree branch (default: HEAD)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: lazyai [--dir DIR] [--worktree BRANCH [--base REF]] [--opencode BIN] [-- opencode args...]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	childArgs := flag.Args()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	// OpenCode reports paths under the *resolved* directory (e.g. /private/tmp
	// for /tmp on macOS); the ledger must use the same root or every hook path
	// looks like it lives outside the workspace.
	if resolved, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = resolved
	}
	if *worktree != "" {
		path, created, err := git.EnsureWorktree(absDir, *worktree, *base)
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(os.Stderr, "lazyai: created worktree %s at %s\n", *worktree, path)
		}
		absDir = path
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

	// p is assigned below; the launcher's goroutines only run after p.Run.
	var p *tea.Program
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
			Command: *bin,
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
					p.Send(app.ScreenDirtyMsg{})
				case <-child.Exited:
					hookSrv.Unregister(token)
					roots.Delete(token)
					p.Send(app.ChildExitedMsg{Token: token, Err: child.Err()})
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
					p.Send(app.ScreenDirtyMsg{})
				case <-child.Exited:
					p.Send(app.ChildExitedMsg{Token: token, Shell: true, Err: child.Err()})
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

	router.OnEscape = func() { p.Send(app.EscapeMsg{}) }
	router.OnQuit = func() { p.Send(app.QuitMsg{}) }
	router.OnZoom = func() { p.Send(app.ZoomMsg{}) }
	router.OnLeader = func() { p.Send(app.LeaderMsg{}) }
	go func() { _ = router.Run() }()

	go func() {
		for ev := range hookSrv.Events {
			p.Send(app.HookMsg{Event: ev})
		}
	}()

	_, err = p.Run()
	_ = hostW.Close()
	return err
}

// discardSink swallows raw input until the first workstream takes over.
type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }
