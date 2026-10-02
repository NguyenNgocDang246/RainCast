package server

import (
	"bytes"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/store"
)

// routeAdmin registers the admin API (Config.Admin). It has no login: it
// is only served in development, on the collector's machine.
func (s *Server) routeAdmin(mux *http.ServeMux) {
	h := mux.HandleFunc
	h("GET /api/admin/overview", s.adminOverview)
	h("GET /api/admin/frames", s.adminFrames)
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
	Counts     *store.Counts   `json:"counts"` // null without a database
	Geocoding  geocode.Stats   `json:"geocoding"`
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	s.src.EnsureFresh(r.Context())
	ov := Overview{ServerTime: time.Now().UTC(), Pipeline: s.src.Status(), Geocoding: s.geo.Stats()}
	if s.store != nil {
		c, err := s.store.Counts(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		ov.Counts = &c
	}
	writeJSON(w, http.StatusOK, ov)
}

// adminFrames lists the frames cmd/collect recorded, newest first.
func (s *Server) adminFrames(w http.ResponseWriter, r *http.Request) {
	out := []store.FrameRef{}
	if s.store != nil {
		var err error
		if out, err = s.store.RecentFrames(r.Context(), limitParam(r, 50)); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// adminForecast runs a forecast anywhere.
func (s *Server) adminForecast(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotFor(w, r)
	if ok {
		writeJSON(w, http.StatusOK, snap)
	}
}

// radarPNG renders the mosaic, motion and target for lat/lon.
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

// snapshotFor forecasts the lat/lon of the request, downloading tiles on
// the server, and writes the error response itself when it fails. There
// is no default location.
func (s *Server) snapshotFor(w http.ResponseWriter, r *http.Request) (*pipeline.Snapshot, bool) {
	lat, lon, ok := s.latLon(w, r)
	if !ok {
		return nil, false
	}
	s.src.EnsureFresh(r.Context())
	snap, err := s.src.ForecastAt(r.Context(), lat, lon)
	if s.forecastError(w, err, lat, lon) {
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
