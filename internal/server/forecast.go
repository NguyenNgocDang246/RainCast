package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"raincast/internal/pipeline"
)

// Upload limits for POST /api/forecast: 20 tiles of a few KB to ~60 KB.
const (
	maxUploadBytes = 4 << 20
	maxTileBytes   = 512 << 10
)

// TilesNeeded is the body of a forecast answer that needs the client to
// download radar tiles (Config.ClientTiles): fetch every URL, then either
// GET again with h=ContentHash of the tiles or POST them.
type TilesNeeded struct {
	Plan  pipeline.TilePlan `json:"tiles_needed"`
	Error string            `json:"error,omitempty"`
}

// forecast serves a forecast for the lat and lon given, which are required.
//
// With Config.ClientTiles the server never downloads radar tiles: a GET is
// answered from the cache when h names tiles it has seen, and otherwise
// with the tiles to download; a POST of those tiles (multipart, one part
// per tile named "<time>_<x>_<y>") computes the forecast.
func (s *Server) forecast(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	lat, lon, ok := s.latLon(w, r)
	if !ok {
		return
	}
	s.src.EnsureFresh(r.Context())
	if !s.clientTiles {
		snap, err := s.src.ForecastAt(r.Context(), lat, lon)
		if s.forecastError(w, err, lat, lon) {
			return
		}
		s.serveSnapshot(w, r, snap)
		return
	}
	if r.Method == http.MethodPost {
		s.forecastFromTiles(w, r, lat, lon)
		return
	}
	if snap, ok := s.src.Cached(r.Context(), lat, lon, r.URL.Query().Get("h")); ok {
		s.serveSnapshot(w, r, snap)
		return
	}
	plan, err := s.src.Plan(lat, lon)
	if s.forecastError(w, err, lat, lon) {
		return
	}
	writeJSON(w, http.StatusOK, TilesNeeded{Plan: plan})
}

func (s *Server) forecastFromTiles(w http.ResponseWriter, r *http.Request, lat, lon float64) {
	tiles, err := readTiles(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := s.src.ForecastFromTiles(r.Context(), lat, lon, tiles)
	switch {
	case errors.Is(err, pipeline.ErrStaleTiles):
		// A new frame arrived: hand over the new plan right away.
		plan, perr := s.src.Plan(lat, lon)
		if s.forecastError(w, perr, lat, lon) {
			return
		}
		writeJSON(w, http.StatusConflict, TilesNeeded{Plan: plan, Error: "radar frames changed; download the new tiles"})
	case errors.Is(err, pipeline.ErrBadTiles):
		writeError(w, http.StatusBadRequest, err.Error())
	case s.forecastError(w, err, lat, lon):
	default:
		s.serveSnapshot(w, r, snap)
	}
}

// readTiles reads the multipart tiles of a POST.
func readTiles(w http.ResponseWriter, r *http.Request) (map[pipeline.TileID][]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("expected multipart tiles: %v", err)
	}
	tiles := map[pipeline.TileID][]byte{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return tiles, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading tiles: %v", err)
		}
		var id pipeline.TileID
		if n, _ := fmt.Sscanf(part.FormName(), "%d_%d_%d", &id.Time, &id.X, &id.Y); n != 3 {
			return nil, fmt.Errorf("tile part %q is not named <time>_<x>_<y>", part.FormName())
		}
		data, err := io.ReadAll(io.LimitReader(part, maxTileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("reading tiles: %v", err)
		}
		if len(data) > maxTileBytes {
			return nil, fmt.Errorf("tile %s is too large", part.FormName())
		}
		tiles[id] = data
	}
}

// forecastError writes the response for a failed forecast and reports
// whether err was one.
func (s *Server) forecastError(w http.ResponseWriter, err error, lat, lon float64) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, pipeline.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, "radar data is still loading; try again shortly")
	default:
		s.log.Error("forecast at", "lat", lat, "lon", lon, "err", err)
		writeError(w, http.StatusBadGateway, "could not load radar data for this location")
	}
	return true
}

// serveSnapshot answers with snap.
func (s *Server) serveSnapshot(w http.ResponseWriter, r *http.Request, snap *pipeline.Snapshot) {
	writeJSON(w, http.StatusOK, snap)
}

// latLon reads the required lat and lon parameters, writing the error
// response itself when they are missing or invalid.
func (s *Server) latLon(w http.ResponseWriter, r *http.Request) (float64, float64, bool) {
	q := r.URL.Query()
	if q.Get("lat") == "" || q.Get("lon") == "" {
		writeError(w, http.StatusBadRequest, "lat and lon are required")
		return 0, 0, false
	}
	lat, lon, err := parseLatLon(q.Get("lat"), q.Get("lon"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, 0, false
	}
	return lat, lon, true
}
