package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	root := t.TempDir()
	l := New(root)
	tick := time.Unix(1000, 0)
	l.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	return l, root
}

func TestRelRejectsEscapes(t *testing.T) {
	l, root := newTestLedger(t)
	if _, _, ok := l.Rel(filepath.Join(root, "..", "x")); ok {
		t.Fatal("parent escape accepted")
	}
	if _, _, ok := l.Rel("/etc/passwd"); ok {
		t.Fatal("absolute outside accepted")
	}
	rel, _, ok := l.Rel("a/./b/../c.go")
	if !ok || rel != "a/c.go" {
		t.Fatalf("rel=%q ok=%v", rel, ok)
	}
}

func TestSnapshotThenWriteClassifiesModified(t *testing.T) {
	l, root := newTestLedger(t)
	p := filepath.Join(root, "f.go")
	os.WriteFile(p, []byte("old\n"), 0o644)

	l.Snapshot(p)
	os.WriteFile(p, []byte("new\n"), 0o644)
	l.Snapshot(p) // second snapshot must not overwrite the baseline
	l.MarkWritten(p)

	e, _ := l.Get("f.go")
	if e.Marker() != "M" {
		t.Fatalf("marker=%q", e.Marker())
	}
	data, existed, known := l.Baseline("f.go")
	if !known || !existed || string(data) != "old\n" {
		t.Fatalf("baseline=%q existed=%v known=%v", data, existed, known)
	}
}

func TestWriteOfNewFileIsAddedAndDeleteIsDeleted(t *testing.T) {
	l, root := newTestLedger(t)
	p := filepath.Join(root, "new.go")
	l.Snapshot(p) // does not exist yet
	os.WriteFile(p, []byte("x"), 0o644)
	l.MarkWritten(p)
	if e, _ := l.Get("new.go"); e.Marker() != "A" {
		t.Fatalf("marker=%q want A", e.Marker())
	}

	q := filepath.Join(root, "gone.go")
	os.WriteFile(q, []byte("x"), 0o644)
	l.Snapshot(q)
	os.Remove(q)
	l.MarkWritten(q)
	if e, _ := l.Get("gone.go"); e.Marker() != "D" {
		t.Fatalf("marker=%q want D", e.Marker())
	}
}

func TestOrderingChangedFirstThenRecency(t *testing.T) {
	l, root := newTestLedger(t)
	for _, n := range []string{"a.go", "b.go", "c.go"} {
		os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644)
	}
	l.MarkRead("a.go") // t=1
	l.MarkRead("b.go") // t=2
	l.Snapshot("c.go")
	l.MarkWritten("c.go") // t=3, changed
	l.MarkRead("a.go")    // t=4, most recent read

	got := []string{}
	for _, e := range l.Entries() {
		got = append(got, e.Marker()+" "+e.Path)
	}
	want := []string{"M c.go", "R a.go", "R b.go"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestShownAttachesReasonWithoutClearingRead(t *testing.T) {
	l, root := newTestLedger(t)
	os.WriteFile(filepath.Join(root, "s.go"), []byte("x"), 0o644)
	l.MarkRead("s.go")
	l.MarkShown("s.go", "entry point")
	e, _ := l.Get("s.go")
	if e.Reason != "entry point" || e.State&Read == 0 || e.Marker() != "S" {
		t.Fatalf("entry=%+v marker=%s", e, e.Marker())
	}
}
