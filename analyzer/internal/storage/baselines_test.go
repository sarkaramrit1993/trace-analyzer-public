package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"testing"
)

const legacyBaselinesDDL = `CREATE TABLE baselines (
	service_id TEXT PRIMARY KEY,
	data_json TEXT NOT NULL,
	updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
)`

func putAll(rows map[[2]string]*BaselineData) func(put func(string, string, *BaselineData) error) error {
	return func(put func(string, string, *BaselineData) error) error {
		keys := make([][2]string, 0, len(rows))
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
		for _, k := range keys {
			if err := put(k[0], k[1], rows[k]); err != nil {
				return err
			}
		}
		return nil
	}
}

func readCombinationRows(t *testing.T, s *SQLiteStorage) (map[[2]string]*BaselineData, int) {
	t.Helper()
	got := map[[2]string]*BaselineData{}
	corrupt, err := s.ForEachCombinationBaseline(func(service, combination string, d *BaselineData) {
		got[[2]string{service, combination}] = d
	})
	if err != nil {
		t.Fatalf("ForEachCombinationBaseline: %v", err)
	}
	return got, corrupt
}

func TestBaselines_LegacyDatabaseOpensAndKeepsLegacyRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(legacyBaselinesDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO baselines (service_id, data_json) VALUES ('svc', '{"service_id":"svc","total_traces":42}')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s := openTestStore(t, path)
	for _, table := range []string{"baseline_combinations", "hourly_baselines"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing after open (n=%d err=%v)", table, n, err)
		}
	}
	legacy, err := s.LoadBaselines()
	if err != nil || len(legacy) != 1 || legacy[0].ServiceID != "svc" || legacy[0].TotalTraces != 42 {
		t.Fatalf("legacy row not loadable: %+v err=%v", legacy, err)
	}

	rows, err := s.db.Query(`PRAGMA table_info(baselines)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pkCols []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if pk > 0 {
			pkCols = append(pkCols, name)
		}
	}
	if len(pkCols) != 1 || pkCols[0] != "service_id" {
		t.Errorf("legacy baselines PK = %v, want [service_id]", pkCols)
	}
}

func TestBaselines_UpsertByServiceAndCombination(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	first := map[[2]string]*BaselineData{
		{"a", "cumulative:50"}: {ServiceID: "a", TotalTraces: 1},
		{"a", "ewma:100"}:      {ServiceID: "a", TotalTraces: 2},
		{"b", "cumulative:50"}: {ServiceID: "b", TotalTraces: 3},
	}
	n, bytes, err := s.SaveCombinationBaselines(putAll(first))
	if err != nil || n != 3 || bytes <= 0 {
		t.Fatalf("save = (%d, %d, %v), want 3 rows", n, bytes, err)
	}
	if _, _, err := s.SaveCombinationBaselines(putAll(map[[2]string]*BaselineData{
		{"a", "ewma:100"}: {ServiceID: "a", TotalTraces: 20},
	})); err != nil {
		t.Fatal(err)
	}

	got, _ := readCombinationRows(t, s)
	want := map[[2]string]int64{{"a", "cumulative:50"}: 1, {"a", "ewma:100"}: 20, {"b", "cumulative:50"}: 3}
	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d", len(got), len(want))
	}
	for k, total := range want {
		if got[k] == nil || got[k].TotalTraces != total {
			t.Errorf("row %v = %+v, want TotalTraces %d", k, got[k], total)
		}
	}
	keys, err := s.CombinationBaselineKeys()
	if err != nil || !keys["a"]["ewma:100"] || !keys["a"]["cumulative:50"] || !keys["b"]["cumulative:50"] || keys["b"]["ewma:100"] {
		t.Errorf("CombinationBaselineKeys = %v err=%v", keys, err)
	}
	var legacy int
	s.db.QueryRow(`SELECT COUNT(*) FROM baselines`).Scan(&legacy)
	if legacy != 0 {
		t.Errorf("combination save wrote %d legacy baselines rows", legacy)
	}
}

func TestBaselines_SaveRollsBackWhenFillFails(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	if _, _, err := s.SaveCombinationBaselines(putAll(map[[2]string]*BaselineData{
		{"a", "cumulative:50"}: {ServiceID: "a", TotalTraces: 1},
	})); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	_, _, err := s.SaveCombinationBaselines(func(put func(string, string, *BaselineData) error) error {
		if err := put("a", "cumulative:50", &BaselineData{ServiceID: "a", TotalTraces: 99}); err != nil {
			return err
		}
		if err := put("c", "cumulative:50", &BaselineData{ServiceID: "c", TotalTraces: 5}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("save err = %v, want boom", err)
	}
	got, _ := readCombinationRows(t, s)
	if len(got) != 1 || got[[2]string{"a", "cumulative:50"}].TotalTraces != 1 {
		t.Errorf("after failed save rows = %v, want only the earlier a/cumulative:50 with TotalTraces 1", got)
	}
}

func TestBaselines_CorruptRowSkipped(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	if _, _, err := s.SaveCombinationBaselines(putAll(map[[2]string]*BaselineData{
		{"a", "cumulative:50"}: {ServiceID: "a", TotalTraces: 1},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO baseline_combinations (service_id, combination, data_json) VALUES ('bad', 'cumulative:50', '{not json')`); err != nil {
		t.Fatal(err)
	}
	got, corrupt := readCombinationRows(t, s)
	if corrupt != 1 || len(got) != 1 || got[[2]string{"a", "cumulative:50"}] == nil {
		t.Errorf("rows = %v corrupt = %d, want the good row and 1 corrupt", got, corrupt)
	}
}

func TestBaselines_HourlyUpsertAndDeleteNonRetained(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	rows := []HourlyBaselineRow{
		{Hour: "2026-09-28-10", ServiceID: "a", DataJSON: []byte(`{"v":1}`)},
		{Hour: "2026-09-28-10", ServiceID: "b", DataJSON: []byte(`{"v":2}`)},
		{Hour: "2026-09-29-10", ServiceID: "a", DataJSON: []byte(`{"v":3}`)},
	}
	if n, _, err := s.SaveHourlyBaselines(rows, nil); err != nil || n != 3 {
		t.Fatalf("first hourly save = %d, %v", n, err)
	}
	n, deleted, err := s.SaveHourlyBaselines([]HourlyBaselineRow{
		{Hour: "2026-09-29-10", ServiceID: "a", DataJSON: []byte(`{"v":30}`)},
	}, []string{"2026-09-29-09", "2026-09-29-10"})
	if err != nil || n != 1 || deleted != 2 {
		t.Fatalf("second hourly save = (%d, %d, %v), want 1 written, 2 deleted", n, deleted, err)
	}
	got, err := s.LoadHourlyBaselines()
	if err != nil || len(got) != 1 || got[0].Hour != "2026-09-29-10" || got[0].ServiceID != "a" || string(got[0].DataJSON) != `{"v":30}` {
		t.Errorf("hourly rows = %+v err=%v, want only the updated 2026-09-29-10/a", got, err)
	}
}
