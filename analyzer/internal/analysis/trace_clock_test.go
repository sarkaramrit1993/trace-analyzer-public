package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	clockVariants = []BaselineVariant{VariantCumulative, VariantEWMA, VariantSlidingWindow, VariantExponentialDecay, VariantSameTimeYesterday}
	janBase       = time.Date(2026, 1, 26, 9, 0, 0, 0, time.UTC) // Monday
)

func withWallClock(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = prev })
}

func clockComputer(v BaselineVariant) *BaselineComputer {
	bc := NewBaselineComputer(10)
	bc.SetVariant(v)
	if v == VariantSlidingWindow {
		bc.SetSlidingWindowDuration(24 * time.Hour)
	}
	return bc
}

// warmClock replays 30 trace-minutes of January traffic in well under a
// second of wall time: path A every second, R once at +5m, N first at +29m.
func warmClock(bc *BaselineComputer) (end time.Time) {
	for i := 0; i <= 1800; i++ {
		at := janBase.Add(time.Duration(i) * time.Second)
		bc.Update(sampleTrace("a", "db", 1000, at), "A")
		if i == 300 {
			bc.Update(sampleTrace("r", "cache", 1000, at), "R")
		}
		if i == 1740 {
			bc.Update(sampleTrace("n", "queue", 1000, at), "N")
		}
	}
	return janBase.Add(30 * time.Minute)
}

func TestIsNewPath_UsesTraceTime(t *testing.T) {
	for _, v := range clockVariants {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			end := warmClock(bc)

			if bc.IsNewPath("svc", "A", end) {
				t.Error("A seen continuously for 30 trace-minutes reported as new")
			}
			if bc.IsNewPath("svc", "R", end) {
				t.Error("R first seen 25 trace-minutes ago reported as new")
			}
			if !bc.IsNewPath("svc", "N", end) {
				t.Error("N first seen 1 trace-minute ago not reported as new")
			}
			if bc.IsNewPath("svc", "N", end.Add(15*time.Minute)) {
				t.Error("N checked 16 trace-minutes after first seen still new; the checked trace's time must count")
			}

			// R has 1 sample < minSamples and is not new, so the gate drops it.
			if d := NewDeviationDetector(bc, 0.01).Check(sampleTrace("r2", "cache", 1000, end), "R"); d != nil {
				t.Errorf("R past the new-path window bypassed the min-samples gate: %s", d.DiffSummary)
			}
		})
	}
}

func TestIsNewPath_SurvivesExportImport(t *testing.T) {
	for _, v := range clockVariants {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			end := warmClock(bc)

			restored := clockComputer(v)
			restored.ImportBaseline(bc.ExportBaseline("svc"))
			if restored.IsNewPath("svc", "R", end) {
				t.Error("R reported as new after restart")
			}
			if !restored.IsNewPath("svc", "N", end) {
				t.Error("N not reported as new after restart")
			}
			if v == VariantSlidingWindow || v == VariantExponentialDecay {
				want := bc.services["svc"].topologyLastSeen
				if got := restored.services["svc"].topologyLastSeen; !reflect.DeepEqual(got, want) {
					t.Errorf("last-seen after restart = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestRecentWindow_RotatesOnTraceTime(t *testing.T) {
	bc := NewBaselineComputer(10)
	feed := func(fp string, n int, from time.Time) {
		for k := 0; k < n; k++ {
			bc.Update(sampleTrace("t", fp, 1000, from.Add(time.Duration(k)*time.Second)), fp)
		}
	}
	feed("A", 45, janBase)
	feed("B", 5, janBase.Add(time.Minute))
	// Six trace-minutes later A collapses to 20% of traffic.
	later := janBase.Add(6 * time.Minute)
	feed("B", 20, later)
	feed("A", 5, later.Add(time.Minute))

	d := NewDeviationDetector(bc, 0.01).Check(sampleTrace("a", "A", 1000, later.Add(2*time.Minute)), "A")
	if d == nil || !strings.HasPrefix(d.DiffSummary, "["+string(DeviationFrequencyDrop)+"]") {
		t.Fatalf("want frequency_drop once the window rotates on trace time, got %+v", d)
	}
}

func TestTraceClock_IgnoresFarFutureForNow(t *testing.T) {
	tue := janBase.AddDate(0, 0, 1)
	wall := at(tue, 1, 30) // Tuesday 10:30
	withWallClock(t, wall)

	bc := clockComputer(VariantSameTimeYesterday)
	for i := 0; i < 60; i++ {
		bc.Update(sampleTrace("mon", "db", 1000, at(janBase, 1, i)), "A") // Monday 10:xx
	}
	bc.Update(sampleTrace("tue", "db", 1000, wall), "A")
	bc.Update(sampleTrace("skewed", "db", 1000, wall.Add(24*time.Hour)), "A")

	if !bc.now.Equal(wall) {
		t.Errorf("trace now = %s after a +1 day trace, want %s", bc.now, wall)
	}
	if pb := bc.YesterdayPathBaseline("svc", "A", wall); pb == nil || pb.Count != 60 {
		t.Fatalf("Monday history dropped by a far-future trace: %+v", pb)
	}
	if !contains(bc.hourly.RetainedHourKeys(), hourKey(at(janBase, 1, 0))) {
		t.Error("Monday 10h no longer retained")
	}
	if got := bc.services["svc"].TopologyCounts["A"]; got != 62 {
		t.Errorf("far-future trace not learned: count %d, want 62", got)
	}

	// A persisted far-future bucket must not move retention on restart either.
	restored := NewHourlyHistory(0)
	restored.Import(bc.hourly.Export(false))
	if pb := restored.PathBaseline("svc", "A", wall); pb == nil || pb.Count != 60 {
		t.Fatalf("Monday history dropped on import next to a far-future bucket: %+v", pb)
	}
}

func TestTraceClock_FarFutureFirstTraceDoesNotPlantFutureFirstSeen(t *testing.T) {
	wall := janBase
	withWallClock(t, wall)
	bc := clockComputer(VariantCumulative)
	bc.Update(sampleTrace("skewed", "db", 1000, wall.Add(24*time.Hour)), "A")
	if !bc.now.IsZero() {
		t.Errorf("trace now = %s, want unset", bc.now)
	}
	if got := bc.services["svc"].TopologyFirstSeen["A"]; got.After(wall.Add(MaxFutureSkew)) {
		t.Errorf("first-seen = %s, want no later than wall + MaxFutureSkew", got)
	}
	withWallClock(t, wall.Add(20*time.Minute))
	if bc.IsNewPath("svc", "A", wall.Add(20*time.Minute)) {
		t.Error("path still new 20 minutes later because of a far-future first-seen")
	}
}

func TestIsNewPath_LateTraceStaysNewForWindow(t *testing.T) {
	for _, v := range clockVariants {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			end := warmClock(bc)
			late := end.Add(-time.Hour)

			if !bc.IsNewPath("svc", "L", late) {
				t.Fatal("unseen path L not new on its own late trace")
			}
			bc.Update(sampleTrace("late", "late", 1000, late), "L")
			bc.Update(sampleTrace("lateA", "db", 1000, late), "A")
			if !bc.IsNewPath("svc", "L", end.Add(9*time.Minute)) {
				t.Error("L first seen via a trace 1h behind now is no longer new 9 trace-minutes later")
			}
			if got := bc.services["svc"].TopologyFirstSeen["L"]; !got.Equal(end) {
				t.Errorf("L first-seen = %s, want trace now %s", got, end)
			}
			if v == VariantSlidingWindow || v == VariantExponentialDecay {
				seen := bc.services["svc"].topologyLastSeen
				if !seen["A"].Equal(end) || !seen["L"].Equal(end) {
					t.Errorf("last-seen moved behind trace now %s: A=%s L=%s", end, seen["A"], seen["L"])
				}
			}
		})
	}
}

func TestTraceClock_FarFutureFirstTraceStampsWallClock(t *testing.T) {
	wall := janBase
	withWallClock(t, wall)
	bc := clockComputer(VariantCumulative)
	bc.Update(sampleTrace("skewed", "db", 1000, wall.Add(24*time.Hour)), "A")
	if got := bc.services["svc"].TopologyFirstSeen["A"]; !got.Equal(wall) {
		t.Errorf("first-seen = %s, want wall clock %s", got, wall)
	}
	later := wall.Add(12 * time.Minute)
	withWallClock(t, later)
	if bc.IsNewPath("svc", "A", later) {
		t.Error("path still new 12 minutes after a far-future first trace")
	}
}

func contains(keys []string, k string) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

// Two replays of the same January stream under wall clocks a month apart must
// produce identical findings, since only the future-skew check reads the clock.
func TestReplaySpeedIndependence(t *testing.T) {
	run := func(wall time.Time) []string {
		withWallClock(t, wall)
		vm := NewVariantManagerWithOptions(VariantOptions{})
		var out []string
		for i := 0; i < 400; i++ {
			fp := "A"
			switch {
			case i%37 == 0:
				fp = "R"
			case i > 300 && i%3 == 0:
				fp = "B"
			}
			tr := sampleTrace("t", fp, int64(1000+i%7*10), janBase.Add(time.Duration(i)*20*time.Second))
			for _, c := range vm.GetAllCombinations() {
				bc := vm.GetCombination(c)
				if d := NewDeviationDetector(bc, 0.01).Check(tr, fp); d != nil {
					out = append(out, string(c)+" "+d.DiffSummary)
				}
			}
			vm.UpdateAll(tr, fp)
		}
		return out
	}
	a := run(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	b := run(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("findings depend on the wall clock: %d vs %d", len(a), len(b))
	}
	if len(a) == 0 {
		t.Fatal("replay produced no findings; test is not exercising detection")
	}
}

// Wall-clock reads in analysis code are limited to bookkeeping stamps
// (LastSeen, LastUpdateTime) and nowFunc; detection must use trace time.
func TestNoWallClockInDetection(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"LastSeen": true, "LastUpdateTime": true}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ok := map[ast.Node]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				if k, isIdent := n.Key.(*ast.Ident); isIdent && allowed[k.Name] {
					ok[n.Value] = true
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if sel, isSel := lhs.(*ast.SelectorExpr); isSel && allowed[sel.Sel.Name] && i < len(n.Rhs) {
						ok[n.Rhs[i]] = true
					}
				}
			case *ast.ValueSpec:
				for i, id := range n.Names {
					if id.Name == "nowFunc" && i < len(n.Values) {
						ok[n.Values[i]] = true
					}
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			if ok[n] {
				return false
			}
			sel, isSel := n.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			pkg, isIdent := sel.X.(*ast.Ident)
			if isIdent && pkg.Name == "time" && (sel.Sel.Name == "Now" || sel.Sel.Name == "Since" || sel.Sel.Name == "Until") {
				t.Errorf("%s: wall clock read outside bookkeeping", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}
