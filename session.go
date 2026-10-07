package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// EnvChannelID is the environment variable that carries the Slack channel ID.
const EnvChannelID = "SLACK_CHANNEL_ID"

// EnvThreadTS is the environment variable that carries the Slack thread timestamp.
const EnvThreadTS = "SLACK_THREAD_TS"

// EnvOpenCodeURL is the environment variable that carries the OpenCode URL.
const EnvOpenCodeURL = "OPENCODE_URL"

// ErrDuplicateSession is returned when more than one session has the target title.
var ErrDuplicateSession = errors.New("multiple sessions found for the same Slack thread")

// ErrEmptySessionID is returned when a session matching the title has no ID.
var ErrEmptySessionID = errors.New("session matching the Slack thread has an empty id")

// SessionTitle builds the OpenCode session title that maps a Slack thread to a
// session, e.g. slack:C01234567:1780000123.456789.
func SessionTitle(channelID, threadTS string) string {
	return fmt.Sprintf("slack:%s:%s", channelID, threadTS)
}

// SessionTitleFromEnv builds the session title from the Slack environment variables.
func SessionTitleFromEnv(getenv func(string) string) (string, error) {
	channelID := strings.TrimSpace(getenv(EnvChannelID))
	if channelID == "" {
		return "", fmt.Errorf("%s is not set", EnvChannelID)
	}
	threadTS := strings.TrimSpace(getenv(EnvThreadTS))
	if threadTS == "" {
		return "", fmt.Errorf("%s is not set", EnvThreadTS)
	}
	return SessionTitle(channelID, threadTS), nil
}

// OpenCodeURLFromEnv reads the OpenCode URL, falling back to the default.
func OpenCodeURLFromEnv(getenv func(string) string) string {
	url := strings.TrimSpace(getenv(EnvOpenCodeURL))
	if url == "" {
		return DefaultOpenCodeURL
	}
	return url
}

// matchSessionByTitle returns the ID of the session whose title matches exactly.
// The server search is fuzzy, so only exact titles count. An empty ID with no
// error means no existing session, in which case one has to be created.
func matchSessionByTitle(sessions []SessionInfo, title string) (string, error) {
	found := ""
	count := 0
	for _, s := range sessions {
		if s.Title != title {
			continue
		}
		count++
		found = s.ID
	}
	switch count {
	case 0:
		return "", nil
	case 1:
		if found == "" {
			return "", fmt.Errorf("%w (title=%q)", ErrEmptySessionID, title)
		}
		return found, nil
	default:
		return "", fmt.Errorf("%w (title=%q, count=%d)", ErrDuplicateSession, title, count)
	}
}

// ResolveSession finds the session of the Slack thread or creates a new one.
// Nothing is remembered afterwards: the next run resolves the session again from
// the OpenCode session titles.
func ResolveSession(ctx context.Context, client *OpenCodeClient, title string) (string, error) {
	sessions, err := client.SearchSessions(ctx, title)
	if err != nil {
		return "", fmt.Errorf("search sessions: %w", err)
	}
	id, err := matchSessionByTitle(sessions, title)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	return client.CreateSession(ctx, title)
}
