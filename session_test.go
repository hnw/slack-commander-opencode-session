package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSessionTitle(t *testing.T) {
	t.Parallel()
	if got, want := SessionTitle("C01234567", "1780000123.456789"), "slack:C01234567:1780000123.456789"; got != want {
		t.Errorf("SessionTitle = %q, want %q", got, want)
	}
}

func TestSessionTitleFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "ok",
			env:  map[string]string{EnvChannelID: "C01234567", EnvThreadTS: "1780000123.456789"},
			want: "slack:C01234567:1780000123.456789",
		},
		{
			name:    "missing channel",
			env:     map[string]string{EnvThreadTS: "1780000123.456789"},
			wantErr: EnvChannelID,
		},
		{
			name:    "blank channel",
			env:     map[string]string{EnvChannelID: "  ", EnvThreadTS: "1780000123.456789"},
			wantErr: EnvChannelID,
		},
		{
			name:    "missing thread",
			env:     map[string]string{EnvChannelID: "C01234567"},
			wantErr: EnvThreadTS,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := SessionTitleFromEnv(func(key string) string { return tt.env[key] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want a %s error", err, tt.wantErr)
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

func TestOpenCodeURLFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"default", "", DefaultOpenCodeURL},
		{"blank", "   ", DefaultOpenCodeURL},
		{"explicit", "http://opencode:4096", "http://opencode:4096"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{EnvOpenCodeURL: tt.url}
			if got := OpenCodeURLFromEnv(func(key string) string { return env[key] }); got != tt.want {
				t.Errorf("OpenCodeURLFromEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchSessionByTitle(t *testing.T) {
	t.Parallel()
	sessions := []SessionInfo{
		{ID: "ses_1", Title: "slack:C1:1.2"},
		{ID: "ses_2", Title: "slack:C1:1.2 (partial match)"},
		{ID: "ses_3", Title: "slack:C1:1"},
		{ID: "ses_4", Title: "other"},
	}

	t.Run("exact match", func(t *testing.T) {
		t.Parallel()
		id, err := matchSessionByTitle(sessions, "slack:C1:1.2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "ses_1" {
			t.Errorf("id = %q, want ses_1", id)
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		id, err := matchSessionByTitle(sessions, "slack:C9:9.9")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "" {
			t.Errorf("id = %q, want empty", id)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()
		duplicates := []SessionInfo{{ID: "a", Title: "slack:C1:1"}, {ID: "b", Title: "slack:C1:1"}}
		if _, err := matchSessionByTitle(duplicates, "slack:C1:1"); !errors.Is(err, ErrDuplicateSession) {
			t.Errorf("error = %v, want ErrDuplicateSession", err)
		}
	})

	t.Run("empty id", func(t *testing.T) {
		t.Parallel()
		if _, err := matchSessionByTitle([]SessionInfo{{Title: "slack:C1:1"}}, "slack:C1:1"); !errors.Is(err, ErrEmptySessionID) {
			t.Errorf("error = %v, want ErrEmptySessionID", err)
		}
	})
}

func TestResolveSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"no session creates one", resolveCreatesWhenAbsent},
		{"existing session is reused", resolveReusesExisting},
		{"partial matches are ignored", resolveIgnoresPartials},
		{"duplicate sessions are ambiguous", resolveRejectsDuplicates},
		{"exact match on a later page is reused", resolveReusesPagedExact},
		{"duplicate sessions across pages are ambiguous", resolveRejectsPagedDuplicates},
		{"search failure does not create a session", resolveFailsWhenSearchFails},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}

// resolveOnFake starts the fake server and resolves the test title against it.
func resolveOnFake(t *testing.T, fake *fakeOpencode) (string, error) {
	t.Helper()
	srv := fake.start(t)
	client := NewOpenCodeClient(srv.URL, WorkspaceDirectory, BasicAuth{})
	return ResolveSession(context.Background(), client, testTitle)
}

func resolveCreatesWhenAbsent(t *testing.T) {
	fake := newFakeOpencode()

	id, err := resolveOnFake(t, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ses_new1" {
		t.Errorf("id = %q, want a newly created session", id)
	}
	if got := fake.titles(); len(got) != 1 || got[0] != testTitle {
		t.Errorf("titles = %v, want [%s]", got, testTitle)
	}
}

func resolveReusesExisting(t *testing.T) {
	fake := newFakeOpencode().addSession("ses_existing", testTitle)

	id, err := resolveOnFake(t, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ses_existing" {
		t.Errorf("id = %q, want ses_existing", id)
	}
	if got := fake.titles(); len(got) != 1 {
		t.Errorf("titles = %v, want no new session", got)
	}
}

func resolveIgnoresPartials(t *testing.T) {
	fake := newFakeOpencode().
		addSession("ses_a", testTitle+" extra").
		addSession("ses_b", strings.TrimSuffix(testTitle, "2"))

	id, err := resolveOnFake(t, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ses_new1" {
		t.Errorf("id = %q, want a newly created session", id)
	}
}

func resolveRejectsDuplicates(t *testing.T) {
	fake := newFakeOpencode().
		addSession("ses_a", testTitle).
		addSession("ses_b", testTitle)

	if _, err := resolveOnFake(t, fake); !errors.Is(err, ErrDuplicateSession) {
		t.Fatalf("error = %v, want ErrDuplicateSession", err)
	}
	if got := fake.titles(); len(got) != 2 {
		t.Errorf("titles = %v, want no session created", got)
	}
}

func resolveReusesPagedExact(t *testing.T) {
	fake := newFakeOpencode().
		addSession("ses_partial", testTitle+" (partial match)").
		addSession("ses_exact", testTitle).
		pageSize(1)

	id, err := resolveOnFake(t, fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "ses_exact" {
		t.Errorf("id = %q, want ses_exact", id)
	}
	if got := fake.titles(); len(got) != 2 {
		t.Errorf("titles = %v, want no session created", got)
	}
}

func resolveRejectsPagedDuplicates(t *testing.T) {
	fake := newFakeOpencode().
		addSession("ses_a", testTitle).
		addSession("ses_b", testTitle).
		pageSize(1)

	if _, err := resolveOnFake(t, fake); !errors.Is(err, ErrDuplicateSession) {
		t.Fatalf("error = %v, want ErrDuplicateSession", err)
	}
	if got := fake.titles(); len(got) != 2 {
		t.Errorf("titles = %v, want no session created", got)
	}
}

func resolveFailsWhenSearchFails(t *testing.T) {
	fake := newFakeOpencode().fail("GET", "/api/session", 500, "boom")

	if _, err := resolveOnFake(t, fake); err == nil {
		t.Fatal("error is nil, want a search error")
	}
	if got := fake.titles(); len(got) != 0 {
		t.Errorf("titles = %v, want no session created", got)
	}
}
