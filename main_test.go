package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// runCLI calls the command line entry point with the given environment.
func runCLI(t *testing.T, args []string, env map[string]string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, func(key string) string { return env[key] }, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"unknown command", []string{"send", "hi"}},
		{"no prompt", []string{"run"}},
		{"blank prompt", []string{"run", "   "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCLI(t, tt.args, map[string]string{})
			if code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if stderr == "" {
				t.Error("stderr is empty, want an explanation")
			}
		})
	}
}

func TestRunMissingSlackEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"missing channel", map[string]string{EnvThreadTS: "1780000123.456789"}},
		{"missing thread", map[string]string{EnvChannelID: "C01234567"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCLI(t, []string{"run", "hi"}, tt.env)
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "opencode-session:") {
				t.Errorf("stderr = %q, want a prefixed error", stderr)
			}
		})
	}
}

func TestRunJoinsPromptArguments(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", "slack:C1:1.2").
		answerAfterPrompt(AssistantContent{Type: "text", Text: "ok"})
	srv := fake.start(t)

	code, stdout, stderr := runCLI(t, []string{"run", "今日の", "todo", "を教えて"}, map[string]string{
		EnvChannelID:   "C1",
		EnvThreadTS:    "1.2",
		EnvOpenCodeURL: srv.URL,
	})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	if log := fake.promptLog(); len(log) != 1 || log[0].Text != "今日の todo を教えて" {
		t.Errorf("prompt log = %+v, want the joined prompt", log)
	}
	if got := strings.TrimSpace(stdout); got != "ok" {
		t.Errorf("stdout = %q, want ok", got)
	}
}

func TestRunCreatesSessionAndPrintsOnlyText(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		answerAfterPrompt(
			AssistantContent{Type: "reasoning", Text: "hidden reasoning"},
			AssistantContent{Type: "tool", Text: "hidden tool"},
			AssistantContent{Type: "text", Text: "visible answer"},
		)
	srv := fake.start(t)

	code, stdout, stderr := runCLI(t, []string{"run", "hi"}, map[string]string{
		EnvChannelID:   "C1",
		EnvThreadTS:    "1.2",
		EnvOpenCodeURL: srv.URL + "/",
	})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "visible answer" {
		t.Errorf("stdout = %q, want only the text content", got)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if got := fake.titles(); len(got) != 1 || got[0] != "slack:C1:1.2" {
		t.Errorf("titles = %v, want the Slack thread title", got)
	}
}

func TestRunKeepsStdoutEmptyOnFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		arrange func(*fakeOpencode)
		wantErr string
	}{
		{
			name: "duplicate sessions",
			arrange: func(f *fakeOpencode) {
				f.addSession("ses_a", "slack:C1:1.2").addSession("ses_b", "slack:C1:1.2")
			},
			wantErr: "multiple sessions",
		},
		{
			name:    "malformed session list",
			arrange: func(f *fakeOpencode) { f.fail(http.MethodGet, "/api/session", http.StatusOK, "not json") },
			wantErr: "list sessions",
		},
		{
			name:    "opencode error",
			arrange: func(f *fakeOpencode) { f.fail(http.MethodGet, "/api/session", http.StatusInternalServerError, "boom") },
			wantErr: "500",
		},
		{
			name: "pending form",
			arrange: func(f *fakeOpencode) {
				f.addSession("ses_1", "slack:C1:1.2").addForm()
			},
			wantErr: "unsupported interaction",
		},
		{
			name: "provider rejection",
			arrange: func(f *fakeOpencode) {
				f.addSession("ses_1", "slack:C1:1.2").
					failAfterPrompt(FinishError, providerRejection())
			},
			wantErr: "free tier can only be used from within OpenCode",
		},
		{
			name: "run failure without a detail",
			arrange: func(f *fakeOpencode) {
				f.addSession("ses_1", "slack:C1:1.2").
					failAfterPrompt(FinishError, nil)
			},
			wantErr: "OpenCode run failed",
		},
		{
			name: "answer without text",
			arrange: func(f *fakeOpencode) {
				f.addSession("ses_1", "slack:C1:1.2").
					answerAfterPrompt(AssistantContent{Type: "reasoning", Text: "hidden"})
			},
			wantErr: "no text content",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeOpencode()
			tt.arrange(fake)
			srv := fake.start(t)

			code, stdout, stderr := runCLI(t, []string{"run", "hi"}, map[string]string{
				EnvChannelID:   "C1",
				EnvThreadTS:    "1.2",
				EnvOpenCodeURL: srv.URL,
			})
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr = %q, want it to mention %q", stderr, tt.wantErr)
			}
		})
	}
}
