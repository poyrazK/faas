package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Compute caps on running databases (ADR-20261025094915). After each usage
// sweep, apid gives every database's provider project a monthly compute quota
// of its own use plus what its account has left, so the provider suspends a
// database the moment the account reaches its ceiling. A cap never deletes.

// CapEnforcementEnv selects off, shadow (the default) or on.
const CapEnforcementEnv = "FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT"

type CapEnforcementMode string

const (
	CapEnforcementOff    CapEnforcementMode = "off"
	CapEnforcementShadow CapEnforcementMode = "shadow"
	CapEnforcementOn     CapEnforcementMode = "on"
)

// ParseCapEnforcementMode reads CapEnforcementEnv; empty means shadow.
func ParseCapEnforcementMode(value string) (CapEnforcementMode, error) {
	switch mode := CapEnforcementMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return CapEnforcementShadow, nil
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want off, shadow or on: %w", CapEnforcementEnv, value, ErrInvalid)
	}
}

// ComputeCapProvider is an optional Provider capability: a monthly compute
// quota held on a running database, which the provider enforces itself by
// suspending the database's computes. Quotas are compute-unit seconds.
type ComputeCapProvider interface {
	ObserveComputeCap(ctx context.Context, providerResourceID string) (ComputeCapObservation, error)
	SetComputeCap(ctx context.Context, providerResourceID string, quotaUnitSeconds int64) error
}

// ComputeCapObservation is one provider object's quota state for the
// provider's current consumption period.
type ComputeCapObservation struct {
	// Scope identifies the object the quota applies to. Databases sharing a
	// scope (a restore inside its source's project) share one counter and one
	// quota. It is opaque and must never be logged.
	Scope            string
	PeriodStart      time.Time
	UsedUnitSeconds  int64
	QuotaUnitSeconds int64 // zero means no quota
}

// capQuotaHysteresisUnitSeconds (0.1 CU-hour) keeps a sweep from rewriting a
// quota for a small move. Lifting or imposing a suspension always writes.
const capQuotaHysteresisUnitSeconds int64 = 360

// capPeriodTolerance is how far the provider's period start may sit from the
// UTC month start before the account is skipped rather than enforced.
const capPeriodTolerance = time.Hour

type computeCapScope struct {
	resourceID  string
	databaseIDs []string
	used        int64
	quota       int64
}

type computeCapDecision struct {
	resourceID  string
	databaseIDs []string
	used        int64
	current     int64
	target      int64
	apply       bool
}

type computeCapPlan struct {
	ceiling, used, remaining int64
	decisions                []computeCapDecision
}

func (p computeCapPlan) suspended() bool { return p.remaining == 0 }

// planComputeCaps gives each scope the quota u_i + R, where R is what the
// account has left of its ceiling. The account's use is the larger of the
// ledger (which still counts databases deleted this month) and the live
// provider counters (which have no ledger lag).
func planComputeCaps(ceiling, ledgerUsed int64, scopes []computeCapScope) (computeCapPlan, error) {
	if ceiling <= 0 || ledgerUsed < 0 {
		return computeCapPlan{}, ErrInvalid
	}
	var live int64
	for _, scope := range scopes {
		var err error
		if live, err = addUsage(live, scope.used); err != nil {
			return computeCapPlan{}, err
		}
	}
	plan := computeCapPlan{ceiling: ceiling, used: max(ledgerUsed, live)}
	plan.remaining = max(0, ceiling-plan.used)
	for _, scope := range scopes {
		target, err := addUsage(scope.used, plan.remaining)
		if err != nil {
			return computeCapPlan{}, err
		}
		// A zero quota means unlimited to the provider; one second suspends.
		target = max(target, 1)
		plan.decisions = append(plan.decisions, computeCapDecision{
			resourceID: scope.resourceID, databaseIDs: scope.databaseIDs,
			used: scope.used, current: scope.quota, target: target,
			apply: capQuotaNeedsWrite(scope.quota, target, scope.used),
		})
	}
	return plan, nil
}

func capQuotaNeedsWrite(current, target, used int64) bool {
	switch {
	case current == 0:
		return true
	case target <= used || current <= used:
		// Imposing or lifting a suspension.
		return target != current
	case target < current:
		return current-target >= capQuotaHysteresisUnitSeconds
	default:
		return target-current >= capQuotaHysteresisUnitSeconds
	}
}

// CapCeiling returns an account's monthly compute ceiling in compute-unit
// seconds. Zero means the account is not capped.
type CapCeiling func(ctx context.Context, accountID string) (int64, error)

type CapEnforcerOptions struct {
	Mode    CapEnforcementMode
	Ceiling CapCeiling
	Now     func() time.Time
	Logger  *slog.Logger
	Observe func(CapSweepSummary)
	// BatchSize pages the catalog; zero uses the usage collector's default.
	BatchSize int
}

// CapSweepSummary counts one enforcement sweep. Scopes are provider quota
// objects; a restore that shares its source's project is one scope.
type CapSweepSummary struct {
	Mode              CapEnforcementMode
	Accounts          int
	SuspendedAccounts int
	Uncapped          int
	PeriodMismatch    int
	Unsupported       int
	Failed            int
	ScopesUnchanged   int
	ScopesApplied     int
	ScopesShadowed    int
	CompletedAt       time.Time
}

// CapEnforcer applies compute ceilings to running databases.
type CapEnforcer struct {
	registry  *Registry
	store     UsageStore
	mode      CapEnforcementMode
	ceiling   CapCeiling
	now       func() time.Time
	logger    *slog.Logger
	observe   func(CapSweepSummary)
	batchSize int
}

func NewCapEnforcer(registry *Registry, store UsageStore, options CapEnforcerOptions) (*CapEnforcer, error) {
	if registry == nil || store == nil || options.Ceiling == nil {
		return nil, ErrInvalid
	}
	switch options.Mode {
	case CapEnforcementOff, CapEnforcementShadow:
	case CapEnforcementOn:
		// Enforcement reads the ledger for databases deleted this month.
		if !registry.UsagePolicy().Enabled {
			return nil, fmt.Errorf("%s=on needs the usage ledger enabled: %w", CapEnforcementEnv, ErrInvalid)
		}
	default:
		return nil, ErrInvalid
	}
	if options.BatchSize == 0 {
		options.BatchSize = defaultUsageBatchSize
	}
	if options.BatchSize < 1 || options.BatchSize > 100 {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &CapEnforcer{
		registry: registry, store: store, mode: options.Mode, ceiling: options.Ceiling,
		now: options.Now, logger: options.Logger, observe: options.Observe, batchSize: options.BatchSize,
	}, nil
}

// Enforce runs one sweep. A failure for one account never stops the others;
// the returned error joins them.
func (e *CapEnforcer) Enforce(ctx context.Context) (summary CapSweepSummary, err error) {
	summary.Mode = e.mode
	defer func() {
		summary.CompletedAt = e.now().UTC()
		if e.observe != nil {
			e.observe(summary)
		}
	}()
	if e.mode == CapEnforcementOff {
		return summary, nil
	}
	accounts, err := e.liveDatabasesByAccount(ctx)
	if err != nil {
		return summary, err
	}
	ids := make([]string, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var failures []error
	for _, accountID := range ids {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		summary.Accounts++
		if err := e.enforceAccount(ctx, accountID, accounts[accountID], &summary); err != nil {
			summary.Failed++
			failures = append(failures, fmt.Errorf("account %s: %w", accountID, err))
		}
	}
	return summary, errors.Join(failures...)
}

func (e *CapEnforcer) liveDatabasesByAccount(ctx context.Context) (map[string][]Database, error) {
	accounts := make(map[string][]Database)
	seen := make(map[string]bool)
	var after UsageDatabaseCursor
	for {
		databases, err := e.store.ListUsageDatabases(ctx, after, e.batchSize)
		if err != nil {
			return nil, err
		}
		for _, database := range databases {
			if seen[database.ID] || !capEnforceable(database) {
				continue
			}
			seen[database.ID] = true
			accounts[database.AccountID] = append(accounts[database.AccountID], database)
		}
		if len(databases) < e.batchSize {
			return accounts, nil
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
}

// capEnforceable keeps databases whose provider resource can be running.
func capEnforceable(database Database) bool {
	if database.ProviderResourceID == "" || database.DeletedAt != nil {
		return false
	}
	switch database.State {
	case StateReady, StateUpdating, StateProvisioning, StateFailed:
		return true
	default:
		return false
	}
}

func (e *CapEnforcer) enforceAccount(ctx context.Context, accountID string, databases []Database, summary *CapSweepSummary) error {
	ceiling, err := e.ceiling(ctx, accountID)
	if err != nil {
		return err
	}
	if ceiling <= 0 {
		summary.Uncapped++
		return nil
	}
	if policy := e.registry.UsagePolicy(); policy.Enabled && policy.MaxMonthlyComputeUnitSeconds > 0 {
		ceiling = min(ceiling, policy.MaxMonthlyComputeUnitSeconds)
	}
	now := e.now().UTC()
	scopes, providers, err := e.observeScopes(ctx, databases, now, summary)
	if err != nil || len(scopes) == 0 {
		return err
	}
	snapshot, err := e.store.UsageSnapshot(ctx, accountID, now)
	if err != nil {
		return err
	}
	plan, err := planComputeCaps(ceiling, snapshot.ComputeUnitSeconds, scopes)
	if err != nil {
		return err
	}
	if plan.suspended() {
		summary.SuspendedAccounts++
	}
	var failures []error
	for _, decision := range plan.decisions {
		if !decision.apply {
			summary.ScopesUnchanged++
			continue
		}
		attrs := []any{"account_id", accountID, "database_ids", decision.databaseIDs,
			"ceiling_unit_seconds", plan.ceiling, "account_used_unit_seconds", plan.used,
			"used_unit_seconds", decision.used, "quota_unit_seconds", decision.current,
			"target_unit_seconds", decision.target, "suspend", decision.target <= decision.used}
		if e.mode == CapEnforcementShadow {
			summary.ScopesShadowed++
			e.logger.Info("managed postgres compute cap (shadow)", attrs...)
			continue
		}
		if err := providers[decision.resourceID].SetComputeCap(ctx, decision.resourceID, decision.target); err != nil {
			failures = append(failures, err)
			continue
		}
		summary.ScopesApplied++
		e.logger.Info("managed postgres compute cap applied", attrs...)
	}
	return errors.Join(failures...)
}

// observeScopes reads each database's provider quota and groups databases
// that share one. An account is skipped, not failed, when its provider cannot
// hold a quota or measures a different period than the ledger's UTC month.
func (e *CapEnforcer) observeScopes(ctx context.Context, databases []Database, now time.Time, summary *CapSweepSummary) ([]computeCapScope, map[string]ComputeCapProvider, error) {
	byScope := make(map[string]*computeCapScope)
	providers := make(map[string]ComputeCapProvider)
	var order []string
	month := monthStart(now)
	for _, database := range databases {
		backend, err := e.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil {
			return nil, nil, err
		}
		provider, ok := backend.Provider.(ComputeCapProvider)
		if !ok {
			summary.Unsupported++
			return nil, nil, nil
		}
		observed, err := provider.ObserveComputeCap(ctx, database.ProviderResourceID)
		if err != nil {
			return nil, nil, err
		}
		if gap := observed.PeriodStart.Sub(month); gap > capPeriodTolerance || gap < -capPeriodTolerance {
			summary.PeriodMismatch++
			return nil, nil, nil
		}
		scope, ok := byScope[observed.Scope]
		if !ok {
			scope = &computeCapScope{resourceID: database.ProviderResourceID, used: observed.UsedUnitSeconds, quota: observed.QuotaUnitSeconds}
			byScope[observed.Scope] = scope
			providers[database.ProviderResourceID] = provider
			order = append(order, observed.Scope)
		}
		scope.databaseIDs = append(scope.databaseIDs, database.ID)
	}
	scopes := make([]computeCapScope, 0, len(order))
	for _, key := range order {
		scopes = append(scopes, *byScope[key])
	}
	return scopes, providers, nil
}
