package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOpencode implements a minimal `opencode serve` for tests.
type fakeOpencode struct {
	mu        sync.Mutex
	sessions  []session
	nextID    int
	promptLog []string
}

func newFakeOpencode(initial ...session) *fakeOpencode {
	return &fakeOpencode{sessions: append([]session(nil), initial...)}
}

func (f *fakeOpencode) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") == "" {
			t.Errorf("search parameter is missing")
		}
		writeJSON(t, w, f.list())
	})
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
		}
		decodeBody(t, r, &body)
		f.mu.Lock()
		f.nextID++
		created := session{ID: "new-" + string(rune('0'+f.nextID)), Title: body.Title}
		f.sessions = append(f.sessions, created)
		f.mu.Unlock()
		writeJSON(t, w, created)
	})
	mux.HandleFunc("POST /session/{id}/message", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parts []textPart `json:"parts"`
		}
		decodeBody(t, r, &body)
		id := r.PathValue("id")
		f.mu.Lock()
		f.promptLog = append(f.promptLog, id+":"+body.Parts[0].Text)
		f.mu.Unlock()
		writeJSON(t, w, message{Parts: []textPart{{Type: "text", Text: "resp-" + id}}})
	})
	return mux
}

func (f *fakeOpencode) list() []session {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]session, len(f.sessions))
	copy(out, f.sessions)
	return out
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func decodeBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if r.Header.Get("x-opencode-directory") == "" {
		t.Errorf("x-opencode-directory header is missing")
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decode request: %v", err)
	}
}

func TestListSessions(t *testing.T) {
	t.Parallel()
	f := newFakeOpencode(session{ID: "s1", Title: "x"})
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	c := NewOpenCodeClient(srv.URL)
	sessions, err := c.ListSessions(context.Background(), "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "s1" {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestListSessionsHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := NewOpenCodeClient(srv.URL).ListSessions(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want HTTP 500 error", err)
	}
}

func TestListSessionsInvalidJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			t.Fatalf("unexpected path %s during session search", r.URL.Path)
		}
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := NewOpenCodeClient(srv.URL).ListSessions(context.Background(), "x")
	if err == nil {
		t.Fatal("error is nil, want decode error")
	}
}

func TestListSessionsNullJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	_, err := NewOpenCodeClient(srv.URL).ListSessions(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "null") {
		t.Errorf("error = %v, want null response error", err)
	}
}

func TestClientTrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	f := newFakeOpencode(session{ID: "s1", Title: "x"})
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	c := NewOpenCodeClient(srv.URL + "/")
	sessions, err := c.ListSessions(context.Background(), "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("sessions = %+v", sessions)
	}
}

func TestCreateSession(t *testing.T) {
	t.Parallel()
	f := newFakeOpencode()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	id, err := NewOpenCodeClient(srv.URL).CreateSession(context.Background(), "slack:T1:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("id is empty")
	}
	if got := f.list()[0].Title; got != "slack:T1:1" {
		t.Errorf("created title = %q", got)
	}
}

func TestCreateSessionEmptyID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, session{Title: "t"})
	}))
	defer srv.Close()

	_, err := NewOpenCodeClient(srv.URL).CreateSession(context.Background(), "t")
	if err == nil || !strings.Contains(err.Error(), "empty session id") {
		t.Errorf("error = %v, want empty session id error", err)
	}
}

func TestSendMessage(t *testing.T) {
	t.Parallel()
	f := newFakeOpencode()
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	msg, err := NewOpenCodeClient(srv.URL).SendMessage(context.Background(), "s1", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.TextParts()) != 1 || msg.TextParts()[0] != "resp-s1" {
		t.Errorf("message = %+v", msg)
	}
	if len(f.promptLog) != 1 || f.promptLog[0] != "s1:hi" {
		t.Errorf("promptLog = %v", f.promptLog)
	}
}

// endToEnd covers resolveSession behaviors against a fake server.
func TestResolveSession(t *testing.T) {
	title := "slack:T123:123.456"

	t.Run("zero sessions creates one", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpencode()
		srv := httptest.NewServer(f.handler(t))
		defer srv.Close()

		id, err := resolveSession(context.Background(), NewOpenCodeClient(srv.URL), title)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id == "" {
			t.Fatal("id is empty")
		}
		if len(f.list()) != 1 || f.list()[0].Title != title {
			t.Errorf("sessions = %+v", f.list())
		}
	})

	t.Run("one session reused without creation", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpencode(session{ID: "existing", Title: title})
		srv := httptest.NewServer(f.handler(t))
		defer srv.Close()

		id, err := resolveSession(context.Background(), NewOpenCodeClient(srv.URL), title)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "existing" {
			t.Errorf("id = %q, want existing", id)
		}
		if len(f.list()) != 1 {
			t.Errorf("a new session was created: %+v", f.list())
		}
	})

	t.Run("multiple sessions errors and does not send prompt", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpencode(
			session{ID: "a", Title: title},
			session{ID: "b", Title: title},
		)
		srv := httptest.NewServer(f.handler(t))
		defer srv.Close()

		_, err := resolveSession(context.Background(), NewOpenCodeClient(srv.URL), title)
		if !errors.Is(err, ErrDuplicateSession) {
			t.Errorf("error = %v, want ErrDuplicateSession", err)
		}
		if len(f.promptLog) != 0 {
			t.Errorf("prompt was sent: %v", f.promptLog)
		}
	})

	t.Run("partial matches are ignored", func(t *testing.T) {
		t.Parallel()
		f := newFakeOpencode(
			session{ID: "partial1", Title: title + " extra"},
			session{ID: "partial2", Title: title[:len(title)-1]},
		)
		srv := httptest.NewServer(f.handler(t))
		defer srv.Close()

		id, err := resolveSession(context.Background(), NewOpenCodeClient(srv.URL), title)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id, "new-") {
			t.Errorf("id = %q, want a newly created session", id)
		}
	})
}
