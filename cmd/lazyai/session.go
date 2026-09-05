package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"lazyai/internal/notes"
	"lazyai/internal/supervisor"
)

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "__direct":
			return runDirect(args[1:])
		case "__supervise":
			return runSupervisor(args[1:])
		case "list":
			return listSessions(args[1:])
		case "stop":
			return stopSession(args[1:])
		}
	}
	return attachSession(args)
}

func attachSession(args []string) error {
	opts, err := parseLaunchOptions(args)
	if err != nil {
		return err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("lazyai must run in an interactive terminal")
	}
	// Resolve identity without applying launch-only worktree mutations.
	root, err := prepareRoot(launchOptions{dir: opts.dir})
	if err != nil {
		return err
	}
	project, err := supervisor.ProjectRoot(root)
	if err != nil {
		return err
	}
	dbPath, err := notes.DefaultPath()
	if err != nil {
		return err
	}
	socket := supervisor.SocketPath(project)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		root, err = prepareRoot(opts)
		if err != nil {
			return err
		}
		if err := startSupervisor(project, root, socket, dbPath, opts, args); err != nil {
			return err
		}
		conn, err = waitForSupervisor(socket, 5*time.Second)
		if err != nil {
			return err
		}
	}

	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		_ = conn.Close()
		return err
	}
	interrupts := make(chan os.Signal, 1)
	clientDone := make(chan struct{})
	interrupted := make(chan struct{})
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(interrupts)
	go func() {
		select {
		case <-interrupts:
			close(interrupted)
			_ = conn.Close()
		case <-clientDone:
		}
	}()
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		close(clientDone)
		_ = conn.Close()
		return err
	}
	restoreTerminal := sync.OnceFunc(func() {
		fmt.Fprint(os.Stdout, "\x1b[?2004l\x1b[?1002l\x1b[?1006l\x1b[?25h\x1b[?1049l")
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	})
	defer restoreTerminal()
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l\x1b[?1002h\x1b[?1006h\x1b[?2004h")

	resizes := make(chan supervisor.Size, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	defer signal.Stop(signals)
	go func() {
		for {
			select {
			case <-clientDone:
				return
			case <-signals:
			}
			w, h, sizeErr := term.GetSize(int(os.Stdout.Fd()))
			if sizeErr != nil {
				continue
			}
			select {
			case resizes <- supervisor.Size{Width: w, Height: h}:
			default:
			}
		}
	}()
	detached, attachErr := supervisor.Attach(conn, os.Stdin, os.Stdout, cols, rows, resizes)
	close(clientDone)
	restoreTerminal()
	select {
	case <-interrupted:
		return nil
	default:
	}
	if detached {
		fmt.Fprintln(os.Stdout, "lazyai: detached; run lazyai again to reattach")
	}
	return attachErr
}

func startSupervisor(project, root, socket, dbPath string, opts launchOptions, originalArgs []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	directArgs := []string{"--dir", root, "--opencode", opts.bin, "--"}
	directArgs = append(directArgs, opts.child...)
	originalJSON, err := json.Marshal(originalArgs)
	if err != nil {
		return err
	}
	internal := []string{"__supervise", "--project", project, "--root", root, "--socket", socket, "--db", dbPath, "--original", string(originalJSON), "--"}
	internal = append(internal, directArgs...)
	cmd := exec.Command(exe, internal...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := os.MkdirAll(supervisor.RuntimeDir(), 0o700); err != nil {
		return err
	}
	logPath := socket + ".log"
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runSupervisor(args []string) error {
	fs := flag.NewFlagSet("lazyai __supervise", flag.ContinueOnError)
	project := fs.String("project", "", "")
	root := fs.String("root", "", "")
	socket := fs.String("socket", "", "")
	dbPath := fs.String("db", "", "")
	original := fs.String("original", "[]", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" || *root == "" || *socket == "" || *dbPath == "" || len(fs.Args()) == 0 {
		return errors.New("invalid supervisor configuration")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var originalArgs []string
	if err := json.Unmarshal([]byte(*original), &originalArgs); err != nil {
		return fmt.Errorf("invalid original arguments: %w", err)
	}
	return supervisor.Serve(supervisor.Config{
		ProjectRoot: *project, RequestedRoot: *root, SocketPath: *socket, DBPath: *dbPath,
		Command: exe, Args: append([]string{"__direct"}, fs.Args()...), OriginalArgs: originalArgs,
	})
}

func waitForSupervisor(socket string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("supervisor did not start: %w (see %s.log)", lastErr, socket)
}

func listSessions(args []string) error {
	fs := flag.NewFlagSet("lazyai list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: lazyai list")
		fmt.Fprintln(os.Stderr, "\nList known project sessions and their current lifecycle status.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lazyai list")
	}
	dbPath, err := notes.DefaultPath()
	if err != nil {
		return err
	}
	store, err := notes.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	sessions, err := store.RuntimeSessions()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tPID\tROOT")
	for _, session := range sessions {
		status := session.Status
		if status == "running" && !socketLive(session.Socket) {
			status = "stale"
			_ = store.MarkRuntimeSession(session.Project, status)
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", status, session.PID, session.Root)
	}
	return w.Flush()
}

func stopSession(args []string) error {
	fs := flag.NewFlagSet("lazyai stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", ".", "project directory to stop")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: lazyai stop [--dir DIR]")
		fmt.Fprintln(os.Stderr, "\nStop the project session and all workstreams, including their OpenCode and shell processes.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: lazyai stop [--dir DIR]")
	}
	project, err := supervisor.ProjectRoot(*dir)
	if err != nil {
		return err
	}
	socket := supervisor.SocketPath(project)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		if supervisor.LockHeld(socket) {
			return fmt.Errorf("LazyAI session for %s is starting or temporarily unavailable", project)
		}
		dbPath, pathErr := notes.DefaultPath()
		if pathErr != nil {
			return pathErr
		}
		store, openErr := notes.Open(dbPath)
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		sessions, listErr := store.RuntimeSessions()
		if listErr != nil {
			return listErr
		}
		for _, session := range sessions {
			if session.Project == project {
				if session.Socket == supervisor.SocketPath(project) {
					_ = os.Remove(session.Socket)
				}
				if err := store.MarkRuntimeSession(project, "stopped"); err != nil {
					return err
				}
				fmt.Fprintf(os.Stdout, "lazyai: stopped LazyAI session for %s\n", project)
				return nil
			}
		}
		return fmt.Errorf("no LazyAI session for %s", project)
	}
	if err := json.NewEncoder(conn).Encode(supervisor.Message{Type: supervisor.MessageStop}); err != nil {
		_ = conn.Close()
		return err
	}
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !socketLive(socket) {
			fmt.Fprintf(os.Stdout, "lazyai: stopped LazyAI session for %s\n", project)
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timed out stopping LazyAI session for %s", project)
}

func socketLive(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	return conn.Close() == nil
}
