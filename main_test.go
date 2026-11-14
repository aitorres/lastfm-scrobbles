package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingReadCloser) Close() error {
	return nil
}

func TestRateLimit(t *testing.T) {
	server := NewServer()
	clientIP := "192.168.1.1"

	// First rateLimit requests should succeed
	for i := range rateLimit {
		if !server.checkRateLimit(clientIP) {
			t.Errorf("Request %d should pass rate limit check", i+1)
		}
	}

	// Next request should fail
	if server.checkRateLimit(clientIP) {
		t.Error("Request after limit should fail rate limit check")
	}
}

func TestRateLimitDifferentIPs(t *testing.T) {
	server := NewServer()
	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Fill rate limit for IP1
	for range rateLimit {
		server.checkRateLimit(ip1)
	}

	// IP1 should be limited
	if server.checkRateLimit(ip1) {
		t.Error("IP1 should be rate limited")
	}

	// IP2 should still have requests available
	if !server.checkRateLimit(ip2) {
		t.Error("IP2 should not be rate limited")
	}
}

func TestCacheSetGet(t *testing.T) {
	server := NewServer()
	response := &ScrobblingResponse{
		IsScrobbling: true,
		Track:        "Test Track",
	}

	server.setCache(response)

	cached, exists := server.getFromCache()
	if !exists {
		t.Error("Cache entry should exist")
	}

	if cached.Track != response.Track {
		t.Errorf("Expected %v, got %v", response.Track, cached.Track)
	}
}

func TestRefreshCache(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"recenttracks":{"track":[{"name":"Fresh Track","artist":{"#text":"Fresh Artist"},"album":{"#text":"Fresh Album"},"@attr":{"nowplaying":"true"}}]}}`))
	}))
	defer mockServer.Close()

	server := NewServer()
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL
	server.refreshCache()

	cached, exists := server.getFromCache()
	if !exists {
		t.Fatal("Refreshed response should be cached")
	}
	if cached.Track != "Fresh Track" {
		t.Errorf("Expected fresh track, got %q", cached.Track)
	}

	server.cacheMu.RLock()
	expiresAt := server.cachedExpiresAt
	server.cacheMu.RUnlock()
	if time.Until(expiresAt) <= 0 {
		t.Error("Refreshed response should have a future expiration")
	}
}

func TestRefreshCacheDoesNotReplaceCacheOnFetchError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":6,"message":"User not found"}`))
	}))
	defer mockServer.Close()

	server := NewServer()
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL
	previous := &ScrobblingResponse{IsScrobbling: true, Track: "Cached Track"}
	server.setCache(previous)

	server.refreshCache()

	cached, exists := server.getFromCache()
	if !exists {
		t.Fatal("Existing cache entry should remain available after refresh failure")
	}
	if cached != previous {
		t.Errorf("Refresh failure replaced cached response: got %#v, want %#v", cached, previous)
	}
}

func TestCacheExpiration(t *testing.T) {
	server := NewServer()
	response := &ScrobblingResponse{
		IsScrobbling: true,
		Track:        "Test Song",
	}

	server.setCache(response)

	// Entry should exist immediately
	_, exists := server.getFromCache()
	if !exists {
		t.Error("Cache entry should exist")
	}

	// Manually expire the entry by setting past time
	server.cacheMu.Lock()
	server.cachedExpiresAt = time.Now().Add(-1 * time.Second)
	server.cacheMu.Unlock()

	_, exists = server.getFromCache()
	if exists {
		t.Error("Cache entry should have expired")
	}
}

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

func TestScrobblingResponseJSON(t *testing.T) {
	response := ScrobblingResponse{
		IsScrobbling: true,
		Track:        "Song",
		Artist:       "Artist",
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Errorf("Failed to marshal response: %v", err)
	}

	var decoded ScrobblingResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	if decoded.IsScrobbling != response.IsScrobbling {
		t.Error("IsScrobbling field mismatch")
	}
	if decoded.Track != response.Track {
		t.Error("Track field mismatch")
	}
}

func TestNewServer(t *testing.T) {
	server := NewServer()

	if server.rateLimitBuckets == nil {
		t.Error("Rate limit buckets should be initialized")
	}
	if server.httpClient == nil {
		t.Error("HTTP client should be initialized")
	}
}

func TestCacheNotFound(t *testing.T) {
	server := NewServer()

	_, exists := server.getFromCache()
	if exists {
		t.Error("Cache entry should not exist for new server")
	}
}

func TestFetchNowPlayingFromLastFMSuccess(t *testing.T) {
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

	result, err := server.fetchNowPlayingFromLastFM()
	if err != nil {
		t.Errorf("Should not error on valid response: %v", err)
	}

	if !result.IsScrobbling {
		t.Error("Should indicate user is scrobbling")
	}
	if result.Track != "Test Song" {
		t.Errorf("Expected track 'Test Song', got %s", result.Track)
	}
}

func TestFetchNowPlayingFromLastFMNoTrack(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := LastFMNowPlaying{
			Error: 0,
		}
		response.RecentTracks.Track = make([]Track, 0)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	result, err := server.fetchNowPlayingFromLastFM()
	if err != nil {
		t.Errorf("Should not error on valid response: %v", err)
	}

	if result.IsScrobbling {
		t.Error("Should not indicate scrobbling when no tracks")
	}
}

func TestFetchNowPlayingFromLastFMError(t *testing.T) {
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

	_, err := server.fetchNowPlayingFromLastFM()
	if err == nil {
		t.Error("Should error on last.fm error response")
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

func TestFetchNowPlayingFromLastFMNetworkError(t *testing.T) {
	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.baseURL = "http://invalid.url.that.does.not.exist/"
	server.httpClient = &http.Client{Timeout: 100 * time.Millisecond}

	_, err := server.fetchNowPlayingFromLastFM()
	if err == nil {
		t.Error("Should error on network failure")
	}
}

func TestFetchNowPlayingFromLastFMInvalidJSON(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json {"))
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	_, err := server.fetchNowPlayingFromLastFM()
	if err == nil {
		t.Error("Should error on invalid JSON")
	}
}

func TestFetchNowPlayingFromLastFMReadError(t *testing.T) {
	server := NewServer()
	server.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       failingReadCloser{},
			}, nil
		}),
	}

	_, err := server.fetchNowPlayingFromLastFM()
	if err == nil || err.Error() != "failed to read response: read failed" {
		t.Errorf("Expected response read error, got %v", err)
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

func TestFetchNowPlayingFromLastFMRealAPIResponse(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Real API response format with @attr as an object
		response := `{
  "recenttracks": {
    "track": [
      {
        "artist": {
          "mbid": "",
          "#text": "brakence"
        },
        "streamable": "0",
        "image": [
          {
            "size": "small",
            "#text": "https://lastfm-img.freetls.fastly.net/i/u/34s/0bf6f387e9621cb3c61a4fddefb0e449.jpg"
          }
        ],
        "mbid": "4e4ba77a-a695-49ec-8599-e48d2806627e",
        "album": {
          "mbid": "",
          "#text": "Rosier"
        },
        "name": "rosier/punk2",
        "@attr": {
          "nowplaying": "true"
        },
        "url": "https://www.last.fm/music/brakence/_/rosier%2Fpunk2"
      },
      {
        "artist": {
          "mbid": "aefba883-dfc2-45ee-bce4-1dd903b13341",
          "#text": "Dorian Electra"
        },
        "streamable": "0",
        "image": [
          {
            "size": "extralarge",
            "#text": "https://lastfm-img.freetls.fastly.net/i/u/300x300/93548b4bdfe9c13ab8604ee53c75c159.jpg"
          }
        ],
        "mbid": "a3c4af46-9231-44f6-a485-6eb2aa49f66b",
        "album": {
          "mbid": "ff30605f-41a4-48f2-ae25-05c769fd7906",
          "#text": "My Agenda"
        },
        "name": "Gentleman",
        "url": "https://www.last.fm/music/Dorian+Electra/_/Gentleman",
        "date": {
          "uts": "1786218145",
          "#text": "08 Aug 2026, 19:42"
        }
      }
    ],
    "@attr": {
      "user": "andresitorresm",
      "totalPages": "116148",
      "page": "1",
      "perPage": "1",
      "total": "116148"
    }
  }
}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	result, err := server.fetchNowPlayingFromLastFM()
	if err != nil {
		t.Fatalf("Should not error on real API response: %v", err)
	}

	if !result.IsScrobbling {
		t.Error("Should indicate user is scrobbling (first track has @attr.nowplaying)")
	}
	if result.Track != "rosier/punk2" {
		t.Errorf("Expected track 'rosier/punk2', got %s", result.Track)
	}
	if result.Artist != "brakence" {
		t.Errorf("Expected artist 'brakence', got %s", result.Artist)
	}
}
