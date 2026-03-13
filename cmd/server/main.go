package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"quill/internal/api"
	"quill/internal/auth"
	"quill/internal/config"
	"quill/internal/contacts"
	"quill/internal/database"
	"quill/internal/folderwatch"
	"quill/internal/models"
	"quill/internal/processing"
	"quill/internal/queue"
	"quill/internal/repository"
	"quill/internal/service"
	"quill/internal/sse"
	"quill/internal/transcription"
	"quill/internal/transcription/adapters"
	"quill/internal/transcription/registry"
	"quill/pkg/logger"
)

// Version information (set by GoReleaser)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// @title Quill API
// @version 1.0
// @description Audio transcription service using WhisperX
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT token with Bearer prefix

func main() {
	// Handle version flag
	var showVersion = flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Quill %s\n", version)
		fmt.Printf("Commit: %s\n", commit)
		fmt.Printf("Built: %s\n", date)
		os.Exit(0)
	}

	// Initialize structured logging first
	logger.Init(os.Getenv("LOG_LEVEL"))
	logger.Info("Starting Quill", "version", version)

	// Load configuration
	logger.Startup("config", "Loading configuration")
	cfg := config.Load()
	_ = os.Setenv("AUTH_MODE", cfg.AuthMode)

	// Register adapters with config-based paths
	registerAdapters(cfg)

	// Initialize database
	logger.Startup("database", "Connecting to database")
	if err := database.Initialize(cfg.DatabasePath); err != nil {
		logger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Initialize authentication service
	logger.Startup("auth", "Setting up authentication")
	authService := auth.NewAuthService(cfg.JWTSecret)

	// Initialize SSE Broadcaster
	logger.Startup("sse", "Initializing SSE broadcaster")
	broadcaster := sse.NewBroadcaster()

	// Initialize repositories
	logger.Startup("repository", "Initializing repositories")
	jobRepo := repository.NewJobRepository(database.DB)
	userRepo := repository.NewUserRepository(database.DB)
	apiKeyRepo := repository.NewAPIKeyRepository(database.DB)
	profileRepo := repository.NewProfileRepository(database.DB)
	llmConfigRepo := repository.NewLLMConfigRepository(database.DB)
	summaryRepo := repository.NewSummaryRepository(database.DB)
	chatRepo := repository.NewChatRepository(database.DB)
	noteRepo := repository.NewNoteRepository(database.DB)
	speakerMappingRepo := repository.NewSpeakerMappingRepository(database.DB)
	contactRepo := repository.NewContactRepository(database.DB)
	refreshTokenRepo := repository.NewRefreshTokenRepository(database.DB)
	cloudProviderRepo := repository.NewCloudProviderConfigRepository(database.DB)
	watchedFolderRepo := repository.NewWatchedFolderRepository(database.DB)

	// Register cloud adapters now that repositories are available.
	registerCloudAdapters(cloudProviderRepo)

	// Initialize services
	logger.Startup("service", "Initializing services")
	userService := service.NewUserService(userRepo, authService)
	fileService := service.NewFileService()

	if cfg.IsLocalAuth() {
		if err := ensureLocalUser(userRepo); err != nil {
			logger.Error("Failed to ensure local user", "error", err)
			os.Exit(1)
		}
	}

	// Initialize unified transcription processor
	logger.Startup("transcription", "Initializing transcription service")
	unifiedProcessor := transcription.NewUnifiedJobProcessor(jobRepo, cfg.TempDir, cfg.TranscriptsDir)
	unifiedProcessor.GetUnifiedService().SetBroadcaster(broadcaster)

	// Bootstrap embedded Python environment (for all adapters) unless deferred.
	// Desktop builds can set QUILL_DEFER_MODEL_INIT=true to avoid long first-run startup delays.
	deferModelInit := strings.EqualFold(os.Getenv("QUILL_DEFER_MODEL_INIT"), "true")
	if deferModelInit {
		logger.Startup("python", "Deferring Python environment setup until first transcription request")
	} else {
		logger.Startup("python", "Preparing Python environment")
		if err := unifiedProcessor.InitEmbeddedPythonEnv(); err != nil {
			logger.Error("Failed to prepare Python environment", "error", err)
			os.Exit(1)
		}
	}

	// Initialize quick transcription service
	logger.Startup("quick-transcription", "Initializing quick transcription service")
	quickTranscriptionService, err := transcription.NewQuickTranscriptionService(cfg, unifiedProcessor, jobRepo)
	if err != nil {
		logger.Error("Failed to initialize quick transcription service", "error", err)
		os.Exit(1)
	}

	// Initialize task queue
	logger.Startup("queue", "Starting background processing")
	taskQueue := queue.NewTaskQueue(2, unifiedProcessor, jobRepo) // 2 workers
	taskQueue.Start()
	defer taskQueue.Stop()

	// Initialize desktop auto-import folder watcher service
	folderWatchService := folderwatch.NewService(cfg, watchedFolderRepo, jobRepo, userRepo, profileRepo, taskQueue)
	if err := folderWatchService.Start(context.Background()); err != nil {
		logger.Warn("Some auto-import folders failed to initialize", "error", err)
	}
	defer folderWatchService.Stop()

	contactManager := contacts.NewManager(database.DB, contactRepo, cfg.WhisperXEnv)
	if err := contactManager.Start(context.Background()); err != nil {
		logger.Warn("Contact file-sync manager failed to initialize", "error", err)
	}
	defer contactManager.Stop()

	runtimeWarmup := transcription.NewDesktopRuntimeWarmupManager(deferModelInit, "small")
	defer runtimeWarmup.Stop()

	// Initialize multi-track processor
	multiTrackProcessor := processing.NewMultiTrackProcessor(database.DB, jobRepo)

	// Initialize API handlers
	handler := api.NewHandler(
		cfg,
		authService,
		userService,
		fileService,
		jobRepo,
		apiKeyRepo,
		profileRepo,
		userRepo,
		llmConfigRepo,
		summaryRepo,
		chatRepo,
		noteRepo,
		speakerMappingRepo,
		contactRepo,
		refreshTokenRepo,
		cloudProviderRepo,
		taskQueue,
		unifiedProcessor,
		quickTranscriptionService,
		multiTrackProcessor,
		broadcaster,
	)
	handler.SetFolderWatchService(folderWatchService)
	handler.SetContactManager(contactManager)
	handler.SetRuntimeWarmupManager(runtimeWarmup)
	taskQueue.SetOnJobCompleted(func(jobID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		if err := handler.AutoGenerateTranscriptionTitleForJob(ctx, jobID); err != nil {
			logger.Warn("Auto title generation after transcription completion failed", "job_id", jobID, "error", err)
		}

		// Run speaker auto-identification using voice signatures from contacts.
		labelCtx, labelCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer labelCancel()
		if err := handler.AutoLabelSpeakersForJob(labelCtx, jobID); err != nil {
			logger.Warn("Auto speaker identification after transcription completion failed", "job_id", jobID, "error", err)
		}
	})

	// Set up router
	router := api.SetupRoutes(handler, authService)

	// Create server
	srv := &http.Server{
		Addr:    cfg.Host + ":" + cfg.Port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		logger.Debug("Starting HTTP server", "host", cfg.Host, "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Failed to start server", "error", err)
			os.Exit(1)
		}
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)
	logger.Info("Quill is ready",
		"url", fmt.Sprintf("http://%s:%s", cfg.Host, cfg.Port))
	logger.Debug("API documentation available at /swagger/index.html")

	if deferModelInit {
		runtimeWarmup.Start(context.Background())
	}

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown broadcaster to close all active SSE connections
	if broadcaster != nil {
		broadcaster.Shutdown()
	}

	// Gracefully shutdown the server
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Server stopped")
}

func ensureLocalUser(userRepo repository.UserRepository) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	user, err := userRepo.FindByID(ctx, uint(1))
	if err == nil && user != nil {
		return nil
	}
	userByName, nameErr := userRepo.FindByUsername(ctx, "local")
	if nameErr == nil && userByName != nil {
		return nil
	}

	defaultPassword := "local-mode-password"
	hashedPassword, hashErr := auth.HashPassword(defaultPassword)
	if hashErr != nil {
		return hashErr
	}

	localUser := models.User{
		ID:                            1,
		Username:                      "local",
		Password:                      hashedPassword,
		AutoTranscriptionEnabled:      false,
		AutoSummaryEnabled:            false,
		AutoTranscriptionTitleEnabled: true,
		AutoChatTitleEnabled:          true,
	}

	return userRepo.Create(ctx, &localUser)
}

// registerAdapters registers all transcription and diarization adapters with config-based paths
func registerAdapters(cfg *config.Config) {
	logger.Info("Registering adapters with environment path", "whisperx_env", cfg.WhisperXEnv)

	// Shared environment path for NVIDIA models (NeMo-based)
	nvidiaEnvPath := filepath.Join(cfg.WhisperXEnv, "parakeet")

	// Dedicated environment path for PyAnnote (to avoid dependency conflicts)
	pyannoteEnvPath := filepath.Join(cfg.WhisperXEnv, "pyannote")

	// Dedicated environment path for Voxtral (Mistral AI model)
	voxtralEnvPath := filepath.Join(cfg.WhisperXEnv, "voxtral")

	// Register transcription adapters
	registry.RegisterTranscriptionAdapter("whisperx",
		adapters.NewWhisperXAdapter(cfg.WhisperXEnv))
	registry.RegisterTranscriptionAdapter("parakeet",
		adapters.NewParakeetAdapter(nvidiaEnvPath))
	registry.RegisterTranscriptionAdapter("canary",
		adapters.NewCanaryAdapter(nvidiaEnvPath)) // Shares with Parakeet
	registry.RegisterTranscriptionAdapter("voxtral",
		adapters.NewVoxtralAdapter(voxtralEnvPath))
	registry.RegisterTranscriptionAdapter("openai_whisper",
		adapters.NewOpenAIAdapter(cfg.OpenAIAPIKey))

	// Register diarization adapters
	registry.RegisterDiarizationAdapter("pyannote",
		adapters.NewPyAnnoteAdapter(pyannoteEnvPath)) // Dedicated environment
	registry.RegisterDiarizationAdapter("sortformer",
		adapters.NewSortformerAdapter(nvidiaEnvPath)) // Shares with Parakeet

	logger.Info("Adapter registration complete")
}

// registerCloudAdapters registers cloud-based adapters that require repository access for API keys.
// Called after the database and repositories are initialized.
func registerCloudAdapters(cloudProviderRepo repository.CloudProviderConfigRepository) {
	assemblyAIAdapter := adapters.NewAssemblyAIAdapter(cloudProviderRepo)
	registry.RegisterTranscriptionAdapter("assemblyai", assemblyAIAdapter)
	registry.RegisterTranscriptionAdapter("assemblyai-best", assemblyAIAdapter)
	registry.RegisterTranscriptionAdapter("assemblyai-nano", assemblyAIAdapter)
	registry.RegisterDiarizationAdapter("assemblyai", assemblyAIAdapter)
	logger.Info("Cloud adapter registration complete", "provider", "assemblyai")

	deepgramAdapter := adapters.NewDeepgramAdapter(cloudProviderRepo)
	registry.RegisterTranscriptionAdapter("deepgram", deepgramAdapter)
	registry.RegisterTranscriptionAdapter("deepgram-nova-3", deepgramAdapter)
	registry.RegisterTranscriptionAdapter("deepgram-nova-2", deepgramAdapter)
	registry.RegisterTranscriptionAdapter("deepgram-whisper", deepgramAdapter)
	registry.RegisterDiarizationAdapter("deepgram", deepgramAdapter)
	logger.Info("Cloud adapter registration complete", "provider", "deepgram")
}
