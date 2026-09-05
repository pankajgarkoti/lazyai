// Package fuzzy is a small subsequence matcher for live-filtering short lists
// (branch names, files) as the user types.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

// Positions returns the candidate rune indexes matched by query, greedily
// left to right, or ok=false when query is not a subsequence of candidate.
func Positions(query, candidate string) ([]int, bool) {
	q := []rune(strings.ToLower(query))
	c := []rune(strings.ToLower(candidate))
	pos := make([]int, 0, len(q))
	ci := 0
	for _, qr := range q {
		for ci < len(c) && c[ci] != qr {
			ci++
		}
		if ci == len(c) {
			return nil, false
		}
		pos = append(pos, ci)
		ci++
	}
	return pos, true
}

// Match scores how well query matches candidate; higher is better. It
// rewards matches at the start, at segment boundaries (after / - _ .) and
// contiguous runs, and penalises gaps and long candidates.
func Match(query, candidate string) (int, bool) {
	pos, ok := Positions(query, candidate)
	if !ok {
		return 0, false
	}
	if len(pos) == 0 {
		return 0, true
	}
	c := []rune(candidate)
	score := 0
	prev := -2
	for _, p := range pos {
		switch {
		case p == 0:
			score += 12
		case isBoundary(c[p-1]):
			score += 8
		case p == prev+1:
			score += 6
		default:
			score += 1
		}
		if p != prev+1 && prev >= 0 {
			score -= 2 * min(p-prev-1, 5)
		}
		prev = p
	}
	score -= len(c) / 8
	return score, true
}

func isBoundary(r rune) bool {
	return r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsSpace(r)
}

// Rank filters candidates to those matching query, best first (stable).
func Rank(query string, candidates []string) []string {
	type scored struct {
		s    string
		n    int
		orig int
	}
	var out []scored
	for i, c := range candidates {
		if n, ok := Match(query, c); ok {
			out = append(out, scored{c, n, i})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].orig < out[j].orig
	})
	res := make([]string, len(out))
	for i, s := range out {
		res[i] = s.s
	}
	return res
}
