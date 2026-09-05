package notes

import (
	"path/filepath"
	"testing"

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
