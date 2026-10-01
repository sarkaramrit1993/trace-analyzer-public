package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
)

func TestLoadConfig_ScopeTagKeysFromEnv(t *testing.T) {
	t.Setenv("DATA_PATH", t.TempDir())
	t.Setenv("SCOPE1_TAG_KEYS", " team.scope1 , scope1 ")
	t.Setenv("SCOPE2_TAG_KEYS", "")
	t.Setenv("SERVICE_IDENTITY_TAG_KEYS", "svc.id,,service_identity")

	c := loadConfig()
	d := models.DefaultGroupingTagKeys()
	if want := []string{"team.scope1", "scope1"}; !reflect.DeepEqual(c.GroupingTagKeys.Scope1, want) {
		t.Errorf("Scope1 = %v, want %v", c.GroupingTagKeys.Scope1, want)
	}
	if !reflect.DeepEqual(c.GroupingTagKeys.Scope2, d.Scope2) {
		t.Errorf("empty SCOPE2_TAG_KEYS changed Scope2 to %v", c.GroupingTagKeys.Scope2)
	}
	if !reflect.DeepEqual(c.GroupingTagKeys.Scope3, d.Scope3) {
		t.Errorf("unset SCOPE3_TAG_KEYS changed Scope3 to %v", c.GroupingTagKeys.Scope3)
	}
	if want := []string{"svc.id", "service_identity"}; !reflect.DeepEqual(c.GroupingTagKeys.ServiceIdentity, want) {
		t.Errorf("ServiceIdentity = %v, want %v", c.GroupingTagKeys.ServiceIdentity, want)
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV(" a, ,b ,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitCSV = %v", got)
	}
	if got := splitCSV(" , "); got != nil {
		t.Errorf("splitCSV blanks = %v, want nil", got)
	}
}

func TestLoadConfig_MinSamplesEnvWarnsAndIsIgnored(t *testing.T) {
	t.Setenv("DATA_PATH", t.TempDir())
	t.Setenv("MIN_SAMPLES_FOR_BASELINE", "999")
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	c := loadConfig()
	if !strings.Contains(buf.String(), "MIN_SAMPLES_FOR_BASELINE") || !strings.Contains(buf.String(), `"level":"warn"`) {
		t.Errorf("no warning for MIN_SAMPLES_FOR_BASELINE: %s", buf.String())
	}
	d := models.DefaultConfig()
	d.DataPath = c.DataPath
	if !reflect.DeepEqual(c, d) {
		t.Errorf("MIN_SAMPLES_FOR_BASELINE changed config:\n got %+v\nwant %+v", c, d)
	}
}

func TestLoadConfig_ActiveAndLearnFromEnv(t *testing.T) {
	t.Setenv("DATA_PATH", t.TempDir())
	if c := loadConfig(); c.ActiveCombination != "cumulative:50" || c.LearnCombinations != nil {
		t.Fatalf("defaults: active %q learn %v", c.ActiveCombination, c.LearnCombinations)
	}

	t.Setenv("ACTIVE_COMBINATION", " ewma:500 ")
	t.Setenv("LEARN_COMBINATIONS", "ewma:500, cumulative:100")
	c := loadConfig()
	if c.ActiveCombination != "ewma:500" {
		t.Errorf("ActiveCombination = %q", c.ActiveCombination)
	}
	if want := []string{"ewma:500", "cumulative:100"}; !reflect.DeepEqual(c.LearnCombinations, want) {
		t.Errorf("LearnCombinations = %v, want %v", c.LearnCombinations, want)
	}

	t.Setenv("LEARN_COMBINATIONS", "active")
	if c := loadConfig(); !reflect.DeepEqual(c.LearnCombinations, []string{"ewma:500"}) {
		t.Errorf("active: LearnCombinations = %v", c.LearnCombinations)
	}
}

func TestLearnCombinations_InvalidIsAnErrorListingValidNames(t *testing.T) {
	for _, bad := range []string{"ewma:42", "bogus:50", "cumulative:50,nope", " , "} {
		got, err := learnCombinations(bad, "cumulative:50")
		if err == nil {
			t.Errorf("learnCombinations(%q) = %v, want error", bad, got)
			continue
		}
		for _, want := range []string{"LEARN_COMBINATIONS", "all", "active", "cumulative:50", "same_time_yesterday:5000"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: error %q does not mention %q", bad, err, want)
			}
		}
	}
	if got, err := learnCombinations("all", "cumulative:50"); err != nil || got != nil {
		t.Errorf("all: %v, %v", got, err)
	}
	if got, err := learnCombinations("active", "ewma:500"); err != nil || !reflect.DeepEqual(got, []string{"ewma:500"}) {
		t.Errorf("active: %v, %v", got, err)
	}
}

func TestApplyActiveCombination_RejectsUnknownAndNotLearned(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"cumulative:50", "ewma:100"}})
	for _, bad := range []string{"bogus:7", "ewma:500"} {
		err := applyActiveCombination(vm, bad)
		if err == nil {
			t.Errorf("applyActiveCombination(%q) accepted", bad)
			continue
		}
		for _, want := range []string{bad, "cumulative:50", "ewma:100"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	}
	if vm.GetActiveCombination() != "cumulative:50" {
		t.Errorf("failed apply changed active to %s", vm.GetActiveCombination())
	}
	if err := applyActiveCombination(vm, "ewma:100"); err != nil || vm.GetActiveCombination() != "ewma:100" {
		t.Errorf("apply ewma:100: err %v active %s", err, vm.GetActiveCombination())
	}
}

func TestHourlyPathSampleCapFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want int
		warn bool
	}{
		{"", analysis.DefaultHourlyPathSampleCap, false},
		{"50", 50, false},
		{" 1000 ", 1000, false},
		{"0", analysis.DefaultHourlyPathSampleCap, true},
		{"-5", analysis.DefaultHourlyPathSampleCap, true},
		{"lots", analysis.DefaultHourlyPathSampleCap, true},
	}
	for _, c := range cases {
		t.Setenv("HOURLY_PATH_SAMPLE_CAP", c.env)
		var buf bytes.Buffer
		prev := log.Logger
		log.Logger = zerolog.New(&buf)
		got := hourlyPathSampleCap()
		log.Logger = prev
		if got != c.want {
			t.Errorf("%q: cap = %d, want %d", c.env, got, c.want)
		}
		if warned := strings.Contains(buf.String(), `"level":"warn"`); warned != c.warn {
			t.Errorf("%q: warned = %v, want %v (%s)", c.env, warned, c.warn, buf.String())
		}
	}
}
