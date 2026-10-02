package server

import (
	"bytes"
	"errors"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/store"
	"raincast/internal/verify"
)

// routeAdmin registers the admin API. It has no authentication yet, so
// only expose it on trusted networks.
func (s *Server) routeAdmin(mux *http.ServeMux) {
	h := mux.HandleFunc
	h("GET /api/admin/overview", s.adminOverview)
	h("GET /api/admin/accuracy", s.accuracy)
	h("GET /api/admin/issues", s.adminIssues)
	h("GET /api/admin/frames", s.adminFrames)
	h("GET /api/admin/lookups", s.adminLookups)
	h("GET /api/admin/forecast", s.adminForecast)
	h("GET /api/admin/radar.png", s.radarPNG)
	h("GET /api/admin/backtest", s.backtestStatus)
}

// BacktestResponse is the body of GET /api/admin/backtest.
type BacktestResponse struct {
	Report *backtest.Report `json:"report"` // null before cmd/backtest has run
}

// backtestStatus serves the report cmd/backtest last wrote. Backtests run
// only from that command, never on request.
func (s *Server) backtestStatus(w http.ResponseWriter, r *http.Request) {
	var resp BacktestResponse
	if s.backtest != nil {
		rep, err := s.backtest()
		if err != nil {
			s.fail(w, err)
			return
		}
		resp.Report = rep
	}
	writeJSON(w, http.StatusOK, resp)
}

// Overview is the body of GET /api/admin/overview.
type Overview struct {
	ServerTime time.Time       `json:"server_time"`
	Pipeline   pipeline.Status `json:"pipeline"`
	Counts     store.Counts    `json:"counts"`
	Geocoding  geocode.Stats   `json:"geocoding"`
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.Counts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ov := Overview{
		ServerTime: time.Now().UTC(), Pipeline: s.src.Status(), Counts: c, Geocoding: s.geo.Stats(),
	}
	writeJSON(w, http.StatusOK, ov)
}

// AccuracyResponse is the body of GET /api/admin/accuracy.
type AccuracyResponse struct {
	verify.Report
	Issued      int       `json:"issued"`
	Verified    int       `json:"verified"`
	GeneratedAt time.Time `json:"generated_at"`
}

func (s *Server) accuracy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.store.VerifiedRows(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	issues, err := s.store.StationIssues(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	rain, err := s.store.StationRain(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Arrival error is computed per station (each has its own observations)
	// and then pooled.
	var arrivals []verify.ArrivalStat
	issued := 0
	for st, is := range issues {
		arrivals = append(arrivals, verify.Arrival(is, rain[st], s.stepMin, s.horizonMin))
		issued += len(is)
	}
	rep := verify.Build(rows)
	rep.Arrival = verify.Pool(arrivals)
	writeJSON(w, http.StatusOK, AccuracyResponse{
		Report: rep, Issued: issued, Verified: len(rows), GeneratedAt: time.Now().UTC(),
	})
}

func (s *Server) adminIssues(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.RecentIssues(r.Context(), limitParam(r, 50))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminFrames(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.RecentFrames(r.Context(), limitParam(r, 50))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminLookups(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Lookups(r.Context(), limitParam(r, 100))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// adminForecast runs a forecast anywhere without logging it as a user lookup.
func (s *Server) adminForecast(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotFor(w, r)
	if ok {
		writeJSON(w, http.StatusOK, snap)
	}
}

// radarPNG renders the mosaic, motion and target for lat/lon, or for the
// home location when they are omitted.
func (s *Server) radarPNG(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotFor(w, r)
	if !ok {
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, renderSnapshot(snap)); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buf.Bytes())
}

// snapshotFor forecasts the lat/lon of the request, writing the error
// response itself when it fails. There is no default location.
func (s *Server) snapshotFor(w http.ResponseWriter, r *http.Request) (*pipeline.Snapshot, bool) {
	q := r.URL.Query()
	if q.Get("lat") == "" || q.Get("lon") == "" {
		writeError(w, http.StatusBadRequest, "lat and lon are required")
		return nil, false
	}
	lat, lon, err := parseLatLon(q.Get("lat"), q.Get("lon"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	snap, err := s.src.ForecastAt(r.Context(), lat, lon)
	switch {
	case errors.Is(err, pipeline.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, "radar data is still loading; try again shortly")
		return nil, false
	case err != nil:
		s.log.Error("forecast at", "lat", lat, "lon", lon, "err", err)
		writeError(w, http.StatusBadGateway, "could not load radar data for this location")
		return nil, false
	}
	return snap, true
}

func limitParam(r *http.Request, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, 500)
}
