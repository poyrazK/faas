package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemStoreStatusUptimeBucketsClassifiesTerminalInvocations(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	dayOne := since.Add(2 * time.Hour)
	dayTwo := since.Add(26 * time.Hour)
	success := OutcomeSuccess
	failed := OutcomeFailed

	m.mu.Lock()
	m.invocations["completed"] = Invocation{ID: "completed", CreatedAt: dayOne, State: InvocationCompleted}
	m.invocations["failed"] = Invocation{ID: "failed", CreatedAt: dayOne, State: InvocationFailed}
	m.invocations["cancelled"] = Invocation{ID: "cancelled", CreatedAt: dayOne, State: InvocationCancelled}
	m.invocations["dead-letter"] = Invocation{ID: "dead-letter", CreatedAt: dayTwo, State: InvocationDeadLetter}
	m.invocations["outcome-success"] = Invocation{ID: "outcome-success", CreatedAt: dayTwo, State: InvocationPending, Outcome: &success}
	m.invocations["outcome-failed"] = Invocation{ID: "outcome-failed", CreatedAt: dayTwo, State: InvocationPending, Outcome: &failed}
	m.invocations["pending"] = Invocation{ID: "pending", CreatedAt: dayTwo, State: InvocationPending}
	m.invocations["old"] = Invocation{ID: "old", CreatedAt: since.Add(-time.Minute), State: InvocationCompleted}
	m.mu.Unlock()

	buckets, err := m.StatusUptimeBuckets(ctx, since)
	if err != nil {
		t.Fatalf("StatusUptimeBuckets: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(buckets), buckets)
	}
	if got := buckets[0]; !got.Day.Equal(dayOne.Truncate(24*time.Hour)) || got.Successful != 1 || got.Total != 3 {
		t.Errorf("day one bucket = %+v, want 1/3", got)
	}
	if got := buckets[1]; !got.Day.Equal(dayTwo.Truncate(24*time.Hour)) || got.Successful != 1 || got.Total != 3 {
		t.Errorf("day two bucket = %+v, want 1/3", got)
	}
}

func TestMemStoreListStatusIncidentsSinceFiltersSortsAndCaps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	if _, err := m.InsertStatusIncident(ctx, "unknown", StatusIncidentSeverityDegraded, "bad"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid component error = %v, want ErrNotFound", err)
	}
	if _, err := m.InsertStatusIncident(ctx, StatusIncidentComponentApid, "unknown", "bad"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid severity error = %v, want ErrNotFound", err)
	}
	if _, err := m.InsertStatusIncident(ctx, StatusIncidentComponentApid, StatusIncidentSeverityDegraded, strings.Repeat("x", 1025)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oversized message error = %v, want ErrNotFound", err)
	}

	oldResolved, err := m.InsertStatusIncident(ctx, StatusIncidentComponentApid, StatusIncidentSeverityDegraded, "resolved")
	if err != nil {
		t.Fatalf("InsertStatusIncident resolved: %v", err)
	}
	oldOpen, err := m.InsertStatusIncident(ctx, StatusIncidentComponentSchedd, StatusIncidentSeverityPartialOutage, "still open")
	if err != nil {
		t.Fatalf("InsertStatusIncident open: %v", err)
	}
	recentOne, err := m.InsertStatusIncident(ctx, StatusIncidentComponentVmmd, StatusIncidentSeverityFullOutage, "recent one")
	if err != nil {
		t.Fatalf("InsertStatusIncident recent one: %v", err)
	}
	recentTwo, err := m.InsertStatusIncident(ctx, StatusIncidentComponentGatewayd, StatusIncidentSeverityMaintenance, "recent two")
	if err != nil {
		t.Fatalf("InsertStatusIncident recent two: %v", err)
	}

	resolvedAt := base.Add(3 * time.Hour)
	m.mu.Lock()
	m.statusIncidents[oldResolved.ID-1].PostedAt = base.Add(-24 * time.Hour)
	m.statusIncidents[oldResolved.ID-1].ResolvedAt = &resolvedAt
	m.statusIncidents[oldOpen.ID-1].PostedAt = base.Add(-24 * time.Hour)
	m.statusIncidents[recentOne.ID-1].PostedAt = base.Add(time.Hour)
	m.statusIncidents[recentTwo.ID-1].PostedAt = base.Add(time.Hour)
	m.mu.Unlock()

	got, err := m.ListStatusIncidentsSince(ctx, base, 2)
	if err != nil {
		t.Fatalf("ListStatusIncidentsSince: %v", err)
	}
	if len(got) != 2 || got[0].ID != recentTwo.ID || got[1].ID != recentOne.ID {
		t.Fatalf("limited incidents = %+v, want newest tie-broken IDs %d,%d", got, recentTwo.ID, recentOne.ID)
	}

	all, err := m.ListStatusIncidentsSince(ctx, base, 0)
	if err != nil {
		t.Fatalf("ListStatusIncidentsSince limit=0: %v", err)
	}
	if len(all) != 3 || all[2].ID != oldOpen.ID {
		t.Fatalf("unlimited incidents = %+v, want open historical incident included", all)
	}
	if all, err = m.ListStatusIncidentsSince(ctx, base, 101); err != nil || len(all) != 3 {
		t.Fatalf("ListStatusIncidentsSince limit=101 = %d, %v; want 3, nil", len(all), err)
	}
}
