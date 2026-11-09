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

// observeReleaseTTL is how long a read owner is reused; a plan change or a
// moved app reaches meterd's decisions within it.
const observeReleaseTTL = time.Minute

// observeRelease decides which customer observability work meterd still does
// (ADR-20261028101615): alert rules and channel deliveries, synthetic probes
// and SLO rollups. It follows the plan and status of the work's owner, as
// apid's create checks do (ADR-20261109120000): an app an organization owns
// follows the organization, any other app and every account-wide rule follow
// the account. It also follows the capability flags (ADR-20261023115741), so
// a downgraded owner or a capability an operator switched off stops costing
// the customer wakes and stops paging. A suspended owner, or one pending
// deletion, gets none of it (ADR-20261029203000): its apps do not serve, so
// probes would only fail and page it. A failed read keeps the work running: a
// database hiccup must never silence alerts.
type observeRelease struct {
	flags   *platformflags.Set
	decider *capflags.Decider
	store   logSearchReleaseStore
	now     func() time.Time

	mu       sync.Mutex
	accounts map[string]releaseOwner
	apps     map[string]releaseOwner
}

// releaseOwner is what the decisions read about the owner of some work.
type releaseOwner struct {
	plan api.Plan
	live bool
	// subject is the billing subject the plan's per-account caps count
	// against: the organization that owns an app, else the account.
	subject string
	// orgID is the organization of an app, for organization flag rules.
	orgID string
	at    time.Time
}

func newObserveRelease(flags *platformflags.Set, store logSearchReleaseStore) (*observeRelease, error) {
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("meterd: load capability catalog: %w", err)
	}
	return &observeRelease{flags: flags, decider: decider, store: store, now: time.Now,
		accounts: map[string]releaseOwner{}, apps: map[string]releaseOwner{}}, nil
}

// ForAccount is the alerts.RuleRelease for one account's tick. Each rule is
// decided by its own owner: one account can hold rules on its personal apps
// and on apps of organizations it created them in.
func (r *observeRelease) ForAccount(context.Context, string) alerts.RuleRelease {
	return func(ctx context.Context, rule state.AlertRule) (bool, bool) {
		owner, err := r.owner(ctx, rule.AccountID, rule.AppID)
		switch {
		case err != nil:
			return true, true
		case !owner.live:
			return false, false
		}
		if limits, ok := api.LimitsFor(owner.plan); !ok || limits.AlertRuleLimitPerApp == 0 {
			return false, false
		}
		subject := platformflags.Subject{AccountID: rule.AccountID, Plan: string(owner.plan), OrgID: owner.orgID}
		metric := string(rule.Metric)
		switch {
		case !r.allows("alerts", subject):
			return false, false
		case metric == api.AlertRuleMetricCustomMetric && !owner.plan.CustomMetricsAllowed():
			return false, false
		case (api.IsSLOAlertMetric(metric) || api.IsSyntheticCheckAlertMetric(metric)) && !owner.plan.PerAppMetricsAllowed():
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

// RuleLimits is the plan caps on alert rules that apply to rule, so rules a
// bigger plan allowed stop evaluating after a downgrade (ADR-20261104142830).
// They are the owner's caps, counted per billing subject (ADR-20261109120000).
// An unreadable or not-live owner reports !ok: ForAccount already decides it.
func (r *observeRelease) RuleLimits(ctx context.Context, rule state.AlertRule) (alerts.RuleLimit, bool) {
	owner, err := r.owner(ctx, rule.AccountID, rule.AppID)
	if err != nil || !owner.live {
		return alerts.RuleLimit{}, false
	}
	limits, known := api.LimitsFor(owner.plan)
	if !known {
		return alerts.RuleLimit{}, false
	}
	return alerts.RuleLimit{PerApp: limits.AlertRuleLimitPerApp, PerAccount: limits.AlertRuleLimitPerAccount, Subject: owner.subject}, true
}

// Synthetic reports whether a due synthetic check is still probed: its
// owner's plan has per-app metrics (Hobby and above, as creating one needs)
// and synthetic-checks is released to it.
func (r *observeRelease) Synthetic(ctx context.Context, check state.RunnableSyntheticCheck) bool {
	owner, err := r.owner(ctx, check.AccountID, check.AppID)
	switch {
	case err != nil:
		return true
	case !owner.live, !owner.plan.PerAppMetricsAllowed():
		return false
	}
	subject := platformflags.Subject{AccountID: check.AccountID, Plan: string(owner.plan), OrgID: owner.orgID}
	return r.allows("synthetic-checks", subject)
}

// SLO reports whether an SLO still gets hourly rollups: its owner is live,
// its plan has per-app metrics (as creating one needs) and custom-slos is
// released to it (ADR-20261029203000).
func (r *observeRelease) SLO(ctx context.Context, def state.SLO) bool {
	owner, err := r.owner(ctx, def.AccountID, def.AppID)
	switch {
	case err != nil:
		return true
	case !owner.live, !owner.plan.PerAppMetricsAllowed():
		return false
	}
	subject := platformflags.Subject{AccountID: def.AccountID, Plan: string(owner.plan), OrgID: owner.orgID}
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

// owner reads, through the cache, who owns work on appID for accountID: the
// app's resource owner (state.AppResourceAccount, as apid decides), or the
// account for account-wide work and for an app that is already gone. live is
// false for an owner that no longer exists, is suspended, or is pending
// deletion.
func (r *observeRelease) owner(ctx context.Context, accountID, appID string) (releaseOwner, error) {
	if appID == "" {
		return r.account(ctx, accountID)
	}
	now := r.now()
	r.mu.Lock()
	cached, ok := r.apps[appID]
	r.mu.Unlock()
	if ok && now.Sub(cached.at) < observeReleaseTTL {
		return cached, nil
	}
	app, err := r.store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return r.account(ctx, accountID)
	}
	if err != nil {
		return releaseOwner{}, err
	}
	acct, err := state.AppResourceAccount(ctx, r.store, app)
	switch {
	case errors.Is(err, state.ErrNotFound):
		cached = releaseOwner{live: false, at: now}
	case err != nil:
		return releaseOwner{}, err
	default:
		cached = ownerOf(acct, now)
		cached.orgID = app.OrgID
	}
	r.mu.Lock()
	r.apps[appID] = cached
	r.mu.Unlock()
	return cached, nil
}

// account reads an account's plan through the cache.
func (r *observeRelease) account(ctx context.Context, id string) (releaseOwner, error) {
	now := r.now()
	r.mu.Lock()
	cached, ok := r.accounts[id]
	r.mu.Unlock()
	if ok && now.Sub(cached.at) < observeReleaseTTL {
		return cached, nil
	}
	acct, err := r.store.AccountByID(ctx, id)
	switch {
	case errors.Is(err, state.ErrNotFound):
		cached = releaseOwner{live: false, at: now}
	case err != nil:
		return releaseOwner{}, err
	default:
		cached = ownerOf(acct, now)
	}
	r.mu.Lock()
	r.accounts[id] = cached
	r.mu.Unlock()
	return cached, nil
}

func ownerOf(acct state.Account, at time.Time) releaseOwner {
	return releaseOwner{
		plan:    acct.Plan,
		live:    acct.Status != state.AccountSuspended && acct.Status != state.AccountDeletedPending,
		subject: acct.ResourceOwnerID(),
		at:      at,
	}
}
