package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// geoapify starts a fake Geoapify API; handle answers /v1/geocode/<endpoint>.
func geoapify(t *testing.T, handle func(endpoint string, w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint, ok := strings.CutPrefix(r.URL.Path, "/v1/geocode/")
		if !ok {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("apiKey") != "k" || r.URL.Query().Get("format") != "json" {
			t.Errorf("query = %v", r.URL.Query())
		}
		handle(endpoint, w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("k", "test-agent", "vn")
	c.BaseURL = srv.URL
	c.rate.interval = 0
	return c
}

const benThanh = `{"results":[
	{"name":"Chợ Bến Thành","address_line1":"Chợ Bến Thành","address_line2":"Lê Lợi, Quận 1, Vietnam","lat":10.7725,"lon":106.698},
	{"name":"Chợ Bến Thành","address_line1":"Chợ Bến Thành","address_line2":"Quận 1","lat":10.7726,"lon":106.6981},
	{"address_line1":"","formatted":"Đường X, Quận 1","lat":10.1,"lon":106.1}
]}`

func TestSearchFavorsUserCountry(t *testing.T) {
	biases := map[string]string{}
	c := geoapify(t, func(endpoint string, w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("filter") {
			t.Errorf("filter = %q, want none", q.Get("filter"))
		}
		biases[q.Get("text")] = q.Get("bias")
		w.Write([]byte(`{"results":[]}`))
	})
	c.Bias = &LatLon{10.8, 106.7}
	ctx := context.Background()
	c.Resolve(ctx, "singapore", "SG") // abroad: no pull toward the default point
	c.Resolve(ctx, "ben thanh", "")   // unknown: configured country and point
	c.Suggest(ctx, "marina", &LatLon{1.28, 103.85}, "sg")
	want := map[string]string{
		"singapore": "countrycode:sg",
		"ben thanh": "countrycode:vn|proximity:106.7000,10.8000",
		"marina":    "countrycode:sg|proximity:103.8500,1.2800",
	}
	for text, b := range want {
		if biases[text] != b {
			t.Errorf("%s: bias = %q, want %q", text, biases[text], b)
		}
	}

	// The cache keeps countries apart.
	calls := len(biases)
	delete(biases, "singapore")
	c.Resolve(ctx, "singapore", "vn")
	if len(biases) != calls || biases["singapore"] != "countrycode:vn|proximity:106.7000,10.8000" {
		t.Fatalf("biases = %v", biases)
	}
}

func TestSuggestParsesAndCaches(t *testing.T) {
	var calls atomic.Int32
	c := geoapify(t, func(endpoint string, w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if endpoint != "autocomplete" || q.Get("text") != "ben thanh" || q.Has("filter") ||
			q.Get("bias") != "countrycode:vn|proximity:106.7000,10.8000" || q.Get("lang") != "vi" || q.Get("limit") != "6" {
			t.Errorf("%s %v", endpoint, q)
		}
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte(benThanh))
	})
	ps, err := c.Suggest(context.Background(), "  ben   thanh ", &LatLon{10.8, 106.7}, "vn")
	if err != nil {
		t.Fatal(err)
	}
	// The near-duplicate market is dropped; a nameless result uses formatted.
	if len(ps) != 2 || ps[0].Name != "Chợ Bến Thành" || ps[0].Address != "Lê Lợi, Quận 1, Vietnam" || ps[1].Name != "Đường X" {
		t.Fatalf("places = %+v", ps)
	}
	c.Suggest(context.Background(), "Ben Thanh", &LatLon{10.81, 106.71}, "")
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls.Load())
	}
	if st := c.Stats(); !st.GeoapifyEnabled || st.AutocompleteOK != 1 || st.CacheHits != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestSuggestSkipsCoordsLinksAndNoKey(t *testing.T) {
	c := geoapify(t, func(string, http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	for _, q := range []string{"a", "10.8, 106.7", "https://maps.app.goo.gl/x"} {
		if ps, err := c.Suggest(context.Background(), q, nil, ""); err != nil || len(ps) != 0 {
			t.Errorf("%q: %v %v", q, ps, err)
		}
	}
	c.Key = ""
	if ps, err := c.Suggest(context.Background(), "ben thanh", nil, ""); err != nil || len(ps) != 0 {
		t.Errorf("no key: %v %v", ps, err)
	}
}

func TestResolveTextUsesSearch(t *testing.T) {
	c := geoapify(t, func(endpoint string, w http.ResponseWriter, r *http.Request) {
		if endpoint != "search" || r.URL.Query().Get("limit") != "5" {
			t.Errorf("%s %v", endpoint, r.URL.Query())
		}
		w.Write([]byte(benThanh))
	})
	ps, err := c.Resolve(context.Background(), "chợ bến thành", "")
	if err != nil || len(ps) != 2 {
		t.Fatalf("%+v %v", ps, err)
	}
	if c.Stats().SearchOK != 1 {
		t.Fatalf("stats = %+v", c.Stats())
	}
}

func TestReverse(t *testing.T) {
	c := geoapify(t, func(endpoint string, w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if endpoint != "reverse" || q.Get("lat") != "10.772500" || q.Get("lon") != "106.698000" {
			t.Errorf("%s %v", endpoint, q)
		}
		w.Write([]byte(`{"results":[{"address_line1":"12 Lê Lợi","address_line2":"Quận 1","lat":10.7724,"lon":106.6979}]}`))
	})
	p, err := c.Reverse(context.Background(), 10.7725, 106.698)
	// The clicked point is kept, not the address's.
	if err != nil || p.Name != "12 Lê Lợi" || p.Address != "Quận 1" || p.Lat != 10.7725 || p.Lon != 106.698 {
		t.Fatalf("%+v %v", p, err)
	}
	if ps, err := c.Resolve(context.Background(), "10.7725, 106.698", ""); err != nil || len(ps) != 1 || ps[0].Name != "12 Lê Lợi" {
		t.Fatalf("resolve coords: %+v %v", ps, err)
	}
	if st := c.Stats(); st.ReverseOK != 1 || st.CacheHits != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestReverseFallsBackToCoords(t *testing.T) {
	c := geoapify(t, func(_ string, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	p, err := c.Reverse(context.Background(), 10.85, 106.77)
	if err == nil || p.Name != "10.85000, 106.77000" || p.Lat != 10.85 {
		t.Fatalf("%+v %v", p, err)
	}
	ps, err := c.Resolve(context.Background(), "10.85, 106.77", "")
	if err != nil || len(ps) != 1 || ps[0].Name != "10.85000, 106.77000" {
		t.Fatalf("resolve coords: %+v %v", ps, err)
	}
	if c.Stats().Errors != 2 {
		t.Fatalf("stats = %+v", c.Stats())
	}
}

func TestRateLimitedCoolsDown(t *testing.T) {
	var calls atomic.Int32
	c := geoapify(t, func(_ string, w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	for _, q := range []string{"aa", "bb"} {
		if _, err := c.Suggest(context.Background(), q, nil, ""); err == nil {
			t.Fatalf("%s: no error", q)
		}
	}
	// During the cool-down Geoapify is not asked again.
	if calls.Load() != 1 || c.Stats().RateLimited != 2 {
		t.Fatalf("calls = %d, stats = %+v", calls.Load(), c.Stats())
	}
}

func TestErrorHidesKey(t *testing.T) {
	c := New("secret-key", "ua", "")
	c.BaseURL = "http://127.0.0.1:1"
	_, err := c.Suggest(context.Background(), "ben thanh", nil, "")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
	c.TileURL = "http://127.0.0.1:1"
	_, err = c.Tile(context.Background(), 1, 0, 0)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("tile err = %v", err)
	}
}

func TestNoKey(t *testing.T) {
	c := New("", "ua", "")
	if _, err := c.Resolve(context.Background(), "ben thanh", ""); err != ErrNoKey {
		t.Fatalf("err = %v", err)
	}
	// Coordinates work without a key.
	if ps, err := c.Resolve(context.Background(), "10.85, 106.77", ""); err != nil || len(ps) != 1 || ps[0].Lat != 10.85 {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestRateLimit(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	c := geoapify(t, func(_ string, w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		w.Write([]byte(`{"results":[]}`))
	})
	c.rate.interval = 50 * time.Millisecond
	var wg sync.WaitGroup
	for _, q := range []string{"aa", "bb", "cc"} {
		wg.Go(func() { c.Suggest(context.Background(), q, nil, "") })
	}
	wg.Wait()
	if len(times) != 3 {
		t.Fatalf("requests = %d", len(times))
	}
	if span := times[2].Sub(times[0]); span < 95*time.Millisecond {
		t.Fatalf("3 requests within %v; rate limit not applied", span)
	}
}
