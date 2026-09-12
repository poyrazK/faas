package state

import (
	"context"
	"testing"
	"time"
)

func TestMemStoreStatusHistoryRollupAndIncidentWindow(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	dayOne := since.Add(2 * time.Hour)
	dayTwo := since.AddDate(0, 0, 1).Add(3 * time.Hour)
	success := OutcomeSuccess
	failed := OutcomeFailed

	store.invocations = map[string]Invocation{
		"before-window": {CreatedAt: since.Add(-time.Minute), State: InvocationCompleted},
		"pending":       {CreatedAt: dayOne, State: InvocationPending},
		"dispatching":   {CreatedAt: dayOne, State: InvocationDispatching},
		"completed":     {CreatedAt: dayOne, State: InvocationCompleted},
		"failed":        {CreatedAt: dayOne, State: InvocationFailed},
		"cancelled":     {CreatedAt: dayTwo, State: InvocationCancelled},
		"dead-letter":   {CreatedAt: dayTwo, State: InvocationDeadLetter},
		"outcome-success": {
			CreatedAt: dayTwo,
			State:     InvocationFailed,
			Outcome:   &success,
		},
		"outcome-failed": {
			CreatedAt: dayTwo,
			State:     InvocationCompleted,
			Outcome:   &failed,
		},
	}

	buckets, err := store.StatusUptimeBuckets(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v, want two UTC days", buckets)
	}
	if !buckets[0].Day.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) ||
		buckets[0].Successful != 1 || buckets[0].Total != 2 {
		t.Fatalf("first bucket = %+v, want 1/2", buckets[0])
	}
	if !buckets[1].Day.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) ||
		buckets[1].Successful != 1 || buckets[1].Total != 4 {
		t.Fatalf("second bucket = %+v, want 1/4", buckets[1])
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
