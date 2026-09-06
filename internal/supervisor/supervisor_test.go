package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"lazyai/internal/notes"
)

func TestProjectRootGroupsWorktreesAndSeparatesClones(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "commit", "--allow-empty", "-qm", "init")
	worktree := filepath.Join(filepath.Dir(repo), "feature")
	gitRun(t, repo, "worktree", "add", "-qb", "feature", worktree)

	mainRoot, err := ProjectRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	worktreeRoot, err := ProjectRoot(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if mainRoot != worktreeRoot {
		t.Fatalf("main=%q worktree=%q", mainRoot, worktreeRoot)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, filepath.Dir(clone), "clone", "-q", repo, clone)
	cloneRoot, err := ProjectRoot(clone)
	if err != nil {
		t.Fatal(err)
	}
	if cloneRoot == mainRoot {
		t.Fatal("separate clones must remain separate projects")
	}
	if SocketPath(mainRoot) == SocketPath(cloneRoot) {
		t.Fatal("separate projects must have separate sockets")
	}
}

func TestSupervisorSurvivesDetachAndReattachesToScreen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lazyai.db")
	project := t.TempDir()
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("lazyai-supervisor-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	// The child prints LATER only once the test has detached (flag file), so
	// the assertion below does not depend on scheduler speed.
	flag := filepath.Join(project, "detached")
	done := make(chan error, 1)
	go func() {
		done <- Serve(Config{
			ProjectRoot:   project,
			RequestedRoot: project,
			SocketPath:    socket,
			DBPath:        dbPath,
			Command:       "/bin/sh",
			Args:          []string{"-c", "printf 'READY\\n'; while [ ! -e \"$0\" ]; do sleep 0.05; done; printf 'LATER\\n'; sleep 30", flag},
			Width:         40,
			Height:        8,
		})
	}()
	t.Cleanup(func() {
		if conn, err := net.Dial("unix", socket); err == nil {
			_ = json.NewEncoder(conn).Encode(Message{Type: MessageStop})
			_ = conn.Close()
		}
	})

	first := dialEventually(t, socket)
	firstScreen := waitForScreen(t, first, "READY")
	if strings.Contains(firstScreen, "LATER") {
		t.Fatal("test did not detach before later output")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(350 * time.Millisecond) // detached progress happens with no client
	second := dialEventually(t, socket)
	defer second.Close()
	if screen := waitForScreen(t, second, "LATER"); !strings.Contains(screen, "READY") {
		t.Fatalf("reattached screen lost earlier output: %q", screen)
	}

	if err := json.NewEncoder(second).Encode(Message{Type: MessageStop}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestAttachCtrlQDetachesWithoutForwardingIt(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	var got []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)
		for {
			var msg Message
			if err := dec.Decode(&msg); err != nil {
				return
			}
			switch msg.Type {
			case MessageAttach:
				_ = enc.Encode(Message{Type: MessageScreen, Data: []byte("screen")})
			case MessageInput:
				got = append(got, msg.Data...)
			}
		}
	}()
	var out strings.Builder
	detached, err := Attach(client, strings.NewReader("abc\x11xyz"), &out, 80, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !detached || string(got) != "abc" {
		t.Fatalf("detached=%v input=%q output=%q", detached, got, out.String())
	}
}

func TestSupervisorForwardsAttachSizeAndInput(t *testing.T) {
	socket := shortSocket(t)
	done := make(chan error, 1)
	go func() {
		done <- Serve(Config{
			ProjectRoot: t.TempDir(), RequestedRoot: t.TempDir(), SocketPath: socket,
			DBPath: filepath.Join(t.TempDir(), "lazyai.db"), Command: "/bin/sh",
			Args:  []string{"-c", "sleep 0.2; stty size; IFS= read -r line; printf 'INPUT:%s' \"$line\"; sleep 30"},
			Width: 20, Height: 5,
		})
	}()
	conn := dialEventually(t, socket)
	defer conn.Close()
	enc := json.NewEncoder(conn)
	if err := enc.Encode(Message{Type: MessageResize, Width: 33, Height: 7}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(Message{Type: MessageInput, Data: []byte("hello\n")}); err != nil {
		t.Fatal(err)
	}
	screen := waitForScreen(t, conn, "INPUT:hello")
	if !strings.Contains(screen, "7 33") {
		t.Fatalf("child did not observe resized PTY: %q", screen)
	}
	if err := enc.Encode(Message{Type: MessageStop}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestNewestAttachmentTakesOver(t *testing.T) {
	socket := shortSocket(t)
	done := make(chan error, 1)
	go func() {
		done <- Serve(Config{
			ProjectRoot: t.TempDir(), RequestedRoot: t.TempDir(), SocketPath: socket,
			DBPath: filepath.Join(t.TempDir(), "lazyai.db"), Command: "/bin/sh",
			Args: []string{"-c", "printf ready; sleep 30"}, Width: 20, Height: 5,
		})
	}()
	first := dialEventually(t, socket)
	defer first.Close()
	_ = waitForScreen(t, first, "ready")
	second := dialEventually(t, socket)
	defer second.Close()
	_ = waitForScreen(t, second, "ready")

	if err := first.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var msg Message
	if err := json.NewDecoder(first).Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != MessageDetach {
		t.Fatalf("takeover message=%+v", msg)
	}
	if err := json.NewEncoder(second).Encode(Message{Type: MessageStop}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorReplacesStaleSocketAndRegistryRow(t *testing.T) {
	socket := shortSocket(t)
	if err := os.WriteFile(socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "lazyai.db")
	project := t.TempDir()
	store, err := notes.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntimeSession(notes.RuntimeSession{
		Project: project, Root: project, Socket: socket, PID: 999999, Status: "stale",
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	done := make(chan error, 1)
	go func() {
		done <- Serve(Config{
			ProjectRoot: project, RequestedRoot: project, SocketPath: socket, DBPath: dbPath,
			Command: "/bin/sh", Args: []string{"-c", "printf recovered; sleep 30"}, Width: 20, Height: 5,
			OriginalArgs: []string{"--worktree", "feature"},
		})
	}()
	conn := dialEventually(t, socket)
	defer conn.Close()
	_ = waitForScreen(t, conn, "recovered")

	store, err = notes.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := store.RuntimeSessions()
	_ = store.Close()
	if err != nil || len(sessions) != 1 || sessions[0].Status != "running" || sessions[0].PID == 999999 || sessions[0].Args != `["--worktree","feature"]` {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if err := json.NewEncoder(conn).Encode(Message{Type: MessageStop}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorDoesNotPublishSocketBeforeChildStarts(t *testing.T) {
	socket := shortSocket(t)
	err := Serve(Config{
		ProjectRoot: t.TempDir(), RequestedRoot: t.TempDir(), SocketPath: socket,
		DBPath: filepath.Join(t.TempDir(), "lazyai.db"), Command: filepath.Join(t.TempDir(), "missing"),
	})
	if err == nil {
		t.Fatal("expected child startup failure")
	}
	if conn, dialErr := net.Dial("unix", socket); dialErr == nil {
		_ = conn.Close()
		t.Fatal("failed startup published an attachable socket")
	}
}

func TestForcedStopKillsNestedChild(t *testing.T) {
	socket := shortSocket(t)
	dbPath := filepath.Join(t.TempDir(), "lazyai.db")
	project := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := "trap '' TERM; /bin/sh -c 'trap \"\" TERM; exec sleep 30' & echo $! > " + pidFile + "; printf ready; wait"
	done := make(chan error, 1)
	go func() {
		done <- Serve(Config{
			ProjectRoot: project, RequestedRoot: project, SocketPath: socket, DBPath: dbPath,
			Command: "/bin/sh", Args: []string{"-c", script}, Width: 20, Height: 5,
		})
	}()
	conn := dialEventually(t, socket)
	_ = waitForScreen(t, conn, "ready")
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(Message{Type: MessageStop}); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not force stop")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nested child %d survived forced stop", pid)
}

func TestRuntimeLockCannotBeStolenAndReleasesOnClose(t *testing.T) {
	socket := shortSocket(t)
	firstRelease, err := acquireLock(socket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(socket); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second lock err=%v", err)
	}
	if !LockHeld(socket) {
		t.Fatal("held lock was not detected")
	}
	firstRelease()
	secondRelease, err := acquireLock(socket)
	if err != nil {
		t.Fatalf("released lock could not be reacquired: %v", err)
	}
	secondRelease()
}

func shortSocket(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.TempDir(), fmt.Sprintf("lazyai-supervisor-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
}

func dialEventually(t *testing.T, socket string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			if err := json.NewEncoder(conn).Encode(Message{Type: MessageAttach}); err != nil {
				t.Fatal(err)
			}
			return conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %s never became available", socket)
	return nil
}

func waitForScreen(t *testing.T, conn net.Conn, want string) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(conn)
	for {
		var msg Message
		if err := dec.Decode(&msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type == MessageScreen && strings.Contains(string(msg.Data), want) {
			return string(msg.Data)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
