package diff

import (
	"fmt"
	"strings"
	"testing"
)

func TestModifiedFileProducesHunksAndReference(t *testing.T) {
	base := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")
	cur := []byte("a\nb\nc\nd\nE\nf\ng\nh\ni\nj\n")
	r := Unified("x.go", base, true, cur, true)
	if r.Note != "" || len(r.Hunks) != 1 {
		t.Fatalf("note=%q hunks=%d lines=%v", r.Note, len(r.Hunks), r.Lines)
	}
	if !strings.HasPrefix(r.Lines[0], "--- a/x.go") || !strings.HasPrefix(r.Lines[1], "+++ b/x.go") {
		t.Fatalf("headers: %q %q", r.Lines[0], r.Lines[1])
	}
	h := r.Hunks[0]
	if h.NewStart != 2 || h.NewEnd != 8 {
		t.Fatalf("range %d-%d", h.NewStart, h.NewEnd)
	}
	if got := r.Reference(0); got != "x.go:2-8" {
		t.Fatalf("ref=%q", got)
	}
	if r.HunkAt(0) != -1 || r.HunkAt(h.Index) != 0 || r.HunkAt(len(r.Lines)-1) != 0 {
		t.Fatal("HunkAt mapping wrong")
	}
}

func TestAddedAndDeletedUseDevNull(t *testing.T) {
	r := Unified("n.go", nil, false, []byte("x\n"), true)
	if r.Lines[0] != "--- /dev/null" {
		t.Fatalf("added header %q", r.Lines[0])
	}
	// One-line file must produce exactly one added line (no phantom blank).
	adds := 0
	for _, l := range r.Lines[2:] {
		if strings.HasPrefix(l, "+") {
			adds++
		}
	}
	if adds != 1 || r.Hunks[0].NewStart != 1 || r.Hunks[0].NewEnd != 1 {
		t.Fatalf("adds=%d hunk=%+v lines=%q", adds, r.Hunks[0], r.Lines)
	}
	r = Unified("g.go", []byte("x\n"), true, nil, false)
	if r.Lines[1] != "+++ /dev/null" {
		t.Fatalf("deleted header %q", r.Lines[1])
	}
}

func TestBinaryAndUnchanged(t *testing.T) {
	if r := Unified("b", []byte("a\x00b"), true, []byte("c"), true); !r.Binary {
		t.Fatal("binary not detected")
	}
	if r := Unified("s", []byte("same\n"), true, []byte("same\n"), true); r.Note == "" || len(r.Lines) != 0 {
		t.Fatalf("unchanged: %+v", r)
	}
}

func TestAnnotateMapsEveryLineToItsSide(t *testing.T) {
	base := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")
	cur := []byte("a\nb\nc\nd\nE\nE2\nf\ng\nh\nj\n") // e→E,E2 ; i deleted
	r := Unified("x.go", base, true, cur, true)
	refs := Annotate(r.Lines)
	if len(refs) != len(r.Lines) {
		t.Fatalf("refs=%d lines=%d", len(refs), len(r.Lines))
	}
	if refs[0].Kind != KindHeader || refs[1].Kind != KindHeader || refs[2].Kind != KindHunk {
		t.Fatalf("prefix kinds: %+v", refs[:3])
	}
	oldLines := strings.Split(strings.TrimSuffix(string(base), "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(string(cur), "\n"), "\n")
	adds, dels, ctx := 0, 0, 0
	for i, l := range r.Lines {
		ref := refs[i]
		switch ref.Kind {
		case KindAdd:
			adds++
			if newLines[ref.New-1] != l[1:] || ref.Old != 0 {
				t.Errorf("line %d %q: add maps to new %d old %d", i, l, ref.New, ref.Old)
			}
		case KindDel:
			dels++
			if oldLines[ref.Old-1] != l[1:] || ref.New != 0 {
				t.Errorf("line %d %q: del maps to old %d new %d", i, l, ref.Old, ref.New)
			}
		case KindContext:
			ctx++
			if oldLines[ref.Old-1] != l[1:] || newLines[ref.New-1] != l[1:] {
				t.Errorf("line %d %q: ctx maps to old %d new %d", i, l, ref.Old, ref.New)
			}
		}
	}
	if adds != 2 || dels != 2 || ctx == 0 {
		t.Fatalf("adds=%d dels=%d ctx=%d", adds, dels, ctx)
	}
}

func TestAnnotateDoesNotMistakeDeletedDashesForHeaders(t *testing.T) {
	base := []byte("--- not a header\n-- also\nx\n")
	cur := []byte("x\n")
	r := Unified("d.txt", base, true, cur, true)
	refs := Annotate(r.Lines)
	for i, l := range r.Lines[3:] {
		if strings.HasPrefix(l, "-") && refs[i+3].Kind != KindDel {
			t.Fatalf("line %q kind=%v", l, refs[i+3].Kind)
		}
	}
}

func TestRepetitiveLargeFileStillProducesTightHunks(t *testing.T) {
	// go-difflib's default matcher enables Python-style autojunk for inputs
	// with >= 200 lines: lines repeated in >1% of the file (blank lines, "}")
	// become junk and a moved function smears into a whole-file diff. We must
	// diff like `diff -u` instead.
	var a []string
	for i := 0; i < 80; i++ {
		a = append(a, fmt.Sprintf("func f%d() {\n", i), fmt.Sprintf("\treturn %d\n", i), "}\n", "\n")
	}
	b := append([]string{}, a...)
	blk := append([]string{}, b[40:44]...) // move f10 after f60
	b = append(b[:40], b[44:]...)
	b = append(b[:240], append(blk, b[240:]...)...)
	b[2*4+1] = "\treturn 999\n" // and edit f2
	r := Unified("big.go", []byte(strings.Join(a, "")), true, []byte(strings.Join(b, "")), true)
	if len(r.Hunks) != 3 {
		t.Fatalf("hunks=%d (want 3)", len(r.Hunks))
	}
	if len(r.Lines) > 40 {
		t.Fatalf("diff smeared: %d lines", len(r.Lines))
	}
	if r.Hunks[0].NewStart != 7 || r.Hunks[0].NewEnd != 13 {
		t.Fatalf("first hunk %d-%d", r.Hunks[0].NewStart, r.Hunks[0].NewEnd)
	}
}
