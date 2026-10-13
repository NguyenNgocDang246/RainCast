// Package bench compares RainCast's rain forecasts with other providers'.
//
// The independent truth is the METAR of the airport a station sits at,
// scored as weatherindex.ai does. Each source's own later "now" is a truth
// too: once time V comes, what a provider says is raining at V is its
// observation, and RainViewer's radar frame at V is RainCast's. Every
// forecaster is scored against every source's truth, one table per source.
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Provider and truth names, as stored.
const (
	NameRainCast   = "raincast"
	NameRainViewer = "rainviewer" // truth only: radar frames, RainCast's observation
	NameTomorrow   = "tomorrow"
	NameRainbow    = "rainbow"
	NameMETAR      = "metar" // truth only: airport reports, independent of every forecaster
)

// How a provider made a sample.
const (
	KindRadar    = "radar"    // RainCast's nowcast from RainViewer frames
	KindMinutely = "minutely" // a minute-by-minute nowcast
	KindHourly   = "hourly"   // interpolated between hourly forecasts (Tomorrow.io's free plan)
	KindRealtime = "realtime" // current conditions only
)

// Step is the spacing of scored times: RainViewer's frame interval.
const Step = 10 * time.Minute

// Horizon is the longest lead scored: weatherindex.ai ranks at 60′.
const Horizon = time.Hour

// Sample is a provider's rain rate for one time.
type Sample struct {
	Valid time.Time
	MMH   float64
	Kind  string
}

// Provider forecasts rain at a point. Forecast returns samples at now and
// every Step after it up to Horizon, as far as it can; now is a multiple of
// Step. It may return samples with an error when only some calls failed.
type Provider interface {
	Name() string
	Forecast(ctx context.Context, lat, lon float64, now time.Time) ([]Sample, error)
}

// slots are the times Forecast fills, now first.
func slots(now time.Time) []time.Time {
	out := make([]time.Time, 0, int(Horizon/Step)+1)
	for d := time.Duration(0); d <= Horizon; d += Step {
		out = append(out, now.Add(d))
	}
	return out
}

// point is a value from begin (unix s) on.
type point struct {
	begin int64
	v     float64
}

// stepAt is the value of the step series pts (each lasting step seconds,
// sorted) at t. A t just before the first point, by up to tol seconds,
// takes the first point: a series that starts with the next minute still
// says what is falling now. Likewise the last point reaches tol past its
// end: Tomorrow.io's hour of minutes stops at minute 59.
func stepAt(pts []point, t int64, step, tol int64) (float64, bool) {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].begin > t }) - 1
	if i >= 0 && t < pts[i].begin+step {
		return pts[i].v, true
	}
	if n := len(pts); n > 0 && i == n-1 && t < pts[i].begin+step+tol {
		return pts[i].v, true
	}
	if len(pts) > 0 && t < pts[0].begin && pts[0].begin-t <= tol {
		return pts[0].v, true
	}
	return 0, false
}

// lerpAt interpolates pts linearly at t, within their span.
func lerpAt(pts []point, t int64) (float64, bool) {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].begin >= t })
	switch {
	case i < len(pts) && pts[i].begin == t:
		return pts[i].v, true
	case i == 0 || i == len(pts):
		return 0, false
	}
	a, b := pts[i-1], pts[i]
	w := float64(t-a.begin) / float64(b.begin-a.begin)
	return a.v + (b.v-a.v)*w, true
}

// StatusError is a provider's HTTP error response.
type StatusError struct {
	Code       int
	Body       string
	RetryAfter time.Duration // from the Retry-After header, 0 when absent
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

// RateLimited reports whether err is a 429, and how long the provider
// asked to wait (0 when it did not say).
func RateLimited(err error) (time.Duration, bool) {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusTooManyRequests {
		return se.RetryAfter, true
	}
	return 0, false
}

// ErrBudget means a provider's call budget is spent.
var ErrBudget = errors.New("call budget spent")

func defaultHTTP() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

// getJSON decodes the JSON at u into out. Errors never carry u's query,
// where API keys travel.
func getJSON(ctx context.Context, hc *http.Client, u string, header http.Header, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return redact(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return redact(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return redact(err)
	}
	if resp.StatusCode != http.StatusOK {
		se := &StatusError{Code: resp.StatusCode, Body: string(body[:min(len(body), 200)])}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			se.RetryAfter = time.Duration(s) * time.Second
		}
		return se
	}
	return json.Unmarshal(body, out)
}

// redact drops the query from the URL a *url.Error names.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			u.RawQuery = ""
			ue.URL = u.String()
		}
	}
	return err
}
