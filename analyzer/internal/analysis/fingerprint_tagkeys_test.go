package analysis

import (
	"testing"

	"github.com/trace-analyzer/internal/models"
)

func scopedTrace(scopeKey string) *models.Trace {
	tags := func() map[string]string { return map[string]string{scopeKey: "payments", "env": "prod"} }
	return &models.Trace{Spans: []*models.Span{
		goldenSpan("s1", "", "api", "POST /pay", tags()),
		goldenSpan("s2", "s1", "ledger", "write", tags()),
	}}
}

func TestFingerprinter_CustomScopeTagKeys(t *testing.T) {
	custom := NewFingerprinterWithTagKeys(models.GroupingTagKeys{Scope1: []string{"team.scope1"}})
	def := NewFingerprinter()

	want := def.Compute(scopedTrace("scope1"))
	got := custom.Compute(scopedTrace("team.scope1"))
	if got != want {
		t.Errorf("custom keys on team.scope1 = %s, want default on scope1 = %s", got, want)
	}
	if other := def.Compute(scopedTrace("team.scope1")); other == want {
		t.Errorf("default fingerprinter read team.scope1 as scope1 (%s)", other)
	}
}
