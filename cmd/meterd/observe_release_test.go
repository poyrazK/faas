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

type observeReleaseFake struct {
	accounts map[string]state.Account
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

func (f observeReleaseFake) AppByID(_ context.Context, id string) (state.App, error) {
	return state.App{ID: id}, nil
}

const (
	proAccount  = "00000000-0000-4000-8000-0000000000a1"
	freeAccount = "00000000-0000-4000-8000-0000000000a2"
	goneAccount = "00000000-0000-4000-8000-0000000000a9"
)

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
	store := observeReleaseFake{accounts: map[string]state.Account{
		proAccount:  {ID: proAccount, Plan: api.PlanPro},
		freeAccount: {ID: freeAccount, Plan: api.PlanFree},
	}}
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
			evaluate, channels := release(context.Background(), tc.rule)
			if evaluate != tc.evaluate || channels != tc.channels {
				t.Fatalf("decision = (%v, %v), want (%v, %v)", evaluate, channels, tc.evaluate, tc.channels)
			}
		})
	}
	if release := releaseFor(t, observeReleaseFake{err: errors.New("connection refused")}, nil).ForAccount(context.Background(), proAccount); release != nil {
		t.Fatal("an unreadable account must keep every rule running")
	}
}

func TestObserveReleaseDecidesSyntheticChecks(t *testing.T) {
	on := platformflags.Definition{Enabled: true, Default: true}
	store := observeReleaseFake{accounts: map[string]state.Account{
		proAccount:  {ID: proAccount, Plan: api.PlanPro},
		freeAccount: {ID: freeAccount, Plan: api.PlanFree},
	}}
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
	if !releaseFor(t, observeReleaseFake{err: errors.New("connection refused")}, nil).Synthetic(ctx, check(proAccount)) {
		t.Error("an unreadable account must keep its checks running")
	}
}
