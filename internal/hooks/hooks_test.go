package hooks

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, s *Server, token, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/event", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func postJSON(t *testing.T, s *Server, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/event", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body) //nolint:errcheck
	return resp.StatusCode, b.String()
}

// TestSetupIsARequestThatWaitsForTheModelReply covers the request/response
// path used by setup_workstreams: the handler blocks until the consumer
// replies, returns the JSON result, and maps a declined request to 4xx.
func TestSetupIsARequestThatWaitsForTheModelReply(t *testing.T) {
	s, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RequestTimeout = 500 * time.Millisecond
	tok := s.Register()
	body := `{"type":"setup","workstreams":[{"branch":"feat/a","nickname":"A","description":"d","base":"main"}]}`

	// Consumer replies with a result.
	go func() {
		ev := <-s.Events
		if ev.Type != "setup" || len(ev.Workstreams) != 1 || ev.Workstreams[0].Branch != "feat/a" || ev.Workstreams[0].Base != "main" {
			ev.Reply <- Reply{Err: errString("bad event")}
			return
		}
		ev.Reply <- Reply{Result: map[string]any{"ok": true}}
	}()
	code, got := postJSON(t, s, tok, body)
	if code != http.StatusOK || !strings.Contains(got, `"ok":true`) {
		t.Fatalf("code=%d body=%q", code, got)
	}

	// Consumer declines: the tool call fails inside OpenCode.
	go func() {
		ev := <-s.Events
		ev.Reply <- Reply{Err: errString("declined by user")}
	}()
	code, got = postJSON(t, s, tok, body)
	if code != http.StatusUnprocessableEntity || !strings.Contains(got, "declined") {
		t.Fatalf("code=%d body=%q", code, got)
	}

	// Nobody replies: the request times out instead of hanging OpenCode, and
	// a late reply is dropped rather than blocking the model.
	code, got = postJSON(t, s, tok, body)
	if code != http.StatusGatewayTimeout {
		t.Fatalf("timeout code=%d body=%q", code, got)
	}
	ev := <-s.Events
	select {
	case ev.Reply <- Reply{Result: "late"}:
	case <-time.After(time.Second):
		t.Fatal("late reply must not block")
	}

	// Fire-and-forget events keep the old contract and carry call IDs.
	if code := post(t, s, tok, `{"type":"tool.before","tool":"read","callID":"c1"}`); code != http.StatusNoContent {
		t.Fatalf("plain event -> %d", code)
	}
	if ev := <-s.Events; ev.CallID != "c1" || ev.Reply != nil {
		t.Fatalf("plain event %+v", ev)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestPerStreamTokensStampEvents(t *testing.T) {
	s, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, b := s.Register(), s.Register()
	if a == b || a == "" {
		t.Fatal("tokens must be unique")
	}
	if code := post(t, s, "nope", `{"type":"hello"}`); code != http.StatusUnauthorized {
		t.Fatalf("bad token -> %d", code)
	}
	if code := post(t, s, b, `{"type":"hello"}`); code != http.StatusNoContent {
		t.Fatalf("good token -> %d", code)
	}
	select {
	case ev := <-s.Events:
		if ev.Token != b || ev.Type != "hello" {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	s.Unregister(b)
	if code := post(t, s, b, `{"type":"hello"}`); code != http.StatusUnauthorized {
		t.Fatalf("unregistered token -> %d", code)
	}
	for _, e := range s.EnvFor(a) {
		if e == "LAZYAI_HOOK_TOKEN="+a {
			return
		}
	}
	t.Fatal("EnvFor missing token")
}
