package supervisor

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProjectRootFromNestedDirectories(t *testing.T) {
	base := t.TempDir()
	identities := map[string]bool{}
	for _, name := range []string{"one", "two"} {
		repo := filepath.Join(base, name)
		nested := filepath.Join(repo, "src", "pkg")
		if err := os.MkdirAll(nested, 0755); err != nil {
			t.Fatal(err)
		}
		gitRun(t, repo, "init", "-q", "-b", "main")
		gitRun(t, repo, "commit", "--allow-empty", "-qm", "init")
		root, err := ProjectRoot(repo)
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, name+"-link")
		if err := os.Symlink(nested, link); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{nested, link} {
			got, err := ProjectRoot(dir)
			if err != nil || got != root {
				t.Errorf("ProjectRoot(%q)=%q,%v; want %q", dir, got, err, root)
			}
		}
		if identities[root] {
			t.Fatal("different repositories share identity")
		}
		identities[root] = true
	}
}

func TestStopKillsWorkerBeforeParentCanOrphanIt(t *testing.T) {
	socket := shortSocket(t)
	pidfile := filepath.Join(t.TempDir(), "pid")
	done := make(chan error, 1)
	script := fmt.Sprintf("trap 'exit 0' TERM; /bin/sh -c 'trap \"\" HUP TERM; echo $$ > %s; exec sleep 60' & while [ ! -s %s ]; do sleep 0.02; done; printf READY; while :; do sleep 0.1; done", pidfile, pidfile)
	go func() {
		done <- Serve(Config{ProjectRoot: t.TempDir(), RequestedRoot: t.TempDir(), SocketPath: socket, DBPath: filepath.Join(t.TempDir(), "db"), Command: "/bin/sh", Args: []string{"-c", script}})
	}()
	conn := dialEventually(t, socket)
	defer conn.Close()
	_ = waitForScreen(t, conn, "READY")
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	unrelated := exec.Command("sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
	_ = json.NewEncoder(conn).Encode(Message{Type: MessageStop})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop timed out")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("worker %d survived successful stop", pid)
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("unrelated process terminated: %v", err)
	}
}

func TestAttachCtrlQInsideFragmentedPasteIsLiteral(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	result := make(chan []byte, 1)
	go func() {
		dec := json.NewDecoder(server)
		var got []byte
		for {
			var msg Message
			if dec.Decode(&msg) != nil {
				result <- got
				return
			}
			if msg.Type == MessageInput {
				got = append(got, msg.Data...)
			}
		}
	}()
	input := io.MultiReader(strings.NewReader("\x1b"), strings.NewReader("[200~hello\x11\n"), strings.NewReader("world\x1b[20"), strings.NewReader("1~\x11"))
	detached, err := Attach(client, input, io.Discard, 80, 24, nil)
	if err != nil || !detached {
		t.Fatalf("detached=%v err=%v", detached, err)
	}
	if got := string(<-result); got != "\x1b[200~hello\x11\nworld\x1b[201~" {
		t.Fatalf("paste bytes=%q", got)
	}
}
