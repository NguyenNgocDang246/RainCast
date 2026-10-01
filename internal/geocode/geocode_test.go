package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchParsesAndCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		q := r.URL.Query()
		if q.Get("q") != "chợ bến thành" || q.Get("countrycodes") != "vn" || q.Get("format") != "jsonv2" {
			t.Errorf("query = %v", q)
		}
		w.Write([]byte(`[
			{"name":"Chợ Bến Thành","display_name":"Chợ Bến Thành, Quận 1, Việt Nam","lat":"10.7725","lon":"106.698"},
			{"name":"","display_name":"Đường X, Quận 1","lat":"10.1","lon":"106.1"},
			{"name":"bad","display_name":"bad","lat":"x","lon":"1"}
		]`))
	}))
	defer srv.Close()

	c := New("test-agent", "vn")
	c.BaseURL = srv.URL
	ps, err := c.Search(context.Background(), "  Chợ   Bến Thành ")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "Chợ Bến Thành" || ps[0].Lat != 10.7725 || ps[1].Name != "Đường X" {
		t.Fatalf("places = %+v", ps)
	}
	if _, err := c.Search(context.Background(), "chợ bến thành"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", calls.Load())
	}
}

func TestRateLimit(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New("ua", "")
	c.BaseURL = srv.URL
	c.nominatimRate.interval = 50 * time.Millisecond
	var wg sync.WaitGroup
	for _, q := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Search(context.Background(), q)
		}()
	}
	wg.Wait()
	if len(times) != 3 {
		t.Fatalf("requests = %d", len(times))
	}
	if span := times[2].Sub(times[0]); span < 95*time.Millisecond {
		t.Fatalf("3 requests within %v; rate limit not applied", span)
	}
}

func TestEmptyQuery(t *testing.T) {
	ps, err := New("ua", "").Search(context.Background(), "   ")
	if err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}
}
