package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/api"
	"github.com/trace-analyzer/internal/ingestion"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

func main() {
	// Configure logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Load configuration
	config := loadConfig()

	log.Info().
		Str("sse_endpoint", config.SSEEndpoint).
		Int("api_port", config.APIPort).
		Str("s3_endpoint", config.S3Endpoint).
		Str("s3_bucket", config.S3Bucket).
		Bool("clean_start", config.CleanStart).
		Msg("Starting Trace Analyzer")

	// Clean old data if requested
	if config.CleanStart {
		dbPath := config.DataPath + "/traces.db"
		if _, err := os.Stat(dbPath); err == nil {
			log.Info().Str("db_path", dbPath).Msg("Cleaning old database for fresh start")
			if err := os.Remove(dbPath); err != nil {
				log.Warn().Err(err).Msg("Failed to remove old database")
			}
		}

		// Also clean any related files (WAL, SHM)
		for _, suffix := range []string{"-wal", "-shm"} {
			walPath := dbPath + suffix
			if _, err := os.Stat(walPath); err == nil {
				os.Remove(walPath)
			}
		}
	}

	// Initialize hot storage (SQLite)
	hotStorage, err := storage.NewSQLiteStorage(
		config.DataPath+"/traces.db",
		time.Duration(config.HotRetentionMinutes)*time.Minute,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize hot storage")
	}

	// Initialize cold storage (S3-compatible) if configured
	var coldStorage *storage.ColdTierStorage
	if config.S3Endpoint != "" {
		coldStorage, err = storage.NewColdTierStorage(storage.S3Config{
			Endpoint:  config.S3Endpoint,
			AccessKey: config.S3AccessKey,
			SecretKey: config.S3SecretKey,
			Bucket:    config.S3Bucket,
			UseSSL:    config.S3UseSSL,
			Region:    "us-east-1",
		})
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize cold storage, continuing without it")
		}
	}

	// Initialize compaction service
	compactionService := storage.NewCompactionService(
		hotStorage,
		coldStorage,
		time.Duration(config.HotRetentionMinutes)*time.Minute,
		time.Duration(config.WarmRetentionMinutes)*time.Minute,
		15*time.Minute, // 15-minute rollup intervals
	)
	compactionService.Start()

	// Parse sliding window duration from config
	slidingWindowDuration, err := time.ParseDuration(config.SlidingWindowDuration)
	if err != nil {
		log.Warn().Str("duration", config.SlidingWindowDuration).Msg("Invalid sliding window duration, using default 24h")
		slidingWindowDuration = 24 * time.Hour
	}

	// Initialize analysis components with variant manager for A/B testing
	learn := make([]analysis.VariantCombination, 0, len(config.LearnCombinations))
	for _, c := range config.LearnCombinations {
		learn = append(learn, analysis.VariantCombination(c))
	}
	variantManager := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{
		SlidingWindow:       slidingWindowDuration,
		Combinations:        learn,
		HourlyPathSampleCap: hourlyPathSampleCap(),
	})
	if err := resolveActiveCombination(variantManager, hotStorage, strings.TrimSpace(os.Getenv("ACTIVE_COMBINATION"))); err != nil {
		log.Fatal().Err(err).Msg("Invalid ACTIVE_COMBINATION")
	}
	learned := variantManager.GetAllCombinations()
	learnedNames := make([]string, 0, len(learned))
	for _, c := range learned {
		learnedNames = append(learnedNames, string(c))
	}
	log.Info().
		Int("count", len(learned)).
		Strs("learned_combinations", learnedNames).
		Str("active_combination", string(variantManager.GetActiveCombination())).
		Msg("Baseline combinations configured")
	fingerprinter := analysis.NewFingerprinterWithTagKeys(config.GroupingTagKeys)

	// Load persisted baselines from database (if not clean start)
	if !config.CleanStart {
		loadPersistedBaselines(hotStorage, variantManager)
	}

	var cold *coldWriter
	if coldStorage != nil {
		cold = newColdWriter(coldStorage, 100)
	}

	pipe := &pipeline{
		fingerprinter: fingerprinter,
		variants:      variantManager,
		store:         hotStorage,
		rollups:       compactionService,
		cold:          cold,
		config:        config,
	}

	// Initialize trace assembler with 3-minute window
	windowDuration := 3 * time.Minute
	if config.TraceAssemblyTimeoutSecs > 0 {
		windowDuration = time.Duration(config.TraceAssemblyTimeoutSecs * float64(time.Second))
	}
	assembler := ingestion.NewTraceAssembler(
		windowDuration,
		config.ServiceIdentityFields,
		pipe.handle,
		hotStorage, // Pass storage for disk persistence
	)
	assembler.SetGroupingTagKeys(config.GroupingTagKeys)
	assembler.Start()

	// Span handler - called for each span from SSE
	spanHandler := func(span *models.Span) error {
		return assembler.AddSpan(span)
	}

	// Initialize SSE client (optional)
	var sseClient *ingestion.SSEClient
	trimmedEndpoint := strings.TrimSpace(config.SSEEndpoint)
	if trimmedEndpoint != "" && strings.ToLower(trimmedEndpoint) != "disabled" {
		sseClient = ingestion.NewSSEClient(trimmedEndpoint, spanHandler)
		if err := sseClient.Start(); err != nil {
			log.Fatal().Err(err).Msg("Failed to start SSE client")
		}
	} else {
		log.Info().Msg("SSE client disabled; relying on direct ingestion")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var background sync.WaitGroup
	background.Add(2)

	// Start baseline persistence goroutine (save every 5 minutes)
	go func() {
		defer background.Done()
		persistBaselinesLoop(ctx, hotStorage, variantManager)
	}()

	// Start stats reporter
	go func() {
		defer background.Done()
		reportStats(ctx, sseClient, assembler, hotStorage, compactionService, variantManager)
	}()

	// Start API server with variant manager
	apiServer := api.NewServerWithVariants(hotStorage, variantManager, config.ServiceIdentityFields, spanHandler)
	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", config.APIPort), Handler: apiServer.Handler()}
	var httpDone sync.WaitGroup
	httpDone.Add(1)
	go func() {
		defer httpDone.Done()
		log.Info().Str("addr", httpServer.Addr).Msg("Starting API server")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("API server error")
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down...")

	// Order matters: stop span sources, flush pending traces through the handler
	// (which writes to cold, compaction and storage), then persist and close.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("API server shutdown")
	}
	shutdownCancel()
	httpDone.Wait()

	if sseClient != nil {
		sseClient.Stop()
	}
	assembler.Stop()
	cold.close()

	cancel()
	background.Wait()
	if err := saveAllBaselines(hotStorage, variantManager, zerolog.InfoLevel); err != nil {
		log.Error().Err(err).Msg("Failed to persist baselines at shutdown")
	}

	compactionService.Stop()
	if err := hotStorage.Close(); err != nil {
		log.Warn().Err(err).Msg("Failed to close hot storage")
	}
}

func loadConfig() *models.Config {
	config := models.DefaultConfig()

	// Override from environment
	if v := os.Getenv("SSE_ENDPOINT"); v != "" {
		config.SSEEndpoint = v
	}
	if v := os.Getenv("API_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &config.APIPort)
	}
	if v := os.Getenv("DATA_PATH"); v != "" {
		config.DataPath = v
	}
	if v := os.Getenv("HOT_RETENTION_MINUTES"); v != "" {
		fmt.Sscanf(v, "%d", &config.HotRetentionMinutes)
	}
	if v := os.Getenv("WARM_RETENTION_MINUTES"); v != "" {
		fmt.Sscanf(v, "%d", &config.WarmRetentionMinutes)
	}
	if v := os.Getenv("TRACE_ASSEMBLY_TIMEOUT_SECS"); v != "" {
		fmt.Sscanf(v, "%f", &config.TraceAssemblyTimeoutSecs)
	}
	if os.Getenv("MIN_SAMPLES_FOR_BASELINE") != "" {
		log.Warn().Msg("MIN_SAMPLES_FOR_BASELINE is no longer supported and is ignored; thresholds come from the combination name (e.g. cumulative:50)")
	}
	if v := strings.TrimSpace(os.Getenv("ACTIVE_COMBINATION")); v != "" {
		config.ActiveCombination = v
	}
	if v := os.Getenv("LEARN_COMBINATIONS"); v != "" {
		learn, err := learnCombinations(v, config.ActiveCombination)
		if err != nil {
			log.Fatal().Err(err).Msg("Invalid LEARN_COMBINATIONS")
		}
		config.LearnCombinations = learn
	}
	if v := os.Getenv("DEVIATION_THRESHOLD"); v != "" {
		fmt.Sscanf(v, "%f", &config.DeviationThreshold)
	}
	if v := os.Getenv("ANOMALY_ZSCORE_THRESHOLD"); v != "" {
		fmt.Sscanf(v, "%f", &config.AnomalyZScoreThreshold)
	}
	if v := os.Getenv("ANOMALY_BASELINE_PERCENTILE"); v != "" {
		fmt.Sscanf(v, "%f", &config.AnomalyBaselinePercentile)
	}

	// S3 configuration
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		config.S3Endpoint = v
	}
	if v := os.Getenv("S3_ACCESS_KEY"); v != "" {
		config.S3AccessKey = v
	}
	if v := os.Getenv("S3_SECRET_KEY"); v != "" {
		config.S3SecretKey = v
	}
	if v := os.Getenv("S3_BUCKET"); v != "" {
		config.S3Bucket = v
	}
	if v := os.Getenv("S3_USE_SSL"); v != "" {
		config.S3UseSSL, _ = strconv.ParseBool(v)
	}
	if fields := splitCSV(os.Getenv("SERVICE_IDENTITY_FIELDS")); len(fields) > 0 {
		config.ServiceIdentityFields = fields
	}
	for env, dst := range map[string]*[]string{
		"SERVICE_IDENTITY_TAG_KEYS": &config.GroupingTagKeys.ServiceIdentity,
		"SCOPE1_TAG_KEYS":           &config.GroupingTagKeys.Scope1,
		"SCOPE2_TAG_KEYS":           &config.GroupingTagKeys.Scope2,
		"SCOPE3_TAG_KEYS":           &config.GroupingTagKeys.Scope3,
	} {
		if keys := splitCSV(os.Getenv(env)); len(keys) > 0 {
			*dst = keys
		}
	}
	if v := os.Getenv("CLEAN_START"); v != "" {
		config.CleanStart, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("SLIDING_WINDOW_DURATION"); v != "" {
		config.SlidingWindowDuration = v // e.g., "24h", "168h", "720h"
	}

	// Ensure data directory exists
	if err := os.MkdirAll(config.DataPath, 0755); err != nil {
		log.Warn().Err(err).Str("path", config.DataPath).Msg("Failed to create data directory")
	}

	return config
}

// applyActiveCombination makes s the active combination, or explains which
// learned combinations are valid.
func applyActiveCombination(vm *analysis.VariantManager, s string) error {
	if vm.SetActiveCombination(analysis.VariantCombination(s)) {
		return nil
	}
	learned := make([]string, 0)
	for _, c := range vm.GetAllCombinations() {
		learned = append(learned, string(c))
	}
	return fmt.Errorf("ACTIVE_COMBINATION %q is not a learned combination (check LEARN_COMBINATIONS); valid: %s", s, strings.Join(learned, ", "))
}

// resolveActiveCombination picks the starting active combination: env (an
// explicitly set ACTIVE_COMBINATION, which must be learned), else the one last
// chosen through the API if it is still learned, else the manager's default
// (cumulative:50 when learned, otherwise the first learned).
func resolveActiveCombination(vm *analysis.VariantManager, store *storage.SQLiteStorage, env string) error {
	if env != "" {
		return applyActiveCombination(vm, env)
	}
	stored, err := store.LoadActiveCombination()
	if err != nil {
		log.Warn().Err(err).Str("active_combination", string(vm.GetActiveCombination())).Msg("Could not read the saved active combination, using the default")
		return nil
	}
	if stored != "" && !vm.SetActiveCombination(analysis.VariantCombination(stored)) {
		log.Warn().Str("saved", stored).Str("active_combination", string(vm.GetActiveCombination())).Msg("Saved active combination is not learned (check LEARN_COMBINATIONS), using the default")
	}
	return nil
}

// learnCombinations parses a LEARN_COMBINATIONS value; nil means all 20.
func learnCombinations(v, active string) ([]string, error) {
	learn, err := analysis.ParseLearnCombinations(v, analysis.VariantCombination(active))
	if err != nil {
		all := make([]string, 0, len(analysis.AllCombinations()))
		for _, c := range analysis.AllCombinations() {
			all = append(all, string(c))
		}
		return nil, fmt.Errorf("LEARN_COMBINATIONS %q: %w; valid: all, active, or a comma-separated list of %s", v, err, strings.Join(all, ", "))
	}
	var out []string
	for _, c := range learn {
		out = append(out, string(c))
	}
	return out, nil
}

// splitCSV splits a comma-separated value, trimming spaces and dropping blanks.
func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// hourlyPathSampleCap reads HOURLY_PATH_SAMPLE_CAP; unset or invalid uses the default.
func hourlyPathSampleCap() int {
	v := strings.TrimSpace(os.Getenv("HOURLY_PATH_SAMPLE_CAP"))
	if v == "" {
		return analysis.DefaultHourlyPathSampleCap
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Warn().Str("value", v).Int("default", analysis.DefaultHourlyPathSampleCap).Msg("Invalid HOURLY_PATH_SAMPLE_CAP, using default")
		return analysis.DefaultHourlyPathSampleCap
	}
	return n
}

func reportStats(ctx context.Context, sseClient *ingestion.SSEClient, assembler *ingestion.TraceAssembler, store *storage.SQLiteStorage, compaction *storage.CompactionService, variants *analysis.VariantManager) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var sseReceived, sseErrors int64
			if sseClient != nil {
				sseReceived, sseErrors = sseClient.Stats()
			}
			spansReceived, tracesEmitted, pending := assembler.Stats()
			storageStats := store.Stats()
			pendingRollups, coldObjects, coldBytes := compaction.Stats()
			hourlyBuckets, hourlySamples := variants.HourlyStats()

			log.Info().
				Int64("sse_spans", sseReceived).
				Int64("sse_errors", sseErrors).
				Int64("spans_processed", spansReceived).
				Int64("traces_emitted", tracesEmitted).
				Int64("pending_traces", pending).
				Int64("stored_traces", storageStats.TraceCount).
				Int64("deviations", storageStats.DeviationCount).
				Int64("anomalies", storageStats.AnomalyCount).
				Int("pending_rollups", pendingRollups).
				Int64("cold_objects", coldObjects).
				Int64("cold_bytes", coldBytes).
				Int("hourly_buckets", hourlyBuckets).
				Int("hourly_samples", hourlySamples).
				Msg("Stats")
		}
	}
}

// variantFinding is what one variant combination flagged for a trace.
type variantFinding struct {
	variant   string
	deviation *models.Deviation
	anomaly   *models.Anomaly
}

// analyzeTrace runs deviation and anomaly detection on every variant combination,
// then teaches the trace to every combination. Detection must see the baseline
// as it was before this trace: otherwise an unseen path already has a count of 1
// and can never be reported as new, and anomaly scoring includes its own sample.
func analyzeTrace(vm *analysis.VariantManager, trace *models.Trace, fingerprint string, config *models.Config) []variantFinding {
	var findings []variantFinding
	for name, baseline := range vm.GetAllBaselines() {
		finding := variantFinding{
			variant:   name,
			deviation: analysis.NewDeviationDetector(baseline, config.DeviationThreshold).Check(trace, fingerprint),
			anomaly:   analysis.NewAnomalyScorer(baseline, config.AnomalyZScoreThreshold, config.AnomalyBaselinePercentile).Score(trace, fingerprint),
		}
		if finding.deviation != nil || finding.anomaly != nil {
			findings = append(findings, finding)
		}
	}
	vm.UpdateAll(trace, fingerprint)
	return findings
}

// loadStats counts where each (service, combination) state came from; Legacy
// counts services whose legacy row seeded at least one combination.
type loadStats struct {
	Own, Sibling, Legacy, Ignored, Corrupt, Hourly int
}

// loadPersistedBaselines restores each learned combination from its own row,
// else from the lowest-threshold stored row of the same variant, else from the
// service's legacy single-row baseline when no row of that variant is stored.
// Rows nothing learned can use are ignored.
func loadPersistedBaselines(store *storage.SQLiteStorage, vm *analysis.VariantManager) loadStats {
	var st loadStats
	keys, err := store.CombinationBaselineKeys()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read persisted baselines, starting fresh")
		return st
	}

	learned := vm.GetAllCombinations()
	sources := make(map[string]map[string][]analysis.VariantCombination, len(keys))
	for svc, present := range keys {
		sources[svc] = make(map[string][]analysis.VariantCombination)
		for _, c := range learned {
			src := string(c)
			if !present[src] {
				if src = lowestSibling(c, present); src == "" {
					continue
				}
			}
			sources[svc][src] = append(sources[svc][src], c)
		}
	}

	st.Corrupt, err = store.ForEachCombinationBaseline(func(svc, combination string, data *storage.BaselineData) {
		targets := sources[svc][combination]
		if len(targets) == 0 {
			st.Ignored++
			log.Debug().Str("service", svc).Str("combination", combination).Msg("Ignoring baseline row for a combination that is not learned")
			return
		}
		exported := convertStorageToAnalysis(data)
		for _, c := range targets {
			vm.ImportBaseline(c, exported)
			if string(c) == combination {
				st.Own++
			} else {
				st.Sibling++
			}
		}
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed while reading persisted baselines; some combinations start fresh")
	}

	legacy, err := store.LoadBaselines()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read legacy baselines")
	}
	for _, data := range legacy {
		var exported *analysis.ExportedBaseline
		for _, c := range learned {
			if lowestSibling(c, keys[data.ServiceID]) != "" {
				continue
			}
			if exported == nil {
				exported = convertStorageToAnalysis(data)
				st.Legacy++
			}
			vm.ImportBaseline(c, exported)
		}
	}

	st.Hourly = loadHourlyHistory(store, vm)

	log.Info().
		Int("own", st.Own).
		Int("sibling", st.Sibling).
		Int("legacy_services", st.Legacy).
		Int("ignored", st.Ignored).
		Int("corrupt", st.Corrupt).
		Int("hourly_buckets", st.Hourly).
		Msg("Loaded persisted baselines")
	return st
}

// lowestSibling is the stored combination with c's variant and the lowest
// threshold, or "" when there is none.
func lowestSibling(c analysis.VariantCombination, present map[string]bool) string {
	variant, _, err := analysis.ParseCombination(string(c))
	if err != nil {
		return ""
	}
	best, bestThreshold := "", 0
	for stored := range present {
		v, threshold, err := analysis.ParseCombination(stored)
		if err == nil && v == variant && (best == "" || threshold < bestThreshold) {
			best, bestThreshold = stored, threshold
		}
	}
	return best
}

func learnsSameTimeYesterday(vm *analysis.VariantManager) bool {
	for _, c := range vm.GetAllCombinations() {
		if v, _, err := analysis.ParseCombination(string(c)); err == nil && v == analysis.VariantSameTimeYesterday {
			return true
		}
	}
	return false
}

func loadHourlyHistory(store *storage.SQLiteStorage, vm *analysis.VariantManager) int {
	if !learnsSameTimeYesterday(vm) {
		return 0
	}
	rows, err := store.LoadHourlyBaselines()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read persisted hourly history")
		return 0
	}
	buckets := make([]analysis.ExportedHourlyBucket, 0, len(rows))
	for _, r := range rows {
		var b analysis.ExportedHourlyBucket
		if err := json.Unmarshal(r.DataJSON, &b); err != nil {
			log.Warn().Err(err).Str("hour", r.Hour).Str("service", r.ServiceID).Msg("Skipping corrupt hourly row")
			continue
		}
		buckets = append(buckets, b)
	}
	vm.ImportHourlyHistory(buckets)
	return len(buckets)
}

// persistBaselinesLoop periodically saves baselines to SQLite
func persistBaselinesLoop(ctx context.Context, store *storage.SQLiteStorage, variantManager *analysis.VariantManager) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := saveAllBaselines(store, variantManager, zerolog.DebugLevel); err != nil {
				log.Warn().Err(err).Msg("Failed to persist baselines")
			}
		}
	}
}

// hourlyFullExport is set when an hourly save fails. Exporting marks buckets
// clean, so without it a past hour that failed to save would never be retried.
var hourlyFullExport atomic.Bool

// saveAllBaselines writes every learned combination's state per service in one
// transaction, one row at a time, then the changed same_time_yesterday buckets.
func saveAllBaselines(store *storage.SQLiteStorage, vm *analysis.VariantManager, level zerolog.Level) error {
	start := time.Now()
	combinations := vm.GetAllCombinations()
	sort.Slice(combinations, func(i, j int) bool { return combinations[i] < combinations[j] })

	rows, bytes, err := store.SaveCombinationBaselines(func(put func(string, string, *storage.BaselineData) error) error {
		for _, c := range combinations {
			bc := vm.GetCombination(c)
			ids := bc.GetAllServiceIDs()
			sort.Strings(ids)
			for _, id := range ids {
				exported := bc.ExportBaseline(id)
				if exported == nil {
					continue
				}
				if err := put(id, string(c), convertAnalysisToStorage(exported)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		err = fmt.Errorf("save combination baselines: %w", err)
	}

	hourlyRows, hourlyDeleted, hourlyErr := saveHourlyHistory(store, vm)
	log.WithLevel(level).
		Int("rows", rows).
		Int("bytes", bytes).
		Int("hourly_rows", hourlyRows).
		Int64("hourly_deleted", hourlyDeleted).
		Dur("took", time.Since(start)).
		Msg("Persisted baselines")
	return errors.Join(err, hourlyErr)
}

func saveHourlyHistory(store *storage.SQLiteStorage, vm *analysis.VariantManager) (int, int64, error) {
	if !learnsSameTimeYesterday(vm) {
		return 0, 0, nil
	}
	buckets := vm.ExportHourlyHistory(!hourlyFullExport.Load())
	rows := make([]storage.HourlyBaselineRow, 0, len(buckets))
	for _, b := range buckets {
		raw, err := json.Marshal(b)
		if err != nil {
			hourlyFullExport.Store(true)
			return 0, 0, fmt.Errorf("marshal hourly bucket %s/%s: %w", b.Hour, b.ServiceID, err)
		}
		rows = append(rows, storage.HourlyBaselineRow{Hour: b.Hour, ServiceID: b.ServiceID, DataJSON: raw})
	}
	n, deleted, err := store.SaveHourlyBaselines(rows, vm.RetainedHourKeys())
	hourlyFullExport.Store(err != nil)
	if err != nil {
		return 0, 0, fmt.Errorf("save hourly history: %w", err)
	}
	return n, deleted, nil
}

// convertStorageToAnalysis converts storage format to analysis format
func convertStorageToAnalysis(data *storage.BaselineData) *analysis.ExportedBaseline {
	pathBaselines := make(map[string]*analysis.ExportedPathBaseline)
	for fp, pb := range data.PathBaselines {
		pathBaselines[fp] = &analysis.ExportedPathBaseline{
			Count:          pb.Count,
			SumDuration:    pb.SumDuration,
			MinDuration:    pb.MinDuration,
			MaxDuration:    pb.MaxDuration,
			Samples:        pb.Samples,
			LastUpdateTime: pb.LastUpdateTime,
		}
	}

	branchBaselines := make(map[string]*analysis.ExportedBranchBaseline)
	for key, bb := range data.BranchBaselines {
		branchBaselines[key] = &analysis.ExportedBranchBaseline{
			Count:       bb.Count,
			SumDuration: bb.SumDuration,
			MinDuration: bb.MinDuration,
			MaxDuration: bb.MaxDuration,
			ErrorCount:  bb.ErrorCount,
			Samples:     bb.Samples,
		}
	}

	return &analysis.ExportedBaseline{
		ServiceID:               data.ServiceID,
		ServiceGrouping:         data.ServiceGrouping,
		TopologyCounts:          data.TopologyCounts,
		TopologyFirstSeen:       data.TopologyFirstSeen,
		KnownGoodPaths:          data.KnownGoodPaths,
		CanonicalFingerprint:    data.CanonicalFingerprint,
		TotalTraces:             data.TotalTraces,
		ErrorCount:              data.ErrorCount,
		DurationSum:             data.DurationSum,
		DurationMin:             data.DurationMin,
		DurationMax:             data.DurationMax,
		DurationSamples:         data.DurationSamples,
		PathBaselines:           pathBaselines,
		BranchBaselines:         branchBaselines,
		TopologyFrequenciesEWMA: data.TopologyFrequenciesEWMA,
		LastUpdateTime:          data.LastUpdateTime,
		LastSeen:                data.LastSeen,
		Clock:                   data.Clock,
	}
}

// convertAnalysisToStorage converts analysis format to storage format
func convertAnalysisToStorage(exported *analysis.ExportedBaseline) *storage.BaselineData {
	pathBaselines := make(map[string]*storage.PathBaselineData)
	for fp, pb := range exported.PathBaselines {
		pathBaselines[fp] = &storage.PathBaselineData{
			Count:          pb.Count,
			SumDuration:    pb.SumDuration,
			MinDuration:    pb.MinDuration,
			MaxDuration:    pb.MaxDuration,
			Samples:        pb.Samples,
			LastUpdateTime: pb.LastUpdateTime,
		}
	}

	branchBaselines := make(map[string]*storage.BranchBaselineData)
	for key, bb := range exported.BranchBaselines {
		branchBaselines[key] = &storage.BranchBaselineData{
			Count:       bb.Count,
			SumDuration: bb.SumDuration,
			MinDuration: bb.MinDuration,
			MaxDuration: bb.MaxDuration,
			ErrorCount:  bb.ErrorCount,
			Samples:     bb.Samples,
		}
	}

	return &storage.BaselineData{
		ServiceID:               exported.ServiceID,
		ServiceGrouping:         exported.ServiceGrouping,
		TopologyCounts:          exported.TopologyCounts,
		TopologyFirstSeen:       exported.TopologyFirstSeen,
		KnownGoodPaths:          exported.KnownGoodPaths,
		CanonicalFingerprint:    exported.CanonicalFingerprint,
		TotalTraces:             exported.TotalTraces,
		ErrorCount:              exported.ErrorCount,
		DurationSum:             exported.DurationSum,
		DurationMin:             exported.DurationMin,
		DurationMax:             exported.DurationMax,
		DurationSamples:         exported.DurationSamples,
		PathBaselines:           pathBaselines,
		BranchBaselines:         branchBaselines,
		TopologyFrequenciesEWMA: exported.TopologyFrequenciesEWMA,
		LastUpdateTime:          exported.LastUpdateTime,
		LastSeen:                exported.LastSeen,
		Clock:                   exported.Clock,
	}
}
