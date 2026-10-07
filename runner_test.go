package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testTitle = "slack:C1:1.2"

func TestRunnerWaitsForRunningSessionThenCompletes(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		answerAfterPrompt(
			AssistantContent{Type: "reasoning", Text: "thinking"},
			AssistantContent{Type: "tool", Text: "read main.go"},
			AssistantContent{Type: "text", Text: "the answer"},
		).
		runPlan([]bool{true, true}, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "the answer" {
		t.Errorf("answer = %q, want %q", got, "the answer")
	}
	if log := fake.promptLog(); len(log) != 1 || log[0] != (promptRecord{SessionID: "ses_1", Text: "hi"}) {
		t.Errorf("prompt log = %+v", log)
	}
}

func TestRunnerCompletesWithoutObservingRunning(t *testing.T) {
	t.Parallel()
	// A run that finishes before the first poll is never seen as running.
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		answerAfterPrompt(AssistantContent{Type: "text", Text: "quick"}).
		runPlan(nil, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "quick" {
		t.Errorf("answer = %q, want quick", got)
	}
}

func TestRunnerWaitsForLaggingMessageProjection(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		addAnswer("msg_old", AssistantContent{Type: "text", Text: "previous answer"}).
		answerAfterPrompt(AssistantContent{Type: "text", Text: "fresh answer"}).
		lagProjection(3).
		runPlan([]bool{true}, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fresh answer" {
		t.Errorf("answer = %q, want the new message", got)
	}
}

func TestRunnerDoesNotReturnPreviousAnswer(t *testing.T) {
	t.Parallel()
	// The session answers nothing this time: the previous message must not be
	// passed off as the answer of this run.
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		addAnswer("msg_old", AssistantContent{Type: "text", Text: "previous answer"}).
		runPlan([]bool{true}, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err == nil {
		t.Fatalf("answer = %q, want an error", got)
	}
	if !strings.Contains(err.Error(), "no new assistant message") {
		t.Errorf("error = %v, want a no new message error", err)
	}
	if strings.Contains(got, "previous answer") {
		t.Errorf("answer = %q, want no previous answer", got)
	}
}

func TestRunnerClassifiesAssistantMessage(t *testing.T) {
	t.Parallel()
	// A new assistant message has four possible outcomes: an answer, a run OpenCode
	// ended with an error, an error finish that carries no explanation, and a run
	// that simply finished without any text.
	tests := []struct {
		name     string
		arrange  func(*fakeOpencode)
		want     string
		wantErr  string
		exactErr bool
		wantFail bool
	}{
		{
			name: "answered",
			arrange: func(f *fakeOpencode) {
				f.answerAfterPrompt(AssistantContent{Type: "text", Text: "the answer"})
			},
			want: "the answer",
		},
		{
			name: "provider rejection",
			arrange: func(f *fakeOpencode) {
				f.failAfterPrompt(FinishError, providerRejection())
			},
			want:     "",
			wantErr:  "OpenCode run failed: Error from provider (Console): OpenCode's free tier can only be used from within OpenCode",
			exactErr: true,
			wantFail: true,
		},
		{
			name: "error finish without a detail",
			arrange: func(f *fakeOpencode) {
				f.failAfterPrompt(FinishError, nil)
			},
			wantErr:  "OpenCode run failed",
			exactErr: true,
			wantFail: true,
		},
		{
			name: "error finish with a blank message",
			arrange: func(f *fakeOpencode) {
				f.failAfterPrompt(FinishError, &AssistantError{Type: "provider.auth", Status: 403})
			},
			wantErr:  "OpenCode run failed",
			exactErr: true,
			wantFail: true,
		},
		{
			name: "finished without text",
			arrange: func(f *fakeOpencode) {
				f.answerAfterPrompt(AssistantContent{Type: "reasoning", Text: "only thinking"})
			},
			wantErr: "no text content",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeOpencode().addSession("ses_1", testTitle)
			tt.arrange(fake)
			// The session ran and then stopped, as any finished run does.
			fake.runPlan([]bool{true}, false)
			runner := testRunner(t, fake)

			got, err := runner.Run(context.Background(), testTitle, "hi")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("answer = %q, want %q", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("answer = %q, want an error", got)
			}
			if tt.exactErr {
				if err.Error() != tt.wantErr {
					t.Errorf("error = %q, want %q", err, tt.wantErr)
				}
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
			if got != "" {
				t.Errorf("answer = %q, want empty", got)
			}
			if errors.Is(err, ErrRunFailed) != tt.wantFail {
				t.Errorf("errors.Is(err, ErrRunFailed) = %v, want %v", !tt.wantFail, tt.wantFail)
			}
		})
	}
}

func TestRunnerJoinsTextContentsInOrder(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		answerAfterPrompt(
			AssistantContent{Type: "text", Text: "first"},
			AssistantContent{Type: "reasoning", Text: "hidden"},
			AssistantContent{Type: "tool", Text: "hidden too"},
			AssistantContent{Type: "text", Text: "second"},
		)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "first\nsecond"; got != want {
		t.Errorf("answer = %q, want %q", got, want)
	}
}

func TestRunnerStopsOnPendingForm(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		addForm().
		runPlan(nil, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if !errors.Is(err, ErrUnsupportedInteraction) {
		t.Fatalf("error = %v, want ErrUnsupportedInteraction", err)
	}
	if !strings.Contains(err.Error(), "form") {
		t.Errorf("error = %v, want the interaction named", err)
	}
	if got != "" {
		t.Errorf("answer = %q, want empty", got)
	}
}

func TestRunnerStopsOnFormWhileWaitingForStop(t *testing.T) {
	t.Parallel()
	// A session blocked on a form keeps reporting itself as running: the wait must
	// end instead of running into the run deadline.
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		addForm().
		runPlan([]bool{true, true, true, true, true, true, true, true, true, true}, true)
	runner := testRunner(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := runner.Run(ctx, testTitle, "hi"); !errors.Is(err, ErrUnsupportedInteraction) {
		t.Fatalf("error = %v, want ErrUnsupportedInteraction", err)
	}
}

func TestRunnerDoesNotStopOnTheFirstInactivePoll(t *testing.T) {
	t.Parallel()
	// active: false, true, true, false. The first false happens before the agent
	// loop claimed the session, so the wait must continue instead of concluding.
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		addAnswer("msg_old", AssistantContent{Type: "text", Text: "previous answer"}).
		answerAfterPrompt(AssistantContent{Type: "text", Text: "eventual answer"}).
		lagProjection(1).
		runPlan([]bool{false, true, true, false}, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "eventual answer" {
		t.Errorf("answer = %q, want eventual answer", got)
	}
	if n := fake.activeRequests(); n < 4 {
		t.Errorf("active requests = %d, want the wait to continue past the first inactive poll", n)
	}
}

func TestRunnerKeepsWaitingWhileTheRunHasNotStarted(t *testing.T) {
	t.Parallel()
	// The session is never observed as running and never answers: the run may
	// still be starting up, so the wait ends with the run deadline instead of a
	// premature "completed".
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		runPlan(nil, false)
	runner := testRunner(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	got, err := runner.Run(ctx, testTitle, "hi")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline error", err)
	}
	if got != "" {
		t.Errorf("answer = %q, want empty", got)
	}
	if n := fake.activeRequests(); n < 2 {
		t.Errorf("active requests = %d, want repeated polling", n)
	}
}

func TestRunnerTimesOutWhileRunning(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		runPlan([]bool{true, true, true, true, true, true}, true)
	runner := testRunner(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	got, err := runner.Run(ctx, testTitle, "hi")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a deadline error", err)
	}
	if got != "" {
		t.Errorf("answer = %q, want empty", got)
	}
}

func TestRunnerReportsPromptFailure(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		fail(http.MethodPost, "/api/session/ses_1/prompt", http.StatusBadRequest, `{"_tag":"InvalidRequestError"}`)
	runner := testRunner(t, fake)

	if _, err := runner.Run(context.Background(), testTitle, "hi"); err == nil ||
		!strings.Contains(err.Error(), "send prompt") {
		t.Errorf("error = %v, want a prompt error", err)
	}
}

func TestRunnerReportsActiveFailure(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		addSession("ses_1", testTitle).
		fail(http.MethodGet, "/api/session/active", http.StatusInternalServerError, "boom")
	runner := testRunner(t, fake)

	if _, err := runner.Run(context.Background(), testTitle, "hi"); err == nil ||
		!strings.Contains(err.Error(), "active sessions") {
		t.Errorf("error = %v, want an active sessions error", err)
	}
}

func TestRunnerCreatesSessionForNewThread(t *testing.T) {
	t.Parallel()
	fake := newFakeOpencode().
		answerAfterPrompt(AssistantContent{Type: "text", Text: "hello"}).
		runPlan(nil, false)
	runner := testRunner(t, fake)

	got, err := runner.Run(context.Background(), testTitle, "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("answer = %q, want hello", got)
	}
	if log := fake.promptLog(); len(log) != 1 || log[0].SessionID != "ses_new1" {
		t.Errorf("prompt log = %+v, want the created session", log)
	}
}
