package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

const (
	// webhookPath is the URL path for the webhook endpoint.
	webhookPath = "/webhook"
	// webhookKeyHeader is the HTTP header used to pass the webhook API key.
	webhookKeyHeader = "X-API-Key"
)

type NowPlayingWebhookPayload struct {
	Type string          `json:"type"`
	Play json.RawMessage `json:"play"`
}

type NowPlayingWebhookPlay struct {
	Play struct {
		Data struct {
			Track struct {
				Name string `json:"name"`
			} `json:"track"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"data"`
	} `json:"play"`
}

func logWebhookRejection(r *http.Request, reason string) {
	log.Printf("Webhook rejected: %s (remote=%s method=%s)", reason, r.RemoteAddr, r.Method)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		logWebhookRejection(r, "method not allowed")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.webhookAPIKey == "" {
		logWebhookRejection(r, "webhook is disabled")
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(webhookKeyHeader)), []byte(s.webhookAPIKey)) != 1 {
		logWebhookRejection(r, "invalid API key")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logWebhookRejection(r, "read request body: "+err.Error())
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	var payload NowPlayingWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logWebhookRejection(r, "decode request: "+err.Error())
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}
	if payload.Type != "nowPlaying" {
		log.Printf("Webhook event ignored: unsupported type %q (remote=%s method=%s)", payload.Type, r.RemoteAddr, r.Method)
		w.WriteHeader(http.StatusOK)
		return
	}
	if payload.Play == nil {
		logWebhookRejection(r, "missing play field")
		http.Error(w, "Payload must include play", http.StatusBadRequest)
		return
	}

	response := &ScrobblingResponse{}
	if !bytes.Equal(bytes.TrimSpace(payload.Play), []byte("null")) {
		var play NowPlayingWebhookPlay
		if err := json.Unmarshal(payload.Play, &play); err != nil {
			logWebhookRejection(r, "decode play data: "+err.Error())
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		track := play.Play.Data.Track.Name
		artists := play.Play.Data.Artists
		if strings.TrimSpace(track) == "" || len(artists) == 0 {
			logWebhookRejection(r, "missing track name or artists")
			http.Error(w, "Payload must include track name and at least one artist", http.StatusBadRequest)
			return
		}
		artistNames := make([]string, 0, len(artists))
		for _, artist := range artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artistNames = append(artistNames, name)
			}
		}
		if len(artistNames) == 0 {
			logWebhookRejection(r, "missing track name or artists")
			http.Error(w, "Payload must include track name and at least one artist", http.StatusBadRequest)
			return
		}
		response.IsScrobbling = true
		response.Track = track
		response.Artist = strings.Join(artistNames, ", ")
	}

	s.setWebhookCache(response)
	if response.IsScrobbling {
		log.Printf("Webhook update: now playing %q by %s", response.Track, response.Artist)
	} else {
		log.Printf("Webhook update: playback stopped")
	}
	w.WriteHeader(http.StatusOK)
}
