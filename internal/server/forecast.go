package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"raincast/internal/pipeline"
)

// Upload limits for POST /api/forecast: 20 tiles of a few KB to ~60 KB, or
// their hashes.
const (
	maxUploadBytes = 4 << 20
	maxTileBytes   = 512 << 10
	maxSumBytes    = 128
)

// TilesNeeded is the body of a forecast answer that needs the client to
// download radar tiles (Config.ClientTiles): fetch every URL, then POST
// the hash of each tile that hashes as the plan's sum and the bytes of the
// rest.
type TilesNeeded struct {
	Plan  pipeline.TilePlan `json:"tiles_needed"`
	Error string            `json:"error,omitempty"`
}

// TilesMissing answers a POST naming tiles the server has none of: the
// client POSTs again with these tiles' bytes.
type TilesMissing struct {
	Missing []pipeline.TileID `json:"tiles_missing"`
}

// forecast serves a forecast for the lat and lon given, which are required.
//
// With Config.ClientTiles a GET is answered from the cache when h names
// tiles it has seen, then from tiles the server can get itself
// (ServerForecast), and otherwise with the tiles to download. A POST
// (multipart) names each planned tile by a part "<time>_<x>_<y>" holding
// its bytes or "sum_<time>_<x>_<y>" holding its hex SHA-256; the answer is
// the forecast, or TilesMissing when the server lacks some named only by
// hash.
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
	if snap, ok := s.src.ServerForecast(r.Context(), lat, lon); ok {
		s.serveSnapshot(w, r, snap)
		return
	}
	plan, err := s.src.ClientPlan(r.Context(), lat, lon)
	if s.forecastError(w, err, lat, lon) {
		return
	}
	writeJSON(w, http.StatusOK, TilesNeeded{Plan: plan})
}

func (s *Server) forecastFromTiles(w http.ResponseWriter, r *http.Request, lat, lon float64) {
	tiles, sums, err := readTiles(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, missing, err := s.src.ForecastFromTiles(r.Context(), lat, lon, tiles, sums)
	switch {
	case err == nil && len(missing) > 0:
		writeJSON(w, http.StatusOK, TilesMissing{Missing: missing})
	case errors.Is(err, pipeline.ErrStaleTiles):
		// A new frame arrived: hand over the new plan right away.
		plan, perr := s.src.ClientPlan(r.Context(), lat, lon)
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

// readTiles reads the multipart tiles of a POST: tile bytes by "<time>_<x>_<y>"
// and tile hashes by "sum_<time>_<x>_<y>".
func readTiles(w http.ResponseWriter, r *http.Request) (map[pipeline.TileID][]byte, map[pipeline.TileID]string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, nil, fmt.Errorf("expected multipart tiles: %v", err)
	}
	tiles := map[pipeline.TileID][]byte{}
	sums := map[pipeline.TileID]string{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return tiles, sums, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading tiles: %v", err)
		}
		name, isSum := strings.CutPrefix(part.FormName(), "sum_")
		var id pipeline.TileID
		if n, _ := fmt.Sscanf(name, "%d_%d_%d", &id.Time, &id.X, &id.Y); n != 3 {
			return nil, nil, fmt.Errorf("tile part %q is not named <time>_<x>_<y> or sum_<time>_<x>_<y>", part.FormName())
		}
		limit := maxTileBytes
		if isSum {
			limit = maxSumBytes
		}
		data, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		if err != nil {
			return nil, nil, fmt.Errorf("reading tiles: %v", err)
		}
		if len(data) > limit {
			return nil, nil, fmt.Errorf("tile part %s is too large", part.FormName())
		}
		if !isSum {
			tiles[id] = data
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(string(data)))
		if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
			return nil, nil, fmt.Errorf("tile part %s is not a hex SHA-256", part.FormName())
		}
		sums[id] = sum
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
	// A copy: the snapshot may be cached and served concurrently.
	out := *snap
	out.NextDue = s.src.NextDue(snap.FrameTime)
	if f, ok := s.src.Radar(); ok && snap.FrameTime.Before(f.Time) {
		out.Outdated = true
	}
	writeJSON(w, http.StatusOK, &out)
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
