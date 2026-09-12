//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStoreStatusHistory pins the Postgres projection described by ADR-130:
// terminal invocation buckets and the bounded incident timeline share the
// same UTC/recent-plus-open semantics as the MemStore implementation.
func TestPgStoreStatusHistory(t *testing.T) {
	s, ctx := pgStore(t)
	account, err := s.CreateAccount(ctx, "status-history-pg@example.test", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "status-history-pg",
		Runtime:   "node22",
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	since := time.Now().UTC().Add(-time.Minute)
	for _, status := range []state.InvocationState{state.InvocationCompleted, state.InvocationFailed} {
		if _, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
			State: status, Method: "POST", Path: "/", DueAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("EnqueueInvocation(%s): %v", status, err)
		}
	}
	buckets, err := s.StatusUptimeBuckets(ctx, since)
	if err != nil {
		t.Fatalf("StatusUptimeBuckets: %v", err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var foundToday bool
	for _, bucket := range buckets {
		if bucket.Day.Equal(today) {
			foundToday = true
			if bucket.Successful != 1 || bucket.Total != 2 {
				t.Fatalf("today bucket = %+v, want 1/2", bucket)
			}
		}
	}
	if !foundToday {
		t.Fatalf("StatusUptimeBuckets = %+v, missing today", buckets)
	}

	incident, err := s.InsertStatusIncident(ctx, state.StatusIncidentComponentApid,
		state.StatusIncidentSeverityDegraded, "database latency")
	if err != nil {
		t.Fatalf("InsertStatusIncident: %v", err)
	}
	open, err := s.ListOpenStatusIncidents(ctx)
	if err != nil || len(open) != 1 || open[0].ID != incident.ID {
		t.Fatalf("ListOpenStatusIncidents = %+v, %v", open, err)
	}
	recent, err := s.ListStatusIncidentsSince(ctx, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil || len(recent) != 1 || recent[0].ID != incident.ID {
		t.Fatalf("ListStatusIncidentsSince = %+v, %v", recent, err)
	}
	if err := s.ResolveStatusIncident(ctx, incident.ID); err != nil {
		t.Fatalf("ResolveStatusIncident: %v", err)
	}
	if err := s.ResolveStatusIncident(ctx, incident.ID); err != nil {
		t.Fatalf("idempotent ResolveStatusIncident: %v", err)
	}
	if err := s.ResolveStatusIncident(ctx, incident.ID+999); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing ResolveStatusIncident = %v, want ErrNotFound", err)
	}
	open, err = s.ListOpenStatusIncidents(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("open incidents after resolve = %+v, %v", open, err)
	}
}
