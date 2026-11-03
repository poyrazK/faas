package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeReleaseTTL is how long a read account plan or app organization is
// reused; a plan change or a moved app reaches meterd's decisions within it.
const observeReleaseTTL = time.Minute

// observeRelease decides which customer observability work meterd still does
// for an account (ADR-20261028101615): alert rules and channel deliveries, and
// synthetic probes. It follows the account's plan, as apid's create checks do,
// and the capability flags (ADR-20261023115741), so a downgraded account or a
// capability an operator switched off stops costing the customer wakes and
// stops paging. A suspended account, or one pending deletion, gets neither
// (ADR-20261029203000): its apps do not serve, so probes would only fail and
// page it. A failed read keeps the work running: a database hiccup must never
// silence alerts.
type observeRelease struct {
	flags   *platformflags.Set
	decider *capflags.Decider
	store   logSearchReleaseStore
	now     func() time.Time

	mu       sync.Mutex
	accounts map[string]releaseAccount
	apps     map[string]releaseApp
}

type releaseAccount struct {
	plan api.Plan
	live bool
	at   time.Time
}

type releaseApp struct {
	orgID string
	at    time.Time
}

func newObserveRelease(flags *platformflags.Set, store logSearchReleaseStore) (*observeRelease, error) {
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("meterd: load capability catalog: %w", err)
	}
	return &observeRelease{flags: flags, decider: decider, store: store, now: time.Now,
		accounts: map[string]releaseAccount{}, apps: map[string]releaseApp{}}, nil
}

// ForAccount is the alerts.RuleRelease for one account's tick. nil (evaluate
// everything) when the account cannot be read.
func (r *observeRelease) ForAccount(ctx context.Context, accountID string) alerts.RuleRelease {
	plan, live, err := r.account(ctx, accountID)
	switch {
	case err != nil:
		return nil
	case !live:
		return func(context.Context, state.AlertRule) (bool, bool) { return false, false }
	}
	if limits, ok := api.LimitsFor(plan); !ok || limits.AlertRuleLimitPerApp == 0 {
		return func(context.Context, state.AlertRule) (bool, bool) { return false, false }
	}
	return func(ctx context.Context, rule state.AlertRule) (bool, bool) {
		subject := platformflags.Subject{AccountID: accountID, Plan: string(plan), OrgID: r.orgOf(ctx, rule.AppID)}
		metric := string(rule.Metric)
		switch {
		case !r.allows("alerts", subject):
			return false, false
		case metric == api.AlertRuleMetricCustomMetric && !plan.CustomMetricsAllowed():
			return false, false
		case (api.IsSLOAlertMetric(metric) || api.IsSyntheticCheckAlertMetric(metric)) && !plan.PerAppMetricsAllowed():
			return false, false
		}
		// anomaly_alerts is not decided here: its flag only refuses creating or
		// changing a baseline rule, and existing ones keep evaluating (ADR-744).
		if capability, ok := api.AlertMetricCapability(metric); ok && !r.allows(capability, subject) {
			return false, false
		}
		return true, r.allows("notification-channels", subject)
	}
}

// Synthetic reports whether a due synthetic check is still probed: its
// account's plan has per-app metrics (Hobby and above, as creating one needs)
// and synthetic-checks is released to it.
func (r *observeRelease) Synthetic(ctx context.Context, check state.RunnableSyntheticCheck) bool {
	plan, live, err := r.account(ctx, check.AccountID)
	switch {
	case err != nil:
		return true
	case !live, !plan.PerAppMetricsAllowed():
		return false
	}
	subject := platformflags.Subject{AccountID: check.AccountID, Plan: string(plan), OrgID: r.orgOf(ctx, check.AppID)}
	return r.allows("synthetic-checks", subject)
}

// SLO reports whether an SLO still gets hourly rollups: its account is live,
// its plan has per-app metrics (as creating one needs) and custom-slos is
// released to it (ADR-20261029203000).
func (r *observeRelease) SLO(ctx context.Context, def state.SLO) bool {
	plan, live, err := r.account(ctx, def.AccountID)
	switch {
	case err != nil:
		return true
	case !live, !plan.PerAppMetricsAllowed():
		return false
	}
	subject := platformflags.Subject{AccountID: def.AccountID, Plan: string(plan), OrgID: r.orgOf(ctx, def.AppID)}
	return r.allows("custom-slos", subject)
}

// allows decides a capability's flag for subject. A hand-written flag no
// operator has written yet falls back, in apid, to an environment gate meterd
// cannot read; apid already checked it when the rule or check was created, so
// meterd allows.
func (r *observeRelease) allows(capability string, subject platformflags.Subject) bool {
	if key, ok := platformflags.CapabilityFlag(capability); ok {
		if spec, ok := platformflags.Lookup(key); ok && !spec.Generated {
			if _, version, written := r.flags.Snapshot().Definition(key); !written || version == 0 {
				return true
			}
		}
	}
	return r.decider.Enabled(capability, subject)
}

// account reads an account's plan through the cache; live is false for an
// account that no longer exists, is suspended, or is pending deletion.
func (r *observeRelease) account(ctx context.Context, id string) (api.Plan, bool, error) {
	now := r.now()
	r.mu.Lock()
	cached, ok := r.accounts[id]
	r.mu.Unlock()
	if ok && now.Sub(cached.at) < observeReleaseTTL {
		return cached.plan, cached.live, nil
	}
	acct, err := r.store.AccountByID(ctx, id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		cached = releaseAccount{live: false, at: now}
	case err != nil:
		return "", false, err
	default:
		live := acct.Status != state.AccountSuspended && acct.Status != state.AccountDeletedPending
		cached = releaseAccount{plan: acct.Plan, live: live, at: now}
	}
	r.mu.Lock()
	r.accounts[id] = cached
	r.mu.Unlock()
	return cached.plan, cached.live, nil
}

// orgOf is the organization an app belongs to, for organization rules; "" for
// account-wide rules, personal apps, and apps that cannot be read.
func (r *observeRelease) orgOf(ctx context.Context, appID string) string {
	if appID == "" {
		return ""
	}
	now := r.now()
	r.mu.Lock()
	cached, ok := r.apps[appID]
	r.mu.Unlock()
	if ok && now.Sub(cached.at) < observeReleaseTTL {
		return cached.orgID
	}
	app, err := r.store.AppByID(ctx, appID)
	if err != nil {
		return ""
	}
	r.mu.Lock()
	r.apps[appID] = releaseApp{orgID: app.OrgID, at: now}
	r.mu.Unlock()
	return app.OrgID
}
