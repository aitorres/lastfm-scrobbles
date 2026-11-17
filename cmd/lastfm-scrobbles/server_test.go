package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	server := NewServer()
	clientIP := "192.168.1.1"

	for i := range rateLimit {
		if !server.checkRateLimit(clientIP) {
			t.Errorf("Request %d should pass rate limit check", i+1)
		}
	}
	if server.checkRateLimit(clientIP) {
		t.Error("Request after limit should fail rate limit check")
	}
}

func TestRateLimitDifferentIPs(t *testing.T) {
	server := NewServer()
	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	for range rateLimit {
		server.checkRateLimit(ip1)
	}
	if server.checkRateLimit(ip1) {
		t.Error("IP1 should be rate limited")
	}
	if !server.checkRateLimit(ip2) {
		t.Error("IP2 should not be rate limited")
	}
}

func TestCacheSetGet(t *testing.T) {
	server := NewServer()
	response := &ScrobblingResponse{IsScrobbling: true, Track: "Test Track"}
	server.setCache(response)

	cached, exists := server.getFromCache()
	if !exists {
		t.Error("Cache entry should exist")
	}
	if cached.Track != response.Track {
		t.Errorf("Expected %v, got %v", response.Track, cached.Track)
	}
}

func TestCacheExpiration(t *testing.T) {
	server := NewServer()
	server.setCache(&ScrobblingResponse{IsScrobbling: true, Track: "Test Song"})

	if _, exists := server.getFromCache(); !exists {
		t.Error("Cache entry should exist immediately")
	}
	server.cacheMu.Lock()
	server.cachedExpiresAt = time.Now().Add(-1 * time.Second)
	server.cacheMu.Unlock()
	if _, exists := server.getFromCache(); exists {
		t.Error("Cache entry should have expired")
	}
}

func TestScrobblingResponseJSON(t *testing.T) {
	response := ScrobblingResponse{IsScrobbling: true, Track: "Song", Artist: "Artist"}
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

func TestStartRejectsMissingSourceConfiguration(t *testing.T) {
	server := &Server{}
	if err := server.Start(":0"); err == nil {
		t.Fatal("Start should reject missing source configuration")
	}
}

func TestValidateConfig(t *testing.T) {
	for _, test := range []struct {
		name       string
		username   string
		lastFMKey  string
		webhookKey string
		wantValid  bool
	}{
		{name: "no source", wantValid: false},
		{name: "partial Last.fm config", username: "user", wantValid: false},
		{name: "Last.fm only", username: "user", lastFMKey: "lastfm-key", wantValid: true},
		{name: "webhook only", webhookKey: "webhook-key", wantValid: true},
		{name: "both", username: "user", lastFMKey: "lastfm-key", webhookKey: "webhook-key", wantValid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{username: test.username, apiKey: test.lastFMKey, webhookAPIKey: test.webhookKey}
			err := server.validateConfig()
			if (err == nil) != test.wantValid {
				t.Errorf("validateConfig() error = %v, want valid %t", err, test.wantValid)
			}
		})
	}
}

func TestCacheNotFound(t *testing.T) {
	server := NewServer()
	if _, exists := server.getFromCache(); exists {
		t.Error("Cache entry should not exist for new server")
	}
}
