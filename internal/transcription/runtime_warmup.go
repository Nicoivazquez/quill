package transcription

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"quill/internal/contacts"
	"quill/internal/transcription/registry"
	"quill/pkg/logger"
)

type RuntimeWarmupState string

const (
	RuntimeWarmupStateDisabled RuntimeWarmupState = "disabled"
	RuntimeWarmupStateIdle     RuntimeWarmupState = "idle"
	RuntimeWarmupStateRunning  RuntimeWarmupState = "running"
	RuntimeWarmupStateReady    RuntimeWarmupState = "ready"
	RuntimeWarmupStateFailed   RuntimeWarmupState = "failed"
)

type RuntimeWarmupStepStatus string

const (
	RuntimeWarmupStepPending RuntimeWarmupStepStatus = "pending"
	RuntimeWarmupStepRunning RuntimeWarmupStepStatus = "running"
	RuntimeWarmupStepReady   RuntimeWarmupStepStatus = "ready"
	RuntimeWarmupStepFailed  RuntimeWarmupStepStatus = "failed"
)

type RuntimeWarmupStep struct {
	ID          string                  `json:"id"`
	Title       string                  `json:"title"`
	Required    bool                    `json:"required"`
	Status      RuntimeWarmupStepStatus `json:"status"`
	Error       string                  `json:"error,omitempty"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

type RuntimeWarmupStatus struct {
	Enabled                bool                `json:"enabled"`
	State                  RuntimeWarmupState  `json:"state"`
	TranscriptionReady     bool                `json:"transcription_ready"`
	VoiceSignaturesReady   bool                `json:"voice_signatures_ready"`
	CurrentStepID          string              `json:"current_step_id,omitempty"`
	CurrentStepTitle       string              `json:"current_step_title,omitempty"`
	CurrentStepDetail      string              `json:"current_step_detail,omitempty"`
	LastError              string              `json:"last_error,omitempty"`
	CompletedSteps         int                 `json:"completed_steps"`
	TotalSteps             int                 `json:"total_steps"`
	CompletedRequiredSteps int                 `json:"completed_required_steps"`
	TotalRequiredSteps     int                 `json:"total_required_steps"`
	StartedAt              *time.Time          `json:"started_at,omitempty"`
	CompletedAt            *time.Time          `json:"completed_at,omitempty"`
	UpdatedAt              time.Time           `json:"updated_at"`
	Steps                  []RuntimeWarmupStep `json:"steps"`
}

type runtimeWarmupStepDefinition struct {
	ID       string
	Title    string
	Required bool
	Run      func(context.Context) error
}

// whisperWarmable is the full WhisperX interface (4-param WarmModel: ctx, model, device, computeType).
type whisperWarmable interface {
	PrepareEnvironment(context.Context) error
	WarmModel(context.Context, string, string, string) error
}

// simpleWarmable is used by MLX Whisper and whisper.cpp (2-param WarmModel: ctx, model).
type simpleWarmable interface {
	PrepareEnvironment(context.Context) error
	WarmModel(context.Context, string) error
}

type RuntimeWarmupManager struct {
	mu       sync.RWMutex
	enabled  bool
	status   RuntimeWarmupStatus
	stepDefs []runtimeWarmupStepDefinition

	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewDesktopRuntimeWarmupManager(enabled bool, whisperModel string) *RuntimeWarmupManager {
	return NewDesktopRuntimeWarmupManagerWithBackend(enabled, whisperModel, "")
}

// NewDesktopRuntimeWarmupManagerWithBackend creates a warmup manager for the specified
// transcription backend. Supported backends: "whisperx" (default), "mlx_whisper", "whisper_cpp".
func NewDesktopRuntimeWarmupManagerWithBackend(enabled bool, whisperModel, backend string) *RuntimeWarmupManager {
	modelName := strings.TrimSpace(whisperModel)
	if modelName == "" {
		modelName = "small"
	}
	if backend == "" {
		backend = ModelWhisperX
	}

	steps := buildTranscriptionWarmupSteps(backend, modelName)

	// Add voice-signature and diarization steps (shared across all backends)
	steps = append(steps,
		runtimeWarmupStepDefinition{
			ID:       "titanet",
			Title:    "Preparing local voice-signature tools",
			Required: false,
			Run: func(ctx context.Context) error {
				return contacts.PrepareTitaNetRuntime(ctx, registryNVIDIAEnv())
			},
		},
		runtimeWarmupStepDefinition{
			ID:       "sortformer",
			Title:    "Preparing local speaker diarization tools",
			Required: false,
			Run: func(ctx context.Context) error {
				adapter, err := registry.GetRegistry().GetDiarizationAdapter(ModelSortformer)
				if err != nil {
					return fmt.Errorf("sortformer adapter unavailable: %w", err)
				}
				return adapter.PrepareEnvironment(ctx)
			},
		},
	)

	manager := newRuntimeWarmupManager(enabled, steps)

	if enabled {
		logger.Info("Desktop runtime warmup configured", "backend", backend, "whisper_model", modelName)
	}

	return manager
}

// buildTranscriptionWarmupSteps returns the runtime + model warmup steps for the given backend.
func buildTranscriptionWarmupSteps(backend, modelName string) []runtimeWarmupStepDefinition {
	switch backend {
	case ModelMLXWhisper:
		return []runtimeWarmupStepDefinition{
			{
				ID:       "mlx-whisper-runtime",
				Title:    "Installing MLX Whisper runtime",
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelMLXWhisper)
					if err != nil {
						return err
					}
					return adapter.PrepareEnvironment(ctx)
				},
			},
			{
				ID:       "mlx-whisper-model",
				Title:    fmt.Sprintf("Downloading MLX Whisper model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelMLXWhisper)
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName)
				},
			},
		}

	case ModelWhisperCpp:
		return []runtimeWarmupStepDefinition{
			{
				ID:       "whisper-cpp-runtime",
				Title:    "Preparing whisper.cpp runtime",
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelWhisperCpp)
					if err != nil {
						return err
					}
					return adapter.PrepareEnvironment(ctx)
				},
			},
			{
				ID:       "whisper-cpp-model",
				Title:    fmt.Sprintf("Downloading whisper.cpp model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelWhisperCpp)
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName)
				},
			},
		}

	default: // ModelWhisperX
		return []runtimeWarmupStepDefinition{
			{
				ID:       "whisperx-runtime",
				Title:    "Installing local transcription runtime",
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveWhisperWarmable()
					if err != nil {
						return err
					}
					return adapter.PrepareEnvironment(ctx)
				},
			},
			{
				ID:       "whisperx-model",
				Title:    fmt.Sprintf("Downloading default speech model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveWhisperWarmable()
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName, "cpu", "float32")
				},
			},
		}
	}
}

func newRuntimeWarmupManager(enabled bool, stepDefs []runtimeWarmupStepDefinition) *RuntimeWarmupManager {
	steps := make([]RuntimeWarmupStep, 0, len(stepDefs))
	totalRequired := 0
	for _, def := range stepDefs {
		if def.Required {
			totalRequired++
		}
		steps = append(steps, RuntimeWarmupStep{
			ID:       def.ID,
			Title:    def.Title,
			Required: def.Required,
			Status:   RuntimeWarmupStepPending,
		})
	}

	state := RuntimeWarmupStateDisabled
	transcriptionReady := true
	voiceSignaturesReady := true
	if enabled {
		state = RuntimeWarmupStateIdle
		transcriptionReady = false
		voiceSignaturesReady = false
	}

	return &RuntimeWarmupManager{
		enabled:  enabled,
		stepDefs: stepDefs,
		status: RuntimeWarmupStatus{
			Enabled:              enabled,
			State:                state,
			TranscriptionReady:   transcriptionReady,
			VoiceSignaturesReady: voiceSignaturesReady,
			TotalSteps:           len(stepDefs),
			TotalRequiredSteps:   totalRequired,
			UpdatedAt:            time.Now().UTC(),
			Steps:                steps,
		},
	}
}

func (m *RuntimeWarmupManager) Start(ctx context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.enabled || m.running || m.status.State == RuntimeWarmupStateReady {
		return false
	}

	m.resetForRunLocked()
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true
	m.wg.Add(1)
	go m.run(runCtx)
	return true
}

func (m *RuntimeWarmupManager) Retry(ctx context.Context) bool {
	return m.Start(ctx)
}

// WarmOnDemandModel triggers a background download for a specific model.
// It reuses the warmup status/polling mechanism so the frontend banner appears automatically.
// Returns false if a warmup is already running.
func (m *RuntimeWarmupManager) WarmOnDemandModel(ctx context.Context, backend, modelName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return false
	}

	steps := buildModelOnlyWarmupSteps(backend, modelName)
	if len(steps) == 0 {
		return false
	}

	m.stepDefs = steps
	m.enabled = true
	m.resetForRunLocked()
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true
	m.wg.Add(1)
	go m.run(runCtx)
	return true
}

// buildModelOnlyWarmupSteps returns a single step that downloads the model for the given backend.
// Unlike buildTranscriptionWarmupSteps, it skips the runtime installation step (assumed already done).
func buildModelOnlyWarmupSteps(backend, modelName string) []runtimeWarmupStepDefinition {
	switch backend {
	case ModelMLXWhisper:
		return []runtimeWarmupStepDefinition{
			{
				ID:       "mlx-whisper-model-ondemand",
				Title:    fmt.Sprintf("Downloading MLX Whisper model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelMLXWhisper)
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName)
				},
			},
		}
	case ModelWhisperCpp:
		return []runtimeWarmupStepDefinition{
			{
				ID:       "whisper-cpp-model-ondemand",
				Title:    fmt.Sprintf("Downloading whisper.cpp model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveSimpleWarmable(ModelWhisperCpp)
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName)
				},
			},
		}
	case ModelWhisperX:
		return []runtimeWarmupStepDefinition{
			{
				ID:       "whisperx-model-ondemand",
				Title:    fmt.Sprintf("Downloading WhisperX model (%s)", modelName),
				Required: true,
				Run: func(ctx context.Context) error {
					adapter, err := resolveWhisperWarmable()
					if err != nil {
						return err
					}
					return adapter.WarmModel(ctx, modelName, "cpu", "float32")
				},
			},
		}
	default:
		return nil
	}
}

func (m *RuntimeWarmupManager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

func (m *RuntimeWarmupManager) Snapshot() RuntimeWarmupStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneRuntimeWarmupStatus(m.status)
}

func (m *RuntimeWarmupManager) resetForRunLocked() {
	now := time.Now().UTC()
	steps := make([]RuntimeWarmupStep, 0, len(m.stepDefs))
	for _, def := range m.stepDefs {
		steps = append(steps, RuntimeWarmupStep{
			ID:       def.ID,
			Title:    def.Title,
			Required: def.Required,
			Status:   RuntimeWarmupStepPending,
		})
	}

	m.status.State = RuntimeWarmupStateRunning
	m.status.TranscriptionReady = false
	m.status.VoiceSignaturesReady = false
	m.status.CurrentStepID = ""
	m.status.CurrentStepTitle = ""
	m.status.CurrentStepDetail = ""
	m.status.LastError = ""
	m.status.CompletedSteps = 0
	m.status.CompletedRequiredSteps = 0
	m.status.StartedAt = &now
	m.status.CompletedAt = nil
	m.status.UpdatedAt = now
	m.status.Steps = steps
}

func (m *RuntimeWarmupManager) run(ctx context.Context) {
	defer m.wg.Done()

	for index, step := range m.stepDefs {
		if err := ctx.Err(); err != nil {
			m.finishCanceled()
			return
		}

		m.markStepRunning(index)
		if err := step.Run(ctx); err != nil {
			if step.Required {
				m.markStepFailedFatal(index, err)
				return
			}
			m.markStepFailedNonFatal(index, err)
			continue
		}
		m.markStepReady(index)
	}

	m.markReady()
}

func (m *RuntimeWarmupManager) finishCanceled() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.running = false
	m.cancel = nil
	if m.status.State != RuntimeWarmupStateReady {
		m.status.State = RuntimeWarmupStateIdle
	}
	m.status.CurrentStepID = ""
	m.status.CurrentStepTitle = ""
	m.status.CurrentStepDetail = ""
	m.status.UpdatedAt = time.Now().UTC()
}

func (m *RuntimeWarmupManager) markStepRunning(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	step := m.status.Steps[index]
	step.Status = RuntimeWarmupStepRunning
	step.StartedAt = &now
	step.CompletedAt = nil
	step.Error = ""
	m.status.Steps[index] = step
	m.status.CurrentStepID = step.ID
	m.status.CurrentStepTitle = step.Title
	m.status.CurrentStepDetail = step.Title
	m.status.UpdatedAt = now
}

func (m *RuntimeWarmupManager) markStepReady(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	step := m.status.Steps[index]
	step.Status = RuntimeWarmupStepReady
	step.CompletedAt = &now
	step.Error = ""
	m.status.Steps[index] = step
	m.recomputeProgressLocked()
	m.status.UpdatedAt = now
}

func (m *RuntimeWarmupManager) markStepFailedNonFatal(index int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	step := m.status.Steps[index]
	step.Status = RuntimeWarmupStepFailed
	step.CompletedAt = &now
	step.Error = strings.TrimSpace(err.Error())
	m.status.Steps[index] = step
	m.recomputeProgressLocked()
	m.status.UpdatedAt = now

	logger.Warn("Desktop runtime warmup optional step failed", "step", step.ID, "error", step.Error)
}

func (m *RuntimeWarmupManager) markStepFailedFatal(index int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	step := m.status.Steps[index]
	step.Status = RuntimeWarmupStepFailed
	step.CompletedAt = &now
	step.Error = strings.TrimSpace(err.Error())
	m.status.Steps[index] = step
	m.recomputeProgressLocked()
	m.status.State = RuntimeWarmupStateFailed
	m.status.LastError = step.Error
	m.status.CurrentStepID = step.ID
	m.status.CurrentStepTitle = step.Title
	m.status.CurrentStepDetail = step.Title
	m.status.CompletedAt = &now
	m.status.UpdatedAt = now
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	logger.Warn("Desktop runtime warmup failed", "step", step.ID, "error", step.Error)
}

func (m *RuntimeWarmupManager) markReady() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	m.recomputeProgressLocked()
	m.status.State = RuntimeWarmupStateReady
	m.status.CurrentStepID = ""
	m.status.CurrentStepTitle = ""
	m.status.CurrentStepDetail = ""
	m.status.LastError = ""
	m.status.CompletedAt = &now
	m.status.UpdatedAt = now
	m.running = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	logger.Info("Desktop runtime warmup completed")
}

func (m *RuntimeWarmupManager) recomputeProgressLocked() {
	completedSteps := 0
	completedRequired := 0

	for _, step := range m.status.Steps {
		if step.Status == RuntimeWarmupStepReady {
			completedSteps++
			if step.Required {
				completedRequired++
			}
		}
	}

	m.status.CompletedSteps = completedSteps
	m.status.CompletedRequiredSteps = completedRequired
	m.status.TranscriptionReady = m.status.TotalRequiredSteps == 0 || completedRequired >= m.status.TotalRequiredSteps
	m.status.VoiceSignaturesReady = false
	for _, step := range m.status.Steps {
		if step.ID == "titanet" {
			m.status.VoiceSignaturesReady = step.Status == RuntimeWarmupStepReady
			break
		}
	}
}

func resolveWhisperWarmable() (whisperWarmable, error) {
	adapter, err := registry.GetRegistry().GetTranscriptionAdapter(ModelWhisperX)
	if err != nil {
		return nil, fmt.Errorf("whisperx adapter unavailable: %w", err)
	}

	warmable, ok := adapter.(whisperWarmable)
	if !ok {
		return nil, fmt.Errorf("whisperx adapter does not support runtime warmup")
	}

	return warmable, nil
}

func resolveSimpleWarmable(modelID string) (simpleWarmable, error) {
	adapter, err := registry.GetRegistry().GetTranscriptionAdapter(modelID)
	if err != nil {
		return nil, fmt.Errorf("%s adapter unavailable: %w", modelID, err)
	}

	warmable, ok := adapter.(simpleWarmable)
	if !ok {
		return nil, fmt.Errorf("%s adapter does not support runtime warmup", modelID)
	}

	return warmable, nil
}

func cloneRuntimeWarmupStatus(status RuntimeWarmupStatus) RuntimeWarmupStatus {
	cloned := status
	if len(status.Steps) > 0 {
		cloned.Steps = make([]RuntimeWarmupStep, len(status.Steps))
		copy(cloned.Steps, status.Steps)
	}
	return cloned
}

func registryNVIDIAEnv() string {
	adapter, err := registry.GetRegistry().GetDiarizationAdapter(ModelSortformer)
	if err != nil {
		return ""
	}

	type modelPathProvider interface {
		GetModelPath() string
	}

	if provider, ok := adapter.(modelPathProvider); ok {
		return provider.GetModelPath()
	}

	return ""
}
