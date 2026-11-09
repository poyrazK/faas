package apidserver

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// keepingBillingProvider is fakeBillingProvider with a subscription that
// reports its scheduled change and can be kept.
type keepingBillingProvider struct {
	*fakeBillingProvider
	current billing.SubscriptionState
	resumed int
}

func (k *keepingBillingProvider) SubscriptionState(context.Context, string) (billing.SubscriptionState, error) {
	return k.current, nil
}

func (k *keepingBillingProvider) ResumeSubscription(context.Context, state.Account) (bool, error) {
	k.resumed++
	undone := k.current.ScheduledChange()
	k.current.CancelAtPeriodEnd, k.current.EndsAt = false, time.Time{}
	k.current.PendingPlanID, k.current.PendingAt = "", time.Time{}
	return undone, nil
}

// The dashboard showed nothing of a scheduled cancellation and offered no way
// to undo it. The upgrade page now says when the subscription ends, refuses
// upgrades while it is ending, and Keep subscription undoes the cancellation
// (ADR-20261103131742 amendment 1).
func TestDashboardKeepSubscription(t *testing.T) {
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	fake := &keepingBillingProvider{
		fakeBillingProvider: &fakeBillingProvider{},
		current:             billing.SubscriptionState{Status: billing.SubscriptionStatusActive, PlanID: "pro", CancelAtPeriodEnd: true, EndsAt: end},
	}
	h, sid, store, acct := newUpgradeDashboardServer(t, api.PlanPro, fake)
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), acct.ID, "sub_live"); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	page, cookie := getUpgradePage(t, h, sid, "?plan=scale")
	body := page.Body.String()
	for _, want := range []string{"ends on 2026-12-01", "Keep subscription", "cannot change plans"} {
		if !strings.Contains(body, want) {
			t.Fatalf("upgrade page lacks %q\nbody = %s", want, body)
		}
	}
	if strings.Contains(body, "Upgrade now") {
		t.Fatalf("an ending subscription must not offer Upgrade now\nbody = %s", body)
	}
	tok := extractInputValue(body, middleware.FormFieldName, "/dashboard/upgrade")
	rec := dashboardPOST(t, h, sid, "/dashboard/upgrade",
		map[string]string{middleware.FormFieldName: tok, "plan": "pro"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/billing?upgrade=kept" {
		t.Fatalf("keep POST = %d %q, want 303 to ?upgrade=kept", rec.Code, rec.Header().Get("Location"))
	}
	if fake.resumed != 1 || fake.planChangeCalls != 0 || fake.calls != 0 {
		t.Fatalf("provider calls: resumed=%d change=%d checkout=%d; want 1, 0, 0", fake.resumed, fake.planChangeCalls, fake.calls)
	}
	if notice := upgradeNoticeFor("kept"); notice == "" {
		t.Fatal("the kept flag has no notice")
	}
}

// The account page sent a downgrade to the billing portal, which no longer
// switches plans (ADR-20261103131742). It is scheduled with the provider.
func TestDashboardAccountPlan_DowngradeIsScheduled(t *testing.T) {
	fake := &fakeBillingProvider{planChangeEffective: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	h, sid, store, acct := newAccountPlanDashboardServer(t, api.PlanPro, "", fake)
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), acct.ID, "sub_live"); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	csrfCookie, token, _ := renderAccountPlan(t, h, sid)
	rec := dashboardPOST(t, h, sid, "/dashboard/account/plan", map[string]string{
		middleware.FormFieldName: token,
		"plan":                   "hobby",
	}, csrfCookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/billing?upgrade=downgrade_scheduled" {
		t.Fatalf("downgrade POST = %d %q, want 303 to ?upgrade=downgrade_scheduled", rec.Code, rec.Header().Get("Location"))
	}
	if fake.planChangeCalls != 1 || fake.planChangeTarget != api.PlanHobby {
		t.Fatalf("plan change calls=%d target=%q, want 1 hobby", fake.planChangeCalls, fake.planChangeTarget)
	}
	if got, err := store.AccountByID(t.Context(), acct.ID); err != nil || got.Plan != api.PlanPro {
		t.Fatalf("plan = %q, %v; want pro until the period ends", got.Plan, err)
	}
}
