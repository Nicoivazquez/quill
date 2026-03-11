package transcription

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeWarmupManagerSuccess(t *testing.T) {
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "runtime",
			Title:    "Prepare runtime",
			Required: true,
			Run: func(context.Context) error {
				return nil
			},
		},
		{
			ID:       "model",
			Title:    "Warm model",
			Required: true,
			Run: func(context.Context) error {
				return nil
			},
		},
		{
			ID:       "optional",
			Title:    "Optional",
			Required: false,
			Run: func(context.Context) error {
				return nil
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}

	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snapshot := manager.Snapshot()
	if !snapshot.TranscriptionReady {
		t.Fatalf("expected transcription to be ready")
	}
	if snapshot.CompletedSteps != 3 {
		t.Fatalf("expected 3 completed steps, got %d", snapshot.CompletedSteps)
	}
	if snapshot.CompletedRequiredSteps != 2 {
		t.Fatalf("expected 2 completed required steps, got %d", snapshot.CompletedRequiredSteps)
	}
}

func TestRuntimeWarmupManagerOptionalFailureStillAllowsTranscription(t *testing.T) {
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "runtime",
			Title:    "Prepare runtime",
			Required: true,
			Run: func(context.Context) error {
				return nil
			},
		},
		{
			ID:       "model",
			Title:    "Warm model",
			Required: true,
			Run: func(context.Context) error {
				return nil
			},
		},
		{
			ID:       "optional",
			Title:    "Optional",
			Required: false,
			Run: func(context.Context) error {
				return errors.New("optional failed")
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}

	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snapshot := manager.Snapshot()
	if !snapshot.TranscriptionReady {
		t.Fatalf("expected transcription to still be ready after optional step failure")
	}
	optionalStep := snapshot.Steps[2]
	if optionalStep.Status != RuntimeWarmupStepFailed {
		t.Fatalf("expected optional step status=failed, got %q", optionalStep.Status)
	}
	if optionalStep.Error == "" {
		t.Fatalf("expected optional step error to be populated")
	}
}

func TestRuntimeWarmupManagerRetry(t *testing.T) {
	attempts := 0
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "runtime",
			Title:    "Prepare runtime",
			Required: true,
			Run: func(context.Context) error {
				attempts++
				if attempts == 1 {
					return errors.New("temporary failure")
				}
				return nil
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}

	waitForWarmupState(t, manager, RuntimeWarmupStateFailed)

	if !manager.Retry(context.Background()) {
		t.Fatalf("expected retry to start")
	}

	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snapshot := manager.Snapshot()
	if !snapshot.TranscriptionReady {
		t.Fatalf("expected transcription to be ready after retry")
	}
}

func waitForWarmupState(t *testing.T, manager *RuntimeWarmupManager, expected RuntimeWarmupState) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Snapshot().State == expected {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for warmup state %s; got %s", expected, manager.Snapshot().State)
}
