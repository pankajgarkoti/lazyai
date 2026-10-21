package codex

import (
	"encoding/json"
	"io"
	"path/filepath"

	"lazyai/internal/hooks"
)

// ClaudeInstructions differs from Codex's: Claude Code's native Read, Write and
// Edit tools are tracked by hooks, so read_file is not needed for the sidebar.
const ClaudeInstructions = `You are running inside LazyAI. Use the lazyai MCP show_locations tool to point the user at exact code with notes. Use setup_workstreams only when the user asks to open workstreams; it uses the project's selected agent and preserves focus. Your Read, Write and Edit tool calls appear in LazyAI's file sidebar; shell-based reads and writes are not tracked. References like [path:line — note] identify code the user is discussing: read its current contents. Prompts beginning with contract: are structured instructions; treat every supplied field as binding. LazyAI owns worktrees; do not create a second worktree for the same task.`

// ClaudeHookEvents are the Claude Code lifecycle events LazyAI registers.
var ClaudeHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop", "SessionEnd"}

func (b Bridge) instructions() string {
	if b.backend() == "claude" {
		return ClaudeInstructions
	}
	return Instructions
}

// claudeToolPath returns the file a Claude Code file tool reads or changes.
func claudeToolPath(tool string, input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	switch tool {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
	default:
		return ""
	}
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	if in.NotebookPath != "" {
		return in.NotebookPath
	}
	return in.FilePath
}

// ClaudeHook handles one Claude Code hook invocation. Claude payloads carry no
// turn ID; tool_use_id is unique within a session.
func (b Bridge) ClaudeHook(in io.Reader, out io.Writer) error {
	b.Backend = "claude"
	var h HookInput
	if err := json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&h); err != nil {
		return err
	}
	if _, err := b.Send(hooks.Event{Type: "hello", Component: "hooks", SessionID: h.SessionID}); err != nil {
		return err
	}
	send := func(kind string) error {
		_, err := b.Send(hooks.Event{Type: kind, SessionID: h.SessionID, Tool: h.Tool, CallID: h.SessionID + ":" + h.CallID})
		return err
	}
	file := func(kind string) error {
		p := claudeToolPath(h.Tool, h.Input)
		if p == "" {
			return nil
		}
		if !filepath.IsAbs(p) {
			root := h.CWD
			if root == "" {
				root = b.Root
			}
			p = filepath.Join(root, p)
		}
		_, err := b.Send(hooks.Event{Type: kind, Path: p, SessionID: h.SessionID, CallID: h.CallID, Tool: h.Tool})
		return err
	}
	switch h.Event {
	case "SessionStart":
		if _, err := b.Send(hooks.Event{Type: "session", SessionID: h.SessionID}); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": h.Event, "additionalContext": ClaudeInstructions}})
	case "PreToolUse":
		if err := send("tool.before"); err != nil {
			return err
		}
		if h.Tool == "AskUserQuestion" {
			if err := send("attention"); err != nil {
				return err
			}
		}
		if h.Tool != "Read" {
			// Blocks until the host holds the pre-image, so diffs have a baseline.
			if err := file("file.snapshot"); err != nil {
				return err
			}
		}
	case "PostToolUse", "PostToolUseFailure":
		if h.Event == "PostToolUse" {
			kind := "file.write"
			if h.Tool == "Read" {
				kind = "file.read"
			}
			if err := file(kind); err != nil {
				return err
			}
		}
		if _, err := b.Send(hooks.Event{Type: "attention.clear", CallID: claudePermissionID(h)}); err != nil {
			return err
		}
		if h.Tool == "AskUserQuestion" {
			if err := send("attention.clear"); err != nil {
				return err
			}
		}
		if err := send("tool.after"); err != nil {
			return err
		}
	case "PermissionRequest":
		if _, err := b.Send(hooks.Event{Type: "attention", SessionID: h.SessionID, Tool: h.Tool, CallID: claudePermissionID(h)}); err != nil {
			return err
		}
	case "Stop", "SessionEnd":
		if err := send("idle"); err != nil {
			return err
		}
	case "UserPromptSubmit":
		if _, err := b.Send(hooks.Event{Type: "attention.clear"}); err != nil {
			return err
		}
	}
	// An empty object makes no permission or blocking decisions.
	_, err := io.WriteString(out, "{}\n")
	return err
}

// PermissionRequest omits tool_use_id; scope its attention to this session/tool.
func claudePermissionID(h HookInput) string {
	return "permission:" + h.SessionID + ":" + h.Tool
}
