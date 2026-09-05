// Package supervisor keeps one LazyAI TUI alive per project while foreground
// terminal clients attach and detach over a local Unix socket.
package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"lazyai/internal/git"
	"lazyai/internal/notes"
	"lazyai/internal/terminal"
)

const (
	MessageAttach = "attach"
	MessageInput  = "input"
	MessageResize = "resize"
	MessageScreen = "screen"
	MessageStop   = "stop"
	MessageExit   = "exit"
	MessageError  = "error"
	MessageDetach = "detach"
)

var ErrAlreadyRunning = errors.New("project supervisor is already running")

// Message is the local client/supervisor wire format.
type Message struct {
	Type   string `json:"type"`
	Data   []byte `json:"data,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Config describes one supervised project TUI.
type Config struct {
	ProjectRoot   string
	RequestedRoot string
	SocketPath    string
	DBPath        string
	Command       string
	Args          []string
	OriginalArgs  []string
	Env           []string
	Width         int
	Height        int
}

// ProjectRoot returns the canonical isolation boundary for dir.
func ProjectRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if info, err := git.Inspect(abs); err == nil && info.Main != "" {
		return info.Main, nil
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// SocketPath returns a short, project-specific Unix socket path.
func SocketPath(project string) string {
	digest := sha256.Sum256([]byte(project))
	return filepath.Join(RuntimeDir(), hex.EncodeToString(digest[:8])+".sock")
}

// RuntimeDir is the user-only directory containing live supervisor sockets.
func RuntimeDir() string {
	if dir := os.Getenv("LAZYAI_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "lazyai-"+strconv.Itoa(os.Getuid()))
}

type client struct {
	conn net.Conn
	mu   sync.Mutex
	enc  *json.Encoder
}

func newClient(conn net.Conn) *client { return &client{conn: conn, enc: json.NewEncoder(conn)} }

func (c *client) send(msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return c.enc.Encode(msg)
}

type inbound struct {
	client *client
	msg    Message
	err    error
}

// Serve runs until the supervised TUI exits or a client requests stop.
func Serve(cfg Config) error {
	if cfg.Width < 2 {
		cfg.Width = 80
	}
	if cfg.Height < 2 {
		cfg.Height = 24
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0o700); err != nil {
		return err
	}
	if len(cfg.SocketPath) >= 100 {
		return fmt.Errorf("runtime socket path is too long: %s", cfg.SocketPath)
	}
	releaseLock, err := acquireLock(cfg.SocketPath)
	if err != nil {
		return err
	}
	defer releaseLock()
	if conn, err := net.DialTimeout("unix", cfg.SocketPath, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}
	if err := os.Remove(cfg.SocketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	term, err := terminal.Start(terminal.Options{
		Command: cfg.Command, Args: cfg.Args, Dir: cfg.RequestedRoot, Env: cfg.Env,
		Width: cfg.Width, Height: cfg.Height,
	})
	if err != nil {
		return err
	}
	defer func() {
		select {
		case <-term.Exited:
			_ = term.Close()
		default:
			forceStopTree(term)
		}
	}()

	store, err := notes.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ln, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(cfg.SocketPath) //nolint:errcheck
	if err := os.Chmod(cfg.SocketPath, 0o600); err != nil {
		return err
	}
	registryArgs := cfg.OriginalArgs
	if registryArgs == nil {
		registryArgs = cfg.Args
	}
	argsJSON, _ := json.Marshal(registryArgs)
	if err := store.UpsertRuntimeSession(notes.RuntimeSession{
		Project: cfg.ProjectRoot, Root: cfg.RequestedRoot, Socket: cfg.SocketPath,
		PID: os.Getpid(), Args: string(argsJSON), Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}

	accepts := make(chan net.Conn)
	acceptErr := make(chan error, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepts <- conn
		}
	}()
	in := make(chan inbound, 32)
	var active *client
	readClient := func(c *client) {
		dec := json.NewDecoder(c.conn)
		for {
			var msg Message
			err := dec.Decode(&msg)
			in <- inbound{client: c, msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}
	snapshot := func(c *client) {
		if c == nil {
			return
		}
		data := "\x1b[H" + strings.Join(term.Snapshot(true), "\r\n")
		if err := c.send(Message{Type: MessageScreen, Data: []byte(data)}); err != nil {
			_ = c.conn.Close()
		}
	}

	stopping := false
	var forceStop <-chan time.Time
	for {
		select {
		case conn := <-accepts:
			c := newClient(conn)
			go readClient(c)
		case event := <-in:
			if event.err != nil {
				if active == event.client {
					active = nil
				}
				_ = event.client.conn.Close()
				continue
			}
			switch event.msg.Type {
			case MessageAttach:
				if active != nil && active != event.client {
					_ = active.send(Message{Type: MessageDetach})
					_ = active.conn.Close()
				}
				active = event.client
				if event.msg.Width > 1 && event.msg.Height > 1 {
					term.Resize(event.msg.Width, event.msg.Height)
				}
				_ = store.TouchRuntimeSession(cfg.ProjectRoot)
				snapshot(active)
			case MessageInput:
				if event.client == active {
					_, _ = term.Write(event.msg.Data)
				}
			case MessageResize:
				if event.client == active && event.msg.Width > 1 && event.msg.Height > 1 {
					term.Resize(event.msg.Width, event.msg.Height)
					snapshot(active)
				}
			case MessageStop:
				if !stopping {
					stopping = true
					_ = store.MarkRuntimeSession(cfg.ProjectRoot, "stopped")
					_ = term.Signal(syscall.SIGTERM)
					forceStop = time.After(2 * time.Second)
				}
			}
		case <-term.Dirty:
			snapshot(active)
		case <-forceStop:
			forceStopTree(term)
			forceStop = nil
		case <-term.Exited:
			status := "exited"
			if stopping {
				status = "stopped"
			}
			_ = store.MarkRuntimeSession(cfg.ProjectRoot, status)
			if active != nil {
				msg := Message{Type: MessageExit}
				if err := term.Err(); err != nil && !stopping {
					msg.Error = err.Error()
				}
				_ = active.send(msg)
			}
			return nil
		case err := <-acceptErr:
			if !errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("accept: %w", err)
			}
			return nil
		}
	}
}

func acquireLock(socket string) (func(), error) {
	path := socket + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, ErrAlreadyRunning
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// LockHeld reports whether a supervisor owns the project startup/runtime lock.
func LockHeld(socket string) bool {
	file, err := os.OpenFile(socket+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func descendantPIDs(root int) []int {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := make(map[int][]int)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr == nil && ppidErr == nil {
			children[ppid] = append(children[ppid], pid)
		}
	}
	var descendants []int
	var walk func(int)
	walk = func(pid int) {
		for _, child := range children[pid] {
			walk(child)
			descendants = append(descendants, child)
		}
	}
	walk(root)
	return descendants
}

func forceStopTree(term *terminal.Terminal) {
	root := term.PID()
	// Signal through os.Process first: once Wait has completed, Signal returns
	// os.ErrProcessDone rather than targeting a potentially reused numeric PID.
	if err := term.Signal(syscall.SIGSTOP); err != nil {
		return
	}
	seen := make(map[int]struct{})
	// Freeze each newly observed generation and continue until the process tree
	// converges. Frozen ancestors cannot create another unseen generation.
	for {
		newChildren := 0
		for _, pid := range descendantPIDs(root) {
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			newChildren++
			_ = syscall.Kill(pid, syscall.SIGSTOP)
		}
		if newChildren == 0 {
			break
		}
	}
	for pid := range seen {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = term.Close()
}
