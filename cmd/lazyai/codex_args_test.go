package main

import (
	"reflect"
	"testing"
)

func TestCodexResumeKeepsConfigAndSandboxFlags(t *testing.T) {
	base := []string{"-c", "model_reasoning_effort=high", "-s", "workspace-write"}
	want := []string{"resume", "codex-session", "-c", "model_reasoning_effort=high", "-s", "workspace-write"}
	if got := codexArgs(base, "codex-session"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}
	if got := codexArgs([]string{"resume", "explicit"}, "stored"); !reflect.DeepEqual(got, []string{"resume", "explicit"}) {
		t.Fatalf("explicit selection lost: %v", got)
	}
	if len(base) != 4 || base[0] != "-c" {
		t.Fatal("mutated base arguments")
	}
}
