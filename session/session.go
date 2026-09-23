// Package session implements the pure logic of opencode-session:
// session title generation from Slack environment variables, parsing of
// `opencode session list --format json` output, and assembly of the
// `opencode run` command arguments.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvChannelID is the environment variable that carries the Slack channel ID.
const EnvChannelID = "SLACK_CHANNEL_ID"

// EnvThreadTS is the environment variable that carries the Slack thread timestamp.
const EnvThreadTS = "SLACK_THREAD_TS"

// DefaultService is the default Compose service name for OpenCode.
const DefaultService = "opencode"

// ManagedFlags are the flags owned by the wrapper; users must not pass them.
var ManagedFlags = []string{"--session", "--title"}

// FlagAliases maps shorthands to their long form for flags the wrapper owns.
var FlagAliases = map[string]string{"-s": "--session"}

// Title builds the OpenCode session title that maps a Slack thread to a session.
func Title(channelID, threadTS string) string {
	return fmt.Sprintf("slack:%s:%s", channelID, threadTS)
}

// TitleFromEnv builds the session title from the Slack environment variables.
func TitleFromEnv(getenv func(string) string) (string, error) {
	channelID := getenv(EnvChannelID)
	if strings.TrimSpace(channelID) == "" {
		return "", fmt.Errorf("%s is not set", EnvChannelID)
	}
	threadTS := getenv(EnvThreadTS)
	if strings.TrimSpace(threadTS) == "" {
		return "", fmt.Errorf("%s is not set", EnvThreadTS)
	}
	return Title(channelID, threadTS), nil
}

// Session represents one entry of `opencode session list --format json`.
type Session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ParseSessions parses the JSON output of `opencode session list --format json`.
// A valid session list is a JSON array; anything else (including null) is an
// error so that unexpected data does not fall back to creating a new session.
func ParseSessions(data []byte) ([]Session, error) {
	var sessions []Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, fmt.Errorf("parse session list JSON: %w", err)
	}
	if sessions == nil {
		return nil, fmt.Errorf("session list JSON is not an array: %s", data)
	}
	return sessions, nil
}

// ErrDuplicateSession is returned when more than one session has the target title.
var ErrDuplicateSession = errors.New("multiple sessions found for the same Slack thread")

// ErrEmptySessionID is returned when a session matching the title has no ID.
var ErrEmptySessionID = errors.New("session matching the Slack thread has an empty id")

// FindSession returns the session ID whose title matches exactly.
// An empty result with no error means no existing session (create a new one).
func FindSession(sessions []Session, title string) (string, error) {
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

// ValidateRunArgs rejects user-supplied occurrences of the wrapper-managed
// flags, including shorthand aliases and `--flag=value` forms.
func ValidateRunArgs(args []string) error {
	banned := map[string]string{}
	for _, f := range ManagedFlags {
		banned[f] = f
	}
	for alias, long := range FlagAliases {
		banned[alias] = long
	}
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		if long, ok := banned[name]; ok {
			if name != long {
				return fmt.Errorf("user must not pass %s (alias of %s)", name, long)
			}
			return fmt.Errorf("user must not pass %s", long)
		}
	}
	return nil
}

// RunArgs assembles the arguments after `opencode run`.
// For a new session it appends `--title <title>`, for an existing one
// `--session <id>`.
func RunArgs(title, sessionID string, userArgs []string) []string {
	args := []string{"run"}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	} else {
		args = append(args, "--title", title)
	}
	return append(args, userArgs...)
}

// Lookup describes the outcome of a session lookup.
type Lookup struct {
	// SessionID is non-empty when an existing session should be reused.
	SessionID string
}

// NewLookupResult reports whether a new session should be created.
func (l Lookup) IsNew() bool { return l.SessionID == "" }

// LookupSessions runs the pure part of session lookup: parse the session list
// JSON and find the session with the exact title.
func LookupSessions(listJSON []byte, title string) (Lookup, error) {
	sessions, err := ParseSessions(listJSON)
	if err != nil {
		return Lookup{}, err
	}
	id, err := FindSession(sessions, title)
	if err != nil {
		return Lookup{}, err
	}
	return Lookup{SessionID: id}, nil
}

// TitleFromOSEnv builds the session title from the process environment.
func TitleFromOSEnv() (string, error) {
	return TitleFromEnv(os.Getenv)
}
