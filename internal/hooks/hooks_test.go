package hooks

import (
	"bytes"
	"net/http"
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
