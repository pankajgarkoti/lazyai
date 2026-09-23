package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lazyai/internal/hooks"
)

func TestPatchTargetsIncludesMovesAndDeletes(t *testing.T) {
	got, err := PatchPaths("*** Begin Patch\n*** Update File: a b.txt\n*** Move to: moved.txt\n@@\n-old\n+new\n*** Delete File: gone.txt\n*** Add File: new.txt\n+x\n*** End Patch")
	if err != nil || !reflect.DeepEqual(got, []string{"a b.txt", "moved.txt", "gone.txt", "new.txt"}) {
		t.Fatalf("paths=%v err=%v", got, err)
	}
	if _, err := PatchPaths("unexpected wire format"); err == nil {
		t.Fatal("unknown format silently accepted")
	}
}

func testBridge(t *testing.T, receive func(hooks.Event) (any, error)) Bridge {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var ev hooks.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if ev.Backend != "codex" {
			http.Error(w, "missing backend", 400)
			return
		}
		result, err := receive(ev)
		if err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		if result == nil {
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(s.Close)
	return Bridge{Root: t.TempDir(), URL: s.URL, Token: "token"}
}

func TestHooksWaitForSnapshotAndKeepCallIdentity(t *testing.T) {
	var events []hooks.Event
	b := testBridge(t, func(ev hooks.Event) (any, error) { events = append(events, ev); return nil, nil })
	input := HookInput{Event: "PreToolUse", SessionID: "session", TurnID: "turn", CallID: "call", Tool: "apply_patch", CWD: b.Root, Input: json.RawMessage(`{"command":"*** Begin Patch\n*** Update File: dirty.txt\n@@\n-old\n+new\n*** End Patch"}`)}
	data, _ := json.Marshal(input)
	var out bytes.Buffer
	if err := b.Hook(bytes.NewReader(data), &out); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Component != "hooks" || events[1].Type != "tool.before" || events[1].CallID != "session:turn:call" || events[2].Type != "file.snapshot" || events[2].Path != filepath.Join(b.Root, "dirty.txt") {
		t.Fatalf("events=%+v", events)
	}
	if out.String() != "{}\n" {
		t.Fatalf("unexpected hook decisions: %q", out.String())
	}
}

func TestSnapshotFailureIsReported(t *testing.T) {
	b := testBridge(t, func(ev hooks.Event) (any, error) {
		if ev.Type == "file.snapshot" {
			return nil, fmt.Errorf("cannot snapshot")
		}
		return nil, nil
	})
	h := HookInput{Event: "PreToolUse", Tool: "apply_patch", Input: json.RawMessage(`{"command":"*** Begin Patch\n*** Delete File: a\n*** End Patch"}`)}
	data, _ := json.Marshal(h)
	if err := b.Hook(bytes.NewReader(data), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "cannot snapshot") {
		t.Fatalf("err=%v", err)
	}
}

func TestMCPHandshakeReadAndToolErrors(t *testing.T) {
	var events []hooks.Event
	b := testBridge(t, func(ev hooks.Event) (any, error) {
		events = append(events, ev)
		if ev.Type == "setup" {
			return nil, fmt.Errorf("declined by user")
		}
		return nil, nil
	})
	if err := os.WriteFile(filepath.Join(b.Root, "code.txt"), []byte("first\nsecond\nthird\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"code.txt","offset":2,"limit":1}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"setup_workstreams","arguments":{"workstreams":[{"branch":"feature","nickname":"feature"}]}}}
{"jsonrpc":"2.0","id":5,"method":"ping"}
`
	var out bytes.Buffer
	if err := b.ServeMCP(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("responses=%s", out.String())
	}
	for _, want := range []string{"show_locations", "setup_workstreams", "read_file"} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("missing %s: %s", want, lines[1])
		}
	}
	if !strings.Contains(lines[2], "2: second") || strings.Contains(lines[2], "1: first") || !strings.Contains(lines[3], `"isError":true`) || !strings.Contains(lines[3], "declined by user") {
		t.Fatalf("responses=%s", out.String())
	}
	if len(events) != 4 || events[0].Component != "tools" || events[1].Type != "file.read" || events[2].Type != "setup" || events[3].Type != "goodbye" {
		t.Fatalf("events=%+v", events)
	}
}

func TestReadRejectsEscapingSymlinks(t *testing.T) {
	b := Bridge{Root: t.TempDir()}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(b.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.readPath("link"); err == nil {
		t.Fatal("symlink escaped workspace")
	}
}
