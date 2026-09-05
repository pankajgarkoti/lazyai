// Package terminal hosts a real child TUI process (OpenCode) inside a
// pseudoterminal and maintains a virtual screen of its output so the host
// application can draw that screen inside one of its own panes.
package terminal

import (
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Terminal is a child process attached to a PTY whose output is fed into a
// VT emulator. All exported methods are safe for concurrent use.
type Terminal struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	pty  *os.File
	emu  *vt.Emulator
	w, h int

	// Dirty is signalled (non-blocking, capacity 1) whenever the screen may
	// have changed.
	Dirty chan struct{}
	// Exited is closed when the child process has exited. Err holds the
	// process error afterwards.
	Exited chan struct{}
	err    error
}

// Options configure Start.
type Options struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Width   int
	Height  int
}

// Start launches the child in a PTY of the requested size.
func Start(opts Options) (*Terminal, error) {
	if opts.Width < 2 {
		opts.Width = 80
	}
	if opts.Height < 2 {
		opts.Height = 24
	}
	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(opts.Height), Cols: uint16(opts.Width)})
	if err != nil {
		return nil, err
	}

	t := &Terminal{
		cmd:    cmd,
		pty:    f,
		emu:    vt.NewEmulator(opts.Width, opts.Height),
		w:      opts.Width,
		h:      opts.Height,
		Dirty:  make(chan struct{}, 1),
		Exited: make(chan struct{}),
	}

	go t.pump()
	go t.replies()
	go t.wait()
	return t, nil
}

// pump copies PTY output into the emulator.
func (t *Terminal) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			t.mu.Lock()
			_, _ = t.emu.Write(buf[:n])
			t.mu.Unlock()
			t.signal()
		}
		if err != nil {
			return
		}
	}
}

// replies forwards emulator-generated responses (device attributes, cursor
// position reports, ...) back to the child so terminal queries work.
func (t *Terminal) replies() {
	buf := make([]byte, 4096)
	for {
		n, err := t.emu.Read(buf)
		if n > 0 {
			_, _ = t.pty.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) wait() {
	err := t.cmd.Wait()
	t.mu.Lock()
	t.err = err
	t.mu.Unlock()
	close(t.Exited)
	t.signal()
}

func (t *Terminal) signal() {
	select {
	case t.Dirty <- struct{}{}:
	default:
	}
}

// Err returns the process exit error once Exited is closed.
func (t *Terminal) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Write sends raw input bytes to the child.
func (t *Terminal) Write(p []byte) (int, error) {
	return t.pty.Write(p)
}

// Paste sends text wrapped in bracketed-paste markers so the child treats it
// as a single paste rather than typed keystrokes.
func (t *Terminal) Paste(text string) error {
	_, err := io.WriteString(t.pty, "\x1b[200~"+text+"\x1b[201~")
	return err
}

// Resize changes both the PTY window size and the emulator dimensions.
func (t *Terminal) Resize(w, h int) {
	if w < 2 || h < 2 {
		return
	}
	t.mu.Lock()
	if w == t.w && h == t.h {
		t.mu.Unlock()
		return
	}
	t.w, t.h = w, h
	t.emu.Resize(w, h)
	t.mu.Unlock()
	_ = pty.Setsize(t.pty, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	t.signal()
}

// Size returns the current emulator dimensions.
func (t *Terminal) Size() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w, t.h
}

// Close terminates the child if still running and releases the PTY.
func (t *Terminal) Close() error {
	select {
	case <-t.Exited:
	default:
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
	}
	return t.pty.Close()
}

// Snapshot renders the current screen as rows of ANSI-styled text. Each row is
// exactly the emulator width in cells and ends with a style reset. The cursor
// cell is drawn in reverse video when showCursor is true.
func (t *Terminal) Snapshot(showCursor bool) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return render(t.emu, showCursor, false)
}

// SnapshotFaded renders the screen dimmed, for an out-of-focus pane, without
// a cursor.
func (t *Terminal) SnapshotFaded() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return render(t.emu, false, true)
}
