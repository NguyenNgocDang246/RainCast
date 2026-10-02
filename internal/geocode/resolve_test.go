package geocode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseCoords(t *testing.T) {
	for _, s := range []string{"10.85, 106.77", "10.85,106.77", " 10.85 106.77 ", "10.85;106.77"} {
		if lat, lon, ok := ParseCoords(s); !ok || lat != 10.85 || lon != 106.77 {
			t.Errorf("%q -> %v %v %v", s, lat, lon, ok)
		}
	}
	for _, s := range []string{"Thủ Đức", "100, 10", "10.8", "10.85, 106.77 abc"} {
		if _, _, ok := ParseCoords(s); ok {
			t.Errorf("%q parsed as coords", s)
		}
	}
}

func TestParseMapsURL(t *testing.T) {
	cases := []struct {
		url       string
		lat, lon  float64
		name      string
		hasCoords bool
	}{
		// Pinned place wins over the viewport center.
		{"https://www.google.com/maps/place/Ch%E1%BB%A3+B%E1%BA%BFn+Th%C3%A0nh/@10.7720,106.6960,17z/data=!3m1!4b1!4m6!3m5!1s0x0:0x0!8m2!3d10.7725301!4d106.6980365",
			10.7725301, 106.6980365, "Chợ Bến Thành", true},
		{"https://www.google.com/maps/@10.85,106.77,15z", 10.85, 106.77, "10.85000, 106.77000", true},
		{"https://maps.google.com/?q=10.8231,106.6297", 10.8231, 106.6297, "10.82310, 106.62970", true},
		{"https://www.google.com/maps/search/?api=1&query=10.1,106.2", 10.1, 106.2, "10.10000, 106.20000", true},
		{"https://maps.google.com/maps?q=Landmark+81&ftid=0x1:0x2", 0, 0, "Landmark 81", false},
	}
	for _, c := range cases {
		u, _ := url.Parse(c.url)
		p, ok := ParseMapsURL(u)
		if ok != c.hasCoords || p.Lat != c.lat || p.Lon != c.lon || p.Name != c.name {
			t.Errorf("%s\n got %+v ok=%v", c.url, p, ok)
		}
	}
}

func TestResolveShortLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://www.google.com/maps/place/X/data=!3d10.5!4d106.5", http.StatusFound)
	}))
	defer srv.Close()
	su, _ := url.Parse(srv.URL)

	c := New("", "ua", "vn")
	c.ShortLinkHosts[su.Hostname()] = true
	ps, err := c.Resolve(context.Background(), "Chỗ X\n"+srv.URL+"/abc", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Lat != 10.5 || ps[0].Lon != 106.5 || ps[0].Name != "X" {
		t.Fatalf("places = %+v", ps)
	}
}

func TestResolveDoesNotFetchOtherHosts(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	c := New("", "ua", "")
	_, err := c.Resolve(context.Background(), srv.URL+"/maps/nothing", "")
	if !errors.Is(err, ErrUnresolvedLink) || hit {
		t.Fatalf("err=%v hit=%v", err, hit)
	}
}

func TestResolveNameLinkFallsBackToSearch(t *testing.T) {
	c := geoapify(t, func(endpoint string, w http.ResponseWriter, r *http.Request) {
		if endpoint != "search" || !strings.Contains(r.URL.Query().Get("text"), "Landmark 81") {
			t.Errorf("%s %v", endpoint, r.URL.Query())
		}
		w.Write([]byte(`{"results":[{"address_line1":"Landmark 81","address_line2":"Bình Thạnh","lat":10.795,"lon":106.722}]}`))
	})
	ps, err := c.Resolve(context.Background(), "https://maps.google.com/maps?q=Landmark+81", "")
	if err != nil || len(ps) != 1 || ps[0].Lat != 10.795 {
		t.Fatalf("%+v %v", ps, err)
	}
}
