package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const photonBody = `{"features":[
 {"geometry":{"coordinates":[106.740878,10.8023668]},"properties":{"name":"Vincom Megamall Thảo Điền","housenumber":"159-161","street":"Đường Võ Nguyên Giáp","district":"An Khánh","city":"Thành phố Thủ Đức","state":"Thành phố Hồ Chí Minh","countrycode":"VN"}},
 {"geometry":{"coordinates":[106.7406138,10.8020001]},"properties":{"name":"Vincom Mega Mall Thảo Điền","street":"Song Hành","countrycode":"VN"}},
 {"geometry":{"coordinates":[2.35,48.85]},"properties":{"name":"Vincom Paris","countrycode":"FR"}},
 {"geometry":{"coordinates":[105.8,21.0]},"properties":{"name":"","street":"Phố Huế","city":"Hà Nội","countrycode":"VN"}}
]}`

func photonServer(t *testing.T, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Write([]byte(photonBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSuggest(t *testing.T) {
	var calls int
	srv := photonServer(t, func(r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("q") != "vincom thao" || q.Get("lat") != "10.8100" || q.Get("lon") != "106.7100" {
			t.Errorf("query = %v", q)
		}
	})
	c := New("ua", "vn")
	c.PhotonURL = srv.URL
	ps, err := c.Suggest(context.Background(), "  vincom   thao ", &LatLon{10.81, 106.71})
	if err != nil {
		t.Fatal(err)
	}
	// FR result filtered, bus-stop duplicate dropped, nameless street kept by street name.
	if len(ps) != 2 {
		t.Fatalf("places = %+v", ps)
	}
	if ps[0].Address != "159-161 Đường Võ Nguyên Giáp, An Khánh, Thành phố Thủ Đức, Thành phố Hồ Chí Minh" {
		t.Errorf("address = %q", ps[0].Address)
	}
	if ps[0].Lat != 10.8023668 || ps[0].Lon != 106.740878 {
		t.Errorf("coords = %v,%v", ps[0].Lat, ps[0].Lon)
	}
	if ps[1].Name != "Phố Huế" || ps[1].Address != "Hà Nội" {
		t.Errorf("street-only = %+v", ps[1])
	}
	if _, err := c.Suggest(context.Background(), "VINCOM THAO", &LatLon{10.82, 106.72}); err != nil || calls != 1 {
		t.Fatalf("expected cache hit, calls=%d err=%v", calls, err)
	}
}

func TestSuggestSkipsShortCoordsAndLinks(t *testing.T) {
	c := New("ua", "")
	c.PhotonURL = "http://127.0.0.1:1" // any request would fail
	for _, q := range []string{"a", "10.8, 106.7", "https://maps.app.goo.gl/x"} {
		ps, err := c.Suggest(context.Background(), q, nil)
		if err != nil || len(ps) != 0 {
			t.Errorf("%q -> %v %v", q, ps, err)
		}
	}
}

func TestResolveUsesPhoton(t *testing.T) {
	srv := photonServer(t, nil)
	c := New("ua", "vn")
	c.PhotonURL = srv.URL
	c.BaseURL = "http://127.0.0.1:1" // Nominatim must not be needed
	ps, err := c.Resolve(context.Background(), "vincom thảo điền")
	if err != nil || len(ps) == 0 || !strings.HasPrefix(ps[0].Name, "Vincom") {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestSearchTextMergesNominatimFirst(t *testing.T) {
	nom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"name":"Quận 1","display_name":"Quận 1, Hồ Chí Minh","lat":"10.77","lon":"106.70"},
			{"name":"Quận 1 cũ","display_name":"x","lat":"10.78","lon":"106.69"},
			{"name":"Tail","display_name":"y","lat":"10.5","lon":"106.5"},
			{"name":"Vincom Megamall Thảo Điền","display_name":"dup of photon","lat":"10.80237","lon":"106.74088"}
		]`))
	}))
	defer nom.Close()
	c := New("ua", "vn")
	c.BaseURL = nom.URL
	c.PhotonURL = photonServer(t, nil).URL

	ps, err := c.Resolve(context.Background(), "quận 1 hồ chí minh")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	// Two Nominatim hits lead, then autocomplete, then the rest; duplicates
	// across providers appear once; capped at Limit.
	want := []string{"Quận 1", "Quận 1 cũ", "Vincom Megamall Thảo Điền", "Phố Huế", "Tail"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestSearchTextOneProviderDown(t *testing.T) {
	c := New("ua", "vn")
	c.BaseURL = "http://127.0.0.1:1" // Nominatim unreachable
	c.PhotonURL = photonServer(t, nil).URL
	ps, err := c.Resolve(context.Background(), "vincom")
	if err != nil || len(ps) == 0 {
		t.Fatalf("%v %v", ps, err)
	}
}
