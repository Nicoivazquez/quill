package repository

import (
	"context"
	"testing"

	"quill/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newJobTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.TranscriptionJob{}, &models.SpeakerMapping{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func seedJobs(t *testing.T, repo JobRepository) {
	t.Helper()
	ctx := context.Background()

	title1 := "Kubernetes Deployment Discussion"
	transcript1 := "We talked about deploying microservices on k8s"
	if err := repo.Create(ctx, &models.TranscriptionJob{
		ID:         "job-k8s",
		Status:     models.StatusCompleted,
		Title:      &title1,
		Transcript: &transcript1,
	}); err != nil {
		t.Fatal(err)
	}

	title2 := "Budget Planning Meeting"
	transcript2 := "Quarterly budget review and forecasting"
	if err := repo.Create(ctx, &models.TranscriptionJob{
		ID:         "job-budget",
		Status:     models.StatusCompleted,
		Title:      &title2,
		Transcript: &transcript2,
	}); err != nil {
		t.Fatal(err)
	}

	title3 := "Team Standup"
	transcript3 := "Daily standup notes"
	if err := repo.Create(ctx, &models.TranscriptionJob{
		ID:         "job-standup",
		Status:     models.StatusCompleted,
		Title:      &title3,
		Transcript: &transcript3,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestListWithParams_FTSJobIDs_FiltersCorrectly(t *testing.T) {
	db := newJobTestDB(t)
	repo := NewJobRepository(db)
	seedJobs(t, repo)

	// Simulate FTS returning only job-k8s and job-budget
	params := ListParams{
		Offset:    0,
		Limit:     10,
		FTSJobIDs: []string{"job-k8s", "job-budget"},
	}

	jobs, total, err := repo.ListWithParams(context.Background(), params)
	if err != nil {
		t.Fatalf("ListWithParams: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total=2, got %d", total)
	}
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}

	ids := map[string]bool{}
	for _, j := range jobs {
		ids[j.ID] = true
	}
	if !ids["job-k8s"] || !ids["job-budget"] {
		t.Errorf("expected job-k8s and job-budget, got %v", ids)
	}
	if ids["job-standup"] {
		t.Error("job-standup should not appear in FTS-filtered results")
	}
}

func TestListWithParams_FTSJobIDs_OverridesLIKE(t *testing.T) {
	db := newJobTestDB(t)
	repo := NewJobRepository(db)
	seedJobs(t, repo)

	// Both FTSJobIDs and SearchQuery are set — FTSJobIDs should take priority
	params := ListParams{
		Offset:      0,
		Limit:       10,
		FTSJobIDs:   []string{"job-k8s"},
		SearchQuery: "budget", // would match job-budget via LIKE
	}

	jobs, total, err := repo.ListWithParams(context.Background(), params)
	if err != nil {
		t.Fatalf("ListWithParams: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total=1 (FTS takes priority), got %d", total)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-k8s" {
		t.Errorf("expected only job-k8s from FTS, got %v", jobs)
	}
}

func TestListWithParams_SearchQuery_LIKEFallback(t *testing.T) {
	db := newJobTestDB(t)
	repo := NewJobRepository(db)
	seedJobs(t, repo)

	// No FTSJobIDs — SearchQuery falls back to LIKE
	params := ListParams{
		Offset:      0,
		Limit:       10,
		SearchQuery: "budget",
	}

	jobs, total, err := repo.ListWithParams(context.Background(), params)
	if err != nil {
		t.Fatalf("ListWithParams: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total=1 via LIKE, got %d", total)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-budget" {
		t.Errorf("expected job-budget via LIKE fallback, got %v", jobs)
	}
}

func TestListWithParams_EmptyFTSJobIDs_FallsBackToLIKE(t *testing.T) {
	db := newJobTestDB(t)
	repo := NewJobRepository(db)
	seedJobs(t, repo)

	// Empty FTSJobIDs slice should not filter; SearchQuery takes over
	params := ListParams{
		Offset:      0,
		Limit:       10,
		FTSJobIDs:   []string{},
		SearchQuery: "standup",
	}

	jobs, total, err := repo.ListWithParams(context.Background(), params)
	if err != nil {
		t.Fatalf("ListWithParams: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total=1, got %d", total)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-standup" {
		t.Errorf("expected job-standup, got %v", jobs)
	}
}
