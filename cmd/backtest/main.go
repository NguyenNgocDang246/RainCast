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
	"strings"
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
	dataDir := flag.String("data", "data", "directory for the saved scores")
	cacheDir := flag.String("cache", "data/tiles", "tile cache directory")
	step := flag.Int("step", 8, "sample spacing in pixels (~1.2 km each)")
	fresh := flag.Bool("fresh", false, "score everything again instead of adding to the saved scores")
	days := flag.Int("days", 0, "with -fresh: only frames from the last N days (0 = all)")
	out := flag.String("out", "data/backtest.json", "write the report here, for the admin page (empty to skip)")
	parallel := flag.Int("parallel", 0, "regions scored at once (0 = all usable cores)")
	workers := flag.Int("workers", 0, "goroutines per motion field (0 = all usable cores)")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile here (go tool pprof)")
	prune := flag.Bool("prune", false, "list the frames, tiles and regions no backtest can use (nothing from the last 3 hours); with -yes, delete them")
	yes := flag.Bool("yes", false, "with -prune: really delete")
	paper := flag.Bool("paper", false, "score the way published nowcast verifications do (one pixel per sample, leads to 90 min, mm/h thresholds), into its own state and -out data/backtest-paper.json")
	inventory := flag.Bool("inventory", false, "only report what collect has cached per region: frames, unbroken runs and how many the backtest can use; scores nothing")
	def := guard.DefaultLimits("data", "data/tiles")
	// Scoring keeps every usable core busy by design: allow it, but not more.
	def.CPUPercent = 65
	// Only cmd/collect stops on battery.
	def.StopOnBattery = false
	limits := guard.Flags(flag.CommandLine, def)
	flag.Parse()

	if *prune {
		if err := runPrune(*dbURL, *cacheDir, *step, *yes); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *inventory {
		if err := runInventory(*dbURL, *cacheDir, *step); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *paper && !flagSet("out") {
		*out = filepath.Join(*dataDir, "backtest-paper.json")
	}
	if err := run(*dbURL, *dataDir, *cacheDir, *out, *cpuProfile, *fresh, *paper, *step, *days, *parallel, *workers, limits()); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(dbURL, dataDir, cacheDir, out, cpuProfile string, fresh, paper bool, step, days, parallel, workers int, lim guard.Limits) error {
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

	o := backtest.Options{Step: step, Parallel: parallel, Workers: workers, Keep: pipeline.DefaultConfig().CacheAge}
	stateFile := "backtest_state.gob"
	if paper {
		cfg := backtest.PaperConfig(step)
		o.Config = &cfg
		stateFile = "backtest_paper_state.gob"
	}
	if fresh {
		o.Days = days
	} else {
		o.StateFile = filepath.Join(dataDir, stateFile)
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

// runInventory prints, per region, the frames in the tile cache, the
// unbroken runs they form and how many the backtest can issue from.
func runInventory(dbURL, cacheDir string, step int) error {
	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	cfg := backtest.DefaultConfig(step)
	inv, err := backtest.Inventory(context.Background(), st, cacheDir, cfg)
	if err != nil {
		return err
	}
	share := func(a, b int) string {
		if b == 0 {
			return "–"
		}
		return fmt.Sprintf("%.0f%%", 100*float64(a)/float64(b))
	}
	fmt.Printf("%-16s %-12s %8s %13s %7s %9s %12s  %s\n", "vùng", "khí hậu", "frame", "có tile", "đoạn", "dài nhất", "dùng được", "các đoạn liên tục")
	var exp, cached, use, runs int
	for _, r := range inv {
		longest := 0
		var parts []string
		for _, s := range r.Runs {
			longest = max(longest, s.Frames)
			parts = append(parts, fmt.Sprintf("%s–%s (%d)", clock(s.From), time.Unix(s.To, 0).Format("15:04"), s.Frames))
		}
		const shown = 4
		list := strings.Join(parts[:min(len(parts), shown)], "  ")
		if len(parts) > shown {
			list += fmt.Sprintf("  … +%d", len(parts)-shown)
		}
		fmt.Printf("%-16s %-12s %8d %7d %5s %7d %9d %6d %5s  %s\n", r.Name, r.Climate, r.Expected, r.Cached,
			share(r.Cached, r.Expected), len(r.Runs), longest, r.Usable, share(r.Usable, r.Cached), list)
		exp += r.Expected
		cached += r.Cached
		use += r.Usable
		runs += len(r.Runs)
	}
	fmt.Printf("\n%d vùng: %d frame, %d có đủ tile (%s), %d đoạn liên tục, %d dùng được cho backtest (%s số có tile, %s tổng)\n",
		len(inv), exp, cached, share(cached, exp), runs, use, share(use, cached), share(use, exp))
	fmt.Println("dùng được: có đủ lịch sử cho chuyển động và có frame ở mọi mốc dự báo (+10′ … +60′).")
	return nil
}

// runPrune lists, and with yes deletes, what no backtest can use.
func runPrune(dbURL, cacheDir string, step int, yes bool) error {
	st, err := store.Open(dbURL)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	plan, err := backtest.PlanPrune(ctx, st, cacheDir, backtest.DefaultConfig(step), time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("không dùng được cho backtest (giữ nguyên mọi thứ trong %s gần nhất):\n", backtest.PruneKeep)
	fmt.Printf("  %d frame (xoá cả thư mục tile), %d thư mục tile mồ côi\n", len(plan.Frames), len(plan.Dirs)-len(plan.Frames))
	fmt.Printf("  %d tile lẻ trong các frame còn giữ\n", len(plan.Files))
	fmt.Printf("  %d vùng không có thời điểm dự báo nào dùng được:", len(plan.Regions))
	for _, r := range plan.Regions {
		fmt.Printf(" %.1f,%.1f", r.Lat, r.Lon)
	}
	fmt.Printf("\n  giải phóng ~%.1f MB\n", float64(plan.Bytes)/1e6)
	if !yes {
		fmt.Println("chưa xoá gì; chạy lại với -prune -yes để xoá.")
		return nil
	}
	if err := plan.Apply(ctx, st); err != nil {
		return err
	}
	fmt.Println("đã xoá.")
	return nil
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
	printPaper(rep)
	printClasses(rep)
	printFSS(rep)
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

// printClasses shows, for each kind of rain, every setting's CSI by lead,
// its difference from the reference with its interval, POD and FAR.
func printClasses(rep backtest.Report) {
	if len(rep.Results) == 0 {
		return
	}
	for k, c := range rep.Results[0].Classes {
		var observed int64
		for _, r := range rep.Results {
			observed = max(observed, r.Classes[k].Observed)
		}
		fmt.Printf("%s — %d mẫu quan sát (so với %s)\n", c.Class, observed, rep.Reference)
		fmt.Printf("  %-28s", "")
		for _, l := range c.Leads {
			fmt.Printf("%7s", fmt.Sprintf("+%d'", l.LeadMin))
		}
		fmt.Printf("%7s%20s%7s%7s\n", "CSI", "Δ CSI [95%]", "POD", "FAR")
		for _, r := range rep.Results {
			cs := r.Classes[k]
			fmt.Printf("  %-28s", r.Name)
			for _, l := range cs.Leads {
				fmt.Printf("%7s", pct(l.Scores.CSI))
			}
			delta := ""
			if cs.DeltaCSI != nil {
				delta = fmt.Sprintf("%+.1f", *cs.DeltaCSI*100)
				if cs.DeltaCSICI != nil {
					delta += fmt.Sprintf(" [%+.1f, %+.1f]", cs.DeltaCSICI[0]*100, cs.DeltaCSICI[1]*100)
				}
			}
			fmt.Printf("%7s%20s%7s%7s\n", pct(cs.Overall.CSI), delta, pct(cs.Overall.POD), pct(cs.Overall.FAR))
		}
		fmt.Println()
	}
}

// printPaper shows, when the run scored to 90 minutes (-paper), each
// threshold's CSI at +30', +60' and +90' and its mean over the leads up to
// 60 and 90 minutes, over all regions and per climate: the figures papers
// report.
func printPaper(rep backtest.Report) {
	if len(rep.Results) == 0 || len(rep.Results[0].Classes) == 0 || rep.Results[0].Classes[0].CSIMean90 == nil {
		return
	}
	at := func(cs backtest.ClassScore, lead int) *float64 {
		for _, l := range cs.Leads {
			if l.LeadMin == lead {
				return l.Scores.CSI
			}
		}
		return nil
	}
	groups := []backtest.Group{{Climate: "tất cả", Regions: len(rep.Regions), Events: rep.Events, Results: rep.Results}}
	groups = append(groups, rep.Groups...)
	for _, g := range groups {
		fmt.Printf("CSI kiểu bài báo — %s (%d vùng, %d đợt mưa; từng pixel ~1,2 km)\n", g.Climate, g.Regions, g.Events)
		for k, c := range g.Results[0].Classes {
			fmt.Printf("  %s\n", c.Class)
			fmt.Printf("    %-28s%7s%7s%7s%7s%7s%7s\n", "", "+10'", "+30'", "+60'", "+90'", "TB 60'", "TB 90'")
			for _, r := range g.Results {
				if k >= len(r.Classes) {
					continue
				}
				cs := r.Classes[k]
				fmt.Printf("    %-28s%7s%7s%7s%7s%7s%7s\n", r.Name, dec(at(cs, 10)), dec(at(cs, 30)), dec(at(cs, 60)),
					dec(at(cs, 90)), dec(cs.CSIMean60), dec(cs.CSIMean90))
			}
		}
		fmt.Println()
	}
}

// printFSS shows, per threshold, every setting's fractions skill score over
// each window, with its difference from the reference.
func printFSS(rep backtest.Report) {
	if len(rep.Results) == 0 || len(rep.Results[0].FSS) == 0 {
		return
	}
	head := rep.Results[0].FSS
	for k := 0; k < len(head); {
		thr := head[k].Threshold
		end := k
		for end < len(head) && head[end].Threshold == thr {
			end++
		}
		fmt.Printf("FSS ≥ %.0f dBZ — cho phép lệch vị trí (1 = hoàn hảo; Δ so với %s)\n", thr, rep.Reference)
		fmt.Printf("  %-28s", "")
		for _, f := range head[k:end] {
			fmt.Printf("%16s", fmt.Sprintf("~%.0f km", f.WindowKm))
		}
		fmt.Println()
		for _, r := range rep.Results {
			fmt.Printf("  %-28s", r.Name)
			for _, f := range r.FSS[k:end] {
				cell := num(f.Overall)
				if f.DeltaFSS != nil {
					cell += fmt.Sprintf(" (%+.1f)", *f.DeltaFSS*100)
				}
				fmt.Printf("%16s", cell)
			}
			fmt.Println()
		}
		fmt.Println()
		k = end
	}
}

// printCorr shows how alike the errors of the main settings are.
func printCorr(rep backtest.Report) {
	ec := rep.ErrCorr
	if ec == nil {
		return
	}
	var idx []int
	for i, r := range rep.Results {
		if !r.Trend && r.Method != "persistence" && r.Method != "ensemble" &&
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

func dec(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmt.Sprintf("%.3f", *v)
}

func pct(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

// flagSet reports whether the flag was given on the command line.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func clock(t int64) string { return time.Unix(t, 0).Format("02/01 15:04") }
