package notes

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"lazyai/internal/show"
)

func TestRecordAndRecent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	set := show.Set{Title: "tour", Locations: []show.Location{
		{Path: "main.go", Abs: "/r/main.go", Line: 6, Column: 6, Note: "greet"},
		{Path: "pkg/u.go", Abs: "/r/pkg/u.go", Line: 4, Column: 1, Note: "clamp"},
	}}
	if err := db.Record("/r", "main", "sess1", set); err != nil {
		t.Fatal(err)
	}
	if err := db.Record("/r", "feat", "sess2", show.Set{Title: "second", Locations: set.Locations[:1]}); err != nil {
		t.Fatal(err)
	}
	sets, err := db.Recent("/r", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0].Title != "second" || sets[1].Title != "tour" {
		t.Fatalf("recent: %+v", sets)
	}
	if sets[1].Branch != "main" || len(sets[1].Locations) != 2 || sets[1].Locations[1].Note != "clamp" || sets[1].Locations[1].Line != 4 {
		t.Fatalf("set 1: %+v", sets[1])
	}
	if other, _ := db.Recent("/elsewhere", 10); len(other) != 0 {
		t.Fatal("recent must be scoped by root")
	}
	// Reopening keeps the data (schema creation is idempotent).
	db.Close()
	db2, err := Open(filepath.Join(filepath.Dir(db.Path), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if sets, _ := db2.Recent("/r", 1); len(sets) != 1 {
		t.Fatal("data lost across reopen")
	}
}

func TestRuntimeSessionRegistryPersistsStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Second)
	if err := db.UpsertRuntimeSession(RuntimeSession{
		Project: "/repo", Root: "/repo/worktree", Socket: "/tmp/lazyai.sock",
		PID: 42, Status: "running", StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkRuntimeSession("/repo", "stale"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sessions, err := db.RuntimeSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Project != "/repo" || sessions[0].PID != 42 || sessions[0].Status != "stale" {
		t.Fatalf("sessions=%+v", sessions)
	}
}

// TestMigrationAddsIdentityColumnsToVersion0Database opens a database created
// by the pre-identity schema and checks the versioned migration adds nickname
// and description without touching existing rows or re-running.
func TestMigrationAddsIdentityColumnsToVersion0Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO worktrees(repo, branch, path, linked, created_at, last_opened, dormant)
		VALUES ('/repo', 'feat/old', '/repo/.worktrees/feat-old', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 1)`); err != nil {
		t.Fatal(err)
	}
	var v0 int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&v0); err != nil || v0 != 0 {
		t.Fatalf("fixture user_version=%d err=%v", v0, err)
	}
	raw.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if v := db.userVersion(t); v != schemaVersion {
		t.Fatalf("user_version=%d want %d", v, schemaVersion)
	}
	all, err := db.Worktrees("/repo")
	if err != nil || len(all) != 1 {
		t.Fatalf("worktrees=%v err=%v", all, err)
	}
	if all[0].Branch != "feat/old" || !all[0].Dormant || all[0].Nickname != "" || all[0].Description != "" {
		t.Fatalf("old row changed: %+v", all[0])
	}
	if err := db.SetWorktreeIdentity("/repo", "feat/old", "Login fix", "remember the cookie bug"); err != nil {
		t.Fatal(err)
	}
	// A wake (upsert) must not clobber identity.
	if err := db.UpsertWorktree("/repo", "feat/old", "/repo/.worktrees/feat-old", true); err != nil {
		t.Fatal(err)
	}
	all, _ = db.Worktrees("/repo")
	if all[0].Nickname != "Login fix" || all[0].Description != "remember the cookie bug" || all[0].Dormant {
		t.Fatalf("identity lost on upsert: %+v", all[0])
	}
	// Identity for a branch that was never opened is stored too (agent setup
	// records identity before launch).
	if err := db.SetWorktreeIdentity("/repo", "feat/new", "New", ""); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// Reopening is idempotent.
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v := db.userVersion(t); v != schemaVersion {
		t.Fatalf("user_version after reopen=%d", v)
	}
	all, _ = db.Worktrees("/repo")
	if len(all) != 2 {
		t.Fatalf("rows=%d", len(all))
	}
}

func (d *DB) userVersion(t *testing.T) int {
	t.Helper()
	var v int
	if err := d.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWorktreeRegistryAndRepoState(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertWorktree("/repo", "main", "/repo", false); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertWorktree("/repo", "feat/a", "/repo/.worktrees/feat-a", true); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertWorktree("/repo", "feat/b", "/repo/.worktrees/feat-b", true); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDormant("/repo", "feat/a", true); err != nil {
		t.Fatal(err)
	}
	all, err := db.Worktrees("/repo")
	if err != nil || len(all) != 3 {
		t.Fatalf("worktrees=%d %v", len(all), err)
	}
	dormant, err := db.Dormant("/repo")
	if err != nil || len(dormant) != 1 || dormant[0].Branch != "feat/a" || !dormant[0].Linked {
		t.Fatalf("dormant=%+v %v", dormant, err)
	}
	// Waking (upsert again) clears dormant and bumps last_opened.
	if err := db.UpsertWorktree("/repo", "feat/a", "/repo/.worktrees/feat-a", true); err != nil {
		t.Fatal(err)
	}
	if d, _ := db.Dormant("/repo"); len(d) != 0 {
		t.Fatal("upsert should wake")
	}
	if other, _ := db.Worktrees("/other"); len(other) != 0 {
		t.Fatal("scoped by repo")
	}
	if err := db.SetState("/repo", "last_branch", "feat/b"); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.State("/repo", "last_branch"); v != "feat/b" {
		t.Fatalf("state=%q", v)
	}
	if v, _ := db.State("/repo", "missing"); v != "" {
		t.Fatalf("missing state=%q", v)
	}
}
