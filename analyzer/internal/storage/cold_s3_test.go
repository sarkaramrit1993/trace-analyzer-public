package storage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestColdTierStats_CountsRollupsAndInterestingTraces(t *testing.T) {
	objects := map[string]int{
		"rollups/2026/09/30/20/rollups_1.json.gz":                100,
		"interesting-traces/2026/09/30/20/interesting_1.json.gz": 40,
		"interesting-traces/2026/09/30/21/interesting_2.json.gz": 60,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := r.URL.Query().Get("prefix")
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>trace-cold</Name><IsTruncated>false</IsTruncated>`)
		for key, size := range objects {
			if strings.HasPrefix(key, prefix) {
				fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>%d</Size></Contents>", key, size)
			}
		}
		b.WriteString("</ListBucketResult>")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	cold, err := NewColdTierStorage(S3Config{Endpoint: srv.URL, AccessKey: "k", SecretKey: "s", Bucket: "trace-cold", Region: "us-east-1"})
	if err != nil {
		t.Fatalf("new cold storage: %v", err)
	}
	count, bytes, err := cold.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if count != 3 || bytes != 200 {
		t.Errorf("expected 3 objects / 200 bytes, got %d / %d", count, bytes)
	}
}
