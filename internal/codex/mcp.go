package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"lazyai/internal/hooks"
)

// ServeMCP implements the stdio MCP surface needed by the native CLI.
// Each JSON-RPC message occupies one line. Diagnostics never go to stdout.
func (b Bridge) ServeMCP(in io.Reader, out io.Writer) error {
	defer func() { _, _ = b.Send(hooks.Event{Type: "goodbye", Component: "tools"}) }()
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 1<<20)
	enc := json.NewEncoder(out)
	for scan.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "invalid JSON"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		var result any
		switch req.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &params)
			version := params.ProtocolVersion
			switch version {
			case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
			default:
				version = "2025-06-18"
			}
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "lazyai", "version": "1"}, "instructions": Instructions}
			if _, err := b.Send(hooks.Event{Type: "hello", Component: "tools"}); err != nil {
				return err
			}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": toolDefinitions()}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			var text string
			err := json.Unmarshal(req.Params, &params)
			if err == nil {
				text, err = b.callTool(params.Name, params.Arguments)
			}
			if err != nil {
				text = err.Error()
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}, "isError": err != nil}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		if result != nil {
			response["result"] = result
		}
		if err := enc.Encode(response); err != nil {
			return err
		}
	}
	return scan.Err()
}

func toolDefinitions() []any {
	// Schemas mirror the OpenCode tools; validation stays in the host.
	var tools []any
	_ = json.Unmarshal([]byte(`[
 {"name":"show_locations","description":"Show the user exact code locations in LazyAI, with notes. Send the complete ordered set; a new call replaces the previous set. Use when code explains something better than prose.","inputSchema":{"type":"object","required":["locations"],"properties":{"title":{"type":"string","maxLength":120},"locations":{"type":"array","minItems":1,"maxItems":200,"items":{"type":"object","required":["path","line"],"properties":{"path":{"type":"string","minLength":1},"line":{"type":"integer","minimum":1},"column":{"type":"integer","minimum":1},"text":{"type":"string"}}}}}}},
 {"name":"setup_workstreams","description":"Open LazyAI workstreams using the project's configured agent, exactly as the user's w key does. Use only when asked to set up workstreams. Validates the whole batch; asks confirmation for multiple new branches. Preserves current focus.","inputSchema":{"type":"object","required":["workstreams"],"properties":{"workstreams":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","required":["branch","nickname"],"properties":{"branch":{"type":"string","minLength":1},"nickname":{"type":"string","minLength":1},"description":{"type":"string"},"base":{"type":"string"}}}}}}},
 {"name":"read_file","description":"Read a workspace text file with line numbers and record it in LazyAI's file sidebar. Prefer this for reading code. Paths must remain inside the worktree. Use offset/limit to page large files.","inputSchema":{"type":"object","required":["path"],"properties":{"path":{"type":"string","minLength":1},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000}}}}
]`), &tools)
	return tools
}

func (b Bridge) callTool(name string, args json.RawMessage) (string, error) {
	var ev hooks.Event
	switch name {
	case "show_locations", "setup_workstreams":
		if err := json.Unmarshal(args, &ev); err != nil {
			return "", err
		}
		ev.SessionID, ev.Component, ev.CallID = "", "", ""
		if name == "show_locations" {
			ev.Type = "show"
			if len(ev.Locations) == 0 || len(ev.Locations) > 200 || len(ev.Title) > 120 {
				return "", fmt.Errorf("expected 1–200 locations and a title up to 120 bytes")
			}
		} else {
			ev.Type = "setup"
			if len(ev.Workstreams) == 0 || len(ev.Workstreams) > 10 {
				return "", fmt.Errorf("expected 1–10 workstreams")
			}
		}
		data, err := b.Send(ev)
		if err != nil {
			return "", err
		}
		if name == "show_locations" {
			return "Locations are shown in LazyAI. Press r on an entry to reference it.", nil
		}
		return string(data), nil
	case "read_file":
		var params struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", err
		}
		if params.Offset == 0 {
			params.Offset = 1
		}
		if params.Limit == 0 {
			params.Limit = 2000
		}
		if params.Offset < 1 || params.Limit < 1 || params.Limit > 2000 {
			return "", fmt.Errorf("offset must be positive and limit must be 1–2000")
		}
		path, err := b.readPath(params.Path)
		if err != nil {
			return "", err
		}
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return "", err
		}
		if !stat.Mode().IsRegular() {
			return "", fmt.Errorf("not a regular file")
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 4096), 1<<20)
		var text strings.Builder
		line := 0
		for sc.Scan() {
			line++
			if strings.ContainsRune(sc.Text(), 0) {
				return "", fmt.Errorf("not a text file")
			}
			if line < params.Offset {
				continue
			}
			if line >= params.Offset+params.Limit || text.Len()+len(sc.Text()) > 256<<10 {
				text.WriteString("[truncated; use offset/limit to continue]\n")
				break
			}
			fmt.Fprintf(&text, "%d: %s\n", line, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return "", err
		}
		if _, err := b.Send(hooks.Event{Type: "file.read", Path: path, Tool: "read_file"}); err != nil {
			return "", err
		}
		return text.String(), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (b Bridge) readPath(path string) (string, error) {
	if path == "" || b.Root == "" {
		return "", fmt.Errorf("workspace and path are required")
	}
	root, err := filepath.EvalSymlinks(b.Root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the worktree")
	}
	return path, nil
}
