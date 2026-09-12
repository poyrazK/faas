package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestStatusUptimeBucketsSemantics pins the terminal/success projection from
// ADR-130: only terminal invocations count, and buckets are UTC calendar days.
func TestStatusUptimeBucketsSemantics(t *testing.T) {
	since := time.Date(2026, 9, 10, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	dayBefore := since.UTC().Add(-time.Hour)
	dayOne := since.UTC().Add(2 * time.Hour)
	dayTwo := since.UTC().AddDate(0, 0, 1).Add(3 * time.Hour)
	outcomeSuccess := OutcomeSuccess
	outcomeFailed := OutcomeFailed
	outcomeTimeout := OutcomeTimeout

	invocations := map[string]Invocation{
		"before":          {CreatedAt: dayBefore, State: InvocationCompleted},
		"pending":         {CreatedAt: dayOne, State: InvocationPending},
		"dispatching":     {CreatedAt: dayOne, State: InvocationDispatching},
		"completed":       {CreatedAt: dayOne, State: InvocationCompleted},
		"failed":          {CreatedAt: dayOne, State: InvocationFailed},
		"cancelled":       {CreatedAt: dayOne, State: InvocationCancelled},
		"dead-letter":     {CreatedAt: dayOne, State: InvocationDeadLetter},
		"outcome-success": {CreatedAt: dayTwo, State: InvocationFailed, Outcome: &outcomeSuccess},
		"outcome-failed":  {CreatedAt: dayTwo, State: InvocationCompleted, Outcome: &outcomeFailed},
		"outcome-timeout": {CreatedAt: dayTwo, State: InvocationPending, Outcome: &outcomeTimeout},
	}

	buckets := statusUptimeBuckets(invocations, since)
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v, want two UTC days", buckets)
	}
	if !buckets[0].Day.Before(buckets[1].Day) {
		t.Fatalf("buckets not sorted: %+v", buckets)
	}
	if buckets[0].Successful != 1 || buckets[0].Total != 4 {
		t.Fatalf("first bucket = %+v, want 1/4", buckets[0])
	}
	if buckets[1].Successful != 1 || buckets[1].Total != 3 {
		t.Fatalf("second bucket = %+v, want 1/3", buckets[1])
	}

	// Exercise the MemStore lock/capability wrapper and its since.UTC path.
	m := NewMemStore()
	m.invocations = invocations
	wrapped, err := m.StatusUptimeBuckets(context.Background(), since)
	if err != nil {
		t.Fatalf("MemStore.StatusUptimeBuckets: %v", err)
	}
	if len(wrapped) != len(buckets) || !wrapped[0].Day.Equal(buckets[0].Day) {
		t.Fatalf("wrapped buckets = %+v, want %+v", wrapped, buckets)
	}
}

// TestMemStoreStatusIncidents pins the append/resolve/list contracts from
// ADR-130, including the bounded recent-plus-open incident timeline.
func TestMemStoreStatusIncidents(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	inc, err := m.InsertStatusIncident(ctx, StatusIncidentComponentApid,
		StatusIncidentSeverityDegraded, "latency elevated")
	if err != nil || inc.ID == 0 || inc.Message != "latency elevated" || inc.ResolvedAt != nil {
		t.Fatalf("InsertStatusIncident = %+v, %v", inc, err)
	}
	for _, tc := range []struct {
		component, severity, message string
	}{
		{"unknown", StatusIncidentSeverityDegraded, "bad component"},
		{StatusIncidentComponentApid, "unknown", "bad severity"},
		{StatusIncidentComponentApid, StatusIncidentSeverityDegraded, strings.Repeat("x", 1025)},
	} {
		if _, err := m.InsertStatusIncident(ctx, tc.component, tc.severity, tc.message); !errors.Is(err, ErrNotFound) {
			t.Errorf("invalid incident %+v returned %v, want ErrNotFound", tc, err)
		}
	}

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	resolvedAt := now.Add(time.Hour)
	m.statusIncidents = []StatusIncident{
		{ID: 1, Component: StatusIncidentComponentApid, PostedAt: now.Add(-48 * time.Hour)},
		{ID: 2, Component: StatusIncidentComponentSchedd, PostedAt: now.Add(-24 * time.Hour), ResolvedAt: &resolvedAt},
		{ID: 3, Component: StatusIncidentComponentVmmd, PostedAt: now.Add(-24 * time.Hour)},
		{ID: 4, Component: StatusIncidentComponentGatewayd, PostedAt: now.Add(-24 * time.Hour), ResolvedAt: &resolvedAt},
		{ID: 5, Component: StatusIncidentComponentMeterd, PostedAt: now.Add(-time.Hour)},
	}
	open, err := m.ListOpenStatusIncidents(ctx)
	if err != nil || len(open) != 3 || open[0].ID != 5 || open[1].ID != 3 || open[2].ID != 1 {
		t.Fatalf("open incidents = %+v, %v", open, err)
	}

	if err := m.ResolveStatusIncident(ctx, 5); err != nil {
		t.Fatalf("ResolveStatusIncident: %v", err)
	}
	if err := m.ResolveStatusIncident(ctx, 5); err != nil {
		t.Fatalf("idempotent ResolveStatusIncident: %v", err)
	}
	if err := m.ResolveStatusIncident(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ResolveStatusIncident = %v, want ErrNotFound", err)
	}

	recent, err := m.ListStatusIncidentsSince(ctx, now.Add(-24*time.Hour), 2)
	if err != nil || len(recent) != 2 || recent[0].ID != 5 || recent[1].ID != 4 {
		t.Fatalf("recent incidents = %+v, %v", recent, err)
	}
	// An open incident older than the window remains visible; a resolved one
	// older than the window does not.
	if err := m.ResolveStatusIncident(ctx, 3); err != nil {
		t.Fatalf("resolve recent incident: %v", err)
	}
	all, err := m.ListStatusIncidentsSince(ctx, now, 0)
	if err != nil || len(all) != 1 || all[0].ID != 1 {
		t.Fatalf("open-old incident projection = %+v, %v", all, err)
	}

	// Exercise the defensive upper cap and truncation branch.
	m.statusIncidents = make([]StatusIncident, 101)
	for i := range m.statusIncidents {
		m.statusIncidents[i] = StatusIncident{ID: int64(i + 1), PostedAt: now.Add(time.Duration(i) * time.Second)}
	}
	if got, err := m.ListStatusIncidentsSince(ctx, now.Add(-time.Hour), 101); err != nil || len(got) != 100 {
		t.Fatalf("upper-capped incident list = %d, %v", len(got), err)
	}
}
