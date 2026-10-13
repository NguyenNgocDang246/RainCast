package bench

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"raincast/internal/store"
)

var t0 = time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)

func serve(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if body == "429" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func byLead(s []Sample, now time.Time) map[int]Sample {
	out := map[int]Sample{}
	for _, x := range s {
		out[int(x.Valid.Sub(now)/time.Minute)] = x
	}
	return out
}

func TestTomorrowMinutely(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"timelines":{"minutely":[`)
	for m := 0; m < 60; m++ { // minutes 0–59, as Tomorrow.io sends
		if m > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"time":%q,"values":{"rainIntensity":%d,"snowIntensity":0}}`, t0.Add(time.Duration(m)*time.Minute).Format(time.RFC3339), m)
	}
	b.WriteString(`],"hourly":[]}}`)
	srv := serve(t, map[string]string{"/v4/weather/forecast": b.String()})
	tm := &Tomorrow{Key: "k", Base: srv.URL, HTTP: srv.Client()}
	s, err := tm.Forecast(context.Background(), 10.7, 106.7, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := byLead(s, t0)
	if len(got) != 7 || got[0].MMH != 0 || got[30].MMH != 30 || got[60].MMH != 59 || got[60].Kind != KindMinutely {
		t.Fatalf("samples = %+v", s)
	}
}

func TestTomorrowFreePlan(t *testing.T) {
	hourly := fmt.Sprintf(`{"timelines":{"hourly":[
		{"time":%q,"values":{"precipitationIntensity":0}},
		{"time":%q,"values":{"precipitationIntensity":6}}]}}`,
		t0.Format(time.RFC3339), t0.Add(time.Hour).Format(time.RFC3339))
	srv := serve(t, map[string]string{
		"/v4/weather/forecast": hourly,
		"/v4/weather/realtime": `{"data":{"time":"2026-10-14T05:58:00Z","values":{"precipitationIntensity":1.5}}}`,
	})
	tm := &Tomorrow{Key: "k", Base: srv.URL, HTTP: srv.Client()}
	s, err := tm.Forecast(context.Background(), 10.7, 106.7, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := byLead(s, t0)
	if got[0].MMH != 1.5 || got[0].Kind != KindRealtime {
		t.Errorf("now = %+v", got[0])
	}
	if got[30].MMH != 3 || got[30].Kind != KindHourly || got[60].MMH != 6 {
		t.Errorf("hourly = %+v", s)
	}
}

func TestRainbow(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"forecast":[`)
	// Starts a minute after now: the first entry still stands for now.
	for m := 1; m <= 240; m++ {
		if m > 1 {
			b.WriteString(",")
		}
		begin := t0.Add(time.Duration(m) * time.Minute).Unix()
		fmt.Fprintf(&b, `{"timestampBegin":%d,"timestampEnd":%d,"precipRate":%d,"precipType":"rain"}`, begin, begin+60, m)
	}
	b.WriteString(`]}`)
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Ocp-Apim-Subscription-Key")
		if r.URL.Path != "/nowcast/v1/precip/106.7000/10.7000" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(b.String()))
	}))
	defer srv.Close()
	rb := &Rainbow{Token: "tok", Base: srv.URL, HTTP: srv.Client()}
	s, err := rb.Forecast(context.Background(), 10.7, 106.7, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := byLead(s, t0)
	if gotKey != "tok" || len(got) != 7 || got[0].MMH != 1 || got[20].MMH != 20 {
		t.Fatalf("key %q, samples = %+v", gotKey, s)
	}
}

func TestRateLimitAndRedact(t *testing.T) {
	srv := serve(t, map[string]string{"/v4/weather/forecast": "429"})
	tm := &Tomorrow{Key: "secret", Base: srv.URL, HTTP: srv.Client()}
	_, err := tm.Forecast(context.Background(), 10.7, 106.7, t0)
	if wait, ok := RateLimited(err); !ok || wait != 2*time.Minute {
		t.Fatalf("err = %v", err)
	}

	tm.Base = "http://127.0.0.1:1" // nothing listens
	_, err = tm.Forecast(context.Background(), 10.7, 106.7, t0)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

type fixed struct{ calls int }

func (f *fixed) Name() string { return "fixed" }
func (f *fixed) Forecast(context.Context, float64, float64, time.Time) ([]Sample, error) {
	f.calls++
	return nil, nil
}

func TestMonthlyCap(t *testing.T) {
	f := &fixed{}
	c := &MonthlyCap{Provider: f, Limit: 3, Count: func(context.Context, time.Time) (int, error) { return 1, nil }}
	ctx := context.Background()
	for range 3 {
		c.Forecast(ctx, 0, 0, t0)
	}
	if _, err := c.Forecast(ctx, 0, 0, t0); !errors.Is(err, ErrBudget) || f.calls != 2 {
		t.Fatalf("err %v after %d calls", err, f.calls)
	}
	// A new month starts over (from what was already counted).
	if _, err := c.Forecast(ctx, 0, 0, t0.AddDate(0, 1, 0)); err != nil || f.calls != 3 {
		t.Fatalf("next month: err %v, %d calls", err, f.calls)
	}
}

// rows makes a provider's forecasts issued at issue: mmh(valid) for each
// slot.
func rows(provider string, issue time.Time, mmh func(valid time.Time) float64) []store.BenchForecast {
	var out []store.BenchForecast
	for _, v := range slots(issue) {
		out = append(out, store.BenchForecast{Provider: provider, Station: "q1", Issued: issue.Unix(), Valid: v.Unix(), MMH: mmh(v), Kind: KindMinutely})
	}
	return out
}

func TestScore(t *testing.T) {
	// Rain from 07:00 on, per RainViewer and Rainbow. Rainbow forecasts it
	// perfectly; RainCast never forecasts rain; Tomorrow always does.
	rain := func(v time.Time) float64 {
		if v.Before(t0.Add(time.Hour)) {
			return 0
		}
		return 5
	}
	var fc []store.BenchForecast
	var obs []store.BenchObs
	for i := range 19 { // 06:00 … 09:00
		issue := t0.Add(time.Duration(i) * Step)
		fc = append(fc, rows(NameRainbow, issue, rain)...)
		fc = append(fc, rows(NameTomorrow, issue, func(time.Time) float64 { return 5 })...)
		rc := rows(NameRainCast, issue, func(time.Time) float64 { return 0 })
		fc = append(fc, rc[1:]...) // RainCast has no "now" of its own
		dbz := float64(-32)
		if rain(issue) > 0 {
			dbz = 40
		}
		obs = append(obs, store.BenchObs{Station: "q1", Frame: issue.Unix(), DBZ: dbz})
	}
	// A RainCast forecast missing at one lead drops that sample for everyone.
	fc = slices.DeleteFunc(fc, func(f store.BenchForecast) bool {
		return f.Provider == NameRainCast && f.Issued == t0.Add(time.Hour).Unix() && f.Valid == t0.Add(90*time.Minute).Unix()
	})

	rep := Score(fc, obs, nil, t0.Add(4*time.Hour))
	if len(rep.Tables) != 3 || rep.Tables[0].Truth != NameRainViewer || rep.Tables[1].Truth != NameTomorrow || rep.Tables[2].Truth != NameRainbow {
		t.Fatalf("tables = %+v", rep.Tables)
	}
	if len(rep.Leads) != 6 || rep.Leads[0] != 10 || rep.Leads[5] != 60 {
		t.Fatalf("leads = %v", rep.Leads)
	}
	rowOf := func(tb Table, name string) Row {
		for _, r := range tb.Rows[1:] {
			if r.Forecaster == name {
				return r
			}
		}
		t.Fatalf("no row %s in %s", name, tb.Truth)
		return Row{}
	}
	rv := rep.Tables[0]
	if !rv.Rows[0].Persistence || rv.Rows[0].Forecaster != NameRainViewer {
		t.Errorf("row 0 = %+v", rv.Rows[0])
	}
	if csi := rowOf(rv, NameRainbow).Overall.CSI; csi == nil || *csi != 1 {
		t.Errorf("rainbow vs radar CSI = %v", csi)
	}
	if csi := rowOf(rv, NameRainCast).Overall.CSI; csi == nil || *csi != 0 {
		t.Errorf("raincast vs radar CSI = %v", csi)
	}
	// Lead 30: valid 06:30 … 09:00 → 16 times, minus the dropped one.
	if n := rowOf(rv, NameRainbow).Leads[2].Scores.N; n != 15 {
		t.Errorf("lead 30 samples = %d, want 15", n)
	}
	// Against Tomorrow's own "now" (always rain), Tomorrow is perfect.
	if csi := rowOf(rep.Tables[1], NameTomorrow).Overall.CSI; csi == nil || *csi != 1 {
		t.Errorf("tomorrow vs itself CSI = %v", csi)
	}
	if mae := rowOf(rep.Tables[2], NameRainbow).MAE; mae == nil || *mae != 0 {
		t.Errorf("rainbow vs itself MAE = %v", mae)
	}
	if len(rep.Providers) != 3 || rep.Providers[0].Name != NameRainCast || rep.Providers[0].Issues != 19 {
		t.Errorf("providers = %+v", rep.Providers)
	}
}

func TestMetarRain(t *testing.T) {
	for wx, want := range map[string]bool{
		"":         false,
		"-RA":      true,
		"+TSRA BR": true,
		"SHRA":     true,
		"-DZ":      true,
		"VCSH":     false,
		"VCTS BR":  false,
		"TS":       false,
		"BR HZ":    false,
		"RERA":     false,
		"-TSRAGR":  true,
		"FG -SHRA": true,
	} {
		if got := MetarRain(wx); got != want {
			t.Errorf("MetarRain(%q) = %v, want %v", wx, got, want)
		}
	}
}

func TestMETARRecent(t *testing.T) {
	srv := serve(t, map[string]string{"/api/data/metar": `[
		{"icaoId":"VTBS","obsTime":1791919800,"wxString":"-SHRA","rawOb":"METAR VTBS 131930Z -SHRA"},
		{"icaoId":"VTBS","obsTime":1791918000,"wxString":null,"rawOb":"METAR VTBS 131900Z"}]`})
	m := &METAR{Base: srv.URL, HTTP: srv.Client()}
	reps, err := m.Recent(context.Background(), []string{"VTBS"}, 2)
	if err != nil || len(reps) != 2 || !reps[0].Rain || reps[1].Rain || reps[0].Obs != 1791919800 {
		t.Fatalf("reps = %+v %v", reps, err)
	}
	if !IsICAO("VTBS") || IsICAO("bangkok") || IsICAO("VTB") {
		t.Error("IsICAO")
	}
}

func TestScoreMETAR(t *testing.T) {
	// Half-hourly METAR: rain reported from 07:00 on. Rainbow forecasts
	// 0.6 mm/h (rain at METAR's 0.5) from 07:00; RainCast never forecasts it.
	var metars []store.BenchMetar
	for i := range 7 { // 06:00 … 09:00
		v := t0.Add(time.Duration(i) * 30 * time.Minute)
		metars = append(metars, store.BenchMetar{Station: "q1", Obs: v.Unix(), Rain: !v.Before(t0.Add(time.Hour))})
	}
	var fc []store.BenchForecast
	for i := range 19 {
		issue := t0.Add(time.Duration(i) * Step)
		fc = append(fc, rows(NameRainbow, issue, func(v time.Time) float64 {
			// Matched to the nearest report: 06:50 → 07:00.
			if v.Before(t0.Add(50 * time.Minute)) {
				return 0
			}
			return 0.6
		})...)
		fc = append(fc, rows(NameRainCast, issue, func(time.Time) float64 { return 0 })[1:]...)
	}
	rep := Score(fc, nil, metars, t0.Add(4*time.Hour))
	tb := rep.Tables[0]
	if tb.Truth != NameMETAR || tb.ThresholdMMH != MetarThresholdMMH {
		t.Fatalf("table 0 = %s %v", tb.Truth, tb.ThresholdMMH)
	}
	// 05:50 … 09:10 on the 10′ grid, each within 10′ of a report.
	if tb.Times != 21 {
		t.Errorf("times = %d, want 21", tb.Times)
	}
	for _, r := range tb.Rows {
		switch {
		case r.Persistence:
		case r.Forecaster == NameRainbow:
			if f := r.Overall.F1; f == nil || *f != 1 {
				t.Errorf("rainbow F1 = %v", f)
			}
		case r.Forecaster == NameRainCast:
			if f := r.Overall.F1; f == nil || *f != 0 {
				t.Errorf("raincast F1 = %v", f)
			}
		}
		if r.MAE != nil {
			t.Errorf("%s MAE = %v, want none against METAR", r.Forecaster, *r.MAE)
		}
	}
}
