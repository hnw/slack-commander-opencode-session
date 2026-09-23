package main

import (
	"errors"
	"testing"
)

func TestTitle(t *testing.T) {
	t.Parallel()
	if got, want := Title("T123", "123.456"), "slack:T123:123.456"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
}

func TestTitleFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		envmap  map[string]string
		want    string
		wantErr bool
	}{
		{
			name:   "ok",
			envmap: map[string]string{"SLACK_CHANNEL_ID": "T123", "SLACK_THREAD_TS": "123.456"},
			want:   "slack:T123:123.456",
		},
		{
			name:    "missing channel",
			envmap:  map[string]string{"SLACK_THREAD_TS": "123.456"},
			wantErr: true,
		},
		{
			name:    "missing thread",
			envmap:  map[string]string{"SLACK_CHANNEL_ID": "T123", "SLACK_THREAD_TS": "  "},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := TitleFromEnv(func(key string) string {
				return tt.envmap[key]
			})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("error is nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("title = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindSession(t *testing.T) {
	t.Parallel()
	sessions := []session{
		{ID: "s1", Title: "slack:T123:123.456"},
		{ID: "s2", Title: "slack:T123:123.456 (partial match)"},
		{ID: "s3", Title: "other"},
	}

	t.Run("exact match found", func(t *testing.T) {
		t.Parallel()
		id, err := findSession(sessions, "slack:T123:123.456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "s1" {
			t.Errorf("id = %q, want s1", id)
		}
	})

	t.Run("zero sessions", func(t *testing.T) {
		t.Parallel()
		id, err := findSession(nil, "slack:T999:1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Errorf("id = %q, want empty", id)
		}
	})

	t.Run("multiple sessions", func(t *testing.T) {
		t.Parallel()
		dup := []session{
			{ID: "a", Title: "slack:T1:1"},
			{ID: "b", Title: "slack:T1:1"},
		}
		_, err := findSession(dup, "slack:T1:1")
		if !errors.Is(err, ErrDuplicateSession) {
			t.Errorf("error = %v, want ErrDuplicateSession", err)
		}
	})

	t.Run("empty id", func(t *testing.T) {
		t.Parallel()
		_, err := findSession([]session{{ID: "", Title: "slack:T1:1"}}, "slack:T1:1")
		if !errors.Is(err, ErrEmptySessionID) {
			t.Errorf("error = %v, want ErrEmptySessionID", err)
		}
	})
}

func TestMessageTextParts(t *testing.T) {
	t.Parallel()
	msg := message{Parts: []textPart{
		{Type: "reasoning", Text: "thinking..."},
		{Type: "text", Text: "answer1"},
		{Type: "text", Text: "answer2"},
		{Type: "tool_use", Text: "tool"},
	}}
	got := msg.TextParts()
	if len(got) != 2 || got[0] != "answer1" || got[1] != "answer2" {
		t.Errorf("TextParts = %v, want [answer1 answer2]", got)
	}
}
