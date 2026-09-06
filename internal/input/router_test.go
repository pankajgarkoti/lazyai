package input

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// sink is a goroutine-safe buffer: the chord timer writes from another goroutine.
type sink struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *sink) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *sink) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func (s *sink) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}
func (s *sink) Len() int { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Len() }
func (s *sink) Reset()   { s.mu.Lock(); defer s.mu.Unlock(); s.b.Reset() }

func TestForwardingRoutesVerbatimExceptHostKeys(t *testing.T) {
	child := &sink{}
	host := &sink{}
	r := New(nil, child, host)
	escapes, quits := 0, 0
	r.OnEscape = func() { escapes++ }
	r.OnQuit = func() { quits++ }

	r.route([]byte("abc"))
	r.route([]byte{0x1b, '[', 'A'}) // arrow up: ESC followed by more bytes
	r.route([]byte{0x1b})           // lone ESC -> LazyAI
	r.route([]byte{0x11})           // Ctrl+Q
	r.route([]byte{0x03})           // confirmation now owns subsequent input

	want := []byte("abc\x1b[A")
	if !bytes.Equal(child.Bytes(), want) {
		t.Fatalf("child got %q, want %q", child.Bytes(), want)
	}
	if got := host.String(); got != "\x03" {
		t.Fatalf("host should own confirmation input, got %q", got)
	}
	if escapes != 1 || quits != 1 {
		t.Fatalf("escapes=%d quits=%d", escapes, quits)
	}
}

func TestHostModeRoutesEverythingToHost(t *testing.T) {
	child := &sink{}
	host := &sink{}
	r := New(nil, child, host)
	r.SetForward(false)
	r.route([]byte{0x1b})
	r.route([]byte("jk"))
	if child.Len() != 0 {
		t.Fatalf("child should receive nothing, got %q", child.Bytes())
	}
	if got := host.String(); got != "\x1bjk" {
		t.Fatalf("host got %q", got)
	}
}

func TestForwardingRoutesMouseEventsToHostForHitTesting(t *testing.T) {
	child, host := &sink{}, &sink{}
	r := New(nil, child, host)

	r.route([]byte("\x1b[<0;40;8M"))
	if got := host.String(); got != "\x1b[<0;40;8M" {
		t.Fatalf("mouse event should reach host, got %q", got)
	}
	if child.Len() != 0 {
		t.Fatalf("child received unadjusted mouse coordinates: %q", child.Bytes())
	}

	host.Reset()
	r.route([]byte("a\x1b[<64;40;8Mb"))
	if got := child.String(); got != "ab" {
		t.Fatalf("keys surrounding mouse event = %q", got)
	}
	if got := host.String(); got != "\x1b[<64;40;8M" {
		t.Fatalf("batched mouse event = %q", got)
	}
}

func TestCtrlRightBracketSendsEscapeToChildAndJKIsLiteral(t *testing.T) {
	child := &sink{}
	r := New(nil, child, &sink{})
	escapes := 0
	r.OnEscape = func() { escapes++ }

	// Ctrl+] (0x1d) is the one way to send a real ESC into the pane.
	r.route([]byte{0x1d})
	if got := child.String(); got != "\x1b" || escapes != 0 {
		t.Fatalf("ctrl+] should be a literal ESC for OpenCode: child=%q escapes=%d", got, escapes)
	}
	child.Reset()

	// j and jk are ordinary text, delivered immediately with no hold.
	r.route([]byte("j"))
	if got := child.String(); got != "j" {
		t.Fatalf("j must be delivered at once, got %q", got)
	}
	r.route([]byte("k"))
	r.route([]byte("jk"))
	if got := child.String(); got != "jkjk" || escapes != 0 {
		t.Fatalf("jk must stay literal: child=%q escapes=%d", got, escapes)
	}

	// Switching child or mode loses nothing.
	child.Reset()
	other := &sink{}
	r.SetChild(other)
	r.route([]byte{0x1d})
	if other.String() != "\x1b" || child.Len() != 0 {
		t.Fatalf("after SetChild: other=%q child=%q", other.String(), child.Bytes())
	}
}

func TestCtrlZTriggersZoom(t *testing.T) {
	child := &sink{}
	r := New(nil, child, &sink{})
	zooms := 0
	r.OnZoom = func() { zooms++ }
	r.route([]byte{0x1a})
	if zooms != 1 || child.Len() != 0 {
		t.Fatalf("zooms=%d child=%q", zooms, child.Bytes())
	}
}

func TestCtrlQCapturesFollowingInputForConfirmation(t *testing.T) {
	child, host := &sink{}, &sink{}
	r := New(nil, child, host)
	quits := 0
	r.OnQuit = func() { quits++ }

	r.route([]byte{0x11})
	if quits != 1 || r.Forwarding() || child.Len() != 0 {
		t.Fatalf("quits=%d forwarding=%v child=%q", quits, r.Forwarding(), child.Bytes())
	}
	r.route([]byte("y"))
	if host.String() != "y" || child.Len() != 0 {
		t.Fatalf("confirmation key should reach host: host=%q child=%q", host.String(), child.Bytes())
	}
}

func TestLeaderCapturesNextKeyForHostAndChildIsSwitchable(t *testing.T) {
	child1, child2, host := &sink{}, &sink{}, &sink{}
	r := New(nil, child1, host)
	leaders := 0
	r.OnLeader = func() { leaders++ }

	r.route([]byte{0x00}) // Ctrl+Space
	r.route([]byte("2"))  // captured for the host even though forwarding
	r.route([]byte("2"))  // back to normal: reaches the child
	if leaders != 1 || host.String() != "2" || child1.String() != "2" {
		t.Fatalf("leaders=%d host=%q child=%q", leaders, host.String(), child1.String())
	}
	r.SetChild(child2)
	r.route([]byte("x"))
	if child2.String() != "x" || child1.String() != "2" {
		t.Fatalf("child switch: c1=%q c2=%q", child1.String(), child2.String())
	}
	// In host mode the leader byte simply reaches the host as a key.
	r.SetForward(false)
	r.route([]byte{0x00})
	if leaders != 1 || host.String() != "2\x00" {
		t.Fatalf("host mode leader: leaders=%d host=%q", leaders, host.String())
	}
}

func TestPastedControlSequencesDoNotTriggerHostCommands(t *testing.T) {
	child, host := &sink{}, &sink{}
	r := New(nil, child, host)
	commands := 0
	r.OnQuit = func() { commands++ }
	r.OnEscape = func() { commands++ }
	r.OnZoom = func() { commands++ }
	r.OnLeader = func() { commands++ }
	chunks := []string{"\x1b", "[20", "0~", "jk", "\x11", "\x1d", "\x1a", "\x00", "\x1b[<0;4;5M", "\x1b[20", "1~"}
	r.src = &chunkReader{chunks: append([]string(nil), chunks...)}
	_ = r.Run()
	if got, want := child.String(), strings.Join(chunks, ""); got != want || commands != 0 || host.Len() != 0 {
		t.Fatalf("paste changed: child=%q host=%q commands=%d; want %q", got, host.String(), commands, want)
	}
}

// chunkReader deliberately fragments paste markers and contents between reads.
type chunkReader struct{ chunks []string }

func (r *chunkReader) Read(b []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

func TestRunStillDeliversLoneEscapeWithoutAnotherRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	r := New(reader, &sink{}, &sink{})
	escaped := make(chan struct{}, 1)
	r.OnEscape = func() { escaped <- struct{}{} }
	done := make(chan error, 1)
	go func() { done <- r.Run() }()
	if _, err := writer.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-escaped:
	case <-time.After(time.Second):
		t.Fatal("lone Escape was held indefinitely")
	}
	writer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router did not exit")
	}
}
