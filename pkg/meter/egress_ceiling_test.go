package meter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// Egress no plan bills is capped at the plan's monthly egress: a Pro account
// past 500 GiB stops like a Free account past its compute allowance, and the
// next month lifts the stop. The ceiling holds even when egress bills live:
// the account has not elected egress overage (ADR-20261104204500).
// adr: 20261103131742 20261104090000
func TestEgressCeilingStopsUnbilledEgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := state.NewMemStore()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	acct := makeAccount(t, ctx, s, api.PlanPro)
	app := newApp(t, ctx, s, acct.ID)
	ins, err := s.CreateInstance(ctx, app.ID, "dep1", string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	ceiling := meter.EgressCeilingBytes(nil, api.PlanPro, state.EgressOverageElection{}, now)
	if ceiling != 500<<30 || meter.EgressCeilingBytes(nil, api.PlanFree, state.EgressOverageElection{}, now) != 10<<30 {
		t.Fatalf("ceilings: Pro %d, Free %d", ceiling, meter.EgressCeilingBytes(nil, api.PlanFree, state.EgressOverageElection{}, now))
	}
	notif, parker := &fakeNotifier{}, &fakeParker{}
	enforce := func(at time.Time) meter.QuotaAction {
		t.Helper()
		usages, err := s.UsageByMonth(ctx, acct.ID, meter.AccountMonthKey(at))
		if err != nil {
			t.Fatal(err)
		}
		current, err := s.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		params := meter.QuotaParams{Store: s, Notif: notif, Parker: parker, Mailer: &fakeMailer{}, Log: discardLog(),
			EgressBytes: meter.MonthlyEgressBytes(usages), EgressCeilingBytes: ceiling}
		act, err := params.Enforce(ctx, current, meter.MonthlyUsageGB(usages), at)
		if err != nil {
			t.Fatal(err)
		}
		return act
	}

	if err := s.AppendUsage(ctx, acct.ID, app.ID, ins.ID, now, 60, 0, 0, 0, ceiling-1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if act := enforce(now); act.Action != "" || len(parker.parked) != 0 {
		t.Fatalf("below the ceiling: %+v, parked %v", act, parker.parked)
	}
	if err := s.AppendUsage(ctx, acct.ID, app.ID, ins.ID, now.Add(time.Minute), 60, 0, 0, 0, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if act := enforce(now.Add(2 * time.Minute)); act.Action != "stop" || !act.EgressStop {
		t.Fatalf("at the ceiling: %+v, want an egress stop", act)
	}
	if got, _ := s.AccountByID(ctx, acct.ID); got.Status != state.AccountSuspended {
		t.Fatalf("status = %s, want suspended", got.Status)
	}
	if len(parker.parked) != 1 || parker.parked[0].Reason != "quota_exceeded_egress" {
		t.Fatalf("parked = %+v, want the instance parked for egress", parker.parked)
	}
	if calls := notif.byChannel(db.NotifyBillingPastDue); len(calls) != 1 || !strings.Contains(calls[0].Payload, `"reason":"egress"`) {
		t.Fatalf("billing_past_due = %+v, want one egress notice", calls)
	}

	if act := enforce(time.Date(2026, 12, 1, 0, 5, 0, 0, time.UTC)); act.Action != "" {
		t.Fatalf("next month: %+v, want no stop", act)
	}
	if got, _ := s.AccountByID(ctx, acct.ID); got.Status != state.AccountActive {
		t.Fatalf("next month status = %s, want active", got.Status)
	}
}
