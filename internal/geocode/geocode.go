// Package geocode turns free text, coordinates and map links into places.
// Text and reverse lookups go to Geoapify, rate-limited and cached; the API
// key stays on the server.
package geocode

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is Geoapify's API host.
const DefaultBaseURL = "https://api.geoapify.com"

// Place is one search result.
type Place struct {
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

// LatLon is a point used to rank nearby results first.
type LatLon struct{ Lat, Lon float64 }

// Client queries Geoapify.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	// Key is the Geoapify API key; empty disables text search and reverse
	// lookups (coordinates and map links still resolve).
	Key       string
	Log       *slog.Logger // optional
	UserAgent string
	Countries string // comma-separated ISO codes limiting results; empty for worldwide
	Language  string
	Limit     int // results for a submitted search
	// SuggestLimit is the number of suggestions while typing.
	SuggestLimit int
	// Bias ranks results near this point first when the caller gives none.
	Bias     *LatLon
	CacheTTL time.Duration
	CacheMax int
	// ShortLinkHosts are the only hosts whose redirects are followed;
	// other links are parsed offline, so user input cannot make the server
	// fetch arbitrary URLs.
	ShortLinkHosts map[string]bool

	// Map tiles: Geoapify style name and where fetched tiles are kept
	// (empty TileDir disables the disk cache), and how long a cached tile
	// is used before it is fetched again (0 keeps tiles forever).
	TileURL   string
	TileStyle string
	TileDir   string
	TileAge   time.Duration

	rate  limiter
	pause coolDown
	stats counters

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	places []Place
	at     time.Time
}

// New returns a client with defaults suited to Geoapify's free plan.
func New(key, userAgent, countries string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 10 * time.Second},
		BaseURL:   DefaultBaseURL,
		Key:       key,
		UserAgent: userAgent,
		Countries: countries,
		Language:  "vi",
		Limit:     5,

		SuggestLimit: 6,
		// Free plan: 5 requests/second.
		rate:     limiter{interval: 200 * time.Millisecond},
		CacheTTL: 24 * time.Hour,
		CacheMax: 1000,
		cache:    map[string]cached{},

		ShortLinkHosts: map[string]bool{"maps.app.goo.gl": true, "goo.gl": true},

		TileURL:   DefaultTileURL,
		TileStyle: "dark-matter",
		TileAge:   24 * time.Hour,
	}
}

func (c *Client) lookup(key string) ([]Place, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Since(e.at) > c.CacheTTL {
		return nil, false
	}
	c.stats.cacheHits.Add(1)
	return e.places, true
}

func (c *Client) store(key string, places []Place) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= c.CacheMax {
		clear(c.cache)
	}
	c.cache[key] = cached{places, time.Now()}
}

func (c *Client) logf(msg string, args ...any) {
	if c.Log != nil {
		c.Log.Debug(msg, args...)
	}
}

// limiter spaces upstream requests at least interval apart.
type limiter struct {
	mu       sync.Mutex // held while waiting for a slot
	last     time.Time
	interval time.Duration
}

// wait blocks until a request slot is free. Callers queue on the mutex, so
// bursts are spread out instead of hitting the upstream at once.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := l.interval - time.Since(l.last); d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	l.last = time.Now()
	return nil
}

// coolDown refuses requests for a while after the upstream answered 429.
type coolDown struct {
	mu    sync.Mutex
	until time.Time
}

func (p *coolDown) active(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return now.Before(p.until)
}

func (p *coolDown) extend(now time.Time, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if u := now.Add(d); u.After(p.until) {
		p.until = u
	}
}

// joinUnique joins the non-empty parts that differ from name and each other.
func joinUnique(name string, parts ...string) string {
	seen := map[string]bool{strings.ToLower(name): true}
	var out []string
	for _, s := range parts {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(s))
	}
	return strings.Join(out, ", ")
}

// duplicate reports whether places has one with the same name within 300 m.
// Names compare without case or spaces ("Megamall" vs "Mega Mall").
func duplicate(places []Place, p Place) bool {
	key := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "") }
	for _, q := range places {
		if key(q.Name) == key(p.Name) && distanceM(q.Lat, q.Lon, p.Lat, p.Lon) < 300 {
			return true
		}
	}
	return false
}

func distanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}
