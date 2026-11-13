// adr: 20261110160000 — slice 3, amendment 2026-11-11: each database's egress
// past its own allowance, as Neon's is per project, is billed at Neon's price
// plus 5% while the subject's egress overage election lifts the allowance.
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
		name   string
		egress map[string]int64
		allow  int64
		billed int64
	}{
		{name: "each within its own allowance", egress: map[string]int64{"a": 9 * testGB, "b": 9 * testGB}, allow: 10 * testGB},
		{name: "one database past its allowance", egress: map[string]int64{"a": 13 * testGB, "b": 9 * testGB}, allow: 10 * testGB, billed: 3 * testGB},
		{name: "every database pays its own", egress: map[string]int64{"a": 13 * testGB, "b": 15 * testGB}, allow: 10 * testGB, billed: 8 * testGB},
		{name: "at most one more allowance each", egress: map[string]int64{"a": 25 * testGB, "b": 21 * testGB}, allow: 10 * testGB, billed: 20 * testGB},
		{name: "no database egress", allow: 10 * testGB},
		{name: "no allowance", egress: map[string]int64{"a": 30 * testGB}},
		{name: "negative egress", egress: map[string]int64{"a": -5}, allow: 10 * testGB},
		{name: "overflow", egress: map[string]int64{"a": math.MaxInt64}, allow: math.MaxInt64 / 2, billed: math.MaxInt64 / 2},
	} {
		billed, millicents := EgressOverageCharge(tc.egress, tc.allow)
		if billed != tc.billed || millicents != egressOverageMillicents(tc.billed) {
			t.Errorf("%s: EgressOverageCharge = %d bytes, %d millicents; want %d bytes", tc.name, billed, millicents, tc.billed)
		}
	}
	if _, millicents := EgressOverageCharge(map[string]int64{"a": 3_000_000}, 1_000_000); millicents != 10 {
		t.Fatalf("a fraction of a millicent = %d, want floored to 10", millicents)
	}
	many := map[string]int64{}
	for _, id := range []string{"a", "b", "c"} {
		many[id] = math.MaxInt64
	}
	if billed, _ := EgressOverageCharge(many, math.MaxInt64/2); billed != math.MaxInt64 {
		t.Fatalf("summed overflow = %d, want capped at MaxInt64", billed)
	}
}

func TestOverageBillerChargesElectedDatabaseEgressOnly(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	price := int64(api.ManagedPostgresEgressOverageMillicentsPerGB)
	f := newOverageFixture(t, november.Add(10*24*time.Hour))
	f.limits.Overage = false
	f.store.SetEgressOverageElected("acct", true)
	f.egress = EgressAllowance{CeilingBytes: 10 * testGB, OverageLifted: true}
	// Compute past the allowance with managed PostgreSQL overage off is not
	// charged because egress overage is elected.
	f.store.used["acct2026-11"] = testAllowance + 3600
	f.store.egress["acct2026-11"] = 13 * testGB
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 1 || len(f.published) != 1 {
		t.Fatalf("egress step = %+v, %v, published %+v", summary, err, f.published)
	}
	if step := f.published[0]; step.BilledUnitSeconds != 0 || step.BilledEgressBytes != 3*testGB || step.UsedEgressBytes != 13*testGB ||
		step.AppEgressBytes != 0 || step.EgressAllowanceBytes != 10*testGB || step.TargetMillicents != 3*price {
		t.Fatalf("egress step %+v", step)
	}
	if delivery, _ := f.store.OverageDelivered(ctx, "polar", "acct", november); delivery.BilledEgressBytes != 3*testGB {
		t.Fatalf("delivery %+v", delivery)
	}

	// The election ends: the month keeps billing database egress past the
	// allowance, as storage overage does, and never charges a step twice.
	f.egress.OverageLifted = false
	f.store.egress["acct2026-11"] = 17 * testGB
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
	f.egress = EgressAllowance{CeilingBytes: 10 * testGB}
	f.store.egress["acct2026-11"] = 13 * testGB
	if summary, err := f.biller(t, true, november).Deliver(ctx); err != nil || summary.Delivered != 0 || len(f.published) != 0 {
		t.Fatalf("egress charged without a lifted election: %+v, %v, %+v", summary, err, f.published)
	}
}
