package analysis

import (
	"testing"

	"github.com/trace-analyzer/internal/models"
)

func TestFingerprinter_Compute(t *testing.T) {
	fp := NewFingerprinter()

	t.Run("empty trace", func(t *testing.T) {
		result := fp.Compute(nil)
		if result != "empty" {
			t.Errorf("expected 'empty', got %s", result)
		}

		result = fp.Compute(&models.Trace{})
		if result != "empty" {
			t.Errorf("expected 'empty', got %s", result)
		}
	})

	t.Run("single span", func(t *testing.T) {
		trace := &models.Trace{
			Spans: []*models.Span{
				{SpanID: "s1", ServiceName: "api-gateway", OperationName: "GET /users", ParentID: ""},
			},
		}
		result := fp.Compute(trace)
		if result == "" || result == "empty" {
			t.Error("expected valid fingerprint")
		}
	})

	t.Run("same structure same fingerprint", func(t *testing.T) {
		trace1 := &models.Trace{
			Spans: []*models.Span{
				{SpanID: "s1", ServiceName: "api", OperationName: "op1", ParentID: ""},
				{SpanID: "s2", ParentID: "s1", ServiceName: "db", OperationName: "query"},
			},
		}

		trace2 := &models.Trace{
			Spans: []*models.Span{
				{SpanID: "x1", ServiceName: "api", OperationName: "op1", ParentID: ""},
				{SpanID: "x2", ParentID: "x1", ServiceName: "db", OperationName: "query"},
			},
		}

		fp1 := fp.Compute(trace1)
		fp2 := fp.Compute(trace2)

		if fp1 != fp2 {
			t.Errorf("same structure should have same fingerprint: %s vs %s", fp1, fp2)
		}
	})

	t.Run("different structure different fingerprint", func(t *testing.T) {
		trace1 := &models.Trace{
			Spans: []*models.Span{
				{SpanID: "s1", ServiceName: "api", OperationName: "op1", ParentID: ""},
			},
		}

		trace2 := &models.Trace{
			Spans: []*models.Span{
				{SpanID: "s1", ServiceName: "api", OperationName: "op1", ParentID: ""},
				{SpanID: "s2", ParentID: "s1", ServiceName: "db", OperationName: "query"},
			},
		}

		fp1 := fp.Compute(trace1)
		fp2 := fp.Compute(trace2)

		if fp1 == fp2 {
			t.Error("different structure should have different fingerprint")
		}
	})
}

func TestFingerprinter_ExtractBranches(t *testing.T) {
	fp := NewFingerprinter()

	trace := &models.Trace{
		Spans: []*models.Span{
			{SpanID: "s1", ServiceName: "api", OperationName: "handler", Duration: 100, ParentID: ""},
			{SpanID: "s2", ParentID: "s1", ServiceName: "db", OperationName: "query", Duration: 50},
		},
	}

	branches := fp.ExtractBranches(trace)

	if len(branches) != 1 {
		t.Errorf("expected 1 branch, got %d", len(branches))
	}

	if branches[0].ParentService != "api" || branches[0].ChildService != "db" {
		t.Error("branch parent/child incorrect")
	}

	if branches[0].Key() != "api:handler->db:query" {
		t.Errorf("unexpected branch key: %s", branches[0].Key())
	}
}
