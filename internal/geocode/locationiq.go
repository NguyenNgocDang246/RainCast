package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultLocationIQURL is LocationIQ's autocomplete endpoint.
const DefaultLocationIQURL = "https://api.locationiq.com/v1/autocomplete"

// errRateLimited means LocationIQ refused the request for quota reasons.
var errRateLimited = errors.New("locationiq: rate limited")

// biasBoxDeg is the half-size of the viewbox that ranks results near the bias
// point first (~55 km); results outside it are still returned.
const biasBoxDeg = 0.5

// locationIQ queries the autocomplete API. It never waits for quota: the
// caller falls back to Photon when no slot is free.
func (c *Client) locationIQ(ctx context.Context, q string, bias *LatLon, limit int) ([]Place, error) {
	if !c.liqQuota.tryAcquire(time.Now()) {
		return nil, errRateLimited
	}
	v := url.Values{}
	v.Set("key", c.LocationIQKey)
	v.Set("q", q)
	v.Set("limit", strconv.Itoa(min(limit, 20)))
	v.Set("dedupe", "1")
	v.Set("normalizecity", "1")
	if c.Countries != "" {
		v.Set("countrycodes", c.Countries)
	}
	if c.Language != "" {
		v.Set("accept-language", c.Language)
	}
	if bias != nil {
		v.Set("viewbox", fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
			bias.Lon-biasBoxDeg, bias.Lat-biasBoxDeg, bias.Lon+biasBoxDeg, bias.Lat+biasBoxDeg))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.LocationIQURL+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// url.Error repeats the URL, which carries the API key.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("locationiq: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("locationiq: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return []Place{}, nil // "Unable to geocode": no match
	case http.StatusTooManyRequests:
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &e)
		c.liqQuota.coolDown(time.Now(), e.Error)
		return nil, fmt.Errorf("%w (%s)", errRateLimited, e.Error)
	default:
		return nil, fmt.Errorf("locationiq: http %d", resp.StatusCode)
	}

	var rs []struct {
		Lat            string `json:"lat"`
		Lon            string `json:"lon"`
		DisplayPlace   string `json:"display_place"`
		DisplayAddress string `json:"display_address"`
	}
	if err := json.Unmarshal(body, &rs); err != nil {
		return nil, fmt.Errorf("locationiq: %w", err)
	}
	places := []Place{}
	for _, r := range rs {
		lat, err1 := strconv.ParseFloat(r.Lat, 64)
		lon, err2 := strconv.ParseFloat(r.Lon, 64)
		if err1 != nil || err2 != nil || r.DisplayPlace == "" {
			continue
		}
		p := Place{Name: r.DisplayPlace, Address: r.DisplayAddress, Lat: lat, Lon: lon}
		if !duplicate(places, p) {
			places = append(places, p)
		}
		if len(places) == limit {
			break
		}
	}
	return places, nil
}

// quota is a non-blocking sliding-window limiter mirroring LocationIQ's
// free plan (per second and per minute), plus a cool-down after a 429.
type quota struct {
	mu     sync.Mutex
	perSec int
	perMin int
	recent []time.Time // request times within the last minute
	until  time.Time   // no requests before this after a 429
}

// tryAcquire takes a slot if one is free right now.
func (q *quota) tryAcquire(now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now.Before(q.until) {
		return false
	}
	kept := q.recent[:0]
	inSec := 0
	for _, t := range q.recent {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
			if now.Sub(t) < time.Second {
				inSec++
			}
		}
	}
	q.recent = kept
	if (q.perSec > 0 && inSec >= q.perSec) || (q.perMin > 0 && len(q.recent) >= q.perMin) {
		return false
	}
	q.recent = append(q.recent, now)
	return true
}

// coolDown pauses requests according to LocationIQ's 429 message.
func (q *quota) coolDown(now time.Time, msg string) {
	var d time.Duration
	switch {
	case strings.Contains(msg, "Second"):
		d = time.Second
	case strings.Contains(msg, "Minute"):
		d = time.Minute
	case strings.Contains(msg, "Day"):
		// The daily quota resets at midnight UTC.
		y, m, dd := now.UTC().Date()
		d = time.Date(y, m, dd+1, 0, 0, 0, 0, time.UTC).Sub(now)
	default:
		d = 10 * time.Second
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if u := now.Add(d); u.After(q.until) {
		q.until = u
	}
}
