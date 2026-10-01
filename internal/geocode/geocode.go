// Package geocode turns free text, coordinates and map links into places.
// Text goes to LocationIQ while within its quota, then Photon, with
// Nominatim as a last resort for submitted searches. Every upstream is
// rate-limited, identified by User-Agent and cached.
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public OpenStreetMap Nominatim instance.
const DefaultBaseURL = "https://nominatim.openstreetmap.org"

// Place is one search result.
type Place struct {
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

// Client queries LocationIQ, Photon and Nominatim.
type Client struct {
	HTTP      *http.Client
	BaseURL   string // Nominatim
	PhotonURL string
	// LocationIQKey enables LocationIQ; empty means Photon only.
	LocationIQKey string
	LocationIQURL string
	Log           *slog.Logger // optional
	UserAgent     string
	Countries     string // comma-separated ISO codes limiting results; empty for worldwide
	Language      string
	Limit         int // results for a submitted search
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

	nominatimRate limiter
	photonRate    limiter
	liqQuota      quota
	stats         counters

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	places []Place
	at     time.Time
}

// New returns a client with policy-compliant defaults.
func New(userAgent, countries string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 10 * time.Second},
		BaseURL:   DefaultBaseURL,
		PhotonURL: DefaultPhotonURL,
		UserAgent: userAgent,
		Countries: countries,
		Language:  "vi",
		Limit:     5,

		SuggestLimit:  6,
		LocationIQURL: DefaultLocationIQURL,
		// LocationIQ free plan: 2 requests/second, 60/minute.
		liqQuota:      quota{perSec: 2, perMin: 60},
		nominatimRate: limiter{interval: time.Second}, // Nominatim policy: max 1 req/s
		// Photon has no hard limit but asks for fair use; typing is already
		// debounced client-side.
		photonRate: limiter{interval: 200 * time.Millisecond},
		CacheTTL:   24 * time.Hour,
		CacheMax:   1000,
		cache:      map[string]cached{},

		ShortLinkHosts: map[string]bool{"maps.app.goo.gl": true, "goo.gl": true},
	}
}

// Search queries Nominatim for up to Limit places matching q.
func (c *Client) Search(ctx context.Context, q string) ([]Place, error) {
	q = strings.ToLower(strings.Join(strings.Fields(q), " "))
	if q == "" {
		return []Place{}, nil
	}
	key := "n:" + q
	if p, ok := c.lookup(key); ok {
		return p, nil
	}
	if err := c.nominatimRate.wait(ctx); err != nil {
		return nil, err
	}
	places, err := c.fetch(ctx, q)
	count(err, &c.stats.nominatimOK, &c.stats.nominatimErrors)
	if err != nil {
		return nil, err
	}
	c.store(key, places)
	return places, nil
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

type result struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
}

func (c *Client) fetch(ctx context.Context, q string) ([]Place, error) {
	v := url.Values{}
	v.Set("q", q)
	v.Set("format", "jsonv2")
	v.Set("limit", strconv.Itoa(c.Limit))
	if c.Countries != "" {
		v.Set("countrycodes", c.Countries)
	}
	if c.Language != "" {
		v.Set("accept-language", c.Language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/search?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geocode: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocode: http %d", resp.StatusCode)
	}
	var rs []result
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rs); err != nil {
		return nil, fmt.Errorf("geocode: %w", err)
	}
	places := make([]Place, 0, len(rs))
	for _, r := range rs {
		lat, err1 := strconv.ParseFloat(r.Lat, 64)
		lon, err2 := strconv.ParseFloat(r.Lon, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		name := r.Name
		if name == "" {
			name, _, _ = strings.Cut(r.DisplayName, ",")
		}
		places = append(places, Place{Name: name, Address: r.DisplayName, Lat: lat, Lon: lon})
	}
	return places, nil
}
