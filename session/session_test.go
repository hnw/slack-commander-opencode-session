package session

import (
	"errors"
	"testing"
)

func TestTitle(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		threadTS  string
		want      string
	}{
		{
			name:      "normal",
			channelID: "C01234567",
			threadTS:  "1780000123.456789",
			want:      "slack:C01234567:1780000123.456789",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Title(tt.channelID, tt.threadTS)
			if got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTitleFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "both set",
			env: map[string]string{
				EnvChannelID: "C01234567",
				EnvThreadTS:  "1780000123.456789",
			},
			want: "slack:C01234567:1780000123.456789",
		},
		{
			name:    "channel missing",
			env:     map[string]string{EnvThreadTS: "1780000123.456789"},
			wantErr: EnvChannelID + " is not set",
		},
		{
			name:    "channel empty",
			env:     map[string]string{EnvChannelID: "", EnvThreadTS: "1.2"},
			wantErr: EnvChannelID + " is not set",
		},
		{
			name:    "thread missing",
			env:     map[string]string{EnvChannelID: "C01234567"},
			wantErr: EnvThreadTS + " is not set",
		},
		{
			name:    "thread empty",
			env:     map[string]string{EnvChannelID: "C01234567", EnvThreadTS: ""},
			wantErr: EnvThreadTS + " is not set",
		},
		{
			name:    "both missing",
			env:     map[string]string{},
			wantErr: EnvChannelID + " is not set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TitleFromEnv(func(key string) string { return tt.env[key] })
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("TitleFromEnv() error = nil, want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Errorf("TitleFromEnv() error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("TitleFromEnv() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("TitleFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSessions(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    []Session
		wantErr bool
	}{
		{
			name: "valid array",
			data: `[{"id":"ses_1","title":"slack:C1:1.2"},{"id":"ses_2","title":"other"}]`,
			want: []Session{
				{ID: "ses_1", Title: "slack:C1:1.2"},
				{ID: "ses_2", Title: "other"},
			},
		},
		{
			name: "unknown fields ignored",
			data: `[{"id":"ses_1","title":"t","updated":123,"directory":"/x"}]`,
			want: []Session{{ID: "ses_1", Title: "t"}},
		},
		{
			name: "empty array",
			data: `[]`,
			want: []Session{},
		},
		{
			name:    "not json",
			data:    `not json`,
			wantErr: true,
		},
		{
			name:    "object instead of array",
			data:    `{"id":"ses_1"}`,
			wantErr: true,
		},
		{
			name:    "null is an error",
			data:    `null`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSessions([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSessions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSessions() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseSessions()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFindSession(t *testing.T) {
	tests := []struct {
		name       string
		sessions   []Session
		title      string
		wantID     string
		wantErr    bool
		wantDupErr bool
	}{
		{
			name:     "no match means new session",
			sessions: []Session{{ID: "ses_1", Title: "other"}},
			title:    "slack:C1:1.2",
			wantID:   "",
		},
		{
			name:     "empty list means new session",
			sessions: nil,
			title:    "slack:C1:1.2",
			wantID:   "",
		},
		{
			name: "exact match returns id",
			sessions: []Session{
				{ID: "ses_1", Title: "other"},
				{ID: "ses_2", Title: "slack:C1:1.2"},
				{ID: "ses_3", Title: "slack:C1:1.20"},
			},
			title:  "slack:C1:1.2",
			wantID: "ses_2",
		},
		{
			name: "prefix match does not count",
			sessions: []Session{
				{ID: "ses_1", Title: "slack:C1:1.2x"},
			},
			title:  "slack:C1:1.2",
			wantID: "",
		},
		{
			name: "duplicates are an error",
			sessions: []Session{
				{ID: "ses_1", Title: "slack:C1:1.2"},
				{ID: "ses_2", Title: "slack:C1:1.2"},
			},
			title:      "slack:C1:1.2",
			wantErr:    true,
			wantDupErr: true,
		},
		{
			name: "three duplicates are an error",
			sessions: []Session{
				{ID: "ses_1", Title: "slack:C1:1.2"},
				{ID: "ses_2", Title: "slack:C1:1.2"},
				{ID: "ses_3", Title: "slack:C1:1.2"},
			},
			title:      "slack:C1:1.2",
			wantErr:    true,
			wantDupErr: true,
		},
		{
			name: "empty id on match is an error",
			sessions: []Session{
				{ID: "", Title: "slack:C1:1.2"},
			},
			title:   "slack:C1:1.2",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindSession(tt.sessions, tt.title)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FindSession() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.wantDupErr && !errors.Is(err, ErrDuplicateSession) {
					t.Errorf("FindSession() error = %v, want ErrDuplicateSession", err)
				}
				return
			}
			if got != tt.wantID {
				t.Errorf("FindSession() = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestLookupSessions(t *testing.T) {
	tests := []struct {
		name     string
		listJSON string
		title    string
		wantNew  bool
		wantID   string
		wantErr  bool
	}{
		{
			name:     "zero sessions means new",
			listJSON: `[]`,
			title:    "slack:C1:1.2",
			wantNew:  true,
		},
		{
			name:     "one match reuses session",
			listJSON: `[{"id":"ses_9","title":"slack:C1:1.2"}]`,
			title:    "slack:C1:1.2",
			wantNew:  false,
			wantID:   "ses_9",
		},
		{
			name:     "duplicate titles are an error",
			listJSON: `[{"id":"ses_1","title":"slack:C1:1.2"},{"id":"ses_2","title":"slack:C1:1.2"}]`,
			title:    "slack:C1:1.2",
			wantErr:  true,
		},
		{
			name:     "empty id on match is an error",
			listJSON: `[{"id":"","title":"slack:C1:1.2"}]`,
			title:    "slack:C1:1.2",
			wantErr:  true,
		},
		{
			name:     "top-level null is an error",
			listJSON: `null`,
			title:    "slack:C1:1.2",
			wantErr:  true,
		},
		{
			name:     "broken json is an error",
			listJSON: `{`,
			title:    "slack:C1:1.2",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LookupSessions([]byte(tt.listJSON), tt.title)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LookupSessions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.IsNew() != tt.wantNew {
				t.Errorf("LookupSessions().IsNew() = %v, want %v", got.IsNew(), tt.wantNew)
			}
			if got.SessionID != tt.wantID {
				t.Errorf("LookupSessions().SessionID = %q, want %q", got.SessionID, tt.wantID)
			}
		})
	}
}

func TestValidateRunArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "plain message", args: []string{"--model", "foo/bar", "review this"}},
		{name: "rejects --session", args: []string{"--session", "ses_x"}, wantErr: true},
		{name: "rejects --title", args: []string{"--title", "my title"}, wantErr: true},
		{name: "rejects --session=value", args: []string{"--session=ses_x"}, wantErr: true},
		{name: "rejects --title=value", args: []string{"--title=foo"}, wantErr: true},
		{name: "rejects -s alias", args: []string{"-s", "ses_x"}, wantErr: true},
		{name: "rejects -s=value", args: []string{"-s=ses_x"}, wantErr: true},
		{name: "rejects --session alone", args: []string{"--session"}, wantErr: true},
		{name: "rejects --title alone", args: []string{"--title"}, wantErr: true},
		{name: "rejects anywhere", args: []string{"--model", "m", "--title", "x"}, wantErr: true},
		{name: "empty args", args: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRunArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRunArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunArgs(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		sessionID string
		userArgs  []string
		want      []string
	}{
		{
			name:      "new session adds --title",
			title:     "slack:C1:1.2",
			sessionID: "",
			userArgs:  []string{"--model", "foo/bar", "review this"},
			want:      []string{"run", "--title", "slack:C1:1.2", "--model", "foo/bar", "review this"},
		},
		{
			name:      "existing session adds --session",
			title:     "slack:C1:1.2",
			sessionID: "ses_9",
			userArgs:  []string{"review this"},
			want:      []string{"run", "--session", "ses_9", "review this"},
		},
		{
			name:      "new session with no user args",
			title:     "slack:C1:1.2",
			sessionID: "",
			userArgs:  nil,
			want:      []string{"run", "--title", "slack:C1:1.2"},
		},
		{
			name:      "user args are preserved as-is",
			title:     "slack:C1:1.2",
			sessionID: "ses_9",
			userArgs:  []string{"-m", "a/b", "--fork", "hello world"},
			want:      []string{"run", "--session", "ses_9", "-m", "a/b", "--fork", "hello world"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RunArgs(tt.title, tt.sessionID, tt.userArgs)
			if len(got) != len(tt.want) {
				t.Fatalf("RunArgs() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("RunArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
