package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectAgentSelection(t *testing.T) {
	for _, tc := range []struct {
		yaml, backend string
		invalid       bool
	}{
		{"version: 1\n", "opencode", false},
		{"version: 1\nagent:\n  backend: codex\n", "codex", false},
		{"version: 1\nagent:\n  backend: typo\n", "", true},
	} {
		cfg, warnings, err := Parse([]byte(tc.yaml))
		if tc.invalid {
			if err == nil {
				t.Fatal("invalid backend accepted")
			}
			continue
		}
		if err != nil || len(warnings) != 0 || cfg.Agent.Backend != tc.backend {
			t.Fatalf("config=%+v warnings=%v err=%v", cfg, warnings, err)
		}
	}
}

func TestBrokenContractDoesNotChangeSelectedAgent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, Dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), []byte("version: 1\nagent:\n  backend: codex\ninteractive:\n  strict: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root); err == nil {
		t.Fatal("invalid strict templates accepted")
	}
	agent, err := LoadAgent(root)
	if err != nil || agent.Backend != "codex" {
		t.Fatalf("agent=%+v err=%v", agent, err)
	}
}
