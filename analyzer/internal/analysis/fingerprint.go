package analysis

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/trace-analyzer/internal/models"
)

// Fingerprinter computes structural fingerprints for traces using the
// Weisfeiler-Lehman (WL) graph hashing algorithm. The WL algorithm iteratively
// refines node labels by incorporating neighborhood structure - after k iterations,
// each node's label encodes k levels of its neighborhood topology.
//
// For production microservice traces, deep call chains (20+ services) are common.
// We use 20 iterations to ensure the fingerprint captures the full trace structure,
// making traces with identical topology hash to the same fingerprint.
type Fingerprinter struct {
	iterations int
	tagKeys    models.GroupingTagKeys
}

// NewFingerprinter creates a new fingerprinter with 20 WL iterations.
// 20 iterations captures up to 20 levels of call depth, sufficient for
// most production distributed traces while remaining performant (O(n*k)
// where n=spans, k=iterations, using fast xxhash).
func NewFingerprinter() *Fingerprinter {
	return NewFingerprinterWithTagKeys(models.DefaultGroupingTagKeys())
}

// NewFingerprinterWithTagKeys is NewFingerprinter reading grouping values from
// the given tag keys. Empty lists fall back to the defaults.
func NewFingerprinterWithTagKeys(keys models.GroupingTagKeys) *Fingerprinter {
	return &Fingerprinter{iterations: 20, tagKeys: keys.WithDefaults()}
}

// Compute generates a Weisfeiler-Lehman hash for trace topology
func (f *Fingerprinter) Compute(trace *models.Trace) string {
	if trace == nil || len(trace.Spans) == 0 {
		return "empty"
	}

	spanMap := make(map[string]*models.Span)
	children := make(map[string][]*models.Span)

	for _, span := range trace.Spans {
		spanMap[span.SpanID] = span
		if !span.IsRoot() {
			children[span.ParentID] = append(children[span.ParentID], span)
		}
	}

	labels := make(map[string]uint64)
	for _, span := range trace.Spans {
		sig := spanTopologySignature(span, f.tagKeys)
		labels[span.SpanID] = xxhash.Sum64String(sig)
	}

	for i := 0; i < f.iterations; i++ {
		newLabels := make(map[string]uint64)

		for _, span := range trace.Spans {
			childLabels := make([]uint64, 0)
			for _, child := range children[span.SpanID] {
				childLabels = append(childLabels, labels[child.SpanID])
			}
			sort.Slice(childLabels, func(i, j int) bool { return childLabels[i] < childLabels[j] })

			h := xxhash.New()
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], labels[span.SpanID])
			h.Write(buf[:])
			for _, cl := range childLabels {
				binary.LittleEndian.PutUint64(buf[:], cl)
				h.Write(buf[:])
			}
			newLabels[span.SpanID] = h.Sum64()
		}

		labels = newLabels
	}

	allLabels := make([]uint64, 0, len(labels))
	for _, label := range labels {
		allLabels = append(allLabels, label)
	}
	sort.Slice(allLabels, func(i, j int) bool { return allLabels[i] < allLabels[j] })

	h := xxhash.New()
	var buf [8]byte
	for _, label := range allLabels {
		binary.LittleEndian.PutUint64(buf[:], label)
		h.Write(buf[:])
	}

	return fmt.Sprintf("%016x", h.Sum64())
}

func spanTopologySignature(span *models.Span, keys models.GroupingTagKeys) string {
	if span == nil {
		return ""
	}
	serviceIdentity := models.Lookup(span.Tags, keys.ServiceIdentity)
	if serviceIdentity == "" {
		serviceIdentity = span.ServiceName
	}

	scope1 := models.Lookup(span.Tags, keys.Scope1)
	scope2 := models.Lookup(span.Tags, keys.Scope2)
	scope3 := models.Lookup(span.Tags, keys.Scope3)
	env := models.Lookup(span.Tags, keys.Env)
	featureGroup := models.Lookup(span.Tags, keys.FeatureGroup)
	featureName := models.Lookup(span.Tags, keys.FeatureName)
	subService := models.Lookup(span.Tags, keys.SubService)

	operation := span.OperationName
	if operation == "" {
		operation = models.Lookup(span.Tags, []string{"operation", "operation_name"})
	}

	parts := []string{
		serviceIdentity,
		scope1,
		scope2,
		scope3,
		env,
		featureGroup,
		featureName,
		subService,
		operation,
	}

	return strings.Join(parts, "|")
}

// ExtractBranches returns all unique branches (edges) in the trace
func (f *Fingerprinter) ExtractBranches(trace *models.Trace) []Branch {
	if trace == nil || len(trace.Spans) == 0 {
		return nil
	}

	spanMap := make(map[string]*models.Span)
	for _, span := range trace.Spans {
		spanMap[span.SpanID] = span
	}

	var branches []Branch
	for _, span := range trace.Spans {
		if !span.IsRoot() {
			parent, ok := spanMap[span.ParentID]
			if ok {
				b := Branch{
					ParentService:   parent.ServiceName,
					ParentOperation: parent.OperationName,
					ChildService:    span.ServiceName,
					ChildOperation:  span.OperationName,
					DurationUs:      span.Duration,
				}
				b.key = b.formatKey()
				branches = append(branches, b)
			}
		}
	}

	return branches
}

// Branch represents an edge in the trace graph
type Branch struct {
	ParentService   string
	ParentOperation string
	ChildService    string
	ChildOperation  string
	DurationUs      int64
	key             string // set by ExtractBranches so each combination reuses it
}

// Key returns a unique identifier for this branch type
func (b Branch) Key() string {
	if b.key != "" {
		return b.key
	}
	return b.formatKey()
}

func (b Branch) formatKey() string {
	return b.ParentService + ":" + b.ParentOperation + "->" + b.ChildService + ":" + b.ChildOperation
}
