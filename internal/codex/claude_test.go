package codex

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lazyai/internal/hooks"
)

func runClaudeHook(t *testing.T, b Bridge, in HookInput) string {
	t.Helper()
	data, _ := json.Marshal(in)
	var out bytes.Buffer
	if err := b.ClaudeHook(bytes.NewReader(data), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestClaudeEditsSnapshotBeforeAndRecordAfter(t *testing.T) {
	var events []hooks.Event
	b := testBridge(t, func(ev hooks.Event) (any, error) { events = append(events, ev); return nil, nil })
	edit := HookInput{Event: "PreToolUse", SessionID: "s", CallID: "toolu_1", Tool: "Edit", CWD: b.Root, Input: json.RawMessage(`{"file_path":"dirty.txt","old_string":"a","new_string":"b"}`)}
	if out := runClaudeHook(t, b, edit); out != "{}\n" {
		t.Fatalf("unexpected hook decisions: %q", out)
	}
	if len(events) != 3 || events[1].Type != "tool.before" || events[1].CallID != "s:toolu_1" || events[2].Type != "file.snapshot" || events[2].Path != filepath.Join(b.Root, "dirty.txt") {
		t.Fatalf("events=%+v", events)
	}
	for _, ev := range events {
		if ev.Backend != "claude" {
			t.Fatalf("backend=%q", ev.Backend)
		}
	}
	events = nil
	edit.Event = "PostToolUse"
	runClaudeHook(t, b, edit)
	if len(events) != 4 || events[1].Type != "file.write" || events[3].Type != "tool.after" || events[3].CallID != "s:toolu_1" {
		t.Fatalf("events=%+v", events)
	}
}

func TestClaudeReadsAreTrackedWithoutSnapshots(t *testing.T) {
	var kinds []string
	b := testBridge(t, func(ev hooks.Event) (any, error) { kinds = append(kinds, ev.Type); return nil, nil })
	read := HookInput{Event: "PreToolUse", SessionID: "s", CallID: "c", Tool: "Read", Input: json.RawMessage(`{"file_path":"/abs/x.go"}`)}
	runClaudeHook(t, b, read)
	read.Event = "PostToolUse"
	runClaudeHook(t, b, read)
	if got := strings.Join(kinds, ","); got != "hello,tool.before,hello,file.read,attention.clear,tool.after" {
		t.Fatalf("events=%s", got)
	}
	kinds = nil
	runClaudeHook(t, b, HookInput{Event: "PostToolUseFailure", SessionID: "s", CallID: "c", Tool: "Write", Input: json.RawMessage(`{"file_path":"/abs/x.go"}`)})
	if got := strings.Join(kinds, ","); got != "hello,attention.clear,tool.after" {
		t.Fatalf("failed write recorded: %s", got)
	}
}

func TestClaudeSessionStartAndAttention(t *testing.T) {
	var events []hooks.Event
	b := testBridge(t, func(ev hooks.Event) (any, error) { events = append(events, ev); return nil, nil })
	out := runClaudeHook(t, b, HookInput{Event: "SessionStart", SessionID: "s"})
	if !strings.Contains(out, "additionalContext") || events[1].Type != "session" || events[1].SessionID != "s" {
		t.Fatalf("out=%q events=%+v", out, events)
	}
	events = nil
	runClaudeHook(t, b, HookInput{Event: "PermissionRequest", SessionID: "s", Tool: "Bash"})
	if events[1].Type != "attention" || events[1].CallID != "permission:s:Bash" {
		t.Fatalf("events=%+v", events)
	}
	events = nil
	runClaudeHook(t, b, HookInput{Event: "Stop", SessionID: "s"})
	if events[1].Type != "idle" {
		t.Fatalf("events=%+v", events)
	}
}
