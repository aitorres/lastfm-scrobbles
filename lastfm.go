package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"
)

// TrackAttr represents the attributes of a track, such as whether it's currently playing.
type TrackAttr struct {
	// NowPlaying indicates whether the track is currently being played.
	NowPlaying string `json:"nowplaying,omitempty"`
}

// TrackArtist represents information about a track's artist.
type TrackArtist struct {
	// Text represents the name of the artist.
	Text string `json:"#text"`
}

// Track represents a single track from the Last.fm API.
type Track struct {
	// Name is the name of the track.
	Name string `json:"name"`
	// Artist contains the artist information for the track.
	Artist TrackArtist `json:"artist"`
	// Attr contains track attributes, such as whether it's currently playing.
	Attr TrackAttr `json:"@attr,omitempty"`
}

// LastFMNowPlaying represents a response from the Last.fm API for
// the "user.getrecenttracks" method, including recent tracks and any error information.
type LastFMNowPlaying struct {
	// RecentTracks contains the list of recently played tracks for the user.
	RecentTracks struct {
		// Track is a list of tracks and their information.
		Track []Track `json:"track"`
	} `json:"recenttracks"`
	// Error is the error code returned by the Last.fm API, if any.
	Error int `json:"error"`
	// Message is the error message returned by the Last.fm API, if any.
	Message string `json:"message"`
}

func (s *Server) refreshCache() {
	response, err := s.fetchNowPlayingFromLastFM()
	if err != nil {
		log.Printf("Failed to refresh Last.fm cache: %v", err)
		return
	}

	s.setCache(response)
}

func (s *Server) refreshCachePeriodically() {
	s.refreshCache()

	ticker := time.NewTicker(cacheTTL * 2 / 3)
	defer ticker.Stop()

	for range ticker.C {
		s.refreshCache()
	}
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
		if track.Attr.NowPlaying != "" {
			scrobbling.IsScrobbling = true
			scrobbling.Track = track.Name
			scrobbling.Artist = track.Artist.Text
		}
	}

	return scrobbling, nil
}
