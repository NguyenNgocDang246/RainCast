// Package server exposes forecasts and verification results over HTTP.
package server

import (
	"context"
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

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"raincast/internal/backtest"
	"raincast/internal/geocode"
	"raincast/internal/pipeline"
	_ "raincast/internal/server/docs" // registers the Swagger spec
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

//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g server.go -o docs --parseInternal --parseDependency --outputTypes go,json,yaml

// New returns the HTTP handler. The API is documented at /api/docs/index.html.
//
//	@title			RainCast API
//	@version		1.0
//	@description	Minute-by-minute rain nowcasts from radar, place search and map tiles.
//	@BasePath		/
func New(src Source, geo Geocoder, st *store.Store, log *slog.Logger, cfg Config) http.Handler {
	s := &Server{
		src: src, geo: geo, store: st, log: log,
		corsOrigin: cfg.CORSOrigin, backtest: cfg.Backtest, countryOf: cfg.CountryOf,
		countryHdr: cfg.CountryHeader, realIPHdr: cfg.RealIPHeader, clientTiles: cfg.ClientTiles,
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(s.logging, s.cors)
	r.NoRoute(func(c *gin.Context) { writeError(c, http.StatusNotFound, "not found") })
	r.GET("/healthz", s.health)
	r.GET("/api/forecast", s.forecast)
	r.POST("/api/forecast", s.forecast)
	r.GET("/api/geocode", s.geocode)
	r.GET("/api/suggest", s.suggest)
	r.GET("/api/reverse", s.reverse)
	r.GET("/api/tiles/:z/:x/:y", s.tile)
	r.GET("/api/radar", s.radar)
	r.GET("/api/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	if cfg.Admin {
		s.routeAdmin(r)
	}
	return r
}

// healthResponse is the body of GET /healthz.
type healthResponse struct {
	OK    bool `json:"ok"`
	Ready bool `json:"ready"` // the radar frame index has loaded
}

// errorResponse is the body of every failed request.
type errorResponse struct {
	Error string `json:"error"`
}

// health reports whether the server is up and ready to forecast.
//
//	@Summary	Health check
//	@Tags		health
//	@Produce	json
//	@Success	200	{object}	healthResponse
//	@Router		/healthz [get]
func (s *Server) health(c *gin.Context) {
	s.src.EnsureFresh(c.Request.Context())
	writeJSON(c, http.StatusOK, healthResponse{OK: true, Ready: s.src.Ready()})
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

// geocode resolves an address, coordinates or a map link into places.
//
//	@Summary	Search places
//	@Tags		geocode
//	@Produce	json
//	@Param		q	query		string	true	"Address, coordinates or a map link (max 500 characters)"
//	@Success	200	{array}		geocode.Place
//	@Failure	400	{object}	errorResponse
//	@Failure	422	{object}	errorResponse	"The link has no location"
//	@Failure	502	{object}	errorResponse
//	@Router		/api/geocode [get]
func (s *Server) geocode(c *gin.Context) {
	q := c.Query("q")
	if q == "" || utf8.RuneCountInString(q) > maxQueryLen {
		writeError(c, http.StatusBadRequest, "q is required (max 500 characters)")
		return
	}
	places, err := s.geo.Resolve(c.Request.Context(), q, s.country(c.Request))
	switch {
	case errors.Is(err, geocode.ErrUnresolvedLink):
		writeError(c, http.StatusUnprocessableEntity, "link has no location")
	case err != nil:
		s.log.Error("geocode", "err", err)
		writeError(c, http.StatusBadGateway, "address lookup failed")
	default:
		writeJSON(c, http.StatusOK, places)
	}
}

// suggest returns places for a partial query; optional lat/lon rank nearby
// results first.
//
//	@Summary		Suggest places while typing
//	@Description	Best-effort: a failed lookup answers with an empty list.
//	@Tags			geocode
//	@Produce		json
//	@Param			q	query		string	false	"Partial query (max 500 characters)"
//	@Param			lat	query		number	false	"Latitude to rank nearby places first"
//	@Param			lon	query		number	false	"Longitude to rank nearby places first"
//	@Success		200	{array}		geocode.Place
//	@Failure		400	{object}	errorResponse
//	@Router			/api/suggest [get]
func (s *Server) suggest(c *gin.Context) {
	text := c.Query("q")
	if utf8.RuneCountInString(text) > maxQueryLen {
		writeError(c, http.StatusBadRequest, "q is too long (max 500 characters)")
		return
	}
	var bias *geocode.LatLon
	lat, err1 := strconv.ParseFloat(c.Query("lat"), 64)
	lon, err2 := strconv.ParseFloat(c.Query("lon"), 64)
	if err1 == nil && err2 == nil && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 {
		bias = &geocode.LatLon{Lat: lat, Lon: lon}
	}
	ctx := c.Request.Context()
	places, err := s.geo.Suggest(ctx, text, bias, s.country(c.Request))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("suggest", "err", err)
		}
		// Suggestions are best-effort; the client keeps typing.
		places = []geocode.Place{}
	}
	writeJSON(c, http.StatusOK, places)
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
//
//	@Summary	Name a point on the map
//	@Tags		geocode
//	@Produce	json
//	@Param		lat	query		number	true	"Latitude (±85)"
//	@Param		lon	query		number	true	"Longitude (±180)"
//	@Success	200	{object}	geocode.Place
//	@Failure	400	{object}	errorResponse
//	@Router		/api/reverse [get]
func (s *Server) reverse(c *gin.Context) {
	lat, lon, err := parseLatLon(c.Query("lat"), c.Query("lon"))
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx := c.Request.Context()
	p, err := s.geo.Reverse(ctx, lat, lon)
	if err != nil && ctx.Err() == nil && !errors.Is(err, geocode.ErrNoKey) {
		s.log.Warn("reverse", "err", err)
	}
	if err == nil {
		// The name depends only on the point, so a CDN may keep it.
		c.Header("Cache-Control", "public, max-age=86400, s-maxage=86400")
	}
	writeJSON(c, http.StatusOK, p)
}

// tile proxies a map tile so the Geoapify key stays on the server.
//
//	@Summary	Base map tile
//	@Tags		map
//	@Produce	png
//	@Param		z	path		int		true	"Zoom"
//	@Param		x	path		int		true	"Tile column"
//	@Param		y	path		string	true	"Tile row, optionally with .png"
//	@Success	200	{file}		binary
//	@Failure	400	{object}	errorResponse
//	@Failure	502	{object}	errorResponse
//	@Failure	503	{object}	errorResponse	"Map tiles are not configured"
//	@Router		/api/tiles/{z}/{x}/{y} [get]
func (s *Server) tile(c *gin.Context) {
	z, err1 := strconv.Atoi(c.Param("z"))
	x, err2 := strconv.Atoi(c.Param("x"))
	y, err3 := strconv.Atoi(strings.TrimSuffix(c.Param("y"), ".png"))
	if err1 != nil || err2 != nil || err3 != nil {
		writeError(c, http.StatusBadRequest, "bad tile")
		return
	}
	ctx := c.Request.Context()
	b, err := s.geo.Tile(ctx, z, x, y)
	switch {
	case errors.Is(err, geocode.ErrBadTile):
		writeError(c, http.StatusBadRequest, "bad tile")
	case errors.Is(err, geocode.ErrNoKey):
		// Logged once at startup.
		writeError(c, http.StatusServiceUnavailable, "map tiles are not configured")
	case err != nil:
		if ctx.Err() == nil {
			s.log.Warn("tile", "err", err)
		}
		writeError(c, http.StatusBadGateway, "map tile unavailable")
	default:
		// s-maxage lets a CDN (Vercel's) keep tiles for every visitor.
		c.Header("Cache-Control", "public, max-age=2592000, s-maxage=2592000")
		c.Data(http.StatusOK, "image/png", b)
	}
}

// radar names the newest radar frame for the map. Browsers fetch its tiles
// from RainViewer themselves, so map views don't spend the server's quota.
//
//	@Summary	Newest radar frame
//	@Tags		map
//	@Produce	json
//	@Success	200	{object}	pipeline.RadarFrame
//	@Failure	503	{object}	errorResponse	"Radar data is still loading"
//	@Router		/api/radar [get]
func (s *Server) radar(c *gin.Context) {
	s.src.EnsureFresh(c.Request.Context())
	f, ok := s.src.Radar()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, "radar data is still loading; try again shortly")
		return
	}
	age := radarMaxAge(f.NextDue, time.Now())
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d, s-maxage=%d, stale-while-revalidate=%d", age, age, age))
	writeJSON(c, http.StatusOK, f)
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

func (s *Server) fail(c *gin.Context, err error) {
	s.log.Error("request failed", "err", err)
	writeError(c, http.StatusInternalServerError, "internal error")
}

// cors also answers preflights: gin runs global middleware for unmatched
// routes too, so an OPTIONS request reaches it.
func (s *Server) cors(c *gin.Context) {
	if s.corsOrigin != "" {
		c.Header("Access-Control-Allow-Origin", s.corsOrigin)
		c.Header("Vary", "Origin")
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
	}
	c.Next()
}

func (s *Server) logging(c *gin.Context) {
	start := time.Now()
	c.Next()
	s.log.Debug("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "dur", time.Since(start))
}

func writeJSON(c *gin.Context, status int, v any) {
	c.JSON(status, v)
}

func writeError(c *gin.Context, status int, msg string) {
	writeJSON(c, status, errorResponse{Error: msg})
}
