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

func TestClaudeResumeKeepsFlagsAndExplicitSelection(t *testing.T) {
	base := []string{"--model", "opus"}
	if got := claudeArgs(base, "sid"); !reflect.DeepEqual(got, []string{"--resume", "sid", "--model", "opus"}) {
		t.Fatalf("args=%v", got)
	}
	for _, explicit := range [][]string{{"--continue"}, {"-r", "other"}, {"--resume=other"}, {"--session-id", "u"}} {
		if got := claudeArgs(explicit, "sid"); !reflect.DeepEqual(got, explicit) {
			t.Fatalf("explicit selection lost: %v", got)
		}
	}
}
