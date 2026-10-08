package nwp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// answer builds an Open-Meteo answer for n locations over a day from t0,
// CAPE rising 100 J/kg an hour.
func answer(n int, t0 int64) string {
	var parts []string
	for range n {
		var times, cape, nulls []string
		for h := range 24 {
			times = append(times, fmt.Sprint(t0+int64(h)*3600))
			cape = append(cape, fmt.Sprint(h*100))
			nulls = append(nulls, "null")
		}
		parts = append(parts, fmt.Sprintf(`{"latitude":10.7,"longitude":106.8,"hourly":{"time":[%s],"cape":[%s],"lifted_index":[%s]}}`,
			strings.Join(times, ","), strings.Join(cape, ","), strings.Join(nulls, ",")))
	}
	if n == 1 {
		return parts[0]
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestParseAndAt(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	list, err := parse([]byte(answer(2, t0)), 2)
	if err != nil {
		t.Fatal(err)
	}
	s := list[1]
	if e := s.At(t0 + 5400); math.Abs(float64(e.CAPE)-150) > 1e-3 {
		t.Fatalf("CAPE at 1.5 h = %v, want 150", e.CAPE)
	}
	e := s.At(t0 + 3600)
	if e.CAPE != 100 || !math.IsNaN(float64(e.LI)) || !math.IsNaN(float64(e.TCWV)) {
		t.Fatalf("env %+v: nulls and absent variables must be NaN", e)
	}
	if e := s.At(t0 - 1); !math.IsNaN(float64(e.CAPE)) {
		t.Fatal("before the series must be missing")
	}
	if _, err := parse([]byte(answer(1, t0)), 2); err == nil {
		t.Fatal("want count mismatch error")
	}
}

func TestSnapSharesNearbyPoints(t *testing.T) {
	a1, o1 := Snap(10.76, 106.71)
	a2, o2 := Snap(10.79, 106.70)
	if a1 != a2 || o1 != o2 || a1 != 10.75 || o1 != 106.75 {
		t.Fatalf("%v,%v vs %v,%v", a1, o1, a2, o2)
	}
}

func server(t *testing.T, status int, hits *atomic.Int64, t0 int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		n := len(strings.Split(r.URL.Query().Get("latitude"), ","))
		fmt.Fprint(w, answer(n, t0))
	}))
}

func TestHistoryCachesPastDaysAndDedupes(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	var hits atomic.Int64
	srv := server(t, http.StatusOK, &hits, t0)
	defer srv.Close()
	c := fast(t.TempDir())
	c.HistoryURL = srv.URL
	day := time.Unix(t0, 0)
	pts := []Point{{10.76, 106.71}, {10.79, 106.70}, {21, 105.8}}
	for range 2 {
		got, err := c.History(context.Background(), pts, day, day)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] == nil || got[2] == nil || got[0].At(t0+3600).CAPE != 100 || !math.IsNaN(float64(got[0].At(t0).LI)) {
			t.Fatalf("got %+v", got)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("%d requests, want 1 (one batch, then the disk cache)", hits.Load())
	}
	if c.Stats().Calls != 2 {
		t.Fatalf("calls %d, want 2: the first two points snap together", c.Stats().Calls)
	}
}

func TestBudgetAndThrottle(t *testing.T) {
	var hits atomic.Int64
	srv := server(t, http.StatusTooManyRequests, &hits, 0)
	defer srv.Close()
	call := func(c *Client, lat, lon float64) error {
		_, err := c.fetch(context.Background(), srv.URL, c.query([]Point{{lat, lon}}), 1)
		return err
	}
	c := fast("")
	if err := call(c, 1, 1); err == nil {
		t.Fatal("want throttled")
	}
	if err := call(c, 2, 2); err == nil || !strings.Contains(err.Error(), "cooling") {
		t.Fatalf("want cool-down, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("%d requests, want 1", hits.Load())
	}

	b := fast("")
	b.Budget = 1
	call(b, 1, 1)
	b.cool = time.Time{}
	if err := call(b, 3, 3); !errors.Is(err, ErrBudget) {
		t.Fatalf("want budget error, got %v", err)
	}
}

// fast is a client without pacing.
func fast(cacheDir string) *Client {
	c := New(cacheDir)
	c.Pace = nil
	return c
}

func TestHistoryWaitsOutTheMinutelyLimit(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 2 { // the second batch is refused once
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":true,"reason":"Minutely API request limit exceeded. Please try again in one minute."}`)
			return
		}
		fmt.Fprint(w, answer(len(strings.Split(r.URL.Query().Get("latitude"), ",")), t0))
	}))
	defer srv.Close()
	c := fast(t.TempDir())
	c.HistoryURL = srv.URL
	c.MinuteCool = 10 * time.Millisecond
	var pts []Point
	for i := range batch + 10 { // two batches
		pts = append(pts, Point{float64(i), 0})
	}
	day := time.Unix(t0, 0)
	got, err := c.History(context.Background(), pts, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range got {
		if s == nil || len(s.Times) != 48 {
			t.Fatalf("point %d: %+v, want two days", i, s)
		}
	}
	for d, v := range c.Coverage() {
		if v[0] != v[1] {
			t.Fatalf("%s: %d of %d", d, v[0], v[1])
		}
	}
	if c.Stats().Throttled != 1 {
		t.Fatalf("throttled %d, want 1", c.Stats().Throttled)
	}
}

func TestHistoryGoesOnAfterAFailedDay(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start_date") == "2026-10-01" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, answer(len(strings.Split(r.URL.Query().Get("latitude"), ",")), t0+86400))
	}))
	defer srv.Close()
	c := fast(t.TempDir())
	c.HistoryURL = srv.URL
	day := time.Unix(t0, 0)
	got, err := c.History(context.Background(), []Point{{1, 1}}, day, day.Add(24*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "2026-10-01") {
		t.Fatalf("want the first day's error, got %v", err)
	}
	if got[0] == nil || got[0].At(t0+86400+3600).CAPE != 100 {
		t.Fatalf("second day missing: %+v", got[0])
	}
	if cv := c.Coverage(); cv["2026-10-01"] != [2]int{0, 1} || cv["2026-10-02"] != [2]int{1, 1} {
		t.Fatalf("coverage %v", cv)
	}
}

func TestWindowCountsCalls(t *testing.T) {
	w := NewWindow(100, 50*time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	w.WaitN(ctx, 60)
	w.WaitN(ctx, 40) // fills the window, no wait
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("waited within the cap")
	}
	w.WaitN(ctx, 1) // over the cap: waits for the window to clear
	if el := time.Since(start); el < 45*time.Millisecond {
		t.Fatalf("went after %v, want about 50ms", el)
	}
}
