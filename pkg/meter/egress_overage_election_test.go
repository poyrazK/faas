package meter_test

// adr: 20261104204500

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// electEgressOverage turns on subjectID's egress overage election from the
// start of 2026, before any fixture's usage, so live egress above the
// allowance is billed (ADR-20261104204500).
func electEgressOverage(t *testing.T, ctx context.Context, s *state.MemStore, subjectID string) {
	t.Helper()
	if _, err := s.SetEgressOverage(ctx, subjectID, true, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("elect egress overage: %v", err)
	}
}

// The egress ceiling lifts only for a plan that allows the election, while
// the subject's election is on and egress bills live.
func TestEgressCeilingLiftsOnlyForElectedLiveEgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	enabled, disabled := now.Add(-48*time.Hour), now.Add(-time.Hour)
	on := state.EgressOverageElection{EnabledAt: &enabled}
	off := state.EgressOverageElection{EnabledAt: &enabled, DisabledAt: &disabled}
	live := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	shadow := &recordingEgressProvider{mode: billing.MeterDeliveryShadow}
	future := &recordingEgressProvider{mode: billing.MeterDeliveryLive, effectiveFrom: now.Add(time.Hour)}
	for _, tc := range []struct {
		name     string
		provider billing.Provider
		plan     api.Plan
		election state.EgressOverageElection
		want     int64
	}{
		{"never elected", live, api.PlanPro, state.EgressOverageElection{}, 500 << 30},
		{"elected, live", live, api.PlanPro, on, 0},
		{"election turned off", live, api.PlanPro, off, 500 << 30},
		{"elected, shadow", shadow, api.PlanPro, on, 500 << 30},
		{"elected, live from later", future, api.PlanPro, on, 500 << 30},
		{"elected, no provider", nil, api.PlanPro, on, 500 << 30},
		{"free cannot elect", live, api.PlanFree, on, 10 << 30},
	} {
		if got := meter.EgressCeilingBytes(tc.provider, tc.plan, tc.election, now); got != tc.want {
			t.Errorf("%s: ceiling = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Live egress above the allowance is billed only for hours the election
// covers: the hour before it settles at zero, the elected hour bills only its
// own overage, and turning the election off later still bills the hours it
// covered.
func TestPushPendingEgressBillsOnlyElectedHours(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)
	unelected, elected := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "allowance", now.Add(-4*time.Hour+time.Minute), 0, 0, 0, 0, gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "unelected", unelected.Add(time.Minute), 0, 0, 0, 0, 3*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "elected", elected.Add(time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEgressOverage(ctx, acct.ID, true, elected); err != nil {
		t.Fatal(err)
	}
	// Turned off before the pusher ran: the hour it covered is still billed.
	if _, err := store.SetEgressOverage(ctx, acct.ID, false, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want only the elected hour", pushed, err)
	}
	calls := provider.MeterCalls()
	if len(calls) != 1 || calls[0].Hour != elected || calls[0].Quantity != 2*gib {
		t.Fatalf("egress calls = %+v, want 2 GiB for the elected hour only", calls)
	}
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("replay = (%d, %v), want every hour settled", pushed, err)
	}
}

// An account that never elected is not billed for live egress at all.
func TestPushPendingEgressUnelectedNeverBills(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "overshoot", now.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 5<<30, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("PushPending = (%d, %v), want nothing billed", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("egress calls = %+v, want none", calls)
	}
}

// Egress past the allowance counts toward the spend cap only when it was sent
// in an hour the election covers: the bytes the subject is billed for
// (ADR-20261103131742 amendment 2).
func TestRunQuotaOnce_EgressOutsideElectionDoesNotCountTowardCap(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	store.SetClockForTest(func() time.Time { return now })
	store.SetOverageCapCentsForTest(acct.ID, 1)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(-time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	cfg := &meter.Config{}
	cfg.Defaults()
	loop := meter.NewLoop(store, nil, &fakeParker{}, provider, &fakeNotifier{}, nil, nil, nil, nil,
		func() time.Time { return now }, discardLog(), cfg, wire.NewOpsMetrics("meter_test_egress_outside_election"))
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || found {
		t.Fatalf("spend cap hold found=%v err=%v; want none without an election", found, err)
	}
	// Elected earlier this month and withdrawn: bytes sent after the
	// withdrawal are not billed, so they still do not count.
	if _, err := store.SetEgressOverage(ctx, acct.ID, true, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEgressOverage(ctx, acct.ID, false, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || found {
		t.Fatalf("spend cap hold found=%v err=%v; want none for bytes sent after the withdrawal", found, err)
	}
	// Bytes above the allowance sent while the election was on are billed
	// and count.
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(-36*time.Hour), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || !found {
		t.Fatalf("spend cap hold found=%v err=%v; want the overage billed under the election counted", found, err)
	}
}
