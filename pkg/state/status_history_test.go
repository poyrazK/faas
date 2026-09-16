package state

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/publicstatus"
)

func TestMemStoreStatusHistoryRollupAndIncidentWindow(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	dayOne := since.Add(2 * time.Hour)
	dayTwo := since.AddDate(0, 0, 1).Add(3 * time.Hour)
	recordCompleteStatusInterval(t, store, dayOne, "", publicstatus.StateOperational)
	recordCompleteStatusInterval(t, store, dayOne.Add(5*time.Minute), publicstatus.ComponentNetworking, publicstatus.StatePartialOutage)
	recordCompleteStatusInterval(t, store, dayTwo, publicstatus.ComponentDeployments, publicstatus.StateMaintenance)
	// An incomplete observation is coverage loss, not downtime.
	if err := store.RecordStatusBucket(ctx, StatusBucket{Component: publicstatus.ComponentAPIConsole, BucketAt: dayTwo.Add(5 * time.Minute), State: publicstatus.StateMajorOutage, HasTelemetry: true}); err != nil {
		t.Fatal(err)
	}
	// Customer outcomes remain in their own ledger and cannot change uptime.
	store.invocations = map[string]Invocation{"customer-failure": {CreatedAt: dayTwo, State: InvocationFailed}}

	buckets, err := store.StatusUptimeBuckets(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v, want two UTC days", buckets)
	}
	if !buckets[0].Day.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) ||
		buckets[0].Successful != 1 || buckets[0].Total != 2 {
		t.Fatalf("first bucket = %+v, want one available of two platform intervals", buckets[0])
	}
	if !buckets[1].Day.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) ||
		buckets[1].Successful != 1 || buckets[1].Total != 1 {
		t.Fatalf("second bucket = %+v, want declared maintenance available and partial interval excluded", buckets[1])
	}

	resolvedAt := since.Add(time.Hour)
	store.statusIncidents = []StatusIncident{
		{ID: 1, Message: "old resolved", PostedAt: since.Add(-time.Hour), ResolvedAt: &resolvedAt},
		{ID: 2, Message: "old open", PostedAt: since.Add(-time.Hour)},
		{ID: 3, Message: "recent resolved", PostedAt: since.Add(2 * time.Hour), ResolvedAt: &resolvedAt},
		{ID: 4, Message: "recent open", PostedAt: since.Add(3 * time.Hour)},
	}

	incidents, err := store.ListStatusIncidentsSince(ctx, since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) != 3 || incidents[0].ID != 4 || incidents[1].ID != 3 || incidents[2].ID != 2 {
		t.Fatalf("incidents = %+v, want recent plus open, newest first", incidents)
	}
	limited, err := store.ListStatusIncidentsSince(ctx, since, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].ID != 4 {
		t.Fatalf("limited incidents = %+v, want newest incident", limited)
	}
}

type statusBucketRecorder interface {
	RecordStatusBucket(context.Context, StatusBucket) error
}

func recordCompleteStatusInterval(t *testing.T, store statusBucketRecorder, at time.Time, changed publicstatus.Component, changedState publicstatus.State) {
	t.Helper()
	for _, component := range publicstatus.AllComponents() {
		stateValue := publicstatus.StateOperational
		if component == changed {
			stateValue = changedState
		}
		if err := store.RecordStatusBucket(context.Background(), StatusBucket{Component: component, BucketAt: at, State: stateValue, HasTelemetry: true}); err != nil {
			t.Fatalf("RecordStatusBucket(%s): %v", component, err)
		}
	}
}
