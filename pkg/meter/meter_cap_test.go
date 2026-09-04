// Issue #279 PR A — overage cap gate inside the meterd quota tick.
//
// The cap is layered on top of the existing free/paid ladder:
// accounts.overage_cap_cents is consulted once per quota tick; an
// account at-or-past the cap skips the overage-row insert path
// inside EnforceQuota and the meterd_billing_cap_exceeded_total
// counter is incremented with the plan label. In-budget usage is
// unchanged; the cap is advisory for overage only.
//
// Calendar-month gotcha: every `now := time.Date(...)` below must
// land inside the CURRENT calendar month (UTC). The cap reads via
// MemStore.CurrentMonthOverageCents which partitions on
// `time.Now().UTC()` (memstore.go:4635), not on the injected clock.
// If a fixture date rolls out of the current month, the loop sees
// 0 overage cents and the cap counter never increments — surfacing
// as a deterministic test failure on the first CI run after a
// month boundary (memory: meterd-testrun-migration-23505-flake.md).
// Bump the dates in lockstep when the calendar crosses.
package meter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestRunQuotaOnce_OverageCapHonored — a paid account at 120% of its
// monthly overage ceiling skips the quota ladder and the counter
// increments. The same account pre-cap is treated as a normal paid
// customer (counter stays zero).
func TestRunQuotaOnce_OverageCapHonored(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	// Cap-bearing Hobby account.
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1000)
	// Pin the store's clock seam to the fixture `now` so
	// CurrentMonthOverageCents's monthStart is derived from the same
	// anchor AppendUsage uses. Without this, real wall-clock has
	// advanced past `now`'s month on the EX44 (test authored when
	// `now` was the current month) and the row is silently filtered
	// as "before monthStart" — cap hit never registers.
	store.SetClockForTest(func() time.Time { return now })

	// Hobby includes 50 GB-hours. Add 1200 billable GB-hours so the
	// derived overage is 1200 cents and exceeds the 1000-cent cap.
	const targetCents = int64(1200)
	mbSeconds := int64(api.PlanHobby.PlanIncludedGBHours()+int(targetCents)) * api.SecondsPerGBHour
	month := meter.AccountMonthKey(now)
	// UsageByMonth is keyed by month; planting a single row in the
	// current month is enough — the cap gate sums usage_minutes, not
	// the per-app rollup.
	row, err := store.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	for _, u := range row {
		mbSeconds -= u.MBSeconds
	}
	if mbSeconds > 0 {
		if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(time.Minute), mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("append usage: %v", err)
		}
	}

	ops := wire.NewOpsMetrics("meter_test_cap")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	loop.RunQuotaOnce(ctx)

	body := scrapeBody(t, ops)
	// The metric prefix is the test registry name from NewOpsMetrics.
	// The cap-hit increments the counter past zero; the {plan="hobby"}
	// line must show ≥ 1.
	hitLine := `meter_test_cap_billing_cap_exceeded_total{plan="hobby"} 1`
	if !strings.Contains(body, hitLine) {
		t.Fatalf("counter did not increment past 0; expected line %q:\n%s", hitLine, body)
	}
	// Sibling pre-init lines for other plans must still be at zero.
	for _, p := range []string{"free", "pro", "scale"} {
		zeroLine := fmt.Sprintf(`meter_test_cap_billing_cap_exceeded_total{plan=%q} 0`, p)
		if !strings.Contains(body, zeroLine) {
			t.Fatalf("sibling pre-init line %q missing:\n%s", zeroLine, body)
		}
	}
}

// TestRunQuotaOnce_OverageCapBelowThreshold — cap is set but the
// account hasn't hit it yet. The quota ladder runs normally; the
// counter is NOT incremented.
func TestRunQuotaOnce_OverageCapBelowThreshold(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, store, api.PlanPro)
	store.SetOverageCapCentsForTest(acct.ID, 10_000)
	store.SetClockForTest(func() time.Time { return now })

	// Pro includes 250 GB-hours. Add 500 billable GB-hours, below the
	// 10_000-cent cap.
	mbSeconds := int64(api.PlanPro.PlanIncludedGBHours()+500) * api.SecondsPerGBHour
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(time.Minute), mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append usage: %v", err)
	}

	ops := wire.NewOpsMetrics("meter_test_cap_below")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	loop.RunQuotaOnce(ctx)

	body := scrapeBody(t, ops)
	// The counter is pre-instantiated for the closed api.Plans set so
	// the line shows up even before the first cap hit. The metric
	// name is prefixed with the test registry so it carries the
	// "meter_test_cap_below_" prefix (Pattern: pkg/wire.NewOpsMetrics).
	zeroLine := `meter_test_cap_below_billing_cap_exceeded_total{plan="pro"} 0`
	if !strings.Contains(body, zeroLine) {
		t.Fatalf("body missing %q (counter pre-init for the closed plan set):\n%s", zeroLine, body)
	}
	// Counter must NOT be incremented for a below-cap account.
	incLine := `meter_test_cap_below_billing_cap_exceeded_total{plan="pro"} 1`
	if strings.Contains(body, incLine) {
		t.Fatalf("counter incremented for a below-cap account:\n%s", body)
	}
}

// TestRunQuotaOnce_OverageCapUnset — accounts without a cap behave
// exactly as before the PR: the quota ladder runs, the counter is
// not emitted for this plan at all (no cap = nothing to skip).
func TestRunQuotaOnce_OverageCapUnset(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	makeAccount(t, ctx, store, api.PlanScale)
	// No SetOverageCapCentsForTest call — NULL column analog.
	store.SetClockForTest(func() time.Time { return now })

	ops := wire.NewOpsMetrics("meter_test_cap_unset")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	// No cap means no skip, no counter increment — the cap counter
	// line should be absent for the scale plan.
	loop.RunQuotaOnce(ctx)

	body := scrapeBody(t, ops)
	if strings.Contains(body, `meter_test_cap_unset_billing_cap_exceeded_total{plan="scale"} 1`) {
		t.Fatalf("counter incremented for a no-cap account:\n%s", body)
	}
	// No-cap account must NOT have bypassed the pre-init: the counter
	// line should still be present at zero (the registry pre-instantiates
	// every closed plan set).
	zeroLine := `meter_test_cap_unset_billing_cap_exceeded_total{plan="scale"} 0`
	if !strings.Contains(body, zeroLine) {
		t.Fatalf("counter pre-init line %q missing:\n%s", zeroLine, body)
	}
}

// TestRunQuotaOnce_OverageCapLoadFailure — fail-open: a transient
// error from LoadAllOverageCapCents must not stall the quota loop.
// The ladder runs as if no caps were configured. We use a hand-rolled
// Store that errors on the cap load to simulate the transient.
func TestRunQuotaOnce_OverageCapLoadFailure(t *testing.T) {
	t.Parallel()
	store := &errCapStore{MemStore: state.NewMemStore()}
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	makeAccount(t, ctx, store.MemStore, api.PlanHobby)
	store.SetClockForTest(func() time.Time { return now })

	ops := wire.NewOpsMetrics("meter_test_cap_fail")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	// Must not panic / return error — the loop's caller is the
	// ticker, and the cap-load failure is logged + swallowed.
	loop.RunQuotaOnce(ctx)

	body := scrapeBody(t, ops)
	if strings.Contains(body, `meter_test_cap_fail_billing_cap_exceeded_total{plan="hobby"} 1`) {
		t.Fatalf("counter incremented on a cap-load failure; expected no increment:\n%s", body)
	}
}

// TestRunQuotaOnce_OverageCapAtCap — pins the boundary semantics:
// monthCents == capCents IS treated as a hit (the implementation
// uses `>=`). An off-by-one fix would silently flip the behaviour
// here, so the test pins the equality.
func TestRunQuotaOnce_OverageCapAtCap(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, store, api.PlanScale)
	store.SetOverageCapCentsForTest(acct.ID, 500) // cents
	store.SetClockForTest(func() time.Time { return now })

	// Scale includes 1500 GB-hours. Add exactly 500 billable GB-hours.
	mbSeconds := int64(api.PlanScale.PlanIncludedGBHours()+500) * api.SecondsPerGBHour
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(time.Minute), mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append usage: %v", err)
	}

	ops := wire.NewOpsMetrics("meter_test_cap_boundary")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	loop.RunQuotaOnce(ctx)

	body := scrapeBody(t, ops)
	// Equality IS a cap hit: counter goes 0 → 1.
	hitLine := `meter_test_cap_boundary_billing_cap_exceeded_total{plan="scale"} 1`
	if !strings.Contains(body, hitLine) {
		t.Fatalf("boundary (monthCents == capCents) should count as a cap hit; expected line %q:\n%s", hitLine, body)
	}
}

// errCapStore wraps MemStore so LoadAllOverageCapCents returns an
// error. Used by TestRunQuotaOnce_OverageCapLoadFailure. Other
// methods fall through to the embedded MemStore so the quota ladder
// has accounts to walk.
type errCapStore struct {
	*state.MemStore
}

func (errCapStore) LoadAllOverageCapCents(ctx context.Context) (map[string]int64, error) {
	return nil, errTransient
}

// errTransient is the synthetic error used by the cap-load failure
// test. Implements error.
var errTransient = errTransientError("synthetic: cap load failed")

type errTransientError string

func (e errTransientError) Error() string { return string(e) }

// TestOverageCap_MeterdAdvisory (issue #561) — pins the contract
// that issue #561 closes only the workload-gate side of the cap
// semantics, NOT the meterd quota tick. The scheduler refuses new
// wakes via CodeAdmissionRefused (HTTP 402) when the cap is
// reached; meterd independently stops pushing overage rows once
// the cap is reached. The two paths trigger on different
// conditions (the wake gate consults the cap at wake time, the
// quota tick consults it at the per-minute tick) and the audit
// rows they emit are distinct. A regression that pivots one
// path to the other would surface here as a counter-mismatch.
//
// The fixture mirrors TestRunQuotaOnce_OverageCapHonored: a
// paid account at 120% of its monthly overage ceiling. The
// meterd tick is expected to skip the overage-row insert AND
// increment the meterd_billing_cap_exceeded_total counter with
// the plan label. The scheduler's wake gate is NOT exercised
// here (a separate test in pkg/sched/overage_test.go covers it).
func TestOverageCap_MeterdAdvisory(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	acct := makeAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1000)
	// Pin the store's clock seam to the fixture `now` so
	// CurrentMonthOverageCents's monthStart is derived from the same
	// anchor AppendUsage uses.
	store.SetClockForTest(func() time.Time { return now })

	// Hobby includes 50 GB-hours. Add 1200 billable GB-hours. The cap is
	// 1000 cents so the cap-hit branch fires.
	const targetCents = int64(1200)
	mbSeconds := int64(api.PlanHobby.PlanIncludedGBHours()+int(targetCents)) * api.SecondsPerGBHour
	month := meter.AccountMonthKey(now)
	row, err := store.UsageByMonth(ctx, acct.ID, month)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	for _, u := range row {
		mbSeconds -= u.MBSeconds
	}
	if mbSeconds > 0 {
		if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(time.Minute), mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("append usage: %v", err)
		}
	}

	ops := wire.NewOpsMetrics("meter_test_advisory")
	loop := meter.NewLoop(
		store,
		nil, /* cpu — cpu-hour metering not exercised here */
		&fakeParker{},
		nil, /* pusher (Provider) — cap test doesn't push usage */
		&fakeNotifier{},
		nil, /* mailer */
		nil, /* dunning */
		nil, /* residency — cpu-hour metering not exercised here */
		nil, /* evaluator — cap test doesn't exercise alerts */
		func() time.Time { return now },
		discardLog(),
		func() *meter.Config {
			cfg := &meter.Config{}
			cfg.Defaults()
			return cfg
		}(),
		ops,
	)

	loop.RunQuotaOnce(ctx)

	// The metric prefix is the test registry name from NewOpsMetrics.
	// The cap-hit increments the counter past zero; the {plan="hobby"}
	// line must show ≥ 1.
	body := scrapeBody(t, ops)
	hitLine := `meter_test_advisory_billing_cap_exceeded_total{plan="hobby"} 1`
	if !strings.Contains(body, hitLine) {
		t.Fatalf("counter did not increment past 0; expected line %q:\n%s", hitLine, body)
	}
	// The contract this test pins: the cap-exceeded counter is
	// the existing advisory signal (issue #279), unchanged by
	// #561. A regression that pivots the counter to "wake
	// rejections" or removes the metric would surface here.
	anyLine := "meter_test_advisory_billing_cap_exceeded_total{"
	if !strings.Contains(body, anyLine) {
		t.Errorf("cap-exceeded counter series missing entirely\nbody:\n%s", body)
	}
}
