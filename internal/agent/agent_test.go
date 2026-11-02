package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"lazyai/internal/config"
)

func TestBackendLaunchIsolation(t *testing.T) {
	for _, name := range []string{"opencode", "codex", "claude"} {
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
		if name == "claude" {
			if args[0] != "--mcp-config" || args[2] != "--settings" || strings.Contains(joined, "OPENCODE_CONFIG_DIR") || strings.Contains(joined, "dangerously") {
				t.Fatalf("claude launch: %v", args)
			}
			var mcp struct {
				MCPServers map[string]struct {
					Command string            `json:"command"`
					Args    []string          `json:"args"`
					Env     map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			var settings struct {
				Hooks map[string][]struct {
					Hooks []struct{ Command string } `json:"hooks"`
				} `json:"hooks"`
			}
			if json.Unmarshal([]byte(args[1]), &mcp) != nil || json.Unmarshal([]byte(args[3]), &settings) != nil {
				t.Fatalf("invalid JSON: %v", args)
			}
			srv := mcp.MCPServers["lazyai"]
			if srv.Command != "/bin/lazy ai" || srv.Args[0] != "__mcp" || srv.Env["LAZYAI_HOOK_TOKEN"] != "secret" || srv.Env["LAZYAI_BACKEND"] != "claude" {
				t.Fatalf("mcp=%+v", srv)
			}
			for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop"} {
				if h := settings.Hooks[event]; len(h) != 1 || h[0].Hooks[0].Command != "'/bin/lazy ai' __claude-hook" {
					t.Fatalf("hook %s=%+v", event, h)
				}
			}
			if !strings.Contains(joined, "LAZYAI_BACKEND=claude") {
				t.Fatalf("env=%v", env)
			}
		}
		if name == "opencode" {
			if !strings.Contains(joined, "--standalone") || !strings.Contains(joined, "OPENCODE_CONFIG_DIR=/cache/opencode") || strings.Contains(joined, "hooks.SessionStart=") {
				t.Fatalf("OpenCode 2 must have a private integration server: %s", joined)
			}
		}
		if args[len(args)-2] != "--model" || args[len(args)-1] != "example" {
			t.Fatalf("lost passthrough: %v", args)
		}
	}
}

func TestMissingAgentAndConflictingOverrideFail(t *testing.T) {
	if _, err := Prepare(config.Agent{Backend: "opencode2"}, ""); err == nil {
		t.Fatal("removed opencode2 backend accepted")
	}
	if _, err := Prepare(config.Agent{Backend: "codex", Executable: "/nonexistent/lazyai-test-codex"}, ""); err == nil {
		t.Fatal("missing codex silently fell back")
	}
	if _, err := Prepare(config.Agent{Backend: "codex"}, "/bin/cat"); err == nil {
		t.Fatal("OpenCode override accepted for Codex")
	}
}
