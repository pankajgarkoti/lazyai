// Package input reads raw bytes from the real terminal and routes them either
// to the embedded child process (verbatim) or to the host Bubble Tea program
// (as key events), depending on which surface currently owns the keyboard.
// While the child owns the keys, Ctrl+] sends it a literal ESC, a lone ESC
// focuses out to LazyAI, and Ctrl+Space / Ctrl+Z / Ctrl+Q are host chords.
//
// Forwarding the original bytes, rather than re-encoding parsed key events,
// guarantees the child sees exactly what a terminal would have sent it.
package input

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// escapeByte is Ctrl+]: the one key that sends a literal ESC into the pane
// while it owns the keyboard. A lone ESC itself is LazyAI's "focus out".
const escapeByte = 0x1d

// Sink receives raw input intended for the child process.
type Sink interface {
	Write(p []byte) (int, error)
}

// Router splits a raw input stream between a child sink and a host writer.
type Router struct {
	src   io.Reader
	child atomic.Pointer[sinkBox]
	host  io.Writer

	// captureNext routes the next chunk to the host regardless of mode: set
	// after the leader key so "Ctrl+Space 2" works while OpenCode owns keys.
	captureNext atomic.Bool

	forward atomic.Bool
	// debug, when non-nil, receives a hex dump of every chunk and where it
	// went. Enabled by LAZYAI_INPUT_LOG=<path>.
	debug io.Writer

	// OnEscape is invoked when a lone ESC byte arrives while forwarding
	// (leave OpenCode for LazyAI). Ctrl+] is the way to send a real ESC to
	// OpenCode itself.
	OnEscape func()
	// OnQuit is invoked when the host quit chord (Ctrl+Q) arrives while
	// forwarding, so the host can always regain control.
	OnQuit func()
	// OnZoom is invoked on Ctrl+Z in any mode (toggle the sidebar).
	OnZoom func()
	// OnLeader is invoked on Ctrl+Space while forwarding; the following chunk
	// is delivered to the host instead of the child.
	OnLeader func()
}

type sinkBox struct{ Sink }

// New creates a router that initially forwards to the child.
func New(src io.Reader, child Sink, host io.Writer) *Router {
	r := &Router{src: src, host: host}
	r.child.Store(&sinkBox{child})
	r.forward.Store(true)
	if p := os.Getenv("LAZYAI_INPUT_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			r.debug = f
		}
	}
	return r
}

// SetChild retargets forwarded input to another child (workstream switch).
func (r *Router) SetChild(c Sink) { r.child.Store(&sinkBox{c}) }

func (r *Router) childSink() Sink { return r.child.Load().Sink }

// SetForward chooses whether raw input goes to the child (true) or host.
func (r *Router) SetForward(v bool) { r.forward.Store(v) }

// escape sends a literal ESC to the child (Ctrl+]).
func (r *Router) escape() { _, _ = r.childSink().Write([]byte{0x1b}) }

// Forwarding reports whether input is currently routed to the child.
func (r *Router) Forwarding() bool { return r.forward.Load() }

// Run blocks, routing input until the source returns an error.
func (r *Router) Run() error {
	return ReadFramed(r.src, func(b []byte, pasted bool) error {
		if pasted {
			if r.forward.Load() && !r.captureNext.Load() {
				_, err := r.childSink().Write(b)
				return err
			}
			_, err := r.host.Write(b)
			return err
		}
		r.route(b)
		return nil
	})
}

func (r *Router) route(b []byte) {
	if start, end := mouseSequenceBounds(b); start >= 0 && (start > 0 || end < len(b)) {
		if start > 0 {
			r.route(b[:start])
		}
		r.route(b[start:end])
		if end < len(b) {
			r.route(b[end:])
		}
		return
	}
	if len(b) == 1 && b[0] == 0x1a && !r.forward.Load() && r.OnZoom != nil {
		r.OnZoom()
		return
	}
	if r.debug != nil {
		dst := "child"
		if !r.forward.Load() {
			dst = "host"
		}
		fmt.Fprintf(r.debug, "%s %-5s %q\n", time.Now().Format("15:04:05.000"), dst, b)
	}
	// Mouse coordinates need layout-aware hit testing and translation before
	// they can be sent to an embedded child, so Bubble Tea always handles them.
	if bytes.HasPrefix(b, []byte("\x1b[<")) || (len(b) >= 3 && bytes.Equal(b[:3], []byte("\x1b[M"))) {
		_, _ = r.host.Write(b)
		return
	}
	if !r.forward.Load() || r.captureNext.Swap(false) {
		_, _ = r.host.Write(b)
		return
	}
	if len(b) == 1 && b[0] == 0x00 { // Ctrl+Space -> leader; next key goes to the host
		r.captureNext.Store(true)
		if r.OnLeader != nil {
			r.OnLeader()
		}
		return
	}
	if len(b) == 1 && b[0] == 0x1a { // Ctrl+Z -> zoom, in every mode
		if r.OnZoom != nil {
			r.OnZoom()
		}
		return
	}
	switch {
	case len(b) == 1 && b[0] == escapeByte: // Ctrl+] -> literal ESC for the pane
		r.escape()
	case len(b) == 1 && b[0] == 0x1b: // lone ESC -> LazyAI
		if r.OnEscape != nil {
			r.OnEscape()
		}
	case len(b) == 1 && b[0] == 0x11: // Ctrl+Q
		// Confirmation belongs to the host. Stop forwarding synchronously so
		// a fast y/n cannot slip into the child before the model updates.
		r.forward.Store(false)
		if r.OnQuit != nil {
			r.OnQuit()
		}
	default:
		_, _ = r.childSink().Write(b)
	}
}

func mouseSequenceBounds(b []byte) (start, end int) {
	start = bytes.Index(b, []byte("\x1b[<"))
	if start >= 0 {
		for i := start + 3; i < len(b); i++ {
			if b[i] == 'M' || b[i] == 'm' {
				return start, i + 1
			}
		}
		return -1, -1
	}
	start = bytes.Index(b, []byte("\x1b[M"))
	if start >= 0 && len(b)-start >= 6 {
		return start, start + 6
	}
	return -1, -1
}
