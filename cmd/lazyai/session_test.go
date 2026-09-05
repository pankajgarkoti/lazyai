package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/creack/pty"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazyai/internal/notes"
	"lazyai/internal/supervisor"
)

func TestStopMarksUnreachableSessionStopped(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lazyai.db")
	t.Setenv("LAZYAI_DB", dbPath)
	project := t.TempDir()
	root, err := supervisor.ProjectRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	store, err := notes.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntimeSession(notes.RuntimeSession{
		Project: root, Root: root, Socket: filepath.Join(t.TempDir(), "missing.sock"),
		PID: 999999, Status: "stale",
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	output, err := captureStdout(t, func() error { return stopSession([]string{"--dir", project}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "stopped LazyAI session") {
		t.Fatalf("stop output = %q", output)
	}
	store, err = notes.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sessions, err := store.RuntimeSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Status != "stopped" {
		t.Fatalf("sessions=%+v", sessions)
	}
}

func TestHelpDescribesSessionLifecycle(t *testing.T) {
	output, err := captureStderr(t, func() error { return run([]string{"--help"}) })
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run --help error = %v", err)
	}
	for _, want := range []string{"lazyai list", "lazyai stop", "Ctrl+Q", "reattach"} {
		if !strings.Contains(output, want) {
			t.Errorf("top-level help missing %q: %q", want, output)
		}
	}

	output, err = captureStderr(t, func() error { return run([]string{"list", "--help"}) })
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run list --help error = %v", err)
	}
	if !strings.Contains(output, "known project sessions") {
		t.Fatalf("list help = %q", output)
	}

	output, err = captureStderr(t, func() error { return run([]string{"stop", "--help"}) })
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run stop --help error = %v", err)
	}
	if !strings.Contains(output, "all workstreams") {
		t.Fatalf("stop help = %q", output)
	}
}

func TestSubcommandsRejectUnexpectedArguments(t *testing.T) {
	if err := run([]string{"list", "extra"}); err == nil {
		t.Fatal("list accepted an unexpected argument")
	}
	if err := stopSession([]string{"extra"}); err == nil {
		t.Fatal("stop accepted an unexpected argument")
	}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return captureFile(t, &os.Stdout, fn)
}

func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return captureFile(t, &os.Stderr, fn)
}

func captureFile(t *testing.T, target **os.File, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := *target
	*target = w
	fnErr := fn()
	*target = original
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output), fnErr
}

func sessionTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	return dir
}

func TestNoninteractiveLaunchHasNoSideEffects(t *testing.T) {
	repo := sessionTestRepo(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	db := filepath.Join(t.TempDir(), "db")
	t.Setenv("LAZYAI_RUNTIME_DIR", runtimeDir)
	t.Setenv("LAZYAI_DB", db)
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	original := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = original }()
	err = run([]string{"--dir", repo, "--worktree", "unused"})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Errorf("error=%v", err)
	}
	for _, p := range []string{runtimeDir, db, filepath.Join(repo, ".worktrees")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("rejected invocation created %s", p)
		}
	}
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/unused")
	cmd.Dir = repo
	if cmd.Run() == nil {
		t.Error("rejected invocation created branch")
	}
}

func TestReattachIgnoresWorktreeAndRestoresTerminalModes(t *testing.T) {
	for _, kind := range []string{supervisor.MessageDetach, supervisor.MessageExit, supervisor.MessageError} {
		t.Run(kind, func(t *testing.T) {
			repo := sessionTestRepo(t)
			runtimeDir, err := os.MkdirTemp("/tmp", "lazyai-client-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(runtimeDir)
			t.Setenv("LAZYAI_RUNTIME_DIR", runtimeDir)
			t.Setenv("LAZYAI_DB", filepath.Join(t.TempDir(), "db"))
			project, err := supervisor.ProjectRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", supervisor.SocketPath(project))
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan error, 1)
			go func() {
				c, err := ln.Accept()
				if err != nil {
					done <- err
					return
				}
				defer c.Close()
				var msg supervisor.Message
				if err = json.NewDecoder(c).Decode(&msg); err == nil {
					reply := supervisor.Message{Type: kind}
					if kind == supervisor.MessageError {
						reply.Error = "test failure"
					}
					err = json.NewEncoder(c).Encode(reply)
				}
				done <- err
			}()
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			if err := pty.Setsize(slave, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
				t.Fatal(err)
			}
			output := make(chan string, 1)
			go func() {
				var b strings.Builder
				buf := make([]byte, 4096)
				for {
					n, err := master.Read(buf)
					b.Write(buf[:n])
					if err != nil || strings.Contains(b.String(), "\x1b[?1049l") {
						break
					}
				}
				output <- b.String()
			}()
			originalIn, originalOut := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = slave, slave
			err = run([]string{"--dir", repo, "--worktree", "unused"})
			os.Stdin, os.Stdout = originalIn, originalOut
			slave.Close()
			if kind == supervisor.MessageError {
				if err == nil {
					t.Error("lost server error")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var got string
			select {
			case got = <-output:
			case <-time.After(time.Second):
				t.Fatal("terminal output did not finish")
			}
			for _, mode := range []string{"\x1b[?1002h", "\x1b[?1006h", "\x1b[?2004h", "\x1b[?1002l", "\x1b[?1006l", "\x1b[?2004l"} {
				if !strings.Contains(got, mode) {
					t.Errorf("terminal missing mode %q", mode)
				}
			}
			if _, err := os.Stat(filepath.Join(repo, ".worktrees")); !os.IsNotExist(err) {
				t.Error("reattach created unused worktree")
			}
		})
	}
}
