package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetIsScrobblingCacheHit(t *testing.T) {
	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"

	// Prime cache
	cachedResponse := &ScrobblingResponse{
		IsScrobbling: true,
		Track:        "Test Track",
		Artist:       "Test Artist",
	}
	server.setCache(cachedResponse)

	req := httptest.NewRequest("GET", "/is-scrobbling", nil)
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response ScrobblingResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if response.Track != "Test Track" {
		t.Errorf("Expected cached track, got %v", response.Track)
	}
}

func TestHandleGetIsScrobblingMethodNotAllowed(t *testing.T) {
	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"

	req := httptest.NewRequest("POST", "/is-scrobbling", nil)
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHandleGetIsScrobblingOptions(t *testing.T) {
	server := NewServer()
	handler := server.withCORS(http.HandlerFunc(server.handleGetIsScrobbling))

	req := httptest.NewRequest("OPTIONS", "/is-scrobbling", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Expected Access-Control-Allow-Origin '*', got '%s'", got)
	}
}

func TestHandleGetIsScrobblingRateLimited(t *testing.T) {
	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"

	// Exhaust rate limit for test IP
	for range rateLimit {
		server.checkRateLimit("127.0.0.1")
	}

	req := httptest.NewRequest("GET", "/is-scrobbling", nil)
	req.RemoteAddr = "127.0.0.1"
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status 429, got %d", w.Code)
	}
}

func TestHandleGetIsScrobblingWebhookOnlyCacheMiss(t *testing.T) {
	server := NewServer()
	server.webhookAPIKey = "webhook-secret"
	req := httptest.NewRequest(http.MethodGet, "/is-scrobbling", nil)
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	var response ScrobblingResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if response.IsScrobbling || response.Track != "" || response.Artist != "" {
		t.Errorf("Expected inactive response, got %#v", response)
	}
	if _, exists := server.getFromCache(); exists {
		t.Error("Cache miss response should not populate cache")
	}
}

func TestHandleGetIsScrobblingSuccess(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := LastFMNowPlaying{
			Error: 0,
		}
		response.RecentTracks.Track = make([]Track, 1)

		response.RecentTracks.Track[0].Name = "Test Song"
		response.RecentTracks.Track[0].Artist.Text = "Test Artist"
		response.RecentTracks.Track[0].Attr.NowPlaying = "true"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	req := httptest.NewRequest("GET", "/is-scrobbling", nil)
	req.RemoteAddr = "192.168.1.100"
	w := httptest.NewRecorder()
	handler := server.withCORS(http.HandlerFunc(server.handleGetIsScrobbling))

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Expected Access-Control-Allow-Origin '*', got '%s'", got)
	}

	var response ScrobblingResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if !response.IsScrobbling {
		t.Error("Should indicate scrobbling")
	}

	// Verify cache was populated
	cached, exists := server.getFromCache()
	if !exists {
		t.Error("Response should be cached")
	}
	if !cached.IsScrobbling {
		t.Error("Cached response should match API response")
	}
}

func TestHandleGetIsScrobblingWithMissingTrack(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := LastFMNowPlaying{
			Error: 0,
		}
		response.RecentTracks.Track = make([]Track, 1)

		response.RecentTracks.Track[0].Name = "Test Song"
		response.RecentTracks.Track[0].Artist.Text = "Test Artist"
		// No Attr.NowPlaying attribute set

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	req := httptest.NewRequest("GET", "/is-scrobbling", nil)
	req.RemoteAddr = "192.168.1.101"
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response ScrobblingResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if response.IsScrobbling {
		t.Error("Should not indicate scrobbling when no NowPlaying attribute")
	}
}

func TestHandleGetIsScrobblingAPIError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := LastFMNowPlaying{
			Error:   6,
			Message: "User not found",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "invaliduser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	req := httptest.NewRequest("GET", "/is-scrobbling", nil)
	req.RemoteAddr = "192.168.1.102"
	w := httptest.NewRecorder()

	server.handleGetIsScrobbling(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response ScrobblingResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if response.Error == "" {
		t.Error("Should have error message when API fails")
	}
}

func TestHandleHealthSuccess(t *testing.T) {
	server := NewServer()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]string
	err := json.NewDecoder(w.Body).Decode(&response)
	if err != nil {
		t.Errorf("Failed to parse response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("Expected status 'ok', got '%s'", response["status"])
	}
}

func TestHandleHealthMethodNotAllowed(t *testing.T) {
	server := NewServer()

	req := httptest.NewRequest("POST", "/health", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}
