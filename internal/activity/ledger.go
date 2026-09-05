// Package activity tracks which files the agent has touched during the
// session and why, and keeps pre-modification snapshots for diffing.
package activity

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// State flags for a file entry.
type State uint8

const (
	Read State = 1 << iota
	Modified
	Added
	Deleted
	Shown
)

// Entry is one sidebar row.
type Entry struct {
	Path    string // workspace-relative, slash-separated
	Abs     string
	State   State
	Reason  string
	Touched time.Time
}

// Marker is the one-character sidebar prefix.
func (e Entry) Marker() string {
	switch {
	case e.State&Deleted != 0:
		return "D"
	case e.State&Added != 0:
		return "A"
	case e.State&Modified != 0:
		return "M"
	case e.State&Shown != 0:
		return "S"
	case e.State&Read != 0:
		return "R"
	}
	return " "
}

// Changed reports whether the file differs from its baseline.
func (e Entry) Changed() bool { return e.State&(Modified|Added|Deleted) != 0 }

// Ledger is the ordered set of touched files plus baselines.
type Ledger struct {
	root    string
	entries map[string]*Entry
	// baseline holds the content of a file just before the agent first
	// modified it. A nil value with present=true means the file did not exist.
	baseline map[string]snapshot
	now      func() time.Time
}

type snapshot struct {
	data    []byte
	existed bool
}

// New creates a ledger rooted at the workspace directory.
func New(root string) *Ledger {
	return &Ledger{
		root:     root,
		entries:  map[string]*Entry{},
		baseline: map[string]snapshot{},
		now:      time.Now,
	}
}

// Rel converts an absolute or relative path into the ledger's canonical
// workspace-relative form. ok is false when the path escapes the workspace.
func (l *Ledger) Rel(p string) (rel string, abs string, ok bool) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(l.root, p)
	}
	abs = filepath.Clean(p)
	r, err := filepath.Rel(l.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", abs, false
	}
	return filepath.ToSlash(r), abs, true
}

func (l *Ledger) touch(abs, rel string) *Entry {
	e, ok := l.entries[rel]
	if !ok {
		e = &Entry{Path: rel, Abs: abs}
		l.entries[rel] = e
	}
	e.Touched = l.now()
	return e
}

// MarkRead records that the agent read a file.
func (l *Ledger) MarkRead(p string) bool {
	rel, abs, ok := l.Rel(p)
	if !ok {
		return false
	}
	e := l.touch(abs, rel)
	e.State |= Read
	return true
}

// Snapshot captures the file's current content as its baseline if no baseline
// exists yet. Call it before the agent's first modification of the file.
func (l *Ledger) Snapshot(p string) {
	rel, abs, ok := l.Rel(p)
	if !ok {
		return
	}
	if _, done := l.baseline[rel]; done {
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		l.baseline[rel] = snapshot{existed: false}
		return
	}
	l.baseline[rel] = snapshot{data: data, existed: true}
}

// MarkWritten records a modification and classifies it against the baseline.
func (l *Ledger) MarkWritten(p string) bool {
	rel, abs, ok := l.Rel(p)
	if !ok {
		return false
	}
	if _, done := l.baseline[rel]; !done {
		// No pre-image: we can only say it changed.
		l.baseline[rel] = snapshot{existed: true, data: nil}
	}
	e := l.touch(abs, rel)
	l.classify(e)
	return true
}

func (l *Ledger) classify(e *Entry) {
	base := l.baseline[e.Path]
	_, statErr := os.Stat(e.Abs)
	exists := statErr == nil
	e.State &^= Modified | Added | Deleted
	switch {
	case !base.existed && exists:
		e.State |= Added
	case base.existed && !exists:
		e.State |= Deleted
	default:
		e.State |= Modified
	}
}

// MarkShown records that the agent pointed at this file and attaches a reason.
func (l *Ledger) MarkShown(p, reason string) bool {
	rel, abs, ok := l.Rel(p)
	if !ok {
		return false
	}
	e := l.touch(abs, rel)
	e.State |= Shown
	if reason != "" {
		e.Reason = reason
	}
	return true
}

// Baseline returns the pre-modification content and whether one is known.
func (l *Ledger) Baseline(rel string) (data []byte, existed bool, known bool) {
	s, ok := l.baseline[rel]
	return s.data, s.existed, ok
}

// Get returns the entry for a workspace-relative path.
func (l *Ledger) Get(rel string) (Entry, bool) {
	e, ok := l.entries[rel]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Entries returns rows ordered: changed files first, then the rest, each
// group most-recently-touched first. Ordering is deterministic.
func (l *Ledger) Entries() []Entry {
	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := out[i].Changed(), out[j].Changed()
		if ci != cj {
			return ci
		}
		if !out[i].Touched.Equal(out[j].Touched) {
			return out[i].Touched.After(out[j].Touched)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Len is the number of tracked files.
func (l *Ledger) Len() int { return len(l.entries) }
