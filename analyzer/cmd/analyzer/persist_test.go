package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

var (
	persistBase     = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC) // a Tuesday
	persistServices = []string{"checkout", "search"}
	persistPaths    = [][]string{{"a"}, {"a", "b"}, {"a", "c"}, {"b"}, {"c"}, {"a", "b", "c"}, {"d"}}
)

func persistTrace(service string, i int, ops []string, start time.Time) *models.Trace {
	id := fmt.Sprintf("%s-%d", service, i)
	dur := int64(50000 + (i*7919)%20000)
	root := &models.Span{TraceID: id, SpanID: id + "-root", ServiceName: "gw", OperationName: "POST /" + service, StartTime: start.UnixMicro(), Duration: dur}
	spans := []*models.Span{root}
	for j, op := range ops {
		spans = append(spans, &models.Span{TraceID: id, SpanID: fmt.Sprintf("%s-%d", id, j), ParentID: root.SpanID,
			ServiceName: op, OperationName: op, StartTime: start.UnixMicro() + 1000, Duration: dur / 4})
	}
	return &models.Trace{TraceID: id, ServiceID: service, StartTime: start, TotalDurationUs: dur, RootSpan: root, Spans: spans,
		ServiceGrouping: models.ServiceGrouping{ServiceIdentity: service, Env: "prod", Operation: "POST /" + service}}
}

// fixtureTraces stays under 500 so sliding_window:500 has no known-good paths.
const fixtureTraces = 450

// feedFixture teaches fixtureTraces traces per service over 7 paths, 10 s apart.
func feedFixture(vm *analysis.VariantManager) {
	fp := analysis.NewFingerprinter()
	pick := []int{0, 0, 0, 0, 1, 1, 2, 3, 4, 5}
	for _, svc := range persistServices {
		for i := 0; i < fixtureTraces; i++ {
			path := pick[i%10]
			if i%37 == 0 {
				path = 6
			}
			tr := persistTrace(svc, i, persistPaths[path], persistBase.Add(time.Duration(i)*10*time.Second))
			vm.UpdateAll(tr, fp.Compute(tr))
		}
	}
}

func openPersistStore(t testing.TB, path string) *storage.SQLiteStorage {
	t.Helper()
	s, err := storage.NewSQLiteStorage(path, 15*time.Minute)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	return s
}

// restart saves vm, closes the store, reopens it and loads into a fresh manager.
func restart(t *testing.T, path string, vm *analysis.VariantManager, opts analysis.VariantOptions) (*analysis.VariantManager, loadStats) {
	t.Helper()
	s := openPersistStore(t, path)
	if vm != nil {
		if err := saveAllBaselines(s, vm, zerolog.DebugLevel); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	s.Close()
	s = openPersistStore(t, path)
	defer s.Close()
	fresh := analysis.NewVariantManagerWithOptions(opts)
	return fresh, loadPersistedBaselines(s, fresh)
}

// exportDiff names the ExportedBaseline fields whose JSON differs, so times
// compare by instant and maps by content.
func exportDiff(a, b *analysis.ExportedBaseline) []string {
	if a == nil || b == nil {
		if a == b {
			return nil
		}
		return []string{fmt.Sprintf("nil mismatch: %v vs %v", a == nil, b == nil)}
	}
	var diff []string
	va, vb := reflect.ValueOf(*a), reflect.ValueOf(*b)
	for i := 0; i < va.NumField(); i++ {
		ja, _ := json.Marshal(va.Field(i).Interface())
		jb, _ := json.Marshal(vb.Field(i).Interface())
		if string(ja) != string(jb) {
			diff = append(diff, va.Type().Field(i).Name)
		}
	}
	return diff
}

func exportsByCombination(vm *analysis.VariantManager) map[analysis.VariantCombination]map[string]*analysis.ExportedBaseline {
	out := map[analysis.VariantCombination]map[string]*analysis.ExportedBaseline{}
	for _, c := range vm.GetAllCombinations() {
		out[c] = map[string]*analysis.ExportedBaseline{}
		for _, svc := range persistServices {
			out[c][svc] = vm.GetCombination(c).ExportBaseline(svc)
		}
	}
	return out
}

func TestPersist_EveryCombinationRoundTripsItsOwnState(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	feedFixture(vm)
	before := exportsByCombination(vm)

	exp := func(c string) *analysis.ExportedBaseline { return before[analysis.VariantCombination(c)]["checkout"] }
	if len(exp("ewma:100").TopologyFrequenciesEWMA) == 0 || len(exp("cumulative:50").TopologyFrequenciesEWMA) != 0 {
		t.Fatal("precondition: ewma:100 must have EWMA frequencies and cumulative:50 none")
	}
	if len(exp("sliding_window:50").KnownGoodPaths) == 0 || len(exp("sliding_window:500").KnownGoodPaths) != 0 {
		t.Fatalf("precondition: sliding_window:50 known-good %d (want >0), sliding_window:500 %d (want 0)",
			len(exp("sliding_window:50").KnownGoodPaths), len(exp("sliding_window:500").KnownGoodPaths))
	}
	if reflect.DeepEqual(exp("exponential_decay:50").TopologyCounts, exp("cumulative:50").TopologyCounts) {
		t.Fatal("precondition: exponential_decay:50 TopologyCounts must differ from cumulative:50")
	}

	fresh, stats := restart(t, filepath.Join(t.TempDir(), "traces.db"), vm, analysis.VariantOptions{})
	if stats.Own != 20*len(persistServices) || stats.Sibling != 0 || stats.Legacy != 0 {
		t.Errorf("load stats = %+v, want %d own", stats, 20*len(persistServices))
	}
	after := exportsByCombination(fresh)
	changed := 0
	for c, svcs := range before {
		for svc, want := range svcs {
			if d := exportDiff(want, after[c][svc]); len(d) > 0 {
				changed++
				t.Errorf("%s/%s changed across restart: %v", c, svc, d)
			}
		}
	}
	if changed > 0 {
		t.Logf("%d of %d (combination, service) states changed", changed, 20*len(persistServices))
	}
}

func TestPersist_ActiveStateDoesNotLeakAfterRestart(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	feedFixture(vm)
	if !vm.SetActiveCombination("ewma:500") {
		t.Fatal("ewma:500 not learned")
	}
	fresh, _ := restart(t, filepath.Join(t.TempDir(), "traces.db"), vm, analysis.VariantOptions{})
	got := fresh.GetCombination("cumulative:50").ExportBaseline("checkout")
	if got == nil {
		t.Fatal("cumulative:50 has no checkout state after restart")
	}
	if len(got.TopologyFrequenciesEWMA) != 0 {
		t.Errorf("cumulative:50 picked up %d EWMA frequencies from the active ewma:500", len(got.TopologyFrequenciesEWMA))
	}
}

func TestPersist_LegacyRowSeedsAllThenOwnRowsWin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.db")
	seed := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"cumulative:50"}})
	feedFixture(seed)
	legacy := convertAnalysisToStorage(seed.GetCombination("cumulative:50").ExportBaseline("checkout"))
	s := openPersistStore(t, path)
	if err := s.StoreBaseline(legacy); err != nil {
		t.Fatal(err)
	}
	s.Close()

	vm, stats := restart(t, path, nil, analysis.VariantOptions{})
	if stats.Legacy != 1 || stats.Own != 0 {
		t.Errorf("first load stats = %+v, want 1 legacy", stats)
	}
	for _, c := range vm.GetAllCombinations() {
		if e := vm.GetCombination(c).ExportBaseline("checkout"); e == nil || e.TotalTraces != fixtureTraces {
			t.Fatalf("%s not seeded from legacy row: %+v", c, e)
		}
	}

	s = openPersistStore(t, path)
	if err := saveAllBaselines(s, vm, zerolog.DebugLevel); err != nil {
		t.Fatal(err)
	}
	legacy.TotalTraces = 1
	if err := s.StoreBaseline(legacy); err != nil {
		t.Fatal(err)
	}
	s.Close()

	vm2, stats := restart(t, path, nil, analysis.VariantOptions{})
	if stats.Own != 20 || stats.Legacy != 0 {
		t.Errorf("second load stats = %+v, want 20 own and no legacy", stats)
	}
	for _, c := range vm2.GetAllCombinations() {
		if e := vm2.GetCombination(c).ExportBaseline("checkout"); e == nil || e.TotalTraces == 1 {
			t.Errorf("%s loaded the legacy row over its own: %+v", c, e)
		}
	}
}

func TestPersist_LegacyRowSeedsVariantsWithoutStoredRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.db")
	seed := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"cumulative:50"}})
	feedFixture(seed)
	legacy := convertAnalysisToStorage(seed.GetCombination("cumulative:50").ExportBaseline("checkout"))
	s := openPersistStore(t, path)
	if err := s.StoreBaseline(legacy); err != nil {
		t.Fatal(err)
	}
	s.Close()

	onlyEWMA := analysis.VariantOptions{Combinations: []analysis.VariantCombination{"ewma:500"}}
	first, stats := restart(t, path, nil, onlyEWMA)
	if stats.Legacy != 1 {
		t.Fatalf("first load stats = %+v, want 1 legacy", stats)
	}
	first.UpdateAll(persistTrace("checkout", 9999, persistPaths[0], persistBase.Add(time.Hour)), "extra")
	s = openPersistStore(t, path)
	if err := saveAllBaselines(s, first, zerolog.DebugLevel); err != nil {
		t.Fatal(err)
	}
	s.Close()

	vm, stats := restart(t, path, nil, analysis.VariantOptions{})
	if stats.Own != 1 || stats.Sibling != 3 || stats.Legacy != 1 {
		t.Errorf("second load stats = %+v, want own=1 sibling=3 legacy=1", stats)
	}
	for _, c := range vm.GetAllCombinations() {
		e := vm.GetCombination(c).ExportBaseline("checkout")
		if e == nil {
			t.Errorf("%s starts empty although the legacy row exists", c)
			continue
		}
		v, _, _ := analysis.ParseCombination(string(c))
		want := int64(fixtureTraces)
		if v == analysis.VariantEWMA {
			want++
		}
		if e.TotalTraces != want {
			t.Errorf("%s TotalTraces = %d, want %d", c, e.TotalTraces, want)
		}
	}
}

func TestPersist_MissingCombinationUsesSiblingUnknownIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.db")
	src := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"ewma:5000", "ewma:100", "ewma:500"}})
	feedFixture(src)
	sibling := src.GetCombination("ewma:100").ExportBaseline("checkout")
	if len(exportDiff(sibling, src.GetCombination("ewma:5000").ExportBaseline("checkout"))) == 0 {
		t.Fatal("precondition: ewma:100 and ewma:5000 must differ")
	}
	s := openPersistStore(t, path)
	if err := saveAllBaselines(s, src, zerolog.DebugLevel); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveCombinationBaselines(func(put func(string, string, *storage.BaselineData) error) error {
		return put("checkout", "bogus:1", &storage.BaselineData{ServiceID: "checkout", TotalTraces: 7})
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	learn := []analysis.VariantCombination{"ewma:50", "ewma:5000", "cumulative:50"}
	vm, stats := restart(t, path, nil, analysis.VariantOptions{Combinations: learn})
	n := len(persistServices)
	if stats.Own != n || stats.Sibling != n || stats.Legacy != 0 || stats.Ignored != n+1 {
		t.Errorf("load stats = %+v, want own=%d sibling=%d ignored=%d (unused ewma:500 rows + bogus)", stats, n, n, n+1)
	}
	if d := exportDiff(sibling, vm.GetCombination("ewma:50").ExportBaseline("checkout")); len(d) > 0 {
		t.Errorf("ewma:50 not seeded from lowest sibling ewma:100: %v", d)
	}
	if d := exportDiff(src.GetCombination("ewma:5000").ExportBaseline("checkout"), vm.GetCombination("ewma:5000").ExportBaseline("checkout")); len(d) > 0 {
		t.Errorf("ewma:5000 did not load its own row: %v", d)
	}
	if ids := vm.GetCombination("cumulative:50").GetAllServiceIDs(); len(ids) != 0 {
		t.Errorf("cumulative:50 has no row or sibling but loaded %v", ids)
	}
}

// fillNonZero sets every exported field reachable from v to a non-zero value.
func fillNonZero(t *testing.T, v reflect.Value, seed int) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 1, 1+seed%28, 3, 4, 5, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillNonZero(t, v.Field(i), seed+i+1)
			}
		}
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fillNonZero(t, v.Elem(), seed)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k := reflect.New(v.Type().Key()).Elem()
		fillNonZero(t, k, seed)
		e := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, e, seed+1)
		v.SetMapIndex(k, e)
	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, e, seed)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), e))
	case reflect.String:
		v.SetString(fmt.Sprintf("s%d", seed))
	case reflect.Int, reflect.Int64:
		v.SetInt(int64(seed + 1))
	case reflect.Float64:
		v.SetFloat(float64(seed) + 0.5)
	case reflect.Bool:
		v.SetBool(true)
	default:
		t.Fatalf("fillNonZero: unhandled kind %s", v.Kind())
	}
}

func TestConverters_RoundTripEveryField(t *testing.T) {
	var in analysis.ExportedBaseline
	fillNonZero(t, reflect.ValueOf(&in).Elem(), 0)
	stored := convertAnalysisToStorage(&in)
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded storage.BaselineData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	out := convertStorageToAnalysis(&decoded)
	if !reflect.DeepEqual(&in, out) {
		t.Errorf("round trip lost data:\n in %+v\nout %+v\ndiff %v", in, *out, exportDiff(&in, out))
	}
	for _, pair := range [][2]reflect.Type{
		{reflect.TypeOf(analysis.ExportedBaseline{}), reflect.TypeOf(storage.BaselineData{})},
		{reflect.TypeOf(analysis.ExportedPathBaseline{}), reflect.TypeOf(storage.PathBaselineData{})},
		{reflect.TypeOf(analysis.ExportedBranchBaseline{}), reflect.TypeOf(storage.BranchBaselineData{})},
	} {
		if pair[0].NumField() != pair[1].NumField() {
			t.Errorf("%s has %d fields, %s has %d", pair[0], pair[0].NumField(), pair[1], pair[1].NumField())
		}
	}
}

func TestPersist_SameTimeYesterdayHourlyHistorySurvivesRestart(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	feedFixture(vm)
	fp := analysis.NewFingerprinter()
	probe := persistTrace("checkout", 0, persistPaths[0], persistBase.Add(24*time.Hour+30*time.Minute))
	f := fp.Compute(probe)
	if vm.GetCombination("same_time_yesterday:50").YesterdayPathBaseline("checkout", f, probe.StartTime) == nil {
		t.Fatal("precondition: no yesterday path baseline before restart")
	}
	fresh, _ := restart(t, filepath.Join(t.TempDir(), "traces.db"), vm, analysis.VariantOptions{})
	got := fresh.GetCombination("same_time_yesterday:50").YesterdayPathBaseline("checkout", f, probe.StartTime)
	want := vm.GetCombination("same_time_yesterday:50").YesterdayPathBaseline("checkout", f, probe.StartTime)
	if got == nil {
		t.Fatal("yesterday path baseline lost across restart")
	}
	if got.Count != want.Count || len(got.Samples) != len(want.Samples) {
		t.Errorf("yesterday path baseline count=%d samples=%d, want %d/%d", got.Count, len(got.Samples), want.Count, len(want.Samples))
	}
}

func TestPersist_LegacyJSONWithoutNewFieldsImports(t *testing.T) {
	old := `{"service_id":"checkout","topology_counts":{"fp1":60},"topology_first_seen":{"fp1":"2026-09-29T10:00:00Z"},
	"known_good_paths":{"fp1":true},"canonical_fingerprint":"fp1","total_traces":60,"error_count":0,"duration_sum":600,
	"duration_min":5,"duration_max":15,"path_baselines":{"fp1":{"count":60,"sum_duration":600,"min_duration":5,"max_duration":15,"samples":[5,10,15]}},
	"branch_baselines":{},"topology_frequencies_ewma":{},"last_update_time":"2026-09-29T10:00:00Z","last_seen":"2026-09-29T10:00:00Z"}`
	var data storage.BaselineData
	if err := json.Unmarshal([]byte(old), &data); err != nil {
		t.Fatalf("old JSON no longer decodes: %v", err)
	}
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"cumulative:50"}})
	if !vm.ImportBaseline("cumulative:50", convertStorageToAnalysis(&data)) {
		t.Fatal("ImportBaseline refused a learned combination")
	}
	got := vm.GetCombination("cumulative:50").ExportBaseline("checkout")
	if got.TotalTraces != 60 || len(got.DurationSamples) != 3 || got.PathBaselines["fp1"].Count != 60 {
		t.Errorf("legacy import = %+v, want 60 traces and 3 reconstructed duration samples", got)
	}
	if vm.ImportBaseline("ewma:100", convertStorageToAnalysis(&data)) {
		t.Error("ImportBaseline accepted a combination that is not learned")
	}
}

// heavyExport is one busy service: 32 paths x 1000 samples, 16 branches.
func heavyExport(service string) *analysis.ExportedBaseline {
	e := &analysis.ExportedBaseline{
		ServiceID:               service,
		TopologyCounts:          map[string]int64{},
		TopologyFirstSeen:       map[string]time.Time{},
		KnownGoodPaths:          map[string]bool{},
		TopologyFrequenciesEWMA: map[string]float64{},
		PathBaselines:           map[string]*analysis.ExportedPathBaseline{},
		BranchBaselines:         map[string]*analysis.ExportedBranchBaseline{},
		TotalTraces:             32000,
		LastSeen:                persistBase,
	}
	for p := 0; p < 32; p++ {
		fp := fmt.Sprintf("%016x", p*7919+1)
		samples := make([]int64, 1000)
		for i := range samples {
			samples[i] = int64(40000 + (i*p*31)%30000)
		}
		e.TopologyCounts[fp] = 1000
		e.TopologyFirstSeen[fp] = persistBase
		e.KnownGoodPaths[fp] = true
		e.TopologyFrequenciesEWMA[fp] = 0.03
		e.PathBaselines[fp] = &analysis.ExportedPathBaseline{Count: 1000, SumDuration: 55000000, MinDuration: 40000, MaxDuration: 70000, Samples: samples, LastUpdateTime: persistBase}
		e.DurationSamples = append(e.DurationSamples, samples[:31]...)
	}
	for b := 0; b < 16; b++ {
		samples := make([]int64, 100)
		for i := range samples {
			samples[i] = int64(1000 + i*b)
		}
		e.BranchBaselines[fmt.Sprintf("svc-%d->op-%d", b, b)] = &analysis.ExportedBranchBaseline{Count: 32000, SumDuration: 1, MinDuration: 1000, MaxDuration: 2600, Samples: samples}
	}
	return e
}

func heavyManager() *analysis.VariantManager {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	for i := 0; i < 20; i++ {
		exported := heavyExport(fmt.Sprintf("heavy-%02d", i))
		for _, c := range vm.GetAllCombinations() {
			vm.ImportBaseline(c, exported)
		}
	}
	return vm
}

func TestPersist_SaveFitsShutdownBudget(t *testing.T) {
	if testing.Short() || raceEnabled() {
		t.Skip("save budget is measured without -short and without the race detector")
	}
	vm := heavyManager()
	s := openPersistStore(t, filepath.Join(t.TempDir(), "traces.db"))
	defer s.Close()
	start := time.Now()
	if err := saveAllBaselines(s, vm, zerolog.InfoLevel); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("saving 20 heavy services x 20 combinations took %v, budget 3s", took)
	} else {
		t.Logf("saved 20 heavy services x 20 combinations in %v", took)
	}
}

func raceEnabled() bool {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "-race" {
				return s.Value == "true"
			}
		}
	}
	return false
}

func BenchmarkSaveAllBaselines(b *testing.B) {
	vm := heavyManager()
	s := openPersistStore(b, filepath.Join(b.TempDir(), "traces.db"))
	defer s.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := saveAllBaselines(s, vm, zerolog.DebugLevel); err != nil {
			b.Fatal(err)
		}
	}
}
