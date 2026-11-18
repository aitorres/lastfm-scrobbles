package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWebhookUpdatesCache(t *testing.T) {
	server := NewServer()
	server.webhookAPIKey = "webhook-secret"
	body := `{"type":"nowPlaying","play":{"play":{"data":{"track":{"name":"Song"},"artists":[{"name":"Artist"},{"name":"Guest Artist"}]}}}}`
	req := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(body))
	req.Header.Set(webhookKeyHeader, "webhook-secret")
	w := httptest.NewRecorder()

	server.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	cached, exists := server.getFromCache()
	if !exists {
		t.Fatal("Webhook response should be cached")
	}
	if !cached.IsScrobbling || cached.Track != "Song" || cached.Artist != "Artist, Guest Artist" {
		t.Errorf("Unexpected cached response: %#v", cached)
	}
	server.cacheMu.RLock()
	cacheExpiresAt := server.cachedExpiresAt
	server.cacheMu.RUnlock()
	if !cacheExpiresAt.IsZero() {
		t.Errorf("Webhook cache should not expire, got expiration %v", cacheExpiresAt)
	}
}

func TestHandleWebhookClearsCache(t *testing.T) {
	server := NewServer()
	server.webhookAPIKey = "webhook-secret"
	server.setCache(&ScrobblingResponse{IsScrobbling: true, Track: "Old Song", Artist: "Old Artist"})
	req := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"type":"nowPlaying","play":null}`))
	req.Header.Set(webhookKeyHeader, "webhook-secret")
	w := httptest.NewRecorder()

	server.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	cached, exists := server.getFromCache()
	if !exists {
		t.Fatal("Cleared response should remain cached")
	}
	if cached.IsScrobbling || cached.Track != "" || cached.Artist != "" {
		t.Errorf("Expected cleared response, got %#v", cached)
	}
}

func TestHandleWebhookValidatesRequests(t *testing.T) {
	for _, test := range []struct {
		name          string
		method        string
		configuredKey string
		key           string
		body          string
		wantStatus    int
	}{
		{name: "invalid key", method: http.MethodPost, configuredKey: "webhook-secret", key: "wrong", body: `{"type":"nowPlaying","play":null}`, wantStatus: http.StatusUnauthorized},
		{name: "missing play", method: http.MethodPost, configuredKey: "webhook-secret", key: "webhook-secret", body: `{"type":"nowPlaying"}`, wantStatus: http.StatusBadRequest},
		{name: "unsupported type", method: http.MethodPost, configuredKey: "webhook-secret", key: "webhook-secret", body: `{"type":"scrobble","play":null}`, wantStatus: http.StatusOK},
		{name: "invalid JSON", method: http.MethodPost, configuredKey: "webhook-secret", key: "webhook-secret", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "missing artist", method: http.MethodPost, configuredKey: "webhook-secret", key: "webhook-secret", body: `{"type":"nowPlaying","play":{"play":{"data":{"track":{"name":"Song"}}}}}`, wantStatus: http.StatusBadRequest},
		{name: "wrong method", method: http.MethodGet, configuredKey: "webhook-secret", key: "webhook-secret", wantStatus: http.StatusMethodNotAllowed},
		{name: "webhook disabled", method: http.MethodPost, body: `{"type":"nowPlaying","play":null}`, wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer()
			server.webhookAPIKey = test.configuredKey
			req := httptest.NewRequest(test.method, webhookPath, strings.NewReader(test.body))
			req.Header.Set(webhookKeyHeader, test.key)
			w := httptest.NewRecorder()
			server.handleWebhook(w, req)

			if w.Code != test.wantStatus {
				t.Errorf("Expected status %d, got %d", test.wantStatus, w.Code)
			}
			if _, exists := server.getFromCache(); exists {
				t.Error("Invalid or ignored webhook must not change the cache")
			}
		})
	}
}
