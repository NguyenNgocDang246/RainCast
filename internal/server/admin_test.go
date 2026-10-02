package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"raincast/internal/backtest"
	"raincast/internal/store"
)

func adminServer(t *testing.T, st *store.Store) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(&fakeSource{}, fakeGeo{}, st, log, Config{
		Admin:    true,
		Backtest: func() (*backtest.Report, error) { return &backtest.Report{Issues: 7}, nil },
	})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminWithDatabase(t *testing.T) {
	st, err := store.Open(store.TestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.RecordFrame(context.Background(), 600, "/p"); err != nil {
		t.Fatal(err)
	}
	h := adminServer(t, st)
	var ov Overview
	rec := get(h, "/api/admin/overview")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ov) != nil || ov.Pipeline.Ticks != 3 ||
		ov.Geocoding.SearchOK != 7 || ov.Counts == nil || ov.Counts.Frames != 1 {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/admin/frames"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"/p"`) {
		t.Fatalf("frames: %d %s", rec.Code, rec.Body)
	}
}

// Without a database the admin still shows the pipeline and the report.
func TestAdminWithoutDatabase(t *testing.T) {
	h := adminServer(t, nil)
	if rec := get(h, "/api/admin/overview"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"counts":null`) {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/admin/frames"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("frames: %d %s", rec.Code, rec.Body)
	}
	rec := get(h, "/api/admin/backtest")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issues":7`) {
		t.Fatalf("backtest: %d %s", rec.Code, rec.Body)
	}
	// Runs are background-only: nothing starts one on request.
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/admin/backtest", nil))
	if post.Code == http.StatusOK {
		t.Fatalf("POST still runs a backtest: %d", post.Code)
	}
}

// Production serves no admin API at all.
func TestNoAdminInProduction(t *testing.T) {
	h := New(&fakeSource{}, fakeGeo{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{})
	for _, path := range []string{"/api/admin/overview", "/api/admin/forecast?lat=1&lon=2", "/api/admin/backtest"} {
		if rec := get(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
	if rec := get(h, "/api/forecast?lat=1&lon=2"); rec.Code != http.StatusOK {
		t.Errorf("public API: %d", rec.Code)
	}
}
