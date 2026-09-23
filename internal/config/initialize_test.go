package config

import (
	"bytes"
	"os"
	"testing"
)

func TestInitializeNeverOverwritesExistingConfiguration(t *testing.T) {
	root := t.TempDir()
	choices := InitialChoices{Agent: Agent{Backend: "codex", Executable: "/path with spaces/codex"}, Strict: true, DefaultContract: "verification"}
	created, err := Initialize(root, choices)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	before, _ := os.ReadFile(Path(root))
	created, err = Initialize(root, InitialChoices{Agent: Agent{Backend: "opencode"}})
	after, _ := os.ReadFile(Path(root))
	if err != nil || created || !bytes.Equal(before, after) {
		t.Fatal("existing configuration overwritten")
	}
	cfg, _, err := Parse(after)
	if err != nil || cfg.Agent != choices.Agent || !cfg.Interactive.Strict || cfg.Interactive.DefaultContract != "verification" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}
