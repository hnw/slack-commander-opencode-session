// Package main implements opencode-session, a stateless command that keeps one
// `opencode serve` session per Slack thread: it resolves the session of the
// thread, sends the prompt, and prints the answer once the agent finished.
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
  SLACK_CHANNEL_ID       Slack channel ID (required)
  SLACK_THREAD_TS        Slack thread timestamp (required)
  OPENCODE_URL           URL of the resident opencode serve (default: %s)
  OPENCODE_SERVER_USERNAME
                         OpenCode server basic-auth username
                         (default: opencode)
  OPENCODE_SERVER_PASSWORD
                         OpenCode server basic-auth password
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		_, _ = fmt.Fprintf(stderr, usage, DefaultOpenCodeURL)
		return 2
	}
	prompt := strings.Join(args[1:], " ")
	if strings.TrimSpace(prompt) == "" {
		_, _ = fmt.Fprintln(stderr, "opencode-session: prompt is empty")
		return 2
	}

	title, err := SessionTitleFromEnv(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opencode-session: %v\n", err)
		return 1
	}

	client := NewOpenCodeClient(OpenCodeURLFromEnv(getenv), WorkspaceDirectory, BasicAuthFromEnv(getenv))

	// The whole run shares one deadline; polling requests must not carry it.
	ctx, cancel := context.WithTimeout(context.Background(), RunTimeout)
	defer cancel()

	// The answer is buffered and written only once it is complete, so a failure
	// never leaves a partial answer on stdout.
	answer, err := NewRunner(client).Run(ctx, title, prompt)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opencode-session: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, answer)
	return 0
}
