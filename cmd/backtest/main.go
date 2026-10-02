// Command backtest scores forecast methods on the radar frames stored by
// cmd/collect (database frame list + tile cache), against each
// other and against persistence on the same cases, over every collected
// region. Run it whenever you want fresh results: it
// keeps the scores in data/backtest_state.gob, so each run only scores
// forecast times collected since the last one, then prints the tables and
// writes data/backtest.json, which the admin page shows.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"
	"time"

	"raincast/internal/backtest"
	"raincast/internal/dotenv"
	"raincast/internal/guard"
	"raincast/internal/pipeline"
	"raincast/internal/store"
	"raincast/internal/verify"
)

func main() {
	// DATABASE_URL comes from the environment or .env.
	if err := dotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "load .env:", err)
		os.Exit(1)
	}
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL of the collected frames, shared with collect (default $DATABASE_URL)")
	dataDir := flag.String("data", "data", "directory for the saved scores and learned weights")
	cacheDir := flag.String("cache", "data/tiles", "tile cache directory")
	step := flag.Int("step", 8, "sample spacing in pixels (~1.2 km each)")
	fresh := flag.Bool("fresh", false, "score everything again instead of adding to the saved scores")
	days := flag.Int("days", 0, "with -fresh: only frames from the last N days (0 = all)")
	out := flag.String("out", "data/backtest.json", "write the report here, for the admin page (empty to skip)")
	parallel := flag.Int("parallel", 0, "regions scored at once (0 = all usable cores)")
	workers := flag.Int("workers", 0, "goroutines per motion field (0 = all usable cores)")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile here (go tool pprof)")
	def := guard.DefaultLimits("data", "data/tiles")
	// Scoring keeps every usable core busy by design: allow it, but not more.
	def.CPUPercent = 65
	// Only cmd/collect stops on battery.
	def.StopOnBattery = false
	limits := guard.Flags(flag.CommandLine, def)
	flag.Parse()

	if err := run(*dbURL, *dataDir, *cacheDir, *out, *cpuProfile, *fresh, *step, *days, *parallel, *workers, limits()); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(dbURL, dataDir, cacheDir, out, cpuProfile string, fresh bool, step, days, parallel, workers int, lim guard.Limits) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			return err
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lim.DiskPath, lim.DirPath = dataDir, cacheDir
	ctx, _ := guard.Start(sigCtx, lim, log)
	lim.Log(log)

	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()

	o := backtest.Options{Step: step, Parallel: parallel, Workers: workers, Keep: pipeline.DefaultConfig().CacheAge,
		WeightsFile: filepath.Join(dataDir, "backtest_weights.gob")}
	if fresh {
		o.Days = days
	} else {
		o.StateFile = filepath.Join(dataDir, "backtest_state.gob")
	}
	rep, err := backtest.RunStored(ctx, st, cacheDir, o)
	if v := guard.Stopped(ctx, log); v != nil {
		return v
	}
	if err != nil {
		return err
	}
	if out != "" {
		if err := backtest.SaveReport(out, rep); err != nil {
			return err
		}
	}
	return printReport(rep, out)
}

// printReport shows the report as tables.
func printReport(rep backtest.Report, out string) error {
	fmt.Printf("frames: %d loaded, %d without cached tiles, %d regions\n", rep.Frames, rep.Skipped, len(rep.Regions))
	if rep.Issues == 0 {
		return fmt.Errorf("no forecast time has 8 pairs of history and 60 min of future; collect more frames")
	}
	fmt.Printf("scored %d forecast times (%d new; %s → %s), up to %d points each, in %s\n",
		rep.Issues, rep.NewIssues, clock(rep.From), clock(rep.To), rep.Points, rep.Duration)
	fmt.Printf("rain events: %d", rep.Events)
	if rep.Events < backtest.MinEvents {
		fmt.Printf(" — fewer than %d: differences between methods may be noise", backtest.MinEvents)
	}
	fmt.Print("\n\n")

	printSummary(rep)
	printTable("CSI theo mốc (càng cao càng tốt)", rep.Results, func(s verify.Scores) *float64 { return s.CSI })
	printTable("Báo động giả FAR (càng thấp càng tốt)", rep.Results, func(s verify.Scores) *float64 { return s.FAR })
	printTable("Bắt được mưa POD (càng cao càng tốt)", rep.Results, func(s verify.Scores) *float64 { return s.POD })
	for _, g := range rep.Groups {
		printTable(fmt.Sprintf("CSI — %s (%d vùng, %d đợt mưa)", g.Climate, g.Regions, g.Events), g.Results,
			func(s verify.Scores) *float64 { return s.CSI })
	}

	printCorr(rep)

	if out != "" {
		fmt.Println("report written to", out)
	}
	return nil
}

// printSummary shows, per setting, the overall CSI with its 95% interval,
// the difference from the reference with its interval, probability skill
// and cost.
func printSummary(rep backtest.Report) {
	fmt.Printf("Tổng hợp (so với %s; khoảng tin cậy 95%% từ %d khối 6 giờ)\n", rep.Reference, rep.Blocks)
	fmt.Printf("  %-28s%16s%20s%8s%8s%8s%9s%9s\n", "", "CSI [95%]", "Δ CSI [95%]", "BSS", "BSS hc", "AUC", "mm 60'", "ms/lần")
	for _, r := range rep.Results {
		csi := pct(r.Overall.CSI)
		if r.CSICI != nil {
			csi += fmt.Sprintf(" [%.0f–%.0f]", r.CSICI[0]*100, r.CSICI[1]*100)
		}
		delta := ""
		if r.DeltaCSI != nil {
			delta = fmt.Sprintf("%+.1f", *r.DeltaCSI*100)
			if r.DeltaCSICI != nil {
				delta += fmt.Sprintf(" [%+.1f, %+.1f]", r.DeltaCSICI[0]*100, r.DeltaCSICI[1]*100)
			}
		}
		var bssCal *float64
		if r.BrierCal != nil && rep.Results[0].Brier != nil && *rep.Results[0].Brier > 0 {
			v := 1 - *r.BrierCal / *rep.Results[0].Brier
			bssCal = &v
		}
		fmt.Printf("  %-28s%16s%20s%8s%8s%8s%9s%9.0f\n", r.Name, csi, delta, num(r.BSS), num(bssCal), num(r.AUC), num(r.AccumMAE), r.MsPerIssue)
	}
	fmt.Println("  BSS: kỹ năng xác suất so với giữ nguyên (càng cao càng tốt); hc: sau hiệu chỉnh.")
	fmt.Println("  mm 60': sai số tuyệt đối trung bình lượng mưa 1 giờ so với radar (càng thấp càng tốt).")
	fmt.Println()
}

// printCorr shows how alike the errors of the main settings are.
func printCorr(rep backtest.Report) {
	ec := rep.ErrCorr
	if ec == nil {
		return
	}
	var idx []int
	for i, r := range rep.Results {
		if !r.Trend && r.Method != "persistence" && r.Method != "ensemble" && r.Method != "vote" &&
			(r.Method != "trec" || r.Name == rep.Reference) {
			idx = append(idx, i)
		}
	}
	fmt.Println("Tương quan sai số ở +30' (thành viên ensemble; < 0.9 là bổ sung thông tin)")
	fmt.Printf("  %-16s", "")
	for _, j := range idx {
		fmt.Printf("%8.7s", ec.Names[j])
	}
	fmt.Println()
	for _, i := range idx {
		fmt.Printf("  %-16.16s", ec.Names[i])
		for _, j := range idx {
			fmt.Printf("%8s", num(ec.R[i][j]))
		}
		fmt.Println()
	}
	fmt.Println()
}

func num(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmt.Sprintf("%.2f", *v)
}

func printTable(title string, results []backtest.Result, metric func(verify.Scores) *float64) {
	if len(results) == 0 {
		return
	}
	fmt.Println(title)
	fmt.Printf("  %-28s", "")
	for _, l := range results[0].Leads {
		fmt.Printf("%7s", fmt.Sprintf("+%d'", l.LeadMin))
	}
	fmt.Printf("%9s\n", "tổng")
	for _, r := range results {
		fmt.Printf("  %-28s", r.Name)
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

func clock(t int64) string { return time.Unix(t, 0).Format("02/01 15:04") }
