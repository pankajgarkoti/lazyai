// Package diff produces session-relative unified diffs (baseline captured just
// before the agent's first edit vs. the file's current content) and parses
// them into hunks for navigation.
package diff

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Hunk is one @@ block with its position in the rendered diff.
type Hunk struct {
	// Line index (0-based) of the @@ header within Lines.
	Index int
	// New-file line range covered by the hunk (1-based, inclusive).
	NewStart, NewEnd int
}

// Result is a rendered diff.
type Result struct {
	Path   string
	Lines  []string // raw unified diff lines, no trailing newline
	Hunks  []Hunk
	Binary bool
	Note   string // human message when no textual diff is available
}

const maxTextual = 2 << 20 // 2 MiB

// Unified diffs baseline against current for a workspace-relative path.
// existed=false means the file was created during the session; current=nil
// with a baseline means it was deleted.
func Unified(path string, baseline []byte, existed bool, current []byte, exists bool) Result {
	r := Result{Path: path}
	switch {
	case !existed && !exists:
		r.Note = "file does not exist"
		return r
	case len(baseline) > maxTextual || len(current) > maxTextual:
		r.Note = "file too large to diff"
		return r
	case bytes.IndexByte(baseline, 0) >= 0 || bytes.IndexByte(current, 0) >= 0:
		r.Binary = true
		r.Note = "binary file changed"
		return r
	}
	a, b := "", ""
	if existed {
		a = "a/" + path
	} else {
		a = "/dev/null"
	}
	if exists {
		b = "b/" + path
	} else {
		b = "/dev/null"
	}
	text := unified(splitLines(baseline), splitLines(current), a, b, 3)
	if text == "" {
		r.Note = "no changes since baseline"
		return r
	}
	r.Lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
	r.Hunks = parseHunks(r.Lines)
	return r
}

// unified renders a unified diff like difflib.GetUnifiedDiffString but with
// autojunk disabled: the default matcher treats lines that repeat in >1% of a
// 200+ line file (blank lines, "}") as junk, which smears real-world diffs.
func unified(a, b []string, fromFile, toFile string, context int) string {
	m := difflib.NewMatcherWithJunk(a, b, false, nil)
	var out strings.Builder
	started := false
	for _, g := range m.GetGroupedOpCodes(context) {
		if !started {
			started = true
			fmt.Fprintf(&out, "--- %s\n+++ %s\n", fromFile, toFile)
		}
		first, last := g[0], g[len(g)-1]
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", rangeUnified(first.I1, last.I2), rangeUnified(first.J1, last.J2))
		for _, c := range g {
			switch c.Tag {
			case 'e':
				for _, l := range a[c.I1:c.I2] {
					out.WriteString(" " + l)
				}
			case 'r', 'd':
				for _, l := range a[c.I1:c.I2] {
					out.WriteString("-" + l)
				}
				if c.Tag == 'r' {
					for _, l := range b[c.J1:c.J2] {
						out.WriteString("+" + l)
					}
				}
			case 'i':
				for _, l := range b[c.J1:c.J2] {
					out.WriteString("+" + l)
				}
			}
		}
	}
	return out.String()
}

// rangeUnified formats a hunk range the way GNU diff does.
func rangeUnified(start, stop int) string {
	beginning := start + 1
	length := stop - start
	if length == 1 {
		return strconv.Itoa(beginning)
	}
	if length == 0 {
		beginning--
	}
	return fmt.Sprintf("%d,%d", beginning, length)
}

// splitLines splits into newline-terminated lines without the phantom empty
// line difflib.SplitLines appends after a trailing newline. A final line that
// lacks a newline gets one so the rendered diff stays line-aligned.
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(b), "\n")
	last := len(lines) - 1
	if lines[last] == "" {
		lines = lines[:last]
	} else {
		lines[last] += "\n"
	}
	return lines
}

// parseHunks finds @@ -a,b +c,d @@ headers.
func parseHunks(lines []string) []Hunk {
	var hunks []Hunk
	for i, l := range lines {
		if !strings.HasPrefix(l, "@@") {
			continue
		}
		start, count := hunkRange(l, '+')
		end := start + count - 1
		if count == 0 {
			end = start
		}
		hunks = append(hunks, Hunk{Index: i, NewStart: start, NewEnd: end})
	}
	return hunks
}

// LineKind classifies one rendered diff line.
type LineKind int

const (
	KindOther   LineKind = iota // e.g. "\ No newline at end of file"
	KindHeader                  // ---/+++ file headers
	KindHunk                    // @@ header
	KindContext                 // unchanged line present on both sides
	KindAdd                     // line only in the new file
	KindDel                     // line only in the old file
)

// LineRef says where a diff line's content lives: Old/New are 1-based line
// numbers in the baseline / current file (0 when not applicable). Renderers
// use it to pull syntax-highlighted text from the right side.
type LineRef struct {
	Kind LineKind
	Old  int
	New  int
}

// Annotate walks unified diff lines and maps each to its origin.
func Annotate(lines []string) []LineRef {
	refs := make([]LineRef, len(lines))
	inHunk := false
	old, nw := 0, 0
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"):
			inHunk = true
			old, _ = hunkRange(l, '-')
			nw, _ = hunkRange(l, '+')
			refs[i] = LineRef{Kind: KindHunk}
		case !inHunk:
			if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") {
				refs[i] = LineRef{Kind: KindHeader}
			}
		case strings.HasPrefix(l, "+"):
			refs[i] = LineRef{Kind: KindAdd, New: nw}
			nw++
		case strings.HasPrefix(l, "-"):
			refs[i] = LineRef{Kind: KindDel, Old: old}
			old++
		case strings.HasPrefix(l, " "), l == "":
			refs[i] = LineRef{Kind: KindContext, Old: old, New: nw}
			old++
			nw++
		}
	}
	return refs
}

// hunkRange extracts the "<sign>start,count" range from an @@ header.
func hunkRange(header string, sign byte) (start, count int) {
	idx := strings.IndexByte(header, sign)
	if idx < 0 {
		return 1, 0
	}
	rest := header[idx+1:]
	if sp := strings.IndexAny(rest, " @"); sp >= 0 {
		rest = rest[:sp]
	}
	s, c := rest, "1"
	if comma := strings.Index(rest, ","); comma >= 0 {
		s, c = rest[:comma], rest[comma+1:]
	}
	start, _ = strconv.Atoi(s)
	count, _ = strconv.Atoi(c)
	if start < 1 {
		start = 1
	}
	return start, count
}

// HunkAt returns the index of the hunk containing rendered line idx, or -1.
func (r Result) HunkAt(idx int) int {
	h := -1
	for i, hk := range r.Hunks {
		if hk.Index <= idx {
			h = i
		}
	}
	return h
}

// Reference describes the current hunk as "path:start-end".
func (r Result) Reference(hunk int) string {
	if hunk < 0 || hunk >= len(r.Hunks) {
		return r.Path
	}
	h := r.Hunks[hunk]
	if h.NewStart == h.NewEnd {
		return fmt.Sprintf("%s:%d", r.Path, h.NewStart)
	}
	return fmt.Sprintf("%s:%d-%d", r.Path, h.NewStart, h.NewEnd)
}
