package fuzzy

import (
	"reflect"
	"testing"
)

func TestMatchAndRank(t *testing.T) {
	if _, ok := Match("fb", "feat/beta"); !ok {
		t.Fatal("subsequence should match")
	}
	if _, ok := Match("zz", "feat/beta"); ok {
		t.Fatal("no match expected")
	}
	if _, ok := Match("", "anything"); !ok {
		t.Fatal("empty query matches everything")
	}
	// Case-insensitive.
	if _, ok := Match("FB", "feat/beta"); !ok {
		t.Fatal("case-insensitive")
	}
	// Ranking: prefix > word-boundary > scattered; ties keep input order.
	got := Rank("fe", []string{"safe/one", "feat/beta", "fix/eta", "feat/alpha"})
	want := []string{"feat/beta", "feat/alpha", "fix/eta", "safe/one"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rank %v", got)
	}
	// Segment-boundary matches: "cfg" should prefer feat/config over "refactor-gui".
	got = Rank("cfg", []string{"refactor-gui", "feat/config"})
	if got[0] != "feat/config" {
		t.Fatalf("boundary rank %v", got)
	}
	// Positions are returned for highlighting.
	pos, ok := Positions("fb", "feat/beta")
	if !ok || !reflect.DeepEqual(pos, []int{0, 5}) {
		t.Fatalf("positions %v %v", pos, ok)
	}
}
