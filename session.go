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

// ErrDuplicateSession is returned when more than one session has the target title.
var ErrDuplicateSession = errors.New("multiple sessions found for the same Slack thread")

// ErrEmptySessionID is returned when a session matching the title has no ID.
var ErrEmptySessionID = errors.New("session matching the Slack thread has an empty id")

// Title builds the OpenCode session title that maps a Slack thread to a session.
func Title(channelID, threadTS string) string {
	return fmt.Sprintf("slack:%s:%s", channelID, threadTS)
}

// TitleFromEnv builds the session title from the Slack environment variables.
func TitleFromEnv(getenv func(string) string) (string, error) {
	channelID := strings.TrimSpace(getenv(EnvChannelID))
	if channelID == "" {
		return "", fmt.Errorf("%s is not set", EnvChannelID)
	}
	threadTS := strings.TrimSpace(getenv(EnvThreadTS))
	if threadTS == "" {
		return "", fmt.Errorf("%s is not set", EnvThreadTS)
	}
	return Title(channelID, threadTS), nil
}

// findSession returns the session ID whose title matches exactly.
// An empty result with no error means no existing session (create a new one).
// Sessions whose title merely resembles the target are never used.
func findSession(sessions []session, title string) (string, error) {
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

// resolveSession finds an existing session for the title or creates a new one.
func resolveSession(ctx context.Context, client *OpenCodeClient, title string) (string, error) {
	sessions, err := client.ListSessions(ctx, title)
	if err != nil {
		return "", fmt.Errorf("search sessions: %w", err)
	}
	id, err := findSession(sessions, title)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	return client.CreateSession(ctx, title)
}
