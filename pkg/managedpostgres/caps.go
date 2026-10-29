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

// CapLimits are a usage subject's monthly compute limits in compute-unit
// seconds. Allowance is the plan's; Ceiling is where databases are suspended,
// the allowance or twice it with overage on. A zero Ceiling means uncapped.
type CapLimits struct {
	Allowance int64
	Ceiling   int64
	Overage   bool
}

// CapCeiling returns a usage subject's limits: an account, or an
// organization that owns resources.
type CapCeiling func(ctx context.Context, subjectID string) (CapLimits, error)

// Cap thresholds (ADR-20261025094915 decision 8), recorded once per subject
// and UTC month while caps are enforced.
const (
	CapThresholdWarning        = "warning"
	CapThresholdAllowance      = "allowance"
	CapThresholdCeilingWarning = "ceiling_warning"
	CapThresholdSuspended      = "suspended"
)

// CapEvent is one threshold a subject reached in a month.
type CapEvent struct {
	SubjectID   string
	PeriodStart time.Time
	Threshold   string
	ReachedAt   time.Time
	Used        int64
	Allowance   int64
	Ceiling     int64
	Overage     bool
}

// CapEventStore is an optional UsageStore capability. RecordCapEvent reports
// whether the event is new; a repeat for the same subject, month and
// threshold is a no-op.
type CapEventStore interface {
	RecordCapEvent(ctx context.Context, event CapEvent) (bool, error)
	CapSuspendedAt(ctx context.Context, subjectID string, periodStart time.Time) (*time.Time, error)
}

// capThresholds lists the thresholds used reaches: 80% of the allowance, the
// allowance, 80% of the ceiling when overage raises it, and the ceiling.
func capThresholds(used int64, limits CapLimits, ceiling int64) []string {
	var reached []string
	if limits.Allowance > 0 && used >= limits.Allowance-limits.Allowance/5 {
		reached = append(reached, CapThresholdWarning)
	}
	if limits.Allowance > 0 && used >= limits.Allowance {
		reached = append(reached, CapThresholdAllowance)
	}
	if limits.Overage && ceiling > limits.Allowance && used >= ceiling-ceiling/5 {
		reached = append(reached, CapThresholdCeilingWarning)
	}
	if used >= ceiling {
		reached = append(reached, CapThresholdSuspended)
	}
	return reached
}

type CapEnforcerOptions struct {
	Mode    CapEnforcementMode
	Ceiling CapCeiling
	Now     func() time.Time
	Logger  *slog.Logger
	Observe func(CapSweepSummary)
	// BatchSize pages the catalog; zero uses the usage collector's default.
	BatchSize int
	// Qualification is the staging evidence `on` requires (decision 10).
	Qualification *ComputeCapQualification
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
	EventsRecorded    int
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
		if options.Qualification == nil {
			return nil, fmt.Errorf("%s=on needs a compute cap qualification at %s: %w", CapEnforcementEnv, CapQualificationPathEnv, ErrInvalid)
		}
		if err := registry.VerifyComputeCapQualification(*options.Qualification); err != nil {
			return nil, fmt.Errorf("%s=on: %w", CapEnforcementEnv, err)
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
	accounts, err := e.liveDatabasesBySubject(ctx)
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

// liveDatabasesBySubject groups running databases by the subject the ledger
// pools their usage by: an organization that owns resources pools every
// namespace's databases under its own ID.
func (e *CapEnforcer) liveDatabasesBySubject(ctx context.Context) (map[string][]Database, error) {
	var live []Database
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
			live = append(live, database)
		}
		if len(databases) < e.batchSize {
			break
		}
		last := databases[len(databases)-1]
		after = UsageDatabaseCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	subjects := map[string]string{}
	if store, ok := e.store.(CapSubjectStore); ok && len(live) > 0 {
		ids := make([]string, 0, len(live))
		for _, database := range live {
			ids = append(ids, database.ID)
		}
		var err error
		if subjects, err = store.UsageSubjects(ctx, ids); err != nil {
			return nil, err
		}
	}
	grouped := make(map[string][]Database)
	for _, database := range live {
		subject := subjects[database.ID]
		if subject == "" {
			subject = database.AccountID
		}
		grouped[subject] = append(grouped[subject], database)
	}
	return grouped, nil
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
	limits, err := e.ceiling(ctx, accountID)
	if err != nil {
		return err
	}
	ceiling := limits.Ceiling
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
	if e.mode == CapEnforcementOn {
		if err := e.recordCapEvents(ctx, accountID, now, plan.used, limits, plan.ceiling, summary); err != nil {
			failures = append(failures, err)
		}
	}
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

// recordCapEvents records each threshold the subject reached this month the
// first time a sweep sees it. Shadow mode records nothing: no customer is
// told about a suspension that is not happening.
func (e *CapEnforcer) recordCapEvents(ctx context.Context, subjectID string, now time.Time, used int64, limits CapLimits, ceiling int64, summary *CapSweepSummary) error {
	store, ok := e.store.(CapEventStore)
	if !ok {
		return nil
	}
	for _, threshold := range capThresholds(used, limits, ceiling) {
		recorded, err := store.RecordCapEvent(ctx, CapEvent{
			SubjectID: subjectID, PeriodStart: monthStart(now), Threshold: threshold, ReachedAt: now,
			Used: used, Allowance: max(limits.Allowance, 1), Ceiling: max(ceiling, limits.Allowance, 1), Overage: limits.Overage,
		})
		if err != nil {
			return err
		}
		if recorded {
			summary.EventsRecorded++
			e.logger.Info("managed postgres compute cap threshold reached", "subject_id", subjectID,
				"threshold", threshold, "used_unit_seconds", used, "ceiling_unit_seconds", ceiling, "overage", limits.Overage)
		}
	}
	return nil
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
