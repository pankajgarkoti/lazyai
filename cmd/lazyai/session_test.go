package main

import (
	"path/filepath"
	"testing"

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

	if err := stopSession([]string{"--dir", project}); err != nil {
		t.Fatal(err)
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

func TestSubcommandsRejectUnexpectedArguments(t *testing.T) {
	if err := run([]string{"list", "extra"}); err == nil {
		t.Fatal("list accepted an unexpected argument")
	}
	if err := stopSession([]string{"extra"}); err == nil {
		t.Fatal("stop accepted an unexpected argument")
	}
}
