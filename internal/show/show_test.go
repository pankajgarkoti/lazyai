package show

import (
	"os"
	"path/filepath"
	"testing"
)

// Ported from nvim/tests/opencode_qf_spec.lua.
func TestValidateDedupesKeepsOrderAndExactPositions(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.txt")
	os.WriteFile(fixture, []byte("first\nsecond\nthird\n"), 0o644)

	set, err := Validate(root, "OpenCode test", []Input{
		{Path: fixture, Line: 2, Column: 3, Text: "target"},
		{Path: "fixture.txt", Line: 2, Column: 3, Text: "duplicate"},
		{Path: fixture, Line: 3, Column: 2, Text: "next target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Title != "OpenCode test" {
		t.Fatalf("title %q", set.Title)
	}
	if len(set.Locations) != 2 {
		t.Fatalf("duplicate locations should be removed, got %d", len(set.Locations))
	}
	if l := set.Locations[0]; l.Line != 2 || l.Column != 3 || l.Note != "target" || l.Path != "fixture.txt" {
		t.Fatalf("first=%+v", l)
	}
	if l := set.Locations[1]; l.Line != 3 || l.Column != 2 || l.Note != "next target" {
		t.Fatalf("second=%+v", l)
	}
	if got := set.Locations[0].Reference(); got != "[fixture.txt:2:3 — target]" {
		t.Fatalf("ref=%q", got)
	}
}

func TestValidateIsAtomic(t *testing.T) {
	root := t.TempDir()
	ok := filepath.Join(root, "ok.txt")
	os.WriteFile(ok, []byte("x\n"), 0o644)

	cases := []struct {
		name string
		in   []Input
	}{
		{"empty", nil},
		{"missing file", []Input{{Path: ok, Line: 1}, {Path: "nope.txt", Line: 1}}},
		{"bad line", []Input{{Path: ok, Line: 0}}},
		{"bad column", []Input{{Path: ok, Line: 1, Column: -1}}},
		{"outside workspace", []Input{{Path: "../etc", Line: 1}}},
		{"directory", []Input{{Path: root, Line: 1}}},
	}
	for _, c := range cases {
		if _, err := Validate(root, "", c.in); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestDefaultsTitleColumnAndNote(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("x\n"), 0o644)
	set, err := Validate(root, "", []Input{{Path: "a.go", Line: 1}})
	if err != nil {
		t.Fatal(err)
	}
	l := set.Locations[0]
	if set.Title != "Locations" || l.Column != 1 || l.Note != "a.go:1" || l.Reference() != "[a.go:1 — a.go:1]" {
		t.Fatalf("set=%+v", set)
	}
}
