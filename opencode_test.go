package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSearchSessionsUsesV2QueryAndWrapper(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", "slack:C1:1.2").
		addSession("ses_2", "unrelated")
	srv := fake.start(t)

	sessions, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		SearchSessions(context.Background(), "slack:C1:1.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_1" {
		t.Errorf("sessions = %+v, want ses_1", sessions)
	}
	if got := fake.directories(); len(got) != 1 || got[0] != WorkspaceDirectory {
		t.Errorf("directory query = %v, want [%s]", got, WorkspaceDirectory)
	}
}

func TestSearchSessionsRejectsNullData(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().fail(http.MethodGet, "/api/session", http.StatusOK, `{"data":null}`)
	srv := fake.start(t)

	// A null list must not be read as "no session exists": that would create a
	// duplicate session for the same Slack thread.
	if _, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		SearchSessions(context.Background(), "slack:C1:1.2"); err == nil {
		t.Fatal("error is nil, want a malformed response error")
	}
}

func TestCreateSessionSendsTitleAndLocation(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode()
	srv := fake.start(t)

	id, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		CreateSession(context.Background(), "slack:C1:1.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ses_new1" {
		t.Errorf("id = %q, want ses_new1", id)
	}
	if got := fake.titles(); len(got) != 1 || got[0] != "slack:C1:1.2" {
		t.Errorf("titles = %v, want [slack:C1:1.2]", got)
	}
}

func TestCreateSessionRejectsEmptyID(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().fail(http.MethodPost, "/api/session", http.StatusOK, `{"data":{"title":"t"}}`)
	srv := fake.start(t)

	_, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).CreateSession(context.Background(), "t")
	if err == nil || !strings.Contains(err.Error(), "empty session id") {
		t.Errorf("error = %v, want an empty session id error", err)
	}
}

func TestSendPromptPostsText(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2")
	srv := fake.start(t)

	accepted, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		SendPrompt(context.Background(), "ses_1", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted.ID != "msg_input1" || accepted.SessionID != "ses_1" {
		t.Errorf("accepted = %+v, want the admitted user input", accepted)
	}
	if log := fake.promptLog(); len(log) != 1 || log[0] != (promptRecord{SessionID: "ses_1", Text: "hello"}) {
		t.Errorf("prompt log = %+v", log)
	}
}

func TestSendPromptRejectsMissingMessageID(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().fail(http.MethodPost, "/api/session/ses_1/prompt", http.StatusOK, `{"data":{}}`)
	srv := fake.start(t)

	if _, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		SendPrompt(context.Background(), "ses_1", "hi"); err == nil {
		t.Fatal("error is nil, want a missing message id error")
	}
}

func TestActiveSessionsReadsRunningMap(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2").runPlan([]bool{true}, false)
	srv := fake.start(t)
	client := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{})

	active, err := client.ActiveSessions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active["ses_1"].Running() {
		t.Errorf("active = %+v, want ses_1 running", active)
	}
	active, err = client.ActiveSessions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active["ses_1"].Running() {
		t.Errorf("active = %+v, want ses_1 inactive", active)
	}
}

func TestActiveSessionsRejectsNullData(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().fail(http.MethodGet, "/api/session/active", http.StatusOK, `{"data":null}`)
	srv := fake.start(t)

	if _, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		ActiveSessions(context.Background()); err == nil {
		t.Fatal("error is nil, want a malformed response error")
	}
}

func TestLatestAssistantMessage(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2").addAnswer("msg_1",
		AssistantContent{Type: "text", Text: "old"})
	srv := fake.start(t)

	answer, found, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		LatestAssistantMessage(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || answer.ID != "msg_1" {
		t.Errorf("answer = %+v, found = %v, want msg_1", answer, found)
	}
}

func TestLatestAssistantMessageWithoutAny(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2")
	srv := fake.start(t)

	answer, found, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		LatestAssistantMessage(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || answer.ID != "" {
		t.Errorf("answer = %+v, found = %v, want no message", answer, found)
	}
}

func TestHasPendingForm(t *testing.T) {
	t.Parallel()
	// OpenCode lists pending forms only: a settled form is simply absent, so no
	// request per form is needed.
	tests := []struct {
		name  string
		setup func(*fakeOpencode)
		want  bool
	}{
		{"no form", func(*fakeOpencode) {}, false},
		{"pending form", func(f *fakeOpencode) { f.addForm() }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2")
			tt.setup(fake)
			srv := fake.start(t)
			client := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{})

			got, err := client.HasPendingForm(context.Background(), "ses_1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("HasPendingForm = %v, want %v", got, tt.want)
			}
			if n := fake.formDetailRequests(); n != 0 {
				t.Errorf("form detail requests = %d, want 0", n)
			}
			if n := fake.activeRequests(); n != 0 {
				t.Errorf("active requests = %d, want 0", n)
			}
		})
	}
}

func TestSearchSessionsFollowsCursor(t *testing.T) {
	t.Parallel()
	// The exact match only appears on the second page.
	fake := newFakeOpencode().
		addSession("ses_page1", "slack:C1:1.2 (partial)").
		addSession("ses_exact", "slack:C1:1.2").
		pageSize(1)
	srv := fake.start(t)

	sessions, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		SearchSessions(context.Background(), "slack:C1:1.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want both pages", sessions)
	}
	if sessions[1].ID != "ses_exact" {
		t.Errorf("sessions = %+v, want the second page result", sessions)
	}
}

func TestSearchSessionsStopsWithoutCursor(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2").pageSize(10)
	srv := fake.start(t)
	client := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{})

	for range 3 {
		sessions, err := client.SearchSessions(context.Background(), "slack:C1:1.2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sessions) != 1 {
			t.Errorf("sessions = %+v, want one session", sessions)
		}
	}
	if n := len(fake.directories()); n != 3 {
		t.Errorf("session list requests = %d, want one per call", n)
	}
}

func TestAssistantMessageTextParts(t *testing.T) {
	t.Parallel()
	message := AssistantMessage{Content: []AssistantContent{
		{Type: "reasoning", Text: "thinking..."},
		{Type: "tool", Text: "tool output"},
		{Type: "text", Text: "answer1"},
		{Type: "text", Text: "answer2"},
	}}
	got := message.TextParts()
	if len(got) != 2 || got[0] != "answer1" || got[1] != "answer2" {
		t.Errorf("TextParts = %v, want [answer1 answer2]", got)
	}
}

func TestLatestAssistantMessageReadsRunFailure(t *testing.T) {
	t.Parallel()
	// The payload a provider rejection produces, as observed on a real run.
	body := `{"data":[{"id":"msg_err","type":"assistant","content":[],"finish":"error","error":` +
		`{"type":"provider.auth","message":"Error from provider (Console): OpenCode's free tier can only be used from within OpenCode","status":403}}]}`
	fake := newFakeOpencode().
		addSession("ses_1", "slack:C1:1.2").
		fail(http.MethodGet, "/api/session/ses_1/message", http.StatusOK, body)
	srv := fake.start(t)

	answer, found, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
		LatestAssistantMessage(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || !answer.Failed() {
		t.Fatalf("answer = %+v, found = %v, want a failed message", answer, found)
	}
	if answer.Error == nil || answer.Error.Type != "provider.auth" || answer.Error.Status != 403 {
		t.Fatalf("error = %+v, want the recorded provider failure", answer.Error)
	}
	want := "OpenCode run failed: Error from provider (Console): OpenCode's free tier can only be used from within OpenCode"
	if got := answer.Failure(); got == nil || got.Error() != want {
		t.Errorf("Failure() = %v, want %q", got, want)
	}
	if !errors.Is(answer.Failure(), ErrRunFailed) {
		t.Error("Failure() does not wrap ErrRunFailed")
	}
}

func TestAssistantMessageFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message AssistantMessage
		want    string
	}{
		{
			name:    "no finish",
			message: AssistantMessage{},
		},
		{
			name:    "completed",
			message: AssistantMessage{Finish: FinishStop, Content: []AssistantContent{{Type: "text", Text: "hi"}}},
		},
		{
			name:    "error finish without a detail",
			message: AssistantMessage{Finish: FinishError},
			want:    "OpenCode run failed",
		},
		{
			name:    "error finish with a blank message",
			message: AssistantMessage{Finish: FinishError, Error: &AssistantError{Type: "provider.auth", Status: 403}},
			want:    "OpenCode run failed",
		},
		{
			name:    "provider rejection",
			message: AssistantMessage{Finish: FinishError, Error: providerRejection()},
			want: "OpenCode run failed: Error from provider (Console): " +
				"OpenCode's free tier can only be used from within OpenCode",
		},
		{
			// A failed run is reported as a failure even when OpenCode left some
			// content behind: that content is not an answer to the prompt.
			name: "error finish with leftover text",
			message: AssistantMessage{
				Finish:  FinishError,
				Content: []AssistantContent{{Type: "text", Text: "partial"}},
				Error:   providerRejection(),
			},
			want: "OpenCode run failed: Error from provider (Console): " +
				"OpenCode's free tier can only be used from within OpenCode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.message.Failure()
			if tt.want == "" {
				if got != nil {
					t.Fatalf("Failure() = %v, want no failure", got)
				}
				if tt.message.Failed() {
					t.Error("Failed() = true, want false")
				}
				return
			}
			if got == nil || got.Error() != tt.want {
				t.Fatalf("Failure() = %v, want %q", got, tt.want)
			}
			if !errors.Is(got, ErrRunFailed) {
				t.Error("Failure() does not wrap ErrRunFailed")
			}
		})
	}
}

func TestClientReportsHTTPStatusAndBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, `{"_tag":"SessionNotFoundError"}`},
		{"server error", http.StatusInternalServerError, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeOpencode().fail(http.MethodGet, "/api/session", tt.status, tt.body)
			srv := fake.start(t)

			_, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
				SearchSessions(context.Background(), "slack:C1:1.2")
			if err == nil {
				t.Fatal("error is nil, want a status error")
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tt.status)) {
				t.Errorf("error = %v, want the status code", err)
			}
			if !strings.Contains(err.Error(), tt.body) {
				t.Errorf("error = %v, want the response body", err)
			}
		})
	}
}

func TestClientRejectsMalformedResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"not json", "not json"},
		{"empty", ""},
		{"missing data", `{"cursor":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeOpencode().fail(http.MethodGet, "/api/session", http.StatusOK, tt.body)
			srv := fake.start(t)

			if _, err := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{}).
				SearchSessions(context.Background(), "slack:C1:1.2"); err == nil {
				t.Fatal("error is nil, want a decode error")
			}
		})
	}
}

func TestClientIgnoresTrailingSlash(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().addSession("ses_1", "slack:C1:1.2")
	srv := fake.start(t)
	client := NewOpenCodeClient(srv.URL+"/", WorkspaceDirectory, BasicAuth{})

	sessions, err := client.SearchSessions(context.Background(), "slack:C1:1.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_1" {
		t.Errorf("sessions = %+v, want ses_1", sessions)
	}
}

func TestClientTimesOutStuckRequest(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	srv := newBlockingServer(t, block)
	t.Cleanup(srv.Close)

	client := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := client.SearchSessions(ctx, "slack:C1:1.2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline error", err)
	}
}

// newBlockingServer answers every request only after the block channel closes.
func newBlockingServer(t *testing.T, block <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
}
