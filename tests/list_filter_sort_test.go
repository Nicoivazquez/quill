package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"quill/internal/api"
	"quill/internal/models"
	"quill/internal/processing"
	"quill/internal/queue"
	"quill/internal/repository"
	"quill/internal/service"
	"quill/internal/sse"
	"quill/internal/transcription"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ListFilterSortTestSuite struct {
	suite.Suite
	helper  *TestHelper
	router  *gin.Engine
	handler *api.Handler
	jobRepo repository.JobRepository
}

func (suite *ListFilterSortTestSuite) SetupSuite() {
	suite.helper = NewTestHelper(suite.T(), "list_filter_sort_test.db")

	// Initialize repositories
	suite.jobRepo = repository.NewJobRepository(suite.helper.DB)
	userRepo := repository.NewUserRepository(suite.helper.DB)
	apiKeyRepo := repository.NewAPIKeyRepository(suite.helper.DB)
	profileRepo := repository.NewProfileRepository(suite.helper.DB)
	llmConfigRepo := repository.NewLLMConfigRepository(suite.helper.DB)
	summaryRepo := repository.NewSummaryRepository(suite.helper.DB)
	chatRepo := repository.NewChatRepository(suite.helper.DB)
	noteRepo := repository.NewNoteRepository(suite.helper.DB)
	speakerMappingRepo := repository.NewSpeakerMappingRepository(suite.helper.DB)
	contactRepo := repository.NewContactRepository(suite.helper.DB)
	refreshTokenRepo := repository.NewRefreshTokenRepository(suite.helper.DB)
	cloudProviderRepo := repository.NewCloudProviderConfigRepository(suite.helper.DB)

	// Initialize services
	userService := service.NewUserService(userRepo, suite.helper.AuthService)
	fileService := service.NewFileService()

	unifiedProcessor := transcription.NewUnifiedJobProcessor(suite.jobRepo, suite.helper.Config.TempDir, suite.helper.Config.TranscriptsDir)
	quickTranscription, err := transcription.NewQuickTranscriptionService(suite.helper.Config, unifiedProcessor, suite.jobRepo)
	assert.NoError(suite.T(), err)

	taskQueue := queue.NewTaskQueue(1, unifiedProcessor, suite.jobRepo)
	broadcaster := sse.NewBroadcaster()
	multiTrackProcessor := processing.NewMultiTrackProcessor(suite.helper.DB, suite.jobRepo)

	suite.handler = api.NewHandler(
		suite.helper.Config,
		suite.helper.AuthService,
		userService,
		fileService,
		suite.jobRepo,
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
		quickTranscription,
		multiTrackProcessor,
		broadcaster,
	)

	suite.router = api.SetupRoutes(suite.handler, suite.helper.AuthService)
}

func (suite *ListFilterSortTestSuite) TearDownSuite() {
	suite.helper.Cleanup()
}

func (suite *ListFilterSortTestSuite) SetupTest() {
	suite.helper.ResetDB(suite.T())
}

// createJobWithStatus creates a job with a specific status
func (suite *ListFilterSortTestSuite) createJobWithStatus(title string, status models.JobStatus) *models.TranscriptionJob {
	job := &models.TranscriptionJob{
		Title:     &title,
		Status:    status,
		AudioPath: "test/path/" + title + ".mp3",
		Parameters: models.WhisperXParams{
			Model:       "base",
			BatchSize:   16,
			ComputeType: "float16",
			Device:      "auto",
		},
	}
	err := suite.helper.DB.Create(job).Error
	suite.Require().NoError(err)
	return job
}

// createJobWithFolder creates a job in a specific folder
func (suite *ListFilterSortTestSuite) createJobWithFolder(title string, folder *string) *models.TranscriptionJob {
	job := &models.TranscriptionJob{
		Title:     &title,
		Status:    models.StatusCompleted,
		AudioPath: "test/path/" + title + ".mp3",
		Folder:    folder,
		Parameters: models.WhisperXParams{
			Model:       "base",
			BatchSize:   16,
			ComputeType: "float16",
			Device:      "auto",
		},
	}
	err := suite.helper.DB.Create(job).Error
	suite.Require().NoError(err)
	return job
}

// createSpeakerMapping creates a speaker mapping for a job
func (suite *ListFilterSortTestSuite) createSpeakerMapping(jobID, originalSpeaker, customName string) {
	mapping := &models.SpeakerMapping{
		TranscriptionJobID: jobID,
		OriginalSpeaker:    originalSpeaker,
		CustomName:         customName,
	}
	err := suite.helper.DB.Create(mapping).Error
	suite.Require().NoError(err)
}

// doRequest performs an authenticated GET request
func (suite *ListFilterSortTestSuite) doRequest(url string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", suite.helper.TestAPIKey)
	w := httptest.NewRecorder()
	suite.router.ServeHTTP(w, req)
	return w
}

// parseListResponse parses the list response and returns jobs and total
func (suite *ListFilterSortTestSuite) parseListResponse(w *httptest.ResponseRecorder) ([]map[string]interface{}, int64) {
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	suite.Require().NoError(err)

	jobs := response["jobs"].([]interface{})
	pagination := response["pagination"].(map[string]interface{})
	total := int64(pagination["total"].(float64))

	result := make([]map[string]interface{}, len(jobs))
	for i, j := range jobs {
		result[i] = j.(map[string]interface{})
	}
	return result, total
}

// --- Status Filter Tests ---

func (suite *ListFilterSortTestSuite) TestFilterByStatusCompleted() {
	suite.createJobWithStatus("Job A", models.StatusCompleted)
	suite.createJobWithStatus("Job B", models.StatusFailed)
	suite.createJobWithStatus("Job C", models.StatusCompleted)
	suite.createJobWithStatus("Job D", models.StatusPending)

	w := suite.doRequest("/api/v1/transcription/list?status=completed")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(2), total)
	assert.Len(suite.T(), jobs, 2)

	for _, job := range jobs {
		assert.Equal(suite.T(), string(models.StatusCompleted), job["status"])
	}
}

func (suite *ListFilterSortTestSuite) TestFilterByStatusFailed() {
	suite.createJobWithStatus("Job A", models.StatusCompleted)
	suite.createJobWithStatus("Job B", models.StatusFailed)

	w := suite.doRequest("/api/v1/transcription/list?status=failed")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
	assert.Equal(suite.T(), string(models.StatusFailed), jobs[0]["status"])
}

func (suite *ListFilterSortTestSuite) TestFilterByStatusNoMatch() {
	suite.createJobWithStatus("Job A", models.StatusCompleted)

	w := suite.doRequest("/api/v1/transcription/list?status=processing")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(0), total)
	assert.Len(suite.T(), jobs, 0)
}

// --- Speaker Filter Tests ---

func (suite *ListFilterSortTestSuite) TestFilterBySpeaker() {
	jobA := suite.createJobWithStatus("Interview with Alice", models.StatusCompleted)
	jobB := suite.createJobWithStatus("Interview with Bob", models.StatusCompleted)
	suite.createJobWithStatus("Solo recording", models.StatusCompleted)

	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice Smith")
	suite.createSpeakerMapping(jobA.ID, "speaker_01", "Bob Jones")
	suite.createSpeakerMapping(jobB.ID, "speaker_00", "Alice Smith")

	w := suite.doRequest("/api/v1/transcription/list?speaker=Alice+Smith")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(2), total)
	assert.Len(suite.T(), jobs, 2)
}

func (suite *ListFilterSortTestSuite) TestFilterBySpeakerNoMatch() {
	jobA := suite.createJobWithStatus("Interview", models.StatusCompleted)
	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice Smith")

	w := suite.doRequest("/api/v1/transcription/list?speaker=Unknown+Person")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(0), total)
	assert.Len(suite.T(), jobs, 0)
}

func (suite *ListFilterSortTestSuite) TestFilterBySpeakerNoDuplicates() {
	// A job with 2 mappings for the same custom name should appear only once
	jobA := suite.createJobWithStatus("Multi-speaker", models.StatusCompleted)
	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice Smith")
	suite.createSpeakerMapping(jobA.ID, "speaker_01", "Alice Smith")

	w := suite.doRequest("/api/v1/transcription/list?speaker=Alice+Smith")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
}

// --- Sort Tests ---

func (suite *ListFilterSortTestSuite) TestSortByTitleAsc() {
	suite.createJobWithStatus("Charlie", models.StatusCompleted)
	suite.createJobWithStatus("Alpha", models.StatusCompleted)
	suite.createJobWithStatus("Bravo", models.StatusCompleted)

	w := suite.doRequest("/api/v1/transcription/list?sort_by=title&sort_order=asc")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, _ := suite.parseListResponse(w)
	assert.Len(suite.T(), jobs, 3)
	assert.Equal(suite.T(), "Alpha", jobs[0]["title"])
	assert.Equal(suite.T(), "Bravo", jobs[1]["title"])
	assert.Equal(suite.T(), "Charlie", jobs[2]["title"])
}

func (suite *ListFilterSortTestSuite) TestSortByTitleDesc() {
	suite.createJobWithStatus("Alpha", models.StatusCompleted)
	suite.createJobWithStatus("Bravo", models.StatusCompleted)
	suite.createJobWithStatus("Charlie", models.StatusCompleted)

	w := suite.doRequest("/api/v1/transcription/list?sort_by=title&sort_order=desc")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, _ := suite.parseListResponse(w)
	assert.Len(suite.T(), jobs, 3)
	assert.Equal(suite.T(), "Charlie", jobs[0]["title"])
	assert.Equal(suite.T(), "Bravo", jobs[1]["title"])
	assert.Equal(suite.T(), "Alpha", jobs[2]["title"])
}

func (suite *ListFilterSortTestSuite) TestSortByCreatedAtDesc() {
	suite.createJobWithStatus("First", models.StatusCompleted)
	suite.createJobWithStatus("Second", models.StatusCompleted)
	suite.createJobWithStatus("Third", models.StatusCompleted)

	w := suite.doRequest("/api/v1/transcription/list?sort_by=created_at&sort_order=desc")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, _ := suite.parseListResponse(w)
	assert.Len(suite.T(), jobs, 3)
	// Most recently created first
	assert.Equal(suite.T(), "Third", jobs[0]["title"])
	assert.Equal(suite.T(), "Second", jobs[1]["title"])
	assert.Equal(suite.T(), "First", jobs[2]["title"])
}

func (suite *ListFilterSortTestSuite) TestSortByInvalidColumnFallsBackToDefault() {
	suite.createJobWithStatus("First", models.StatusCompleted)
	suite.createJobWithStatus("Second", models.StatusCompleted)

	// Attempt SQL injection via sort_by
	w := suite.doRequest("/api/v1/transcription/list?sort_by=id;DROP+TABLE+transcription_jobs--&sort_order=asc")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	// Should still return results (falls back to default sort)
	assert.Equal(suite.T(), int64(2), total)
	assert.Len(suite.T(), jobs, 2)
	// Default sort is created_at desc, so "Second" (created later) is first
	assert.Equal(suite.T(), "Second", jobs[0]["title"])
}

func (suite *ListFilterSortTestSuite) TestSortByInvalidOrderDefaultsToDesc() {
	suite.createJobWithStatus("Alpha", models.StatusCompleted)
	suite.createJobWithStatus("Bravo", models.StatusCompleted)

	w := suite.doRequest("/api/v1/transcription/list?sort_by=title&sort_order=INVALID")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, _ := suite.parseListResponse(w)
	assert.Len(suite.T(), jobs, 2)
	// Invalid order defaults to desc
	assert.Equal(suite.T(), "Bravo", jobs[0]["title"])
}

// --- Combined Filter Tests ---

func (suite *ListFilterSortTestSuite) TestFilterByStatusAndSpeaker() {
	jobA := suite.createJobWithStatus("Completed with Alice", models.StatusCompleted)
	jobB := suite.createJobWithStatus("Failed with Alice", models.StatusFailed)
	suite.createJobWithStatus("Completed without speaker", models.StatusCompleted)

	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice")
	suite.createSpeakerMapping(jobB.ID, "speaker_00", "Alice")

	w := suite.doRequest("/api/v1/transcription/list?status=completed&speaker=Alice")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
	assert.Equal(suite.T(), "Completed with Alice", jobs[0]["title"])
}

func (suite *ListFilterSortTestSuite) TestFilterByStatusAndFolder() {
	workFolder := "Work"
	suite.createJobWithFolder("Work completed", &workFolder)
	suite.createJobWithFolder("Unfiled completed", nil)
	// Change the first job's status
	suite.helper.DB.Model(&models.TranscriptionJob{}).Where("title = ?", "Work completed").Update("status", models.StatusCompleted)
	suite.helper.DB.Model(&models.TranscriptionJob{}).Where("title = ?", "Unfiled completed").Update("status", models.StatusFailed)

	w := suite.doRequest("/api/v1/transcription/list?folder=Work&status=completed")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
}

// --- ListDistinctSpeakers Tests ---

func (suite *ListFilterSortTestSuite) TestListDistinctSpeakers() {
	jobA := suite.createJobWithStatus("Job A", models.StatusCompleted)
	jobB := suite.createJobWithStatus("Job B", models.StatusCompleted)

	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice Smith")
	suite.createSpeakerMapping(jobA.ID, "speaker_01", "Bob Jones")
	suite.createSpeakerMapping(jobB.ID, "speaker_00", "Alice Smith") // duplicate
	suite.createSpeakerMapping(jobB.ID, "speaker_01", "Charlie Brown")

	w := suite.doRequest("/api/v1/transcription/speakers/distinct")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(suite.T(), err)

	speakers := response["speakers"].([]interface{})
	assert.Len(suite.T(), speakers, 3) // Alice, Bob, Charlie (deduped)
	// Sorted alphabetically
	assert.Equal(suite.T(), "Alice Smith", speakers[0])
	assert.Equal(suite.T(), "Bob Jones", speakers[1])
	assert.Equal(suite.T(), "Charlie Brown", speakers[2])
}

func (suite *ListFilterSortTestSuite) TestListDistinctSpeakersEmpty() {
	w := suite.doRequest("/api/v1/transcription/speakers/distinct")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(suite.T(), err)

	speakers := response["speakers"].([]interface{})
	assert.Len(suite.T(), speakers, 0)
}

// --- Repository Unit Tests ---

func (suite *ListFilterSortTestSuite) TestListParamsStatusFilter() {
	suite.createJobWithStatus("Completed", models.StatusCompleted)
	suite.createJobWithStatus("Failed", models.StatusFailed)
	suite.createJobWithStatus("Pending", models.StatusPending)

	ctx := context.Background()
	jobs, total, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:  100,
		Status: string(models.StatusCompleted),
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
	assert.Equal(suite.T(), models.StatusCompleted, jobs[0].Status)
}

func (suite *ListFilterSortTestSuite) TestListParamsSpeakerFilter() {
	jobA := suite.createJobWithStatus("With Speaker", models.StatusCompleted)
	suite.createJobWithStatus("Without Speaker", models.StatusCompleted)
	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Jane Doe")

	ctx := context.Background()
	jobs, total, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:   100,
		Speaker: "Jane Doe",
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), int64(1), total)
	assert.Len(suite.T(), jobs, 1)
	assert.Equal(suite.T(), "With Speaker", *jobs[0].Title)
}

func (suite *ListFilterSortTestSuite) TestListParamsSortAllowlist() {
	suite.createJobWithStatus("Alpha", models.StatusCompleted)
	suite.createJobWithStatus("Bravo", models.StatusCompleted)

	ctx := context.Background()

	// Valid sort column
	jobs, _, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:     100,
		SortBy:    "title",
		SortOrder: "asc",
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "Alpha", *jobs[0].Title)
	assert.Equal(suite.T(), "Bravo", *jobs[1].Title)

	// Invalid sort column falls back to default (created_at desc)
	jobs2, _, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:     100,
		SortBy:    "malicious_column",
		SortOrder: "asc",
	})
	assert.NoError(suite.T(), err)
	// Default is created_at desc, so "Bravo" (created second) comes first
	assert.Equal(suite.T(), "Bravo", *jobs2[0].Title)
}

func (suite *ListFilterSortTestSuite) TestListParamsFolderFilter() {
	workFolder := "Work"
	personalFolder := "Personal"
	suite.createJobWithFolder("Work Job", &workFolder)
	suite.createJobWithFolder("Personal Job", &personalFolder)
	suite.createJobWithFolder("Unfiled Job", nil)

	ctx := context.Background()

	// Filter by specific folder
	jobs, total, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:  100,
		Folder: &workFolder,
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), int64(1), total)
	assert.Equal(suite.T(), "Work Job", *jobs[0].Title)

	// Filter by root (unfiled)
	emptyFolder := ""
	jobs2, total2, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:  100,
		Folder: &emptyFolder,
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), int64(1), total2)
	assert.Equal(suite.T(), "Unfiled Job", *jobs2[0].Title)

	// No folder filter (all)
	jobs3, total3, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit: 100,
	})
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), int64(3), total3)
	assert.Len(suite.T(), jobs3, 3)
}

func (suite *ListFilterSortTestSuite) TestListDistinctSpeakersRepo() {
	jobA := suite.createJobWithStatus("Job A", models.StatusCompleted)
	jobB := suite.createJobWithStatus("Job B", models.StatusCompleted)

	suite.createSpeakerMapping(jobA.ID, "speaker_00", "Alice")
	suite.createSpeakerMapping(jobA.ID, "speaker_01", "Bob")
	suite.createSpeakerMapping(jobB.ID, "speaker_00", "Alice") // duplicate

	ctx := context.Background()
	speakers, err := suite.jobRepo.ListDistinctSpeakers(ctx, nil)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), speakers, 2) // Alice, Bob
	assert.Equal(suite.T(), "Alice", speakers[0])
	assert.Equal(suite.T(), "Bob", speakers[1])
}

func (suite *ListFilterSortTestSuite) TestSortOrderValidation() {
	suite.createJobWithStatus("Alpha", models.StatusCompleted)

	ctx := context.Background()

	// Sort order not asc or desc defaults to desc
	jobs, _, err := suite.jobRepo.ListWithParams(ctx, repository.ListParams{
		Limit:     100,
		SortBy:    "title",
		SortOrder: "DROP TABLE",
	})
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), jobs, 1) // Still returns results safely
}

// --- Pagination with Filters ---

func (suite *ListFilterSortTestSuite) TestPaginationWithFilters() {
	// Create 5 completed jobs
	for i := 0; i < 5; i++ {
		suite.createJobWithStatus(fmt.Sprintf("Completed %d", i), models.StatusCompleted)
	}
	// Create 3 failed jobs
	for i := 0; i < 3; i++ {
		suite.createJobWithStatus(fmt.Sprintf("Failed %d", i), models.StatusFailed)
	}

	// Page 1 of completed, 2 per page
	w := suite.doRequest("/api/v1/transcription/list?status=completed&page=1&limit=2")
	assert.Equal(suite.T(), http.StatusOK, w.Code)

	jobs, total := suite.parseListResponse(w)
	assert.Equal(suite.T(), int64(5), total) // 5 total completed
	assert.Len(suite.T(), jobs, 2)           // 2 per page

	// Page 3 of completed, 2 per page (should have 1)
	w2 := suite.doRequest("/api/v1/transcription/list?status=completed&page=3&limit=2")
	assert.Equal(suite.T(), http.StatusOK, w2.Code)

	jobs2, _ := suite.parseListResponse(w2)
	assert.Len(suite.T(), jobs2, 1) // Last page
}

func TestListFilterSortTestSuite(t *testing.T) {
	suite.Run(t, new(ListFilterSortTestSuite))
}
