// adr: 1027 — the native driver holds databases and keeps their compute
// quotas through schedd (amendment 2026-11-10).
package firecracker

import (
	"context"
	"errors"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestNativeComputeCapAndHold(t *testing.T) {
	ctx := context.Background()
	p, sched, _ := newTestProvider(t)
	const id = "7d4c2f2a-3b1e-4f55-9a0e-2c1d5e6f7a8b"
	sched.vms[id] = &scheddpb.DatabaseRuntime{DatabaseId: id, State: "parked", Bootstrapped: true}
	sched.usage = &scheddpb.DatabaseUsageResponse{ComputeUnitSeconds: 4200}

	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.FixedZone("TRT", 3*3600))
	observed, err := p.observeComputeCap(ctx, id, now)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if observed.Scope != id || !observed.PeriodStart.Equal(month) || observed.UsedUnitSeconds != 4200 || observed.QuotaUnitSeconds != 0 {
		t.Fatalf("observed = %+v", observed)
	}
	if from := sched.usageReq.GetFrom().AsTime(); !from.Equal(month) {
		t.Fatalf("usage read from %s, want the UTC month start %s", from, month)
	}
	// The month's first instant still asks for a non-empty window.
	if _, err := p.observeComputeCap(ctx, id, month); err != nil || !sched.usageReq.GetTo().AsTime().After(month) {
		t.Fatalf("observe at the month start = %v, to %s", err, sched.usageReq.GetTo().AsTime())
	}

	if err := p.SetComputeCap(ctx, id, 7200); err != nil {
		t.Fatal(err)
	}
	if observed, _ := p.ObserveComputeCap(ctx, id); observed.QuotaUnitSeconds != 7200 {
		t.Fatalf("quota after SetComputeCap = %d", observed.QuotaUnitSeconds)
	}
	if err := p.SetComputeCap(ctx, id, 0); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("zero quota = %v, want ErrInvalid", err)
	}

	for _, held := range []bool{true, false} {
		set := p.ReleaseDeletionHold
		if held {
			set = p.HoldForDeletion
		}
		if err := set(ctx, id); err != nil {
			t.Fatal(err)
		}
		if got, err := p.ComputeHeld(ctx, id); err != nil || got != held {
			t.Fatalf("ComputeHeld = %v, %v; want %v", got, err, held)
		}
	}
	if err := p.HoldForDeletion(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("hold of a missing database = %v, want ErrNotFound", err)
	}
}
