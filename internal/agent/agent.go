// Package agent prepares native CLI processes; LazyAI continues to own their PTYs.
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazyai/internal/config"
	"lazyai/internal/integration"
)

type Backend struct {
	Name       string
	Executable string
	configDir  string
}

func Prepare(cfg config.Agent, opencodeOverride string) (Backend, error) {
	if cfg.Backend == "" {
		cfg.Backend = "opencode"
	}
	if cfg.Backend != "opencode" && cfg.Backend != "codex" {
		return Backend{}, fmt.Errorf("unknown agent %q", cfg.Backend)
	}
	bin := cfg.Executable
	if bin == "" {
		bin = cfg.Backend
	}
	if opencodeOverride != "" {
		if cfg.Backend != "opencode" {
			return Backend{}, fmt.Errorf("--opencode cannot override a %s project; use agent.executable", cfg.Backend)
		}
		bin = opencodeOverride
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return Backend{}, fmt.Errorf("%s executable %q unavailable: %w", cfg.Backend, bin, err)
	}
	// Children run in different worktrees; retain the executable we validated.
	path, err = filepath.Abs(path)
	if err != nil {
		return Backend{}, err
	}
	b := Backend{Name: cfg.Backend, Executable: path}
	if b.Name == "codex" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		var major, minor, patch int
		if err != nil {
			return Backend{}, fmt.Errorf("check Codex version: %w", err)
		}
		if n, _ := fmt.Sscanf(strings.TrimSpace(string(out)), "codex-cli %d.%d.%d", &major, &minor, &patch); n != 3 || major == 0 && (minor < 155 || minor == 155 && patch < 1) {
			return Backend{}, fmt.Errorf("Codex 0.155.1 or newer is required for LazyAI hooks (found %q)", strings.TrimSpace(string(out)))
		}
	}
	if b.Name == "opencode" {
		dir, err := integration.DefaultDir()
		if err != nil {
			return Backend{}, err
		}
		b.configDir, err = integration.Materialize(dir)
		if err != nil {
			return Backend{}, err
		}
	}
	return b, nil
}

// Launch augments this invocation only. User config, auth and CODEX_HOME remain intact.
func (b Backend) Launch(lazyai, root, url, token string, extra []string) (args, env []string) {
	env = []string{"LAZYAI=1", "LAZYAI_WORKTREE=" + root, "LAZYAI_HOOK_URL=" + url, "LAZYAI_HOOK_TOKEN=" + token}
	if b.Name == "opencode" {
		return append([]string{}, extra...), append(env, "OPENCODE_CONFIG_DIR="+b.configDir)
	}
	// A stable command keeps Codex's native hook trust valid across launches.
	command := "'" + strings.ReplaceAll(lazyai, "'", "'\"'\"'") + "' __codex-hook"
	for _, event := range []string{"SessionStart", "SubagentStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Interrupt", "SessionEnd"} {
		args = append(args, "-c", "hooks."+event+"=[{hooks=[{type=\"command\",command="+strconv.Quote(command)+",timeout=3}]}]")
	}
	// Explicit MCP env also works when Codex filters the inherited environment.
	mcp := fmt.Sprintf("mcp_servers.lazyai={command=%s,args=[\"__mcp\"],env={LAZYAI_WORKTREE=%s,LAZYAI_HOOK_URL=%s,LAZYAI_HOOK_TOKEN=%s},tool_timeout_sec=130}", strconv.Quote(lazyai), strconv.Quote(root), strconv.Quote(url), strconv.Quote(token))
	args = append(args, "-c", mcp)
	return append(args, extra...), env
}
