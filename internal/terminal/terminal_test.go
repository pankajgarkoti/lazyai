package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func waitFor(t *testing.T, term *Terminal, want string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := term.Snapshot(false)
		for _, r := range rows {
			if strings.Contains(stripANSI(r), want) {
				return rows
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not observe %q in screen", want)
	return nil
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestChildOutputAppearsInSnapshotAndRowsAreFullWidth(t *testing.T) {
	term, err := Start(Options{
		Command: "/bin/sh", Args: []string{"-c", "printf 'hello lazyai\\n'; sleep 30"},
		Env: append(os.Environ(), "TERM=xterm-256color"), Width: 40, Height: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	rows := waitFor(t, term, "hello lazyai")
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if got := len([]rune(stripANSI(r))); got != 40 {
			t.Errorf("row %d width = %d, want 40", i, got)
		}
		if !strings.HasSuffix(r, "\x1b[0m") {
			t.Errorf("row %d does not end with reset", i)
		}
	}
}

func TestResizePropagatesToChild(t *testing.T) {
	term, err := Start(Options{
		Command: "/bin/sh", Args: []string{"-c", "while true; do stty size; sleep 0.1; done"},
		Env: append(os.Environ(), "TERM=xterm-256color"), Width: 40, Height: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	waitFor(t, term, "5 40")
	term.Resize(60, 8)
	waitFor(t, term, "8 60")
	if w, h := term.Size(); w != 60 || h != 8 {
		t.Fatalf("size = %dx%d", w, h)
	}
}

func TestSendMouseUsesChildModeAndLocalCoordinates(t *testing.T) {
	out := filepath.Join(t.TempDir(), "mouse-event")
	term, err := Start(Options{
		Command: "/bin/sh",
		Args:    []string{"-c", `stty raw -echo; printf '\033[?1000h\033[?1006hREADY\r\n'; dd bs=1 count=9 of="$1" 2>/dev/null`, "mouse-child", out},
		Env:     append(os.Environ(), "TERM=xterm-256color"), Width: 40, Height: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	waitFor(t, term, "READY")
	term.SendMouse(Mouse{X: 3, Y: 5, Button: vt.MouseLeft, Action: MousePress})
	select {
	case <-term.Exited:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not receive mouse event")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\x1b[<0;4;6M"; string(got) != want {
		t.Fatalf("mouse bytes = %q, want %q", got, want)
	}
}

func TestExitedClosesWhenChildEnds(t *testing.T) {
	term, err := Start(Options{Command: "/bin/sh", Args: []string{"-c", "exit 3"}, Width: 20, Height: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	select {
	case <-term.Exited:
		if term.Err() == nil {
			t.Fatal("expected non-nil exit error for status 3")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
}

func TestFadedSnapshotDimsEveryCell(t *testing.T) {
	term, err := Start(Options{Command: "/bin/sh", Args: []string{"-c", "printf '\\033[31mred\\033[0m plain'; sleep 5"}, Env: os.Environ(), Width: 20, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(term.Snapshot(false), ""), "plain") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	normal := term.Snapshot(false)[0]
	faded := term.SnapshotFaded()[0]
	if normal == faded {
		t.Fatal("faded snapshot should differ from the normal one")
	}
	// Both carry the same text.
	if ansi.Strip(normal) != ansi.Strip(faded) {
		t.Fatalf("text drifted:\n%q\n%q", ansi.Strip(normal), ansi.Strip(faded))
	}
	// Faded output uses the faint attribute and no longer uses the bright
	// "red" (ANSI 31) but a blended RGB colour instead.
	if !strings.Contains(faded, "\x1b[2") && !strings.Contains(faded, ";2;") && !strings.Contains(faded, ";2m") {
		t.Fatalf("no faint attribute in %q", faded)
	}
	if strings.Contains(faded, "[31m") || strings.Contains(faded, ";31m") {
		t.Fatalf("faded output still uses plain ANSI red: %q", faded)
	}
}
