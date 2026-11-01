package receivers

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

// TracingReleased decides the tracing capability's platform flag for a
// subject (ADR-20261023115741): the same decision apid's capability gate
// makes for GET /v1/account/traces, applied where spans are ingested. Nil
// releases every subject.
type TracingReleased func(platformflags.Subject) bool

// telemetryPlanTTL matches the limiter's own plan cache: a plan change takes
// effect within a minute.
const telemetryPlanTTL = time.Minute

// telemetryPlanMaxEntries bounds the cache; past it, expired entries are
// swept and, if that is not enough, the cache starts over.
const telemetryPlanMaxEntries = 50000

// telemetryPlan is the plan that admits an app's telemetry and the bucket it
// draws from (tracing tenancy T6, 2026-10-31 logs/traces audit).
type telemetryPlan struct {
	// Subject keys the rate-limit bucket: the organization for a company
	// that owns its resources, else the account.
	Subject uuid.UUID
	Limits  api.Limits
	// Flag is the platform-flag subject for the capability gate.
	Flag platformflags.Subject
}

type telemetryPlanKey struct {
	app  bool
	org  bool
	id   uuid.UUID
	user uuid.UUID // org keys: the holder, whose plan applies to a legacy company
}

type telemetryPlanEntry struct {
	plan telemetryPlan
	at   time.Time
}

type telemetryAccounts interface {
	AccountByID(ctx context.Context, id string) (state.Account, error)
}

// telemetryOwners is what resolving an app's resource owner reads. Stores
// without it (older test adapters) decide by the storage account, as before.
type telemetryOwners interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	state.AppOwnerReader
}

// telemetryPlans resolves telemetry plans from the app's resource owner
// (state.AppResourceAccount): the owner request-telemetry retention already
// keys on through app_resource_accounts, so the plan that admits a row is the
// plan that keeps it. A creator's personal plan no longer decides for the
// company apps they made. Results are cached so steady-state ingest does no
// extra database read; a fresh resolution also tells the limiter the bucket's
// authoritative capacity.
type telemetryPlans struct {
	store   telemetryAccounts
	limiter *peraccount.Limiter
	now     func() time.Time

	mu      sync.Mutex
	entries map[telemetryPlanKey]telemetryPlanEntry
}

func newTelemetryPlans(store telemetryAccounts, limiter *peraccount.Limiter) *telemetryPlans {
	return &telemetryPlans{store: store, limiter: limiter, now: time.Now, entries: map[telemetryPlanKey]telemetryPlanEntry{}}
}

// forApp resolves the plan of the app's resource owner. accountID is the
// storage namespace the producer named; it decides when the store cannot read
// apps.
func (p *telemetryPlans) forApp(ctx context.Context, accountID, appID uuid.UUID) (telemetryPlan, error) {
	owners, ok := p.store.(telemetryOwners)
	if !ok || appID == uuid.Nil {
		return p.forAccount(ctx, accountID)
	}
	key := telemetryPlanKey{app: true, id: appID}
	if plan, ok := p.cached(key); ok {
		return plan, nil
	}
	app, err := owners.AppByID(ctx, appID.String())
	if err != nil {
		return telemetryPlan{}, err
	}
	owner, err := state.AppResourceAccount(ctx, owners, app)
	if err != nil {
		return telemetryPlan{}, err
	}
	return p.remember(key, ownerPlan(owner, app.OrgID, accountID)), nil
}

// forAccount resolves an account's own plan.
func (p *telemetryPlans) forAccount(ctx context.Context, accountID uuid.UUID) (telemetryPlan, error) {
	key := telemetryPlanKey{id: accountID}
	if plan, ok := p.cached(key); ok {
		return plan, nil
	}
	acct, err := p.store.AccountByID(ctx, accountID.String())
	if err != nil {
		return telemetryPlan{}, err
	}
	return p.remember(key, ownerPlan(acct, "", accountID)), nil
}

// forOrg resolves the plan for spans a key bound to a company sent: the
// company's once it owns its resources, else the holder's, as
// app_resource_accounts decides for the holder's own apps there.
func (p *telemetryPlans) forOrg(ctx context.Context, orgID, holderID uuid.UUID) (telemetryPlan, error) {
	owners, ok := p.store.(telemetryOwners)
	if !ok || orgID == uuid.Nil {
		return p.forAccount(ctx, holderID)
	}
	key := telemetryPlanKey{org: true, id: orgID, user: holderID}
	if plan, ok := p.cached(key); ok {
		return plan, nil
	}
	org, err := owners.OrgByID(ctx, orgID.String())
	if err != nil {
		return telemetryPlan{}, err
	}
	if org.Personal || !org.ResourceOwnershipEnabled {
		plan, err := p.forAccount(ctx, holderID)
		if err != nil {
			return telemetryPlan{}, err
		}
		plan.Flag.OrgID = org.ID
		return p.remember(key, plan), nil
	}
	owner, err := state.BillingResourceAccount(ctx, owners, org.ID)
	if err != nil {
		return telemetryPlan{}, err
	}
	plan := ownerPlan(owner, org.ID, holderID)
	plan.Flag.AccountID = holderID.String()
	return p.remember(key, plan), nil
}

// ownerPlan builds the plan of a resource projection. fallback keys the
// bucket when the projection names no parseable owner.
func ownerPlan(owner state.Account, orgID string, fallback uuid.UUID) telemetryPlan {
	subject, err := uuid.Parse(owner.ResourceOwnerID())
	if err != nil {
		subject = fallback
	}
	// An unknown plan admits nothing rather than panicking.
	limits, _ := api.LimitsFor(owner.Plan)
	flagAccount := owner.ID
	if flagAccount == "" {
		flagAccount = fallback.String()
	}
	return telemetryPlan{
		Subject: subject,
		Limits:  limits,
		Flag:    platformflags.Subject{AccountID: flagAccount, Plan: string(owner.Plan), OrgID: orgID},
	}
}

func (p *telemetryPlans) cached(key telemetryPlanKey) (telemetryPlan, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.entries[key]
	if !ok || p.now().Sub(entry.at) > telemetryPlanTTL {
		return telemetryPlan{}, false
	}
	return entry.plan, true
}

// remember caches a fresh resolution and hands its capacity to the limiter.
func (p *telemetryPlans) remember(key telemetryPlanKey, plan telemetryPlan) telemetryPlan {
	now := p.now()
	p.mu.Lock()
	if len(p.entries) >= telemetryPlanMaxEntries {
		for k, entry := range p.entries {
			if now.Sub(entry.at) > telemetryPlanTTL {
				delete(p.entries, k)
			}
		}
		if len(p.entries) >= telemetryPlanMaxEntries {
			p.entries = map[telemetryPlanKey]telemetryPlanEntry{}
		}
	}
	p.entries[key] = telemetryPlanEntry{plan: plan, at: now}
	p.mu.Unlock()
	if p.limiter != nil {
		p.limiter.CacheLimits(plan.Subject, plan.Limits)
	}
	return plan
}
