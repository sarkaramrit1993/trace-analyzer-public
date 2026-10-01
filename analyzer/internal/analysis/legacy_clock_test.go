package analysis

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var legacyReplay = time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)

// legacyRow is an export as written before stamps moved to trace time: no
// clock marker, first-seen from the wall clock at save time.
func legacyRow(t *testing.T, firstSeen time.Time, pathLastUpdate map[string]time.Time) *ExportedBaseline {
	t.Helper()
	paths := map[string]string{}
	for _, fp := range []string{"A", "C"} {
		lu := ""
		if at, ok := pathLastUpdate[fp]; ok {
			lu = fmt.Sprintf(`,"last_update_time":%q`, at.Format(time.RFC3339Nano))
		}
		paths[fp] = fmt.Sprintf(`{"count":600,"sum_duration":600000,"min_duration":900,"max_duration":1100,"samples":[900,1000,1100]%s}`, lu)
	}
	fs := firstSeen.Format(time.RFC3339Nano)
	raw := fmt.Sprintf(`{"service_id":"svc","topology_counts":{"A":600,"C":600},"topology_first_seen":{"A":%q,"C":%q},
		"known_good_paths":{"A":true,"C":true},"canonical_fingerprint":"A","total_traces":1200,"error_count":0,
		"duration_sum":1200000,"duration_min":900,"duration_max":1100,
		"path_baselines":{"A":%s,"C":%s},"branch_baselines":{},"topology_frequencies_ewma":{},
		"last_update_time":%q,"last_seen":%q}`, fs, fs, paths["A"], paths["C"], fs, fs)
	var e ExportedBaseline
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	return &e
}

func TestImport_LegacyWallClockFirstSeenDoesNotMakeReplayNew(t *testing.T) {
	wall := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	withWallClock(t, wall)
	for _, v := range clockVariants {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			bc.ImportBaseline(legacyRow(t, wall, nil))

			if bc.IsNewPath("svc", "A", legacyReplay) {
				t.Error("path in a legacy row reported new for a replayed January trace")
			}
			bc.Update(sampleTrace("r1", "db", 1000, legacyReplay), "A")
			if bc.IsNewPath("svc", "A", legacyReplay.Add(time.Second)) {
				t.Error("path in a legacy row new after learning a replayed trace")
			}
			if !bc.IsNewPath("svc", "B", legacyReplay.Add(time.Second)) {
				t.Error("genuinely unseen path not reported new")
			}
		})
	}
}

func TestImport_LegacyDecayLastSeenFromPathLastUpdate(t *testing.T) {
	wall := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	withWallClock(t, wall)
	traceSeen := legacyReplay.Add(-time.Hour)
	for _, v := range []BaselineVariant{VariantSlidingWindow, VariantExponentialDecay} {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			bc.ImportBaseline(legacyRow(t, wall, map[string]time.Time{"A": traceSeen}))
			last := bc.services["svc"].topologyLastSeen
			if got := last["A"]; !got.Equal(traceSeen) {
				t.Errorf("A last-seen = %s, want path LastUpdateTime %s", got, traceSeen)
			}
			if got, ok := last["C"]; ok {
				t.Errorf("C has no path LastUpdateTime but last-seen = %s, want none until the first trace", got)
			}
			bc.Update(sampleTrace("r1", "db", 1000, legacyReplay), "A")
			if got := bc.services["svc"].topologyLastSeen["C"]; !got.Equal(legacyReplay) {
				t.Errorf("C last-seen after the first post-restart trace = %s, want %s", got, legacyReplay)
			}
			if got := bc.services["svc"].TopologyCounts["C"]; got != 600 {
				t.Errorf("C count = %d, want 600 (its decay starts at the first post-restart trace)", got)
			}
		})
	}
}

func TestExport_MarkedRowKeepsExactStamps(t *testing.T) {
	withWallClock(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	for _, v := range clockVariants {
		t.Run(string(v), func(t *testing.T) {
			bc := clockComputer(v)
			end := warmClock(bc)
			raw, err := json.Marshal(bc.ExportBaseline("svc"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"clock":"trace"`) {
				t.Fatalf("export has no trace clock marker: %.200s", raw)
			}
			var e ExportedBaseline
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatal(err)
			}
			fresh := clockComputer(v)
			fresh.ImportBaseline(&e)
			src, dst := bc.services["svc"], fresh.services["svc"]
			for fp, want := range src.TopologyFirstSeen {
				if got := dst.TopologyFirstSeen[fp]; !got.Equal(want) {
					t.Errorf("first-seen %s = %s, want %s", fp, got, want)
				}
			}
			for fp, want := range src.topologyLastSeen {
				if got := dst.topologyLastSeen[fp]; !got.Equal(want) {
					t.Errorf("last-seen %s = %s, want %s", fp, got, want)
				}
			}
			if !fresh.IsNewPath("svc", "N", end) {
				t.Error("N, first seen a minute before the save, no longer new after import")
			}
		})
	}
}
