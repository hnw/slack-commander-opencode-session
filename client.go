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

// DefaultOpencodeURL is the default URL of the resident `opencode serve`.
const DefaultOpencodeURL = "http://opencode:4096"

// DefaultDirectory is the OpenCode working directory used in API requests.
const DefaultDirectory = "/workspace"

// MetadataTimeout is the timeout for session list/search and creation.
const MetadataTimeout = 30 * time.Second

// MessageTimeout is the timeout for prompt submission; prompts to an AI agent
// may take a long time, so it is deliberately generous.
const MessageTimeout = 15 * time.Minute

// OpenCodeClient is a minimal client for the `opencode serve` HTTP API.
// It covers only the operations needed by this wrapper: listing sessions,
// creating a session, and sending a prompt.
type OpenCodeClient struct {
	baseURL    string
	directory  string
	httpClient *http.Client
}

// NewOpenCodeClient builds a client for the given opencode URL.
func NewOpenCodeClient(baseURL string) *OpenCodeClient {
	return &OpenCodeClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		directory:  DefaultDirectory,
		httpClient: &http.Client{},
	}
}

// session is one entry of the OpenCode session list.
type session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// textPart is a text part of an OpenCode message.
type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// message is the response of a prompt submission.
type message struct {
	Parts []textPart `json:"parts"`
}

// get issues a GET request to the given path with query parameters.
func (c *OpenCodeClient) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// post issues a POST request with a JSON body.
func (c *OpenCodeClient) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *OpenCodeClient) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
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
	// OpenCode requires the working directory in the encoded header form,
	// e.g. x-opencode-directory: %2Fworkspace
	req.Header.Set("x-opencode-directory", url.PathEscape(c.directory))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode request %s %s: %w", method, path, err)
	}
	// Body close errors are irrelevant after a successful read.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("opencode request %s %s: unexpected status %d: %s", method, path, resp.StatusCode, data)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("opencode request %s %s: decode response: %w", method, path, err)
	}
	return nil
}

// ListSessions lists sessions, optionally narrowing candidates by title search.
// The caller must still verify exact title matches client side.
func (c *OpenCodeClient) ListSessions(ctx context.Context, search string) ([]session, error) {
	ctx, cancel := context.WithTimeout(ctx, MetadataTimeout)
	defer cancel()

	var query url.Values
	if search != "" {
		query = url.Values{"search": []string{search}}
	}
	var sessions []session
	if err := c.get(ctx, "/session", query, &sessions); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	// A JSON `null` decodes into a nil slice; treat it as a malformed response
	// rather than an empty list so that a new session is not created.
	if sessions == nil {
		return nil, errors.New("list sessions: server returned null")
	}
	return sessions, nil
}

// CreateSession creates a new session with the given title and returns its ID.
func (c *OpenCodeClient) CreateSession(ctx context.Context, title string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, MetadataTimeout)
	defer cancel()

	var created session
	body := struct {
		Title string `json:"title"`
	}{Title: title}
	if err := c.post(ctx, "/session", body, &created); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("create session: server returned an empty session id (title=%q)", title)
	}
	return created.ID, nil
}

// SendMessage sends a prompt to the session and returns the response message.
func (c *OpenCodeClient) SendMessage(ctx context.Context, sessionID, prompt string) (message, error) {
	ctx, cancel := context.WithTimeout(ctx, MessageTimeout)
	defer cancel()

	body := struct {
		Parts []textPart `json:"parts"`
	}{Parts: []textPart{{Type: "text", Text: prompt}}}

	var msg message
	if err := c.post(ctx, "/session/"+url.PathEscape(sessionID)+"/message", body, &msg); err != nil {
		return message{}, fmt.Errorf("send message: %w", err)
	}
	return msg, nil
}

// TextParts extracts the text of the message parts usable as the answer.
func (m message) TextParts() []string {
	var texts []string
	for _, p := range m.Parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return texts
}
