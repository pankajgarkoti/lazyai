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

// WorkstreamSpec is one requested workstream in a setup request.
type WorkstreamSpec struct {
	Branch      string `json:"branch"`
	Nickname    string `json:"nickname"`
	Description string `json:"description,omitempty"`
	Base        string `json:"base,omitempty"`
}

// Reply is the consumer's answer to a request event (Type "setup"). Result
// is serialized as the HTTP response body; Err fails the tool call.
type Reply struct {
	Result any
	Err    error
}

// Event is the wire format. Type selects which fields are meaningful.
//
//	hello       -> Version
//	file.before -> Path, Tool        (edit/write about to run; snapshot now)
//	file.read   -> Path, Tool
//	file.write  -> Path, Tool
//	show        -> Title, Locations
//	tool.before -> Tool, CallID     (a tool call started: stream is working)
//	tool.after  -> Tool, CallID
//	idle        ->                  (session went idle; clears stale calls)
//	attention   ->                  (OpenCode is waiting on the user)
//	setup       -> Workstreams      (request: the consumer must send on Reply)
type Event struct {
	// Token identifies the stream (child) that sent the event; set by the
	// server, never trusted from the body.
	Token     string     `json:"-"`
	Type      string     `json:"type"`
	Version   int        `json:"version,omitempty"`
	SessionID string     `json:"sessionID,omitempty"`
	CallID    string     `json:"callID,omitempty"`
	Tool      string     `json:"tool,omitempty"`
	Path      string     `json:"path,omitempty"`
	Title     string     `json:"title,omitempty"`
	Locations []Location `json:"locations,omitempty"`

	Workstreams []WorkstreamSpec `json:"workstreams,omitempty"`
	// Reply is set by the server on request events. It has capacity 1, so a
	// consumer send never blocks even after the request timed out.
	Reply chan Reply `json:"-"`
}

// IsRequest reports whether the event expects a Reply.
func (e Event) IsRequest() bool { return e.Type == "setup" }

// DefaultRequestTimeout bounds how long a request event waits for the model.
// Setup may show a confirmation overlay, so this is generous.
const DefaultRequestTimeout = 120 * time.Second

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
	// RequestTimeout bounds request events (see Event.Reply).
	RequestTimeout time.Duration
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

		RequestTimeout: DefaultRequestTimeout,
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
	if ev.IsRequest() {
		ev.Reply = make(chan Reply, 1)
	}
	select {
	case s.Events <- ev:
	case <-time.After(2 * time.Second):
		http.Error(w, "lazyai busy", http.StatusServiceUnavailable)
		return
	}
	if ev.Reply == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	timeout := s.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	select {
	case reply := <-ev.Reply:
		if reply.Err != nil {
			http.Error(w, reply.Err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply.Result)
	case <-r.Context().Done():
	case <-time.After(timeout):
		http.Error(w, "lazyai did not answer the setup request in time", http.StatusGatewayTimeout)
	}
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
