package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// Metering review r10: a paid subject with no billing identity is never
// billed; PushPending reports it per plan instead of only logging.
func TestPushPending_ReportsPaidSubjectsWithoutIdentity(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	makeBillableAccount(t, ctx, s, api.PlanPro) // billed: not counted
	makeAccount(t, ctx, s, api.PlanScale)       // paid, no identity
	if _, err := s.CreateAccount(ctx, "second-scale@example.com", api.PlanScale); err != nil {
		t.Fatal(err) // paid, no identity
	}
	makeAccount(t, ctx, s, api.PlanHobby)        // paid, no identity
	makeBillableAccount(t, ctx, s, api.PlanFree) // free: not counted

	ops := testOpsMetrics(t)
	pusher := meter.NewPusher(s, &recordingStripe{}, discardLog(), clock, ops)
	// meterd's loop runs PushPending, never PushHour; the gauge must come
	// from it. A delivery error does not matter here.
	_, _ = pusher.PushPending(ctx, 0)
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "meterd_billing_paid_subjects_without_identity" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "plan" {
					got[l.GetValue()] = m.GetGauge().GetValue()
				}
			}
		}
	}
	want := map[string]float64{"hobby": 1, "pro": 0, "scale": 2}
	for plan, n := range want {
		if got[plan] != n {
			t.Errorf("plan %s = %v, want %v (all: %v)", plan, got[plan], n, got)
		}
	}
}
