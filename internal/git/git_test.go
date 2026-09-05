package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-qm", "init")
	// Resolve symlinks so paths compare equal with git's output on macOS.
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

func TestInfoOnMainWorktree(t *testing.T) {
	repo := initRepo(t)
	info, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "main" || info.Linked || info.Top != repo {
		t.Fatalf("%+v", info)
	}
	if _, err := Inspect(t.TempDir()); err == nil {
		t.Fatal("non-repo should error")
	}
}

func TestEnsureWorktreeCreatesReusesAndExcludes(t *testing.T) {
	repo := initRepo(t)
	wt, created, err := EnsureWorktree(repo, "feat/one", "")
	if err != nil {
		t.Fatal(err)
	}
	if !created || wt != filepath.Join(repo, WorktreeDir, "feat-one") {
		t.Fatalf("created=%v wt=%q", created, wt)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatal("worktree not checked out")
	}
	info, err := Inspect(wt)
	if err != nil || info.Branch != "feat/one" || !info.Linked || info.Top != wt {
		t.Fatalf("%+v %v", info, err)
	}
	excl, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if !strings.Contains(string(excl), WorktreeDir+"/") {
		t.Fatalf("exclude missing: %q", excl)
	}
	// Second call is idempotent.
	wt2, created2, err := EnsureWorktree(repo, "feat/one", "")
	if err != nil || created2 || wt2 != wt {
		t.Fatalf("reuse: %q %v %v", wt2, created2, err)
	}
	// Existing branch without a worktree gets one (no new branch).
	cmd := exec.Command("git", "branch", "other")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	wt3, created3, err := EnsureWorktree(repo, "other", "")
	if err != nil || !created3 {
		t.Fatalf("other: %v %v", created3, err)
	}
	if i, _ := Inspect(wt3); i.Branch != "other" {
		t.Fatalf("branch %q", i.Branch)
	}
	// Calling from inside a linked worktree still resolves the main repo.
	wt4, _, err := EnsureWorktree(wt, "feat/two", "main")
	if err != nil || !strings.HasPrefix(wt4, filepath.Join(repo, WorktreeDir)) {
		t.Fatalf("from linked: %q %v", wt4, err)
	}
	list, err := Worktrees(repo)
	if err != nil || len(list) != 4 {
		t.Fatalf("worktrees=%d %v", len(list), err)
	}
	if err := ValidateBranch("bad name"); err == nil {
		t.Fatal("space should be rejected")
	}
}

func TestBranchExists(t *testing.T) {
	repo := initRepo(t)
	if !BranchExists(repo, "main") || BranchExists(repo, "nope") {
		t.Fatal("BranchExists wrong")
	}
	if _, _, err := EnsureWorktree(repo, "feat/base", "main"); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(repo, "feat/base") {
		t.Fatal("new branch should exist")
	}
}

func TestBranchesLists(t *testing.T) {
	repo := initRepo(t)
	for _, b := range []string{"feat/a", "feat/b"} {
		cmd := exec.Command("git", "branch", b)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	got, err := Branches(repo)
	if err != nil || strings.Join(got, ",") != "feat/a,feat/b,main" {
		t.Fatalf("branches %v %v", got, err)
	}
}
