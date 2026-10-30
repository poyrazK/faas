package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// A paid month warns only above the largest allowance its plans granted, the
// allowance billing removes; Free keeps its own allowance after a downgrade.
// adr: 20261030120000
func TestEnforceQuotaFollowsTheMonthsPlanHistory(t *testing.T) {
	ctx := context.Background()
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	downgrade := monthStart.Add(10 * 24 * time.Hour)
	now := downgrade.Add(time.Hour)
	// Between Hobby's and Pro's allowance.
	usedGB := float64(api.PlanHobby.PlanIncludedGBHours()+api.PlanPro.PlanIncludedGBHours()) / 2

	for _, tc := range []struct {
		name     string
		to       api.Plan
		wantWarn bool
		wantStop bool
	}{
		{name: "pro to hobby", to: api.PlanHobby},
		{name: "pro to free", to: api.PlanFree, wantStop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, s, api.PlanPro)
			putComputeGrant(t, ctx, s, acct, api.PlanPro, monthStart)
			putComputeGrant(t, ctx, s, acct, tc.to, downgrade)
			if err := s.UpdateAccountPlan(ctx, acct.ID, tc.to); err != nil {
				t.Fatal(err)
			}
			acct.Plan = tc.to
			notif, mailer := &fakeNotifier{}, &fakeMailer{}
			act, err := meter.EnforceQuota(ctx, s, notif, &fakeParker{}, mailer, discardLog(), acct, usedGB, now)
			if err != nil {
				t.Fatalf("EnforceQuota: %v", err)
			}
			if warned := len(notif.byChannel(db.NotifyQuotaWarning)) > 0 || mailer.count() > 0; warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v (action %q)", warned, tc.wantWarn, act.Action)
			}
			if stopped := act.Action == "stop"; stopped != tc.wantStop {
				t.Errorf("stopped = %v, want %v", stopped, tc.wantStop)
			}
		})
	}

	// Above the month's largest allowance a paid account still warns.
	s := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, s, api.PlanPro)
	putComputeGrant(t, ctx, s, acct, api.PlanPro, monthStart)
	putComputeGrant(t, ctx, s, acct, api.PlanHobby, downgrade)
	acct.Plan = api.PlanHobby
	act, err := meter.EnforceQuota(ctx, s, &fakeNotifier{}, &fakeParker{}, &fakeMailer{}, discardLog(), acct,
		float64(api.PlanPro.PlanIncludedGBHours()+1), now)
	if err != nil || act.Action != "warn" || act.QuotaGB != api.PlanPro.PlanIncludedGBHours() {
		t.Fatalf("above the Pro grant: action %q quota %d err %v, want warn at %d", act.Action, act.QuotaGB, err, api.PlanPro.PlanIncludedGBHours())
	}
}
