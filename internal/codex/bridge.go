// Package codex translates Codex hooks and MCP calls into LazyAI host events.
package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazyai/internal/hooks"
)

type Bridge struct {
	Root, URL, Token string
	Client           *http.Client
}

func FromEnv() Bridge {
	return Bridge{Root: os.Getenv("LAZYAI_WORKTREE"), URL: os.Getenv("LAZYAI_HOOK_URL"), Token: os.Getenv("LAZYAI_HOOK_TOKEN")}
}

func (b Bridge) Send(ev hooks.Event) (json.RawMessage, error) {
	if b.URL == "" || b.Token == "" {
		return nil, fmt.Errorf("LazyAI is not connected")
	}
	ev.Backend = "codex"
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, b.URL+"/event", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Content-Type", "application/json")
	client := b.Client
	if client == nil {
		timeout := 2 * time.Second
		if ev.Type == "setup" {
			timeout = 125 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("LazyAI: %s", strings.TrimSpace(string(data)))
	}
	return data, nil
}

// HookInput matches the released Codex hook payload, not transcript internals.
type HookInput struct {
	Event     string          `json:"hook_event_name"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Tool      string          `json:"tool_name"`
	CallID    string          `json:"tool_use_id"`
	CWD       string          `json:"cwd"`
	Input     json.RawMessage `json:"tool_input"`
	Response  json.RawMessage `json:"tool_response"`
}

const Instructions = `You are running inside LazyAI. Use the lazyai MCP show_locations tool to point the user at exact code with notes. Use setup_workstreams only when the user asks to open workstreams; it uses the project's selected agent and preserves focus. Prefer the lazyai MCP read_file tool for reading workspace text files so reads appear in the sidebar. Shell-based reads are not automatically tracked. References like [path:line — note] identify code the user is discussing: read its current contents. Prompts beginning with contract: are structured instructions; treat every supplied field as binding. LazyAI owns worktrees; do not create a second worktree for the same task.`

func (b Bridge) Hook(in io.Reader, out io.Writer) error {
	var h HookInput
	if err := json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&h); err != nil {
		return err
	}
	// Emit readiness only after an actual lifecycle callback reaches the bridge.
	if _, err := b.Send(hooks.Event{Type: "hello", Component: "hooks", SessionID: h.SessionID}); err != nil {
		return err
	}
	send := func(kind string) error {
		_, err := b.Send(hooks.Event{Type: kind, SessionID: h.SessionID, Tool: h.Tool, CallID: h.SessionID + ":" + h.TurnID + ":" + h.CallID})
		return err
	}
	switch h.Event {
	case "SessionStart", "SubagentStart":
		return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": h.Event, "additionalContext": Instructions}})
	case "PreToolUse":
		if err := send("tool.before"); err != nil {
			return err
		}
		if h.Tool == "request_user_input" {
			if err := send("attention"); err != nil {
				return err
			}
		}
		if h.Tool == "apply_patch" {
			if err := b.patchEvents(h, "file.snapshot"); err != nil {
				return err
			}
		}
	case "PostToolUse":
		if h.Tool == "apply_patch" {
			if err := b.patchEvents(h, "file.write"); err != nil {
				return err
			}
		}
		if _, err := b.Send(hooks.Event{Type: "attention.clear", CallID: permissionID(h)}); err != nil {
			return err
		}
		if h.Tool == "request_user_input" {
			if err := send("attention.clear"); err != nil {
				return err
			}
		}
		if err := send("tool.after"); err != nil {
			return err
		}
	case "PermissionRequest":
		if _, err := b.Send(hooks.Event{Type: "attention", SessionID: h.SessionID, Tool: h.Tool, CallID: permissionID(h)}); err != nil {
			return err
		}
	case "Stop", "Interrupt", "SessionEnd":
		if err := send("idle"); err != nil {
			return err
		}
	case "UserPromptSubmit":
		if _, err := b.Send(hooks.Event{Type: "attention.clear"}); err != nil {
			return err
		}
	}
	// Stop and Interrupt require JSON, never prose. No permission decisions.
	_, err := io.WriteString(out, "{}\n")
	return err
}

// PermissionRequest omits tool_use_id; scope its attention to this turn/tool.
func permissionID(h HookInput) string {
	return "permission:" + h.SessionID + ":" + h.TurnID + ":" + h.Tool
}

func (b Bridge) patchEvents(h HookInput, kind string) error {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(h.Input, &input); err != nil {
		return err
	}
	paths, err := PatchPaths(input.Command)
	if err != nil {
		return err
	}
	root := h.CWD
	if root == "" {
		root = b.Root
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := b.Send(hooks.Event{Type: kind, Path: p, SessionID: h.SessionID, CallID: h.CallID, Tool: h.Tool}); err != nil {
			return err
		}
	}
	return nil
}

// PatchPaths includes both sides of moves and every file in a multi-file patch.
func PatchPaths(patch string) ([]string, error) {
	if !strings.HasPrefix(strings.TrimSpace(patch), "*** Begin Patch") {
		return nil, fmt.Errorf("unrecognized Codex patch payload")
	}
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				p := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "\r")
				if p == "" {
					return nil, fmt.Errorf("empty patch path")
				}
				if !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("patch contains no file targets")
	}
	return paths, nil
}
