package server

import (
	"bytes"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/store"
)

// routeAdmin registers the admin API (Config.Admin). It has no login: it
// is only served in development, on the collector's machine.
func (s *Server) routeAdmin(r *gin.Engine) {
	g := r.Group("/api/admin")
	g.GET("/overview", s.adminOverview)
	g.GET("/frames", s.adminFrames)
	g.GET("/forecast", s.adminForecast)
	g.GET("/radar.png", s.radarPNG)
	g.GET("/backtest", s.backtestStatus)
}

// BacktestResponse is the body of GET /api/admin/backtest.
type BacktestResponse struct {
	Report *backtest.Report `json:"report"` // null before cmd/backtest has run
}

// backtestStatus serves the report cmd/backtest last wrote. Backtests run
// only from that command, never on request.
//
//	@Summary		Backtest report
//	@Description	Development only (served when the server runs with Config.Admin).
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	BacktestResponse
//	@Failure		500	{object}	errorResponse
//	@Router			/api/admin/backtest [get]
func (s *Server) backtestStatus(c *gin.Context) {
	var resp BacktestResponse
	if s.backtest != nil {
		rep, err := s.backtest()
		if err != nil {
			s.fail(c, err)
			return
		}
		resp.Report = rep
	}
	writeJSON(c, http.StatusOK, resp)
}

// Overview is the body of GET /api/admin/overview.
type Overview struct {
	ServerTime time.Time       `json:"server_time"`
	Pipeline   pipeline.Status `json:"pipeline"`
	Counts     *store.Counts   `json:"counts"` // null without a database
	Geocoding  geocode.Stats   `json:"geocoding"`
}

// adminOverview reports the pipeline, database and geocoding state.
//
//	@Summary		Server overview
//	@Description	Development only (served when the server runs with Config.Admin).
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	Overview
//	@Failure		500	{object}	errorResponse
//	@Router			/api/admin/overview [get]
func (s *Server) adminOverview(c *gin.Context) {
	ctx := c.Request.Context()
	s.src.EnsureFresh(ctx)
	ov := Overview{ServerTime: time.Now().UTC(), Pipeline: s.src.Status(), Geocoding: s.geo.Stats()}
	if s.store != nil {
		n, err := s.store.Counts(ctx)
		if err != nil {
			s.fail(c, err)
			return
		}
		ov.Counts = &n
	}
	writeJSON(c, http.StatusOK, ov)
}

// adminFrames lists the frames cmd/collect recorded, newest first.
//
//	@Summary		Recorded radar frames
//	@Description	Development only (served when the server runs with Config.Admin).
//	@Tags			admin
//	@Produce		json
//	@Param			limit	query		int	false	"Frames to list (default 50, max 500)"
//	@Success		200		{array}		store.FrameRef
//	@Failure		500		{object}	errorResponse
//	@Router			/api/admin/frames [get]
func (s *Server) adminFrames(c *gin.Context) {
	out := []store.FrameRef{}
	if s.store != nil {
		var err error
		if out, err = s.store.RecentFrames(c.Request.Context(), limitParam(c, 50)); err != nil {
			s.fail(c, err)
			return
		}
	}
	writeJSON(c, http.StatusOK, out)
}

// adminForecast runs a forecast anywhere.
//
//	@Summary		Forecast with server-side tiles
//	@Description	Development only (served when the server runs with Config.Admin).
//	@Tags			admin
//	@Produce		json
//	@Param			lat	query		number	true	"Latitude (±85)"
//	@Param			lon	query		number	true	"Longitude (±180)"
//	@Success		200	{object}	pipeline.Snapshot
//	@Failure		400	{object}	errorResponse
//	@Failure		502	{object}	errorResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/admin/forecast [get]
func (s *Server) adminForecast(c *gin.Context) {
	snap, ok := s.snapshotFor(c)
	if ok {
		writeJSON(c, http.StatusOK, snap)
	}
}

// radarPNG renders the mosaic, motion and target for lat/lon.
//
//	@Summary		Radar mosaic image for a point
//	@Description	Development only (served when the server runs with Config.Admin).
//	@Tags			admin
//	@Produce		png
//	@Param			lat	query		number	true	"Latitude (±85)"
//	@Param			lon	query		number	true	"Longitude (±180)"
//	@Success		200	{file}		binary
//	@Failure		400	{object}	errorResponse
//	@Router			/api/admin/radar.png [get]
func (s *Server) radarPNG(c *gin.Context) {
	snap, ok := s.snapshotFor(c)
	if !ok {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, renderSnapshot(snap)); err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "image/png", buf.Bytes())
}

// snapshotFor forecasts the lat/lon of the request, downloading tiles on
// the server, and writes the error response itself when it fails. There
// is no default location.
func (s *Server) snapshotFor(c *gin.Context) (*pipeline.Snapshot, bool) {
	lat, lon, ok := s.latLon(c)
	if !ok {
		return nil, false
	}
	ctx := c.Request.Context()
	s.src.EnsureFresh(ctx)
	snap, err := s.src.ForecastAt(ctx, lat, lon)
	if s.forecastError(c, err, lat, lon) {
		return nil, false
	}
	return snap, true
}

func limitParam(c *gin.Context, def int) int {
	n, err := strconv.Atoi(c.Query("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, 500)
}
