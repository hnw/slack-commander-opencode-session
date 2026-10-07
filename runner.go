package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PollInterval is the fixed interval between completion polls.
const PollInterval = 500 * time.Millisecond

// AnswerGrace is how long the answer projection may lag behind the stop point
// before the run is considered to have produced no answer.
const AnswerGrace = 10 * time.Second

// ErrUnsupportedInteraction reports an OpenCode interaction this wrapper cannot
// answer yet, such as a form or a permission request.
var ErrUnsupportedInteraction = errors.New("unsupported interaction")

// stopReason explains why a run stopped. It is deliberately small; new reasons
// can be added when the corresponding interaction is implemented.
type stopReason string

const (
	// stopCompleted means the session is no longer active.
	stopCompleted stopReason = "completed"
	// stopForm means the session waits for an answer to a form.
	stopForm stopReason = "form"
)

// Runner runs one prompt against an OpenCode session and returns the final
// assistant text. It keeps no state between runs.
type Runner struct {
	client       *OpenCodeClient
	pollInterval time.Duration
	answerGrace  time.Duration
}

// NewRunner builds a runner with the default polling timings.
func NewRunner(client *OpenCodeClient) *Runner {
	return &Runner{
		client:       client,
		pollInterval: PollInterval,
		answerGrace:  AnswerGrace,
	}
}

// Run sends the prompt to the session of the Slack thread and waits for the run
// to stop. The returned string is the text of the new assistant message.
func (r *Runner) Run(ctx context.Context, title, prompt string) (string, error) {
	sessionID, err := ResolveSession(ctx, r.client, title)
	if err != nil {
		return "", err
	}

	// The newest assistant message before the prompt is the baseline: only a
	// message newer than this one belongs to the current run.
	previous, _, err := r.client.LatestAssistantMessage(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("read the previous answer: %w", err)
	}

	accepted, err := r.client.SendPrompt(ctx, sessionID, prompt)
	if err != nil {
		return "", err
	}

	reason, err := r.waitForStop(ctx, sessionID, previous.ID)
	if err != nil {
		return "", err
	}
	if reason != stopCompleted {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedInteraction, reason)
	}

	answer, err := r.waitForAnswer(ctx, sessionID, previous.ID, accepted.ID)
	if err != nil {
		return "", err
	}
	texts := answer.TextParts()
	if len(texts) == 0 {
		return "", fmt.Errorf("no text content in the new assistant message (session=%s, message=%s)", sessionID, answer.ID)
	}
	return strings.Join(texts, "\n"), nil
}

// waitForStop polls until the session reaches a stop point. A session that is not
// active is ambiguous right after a prompt: the agent loop may not have claimed
// the session yet, and a very short run may already be over. So the session has
// to be seen running once, or the answer has to be projected already, before a
// not-active session counts as finished.
func (r *Runner) waitForStop(ctx context.Context, sessionID, baselineID string) (stopReason, error) {
	seenRunning := false
	for {
		// A session that waits for a form keeps reporting itself as running, so
		// the forms are checked on every poll, not only once the session stops.
		pending, err := r.client.HasPendingForm(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if pending {
			return stopForm, nil
		}

		active, err := r.client.ActiveSessions(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case active[sessionID].Running():
			seenRunning = true
		case seenRunning:
			return stopCompleted, nil
		default:
			// Not running yet, or already finished: only a new answer proves the
			// run is over. Otherwise the agent loop may still be starting up.
			answered, err := r.hasNewAnswer(ctx, sessionID, baselineID)
			if err != nil {
				return "", err
			}
			if answered {
				return stopCompleted, nil
			}
		}
		if err := r.sleep(ctx); err != nil {
			return "", fmt.Errorf("wait for the run to stop: %w", err)
		}
	}
}

// waitForAnswer waits for an assistant message that is not the baseline one. The
// message projection can lag behind the stop point, so the lookup is retried for
// a short while instead of trusting the first result.
func (r *Runner) waitForAnswer(ctx context.Context, sessionID, baselineID, inputID string) (AssistantMessage, error) {
	deadline := time.Now().Add(r.answerGrace)
	for {
		answer, found, err := r.client.LatestAssistantMessage(ctx, sessionID)
		if err != nil {
			return AssistantMessage{}, fmt.Errorf("read the new answer: %w", err)
		}
		if found && answer.ID != baselineID {
			return answer, nil
		}

		// No new answer: the run may be blocked on an interaction instead of finished.
		pending, err := r.client.HasPendingForm(ctx, sessionID)
		if err != nil {
			return AssistantMessage{}, err
		}
		if pending {
			return AssistantMessage{}, fmt.Errorf("%w: %s", ErrUnsupportedInteraction, stopForm)
		}
		if !time.Now().Before(deadline) {
			return AssistantMessage{}, fmt.Errorf("no new assistant message after the prompt (session=%s, input=%s)", sessionID, inputID)
		}
		if err := r.sleep(ctx); err != nil {
			return AssistantMessage{}, fmt.Errorf("wait for the new answer: %w", err)
		}
	}
}

// hasNewAnswer reports whether the session already has an assistant message that
// belongs to the current run.
func (r *Runner) hasNewAnswer(ctx context.Context, sessionID, baselineID string) (bool, error) {
	answer, found, err := r.client.LatestAssistantMessage(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("read the new answer: %w", err)
	}
	return found && answer.ID != baselineID, nil
}

func (r *Runner) sleep(ctx context.Context) error {
	timer := time.NewTimer(r.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
