// adr: 20261110160000 — slice 3: database egress past the shared egress
// allowance, apps first, is billed at Neon's price plus 5% while the subject's
// egress overage election lifts the allowance.
package managedpostgres

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testGB = int64(1_000_000_000)

func TestEgressOverageCharge(t *testing.T) {
	price := int64(api.ManagedPostgresEgressOverageMillicentsPerGB)
	if price != 10_500 {
		t.Fatalf("egress overage price = %d millicents/GB, want Neon's 10000 plus 5%%", price)
	}
	for _, tc := range []struct {
		name                 string
		database, app, allow int64
		billed               int64
	}{
		{name: "within the allowance", database: 4 * testGB, app: 6 * testGB, allow: 10 * testGB},
		{name: "databases push the total past it", database: 6 * testGB, app: 7 * testGB, allow: 10 * testGB, billed: 3 * testGB},
		{name: "apps already past it: databases pay only their own", database: 5 * testGB, app: 12 * testGB, allow: 10 * testGB, billed: 5 * testGB},
		{name: "at most one more allowance", database: 25 * testGB, allow: 10 * testGB, billed: 10 * testGB},
		{name: "no database egress", app: 30 * testGB, allow: 10 * testGB},
		{name: "no allowance", database: 30 * testGB},
		{name: "overflow", database: math.MaxInt64, app: math.MaxInt64, allow: 10 * testGB, billed: 10 * testGB},
	} {
		billed, millicents := EgressOverageCharge(tc.database, tc.app, tc.allow)
		if billed != tc.billed || millicents != tc.billed/testGB*price {
			t.Errorf("%s: EgressOverageCharge = %d bytes, %d millicents; want %d bytes", tc.name, billed, millicents, tc.billed)
		}
	}
	if _, millicents := EgressOverageCharge(3_000_000, 0, 2_000_000); millicents != 10 {
		t.Fatalf("a fraction of a millicent = %d, want floored to 10", millicents)
	}
}

func TestOverageBillerChargesElectedDatabaseEgressOnly(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	price := int64(api.ManagedPostgresEgressOverageMillicentsPerGB)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.limits.Overage = false
	f.store.SetEgressOverageElected("acct", true)
	f.egress = EgressAllowance{CeilingBytes: 10 * testGB, AppEgressBytes: 7 * testGB, OverageLifted: true}
	// Compute past the allowance with managed PostgreSQL overage off is not
	// charged because egress overage is elected.
	f.store.used["acct2026-11"] = testAllowance + 3600
	f.store.egress["acct2026-11"] = 6 * testGB
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 1 || len(f.published) != 1 {
		t.Fatalf("egress step = %+v, %v, published %+v", summary, err, f.published)
	}
	if step := f.published[0]; step.BilledUnitSeconds != 0 || step.BilledEgressBytes != 3*testGB || step.UsedEgressBytes != 6*testGB ||
		step.AppEgressBytes != 7*testGB || step.EgressAllowanceBytes != 10*testGB || step.TargetMillicents != 3*price {
		t.Fatalf("egress step %+v", step)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.BilledEgressBytes != 3*testGB {
		t.Fatalf("delivery %+v", delivery)
	}

	// The election ends: the month keeps billing database egress past the
	// allowance, as storage overage does, and never charges a step twice.
	f.egress.OverageLifted = false
	f.store.egress["acct2026-11"] = 10 * testGB
	if _, err := f.biller(t, true, november).Deliver(ctx); err != nil || len(f.published) != 2 ||
		f.published[1].DeliveredMillicents != 3*price || f.published[1].TargetMillicents != 7*price {
		t.Fatalf("later egress %+v, %v", f.published, err)
	}
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 0 || len(f.published) != 2 {
		t.Fatalf("unchanged egress charged again: %+v, %v", summary, err)
	}
}

func TestOverageBillerSkipsEgressWithoutALiftedElection(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.limits.Overage = false
	f.store.SetEgressOverageElected("acct", true)
	// Elected, but the provider does not bill egress live yet.
	f.egress = EgressAllowance{CeilingBytes: 10 * testGB, AppEgressBytes: 7 * testGB}
	f.store.egress["acct2026-11"] = 6 * testGB
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 0 || len(f.published) != 0 {
		t.Fatalf("egress charged without a lifted election: %+v, %v, %+v", summary, err, f.published)
	}
}
