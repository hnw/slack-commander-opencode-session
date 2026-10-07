package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOverride replaces the response of one endpoint, to simulate failures.
type fakeOverride struct {
	status int
	body   string
}

// promptRecord is one prompt the fake OpenCode received.
type promptRecord struct {
	SessionID string
	Text      string
}

// fakeOpencode is a minimal `opencode serve` V2 for tests. It reproduces the
// server side behaviour the wrapper relies on: fuzzy title search, sessions that
// run only after a prompt was accepted, and a message projection that can lag
// behind the stop point.
type fakeOpencode struct {
	mu             sync.Mutex
	directory      string
	sessions       []SessionInfo
	nextID         int
	prompts        []promptRecord
	answers        map[string][]AssistantMessage
	promptAnswer   *AssistantMessage
	lagAfterPrompt int
	forms          []string
	formDetailGets int
	activePlan     []bool
	activeRest     bool
	activeGets     int
	pageSizeValue  int
	projectionLag  int
	overrides      map[string]fakeOverride
	directoriesSet []string
}

func newFakeOpencode() *fakeOpencode {
	return &fakeOpencode{
		directory: WorkspaceDirectory,
		answers:   map[string][]AssistantMessage{},
		overrides: map[string]fakeOverride{},
	}
}

// addSession registers a session that already exists on the server.
func (f *fakeOpencode) addSession(id, title string) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, SessionInfo{ID: id, Title: title})
	return f
}

// addAnswer registers an assistant message the session has already produced.
func (f *fakeOpencode) addAnswer(id string, content ...AssistantContent) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	const sessionID = "ses_1"
	f.answers[sessionID] = append(f.answers[sessionID], AssistantMessage{ID: id, Type: "assistant", Content: content})
	return f
}

// answerAfterPrompt is the assistant message a prompt produces: the fake appends
// it to the prompted session once the prompt was accepted.
func (f *fakeOpencode) answerAfterPrompt(content ...AssistantContent) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptAnswer = &AssistantMessage{ID: "msg_new", Type: "assistant", Content: content}
	return f
}

// runPlan makes the active endpoint report the given sequence of running states,
// and then the given resting state.
func (f *fakeOpencode) runPlan(states []bool, rest bool) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activePlan = append([]bool(nil), states...)
	f.activeRest = rest
	return f
}

// lagProjection makes the next n message lookups after a prompt report the
// previous newest message, as if the projection had not caught up yet.
func (f *fakeOpencode) lagProjection(n int) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lagAfterPrompt = n
	return f
}

// addForm registers a form waiting for an answer. OpenCode lists only the forms
// that are still pending, so a settled form is simply absent from the list.
func (f *fakeOpencode) addForm() *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forms = append(f.forms, "frm_1")
	return f
}

// formDetailRequests counts the per-form detail lookups the client made.
func (f *fakeOpencode) formDetailRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.formDetailGets
}

// pageSize makes the session list answer one page at a time, with an opaque
// cursor, like the real server does.
func (f *fakeOpencode) pageSize(size int) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSizeValue = size
	return f
}

// fail makes an endpoint answer with the given status and body.
func (f *fakeOpencode) fail(method, path string, status int, body string) *fakeOpencode {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[method+" "+path] = fakeOverride{status: status, body: body}
	return f
}

// directories returns the `directory` values the session list was asked for.
func (f *fakeOpencode) directories() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.directoriesSet...)
}

func (f *fakeOpencode) promptLog() []promptRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]promptRecord(nil), f.prompts...)
}

// activeRequests counts the active endpoint lookups.
func (f *fakeOpencode) activeRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeGets
}

func (f *fakeOpencode) titles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var titles []string
	for _, s := range f.sessions {
		titles = append(titles, s.Title)
	}
	return titles
}

// start serves the fake endpoints until the test ends.
func (f *fakeOpencode) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	f.handleSessionList(t, mux)
	f.handleSessionCreate(t, mux)
	f.handlePrompt(t, mux)
	f.handleActive(t, mux)
	f.handleMessages(t, mux)
	f.handleForms(t, mux)
	f.handleFormDetail(t, mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// handleSessionList serves the session search: the real server filters titles
// fuzzily and hands out an opaque cursor for the next page.
func (f *fakeOpencode) handleSessionList(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		offset, err := decodeCursor(cursor)
		if err != nil {
			t.Errorf("cursor is not the opaque token the fake issued: %q: %v", cursor, err)
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.directoriesSet = append(f.directoriesSet, r.URL.Query().Get("directory"))
		data, next := f.sessionPage(matchingSessions(f.sessions, r.URL.Query().Get("search")), offset)
		f.mu.Unlock()
		f.respond(t, w, http.MethodGet, "/api/session", map[string]any{
			"data":   data,
			"cursor": map[string]any{"previous": nil, "next": next},
		})
	})
}

// sessionPage cuts one page out of the matches and returns the cursor to
// continue from, if any page is left. The cursor is opaque: it only says where
// to continue. It must be called with the fake lock held.
func (f *fakeOpencode) sessionPage(matches []SessionInfo, offset int) ([]SessionInfo, any) {
	size := f.pageSizeValue
	if size <= 0 || offset+size >= len(matches) {
		return matches[offset:], nil
	}
	end := offset + size
	return matches[offset:end], base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
}

// matchingSessions keeps the sessions whose title matches the search term.
func matchingSessions(sessions []SessionInfo, search string) []SessionInfo {
	matches := []SessionInfo{}
	for _, s := range sessions {
		if search == "" || strings.Contains(s.Title, search) {
			matches = append(matches, s)
		}
	}
	return matches
}

// decodeCursor reads back the offset the fake encoded into a cursor.
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(decoded))
}

// handleSessionCreate serves the session creation.
func (f *fakeOpencode) handleSessionCreate(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title    string          `json:"title"`
			Location sessionLocation `json:"location"`
		}
		if err := decodeRequest(t, r, &body); err != nil {
			return
		}
		if body.Location.Directory != f.directory {
			t.Errorf("create session location = %q, want %q", body.Location.Directory, f.directory)
		}
		f.mu.Lock()
		f.nextID++
		created := SessionInfo{ID: "ses_new" + strconv.Itoa(f.nextID), Title: body.Title}
		f.sessions = append(f.sessions, created)
		f.mu.Unlock()
		f.respond(t, w, http.MethodPost, "/api/session", map[string]any{"data": created})
	})
}

// handlePrompt serves the prompt submission and appends the answer the prompt
// produces to the prompted session.
func (f *fakeOpencode) handlePrompt(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("POST /api/session/{sessionID}/prompt", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := decodeRequest(t, r, &body); err != nil {
			return
		}
		sessionID := r.PathValue("sessionID")
		f.mu.Lock()
		f.prompts = append(f.prompts, promptRecord{SessionID: sessionID, Text: body.Text})
		count := len(f.prompts)
		if answer := f.promptAnswer; answer != nil {
			f.answers[sessionID] = append(f.answers[sessionID], *answer)
		}
		f.projectionLag = f.lagAfterPrompt
		f.mu.Unlock()
		f.respond(t, w, http.MethodPost, "/api/session/"+sessionID+"/prompt", map[string]any{
			"data": map[string]any{
				"id":        "msg_input" + strconv.Itoa(count),
				"sessionID": sessionID,
				"time":      map[string]any{"created": 1},
				"type":      "user",
				"payload":   map[string]any{"text": body.Text},
				"delivery":  "steer",
			},
		})
	})
}

// handleActive serves the sessions the server is currently running, following
// the plan the test set up.
func (f *fakeOpencode) handleActive(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /api/session/active", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.activeGets++
		running := f.activeRest
		if len(f.activePlan) > 0 {
			running = f.activePlan[0]
			f.activePlan = f.activePlan[1:]
		}
		data := map[string]any{}
		for _, s := range f.sessions {
			if running {
				data[s.ID] = map[string]any{"type": "running"}
			}
		}
		f.mu.Unlock()
		f.respond(t, w, http.MethodGet, "/api/session/active", map[string]any{"data": data})
	})
}

// handleMessages serves the newest assistant message of a session.
func (f *fakeOpencode) handleMessages(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /api/session/{sessionID}/message", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("type") != "assistant" || query.Get("order") != "desc" || query.Get("limit") != "1" {
			t.Errorf("message query = %v, want type=assistant order=desc limit=1", query)
		}
		sessionID := r.PathValue("sessionID")
		f.mu.Lock()
		messages := f.answers[sessionID]
		// A lagging projection still reports the previous newest message.
		if f.projectionLag > 0 && len(messages) > 0 {
			f.projectionLag--
			messages = messages[:len(messages)-1]
		}
		data := []AssistantMessage{}
		if n := len(messages); n > 0 {
			data = append(data, messages[n-1])
		}
		f.mu.Unlock()
		f.respond(t, w, http.MethodGet, "/api/session/"+sessionID+"/message", map[string]any{
			"data":   data,
			"cursor": map[string]any{"previous": nil, "next": nil},
		})
	})
}

// handleForms serves the forms still waiting for an answer.
func (f *fakeOpencode) handleForms(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /api/session/{sessionID}/form", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.PathValue("sessionID")
		f.mu.Lock()
		forms := append([]string(nil), f.forms...)
		f.mu.Unlock()
		data := []map[string]any{}
		for _, id := range forms {
			data = append(data, map[string]any{"id": id, "sessionID": sessionID, "title": "question", "fields": []any{}})
		}
		f.respond(t, w, http.MethodGet, "/api/session/"+sessionID+"/form", map[string]any{"data": data})
	})
}

// handleFormDetail counts the per-form detail lookups. The route is deliberately
// not implemented: detecting a pending form must not cost one request per form.
func (f *fakeOpencode) handleFormDetail(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("/api/session/{sessionID}/form/{formID}", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.formDetailGets++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
	})
}

// respond writes the endpoint response, or the configured failure.
func (f *fakeOpencode) respond(t *testing.T, w http.ResponseWriter, method, path string, body any) {
	t.Helper()
	f.mu.Lock()
	override, ok := f.overrides[method+" "+path]
	f.mu.Unlock()
	if ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(override.status)
		if _, err := w.Write([]byte(override.body)); err != nil {
			t.Errorf("write override body: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func decodeRequest(t *testing.T, r *http.Request, v any) error {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decode request body: %v", err)
		return err
	}
	return nil
}

// testRunner builds a runner against the fake server with fast polling.
func testRunner(t *testing.T, fake *fakeOpencode) *Runner {
	t.Helper()
	srv := fake.start(t)
	runner := NewRunner(NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}))
	runner.pollInterval = time.Millisecond
	runner.answerGrace = 200 * time.Millisecond
	return runner
}
