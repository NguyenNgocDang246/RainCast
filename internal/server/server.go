// Package server exposes forecasts and verification results over HTTP.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	"raincast/internal/store"
)

// Source provides forecasts on demand.
type Source interface {
	Ready() bool
	// EnsureFresh reloads a stale frame index (serverless hosts have no
	// background poller).
	EnsureFresh(ctx context.Context)
	ForecastAt(ctx context.Context, lat, lon float64) (*pipeline.Snapshot, error)
	// ClientPlan, Cached and ForecastFromTiles serve forecasts from tiles
	// the client downloads (Config.ClientTiles); ServerForecast first tries
	// the tiles the server has kept or can download within its budget.
	ClientPlan(ctx context.Context, lat, lon float64) (pipeline.TilePlan, error)
	Cached(ctx context.Context, lat, lon float64, hash string) (*pipeline.Snapshot, bool)
	ServerForecast(ctx context.Context, lat, lon float64) (*pipeline.Snapshot, bool)
	ForecastFromTiles(ctx context.Context, lat, lon float64, tiles map[pipeline.TileID][]byte, sums map[pipeline.TileID]string) (*pipeline.Snapshot, []pipeline.TileID, error)
	Status() pipeline.Status
	Radar() (pipeline.RadarFrame, bool)
	// NextDue is when the radar frame after the one at t is expected.
	NextDue(t time.Time) time.Time
}

// Geocoder turns an address, coordinates or a map link into places, names
// points picked on the map and serves the map's tiles.
type Geocoder interface {
	// country is the user's ISO code ("" when unknown); its places rank first.
	Resolve(ctx context.Context, input, country string) ([]geocode.Place, error)
	Suggest(ctx context.Context, q string, bias *geocode.LatLon, country string) ([]geocode.Place, error)
	Reverse(ctx context.Context, lat, lon float64) (geocode.Place, error)
	Tile(ctx context.Context, z, x, y int) ([]byte, error)
	Stats() geocode.Stats
}

// Config holds the server settings.
type Config struct {
	CORSOrigin string // empty disables CORS
	// Admin serves the admin API (/api/admin/*), which has no login: only
	// in development.
	Admin bool
	// Backtest reads the report cmd/backtest last wrote (nil when none);
	// nil serves no report.
	Backtest func() (*backtest.Report, error)
	// CountryOf names a client IP's country ("vn"), "" when unknown; nil
	// knows none. Searches favor the user's country.
	CountryOf func(netip.Addr) string
	// CountryHeader, when set, is a request header naming the user's
	// country that the host fills in (Vercel: X-Vercel-IP-Country). It is
	// used before CountryOf.
	CountryHeader string
	// RealIPHeader, when set, is a header the host's proxy sets to the
	// client IP (Vercel: X-Real-IP), used instead of X-Forwarded-For.
	RealIPHeader string
	// ClientTiles makes browsers download the radar tiles of their own
	// forecasts (see forecast), so the server stays within RainViewer's
	// per-IP limit on hosts with shared outbound IPs.
	ClientTiles bool
}

// Server holds the handler dependencies.
type Server struct {
	src         Source
	geo         Geocoder
	store       *store.Store
	log         *slog.Logger
	corsOrigin  string
	backtest    func() (*backtest.Report, error)
	countryOf   func(netip.Addr) string
	countryHdr  string
	realIPHdr   string
	clientTiles bool
}

// New returns the HTTP handler.
func New(src Source, geo Geocoder, st *store.Store, log *slog.Logger, cfg Config) http.Handler {
	s := &Server{
		src: src, geo: geo, store: st, log: log,
		corsOrigin: cfg.CORSOrigin, backtest: cfg.Backtest, countryOf: cfg.CountryOf,
		countryHdr: cfg.CountryHeader, realIPHdr: cfg.RealIPHeader, clientTiles: cfg.ClientTiles,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/forecast", s.forecast)
	mux.HandleFunc("POST /api/forecast", s.forecast)
	mux.HandleFunc("GET /api/geocode", s.geocode)
	mux.HandleFunc("GET /api/suggest", s.suggest)
	mux.HandleFunc("GET /api/reverse", s.reverse)
	mux.HandleFunc("GET /api/tiles/{z}/{x}/{y}", s.tile)
	mux.HandleFunc("GET /api/radar", s.radar)
	if cfg.Admin {
		s.routeAdmin(mux)
	}
	return s.logging(s.cors(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.src.EnsureFresh(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": s.src.Ready()})
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
	places, err := s.geo.Resolve(r.Context(), q, s.country(r))
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
	places, err := s.geo.Suggest(r.Context(), text, bias, s.country(r))
	if err != nil {
		if r.Context().Err() == nil {
			s.log.Warn("suggest", "err", err)
		}
		// Suggestions are best-effort; the client keeps typing.
		places = []geocode.Place{}
	}
	writeJSON(w, http.StatusOK, places)
}

// country is the requesting user's country, "" when unknown.
func (s *Server) country(r *http.Request) string {
	if s.countryHdr != "" {
		if c := r.Header.Get(s.countryHdr); len(c) == 2 {
			return strings.ToLower(c)
		}
	}
	if s.countryOf == nil {
		return ""
	}
	return s.countryOf(s.clientIP(r))
}

// clientIP is the request's origin, from the host's real-IP header when
// one is configured.
func (s *Server) clientIP(r *http.Request) netip.Addr {
	if s.realIPHdr != "" {
		if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(s.realIPHdr))); err == nil {
			return ip.Unmap()
		}
	}
	return clientIP(r)
}

// clientIP is the request's origin. Behind the reverse proxy (a private or
// loopback peer) it is the last X-Forwarded-For entry, which the proxy
// appends; a direct client's header is ignored, so it cannot claim another IP.
func clientIP(r *http.Request) netip.Addr {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	ip := peer.Addr().Unmap()
	if !ip.IsLoopback() && !ip.IsPrivate() {
		return ip
	}
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if xff == "" {
		return ip
	}
	last := xff[strings.LastIndex(xff, ",")+1:]
	if fwd, err := netip.ParseAddr(strings.TrimSpace(last)); err == nil {
		return fwd.Unmap()
	}
	return ip
}

// reverse names a point picked on the map. A failed lookup still answers
// with the point, named by its coordinates.
func (s *Server) reverse(w http.ResponseWriter, r *http.Request) {
	lat, lon, err := parseLatLon(r.URL.Query().Get("lat"), r.URL.Query().Get("lon"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.geo.Reverse(r.Context(), lat, lon)
	if err != nil && r.Context().Err() == nil && !errors.Is(err, geocode.ErrNoKey) {
		s.log.Warn("reverse", "err", err)
	}
	if err == nil {
		// The name depends only on the point, so a CDN may keep it.
		w.Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400")
	}
	writeJSON(w, http.StatusOK, p)
}

// tile proxies a map tile so the Geoapify key stays on the server.
func (s *Server) tile(w http.ResponseWriter, r *http.Request) {
	z, err1 := strconv.Atoi(r.PathValue("z"))
	x, err2 := strconv.Atoi(r.PathValue("x"))
	y, err3 := strconv.Atoi(strings.TrimSuffix(r.PathValue("y"), ".png"))
	if err1 != nil || err2 != nil || err3 != nil {
		writeError(w, http.StatusBadRequest, "bad tile")
		return
	}
	b, err := s.geo.Tile(r.Context(), z, x, y)
	switch {
	case errors.Is(err, geocode.ErrBadTile):
		writeError(w, http.StatusBadRequest, "bad tile")
	case errors.Is(err, geocode.ErrNoKey):
		// Logged once at startup.
		writeError(w, http.StatusServiceUnavailable, "map tiles are not configured")
	case err != nil:
		if r.Context().Err() == nil {
			s.log.Warn("tile", "err", err)
		}
		writeError(w, http.StatusBadGateway, "map tile unavailable")
	default:
		w.Header().Set("Content-Type", "image/png")
		// s-maxage lets a CDN (Vercel's) keep tiles for every visitor.
		w.Header().Set("Cache-Control", "public, max-age=2592000, s-maxage=2592000")
		w.Write(b)
	}
}

// radar names the newest radar frame for the map. Browsers fetch its tiles
// from RainViewer themselves, so map views don't spend the server's quota.
func (s *Server) radar(w http.ResponseWriter, r *http.Request) {
	s.src.EnsureFresh(r.Context())
	f, ok := s.src.Radar()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "radar data is still loading; try again shortly")
		return
	}
	age := radarMaxAge(f.NextDue, time.Now())
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, s-maxage=%d, stale-while-revalidate=%d", age, age, age))
	writeJSON(w, http.StatusOK, f)
}

// radarMaxAge is how many seconds a radar answer may be cached: up to a
// minute, but not past when the next frame is due, and only briefly once it
// is.
func radarMaxAge(due, now time.Time) int {
	const overdue, most = 15, 60
	until := int(due.Sub(now) / time.Second)
	if until <= 0 {
		return overdue
	}
	return min(until, most)
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
