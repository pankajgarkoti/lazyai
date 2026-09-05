package input

import (
	"bytes"
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

func TestForwardingRoutesVerbatimExceptChords(t *testing.T) {
	child := &sink{}
	host := &sink{}
	r := New(nil, child, host)
	escapes, quits := 0, 0
	r.OnEscape = func() { escapes++ }
	r.OnQuit = func() { quits++ }

	r.ChordTimeout = 0 // chord tested separately

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

func TestJKChordSendsEscapeToChild(t *testing.T) {
	child := &sink{}
	r := New(nil, child, &sink{})
	r.ChordTimeout = 30 * time.Millisecond
	escapes := 0
	r.OnEscape = func() { escapes++ }

	r.route([]byte("j"))
	if child.Len() != 0 {
		t.Fatalf("j must be held while the chord is pending, child got %q", child.Bytes())
	}
	r.route([]byte("k"))
	if got := child.String(); got != "\x1b" || escapes != 0 {
		t.Fatalf("jk should be a literal ESC for OpenCode: child=%q escapes=%d", got, escapes)
	}
	child.Reset()

	// Batched "jk" in one read (fast typing) counts too.
	r.route([]byte("jk"))
	if got := child.String(); got != "\x1b" {
		t.Fatalf("batched jk -> %q", got)
	}
	child.Reset()

	// j followed by anything else flushes j then the byte, in order.
	r.route([]byte("j"))
	r.route([]byte("a"))
	if got := child.String(); got != "ja" {
		t.Fatalf("ja -> %q", got)
	}
	child.Reset()

	// jj: the first j flushes, the second stays pending until the timeout.
	r.route([]byte("j"))
	r.route([]byte("j"))
	if got := child.String(); got != "j" {
		t.Fatalf("jj (before timeout) -> %q", got)
	}
	deadline := time.Now().Add(time.Second)
	for child.String() != "jj" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := child.String(); got != "jj" {
		t.Fatalf("jj (after timeout) -> %q", got)
	}
	child.Reset()

	// Timeout alone delivers the lone j.
	r.route([]byte("j"))
	deadline = time.Now().Add(time.Second)
	for child.String() != "j" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := child.String(); got != "j" {
		t.Fatalf("lone j -> %q", got)
	}

	// Switching to host mode flushes a pending j to the child first.
	child.Reset()
	r.route([]byte("j"))
	r.SetForward(false)
	if got := child.String(); got != "j" {
		t.Fatalf("pending j lost on mode switch: %q", got)
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
	r.ChordTimeout = 0

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
