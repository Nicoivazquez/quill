package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"scriberr/internal/api"
	"scriberr/internal/models"

	"github.com/stretchr/testify/assert"
)

func (suite *APIHandlerTestSuite) TestGetChatModels() {
	modelsResp := suite.makeAuthenticatedRequest("GET", "/api/v1/chat/models", nil, true)
	assert.Equal(suite.T(), http.StatusOK, modelsResp.Code)

	var resp api.ChatModelsResponse
	err := json.Unmarshal(modelsResp.Body.Bytes(), &resp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "openai", resp.Provider)
	assert.Contains(suite.T(), resp.Models, "gpt-3.5-turbo")
	assert.Contains(suite.T(), resp.Models, "gpt-4")
}

func (suite *APIHandlerTestSuite) TestGetChatModelsAllProviders() {
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	err := suite.helper.DB.Create(&models.LLMConfig{
		Provider: "ollama",
		BaseURL:  &ollamaServer.URL,
		IsActive: false,
	}).Error
	assert.NoError(suite.T(), err)

	modelsResp := suite.makeAuthenticatedRequest("GET", "/api/v1/chat/models?all_providers=true", nil, true)
	assert.Equal(suite.T(), http.StatusOK, modelsResp.Code)

	var resp api.ChatModelsResponse
	err = json.Unmarshal(modelsResp.Body.Bytes(), &resp)
	assert.NoError(suite.T(), err)

	var openAIModels []string
	var ollamaModels []string
	for _, provider := range resp.Providers {
		switch provider.Provider {
		case "openai":
			openAIModels = provider.Models
		case "ollama":
			ollamaModels = provider.Models
		}
	}

	assert.Contains(suite.T(), openAIModels, "gpt-3.5-turbo")
	assert.Contains(suite.T(), ollamaModels, "llama3:latest")
}

func (suite *APIHandlerTestSuite) TestCreateChatSession() {
	// Create a transcription first (requires completed status for chat)
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	job.Status = models.StatusCompleted
	transcript := "This is a transcript."
	job.Transcript = &transcript
	suite.helper.DB.Save(job)

	// Test success
	req := api.ChatCreateRequest{
		TranscriptionID: job.ID,
		Model:           "gpt-3.5-turbo",
		Title:           "My Chat Session",
	}
	resp := suite.makeAuthenticatedRequest("POST", "/api/v1/chat/sessions", req, true)
	assert.Equal(suite.T(), http.StatusCreated, resp.Code)

	var sessionResp api.ChatSessionResponse
	err := json.Unmarshal(resp.Body.Bytes(), &sessionResp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), job.ID, sessionResp.TranscriptionID)
	assert.Equal(suite.T(), "My Chat Session", sessionResp.Title)
	assert.Equal(suite.T(), "gpt-3.5-turbo", sessionResp.Model)
	assert.Equal(suite.T(), "openai", sessionResp.Provider)

	// Test validation - transcription must exist
	req.TranscriptionID = "non-existent"
	resp = suite.makeAuthenticatedRequest("POST", "/api/v1/chat/sessions", req, true)
	assert.Equal(suite.T(), http.StatusNotFound, resp.Code)
}

func (suite *APIHandlerTestSuite) TestCreateChatSessionWithExplicitProvider() {
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	job.Status = models.StatusCompleted
	transcript := "This is a transcript."
	job.Transcript = &transcript
	suite.helper.DB.Save(job)

	ollamaBaseURL := "http://localhost:11434"
	err := suite.helper.DB.Create(&models.LLMConfig{
		Provider: "ollama",
		BaseURL:  &ollamaBaseURL,
		IsActive: false,
	}).Error
	assert.NoError(suite.T(), err)

	req := api.ChatCreateRequest{
		TranscriptionID: job.ID,
		Model:           "llama3:latest",
		Provider:        "ollama",
		Title:           "Ollama Session",
	}
	resp := suite.makeAuthenticatedRequest("POST", "/api/v1/chat/sessions", req, true)
	assert.Equal(suite.T(), http.StatusCreated, resp.Code)

	var sessionResp api.ChatSessionResponse
	err = json.Unmarshal(resp.Body.Bytes(), &sessionResp)
	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), "ollama", sessionResp.Provider)
	assert.Equal(suite.T(), "llama3:latest", sessionResp.Model)
}

func (suite *APIHandlerTestSuite) TestSendChatMessageUsesSessionProvider() {
	chatHits := 0
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_, _ = w.Write([]byte(`{"details":{"context_length":4096}}`))
		case "/api/chat":
			chatHits++
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = fmt.Fprintln(w, `{"model":"llama3:latest","message":{"role":"assistant","content":"OLLAMA"},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"model":"llama3:latest","message":{"role":"assistant","content":" response"},"done":false}`)
			_, _ = fmt.Fprintln(w, `{"model":"llama3:latest","message":{"role":"assistant","content":""},"done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollamaServer.Close()

	err := suite.helper.DB.Create(&models.LLMConfig{
		Provider: "ollama",
		BaseURL:  &ollamaServer.URL,
		IsActive: false,
	}).Error
	assert.NoError(suite.T(), err)

	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	job.Status = models.StatusCompleted
	transcript := `{"segments":[{"start":0.0,"end":1.0,"text":"Hello transcript","speaker":"SPEAKER_00"}]}`
	job.Transcript = &transcript
	suite.helper.DB.Save(job)

	session := &models.ChatSession{
		ID:              "test-chat-session-ollama-provider",
		JobID:           job.ID,
		TranscriptionID: job.ID,
		Title:           "Ollama Provider Session",
		Model:           "llama3:latest",
		Provider:        "ollama",
		MessageCount:    0,
		IsActive:        true,
	}
	err = suite.helper.DB.Create(session).Error
	assert.NoError(suite.T(), err)

	req := api.ChatMessageRequest{Content: "Hello from session provider test"}
	resp := suite.makeAuthenticatedRequest("POST", "/api/v1/chat/sessions/"+session.ID+"/messages", req, true)
	assert.Equal(suite.T(), http.StatusOK, resp.Code)
	assert.Contains(suite.T(), resp.Body.String(), "OLLAMA response")
	assert.Equal(suite.T(), 1, chatHits)
}

func (suite *APIHandlerTestSuite) TestGetChatSessions() {
	// Setup
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	job.Status = models.StatusCompleted
	transcript := "This is a transcript."
	job.Transcript = &transcript
	suite.helper.DB.Save(job)

	session1 := suite.helper.CreateTestChatSession(suite.T(), job.ID)
	// Give different IDs if helper doesn't (it does make unique IDs based on test name, but we call it twice)

	// Create second session manually to avoid ID collision from helper
	session2 := &models.ChatSession{
		ID:              session1.ID + "-2",
		JobID:           job.ID,
		TranscriptionID: job.ID,
		Title:           "Session 2",
		Model:           "gpt-4",
		Provider:        "openai",
		IsActive:        true,
	}
	suite.helper.DB.Create(session2)

	// Test list
	resp := suite.makeAuthenticatedRequest("GET", "/api/v1/chat/transcriptions/"+job.ID+"/sessions", nil, true)
	assert.Equal(suite.T(), http.StatusOK, resp.Code)

	var sessions []api.ChatSessionResponse
	err := json.Unmarshal(resp.Body.Bytes(), &sessions)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), sessions, 2)
}

func (suite *APIHandlerTestSuite) TestSendChatMessage() {
	// Setup
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	job.Status = models.StatusCompleted
	transcript := `{"segments": [{"start": 0.0, "end": 1.0, "text": "This is a transcript.", "speaker": "SPEAKER_00"}]}`
	job.Transcript = &transcript
	suite.helper.DB.Save(job)

	session := suite.helper.CreateTestChatSession(suite.T(), job.ID)

	// Test sending message
	req := api.ChatMessageRequest{
		Content: "Hello, world!",
	}
	resp := suite.makeAuthenticatedRequest("POST", "/api/v1/chat/sessions/"+session.ID+"/messages", req, true)
	assert.Equal(suite.T(), http.StatusOK, resp.Code)

	// Check headers for streaming (API implementation uses text/plain)
	assert.Contains(suite.T(), resp.Header().Get("Content-Type"), "text/plain")

	// Check response body contains streamed data (raw text)
	body := resp.Body.String()
	// Mock server returns "This is a test streaming response."
	assert.Contains(suite.T(), body, "This")
	assert.Contains(suite.T(), body, "test")

	// Verify message saved in DB (User message)
	var count int64
	suite.helper.DB.Model(&models.ChatMessage{}).Where("chat_session_id = ?", session.ID).Count(&count)
	assert.Equal(suite.T(), int64(2), count) // 1 user + 1 assistant
}

func (suite *APIHandlerTestSuite) TestUpdateChatSessionTitle() {
	// Setup
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	session := suite.helper.CreateTestChatSession(suite.T(), job.ID)

	req := map[string]string{
		"title": "New Title",
	}

	resp := suite.makeAuthenticatedRequest("PUT", "/api/v1/chat/sessions/"+session.ID+"/title", req, true)
	assert.Equal(suite.T(), http.StatusOK, resp.Code)

	var sessionResp api.ChatSessionResponse
	json.Unmarshal(resp.Body.Bytes(), &sessionResp)
	assert.Equal(suite.T(), "New Title", sessionResp.Title)

	// Verify DB update
	var updatedSession models.ChatSession
	suite.helper.DB.First(&updatedSession, "id = ?", session.ID)
	assert.Equal(suite.T(), "New Title", updatedSession.Title)
}

func (suite *APIHandlerTestSuite) TestDeleteChatSession() {
	// Setup
	job := suite.helper.CreateTestTranscriptionJob(suite.T(), "Chat Test Transcription")
	session := suite.helper.CreateTestChatSession(suite.T(), job.ID)

	resp := suite.makeAuthenticatedRequest("DELETE", "/api/v1/chat/sessions/"+session.ID, nil, true)
	assert.Equal(suite.T(), http.StatusNoContent, resp.Code)

	// Verify deletion
	var count int64
	suite.helper.DB.Model(&models.ChatSession{}).Where("id = ?", session.ID).Count(&count)
	assert.Equal(suite.T(), int64(0), count)
}
