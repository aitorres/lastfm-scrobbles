// Package main implements a simple HTTP server that returns the last scrobbling status
// of a Last.fm user. The main endpoint fetches the information from Last.fm's API, caches
// the result for a short duration, and applies rate limiting to prevent abuse.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	// cacheTTL defines how long the cached response from Last.fm is valid.
	cacheTTL = 90 * time.Second
	// rateLimit defines the maximum number of requests allowed per minute per IP address.
	rateLimit = 90
	// rateLimitWindow defines the time window for rate limiting.
	rateLimitWindow = 1 * time.Minute
)

// RateLimitBucket keeps track of the number of requests made by a client.
type RateLimitBucket struct {
	// Count is the number of requests made in the current window.
	Count int
	// ResetTime is the time when the rate limit window resets.
	ResetTime time.Time
}

// Server represents the state of the HTTP server, including its cache, rate limiting buckets,
// Last.fm credentials, and the HTTP client for outgoing API requests.
type Server struct {
	// cachedResponse stores the cached scrobbling response from Last.fm.
	cachedResponse *ScrobblingResponse
	// cachedExpiresAt is the expiration time of the cached response.
	cachedExpiresAt time.Time
	// cacheMu protects access to the cached response via a read-write mutex.
	cacheMu sync.RWMutex
	// rateLimitBuckets maps a client IP address to its corresponding [RateLimitBucket].
	rateLimitBuckets map[string]*RateLimitBucket
	// rateLimitMu protects access to the rateLimitBuckets map via a read-write mutex.
	rateLimitMu sync.RWMutex
	// username is the Last.fm username for which to fetch scrobbling information.
	username string
	// apiKey is the Last.fm API key used for authentication when making requests to the Last.fm API.
	apiKey string
	// httpClient is the HTTP client used to make requests to the Last.fm API.
	httpClient *http.Client
	// baseURL is the base URL for the Last.fm API.
	baseURL string
}

// LastFMNowPlaying represents a response from the Last.fm API for
// the "user.getrecenttracks" method, including recent tracks and any error information.
type LastFMNowPlaying struct {
	// RecentTracks contains the list of recently played tracks for the user.
	RecentTracks struct {
		// Track is a list of tracks and their information.
		Track []struct {
			// Name is the name of the track.
			Name string `json:"name"`
			// Artist contains the artist information for the track.
			Artist struct {
				// Text represents the name of the artist.
				Text string `json:"#text"`
			} `json:"artist"`
			// Album contains the album information for the track.
			Album struct {
				// Text represents the name of the album.
				Text string `json:"#text"`
			} `json:"album"`
			// NowPlaying indicates whether the track is currently being played.
			NowPlaying string `json:"@attr,omitempty"`
		} `json:"track"`
	} `json:"recenttracks"`
	// Error is the error code returned by the Last.fm API, if any.
	Error int `json:"error"`
	// Message is the error message returned by the Last.fm API, if any.
	Message string `json:"message"`
}

// ScrobblingResponse represents the response returned by the server's "/is-scrobbling" endpoint,
// indicating whether the user is currently scrobbling and providing track details if applicable.
type ScrobblingResponse struct {
	// IsScrobbling indicates whether the user is currently scrobbling a track.
	IsScrobbling bool `json:"isScrobbling"`
	// Track is the name of the currently scrobbling track, if any.
	Track string `json:"track,omitempty"`
	// Artist is the name of the artist of the currently scrobbling track, if any.
	Artist string `json:"artist,omitempty"`
	// Album is the name of the album of the currently scrobbling track, if any.
	Album string `json:"album,omitempty"`
	// Error is an optional error message returned by the server if an error
	// occurred while fetching the scrobbling status.
	Error string `json:"error,omitempty"`
}

// NewServer initializes and returns a new instance of the [Server] with default values
// and credentials fetched from environment variables, so it's ready to handle requests.
func NewServer() *Server {
	return &Server{
		rateLimitBuckets: make(map[string]*RateLimitBucket),
		username:         os.Getenv("LASTFM_USERNAME"),
		apiKey:           os.Getenv("LASTFM_API_KEY"),
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		baseURL:          "https://ws.audioscrobbler.com/2.0/",
	}
}

// checkRateLimit reports whether the client with the given IP address is allowed to make
// request, based on the current rate limit settings, and updates the rate limit bucket accordingly.
func (s *Server) checkRateLimit(clientIP string) bool {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	bucket, exists := s.rateLimitBuckets[clientIP]
	now := time.Now()

	if !exists || now.After(bucket.ResetTime) {
		s.rateLimitBuckets[clientIP] = &RateLimitBucket{
			Count:     1,
			ResetTime: now.Add(rateLimitWindow),
		}
		return true
	}

	if bucket.Count < rateLimit {
		bucket.Count++
		return true
	}

	return false
}

// getFromCache retrieves the cached response if it exists and is still valid.
func (s *Server) getFromCache() (*ScrobblingResponse, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if s.cachedResponse == nil || time.Now().After(s.cachedExpiresAt) {
		return nil, false
	}

	return s.cachedResponse, true
}

// setCache stores the response in the cache with an expiration time.
func (s *Server) setCache(response *ScrobblingResponse) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	s.cachedResponse = response
	s.cachedExpiresAt = time.Now().Add(cacheTTL)
}

// fetchNowPlayingFromLastFM makes a request to the Last.fm API to fetch the most recent track
// played by the user and returns a [ScrobblingResponse] with the response.
func (s *Server) fetchNowPlayingFromLastFM() (*ScrobblingResponse, error) {
	url := fmt.Sprintf(
		"%s?method=user.getrecenttracks&user=%s&api_key=%s&format=json&limit=1",
		s.baseURL,
		s.username,
		s.apiKey,
	)

	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from last.fm: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var lastFMData LastFMNowPlaying
	if err := json.Unmarshal(body, &lastFMData); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if lastFMData.Error != 0 {
		return nil, fmt.Errorf("last.fm error %d: %s", lastFMData.Error, lastFMData.Message)
	}

	scrobbling := &ScrobblingResponse{
		IsScrobbling: false,
	}

	if len(lastFMData.RecentTracks.Track) > 0 {
		track := lastFMData.RecentTracks.Track[0]
		if track.NowPlaying != "" {
			scrobbling.IsScrobbling = true
			scrobbling.Track = track.Name
			scrobbling.Artist = track.Artist.Text
			scrobbling.Album = track.Album.Text
		}
	}

	return scrobbling, nil
}

// handleGetIsScrobbling handles HTTP GET requests to the "/is-scrobbling" endpoint,
// which returns the current scrobbling status of the Last.fm user in JSON format.
func (s *Server) handleGetIsScrobbling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := r.RemoteAddr
	if !s.checkRateLimit(clientIP) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if cached, exists := s.getFromCache(); exists {
		json.NewEncoder(w).Encode(cached)
		return
	}

	result, err := s.fetchNowPlayingFromLastFM()
	if err != nil {
		result = &ScrobblingResponse{
			Error: err.Error(),
		}
	}

	s.setCache(result)
	json.NewEncoder(w).Encode(result)
}

// Start is the entry point for the server, which sets up the HTTP handler and
// starts listening on the specified address.
func (s *Server) Start(addr string) error {
	http.HandleFunc("/is-scrobbling", s.handleGetIsScrobbling)
	log.Printf("Server starting on %s", addr)
	return http.ListenAndServe(addr, nil)
}

// main is the entry point of the application when run as a standalone program,
// which initializes the server, ensures required environment variables are set, and starts the HTTP server.
func main() {
	server := NewServer()

	if server.username == "" || server.apiKey == "" {
		log.Fatal("LASTFM_USERNAME and LASTFM_API_KEY environment variables must be set")
	}

	if err := server.Start(":8080"); err != nil {
		log.Fatal(err)
	}
}
