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

func TestFetchNowPlayingFromLastFMSuccess(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := LastFMNowPlaying{Error: 0}
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
		response := LastFMNowPlaying{Error: 0}
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
		response := LastFMNowPlaying{Error: 6, Message: "User not found"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	server := NewServer()
	server.username = "invaliduser"
	server.apiKey = "testkey"
	server.httpClient = mockServer.Client()
	server.baseURL = mockServer.URL + "/"

	if _, err := server.fetchNowPlayingFromLastFM(); err == nil {
		t.Error("Should error on last.fm error response")
	}
}

func TestFetchNowPlayingFromLastFMNetworkError(t *testing.T) {
	server := NewServer()
	server.username = "testuser"
	server.apiKey = "testkey"
	server.baseURL = "http://invalid.url.that.does.not.exist/"
	server.httpClient = &http.Client{Timeout: 100 * time.Millisecond}

	if _, err := server.fetchNowPlayingFromLastFM(); err == nil {
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

	if _, err := server.fetchNowPlayingFromLastFM(); err == nil {
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

	if _, err := server.fetchNowPlayingFromLastFM(); err == nil || err.Error() != "failed to read response: read failed" {
		t.Errorf("Expected response read error, got %v", err)
	}
}

func TestFetchNowPlayingFromLastFMRealAPIResponse(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
