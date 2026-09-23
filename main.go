package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hnw/compose-exec/compose"
	"github.com/hnw/slack-commander-opencode-session/session"
)

const usage = `opencode-session - run OpenCode in a Compose service with Slack-thread-bound sessions

Usage:
  opencode-session [--service SERVICE] run [OPENCODE RUN ARGS...]

Environment:
  SLACK_CHANNEL_ID   Slack channel ID (required)
  SLACK_THREAD_TS    Slack thread timestamp (required)
`

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "opencode-session: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	os.Exit(run())
}

func run() int {
	service := session.DefaultService
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--service" {
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, usage)
			fatalf("--service requires a value")
		}
		service = args[1]
		args = args[2:]
	}
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	userArgs := args[1:]

	if err := session.ValidateRunArgs(userArgs); err != nil {
		fatalf("%v", err)
	}

	title, err := session.TitleFromOSEnv()
	if err != nil {
		fatalf("%v", err)
	}

	if err := execute(os.Args[0], service, title, userArgs); err != nil {
		var exitErr *compose.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fatalf("%v", err)
	}
	return 0
}

func execute(name, service, title string, userArgs []string) error {
	ctx := context.Background()

	// The compose project is loaded once and its service is reused for both
	// session lookup and `opencode run`.
	project, err := compose.LoadProject(ctx, ".")
	if err != nil {
		return fmt.Errorf("load compose project: %w", err)
	}
	svc, err := project.Service(service)
	if err != nil {
		return fmt.Errorf("opencode service: %w", err)
	}

	lookup, err := lookupSession(ctx, svc, title)
	if err != nil {
		return err
	}

	return runOpenCode(name, svc, title, lookup, userArgs)
}

func lookupSession(ctx context.Context, svc *compose.Service, title string) (session.Lookup, error) {
	cmd := svc.CommandContext(ctx, "session", "list", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return session.Lookup{}, fmt.Errorf("session list: %w", err)
	}
	return session.LookupSessions(out, title)
}

func runOpenCode(
	name string,
	svc *compose.Service,
	title string,
	lookup session.Lookup,
	userArgs []string,
) error {
	args := session.RunArgs(title, lookup.SessionID, userArgs)
	cmd := svc.CommandContext(context.Background(), args...)
	cmd.TTY = isTTY()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *compose.ExitError
		if errors.As(err, &exitErr) {
			return exitErr
		}
		return fmt.Errorf("%s %s: %w", name, cmd.String(), err)
	}
	return nil
}
