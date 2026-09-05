// Package git wraps the few git operations LazyAI needs for first-class
// worktree support: describing the current checkout and creating or reusing
// a worktree for a branch so an agent can work in isolation.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeDir is where LazyAI keeps linked worktrees, relative to the main
// repository's top level. It is added to .git/info/exclude so it never shows
// up as untracked and no tracked file is modified.
const WorktreeDir = ".worktrees"

// Info describes the checkout containing a directory.
type Info struct {
	Top    string // top level of this worktree
	Main   string // top level of the main worktree (== Top unless Linked)
	Branch string // current branch, or short HEAD when detached
	Linked bool   // true when Top is a linked worktree, not the main one
}

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string
	Head   string
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Inspect describes the repository containing dir.
func Inspect(dir string) (Info, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Info{}, err
	}
	dir = realpath(dir)
	top, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, err
	}
	gitDir, err := run(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return Info{}, err
	}
	common, err := run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return Info{}, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	info := Info{Top: realpath(top)}
	info.Linked = realpath(gitDir) != realpath(common)
	info.Main = realpath(filepath.Dir(realpath(common)))
	if !info.Linked {
		info.Main = info.Top
	}
	branch, err := run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch, _ = run(dir, "rev-parse", "--short", "HEAD")
		if branch != "" {
			branch = "@" + branch
		}
	}
	info.Branch = branch
	return info, nil
}

// Worktrees lists all worktrees of the repository containing dir.
func Worktrees(dir string) ([]Worktree, error) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: realpath(strings.TrimPrefix(line, "worktree "))})
			cur = &list[len(list)-1]
		case cur != nil && strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case cur != nil && strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return list, nil
}

// ValidateBranch rejects names git would refuse or that would escape the
// worktree directory.
func ValidateBranch(name string) error {
	if name == "" {
		return fmt.Errorf("branch name is empty")
	}
	if strings.ContainsAny(name, " \t~^:?*[\\") || strings.Contains(name, "..") || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

// Branches lists local branch names, sorted.
func Branches(dir string) ([]string, error) {
	out, err := run(dir, "for-each-ref", "--format=%(refname:short)", "--sort=refname", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// BranchExists reports whether a local branch exists in dir's repository.
func BranchExists(dir, branch string) bool {
	_, err := run(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// dirName maps a branch to a filesystem-friendly worktree directory name.
func dirName(branch string) string {
	return strings.NewReplacer("/", "-", "\\", "-").Replace(branch)
}

// EnsureWorktree returns the path of a worktree checked out on branch inside
// the repository containing dir, creating it (and the branch, from base or
// HEAD) when needed. created reports whether a new worktree was added.
func EnsureWorktree(dir, branch, base string) (path string, created bool, err error) {
	if err := ValidateBranch(branch); err != nil {
		return "", false, err
	}
	info, err := Inspect(dir)
	if err != nil {
		return "", false, err
	}
	list, err := Worktrees(info.Main)
	if err != nil {
		return "", false, err
	}
	for _, wt := range list {
		if wt.Branch == branch {
			return wt.Path, false, nil
		}
	}
	path = filepath.Join(info.Main, WorktreeDir, dirName(branch))
	if _, statErr := os.Stat(path); statErr == nil {
		return "", false, fmt.Errorf("%s exists but is not a worktree for %s", path, branch)
	}
	if err := exclude(info.Main); err != nil {
		return "", false, err
	}
	if _, err := run(info.Main, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = run(info.Main, "worktree", "add", path, branch)
	} else {
		args := []string{"worktree", "add", "-b", branch, path}
		if base != "" {
			args = append(args, base)
		}
		_, err = run(info.Main, args...)
	}
	if err != nil {
		return "", false, err
	}
	return path, true, nil
}

// exclude makes sure WorktreeDir is ignored via .git/info/exclude.
func exclude(mainTop string) error {
	gitDir, err := run(mainTop, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(mainTop, gitDir)
	}
	p := filepath.Join(gitDir, "info", "exclude")
	entry := WorktreeDir + "/"
	if data, err := os.ReadFile(p); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == entry {
				return nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n# LazyAI worktrees\n%s\n", entry)
	return err
}
