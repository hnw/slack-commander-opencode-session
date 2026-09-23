package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

const usage = `opencode-session - HTTP client for a resident ` + "`opencode serve`" + `

Usage:
  opencode-session run PROMPT...

Environment:
  SLACK_CHANNEL_ID   Slack channel ID (required)
  SLACK_THREAD_TS    Slack thread timestamp (required)
  OPENCODE_URL       URL of the resident opencode serve (default: %s)
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		_, _ = fmt.Fprintf(stderr, usage, DefaultOpencodeURL)
		return 2
	}
	prompt := strings.Join(args[1:], " ")
	if strings.TrimSpace(prompt) == "" {
		_, _ = fmt.Fprintln(stderr, "opencode-session: prompt is empty")
		return 2
	}

	title, err := TitleFromEnv(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opencode-session: %v\n", err)
		return 1
	}
	baseURL := strings.TrimSpace(getenv("OPENCODE_URL"))
	if baseURL == "" {
		baseURL = DefaultOpencodeURL
	}

	client := NewOpenCodeClient(baseURL)
	ctx := context.Background()

	sessionID, err := resolveSession(ctx, client, title)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opencode-session: %v\n", err)
		return 1
	}

	msg, err := client.SendMessage(ctx, sessionID, prompt)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opencode-session: %v\n", err)
		return 1
	}

	texts := msg.TextParts()
	if len(texts) == 0 {
		_, _ = fmt.Fprintf(stderr, "opencode-session: no text part in the response message (session=%s)\n", sessionID)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, strings.Join(texts, "\n"))
	return 0
}
