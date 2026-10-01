package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/api"
	"github.com/trace-analyzer/internal/storage"
)

// bootActive opens the database and builds the manager and API server the way
// main does, returning the server handler and a close func.
func bootActive(t *testing.T, dbPath, env string, learn []analysis.VariantCombination) (*analysis.VariantManager, http.Handler, func()) {
	t.Helper()
	store, err := storage.NewSQLiteStorage(dbPath, 15*time.Minute)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: learn})
	if err := resolveActiveCombination(vm, store, env); err != nil {
		store.Close()
		t.Fatalf("resolveActiveCombination: %v", err)
	}
	return vm, api.NewServerWithVariants(store, vm, nil, nil).Handler(), func() { store.Close() }
}

func post(t *testing.T, h http.Handler, path, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s %s: status %d %s", path, body, w.Code, w.Body.String())
	}
}

func TestActiveCombination_APISwitchSurvivesRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "traces.db")

	vm, h, closeDB := bootActive(t, db, "", nil)
	if vm.GetActiveCombination() != "cumulative:50" {
		t.Fatalf("fresh start active = %s, want cumulative:50", vm.GetActiveCombination())
	}
	post(t, h, "/api/variants/combination", `{"combination":"ewma:500"}`)
	closeDB()

	vm, h, closeDB = bootActive(t, db, "", nil)
	if got := vm.GetActiveCombination(); got != "ewma:500" {
		t.Errorf("after restart active = %s, want ewma:500", got)
	}
	post(t, h, "/api/variants/active", `{"variant":"sliding_window"}`)
	closeDB()

	vm, _, closeDB = bootActive(t, db, "", nil)
	defer closeDB()
	if got := vm.GetActiveCombination(); got != "sliding_window:50" {
		t.Errorf("after /api/variants/active and restart active = %s, want sliding_window:50", got)
	}
}

func TestActiveCombination_ExplicitEnvOverridesStored(t *testing.T) {
	db := filepath.Join(t.TempDir(), "traces.db")
	_, h, closeDB := bootActive(t, db, "", nil)
	post(t, h, "/api/variants/combination", `{"combination":"ewma:500"}`)
	closeDB()

	vm, _, closeDB := bootActive(t, db, "cumulative:100", nil)
	defer closeDB()
	if got := vm.GetActiveCombination(); got != "cumulative:100" {
		t.Errorf("active = %s, want ACTIVE_COMBINATION cumulative:100 over stored ewma:500", got)
	}
}

func TestActiveCombination_StoredNotLearnedFallsBackWithLog(t *testing.T) {
	db := filepath.Join(t.TempDir(), "traces.db")
	_, h, closeDB := bootActive(t, db, "", nil)
	post(t, h, "/api/variants/combination", `{"combination":"ewma:500"}`)
	closeDB()

	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	vm, _, closeDB := bootActive(t, db, "", []analysis.VariantCombination{"ewma:100", "cumulative:50"})
	if got := vm.GetActiveCombination(); got != "cumulative:50" {
		t.Errorf("active = %s, want default cumulative:50 when stored ewma:500 is not learned", got)
	}
	closeDB()
	if !strings.Contains(buf.String(), "ewma:500") || !strings.Contains(buf.String(), `"level":"warn"`) {
		t.Errorf("no warning naming the unlearned stored combination: %s", buf.String())
	}

	vm, _, closeDB = bootActive(t, db, "", []analysis.VariantCombination{"ewma:100"})
	defer closeDB()
	if got := vm.GetActiveCombination(); got != "ewma:100" {
		t.Errorf("active = %s, want first learned ewma:100 when neither stored nor cumulative:50 is learned", got)
	}
}
