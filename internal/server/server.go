// Package server exposes forecasts and verification results over HTTP.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/store"
)

// Source provides forecasts: the polled home location and on demand.
type Source interface {
	Latest() *pipeline.Snapshot
	ForecastAt(ctx context.Context, lat, lon float64) (*pipeline.Snapshot, error)
	Status() pipeline.Status
}

// Geocoder turns an address, coordinates or a map link into places.
type Geocoder interface {
	Resolve(ctx context.Context, input string) ([]geocode.Place, error)
	Suggest(ctx context.Context, q string, bias *geocode.LatLon) ([]geocode.Place, error)
	Stats() geocode.Stats
}

// Config holds the server settings.
type Config struct {
	CORSOrigin string // empty disables CORS
	HorizonMin int
	// Backtest re-scores stored frames; nil disables the admin backtest.
	Backtest func(ctx context.Context) (backtest.Report, error)
	// BacktestFile keeps the latest backtest report between restarts.
	BacktestFile string
}

// Server holds the handler dependencies.
type Server struct {
	src        Source
	geo        Geocoder
	store      *store.Store
	log        *slog.Logger
	corsOrigin string
	backtest   func(ctx context.Context) (backtest.Report, error)
	btFile     string
	btRunning  sync.Mutex
	stepMin    int
	horizonMin int
}

// New returns the HTTP handler.
func New(src Source, geo Geocoder, st *store.Store, log *slog.Logger, cfg Config) http.Handler {
	s := &Server{
		src: src, geo: geo, store: st, log: log,
		corsOrigin: cfg.CORSOrigin, backtest: cfg.Backtest, btFile: cfg.BacktestFile,
		stepMin: 10, horizonMin: cfg.HorizonMin,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/forecast", s.forecast)
	mux.HandleFunc("GET /api/geocode", s.geocode)
	mux.HandleFunc("GET /api/suggest", s.suggest)
	s.routeAdmin(mux)
	return s.logging(s.cors(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": s.src.Latest() != nil})
}

// forecast serves the home snapshot, or an on-demand forecast when lat and
// lon are given. On-demand forecasts are logged for the admin page.
func (s *Server) forecast(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotFor(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("lat") != "" && s.store != nil {
		err := s.store.RecordLookup(r.Context(), store.Lookup{
			At: time.Now(), Lat: snap.Location.Lat, Lon: snap.Location.Lon, FrameTime: snap.FrameTime,
			RainingNow: snap.RainingNow, ArrivalMin: snap.ArrivalMin,
			HeavyNow: snap.HeavyNow, HeavyArrivalMin: snap.HeavyArrivalMin,
			SpeedKmh: snap.SpeedKmh, DirectionDeg: snap.DirectionDeg,
		})
		if err != nil {
			s.log.Warn("record lookup", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, snap)
}

// parseLatLon validates coordinates and rounds them to ~10 m, which is
// plenty for 1.2 km radar pixels and lets repeat requests share a row.
func parseLatLon(latS, lonS string) (lat, lon float64, err error) {
	lat, err1 := strconv.ParseFloat(latS, 64)
	lon, err2 := strconv.ParseFloat(lonS, 64)
	if err1 != nil || err2 != nil || math.Abs(lat) > 85 || math.Abs(lon) > 180 {
		return 0, 0, errors.New("lat must be within ±85 and lon within ±180")
	}
	round := func(v float64) float64 { return math.Round(v*1e4) / 1e4 }
	return round(lat), round(lon), nil
}

const maxQueryLen = 500

func (s *Server) geocode(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" || utf8.RuneCountInString(q) > maxQueryLen {
		writeError(w, http.StatusBadRequest, "q is required (max 500 characters)")
		return
	}
	places, err := s.geo.Resolve(r.Context(), q)
	switch {
	case errors.Is(err, geocode.ErrUnresolvedLink):
		writeError(w, http.StatusUnprocessableEntity, "link has no location")
	case err != nil:
		s.log.Error("geocode", "err", err)
		writeError(w, http.StatusBadGateway, "address lookup failed")
	default:
		writeJSON(w, http.StatusOK, places)
	}
}

// suggest returns places for a partial query; optional lat/lon rank nearby
// results first.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := q.Get("q")
	if utf8.RuneCountInString(text) > maxQueryLen {
		writeError(w, http.StatusBadRequest, "q is too long (max 500 characters)")
		return
	}
	var bias *geocode.LatLon
	lat, err1 := strconv.ParseFloat(q.Get("lat"), 64)
	lon, err2 := strconv.ParseFloat(q.Get("lon"), 64)
	if err1 == nil && err2 == nil && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 {
		bias = &geocode.LatLon{Lat: lat, Lon: lon}
	}
	places, err := s.geo.Suggest(r.Context(), text, bias)
	if err != nil {
		if r.Context().Err() == nil {
			s.log.Warn("suggest", "err", err)
		}
		// Suggestions are best-effort; the client keeps typing.
		places = []geocode.Place{}
	}
	writeJSON(w, http.StatusOK, places)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.corsOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
