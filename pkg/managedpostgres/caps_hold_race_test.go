// adr: 1027 — cap writes share the database lifecycle lease.
package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type deletingCapProvider struct {
	*holdingProvider
	beforeRead func()
}

func (p *deletingCapProvider) ComputeHeld(_ context.Context, id string) (bool, error) {
	if p.beforeRead != nil {
		f := p.beforeRead
		p.beforeRead = nil
		f()
	}
	return p.held[id], nil
}

func capForGraceFixture(f graceFixture) *CapEnforcer {
	return &CapEnforcer{registry: f.service.registry, store: f.store, mode: CapEnforcementOn,
		logger: slog.Default(), now: func() time.Time { return *f.clock }}
}

func TestComputeHoldStaleSweepPreservesDeletionHold(t *testing.T) {
	provider := &deletingCapProvider{holdingProvider: newHoldingProvider()}
	f := newGraceFixture(t, provider)
	ready := f.readyDatabase(t, "stale-cap")
	if _, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID); err != nil {
		t.Fatal(err)
	}
	summary := &CapSweepSummary{}
	if err := capForGraceFixture(f).holdDatabases(t.Context(), ready.AccountID, []Database{ready}, capHoldNone, nil, summary); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Get(t.Context(), ready.AccountID, ready.ID)
	if err != nil || current.State != StateDeleting || !provider.held[databaseDataResource(ready)] || summary.HoldsReleased != 0 {
		t.Fatalf("stale sweep removed deletion hold: state=%s held=%v summary=%+v err=%v", current.State, provider.held, summary, err)
	}
}

func TestComputeHoldLeaseFencesConcurrentDeletion(t *testing.T) {
	provider := &deletingCapProvider{holdingProvider: newHoldingProvider()}
	f := newGraceFixture(t, provider)
	ready := f.readyDatabase(t, "cap-first")
	provider.held[databaseDataResource(ready)] = true // an old compute hold
	blocked := false
	provider.beforeRead = func() {
		_, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID)
		blocked = errors.Is(err, ErrConflict)
	}
	if err := capForGraceFixture(f).holdDatabases(t.Context(), ready.AccountID, []Database{ready}, capHoldNone, nil, &CapSweepSummary{}); err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("deletion acquired the database during a cap provider operation")
	}
	if _, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID); err != nil {
		t.Fatal("deletion after successful lease release", err)
	}
	if !provider.held[databaseDataResource(ready)] {
		t.Fatal("cap release outlived the subsequent deletion hold")
	}
}

func TestComputeHoldUncertainProviderOutcomeKeepsLease(t *testing.T) {
	provider := &deletingCapProvider{holdingProvider: newHoldingProvider()}
	f := newGraceFixture(t, provider)
	ready := f.readyDatabase(t, "cap-uncertain")
	provider.held[databaseDataResource(ready)] = true
	provider.releaseErr = ErrUnavailable
	if err := capForGraceFixture(f).holdDatabases(t.Context(), ready.AccountID, []Database{ready}, capHoldNone, nil, &CapSweepSummary{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("provider error was lost", err)
	}
	if _, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertain cap operation released its fence", err)
	}
	*f.clock = f.clock.Add(defaultLeaseDuration + time.Second)
	if _, err := f.service.ScheduleDelete(t.Context(), ready.AccountID, ready.ID); err != nil {
		t.Fatal("expired cap lease did not allow recovery", err)
	}
}
