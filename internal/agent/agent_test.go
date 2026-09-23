package agent

import (
	"strings"
	"testing"

	"lazyai/internal/config"
)

func TestBackendLaunchIsolation(t *testing.T) {
	for _, name := range []string{"opencode", "codex"} {
		b := Backend{Name: name, configDir: "/cache/opencode"}
		args, env := b.Launch("/bin/lazy ai", "/worktree", "http://127.0.0.1:1234", "secret", []string{"--model", "example"})
		joined := strings.Join(args, " ") + strings.Join(env, " ")
		if name == "codex" {
			if strings.Contains(joined, "OPENCODE_CONFIG_DIR") || strings.Contains(joined, "CODEX_HOME=") || strings.Contains(joined, "bypass") {
				t.Fatalf("config isolation: %s", joined)
			}
			for _, want := range []string{"hooks.SessionStart=", "hooks.PreToolUse=", "hooks.PermissionRequest=", "mcp_servers.lazyai=", "LAZYAI_HOOK_TOKEN"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("missing %s", want)
				}
			}
		}
		if args[len(args)-2] != "--model" || args[len(args)-1] != "example" {
			t.Fatalf("lost passthrough: %v", args)
		}
	}
}

func TestMissingAgentAndConflictingOverrideFail(t *testing.T) {
	if _, err := Prepare(config.Agent{Backend: "codex", Executable: "/nonexistent/lazyai-test-codex"}, ""); err == nil {
		t.Fatal("missing codex silently fell back")
	}
	if _, err := Prepare(config.Agent{Backend: "codex"}, "/bin/cat"); err == nil {
		t.Fatal("OpenCode override accepted for Codex")
	}
}
