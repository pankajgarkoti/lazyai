// Package hooks receives structured events from the LazyAI OpenCode plugin
// running inside the embedded OpenCode process. Transport is a loopback HTTP
// listener whose URL and bearer token are handed to the child via env.
package hooks

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Location is a single quickfix-style entry from show_locations.
type Location struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

// Event is the wire format. Type selects which fields are meaningful.
//
//	hello       -> Version
//	file.before -> Path, Tool        (edit/write about to run; snapshot now)
//	file.read   -> Path, Tool
//	file.write  -> Path, Tool
//	show        -> Title, Locations
//	tool.before -> Tool             (any tool call started: stream is busy)
//	tool.after  -> Tool
//	idle        ->                  (session went idle)
//	attention   ->                  (OpenCode is waiting on the user)
type Event struct {
	// Token identifies the stream (child) that sent the event; set by the
	// server, never trusted from the body.
	Token     string     `json:"-"`
	Type      string     `json:"type"`
	Version   int        `json:"version,omitempty"`
	SessionID string     `json:"sessionID,omitempty"`
	Tool      string     `json:"tool,omitempty"`
	Path      string     `json:"path,omitempty"`
	Title     string     `json:"title,omitempty"`
	Locations []Location `json:"locations,omitempty"`
}

// Server accepts plugin events from any number of children, each identified
// by its own bearer token.
type Server struct {
	URL    string
	Events chan Event

	mu     sync.Mutex
	tokens map[string]bool

	ln  net.Listener
	srv *http.Server
	// Validate, when set, is consulted for "show" events so the tool call can
	// fail inside OpenCode when LazyAI rejects the payload.
	Validate func(Event) error
}

// Listen binds a loopback port and starts serving.
func Listen() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		URL:    "http://" + ln.Addr().String(),
		Events: make(chan Event, 256),
		tokens: map[string]bool{},
		ln:     ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/event", s.handle)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	ok := s.tokens[token]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if ev.Type == "" {
		http.Error(w, "missing type", http.StatusBadRequest)
		return
	}
	ev.Token = token
	if ev.Type == "show" && s.Validate != nil {
		if err := s.Validate(ev); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}
	select {
	case s.Events <- ev:
	case <-time.After(2 * time.Second):
		http.Error(w, "lazyai busy", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Close stops the listener.
func (s *Server) Close() error {
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// Register mints a token for a new child.
func (s *Server) Register() string {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		panic(err)
	}
	t := hex.EncodeToString(tok)
	s.mu.Lock()
	s.tokens[t] = true
	s.mu.Unlock()
	return t
}

// Unregister revokes a token once its child is gone.
func (s *Server) Unregister(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// EnvFor returns the environment entries a child needs to reach this server.
func (s *Server) EnvFor(token string) []string {
	return []string{
		fmt.Sprintf("LAZYAI_HOOK_URL=%s", s.URL),
		fmt.Sprintf("LAZYAI_HOOK_TOKEN=%s", token),
	}
}
