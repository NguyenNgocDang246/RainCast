package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

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
//
//	@Summary		Rain forecast for a point
//	@Description	Answers with a Snapshot. When the server runs with client tiles, a GET may instead answer
//	@Description	{"tiles_needed": TilePlan}: download every tile URL, then POST the same query as multipart,
//	@Description	one part per tile named <time>_<x>_<y> (PNG bytes) or sum_<time>_<x>_<y> (hex SHA-256 when it
//	@Description	matches the plan's sum). A POST answers the Snapshot, {"tiles_missing": [...]} (POST those
//	@Description	tiles' bytes), or 409 with a new plan when a newer radar frame arrived.
//	@Tags			forecast
//	@Accept			multipart/form-data
//	@Produce		json
//	@Param			lat	query		number	true	"Latitude (±85)"
//	@Param			lon	query		number	true	"Longitude (±180)"
//	@Param			h	query		string	false	"Content hash of tiles the client has (GET, cached forecast)"
//	@Success		200	{object}	pipeline.Snapshot
//	@Failure		400	{object}	errorResponse
//	@Failure		409	{object}	TilesNeeded	"Radar frames changed; download the new tiles"
//	@Failure		502	{object}	errorResponse
//	@Failure		503	{object}	errorResponse	"Radar data is still loading"
//	@Router			/api/forecast [get]
//	@Router			/api/forecast [post]
func (s *Server) forecast(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	lat, lon, ok := s.latLon(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	s.src.EnsureFresh(ctx)
	if !s.clientTiles {
		snap, err := s.src.ForecastAt(ctx, lat, lon)
		if s.forecastError(c, err, lat, lon) {
			return
		}
		s.serveSnapshot(c, snap)
		return
	}
	if c.Request.Method == http.MethodPost {
		s.forecastFromTiles(c, lat, lon)
		return
	}
	if snap, ok := s.src.Cached(ctx, lat, lon, c.Query("h")); ok {
		s.serveSnapshot(c, snap)
		return
	}
	if snap, ok := s.src.ServerForecast(ctx, lat, lon); ok {
		s.serveSnapshot(c, snap)
		return
	}
	plan, err := s.src.ClientPlan(ctx, lat, lon)
	if s.forecastError(c, err, lat, lon) {
		return
	}
	writeJSON(c, http.StatusOK, TilesNeeded{Plan: plan})
}

func (s *Server) forecastFromTiles(c *gin.Context, lat, lon float64) {
	tiles, sums, err := readTiles(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx := c.Request.Context()
	snap, missing, err := s.src.ForecastFromTiles(ctx, lat, lon, tiles, sums)
	switch {
	case err == nil && len(missing) > 0:
		writeJSON(c, http.StatusOK, TilesMissing{Missing: missing})
	case errors.Is(err, pipeline.ErrStaleTiles):
		// A new frame arrived: hand over the new plan right away.
		plan, perr := s.src.ClientPlan(ctx, lat, lon)
		if s.forecastError(c, perr, lat, lon) {
			return
		}
		writeJSON(c, http.StatusConflict, TilesNeeded{Plan: plan, Error: "radar frames changed; download the new tiles"})
	case errors.Is(err, pipeline.ErrBadTiles):
		writeError(c, http.StatusBadRequest, err.Error())
	case s.forecastError(c, err, lat, lon):
	default:
		s.serveSnapshot(c, snap)
	}
}

// readTiles reads the multipart tiles of a POST: tile bytes by "<time>_<x>_<y>"
// and tile hashes by "sum_<time>_<x>_<y>".
func readTiles(c *gin.Context) (map[pipeline.TileID][]byte, map[pipeline.TileID]string, error) {
	r := c.Request
	r.Body = http.MaxBytesReader(c.Writer, r.Body, maxUploadBytes)
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
func (s *Server) forecastError(c *gin.Context, err error, lat, lon float64) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, pipeline.ErrNotReady):
		writeError(c, http.StatusServiceUnavailable, "radar data is still loading; try again shortly")
	default:
		s.log.Error("forecast at", "lat", lat, "lon", lon, "err", err)
		writeError(c, http.StatusBadGateway, "could not load radar data for this location")
	}
	return true
}

// serveSnapshot answers with snap.
func (s *Server) serveSnapshot(c *gin.Context, snap *pipeline.Snapshot) {
	// A copy: the snapshot may be cached and served concurrently.
	out := *snap
	out.NextDue = s.src.NextDue(snap.FrameTime)
	if f, ok := s.src.Radar(); ok && snap.FrameTime.Before(f.Time) {
		out.Outdated = true
	}
	writeJSON(c, http.StatusOK, &out)
}

// latLon reads the required lat and lon parameters, writing the error
// response itself when they are missing or invalid.
func (s *Server) latLon(c *gin.Context) (float64, float64, bool) {
	latS, lonS := c.Query("lat"), c.Query("lon")
	if latS == "" || lonS == "" {
		writeError(c, http.StatusBadRequest, "lat and lon are required")
		return 0, 0, false
	}
	lat, lon, err := parseLatLon(latS, lonS)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return 0, 0, false
	}
	return lat, lon, true
}
