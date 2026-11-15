package attention

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestForCustomerWordsOnlyCustomerKinds(t *testing.T) {
	since := time.Date(2026, 11, 14, 8, 0, 0, 0, time.FixedZone("x", 3600))
	open := func(kind, subject string, observed int64) api.ObsAttentionSignal {
		return api.ObsAttentionSignal{Kind: kind, Subject: subject, Observed: observed, State: api.AttentionStateOpen,
			Detail: "internal staff detail", FirstSeenAt: since}
	}
	dismissed := open(api.AttentionKindAppOOM, "dismissed-app", 4)
	dismissed.State = api.AttentionStateDismissed
	got := ForCustomer([]api.ObsAttentionSignal{
		open(api.AttentionKindDeployFailing, "shop", 3),
		open(api.AttentionKindAppOOM, "api", 1),
		open(api.AttentionKindJobsFailing, "nightly", 3),
		open(api.AttentionKindCronsFailing, "shop /sync", 5),
		open(api.AttentionKindLimitPressure, "plan_limit_apps", 4),
		open(api.AttentionKindRefusedBurst, "unauthorized", 20),
		open(api.AttentionKindDomainStuck, "www.shop.example", 1),
		open(api.AttentionKindPaymentFailing, "billing", 1),
		open(api.AttentionKindBootFailing, "shop", 5),
		open(api.AttentionKindDeployFailing, "", 3),
		dismissed,
	})
	if len(got) != 5 {
		t.Fatalf("got %d customer signals, want 5: %+v", len(got), got)
	}
	want := []struct{ kind, title, detail, url string }{
		{api.AttentionKindDeployFailing, "Deployments of shop keep failing", "3 deployments failed in the last 24 hours", "/dashboard/apps/shop"},
		{api.AttentionKindAppOOM, "api keeps running out of memory", "exceeding its memory limit once in the last 24 hours", "/dashboard/billing"},
		{api.AttentionKindJobsFailing, "Scheduled job nightly keeps failing", "Its last 3 scheduled runs failed. Run gregale jobs runs nightly", ""},
		{api.AttentionKindCronsFailing, "Cron /sync on shop keeps failing", "Its last 5 runs failed", "/dashboard/apps/shop"},
		{api.AttentionKindLimitPressure, "You keep reaching a plan limit", "refused with plan_limit_apps on 4 days of the last 7 days", "/dashboard/billing"},
	}
	for i, w := range want {
		s := got[i]
		if s.Kind != w.kind || s.Title != w.title || !strings.Contains(s.Detail, w.detail) || s.ActionURL != w.url {
			t.Errorf("signal %d = %+v, want %+v", i, s, w)
		}
		if strings.Contains(s.Detail, "internal") {
			t.Errorf("signal %d leaks the staff detail: %q", i, s.Detail)
		}
		if !s.Since.Equal(since) || s.Since.Location() != time.UTC {
			t.Errorf("signal %d since = %v, want %v in UTC", i, s.Since, since)
		}
	}
	if !slices.Equal(CustomerKinds(), []string{api.AttentionKindDeployFailing, api.AttentionKindAppOOM,
		api.AttentionKindJobsFailing, api.AttentionKindCronsFailing, api.AttentionKindLimitPressure}) {
		t.Fatalf("customer kinds = %v", CustomerKinds())
	}
	if appURL("../admin") != "/dashboard/apps" {
		t.Fatal("an invalid slug reached the action URL")
	}
	if got := ForCustomer(nil); got == nil || len(got) != 0 {
		t.Fatalf("ForCustomer(nil) = %#v, want an empty slice", got)
	}
}

func TestOpenForCustomerReadsOnlyTheAccountsOpenSignals(t *testing.T) {
	var none *Evaluator
	if got, err := none.OpenForCustomer(t.Context(), uuid.NewString()); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil evaluator = %#v, %v", got, err)
	}
	e, pool := pgEvaluator(t)
	ctx := t.Context()
	f := fixture{t: t, pool: pool, now: time.Now().UTC().Truncate(time.Second)}
	mine, other := f.account(), f.account()
	for _, account := range []string{mine, other} {
		app := f.app(account, "shop")
		for i := 0; i < api.AttentionDeployFailures; i++ {
			f.deploy(app, "failed", time.Hour, "build_failed")
		}
	}
	f.refusals(mine, "plan_limit_apps", 1, time.Hour)
	f.refusals(mine, "plan_limit_apps", 1, 25*time.Hour)
	f.refusals(mine, "plan_limit_apps", 1, 49*time.Hour)
	f.refusals(mine, api.CodeUnauthorized, api.AttentionRefusedBurst, time.Minute)
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.OpenForCustomer(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range got {
		kinds[s.Kind] = s.Subject
	}
	if len(got) != 2 || kinds[api.AttentionKindDeployFailing] != "shop" || kinds[api.AttentionKindLimitPressure] != "plan_limit_apps" {
		t.Fatalf("customer signals = %+v; want deploy_failing and limit_pressure only", got)
	}

	// Staff dismissing a signal hides it from the customer.
	signals, err := e.List(ctx, Filter{AccountID: mine, Kind: api.AttentionKindDeployFailing})
	if err != nil || len(signals) != 1 {
		t.Fatalf("list = %v, %v", signals, err)
	}
	if err := e.Dismiss(ctx, signals[0].ID, f.account(), api.AttentionOutcomeFalsePositive, ""); err != nil {
		t.Fatal(err)
	}
	got, err = e.OpenForCustomer(ctx, mine)
	if err != nil || len(got) != 1 || got[0].Kind != api.AttentionKindLimitPressure {
		t.Fatalf("after dismiss = %+v, %v", got, err)
	}
}
