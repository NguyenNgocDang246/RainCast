package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func clientTilesServer(src *fakeSource) http.Handler {
	return New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{ClientTiles: true})
}

// postTiles uploads parts named name → content.
func postTiles(t *testing.T, h http.Handler, query string, parts map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, content := range parts {
		w, _ := mw.CreateFormFile(name, name+".png")
		io.WriteString(w, content)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/forecast?"+query, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestClientTilesFlow(t *testing.T) {
	src := &fakeSource{frame: 6000}
	h := clientTilesServer(src)

	// 1. No hash: the tiles to download.
	rec := do(t, h, "/api/forecast?lat=10.8&lon=106.7")
	var need TilesNeeded
	if err := json.Unmarshal(rec.Body.Bytes(), &need); err != nil || rec.Code != http.StatusOK ||
		len(need.Plan.Tiles) != 1 || need.Plan.Tiles[0].URL == "" || need.Plan.Frame != 6000 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	if src.refreshed == 0 {
		t.Error("frame index not refreshed")
	}
	// 2. A hash the server has not seen: the plan again.
	if rec := do(t, h, "/api/forecast?lat=10.8&lon=106.7&h=png1"); !strings.Contains(rec.Body.String(), "tiles_needed") {
		t.Fatalf("unknown hash: %d %s", rec.Code, rec.Body)
	}
	// 3. Upload.
	if rec := postTiles(t, h, "lat=10.8&lon=106.7", map[string]string{"6000_101_60": "png1"}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"location"`) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	// 4. Now the hash is known.
	if rec := do(t, h, "/api/forecast?lat=10.8&lon=106.7&h=png1"); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "tiles_needed") {
		t.Fatalf("known hash: %d %s", rec.Code, rec.Body)
	}
}

func TestClientTilesRejects(t *testing.T) {
	src := &fakeSource{frame: 6000}
	h := clientTilesServer(src)
	if rec := postTiles(t, h, "lat=10.8&lon=106.7", map[string]string{"5400_101_60": "png"}); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "tiles_needed") {
		t.Errorf("stale: %d %s", rec.Code, rec.Body)
	}
	if rec := postTiles(t, h, "lat=10.8&lon=106.7", map[string]string{"6000_101_60": "bad"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad tile: %d %s", rec.Code, rec.Body)
	}
	if rec := postTiles(t, h, "lat=10.8&lon=106.7", map[string]string{"tile": "png"}); rec.Code != http.StatusBadRequest {
		t.Errorf("misnamed part: %d %s", rec.Code, rec.Body)
	}
	big := map[string]string{"6000_101_60": strings.Repeat("x", maxTileBytes+1)}
	if rec := postTiles(t, h, "lat=10.8&lon=106.7", big); rec.Code != http.StatusBadRequest {
		t.Errorf("huge tile: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/forecast?lat=10.8&lon=106.7", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("not multipart: %d", rec.Code)
	}
}

// Without ClientTiles a POST is just another forecast request; the server
// downloads the tiles itself.
func TestServerTilesIgnoresUploads(t *testing.T) {
	src := &fakeSource{frame: 6000}
	h := New(src, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	if rec := do(t, h, "/api/forecast?lat=10.8&lon=106.7"); strings.Contains(rec.Body.String(), "tiles_needed") || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestVercelHeaders(t *testing.T) {
	var gotIP string
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		CountryHeader: "X-Vercel-IP-Country", RealIPHeader: "X-Real-IP",
		CountryOf: func(ip netip.Addr) string { gotIP = ip.String(); return "us" },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/geocode?q=x", nil)
	req.Header.Set("X-Vercel-IP-Country", "VN")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"address":"vn"`) {
		t.Errorf("country header ignored: %s", rec.Body)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/geocode?q=x", nil)
	req.Header.Set("X-Real-IP", "203.0.113.9")
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotIP != "203.0.113.9" {
		t.Errorf("client IP = %q, want the X-Real-IP one", gotIP)
	}
}
