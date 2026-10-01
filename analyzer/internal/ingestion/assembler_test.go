package ingestion

import (
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func groupingSpans(tags map[string]string) []*models.Span {
	return []*models.Span{
		{TraceID: "t", SpanID: "r", ServiceName: "root-svc", OperationName: "GET /", Tags: tags},
		{TraceID: "t", SpanID: "c", ParentID: "r", ServiceName: "child", OperationName: "q"},
	}
}

func TestAssembler_DefaultGroupingTagKeys(t *testing.T) {
	a := NewTraceAssembler(time.Minute, []string{"service_identity", "scope1"}, nil, nil)
	trace := a.buildTrace("t", groupingSpans(map[string]string{"service_identity": "svc", "scope_1": "a"}))
	if trace.ServiceID != "svc:a" {
		t.Errorf("ServiceID = %q, want %q", trace.ServiceID, "svc:a")
	}
	if trace.ServiceGrouping.Scope1 != "a" {
		t.Errorf("Scope1 = %q, want %q", trace.ServiceGrouping.Scope1, "a")
	}
}

func TestAssembler_CustomGroupingTagKeys(t *testing.T) {
	a := NewTraceAssembler(time.Minute, []string{"service_identity", "scope1"}, nil, nil)
	a.SetGroupingTagKeys(models.GroupingTagKeys{Scope1: []string{"team.scope1"}})
	trace := a.buildTrace("t", groupingSpans(map[string]string{"service_identity": "svc", "team.scope1": "payments", "scope1": "ignored"}))
	if trace.ServiceID != "svc:payments" {
		t.Errorf("ServiceID = %q, want %q", trace.ServiceID, "svc:payments")
	}
}

func TestAssembler_GroupingFallsBackToChildSpans(t *testing.T) {
	a := NewTraceAssembler(time.Minute, []string{"service_identity", "scope1"}, nil, nil)
	spans := groupingSpans(map[string]string{"service_identity": "svc"})
	spans[1].Tags = map[string]string{"scope1": "from-child"}
	if got := a.buildTrace("t", spans).ServiceID; got != "svc:from-child" {
		t.Errorf("ServiceID = %q, want %q", got, "svc:from-child")
	}
}
