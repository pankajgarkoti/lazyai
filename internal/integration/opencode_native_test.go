package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run with OPENCODE_TEST_BINARY=/path/to/opencode. This starts an isolated
// local server and queries its plugin catalog without making a model request.
type nativeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
}

func TestOpenCodeNativePluginLoads(t *testing.T) {
	bin := os.Getenv("OPENCODE_TEST_BINARY")
	if bin == "" {
		t.Skip("set OPENCODE_TEST_BINARY to exercise the installed V2 executable")
	}
	var mu sync.Mutex
	var events []nativeEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var event nativeEvent
		_ = json.NewDecoder(r.Body).Decode(&event)
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "integration")
	if _, err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	if err := EnsureV2PluginAPI(dir); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"OPENCODE_CONFIG_DIR="+dir,
		"LAZYAI_WORKTREE="+root,
		"LAZYAI_HOOK_URL="+server.URL,
		"LAZYAI_HOOK_TOKEN=test-token",
		"OPENCODE_SERVER_PASSWORD=test-password",
	)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	var catalog []byte
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/plugin", port), nil)
		req.SetBasicAuth("opencode", "test-password")
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			catalog, _ = io.ReadAll(res.Body)
			res.Body.Close()
			mu.Lock()
			loaded := hasEvent(events, "hello", "")
			mu.Unlock()
			if loaded && pluginState(catalog) == `lazyai: {"status":"active"}` || strings.Contains(string(catalog), `"status":"failed"`) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	mu.Lock()
	connected := hasEvent(events, "hello", "")
	observed := append([]nativeEvent(nil), events...)
	mu.Unlock()
	if ctx.Err() != nil || !connected || pluginState(catalog) != `lazyai: {"status":"active"}` {
		t.Fatalf("OpenCode 2 plugin did not connect (events=%v, err=%v, plugin=%s)", observed, ctx.Err(), pluginState(catalog))
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/api/session", port)
	call := func(url, body string) []byte {
		req, _ := http.NewRequest("POST", url, strings.NewReader(body))
		req.SetBasicAuth("opencode", "test-password")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %s", res.Status, data)
		}
		return data
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(call(endpoint, `{}`), &created); err != nil {
		t.Fatal(err)
	}
	call(endpoint+"/"+created.Data.ID+"/prompt", `{"text":"No model call","resume":false}`)
	mu.Lock()
	defer mu.Unlock()
	if !hasEvent(events, "session", created.Data.ID) {
		t.Fatalf("prompt hook did not identify the session: %v", events)
	}
}

func hasEvent(events []nativeEvent, kind, sessionID string) bool {
	for _, event := range events {
		if event.Type == kind && event.SessionID == sessionID {
			return true
		}
	}
	return false
}

func pluginState(catalog []byte) string {
	var response struct {
		Data []struct {
			ID    string          `json:"id"`
			State json.RawMessage `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(catalog, &response); err != nil {
		return string(catalog)
	}
	for _, item := range response.Data {
		if item.ID == "lazyai" || strings.Contains(string(item.State), "failed") {
			return fmt.Sprintf("%s: %s", item.ID, item.State)
		}
	}
	return "not listed"
}
