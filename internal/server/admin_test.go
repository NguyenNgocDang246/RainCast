package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"raincast/internal/backtest"
	"raincast/internal/store"
)

func adminServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(&fakeSource{}, fakeGeo{}, st, log, Config{HorizonMin: 60}), st
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminEndpoints(t *testing.T) {
	h, _ := adminServer(t)
	rec := get(h, "/api/admin/overview")
	var ov Overview
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ov) != nil || ov.Pipeline.Ticks != 3 || ov.Geocoding.PhotonOK != 7 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/admin/accuracy", "/api/admin/issues", "/api/admin/frames", "/api/admin/lookups"} {
		if rec := get(h, path); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// Moved out of the public API.
	for _, path := range []string{"/api/accuracy", "/api/radar.png"} {
		if rec := get(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s still public: %d", path, rec.Code)
		}
	}
}

func TestForecastLogsLookups(t *testing.T) {
	h, st := adminServer(t)
	get(h, "/api/forecast?lat=10.8&lon=106.7")
	get(h, "/api/admin/forecast?lat=11&lon=107") // admin tool: not logged
	ls, err := st.Lookups(context.Background(), 10)
	if err != nil || len(ls) != 1 || ls[0].Lat != 10.8 {
		t.Fatalf("lookups = %+v %v", ls, err)
	}
}

func TestBacktestEndpoints(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), 20)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	file := filepath.Join(t.TempDir(), "bt.json")
	h := New(&fakeSource{}, fakeGeo{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		HorizonMin: 60, BacktestFile: file,
		Backtest: func(context.Context) (backtest.Report, error) { return backtest.Report{Issues: 7}, nil },
	})

	if rec := get(h, "/api/admin/backtest"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Fatalf("before run: %d %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/admin/backtest", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issues":7`) {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}
	// The saved report is served afterwards.
	if rec := get(h, "/api/admin/backtest"); !strings.Contains(rec.Body.String(), `"issues": 7`) {
		t.Fatalf("after run: %s", rec.Body)
	}
}
