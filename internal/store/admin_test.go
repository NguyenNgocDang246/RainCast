package store

import (
	"context"
	"testing"
	"time"
)

func TestLookupsDedupePerFrame(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	frame := time.Unix(6000, 0)
	l := Lookup{At: time.Unix(6100, 0), Lat: 10.8, Lon: 106.7, FrameTime: frame, ArrivalMin: 12, HeavyArrivalMin: -1}
	for range 3 { // the page refreshes every minute
		if err := s.RecordLookup(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	l.FrameTime = time.Unix(6600, 0)
	if err := s.RecordLookup(ctx, l); err != nil {
		t.Fatal(err)
	}
	ls, err := s.Lookups(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 2 || ls[0].FrameTime.Unix() != 6600 || ls[1].ArrivalMin != 12 {
		t.Fatalf("lookups = %+v", ls)
	}
}

func TestRecentIssuesAndCounts(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, at := range []int64{600, 1200} {
		if err := s.SaveIssue(ctx, "a", at, -1, false, []byte(`{"heavy_arrival_min":-1}`),
			[]Forecast{{LeadMin: 10, PredDBZ: 25, PredRain: true}, {LeadMin: 20}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RecordFrame(ctx, 1200, "/p", map[string]float64{"a": 30}, 1); err != nil { // verifies 600+10min
		t.Fatal(err)
	}

	is, err := s.RecentIssues(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(is) != 2 || is[0].IssuedAt.Unix() != 1200 || is[0].Station != "a" || len(is[1].Leads) != 2 {
		t.Fatalf("issues = %+v", is)
	}
	l := is[1].Leads[0]
	if l.ObsDBZ == nil || *l.ObsDBZ != 30 || !*l.ObsRain || is[1].Leads[1].ObsDBZ != nil {
		t.Fatalf("leads = %+v", is[1].Leads)
	}
	if string(is[0].Result) != `{"heavy_arrival_min":-1}` {
		t.Fatalf("result = %s", is[0].Result)
	}

	fs, err := s.RecentFrames(ctx, 10)
	if err != nil || len(fs) != 1 || fs[0].Obs["a"] != 30 {
		t.Fatalf("frames = %+v %v", fs, err)
	}
	c, err := s.Counts(ctx)
	if err != nil || c != (Counts{Frames: 1, Stations: 1, Issues: 2, Forecasts: 4, Verified: 1}) {
		t.Fatalf("counts = %+v %v", c, err)
	}
}
