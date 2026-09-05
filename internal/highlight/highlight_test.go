package highlight

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

const goSrc = "package main\n\nimport \"fmt\"\n\n// greet says hi.\nfunc greet(name string) {\n\tfmt.Println(\"hi\", name) // trailing\n}\n"

func plainLines(ls []Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Plain()
	}
	return out
}

func TestFilePreservesTextExactly(t *testing.T) {
	want := strings.Split(strings.TrimSuffix(goSrc, "\n"), "\n")
	got := plainLines(File("main.go", goSrc))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plain text drifted\n got: %q\nwant: %q", got, want)
	}
	// Multi-line tokens (block comments, raw strings) must still split per line.
	raw := "x := `a\nb\nc`\n/* one\ntwo */\n"
	got = plainLines(File("x.go", raw))
	want = strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("multi-line tokens drifted\n got: %q\nwant: %q", got, want)
	}
}

func TestUnknownLanguageIsPassThrough(t *testing.T) {
	src := "just some text\nwith two lines"
	ls := File("notes.weirdext", src)
	if len(ls) != 2 || ls[0].Plain() != "just some text" || ls[1].Plain() != "with two lines" {
		t.Fatalf("unexpected lines: %+v", ls)
	}
	if got := File("a.go", ""); len(got) != 0 {
		t.Fatalf("empty source should yield no lines, got %d", len(got))
	}
}

func TestLineCountMatchesInputIncludingTrailingNewline(t *testing.T) {
	// "a\nb\n" is two lines; "a\nb" is also two lines; "a\n\n" is a, "".
	cases := map[string]int{"a\nb\n": 2, "a\nb": 2, "a\n\n": 2, "\n": 1, "a": 1}
	for src, n := range cases {
		if got := len(File("f.txt", src)); got != n {
			t.Errorf("File(%q): %d lines, want %d", src, got, n)
		}
	}
}

func TestRenderAppliesColourAndBackground(t *testing.T) {
	r := lipgloss.NewRenderer(&strings.Builder{})
	r.SetColorProfile(termenv.TrueColor)
	h := New(WithRenderer(r))
	ls := h.File("main.go", "func main() {}\n")
	if len(ls) != 1 {
		t.Fatalf("lines=%d", len(ls))
	}
	out := ls[0].Render(nil)
	if ansi.Strip(out) != "func main() {}" {
		t.Fatalf("plain=%q", ansi.Strip(out))
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("expected ANSI styling for a Go keyword")
	}
	bg := lipgloss.Color("#2a3d34")
	tinted := ls[0].Render(bg)
	// Every span carries the background so a tinted row never "leaks" back to
	// the terminal default when a token resets its own style.
	if strings.Count(tinted, "48;2;") < 2 {
		t.Fatalf("background not applied per span: %q", tinted)
	}
}

func TestCutSplitsAtRuneIndexAcrossSpans(t *testing.T) {
	ls := File("main.go", "func héllo() {}\n")
	l := ls[0]
	for _, i := range []int{0, 4, 5, 7, 15, 99} {
		a, b := l.Cut(i)
		runes := []rune(l.Plain())
		if i > len(runes) {
			i = len(runes)
		}
		if a.Plain() != string(runes[:i]) || b.Plain() != string(runes[i:]) {
			t.Errorf("Cut(%d): %q | %q", i, a.Plain(), b.Plain())
		}
	}
}
