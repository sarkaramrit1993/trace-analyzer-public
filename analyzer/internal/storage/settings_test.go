package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestActiveCombination_SaveLoadAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.db")
	s, err := NewSQLiteStorage(path, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadActiveCombination(); err != nil || got != "" {
		t.Fatalf("empty db: %q, %v; want \"\", nil", got, err)
	}
	for _, c := range []string{"ewma:500", "sliding_window:100"} {
		if err := s.SaveActiveCombination(c); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = NewSQLiteStorage(path, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.LoadActiveCombination(); err != nil || got != "sliding_window:100" {
		t.Errorf("after reopen: %q, %v; want sliding_window:100", got, err)
	}
}
