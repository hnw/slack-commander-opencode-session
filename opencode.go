package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultOpenCodeURL is the default URL of the resident `opencode serve`.
const DefaultOpenCodeURL = "http://opencode:4096"

// WorkspaceDirectory is the OpenCode working directory this wrapper drives.
const WorkspaceDirectory = "/workspace"

// EnvServerUsername is the environment variable that carries the OpenCode server
// username.
const EnvServerUsername = "OPENCODE_SERVER_USERNAME"

// EnvServerPassword is the environment variable that carries the OpenCode server
// password.
const EnvServerPassword = "OPENCODE_SERVER_PASSWORD"

// defaultServerUsername is the username OpenCode assumes when only a password is
// configured.
const defaultServerUsername = "opencode"

// RequestTimeout bounds a single HTTP request to OpenCode. The whole run has its
// own, much longer deadline; this only keeps a stuck request from hanging.
const RequestTimeout = 30 * time.Second

// RunTimeout bounds one whole run, from session resolution to the final answer.
const RunTimeout = 15 * time.Minute

// errorBodyLimit is how many bytes of an error response are kept for stderr.
const errorBodyLimit = 4096

// BasicAuth are the HTTP basic credentials of a protected OpenCode server.
type BasicAuth struct {
	Username string
	Password string
}

// BasicAuthFromEnv reads the credentials from the variables OpenCode itself
// uses. OpenCode V2 protects its server with a password, so the credentials are
// forwarded whenever they are configured.
func BasicAuthFromEnv(getenv func(string) string) BasicAuth {
	password := getenv(EnvServerPassword)
	if password == "" {
		return BasicAuth{}
	}
	username := strings.TrimSpace(getenv(EnvServerUsername))
	if username == "" {
		username = defaultServerUsername
	}
	return BasicAuth{Username: username, Password: password}
}

// OpenCodeClient is a client for the OpenCode V2 HTTP API. It holds no state
// between calls: everything needed for a run is read from OpenCode itself.
type OpenCodeClient struct {
	baseURL   string
	directory string
	auth      BasicAuth
	http      *http.Client
}

// NewOpenCodeClient builds a client for the given OpenCode URL and working
// directory. A trailing slash on the URL is irrelevant.
func NewOpenCodeClient(baseURL, directory string, auth BasicAuth) *OpenCodeClient {
	return &OpenCodeClient{
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		directory: directory,
		auth:      auth,
		http:      &http.Client{},
	}
}

// SessionInfo is one entry of the OpenCode session list.
type SessionInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// sessionLocation is the location of a session inside the OpenCode workspace.
type sessionLocation struct {
	Directory string `json:"directory"`
}

// AssistantContent is one content entry of an assistant message. Only the text
// type is used; reasoning and tool entries are deliberately ignored.
type AssistantContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// FinishError is the finish reason of a message whose run OpenCode ended with an
// error instead of an answer.
const FinishError = "error"

// FinishStop is the finish reason of a message whose run ended normally.
const FinishStop = "stop"

// ErrRunFailed reports a run OpenCode itself ended with an error. The failure it
// explained, if any, is wrapped around it, so it has to be unwrapped rather than
// compared.
var ErrRunFailed = errors.New("OpenCode run failed")

// AssistantError is the failure OpenCode recorded on a message that ended in an
// error finish, as a rejected provider request does. A malformed response may
// omit it, so every field can be empty.
type AssistantError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// AssistantMessage is an assistant message projected from a session.
type AssistantMessage struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Content []AssistantContent `json:"content"`
	Finish  string             `json:"finish"`
	Error   *AssistantError    `json:"error"`
}

// Failed reports whether OpenCode ended the run of this message with an error.
// Such a message carries no answer to read, so its text must not be projected.
func (m AssistantMessage) Failed() bool { return m.Finish == FinishError }

// Failure returns the error the failed message explains, wrapped around
// ErrRunFailed. It is nil for a message that did not fail. A failed message
// without a usable message is still a run failure, only an unexplained one.
func (m AssistantMessage) Failure() error {
	if !m.Failed() {
		return nil
	}
	if m.Error == nil || strings.TrimSpace(m.Error.Message) == "" {
		return ErrRunFailed
	}
	return fmt.Errorf("%w: %s", ErrRunFailed, m.Error.Message)
}

// TextParts returns the text contents of the message in order.
func (m AssistantMessage) TextParts() []string {
	var texts []string
	for _, c := range m.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	return texts
}

// PromptInfo is the user input OpenCode accepted for a prompt.
type PromptInfo struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
}

// SessionActivity is the state of a session OpenCode is currently running.
type SessionActivity struct {
	Type string `json:"type"`
}

// Running reports whether OpenCode is still draining this session.
func (a SessionActivity) Running() bool { return a.Type == "running" }

// maxSessionPages bounds how many pages of the session list are read. It only
// exists to stop a server that keeps handing out cursors from looping forever.
const maxSessionPages = 100

// sessionsPage is one page of the V2 session list.
type sessionsPage struct {
	Data   []SessionInfo `json:"data"`
	Cursor struct {
		Next string `json:"next"`
	} `json:"cursor"`
}

// SearchSessions returns every session whose title matches the search term,
// following the pagination cursor. The search is fuzzy on the server side, so
// the caller must still verify exact title matches client side.
func (c *OpenCodeClient) SearchSessions(ctx context.Context, search string) ([]SessionInfo, error) {
	query := url.Values{"search": {search}, "directory": {c.directory}}

	var sessions []SessionInfo
	for page := 0; ; page++ {
		if page == maxSessionPages {
			return nil, fmt.Errorf("list sessions: more than %d pages", maxSessionPages)
		}

		var out sessionsPage
		if err := c.get(ctx, "/api/session", query, &out); err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		// A missing or null `data` is a malformed response, not an empty list:
		// treating it as empty would create a duplicate session.
		if out.Data == nil {
			return nil, errors.New("list sessions: server returned no data array")
		}
		sessions = append(sessions, out.Data...)

		// The cursor is opaque and only handed back to the server.
		next := out.Cursor.Next
		if next == "" {
			return sessions, nil
		}
		query.Set("cursor", next)
	}
}

// CreateSession creates a session with the given title in the working directory
// and returns its ID.
func (c *OpenCodeClient) CreateSession(ctx context.Context, title string) (string, error) {
	body := struct {
		Title    string          `json:"title"`
		Location sessionLocation `json:"location"`
	}{Title: title, Location: sessionLocation{Directory: c.directory}}

	var out struct {
		Data SessionInfo `json:"data"`
	}
	if err := c.post(ctx, "/api/session", body, &out); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("create session: server returned an empty session id (title=%q)", title)
	}
	return out.Data.ID, nil
}

// SendPrompt submits a prompt and starts the agent loop. The response is the
// accepted user input, not the answer, so the answer has to be read back from
// the session messages afterwards.
func (c *OpenCodeClient) SendPrompt(ctx context.Context, sessionID, text string) (PromptInfo, error) {
	body := struct {
		Text string `json:"text"`
	}{Text: text}

	var out struct {
		Data PromptInfo `json:"data"`
	}
	path := "/api/session/" + url.PathEscape(sessionID) + "/prompt"
	if err := c.post(ctx, path, body, &out); err != nil {
		return PromptInfo{}, fmt.Errorf("send prompt: %w", err)
	}
	if out.Data.ID == "" {
		return PromptInfo{}, fmt.Errorf("send prompt: server accepted no message id (session=%s)", sessionID)
	}
	return out.Data, nil
}

// ActiveSessions returns the sessions this OpenCode process is currently
// running, keyed by session ID. A session missing from the map is inactive.
func (c *OpenCodeClient) ActiveSessions(ctx context.Context) (map[string]SessionActivity, error) {
	var out struct {
		Data map[string]SessionActivity `json:"data"`
	}
	if err := c.get(ctx, "/api/session/active", nil, &out); err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	if out.Data == nil {
		return nil, errors.New("list active sessions: server returned no data object")
	}
	return out.Data, nil
}

// LatestAssistantMessage returns the newest assistant message of a session. The
// second result is false when the session has no assistant message yet.
func (c *OpenCodeClient) LatestAssistantMessage(ctx context.Context, sessionID string) (AssistantMessage, bool, error) {
	query := url.Values{"type": {"assistant"}, "order": {"desc"}, "limit": {"1"}}

	var out struct {
		Data []AssistantMessage `json:"data"`
	}
	path := "/api/session/" + url.PathEscape(sessionID) + "/message"
	if err := c.get(ctx, path, query, &out); err != nil {
		return AssistantMessage{}, false, fmt.Errorf("list assistant messages: %w", err)
	}
	if out.Data == nil {
		return AssistantMessage{}, false, errors.New("list assistant messages: server returned no data array")
	}
	if len(out.Data) == 0 {
		return AssistantMessage{}, false, nil
	}
	return out.Data[0], true, nil
}

// HasPendingForm reports whether the session is waiting for an answer to a form.
// OpenCode lists only the forms that are still pending, so no further request per
// form is needed.
func (c *OpenCodeClient) HasPendingForm(ctx context.Context, sessionID string) (bool, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	path := "/api/session/" + url.PathEscape(sessionID) + "/form"
	if err := c.get(ctx, path, nil, &out); err != nil {
		return false, fmt.Errorf("list session forms: %w", err)
	}
	if out.Data == nil {
		return false, errors.New("list session forms: server returned no data array")
	}
	return len(out.Data) > 0, nil
}

func (c *OpenCodeClient) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *OpenCodeClient) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *OpenCodeClient) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.auth.Password != "" {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("opencode request %s %s: %w", method, path, err)
	}
	// Body close errors are irrelevant after a successful read.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return fmt.Errorf("opencode request %s %s: unexpected status %d: %s", method, path, resp.StatusCode, snippet)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("opencode request %s %s: decode response: %w", method, path, err)
	}
	return nil
}
