// Package nwp reads the convective environment from numerical weather
// prediction through Open-Meteo: CAPE, convective inhibition, lifted index,
// the model's rain and cloud, and the water in the column. Radar shows where
// rain is; these say whether the air will let new storms grow, which is
// what extrapolation misses in the tropics.
//
// Open-Meteo is free for non-commercial use within about 10,000 calls a
// day, a call per location: points are snapped to a 0.25° grid so nearby
// regions share one, answers are cached, and a daily budget stops the
// client well before the limit.
package nwp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// HistoryURL serves past forecasts, the first hours of each run
	// stitched together: slightly better than what a live forecast had,
	// which matters little for the slowly changing environment.
	HistoryURL = "https://historical-forecast-api.open-meteo.com/v1/forecast"
	// Grid is the snapping step in degrees, near the global models' own.
	Grid = 0.25
	// DefaultBudget is the calls allowed a day (UTC), under the free 10,000.
	DefaultBudget = 8000
	// MinuteCalls and HourCalls pace the calls under the free 600 a minute
	// and 5,000 an hour. A call is a location, not a request.
	MinuteCalls = 500
	HourCalls   = 4500
	// minuteCool is the pause after Open-Meteo's minutely limit; any other
	// throttle (hourly, daily) pauses throttleCool.
	minuteCool   = 65 * time.Second
	throttleCool = 10 * time.Minute
	// historyRetries is how often History sends a batch again after a
	// short pause for the minutely limit.
	historyRetries = 3
	// batch is how many locations one history request asks for.
	batch = 50
)

// Vars are the hourly variables read, in Env order.
var Vars = []string{"cape", "convective_inhibition", "lifted_index", "precipitation", "showers",
	"cloud_cover", "total_column_integrated_water_vapour"}

// Env is the environment at one time; NaN where unknown.
type Env struct {
	CAPE    float32 // J/kg
	CIN     float32 // J/kg
	LI      float32 // lifted index, K
	Precip  float32 // mm in the hour ending then
	Showers float32 // mm in the hour ending then (convective)
	Cloud   float32 // %
	TCWV    float32 // kg/m²
}

// Missing is an Env with nothing known.
func Missing() Env {
	n := float32(math.NaN())
	return Env{n, n, n, n, n, n, n}
}

// Series is one location's hourly variables.
type Series struct {
	Lat   float64     `json:"lat"`
	Lon   float64     `json:"lon"`
	Times []int64     `json:"times"` // unix s, hourly, ascending
	Vals  [][]float32 `json:"vals"`  // Vals[v][i] for Vars[v] at Times[i]; NaN missing
}

// seriesJSON is Series on the wire, NaN as null (JSON has no NaN).
type seriesJSON struct {
	Lat   float64      `json:"lat"`
	Lon   float64      `json:"lon"`
	Times []int64      `json:"times"`
	Vals  [][]*float32 `json:"vals"`
}

// MarshalJSON writes NaN as null.
func (s *Series) MarshalJSON() ([]byte, error) {
	w := seriesJSON{Lat: s.Lat, Lon: s.Lon, Times: s.Times, Vals: make([][]*float32, len(s.Vals))}
	for k, col := range s.Vals {
		w.Vals[k] = make([]*float32, len(col))
		for i := range col {
			if !math.IsNaN(float64(col[i])) {
				w.Vals[k][i] = &col[i]
			}
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads null as NaN.
func (s *Series) UnmarshalJSON(data []byte) error {
	var w seriesJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	s.Lat, s.Lon, s.Times, s.Vals = w.Lat, w.Lon, w.Times, make([][]float32, len(w.Vals))
	for k, col := range w.Vals {
		s.Vals[k] = make([]float32, len(col))
		for i, v := range col {
			s.Vals[k][i] = float32(math.NaN())
			if v != nil {
				s.Vals[k][i] = *v
			}
		}
	}
	return nil
}

// At interpolates the series linearly to t; Missing outside it.
func (s *Series) At(t int64) Env {
	if s == nil || len(s.Times) == 0 || t < s.Times[0] || t > s.Times[len(s.Times)-1] {
		return Missing()
	}
	i := 0
	for i+1 < len(s.Times) && s.Times[i+1] <= t {
		i++
	}
	var v [7]float32
	for k := range v {
		v[k] = float32(math.NaN())
		if k >= len(s.Vals) {
			continue
		}
		a := s.Vals[k][i]
		if i+1 >= len(s.Times) || t == s.Times[i] {
			v[k] = a
			continue
		}
		b := s.Vals[k][i+1]
		w := float32(t-s.Times[i]) / float32(s.Times[i+1]-s.Times[i])
		v[k] = a*(1-w) + b*w
	}
	return Env{v[0], v[1], v[2], v[3], v[4], v[5], v[6]}
}

// Snap rounds a location to the Grid, so nearby ones share a call.
func Snap(lat, lon float64) (float64, float64) {
	r := func(v float64) float64 { return math.Round(v/Grid) * Grid }
	return r(lat), r(lon)
}

// Point is a snapped location.
type Point struct{ Lat, Lon float64 }

func (p Point) key() string { return fmt.Sprintf("%.2f_%.2f", p.Lat, p.Lon) }

// Stats counts use.
type Stats struct {
	Calls     int64 `json:"calls"` // locations asked for
	Requests  int64 `json:"requests"`
	CacheHits int64 `json:"cache_hits"`
	Throttled int64 `json:"throttled"`
	Errors    int64 `json:"errors"`
	BudgetHit int64 `json:"budget_hit"` // calls refused by the daily budget
}

// Client fetches and caches environments.
type Client struct {
	HTTP       *http.Client
	HistoryURL string
	CacheDir   string    // history cache on disk; empty disables
	Pace       []*Window // every request waits in each for its calls
	Budget     int       // calls per UTC day; 0 means no limit
	UserAgent  string
	now        func() time.Time
	// MinuteCool is the pause after the minutely limit (tests shorten it).
	MinuteCool time.Duration

	calls, requests, hits, throttled, errs, budgetHit atomic.Int64

	coverMu sync.Mutex
	cover   map[string][2]int // History: day → locations had, asked

	mu   sync.Mutex
	day  string // UTC day the budget counts
	used int
	cool time.Time // no requests before this, after a 429
}

// New returns a client paced under Open-Meteo's free limits.
func New(cacheDir string) *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 20 * time.Second},
		HistoryURL: HistoryURL,
		CacheDir:   cacheDir,
		Pace:       []*Window{NewWindow(MinuteCalls, time.Minute), NewWindow(HourCalls, time.Hour)},
		Budget:     DefaultBudget,
		UserAgent:  "raincast/1.0 (radar nowcast research)",
		now:        time.Now,
		MinuteCool: minuteCool,
		cover:      map[string][2]int{},
	}
}

// Stats reports use since the client started.
func (c *Client) Stats() Stats {
	return Stats{Calls: c.calls.Load(), Requests: c.requests.Load(), CacheHits: c.hits.Load(),
		Throttled: c.throttled.Load(), Errors: c.errs.Load(), BudgetHit: c.budgetHit.Load()}
}

// ErrBudget is a request refused because the day's calls are spent.
var ErrBudget = errors.New("nwp: daily call budget spent")

// ErrThrottled is a request Open-Meteo refused (http 429), or one not sent
// during the cool-down after.
var ErrThrottled = errors.New("nwp: throttled")

// take reserves n calls from today's budget and checks the cool-down.
func (c *Client) take(n int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Before(c.cool) {
		return fmt.Errorf("%w: cooling down for %s", ErrThrottled, c.cool.Sub(now).Round(time.Second))
	}
	if d := now.UTC().Format("2006-01-02"); d != c.day {
		c.day, c.used = d, 0
	}
	if c.Budget > 0 && c.used+n > c.Budget {
		c.budgetHit.Add(int64(n))
		return ErrBudget
	}
	c.used += n
	return nil
}

// History returns, for each location, the past forecasts over the UTC days
// from..to (inclusive), from the disk cache when there. Locations are
// snapped; the result follows pts, nil where no day could be had. A batch
// hit by the minutely limit is sent again after the pause; any other
// failure skips that batch and the rest go on, the errors joined. Only a
// spent budget, a long throttle or ctx stop it early.
func (c *Client) History(ctx context.Context, pts []Point, from, to time.Time) ([]*Series, error) {
	days := []string{}
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.Add(24 * time.Hour) {
		days = append(days, d.Format("2006-01-02"))
	}
	out := make([]*Series, len(pts))
	snapped := make([]Point, len(pts))
	for i, p := range pts {
		lat, lon := Snap(p.Lat, p.Lon)
		snapped[i] = Point{lat, lon}
	}
	var errs []error
	stopped := false
	for _, day := range days {
		// Points whose day is not cached yet, without repeats.
		var need []Point
		seen := map[Point]bool{}
		got := map[Point]*Series{}
		for _, p := range snapped {
			if seen[p] {
				continue
			}
			seen[p] = true
			if s := c.readDay(p, day); s != nil {
				c.hits.Add(1)
				got[p] = s
				continue
			}
			need = append(need, p)
		}
		for i := 0; i < len(need) && !stopped; i += batch {
			part := need[i:min(i+batch, len(need))]
			list, err := c.historyBatch(ctx, part, day)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", day, err))
				stopped = errors.Is(err, ErrBudget) || errors.Is(err, ErrThrottled) || ctx.Err() != nil
				continue
			}
			for j, s := range list {
				s.Lat, s.Lon = part[j].Lat, part[j].Lon
				got[part[j]] = s
				c.writeDay(part[j], day, s)
			}
		}
		for i, p := range snapped {
			out[i] = appendSeries(out[i], got[p])
		}
		c.coverMu.Lock()
		if c.cover == nil {
			c.cover = map[string][2]int{}
		}
		c.cover[day] = [2]int{len(got), len(seen)}
		c.coverMu.Unlock()
	}
	return out, errors.Join(errs...)
}

// historyBatch fetches one day for part, waiting out the minutely limit
// up to historyRetries times.
func (c *Client) historyBatch(ctx context.Context, part []Point, day string) ([]*Series, error) {
	q := c.query(part)
	q.Set("start_date", day)
	q.Set("end_date", day)
	for try := 0; ; try++ {
		list, err := c.fetch(ctx, c.HistoryURL, q, len(part))
		if err == nil || !errors.Is(err, ErrThrottled) || try == historyRetries {
			return list, err
		}
		c.mu.Lock()
		wait := c.cool.Sub(c.now())
		c.mu.Unlock()
		if wait > c.MinuteCool { // hourly or daily: not worth waiting here
			return nil, err
		}
		t := time.NewTimer(max(wait, 0))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

// Coverage is, per UTC day History was asked for, the snapped locations
// it had and those it was asked for.
func (c *Client) Coverage() map[string][2]int {
	c.coverMu.Lock()
	defer c.coverMu.Unlock()
	out := make(map[string][2]int, len(c.cover))
	for d, v := range c.cover {
		out[d] = v
	}
	return out
}

func appendSeries(a, b *Series) *Series {
	if b == nil {
		return a
	}
	if a == nil {
		cp := *b
		cp.Times = append([]int64(nil), b.Times...)
		cp.Vals = make([][]float32, len(b.Vals))
		for k := range b.Vals {
			cp.Vals[k] = append([]float32(nil), b.Vals[k]...)
		}
		return &cp
	}
	a.Times = append(a.Times, b.Times...)
	for k := range a.Vals {
		a.Vals[k] = append(a.Vals[k], b.Vals[k]...)
	}
	return a
}

func (c *Client) dayPath(p Point, day string) string {
	return filepath.Join(c.CacheDir, "history", day, p.key()+".json")
}

func (c *Client) readDay(p Point, day string) *Series {
	if c.CacheDir == "" {
		return nil
	}
	data, err := os.ReadFile(c.dayPath(p, day))
	if err != nil {
		return nil
	}
	var s Series
	if json.Unmarshal(data, &s) != nil || len(s.Times) == 0 {
		return nil
	}
	return &s
}

func (c *Client) writeDay(p Point, day string, s *Series) {
	// Today's runs are not final yet.
	if c.CacheDir == "" || day >= c.now().UTC().Format("2006-01-02") {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	path := c.dayPath(p, day)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (c *Client) query(pts []Point) url.Values {
	lats := make([]string, len(pts))
	lons := make([]string, len(pts))
	for i, p := range pts {
		lats[i] = strconv.FormatFloat(p.Lat, 'f', 2, 64)
		lons[i] = strconv.FormatFloat(p.Lon, 'f', 2, 64)
	}
	q := url.Values{}
	q.Set("latitude", strings.Join(lats, ","))
	q.Set("longitude", strings.Join(lons, ","))
	q.Set("hourly", strings.Join(Vars, ","))
	q.Set("timezone", "GMT")
	q.Set("timeformat", "unixtime")
	return q
}

// response is one location's answer.
type response struct {
	Latitude  float64                    `json:"latitude"`
	Longitude float64                    `json:"longitude"`
	Hourly    map[string]json.RawMessage `json:"hourly"`
	Error     bool                       `json:"error"`
	Reason    string                     `json:"reason"`
}

// fetch asks base with q for n locations.
func (c *Client) fetch(ctx context.Context, base string, q url.Values, n int) ([]*Series, error) {
	if err := c.take(n); err != nil {
		return nil, err
	}
	for _, w := range c.Pace {
		if err := w.WaitN(ctx, n); err != nil {
			return nil, err
		}
	}
	c.calls.Add(int64(n))
	c.requests.Add(1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.errs.Add(1)
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		c.errs.Add(1)
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		c.throttled.Add(1)
		var r response
		_ = json.Unmarshal(data, &r)
		pause := throttleCool
		if strings.Contains(strings.ToLower(r.Reason), "minutely") {
			pause = c.MinuteCool
		}
		c.mu.Lock()
		c.cool = c.now().Add(pause)
		c.mu.Unlock()
		return nil, fmt.Errorf("%w (http 429): %.200s", ErrThrottled, r.Reason)
	}
	if resp.StatusCode != http.StatusOK {
		c.errs.Add(1)
		return nil, fmt.Errorf("nwp: http %d: %.200s", resp.StatusCode, data)
	}
	list, err := parse(data, n)
	if err != nil {
		c.errs.Add(1)
	}
	return list, err
}

// parse reads an answer: one object for one location, an array for more.
func parse(data []byte, n int) ([]*Series, error) {
	var rs []response
	if n == 1 && len(data) > 0 && data[0] == '{' {
		var r response
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		rs = []response{r}
	} else if err := json.Unmarshal(data, &rs); err != nil {
		return nil, err
	}
	if len(rs) != n {
		return nil, fmt.Errorf("nwp: %d answers for %d locations", len(rs), n)
	}
	out := make([]*Series, n)
	for i, r := range rs {
		if r.Error {
			return nil, fmt.Errorf("nwp: %s", r.Reason)
		}
		s := &Series{Lat: r.Latitude, Lon: r.Longitude}
		if err := json.Unmarshal(r.Hourly["time"], &s.Times); err != nil {
			return nil, fmt.Errorf("nwp: time: %w", err)
		}
		for _, v := range Vars {
			var vals []*float64
			if raw, ok := r.Hourly[v]; ok {
				if err := json.Unmarshal(raw, &vals); err != nil {
					return nil, fmt.Errorf("nwp: %s: %w", v, err)
				}
			}
			col := make([]float32, len(s.Times))
			for j := range col {
				col[j] = float32(math.NaN())
				if j < len(vals) && vals[j] != nil {
					col[j] = float32(*vals[j])
				}
			}
			s.Vals = append(s.Vals, col)
		}
		out[i] = s
	}
	return out, nil
}
