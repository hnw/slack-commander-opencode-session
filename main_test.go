package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunUsage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{}, func(string) string { return "" }, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunEmptyPrompt(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "  "}, func(string) string { return "" }, &stdout, &stderr)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestRunMissingEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"missing channel", map[string]string{"SLACK_THREAD_TS": "1.2"}},
		{"missing thread", map[string]string{"SLACK_CHANNEL_ID": "C1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run([]string{"run", "prompt"}, func(key string) string { return tt.env[key] }, &stdout, &stderr)
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
		})
	}
}

func TestRunHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null")) // session list returns null -> error
	}))
	defer srv.Close()

	env := map[string]string{
		"SLACK_CHANNEL_ID": "C1",
		"SLACK_THREAD_TS":  "1.2",
		"OPENCODE_URL":     srv.URL,
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "hi"}, func(key string) string { return env[key] }, &stdout, &stderr)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSuccess(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			t.Errorf("path = %s, want /session", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]session{})
	})
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(session{ID: "new1", Title: "slack:C1:1.2"})
	})
	mux.HandleFunc("POST /session/new1/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(message{Parts: []textPart{{Type: "text", Text: "answer"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	env := map[string]string{
		"SLACK_CHANNEL_ID": "C1",
		"SLACK_THREAD_TS":  "1.2",
		"OPENCODE_URL":     srv.URL + "/",
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "hi"}, func(key string) string { return env[key] }, &stdout, &stderr)
	if code != 0 {
		t.Errorf("code = %d, stderr: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "answer" {
		t.Errorf("stdout = %q, want answer", got)
	}
}
