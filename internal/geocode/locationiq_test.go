package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const liqBody = `[
 {"lat":"10.7725","lon":"106.698","display_place":"Chợ Bến Thành","display_address":"Lê Lợi, Bến Thành, Hồ Chí Minh"},
 {"lat":"10.7726","lon":"106.6981","display_place":"Chợ Bến Thành","display_address":"duplicate a few meters away"},
 {"lat":"bad","lon":"1","display_place":"broken"}
]`

// providers starts fake LocationIQ and Photon servers; liq decides the
// LocationIQ response.
func providers(t *testing.T, liq http.HandlerFunc) (*Client, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var liqCalls, photonCalls atomic.Int32
	l := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liqCalls.Add(1)
		liq(w, r)
	}))
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		photonCalls.Add(1)
		w.Write([]byte(photonBody))
	}))
	t.Cleanup(l.Close)
	t.Cleanup(p.Close)
	c := New("ua", "vn")
	c.LocationIQKey = "k"
	c.LocationIQURL = l.URL
	c.PhotonURL = p.URL
	c.photonRate.interval = 0
	return c, &liqCalls, &photonCalls
}

func TestLocationIQPreferred(t *testing.T) {
	c, liq, photon := providers(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") != "k" || q.Get("q") != "ben thanh" || q.Get("countrycodes") != "vn" ||
			q.Get("dedupe") != "1" || q.Get("viewbox") != "106.2000,10.3000,107.2000,11.3000" {
			t.Errorf("query = %v", q)
		}
		w.Write([]byte(liqBody))
	})
	ps, err := c.Suggest(context.Background(), "ben thanh", &LatLon{10.8, 106.7})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "Chợ Bến Thành" || ps[0].Address != "Lê Lợi, Bến Thành, Hồ Chí Minh" || ps[0].Lat != 10.7725 {
		t.Fatalf("places = %+v", ps)
	}
	if liq.Load() != 1 || photon.Load() != 0 {
		t.Fatalf("calls liq=%d photon=%d", liq.Load(), photon.Load())
	}
}

func TestLocationIQNoMatch(t *testing.T) {
	c, _, photon := providers(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Unable to geocode"}`, http.StatusNotFound)
	})
	ps, err := c.Suggest(context.Background(), "zzzz", nil)
	if err != nil || len(ps) != 0 || photon.Load() != 0 {
		t.Fatalf("%+v %v photon=%d", ps, err, photon.Load())
	}
}

func TestLocationIQ429FallsBackAndCoolsDown(t *testing.T) {
	c, liq, photon := providers(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"Rate Limited Minute"}`))
	})
	ps, err := c.Suggest(context.Background(), "vincom", nil)
	if err != nil || len(ps) == 0 || photon.Load() != 1 {
		t.Fatalf("fallback: %+v %v photon=%d", ps, err, photon.Load())
	}
	// During the cool-down LocationIQ is skipped entirely.
	if _, err := c.Suggest(context.Background(), "vincom 2", nil); err != nil {
		t.Fatal(err)
	}
	if liq.Load() != 1 || photon.Load() != 2 {
		t.Fatalf("calls liq=%d photon=%d", liq.Load(), photon.Load())
	}
}

func TestLocationIQLocalQuotaFallsBack(t *testing.T) {
	c, liq, photon := providers(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(liqBody))
	})
	for _, q := range []string{"a1", "a2", "a3"} {
		if _, err := c.Suggest(context.Background(), q, nil); err != nil {
			t.Fatal(err)
		}
	}
	// 2 per second: the third query goes to Photon without waiting.
	if liq.Load() != 2 || photon.Load() != 1 {
		t.Fatalf("calls liq=%d photon=%d", liq.Load(), photon.Load())
	}
}

func TestNoKeyUsesPhoton(t *testing.T) {
	c, liq, photon := providers(t, func(w http.ResponseWriter, r *http.Request) {})
	c.LocationIQKey = ""
	if _, err := c.Suggest(context.Background(), "vincom", nil); err != nil {
		t.Fatal(err)
	}
	if liq.Load() != 0 || photon.Load() != 1 {
		t.Fatalf("calls liq=%d photon=%d", liq.Load(), photon.Load())
	}
}

func TestLocationIQErrorHidesKey(t *testing.T) {
	c := New("ua", "")
	c.LocationIQKey = "secret-key"
	c.LocationIQURL = "http://127.0.0.1:1/autocomplete"
	_, err := c.locationIQ(context.Background(), "x", nil, 5)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestQuota(t *testing.T) {
	q := quota{perSec: 2, perMin: 3}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if !q.tryAcquire(t0) || !q.tryAcquire(t0.Add(100*time.Millisecond)) {
		t.Fatal("first two should pass")
	}
	if q.tryAcquire(t0.Add(500 * time.Millisecond)) {
		t.Fatal("third within a second should fail")
	}
	if !q.tryAcquire(t0.Add(1100 * time.Millisecond)) {
		t.Fatal("next second should pass")
	}
	if q.tryAcquire(t0.Add(3 * time.Second)) {
		t.Fatal("per-minute cap of 3 should fail")
	}
	if !q.tryAcquire(t0.Add(61 * time.Second)) {
		t.Fatal("after a minute should pass")
	}

	q = quota{perSec: 2}
	q.coolDown(t0, "Rate Limited Day")
	if q.tryAcquire(t0.Add(11*time.Hour + 59*time.Minute)) {
		t.Fatal("daily cool-down should last until midnight UTC")
	}
	if !q.tryAcquire(t0.Add(12*time.Hour + time.Second)) {
		t.Fatal("should resume after midnight UTC")
	}
}

func TestStatsCountProviders(t *testing.T) {
	c, _, _ := providers(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(liqBody))
	})
	for _, q := range []string{"a1", "a2", "a3", "a1"} { // 2 LIQ, 1 over quota, 1 cached
		if _, err := c.Suggest(context.Background(), q, nil); err != nil {
			t.Fatal(err)
		}
	}
	st := c.Stats()
	if !st.LocationIQEnabled || st.LocationIQOK != 2 || st.LocationIQLimited != 1 || st.PhotonOK != 1 || st.CacheHits != 1 {
		t.Fatalf("stats = %+v", st)
	}
}
