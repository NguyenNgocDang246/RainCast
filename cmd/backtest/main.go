// Command backtest re-scores the nowcast on radar frames already stored by
// raincast (DB frame list + tile cache), comparing several model settings
// against each other and against persistence on the same cases. The admin
// page can run the same thing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/store"
	"raincast/internal/verify"
)

func main() {
	lat := flag.Float64("lat", 10.85, "center latitude (the primary station)")
	lon := flag.Float64("lon", 106.77, "center longitude")
	dbPath := flag.String("db", "data/raincast.db", "raincast SQLite database (frame list)")
	cacheDir := flag.String("cache", "data/tiles", "tile cache directory")
	step := flag.Int("step", 8, "sample spacing in pixels (~1.2 km each)")
	out := flag.String("out", "data/backtest.json", "write the report here as JSON (empty to skip)")
	flag.Parse()

	if err := run(*lat, *lon, *dbPath, *cacheDir, *step, *out); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(lat, lon float64, dbPath, cacheDir string, step int, out string) error {
	st, err := store.Open(dbPath, 20)
	if err != nil {
		return err
	}
	defer st.Close()
	rep, err := backtest.RunStored(context.Background(), st, cacheDir, lat, lon, step)
	if err != nil {
		return err
	}
	fmt.Printf("frames: %d loaded, %d without cached tiles\n", rep.Frames, rep.Skipped)
	if rep.Issues == 0 {
		return fmt.Errorf("no forecast time has 8 pairs of history and 60 min of future; collect more frames")
	}
	fmt.Printf("scored %d forecast times (%s → %s), %d points each, in %s\n\n",
		rep.Issues, clock(rep.From), clock(rep.To), rep.Points, rep.Duration)

	printTable("CSI theo mốc (càng cao càng tốt)", rep, func(s verify.Scores) *float64 { return s.CSI })
	printTable("Báo động giả FAR (càng thấp càng tốt)", rep, func(s verify.Scores) *float64 { return s.FAR })
	printTable("Bắt được mưa POD (càng cao càng tốt)", rep, func(s verify.Scores) *float64 { return s.POD })

	if out != "" {
		js, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(out, js, 0o644); err != nil {
			return err
		}
		fmt.Println("report written to", out)
	}
	return nil
}

func printTable(title string, rep backtest.Report, metric func(verify.Scores) *float64) {
	fmt.Println(title)
	fmt.Printf("  %-22s", "")
	for _, l := range rep.Results[0].Leads {
		fmt.Printf("%7s", fmt.Sprintf("+%d'", l.LeadMin))
	}
	fmt.Printf("%9s\n", "tổng")
	for _, r := range rep.Results {
		fmt.Printf("  %-22s", r.Name)
		for _, l := range r.Leads {
			fmt.Printf("%7s", pct(metric(l.Scores)))
		}
		fmt.Printf("%9s\n", pct(metric(r.Overall)))
	}
	fmt.Println()
}

func pct(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

func clock(t int64) string { return time.Unix(t, 0).Format("15:04") }
