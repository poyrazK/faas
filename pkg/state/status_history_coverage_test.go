package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/publicstatus"
)

func TestStatusUptimeBucketsAndIncidentsCoverage(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	base := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	since := base.Add(-24 * time.Hour)
	recordCompleteStatusInterval(t, m, base, "", publicstatus.StateOperational)
	recordCompleteStatusInterval(t, m, base.Add(5*time.Minute), publicstatus.ComponentAppExecution, publicstatus.StateMajorOutage)
	buckets, err := m.StatusUptimeBuckets(ctx, since)
	if err != nil {
		t.Fatalf("StatusUptimeBuckets: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Total != 2 || buckets[0].Successful != 1 {
		t.Fatalf("buckets = %+v, want one day with two platform observations and one available", buckets)
	}

	resolved := base.Add(-2 * time.Hour)
	m.statusIncidents = []StatusIncident{
		{ID: 1, PostedAt: base.Add(-48 * time.Hour), ResolvedAt: &resolved},
		{ID: 2, PostedAt: base.Add(-48 * time.Hour)},
		{ID: 3, PostedAt: base.Add(-time.Hour), ResolvedAt: &resolved},
		{ID: 4, PostedAt: base.Add(-time.Hour)},
		{ID: 5, PostedAt: base.Add(-time.Hour)},
	}
	incidents, err := m.ListStatusIncidentsSince(ctx, base.Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatalf("ListStatusIncidentsSince: %v", err)
	}
	if len(incidents) != 2 || incidents[0].ID != 5 || incidents[1].ID != 4 {
		t.Fatalf("incidents = %+v, want newest same-time IDs first", incidents)
	}
	// Non-positive and over-large limits both normalize to the safe maximum.
	for _, limit := range []int{0, 101} {
		if got, err := m.ListStatusIncidentsSince(ctx, since, limit); err != nil || len(got) != 4 {
			t.Fatalf("limit %d: got %d incidents, err=%v; want 4", limit, len(got), err)
		}
	}
}
