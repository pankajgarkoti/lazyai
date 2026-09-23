package app

import (
	"os"
	"path/filepath"
	"testing"

	"lazyai/internal/config"
	"lazyai/internal/hooks"
)

func TestSnapshotAcknowledgesCapturedDirtyBaseline(t *testing.T) {
	h := newHarness(t)
	p := filepath.Join(h.root, "dirty.txt")
	if err := os.WriteFile(p, []byte("pre-existing dirty contents"), 0600); err != nil {
		t.Fatal(err)
	}
	reply := make(chan hooks.Reply, 1)
	h.hook(hooks.Event{Type: "file.snapshot", Path: p, Reply: reply})
	select {
	case r := <-reply:
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	default:
		t.Fatal("snapshot was not acknowledged")
	}
	if err := os.WriteFile(p, []byte("agent edit"), 0600); err != nil {
		t.Fatal(err)
	}
	h.hook(hooks.Event{Type: "file.write", Path: p})
	data, existed, known := h.m.ledger.Baseline("dirty.txt")
	if !known || !existed || string(data) != "pre-existing dirty contents" {
		t.Fatalf("baseline=%q existed=%v known=%v", data, existed, known)
	}
}

func TestProjectBackendReloadRequiresRestart(t *testing.T) {
	h := newHarness(t)
	h.m.cfg.Backend = "opencode"
	h.m.cfg.LoadConfig = func() (config.Config, []string, error) {
		return config.Parse([]byte("version: 1\nagent:\n  backend: codex\n"))
	}
	h.m.reloadConfig()
	if !h.m.backendPending || h.m.backend() != "opencode" {
		t.Fatalf("reload switched live backend: %s pending=%v", h.m.backend(), h.m.backendPending)
	}
	h.m.cfg.Backend = "codex"
	h.m.reloadConfig()
	if h.m.backendPending {
		t.Fatal("matching backend still pending")
	}
}

func TestCodexAttentionSurvivesUnrelatedConcurrentCalls(t *testing.T) {
	h := newHarness(t)
	h.m.cfg.Backend = "codex"
	h.hook(hooks.Event{Type: "hello", Component: "tools", Backend: "codex"})
	if h.m.pluginOK {
		t.Fatal("MCP alone must not imply hook readiness")
	}
	h.hook(hooks.Event{Type: "hello", Component: "hooks", Backend: "codex"})
	if !h.m.pluginOK {
		t.Fatal("both components should be ready")
	}
	h.hook(hooks.Event{Type: "attention", CallID: "question", Backend: "codex"})
	h.hook(hooks.Event{Type: "tool.before", CallID: "other", Backend: "codex"})
	h.hook(hooks.Event{Type: "attention.clear", CallID: "other", Backend: "codex"})
	h.hook(hooks.Event{Type: "tool.after", CallID: "other", Backend: "codex"})
	if !h.m.attention {
		t.Fatal("unrelated call cleared pending question")
	}
	h.hook(hooks.Event{Type: "attention.clear", CallID: "question", Backend: "codex"})
	if h.m.attention {
		t.Fatal("answered question is still pending")
	}
	h.hook(hooks.Event{Type: "goodbye", Component: "tools", Backend: "codex"})
	if h.m.pluginOK {
		t.Fatal("dead MCP server still reported healthy")
	}
}
