// Package show implements the quickfix-style "Show" set: an ordered list of
// exact code locations chosen by the agent, each with a note, mirroring the
// behaviour of the Neovim opencode_qf integration.
package show

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Location is one entry.
type Location struct {
	Path   string // workspace-relative
	Abs    string
	Line   int // 1-based
	Column int // 1-based
	Note   string
}

// Set is a validated, deduplicated, ordered set of locations.
type Set struct {
	Title     string
	Locations []Location
	Sequence  uint64
}

// Input is the raw payload from the plugin.
type Input struct {
	Path   string
	Line   int
	Column int
	Text   string
}

// Validate normalizes a payload against the workspace root. It fails
// atomically: any invalid entry rejects the whole set, matching the Neovim
// implementation. Duplicate (path, line, column) entries are dropped, keeping
// the first occurrence.
func Validate(root, title string, in []Input) (Set, error) {
	if len(in) == 0 {
		return Set{}, fmt.Errorf("payload must contain at least one location")
	}
	if title == "" {
		title = "Locations"
	}
	seen := map[string]bool{}
	out := Set{Title: title}
	for i, loc := range in {
		idx := i + 1
		if strings.TrimSpace(loc.Path) == "" {
			return Set{}, fmt.Errorf("location %d has an invalid path", idx)
		}
		if loc.Line < 1 {
			return Set{}, fmt.Errorf("location %d has an invalid line", idx)
		}
		col := loc.Column
		if col == 0 {
			col = 1
		}
		if col < 1 {
			return Set{}, fmt.Errorf("location %d has an invalid column", idx)
		}
		abs := loc.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return Set{}, fmt.Errorf("location %d is outside the workspace: %s", idx, abs)
		}
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() {
			return Set{}, fmt.Errorf("location %d is not a readable file: %s", idx, abs)
		}
		key := fmt.Sprintf("%s\x00%d\x00%d", abs, loc.Line, col)
		if seen[key] {
			continue
		}
		seen[key] = true
		note := loc.Text
		if note == "" {
			note = fmt.Sprintf("%s:%d", filepath.ToSlash(rel), loc.Line)
		}
		out.Locations = append(out.Locations, Location{
			Path: filepath.ToSlash(rel), Abs: abs, Line: loc.Line, Column: col, Note: note,
		})
	}
	return out, nil
}

// Reference renders "[path:line:col — note]" for pasting into the prompt.
func (l Location) Reference() string {
	pos := fmt.Sprintf("%s:%d", l.Path, l.Line)
	if l.Column > 1 {
		pos += fmt.Sprintf(":%d", l.Column)
	}
	return fmt.Sprintf("[%s — %s]", pos, l.Note)
}

// Source loads the file's lines (without newlines). Tabs are expanded so
// column highlighting lines up with the rendering.
func Source(abs string) ([]string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		lines = append(lines, strings.ReplaceAll(sc.Text(), "\t", "    "))
	}
	return lines, sc.Err()
}
