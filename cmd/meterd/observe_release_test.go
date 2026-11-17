package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261028101615 — meterd stops alert rules, channel deliveries and
// synthetic probes that an account's plan or capability flags no longer
// cover, and keeps them running when it cannot read the account.
// adr: 20261029203000 — a suspended account, or one pending deletion, gets
// neither rules nor probes; a past-due account still serves and keeps both.

type observeReleaseFake struct {
	accounts map[string]state.Account
	apps     map[string]state.App
	orgs     map[string]state.Org
	err      error
}

func (f observeReleaseFake) AccountByID(_ context.Context, id string) (state.Account, error) {
	if f.err != nil {
		return state.Account{}, f.err
	}
	if acct, ok := f.accounts[id]; ok {
		return acct, nil
	}
	return state.Account{}, state.ErrNotFound
}

// AppByID knows only the apps a test lists; any other app reads as gone, so
// its work is decided by the account.
func (f observeReleaseFake) AppByID(_ context.Context, id string) (state.App, error) {
	if f.err != nil {
		return state.App{}, f.err
	}
	if app, ok := f.apps[id]; ok {
		return app, nil
	}
	return state.App{}, state.ErrNotFound
}

func (f observeReleaseFake) OrgByID(_ context.Context, id string) (state.Org, error) {
	if org, ok := f.orgs[id]; ok {
		return org, nil
	}
	return state.Org{}, state.ErrNotFound
}

const (
	proOrg       = "00000000-0000-4000-8000-0000000000c1"
	suspendedOrg = "00000000-0000-4000-8000-0000000000c2"
)

// orgStore is releaseAccounts plus a Free member's apps in a Pro organization
// and a suspended one, both owning their apps.
func orgStore() observeReleaseFake {
	return observeReleaseFake{
		accounts: releaseAccounts(),
		apps: map[string]state.App{
			"org-app":       {ID: "org-app", AccountID: freeAccount, OrgID: proOrg},
			"suspended-app": {ID: "suspended-app", AccountID: proAccount, OrgID: suspendedOrg},
		},
		orgs: map[string]state.Org{
			proOrg:       {ID: proOrg, Plan: api.PlanPro, Status: state.OrgStatusActive, ResourceOwnershipEnabled: true},
			suspendedOrg: {ID: suspendedOrg, Plan: api.PlanPro, Status: state.OrgStatusSuspended, ResourceOwnershipEnabled: true},
		},
	}
}

// adr: 20261109153000 — work on an app an organization owns follows the
// organization's plan and status, not the creating member's: a Free member's
// rules, probes and rollups in a Pro organization run, and a suspended
// organization's stop although its member's account is active.
func TestObserveReleaseFollowsTheAppsOwner(t *testing.T) {
	on := platformflags.Definition{Enabled: true, Default: true}
	written := map[string]platformflags.Definition{"synthetic-checks": on, "custom-slos": on}
	r := releaseFor(t, orgStore(), written)
	ctx := context.Background()
	rule := state.AlertRule{ID: "r1", AccountID: freeAccount, AppID: "org-app", Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt}
	if evaluate, channels := r.ForAccount(ctx, freeAccount)(ctx, rule); !evaluate || !channels {
		t.Errorf("a Free member's rule on a Pro organization's app = (%v, %v), want it running", evaluate, channels)
	}
	limit, ok := r.RuleLimits(ctx, rule)
	pro, _ := api.LimitsFor(api.PlanPro)
	if !ok || limit.PerApp != pro.AlertRuleLimitPerApp || limit.Subject != proOrg {
		t.Errorf("caps = %+v, %v; want the organization's Pro caps counted against it", limit, ok)
	}
	check := state.RunnableSyntheticCheck{SyntheticCheck: state.SyntheticCheck{AccountID: freeAccount, AppID: "org-app"}}
	if !r.Synthetic(ctx, check) {
		t.Error("a Pro organization's check must be probed whoever created it")
	}
	if !r.SLO(ctx, state.SLO{AccountID: freeAccount, AppID: "org-app"}) {
		t.Error("a Pro organization's SLO must get rollups whoever created it")
	}
	suspended := state.AlertRule{ID: "r2", AccountID: proAccount, AppID: "suspended-app", Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt}
	if evaluate, _ := r.ForAccount(ctx, proAccount)(ctx, suspended); evaluate {
		t.Error("a suspended organization's rule must not run though its member is active")
	}
	if r.Synthetic(ctx, state.RunnableSyntheticCheck{SyntheticCheck: state.SyntheticCheck{AccountID: proAccount, AppID: "suspended-app"}}) {
		t.Error("a suspended organization's check must not be probed")
	}
	// An account-wide rule still follows the account.
	if evaluate, _ := r.ForAccount(ctx, freeAccount)(ctx, state.AlertRule{AccountID: freeAccount, Metric: state.AlertMetricErrorRate}); evaluate {
		t.Error("a Free account's own account-wide rule must not run")
	}
}

const (
	proAccount       = "00000000-0000-4000-8000-0000000000a1"
	freeAccount      = "00000000-0000-4000-8000-0000000000a2"
	pastDueAccount   = "00000000-0000-4000-8000-0000000000a3"
	suspendedAccount = "00000000-0000-4000-8000-0000000000a4"
	deletingAccount  = "00000000-0000-4000-8000-0000000000a5"
	goneAccount      = "00000000-0000-4000-8000-0000000000a9"
)

// releaseAccounts are the accounts both decision tests read.
func releaseAccounts() map[string]state.Account {
	return map[string]state.Account{
		proAccount:       {ID: proAccount, Plan: api.PlanPro, Status: state.AccountActive},
		freeAccount:      {ID: freeAccount, Plan: api.PlanFree, Status: state.AccountActive},
		pastDueAccount:   {ID: pastDueAccount, Plan: api.PlanPro, Status: state.AccountPastDue},
		suspendedAccount: {ID: suspendedAccount, Plan: api.PlanPro, Status: state.AccountSuspended},
		deletingAccount:  {ID: deletingAccount, Plan: api.PlanPro, Status: state.AccountDeletedPending},
	}
}

func releaseFor(t *testing.T, store observeReleaseFake, written map[string]platformflags.Definition) *observeRelease {
	t.Helper()
	flags := platformflags.NewSet(nil)
	for capability, def := range written {
		key, ok := platformflags.CapabilityFlag(capability)
		if !ok {
			t.Fatalf("capability %q has no flag", capability)
		}
		if err := flags.Apply(platformflags.ConfigKey(key), def.Marshal(), 1); err != nil {
			t.Fatal(err)
		}
	}
	r, err := newObserveRelease(flags, store)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestObserveReleaseDecidesAlertRules(t *testing.T) {
	on, off := platformflags.Definition{Enabled: true, Default: true}, platformflags.Definition{Enabled: false}
	store := observeReleaseFake{accounts: releaseAccounts()}
	rule := func(metric, comparison string) state.AlertRule {
		return state.AlertRule{AccountID: proAccount, AppID: "app", Metric: state.AlertMetric(metric), Comparison: state.AlertComparison(comparison)}
	}
	for _, tc := range []struct {
		name               string
		account            string
		written            map[string]platformflags.Definition
		rule               state.AlertRule
		evaluate, channels bool
	}{
		{name: "a beta metric on a paid plan runs and notifies", account: proAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt"), evaluate: true, channels: true},
		{name: "a plan without alerts runs nothing", account: freeAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt")},
		{name: "an account that no longer exists runs nothing", account: goneAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt")},
		{name: "a suspended account runs nothing", account: suspendedAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt")},
		{name: "an account pending deletion runs nothing", account: deletingAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt")},
		{name: "a past-due account still serves, so it keeps its alerts", account: pastDueAccount,
			rule: rule(string(state.AlertMetricErrorRate), "gt"), evaluate: true, channels: true},
		{name: "an internal capability's metric waits for its release", account: proAccount,
			rule: rule(api.AlertRuleMetricLogSearchMatches, "gt")},
		{name: "releasing one capability does not release another", account: proAccount, written: map[string]platformflags.Definition{"log-search": on},
			rule: rule(api.AlertRuleMetricCustomMetric, "gt")},
		{name: "a released capability's metric runs once that capability is released", account: proAccount, written: map[string]platformflags.Definition{"custom-metric-history": on},
			rule: rule(api.AlertRuleMetricCustomMetric, "gt"), evaluate: true, channels: true},
		{name: "channels switched off still evaluate the rule", account: proAccount, written: map[string]platformflags.Definition{"notification-channels": off},
			rule: rule(string(state.AlertMetricErrorRate), "gt"), evaluate: true, channels: false},
		{name: "anomaly_alerts off keeps existing baseline rules evaluating, as its flag promises", account: proAccount, written: map[string]platformflags.Definition{"anomaly-alerts": off},
			rule: rule(string(state.AlertMetricErrorRate), api.AlertComparisonAboveBaseline), evaluate: true, channels: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := releaseFor(t, store, tc.written).ForAccount(context.Background(), tc.account)
			if release == nil {
				t.Fatal("a readable account must get a decision")
			}
			tc.rule.AccountID = tc.account
			evaluate, channels := release(context.Background(), tc.rule)
			if evaluate != tc.evaluate || channels != tc.channels {
				t.Fatalf("decision = (%v, %v), want (%v, %v)", evaluate, channels, tc.evaluate, tc.channels)
			}
		})
	}
	release := releaseFor(t, observeReleaseFake{err: errors.New("connection refused")}, nil).ForAccount(context.Background(), proAccount)
	if evaluate, channels := release(context.Background(), rule(string(state.AlertMetricErrorRate), "gt")); !evaluate || !channels {
		t.Fatal("an unreadable account must keep every rule running")
	}
}

func TestObserveReleaseDecidesSyntheticChecks(t *testing.T) {
	on := platformflags.Definition{Enabled: true, Default: true}
	store := observeReleaseFake{accounts: releaseAccounts()}
	check := func(account string) state.RunnableSyntheticCheck {
		return state.RunnableSyntheticCheck{SyntheticCheck: state.SyntheticCheck{AccountID: account, AppID: "app"}}
	}
	ctx := context.Background()
	if releaseFor(t, store, nil).Synthetic(ctx, check(proAccount)) {
		t.Error("a preview capability must stay off until released")
	}
	released := releaseFor(t, store, map[string]platformflags.Definition{"synthetic-checks": on})
	if !released.Synthetic(ctx, check(proAccount)) {
		t.Error("a released account on a paid plan must be probed")
	}
	if released.Synthetic(ctx, check(freeAccount)) || released.Synthetic(ctx, check(goneAccount)) {
		t.Error("a downgraded or deleted account must not be probed")
	}
	if released.Synthetic(ctx, check(suspendedAccount)) || released.Synthetic(ctx, check(deletingAccount)) {
		t.Error("a suspended account, or one pending deletion, must not be probed")
	}
	if !released.Synthetic(ctx, check(pastDueAccount)) {
		t.Error("a past-due account still serves and must keep its probes")
	}
	if !releaseFor(t, observeReleaseFake{err: errors.New("connection refused")}, nil).Synthetic(ctx, check(proAccount)) {
		t.Error("an unreadable account must keep its checks running")
	}
}

func TestObserveReleaseDecidesSLORollups(t *testing.T) {
	on := platformflags.Definition{Enabled: true, Default: true}
	store := observeReleaseFake{accounts: releaseAccounts()}
	def := func(account string) state.SLO { return state.SLO{AccountID: account, AppID: "app"} }
	ctx := context.Background()
	if releaseFor(t, store, nil).SLO(ctx, def(proAccount)) {
		t.Error("a preview capability must stay off until released")
	}
	released := releaseFor(t, store, map[string]platformflags.Definition{"custom-slos": on})
	if !released.SLO(ctx, def(proAccount)) || !released.SLO(ctx, def(pastDueAccount)) {
		t.Error("a released account on a paid plan must get rollups")
	}
	for _, account := range []string{freeAccount, suspendedAccount, deletingAccount, goneAccount} {
		if released.SLO(ctx, def(account)) {
			t.Errorf("account %s must get no rollups", account)
		}
	}
	if !releaseFor(t, observeReleaseFake{err: errors.New("connection refused")}, nil).SLO(ctx, def(proAccount)) {
		t.Error("an unreadable account must keep its rollups")
	}
}
