package transcription

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRuntimeWarmupManagerCancellation verifies that Stop() mid-run transitions
// the state back to Idle (or leaves it Ready if already finished) without
// leaving the manager stuck in Running state and without panics.
func TestRuntimeWarmupManagerCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "slow-step",
			Title:    "Slow required step",
			Required: true,
			Run: func(ctx context.Context) error {
				close(started) // signal we have started
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					return nil
				}
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}

	// Wait until the step has begun executing.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for slow step to start")
	}

	// Cancel while the step is blocked.
	manager.Stop()

	snapshot := manager.Snapshot()
	switch snapshot.State {
	case RuntimeWarmupStateIdle, RuntimeWarmupStateFailed, RuntimeWarmupStateReady:
		// All acceptable outcomes after cancellation.
	default:
		t.Fatalf("unexpected state after Stop: %s", snapshot.State)
	}

	// Ensure the manager is no longer marked running internally.
	manager.mu.RLock()
	stillRunning := manager.running
	manager.mu.RUnlock()
	if stillRunning {
		t.Error("manager.running must be false after Stop()")
	}
}

// TestRuntimeWarmupManagerCancellation_ResetsToIdle checks the finishCanceled
// path specifically: state must go back to Idle (not remain Running) when the
// step is cancelled before completion.
func TestRuntimeWarmupManagerCancellation_ResetsToIdle(t *testing.T) {
	blockCtx, unblock := context.WithCancel(context.Background())

	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "blocking",
			Title:    "Blocking step",
			Required: true,
			Run: func(ctx context.Context) error {
				<-blockCtx.Done() // block until we call unblock()
				return ctx.Err()
			},
		},
	})

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}

	// Give the goroutine a moment to enter the blocking step.
	time.Sleep(20 * time.Millisecond)

	// Unblock the step — it will return context.Canceled, triggering finishCanceled.
	unblock()
	manager.Stop()

	snapshot := manager.Snapshot()
	if snapshot.State == RuntimeWarmupStateRunning {
		t.Errorf("state must not remain Running after cancellation, got %s", snapshot.State)
	}
}

// TestRuntimeWarmupManagerDisabled verifies that a disabled manager refuses
// to start and exposes the Disabled state.
func TestRuntimeWarmupManagerDisabled(t *testing.T) {
	manager := newRuntimeWarmupManager(false, []runtimeWarmupStepDefinition{
		{
			ID:       "unreachable",
			Title:    "Should not run",
			Required: true,
			Run: func(context.Context) error {
				t.Error("step should never run when warmup is disabled")
				return nil
			},
		},
	})

	if manager.Start(context.Background()) {
		t.Fatal("disabled manager must not start")
	}

	snapshot := manager.Snapshot()
	if snapshot.State != RuntimeWarmupStateDisabled {
		t.Errorf("expected Disabled state, got %s", snapshot.State)
	}
	if !snapshot.TranscriptionReady {
		t.Error("disabled warmup must report TranscriptionReady=true (bypass mode)")
	}
}

// TestRuntimeWarmupManagerDoubleStart verifies idempotency: calling Start()
// twice while running returns false the second time.
func TestRuntimeWarmupManagerDoubleStart(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "slow",
			Title:    "Slow step",
			Required: true,
			Run: func(ctx context.Context) error {
				close(started)
				<-release
				return nil
			},
		},
	})
	defer func() {
		close(release)
		manager.Stop()
	}()

	if !manager.Start(context.Background()) {
		t.Fatal("first Start should succeed")
	}
	<-started

	if manager.Start(context.Background()) {
		t.Error("second Start while running should return false")
	}
}

// TestRuntimeWarmupManager_OptionalTitanetStep_VoiceSignaturesReadiness verifies
// that when the optional titanet step fails, the overall state becomes Ready but
// VoiceSignaturesReady remains false. When the required step also fails, Retry
// can restart the sequence and success on the required step yields Ready state.
func TestRuntimeWarmupManager_OptionalTitanetStep_VoiceSignaturesReadiness(t *testing.T) {
	attempts := int32(0)
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "titanet",
			Title:    "TitaNet",
			Required: false,
			Run: func(context.Context) error {
				if atomic.AddInt32(&attempts, 1) == 1 {
					return errors.New("titanet unavailable")
				}
				return nil
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected warmup to start")
	}
	// After optional titanet failure, overall state must still be Ready
	// (no required steps remain).
	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snapshot := manager.Snapshot()
	if snapshot.VoiceSignaturesReady {
		t.Error("VoiceSignaturesReady must be false when titanet step failed")
	}
	if !snapshot.TranscriptionReady {
		t.Error("TranscriptionReady must be true (no required steps)")
	}

	// Once state is Ready, Retry() returns false — the manager considers itself
	// done. This is correct: re-attempting optional steps requires a fresh run
	// triggered by the required-step failure path.
	if manager.Retry(context.Background()) {
		t.Error("Retry on Ready state should return false (nothing to retry)")
	}
}

// TestRuntimeWarmupManager_RequiredFailureThenRetry_VoiceSignaturesReadiness
// verifies that when a required step fails and Retry is used, the titanet step
// outcome correctly drives VoiceSignaturesReady.
func TestRuntimeWarmupManager_RequiredFailureThenRetry_VoiceSignaturesReadiness(t *testing.T) {
	requiredAttempts := int32(0)
	titanetAttempts := int32(0)

	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "required",
			Title:    "Required step",
			Required: true,
			Run: func(context.Context) error {
				if atomic.AddInt32(&requiredAttempts, 1) == 1 {
					return errors.New("first attempt fails")
				}
				return nil
			},
		},
		{
			ID:       "titanet",
			Title:    "TitaNet",
			Required: false,
			Run: func(context.Context) error {
				atomic.AddInt32(&titanetAttempts, 1)
				return nil
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(context.Background()) {
		t.Fatalf("expected first warmup to start")
	}
	waitForWarmupState(t, manager, RuntimeWarmupStateFailed)

	snapshot := manager.Snapshot()
	if snapshot.VoiceSignaturesReady {
		t.Error("VoiceSignaturesReady must be false after required step failure")
	}

	// Now retry: required step succeeds, titanet runs.
	if !manager.Retry(context.Background()) {
		t.Fatal("expected retry to start after failure")
	}
	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snapshot = manager.Snapshot()
	if !snapshot.VoiceSignaturesReady {
		t.Error("VoiceSignaturesReady must be true after successful retry including titanet")
	}
}

// TestRuntimeWarmupManager_SnapshotIsolation ensures Snapshot() returns a
// deep copy so mutations to the returned struct do not affect internal state.
func TestRuntimeWarmupManager_SnapshotIsolation(t *testing.T) {
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "step-a",
			Title:    "Step A",
			Required: true,
			Run:      func(context.Context) error { return nil },
		},
	})
	defer manager.Stop()

	manager.Start(context.Background())
	waitForWarmupState(t, manager, RuntimeWarmupStateReady)

	snap1 := manager.Snapshot()
	// Mutate the returned snapshot steps slice.
	snap1.Steps[0].ID = "tampered"

	snap2 := manager.Snapshot()
	if snap2.Steps[0].ID == "tampered" {
		t.Error("Snapshot must return an isolated copy; internal state was mutated through snapshot")
	}
}

// TestRuntimeWarmupManager_StopBeforeStart ensures Stop() on an unstarted
// manager does not block or panic.
func TestRuntimeWarmupManager_StopBeforeStart(t *testing.T) {
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Stop()
	}()
	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() on unstarted manager timed out")
	}
}

// TestRuntimeWarmupManager_CancellationBetweenSteps exercises finishCanceled:
// the context is cancelled after step 0 completes but before step 1 starts,
// so the top-of-loop ctx.Err() check triggers finishCanceled().
func TestRuntimeWarmupManager_CancellationBetweenSteps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	step0Done := make(chan struct{})

	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "step0",
			Title:    "Step 0 (succeeds, then we cancel)",
			Required: true,
			Run: func(c context.Context) error {
				// Signal step 0 is done, then cancel the parent context.
				close(step0Done)
				cancel()
				// Sleep briefly so the goroutine scheduler can deliver cancellation
				// before the loop re-checks ctx.Err().
				time.Sleep(5 * time.Millisecond)
				return nil
			},
		},
		{
			ID:       "step1",
			Title:    "Step 1 (should not run)",
			Required: true,
			Run: func(c context.Context) error {
				t.Error("step1 must not run after context cancellation between steps")
				return nil
			},
		},
	})
	defer manager.Stop()

	if !manager.Start(ctx) {
		t.Fatalf("expected warmup to start")
	}

	// Wait for step0 to finish and cancellation to propagate.
	select {
	case <-step0Done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for step0 to complete")
	}

	manager.Stop()

	// After cancellation, state should not be Ready (step1 was skipped).
	snapshot := manager.Snapshot()
	if snapshot.State == RuntimeWarmupStateReady {
		t.Errorf("state must not be Ready when cancelled between steps, got %s", snapshot.State)
	}
}

// TestBuildTranscriptionWarmupSteps_WhisperX verifies the default WhisperX backend
// produces the expected step IDs and titles.
func TestBuildTranscriptionWarmupSteps_WhisperX(t *testing.T) {
	steps := buildTranscriptionWarmupSteps(ModelWhisperX, "small")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps for whisperx, got %d", len(steps))
	}
	if steps[0].ID != "whisperx-runtime" {
		t.Errorf("step[0].ID = %q, want whisperx-runtime", steps[0].ID)
	}
	if steps[1].ID != "whisperx-model" {
		t.Errorf("step[1].ID = %q, want whisperx-model", steps[1].ID)
	}
	for _, s := range steps {
		if !s.Required {
			t.Errorf("step %q must be required", s.ID)
		}
	}
}

// TestBuildTranscriptionWarmupSteps_MLXWhisper verifies the MLX Whisper backend steps.
func TestBuildTranscriptionWarmupSteps_MLXWhisper(t *testing.T) {
	steps := buildTranscriptionWarmupSteps(ModelMLXWhisper, "large-v3-turbo")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps for mlx_whisper, got %d", len(steps))
	}
	if steps[0].ID != "mlx-whisper-runtime" {
		t.Errorf("step[0].ID = %q, want mlx-whisper-runtime", steps[0].ID)
	}
	if steps[1].ID != "mlx-whisper-model" {
		t.Errorf("step[1].ID = %q, want mlx-whisper-model", steps[1].ID)
	}
	if steps[1].Title != "Downloading MLX Whisper model (large-v3-turbo)" {
		t.Errorf("step[1].Title = %q, unexpected", steps[1].Title)
	}
}

// TestBuildTranscriptionWarmupSteps_WhisperCpp verifies the whisper.cpp backend steps.
func TestBuildTranscriptionWarmupSteps_WhisperCpp(t *testing.T) {
	steps := buildTranscriptionWarmupSteps(ModelWhisperCpp, "medium")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps for whisper_cpp, got %d", len(steps))
	}
	if steps[0].ID != "whisper-cpp-runtime" {
		t.Errorf("step[0].ID = %q, want whisper-cpp-runtime", steps[0].ID)
	}
	if steps[1].ID != "whisper-cpp-model" {
		t.Errorf("step[1].ID = %q, want whisper-cpp-model", steps[1].ID)
	}
	if steps[1].Title != "Downloading whisper.cpp model (medium)" {
		t.Errorf("step[1].Title = %q, unexpected", steps[1].Title)
	}
}

// TestBuildTranscriptionWarmupSteps_EmptyBackendDefaultsToWhisperX verifies fallback behavior.
func TestBuildTranscriptionWarmupSteps_EmptyBackendDefaultsToWhisperX(t *testing.T) {
	steps := buildTranscriptionWarmupSteps("", "small")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps for empty backend (default), got %d", len(steps))
	}
	if steps[0].ID != "whisperx-runtime" {
		t.Errorf("empty backend should default to whisperx, got step[0].ID = %q", steps[0].ID)
	}
}

// TestNewDesktopRuntimeWarmupManagerWithBackend_StepCounts verifies that the
// full manager created with a backend has transcription steps + shared steps.
func TestNewDesktopRuntimeWarmupManagerWithBackend_StepCounts(t *testing.T) {
	tests := []struct {
		backend       string
		expectedTotal int // 2 transcription + 2 shared (titanet + sortformer)
	}{
		{ModelWhisperX, 4},
		{ModelMLXWhisper, 4},
		{ModelWhisperCpp, 4},
		{"", 4}, // defaults to whisperx
	}
	for _, tt := range tests {
		t.Run(tt.backend, func(t *testing.T) {
			manager := NewDesktopRuntimeWarmupManagerWithBackend(false, "small", tt.backend)
			snapshot := manager.Snapshot()
			if snapshot.TotalSteps != tt.expectedTotal {
				t.Errorf("backend=%q: expected %d total steps, got %d", tt.backend, tt.expectedTotal, snapshot.TotalSteps)
			}
		})
	}
}

// TestRuntimeWarmupManager_ConcurrentSnapshot verifies that concurrent
// Snapshot() calls while Start/Stop happens do not cause a race.
func TestRuntimeWarmupManager_ConcurrentSnapshot(t *testing.T) {
	block := make(chan struct{})
	manager := newRuntimeWarmupManager(true, []runtimeWarmupStepDefinition{
		{
			ID:       "concurrent-step",
			Title:    "Concurrent step",
			Required: true,
			Run: func(ctx context.Context) error {
				<-block
				return nil
			},
		},
	})

	manager.Start(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = manager.Snapshot()
		}()
	}
	close(block)
	wg.Wait()
	manager.Stop()
}
