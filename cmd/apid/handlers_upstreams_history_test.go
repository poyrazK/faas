package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseDataUpstreamHistoryQueryDefaults(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 34, 56, 0, time.UTC)
	q, prob := parseDataUpstreamHistoryQuery(httptest.NewRequest("GET", "/v1/apps/demo/upstreams/history", nil), now)
	if prob != nil {
		t.Fatalf("unexpected problem: %+v", prob)
	}
	if !q.To.Equal(now) || !q.From.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("default window = [%s,%s], want [%s,%s]", q.From, q.To, now.Add(-24*time.Hour), now)
	}
	if q.Bucket != 5*time.Minute {
		t.Fatalf("default bucket = %s, want 5m", q.Bucket)
	}
}

func TestParseDataUpstreamHistoryQueryFiltersAndBounds(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/demo/upstreams/history?from=2026-09-01T00:00:00Z&to=2026-09-01T01:00:00Z&bucket=10m&region=eu-west-1&deployment_scope=production", nil)
	q, prob := parseDataUpstreamHistoryQuery(r, time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if prob != nil {
		t.Fatalf("unexpected problem: %+v", prob)
	}
	if q.Region != "eu-west-1" || q.DeploymentScope != "production" || q.Bucket != 10*time.Minute {
		t.Fatalf("parsed filters = %+v", q)
	}
}

func TestParseDataUpstreamHistoryQueryRejectsUnsafeWindows(t *testing.T) {
	for name, target := range map[string]string{
		"reversed":     "/v1/apps/demo/upstreams/history?from=2026-09-02T00:00:00Z&to=2026-09-01T00:00:00Z",
		"too wide":     "/v1/apps/demo/upstreams/history?from=2026-08-01T00:00:00Z&to=2026-09-01T00:00:00Z",
		"small bucket": "/v1/apps/demo/upstreams/history?bucket=30s",
		"bad region":   "/v1/apps/demo/upstreams/history?region=EU-West-1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, prob := parseDataUpstreamHistoryQuery(httptest.NewRequest("GET", target, nil), time.Now().UTC()); prob == nil {
				t.Fatal("expected validation problem")
			}
		})
	}
}

func TestValidDataUpstreamHistoryRegion(t *testing.T) {
	if !validDataUpstreamHistoryRegion("us_east-1") {
		t.Fatal("expected valid region")
	}
	for _, region := range []string{"", "EU-west-1", "us east", "a/b"} {
		if validDataUpstreamHistoryRegion(region) {
			t.Errorf("validDataUpstreamHistoryRegion(%q) = true, want false", region)
		}
	}
}
