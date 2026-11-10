package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

// CapsEnforced reports whether both the compute and the storage cap are on.
// A malformed mode counts as not enforced. Until both are on, the production
// release holds managed PostgreSQL to accounts a flag rule names
// (ADR-20261031091607).
func CapsEnforced(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	compute, err := ParseCapEnforcementMode(getenv(CapEnforcementEnv))
	if err != nil || compute != CapEnforcementOn {
		return false
	}
	storage, err := ParseStorageCapEnforcementMode(getenv(StorageCapEnforcementEnv))
	return err == nil && storage == CapEnforcementOn
}

// ComputeCapProvider is an optional Provider capability: a monthly compute
// quota held on a running database, which the provider enforces itself by
// suspending the database's computes. Quotas are compute-unit seconds.
type ComputeCapProvider interface {
	ObserveComputeCap(ctx context.Context, providerResourceID string) (ComputeCapObservation, error)
	SetComputeCap(ctx context.Context, providerResourceID string, quotaUnitSeconds int64) error
}

// ComputeUseReporter is implemented by a provider whose quota counters trail
// use while its consumption report is current. ComputeUnitSecondsSince
// returns what the resource's quota scope used from from, a ledger window
// boundary, until now, the open window included. The sweep then counts use as
// the ledger plus each scope's use since the ledger's coverage, not the
// counters (ADR-20261025094915 amendment 2026-11-05).
type ComputeUseReporter interface {
	ComputeUnitSecondsSince(ctx context.Context, providerResourceID string, from time.Time) (int64, error)
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
	// used is the provider's quota counter, which the quota is written
	// against.
	used  int64
	quota int64
	// reported marks a scope whose provider reports use since the ledger's
	// coverage; unreported is that use.
	reported   bool
	unreported int64
	// preview is set when every database in the scope is a PR preview
	// database (ADR-20261102123523).
	preview bool
}

type computeCapDecision struct {
	resourceID  string
	databaseIDs []string
	used        int64
	current     int64
	target      int64
	apply       bool
	preview     bool
}

type computeCapPlan struct {
	ceiling, used, remaining int64
	decisions                []computeCapDecision
}

func (p computeCapPlan) suspended() bool { return p.remaining == 0 }

// planComputeCaps gives each scope the quota u_i + R, where u_i is its
// provider counter and R is what the account has left of its ceiling. When
// every scope's provider reports use, the account's use is the ledger (which
// still counts databases deleted this month) plus each scope's use since the
// ledger's coverage (ADR-20261025094915 amendment 2026-11-05). Otherwise the
// counters are taken as current, and use is the larger of the ledger and
// their sum (decision 5).
//
// A scope of PR preview databases only gets what the account has left of 80%
// of its ceiling (ADR-20261102123523 decision 6): previews are held once the
// account reaches it, and production keeps the last fifth. The hold lifts
// with the next month's counters, or when overage or an upgrade raises the
// ceiling.
func planComputeCaps(ceiling, ledgerUsed int64, scopes []computeCapScope) (computeCapPlan, error) {
	if ceiling <= 0 || ledgerUsed < 0 {
		return computeCapPlan{}, ErrInvalid
	}
	var counters int64
	reported, unreported := len(scopes) > 0, ledgerUsed
	for _, scope := range scopes {
		var err error
		if counters, err = addUsage(counters, scope.used); err != nil {
			return computeCapPlan{}, err
		}
		if unreported, err = addUsage(unreported, scope.unreported); err != nil {
			return computeCapPlan{}, err
		}
		reported = reported && scope.reported
	}
	plan := computeCapPlan{ceiling: ceiling, used: max(ledgerUsed, counters)}
	if reported {
		plan.used = unreported
	}
	plan.remaining = max(0, ceiling-plan.used)
	previewRemaining := max(0, previewComputeCeiling(ceiling)-plan.used)
	for _, scope := range scopes {
		remaining := plan.remaining
		if scope.preview {
			remaining = previewRemaining
		}
		target, err := addUsage(scope.used, remaining)
		if err != nil {
			return computeCapPlan{}, err
		}
		// A zero quota means unlimited to the provider; one second suspends.
		target = max(target, 1)
		plan.decisions = append(plan.decisions, computeCapDecision{
			resourceID: scope.resourceID, databaseIDs: scope.databaseIDs,
			used: scope.used, current: scope.quota, target: target,
			apply: capQuotaNeedsWrite(scope.quota, target, scope.used), preview: scope.preview,
		})
	}
	return plan, nil
}

// previewComputeCeiling is 80% of ceiling, the share PR preview databases
// may run to.
func previewComputeCeiling(ceiling int64) int64 { return ceiling - ceiling/5 }

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
// StoragePool is the subject's pooled storage in bytes (decision 9), twice
// the plan's with overage on; zero means uncapped.
type CapLimits struct {
	Allowance   int64
	Ceiling     int64
	Overage     bool
	StoragePool int64

	// StorageAllowance is the plan's pool in bytes, before overage doubles
	// it: storage past it is charged (ADR-20261031110037 amendment 2).
	StorageAllowance int64

	// Entitlement is the subject's plan: databases it no longer includes
	// are suspended (ADR-20261102125208). A zero database limit means the
	// plan is unknown, and nothing is suspended for it.
	Entitlement api.ManagedPostgresPlanLimits

	// Inactive marks a subject that may not run workloads: suspended,
	// abuse-held or pending deletion (ADR-20261025094915 amendment
	// 2026-11-03). Its computes are suspended whatever its ceiling, as its
	// apps are parked; data is kept, and the next sweep after reactivation
	// lifts the suspension. No cap threshold is recorded or mailed for it.
	Inactive bool
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
	// Mailed is read only: an email is linked to the event.
	Mailed bool
}

// CapEventStore is an optional UsageStore capability. RecordCapEvent reports
// whether the event is new; a repeat for the same subject, month and
// threshold is a no-op. MostSevereCapEvent returns the subject's most severe
// threshold of the month, or nil.
type CapEventStore interface {
	RecordCapEvent(ctx context.Context, event CapEvent) (bool, error)
	CapSuspendedAt(ctx context.Context, subjectID string, periodStart time.Time) (*time.Time, error)
	MostSevereCapEvent(ctx context.Context, subjectID string, periodStart time.Time) (*CapEvent, error)
}

// capMailWindow is how long after a threshold is reached its email may still
// be queued; it matches api.InternalMailManagedPostgresCapTTL.
const capMailWindow = 24 * time.Hour

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
	Mode CapEnforcementMode
	// StorageMode runs the storage pool pass (decision 9): off, shadow or on.
	StorageMode CapEnforcementMode
	// StorageQualification is the staging evidence storage `on` requires.
	StorageQualification *StorageCapQualification
	Ceiling              CapCeiling
	Now                  func() time.Time
	Logger               *slog.Logger
	Observe              func(CapSweepSummary)
	// BatchSize pages the catalog; zero uses the usage collector's default.
	BatchSize int
	// Qualification is the staging evidence `on` requires (decision 10).
	Qualification *ComputeCapQualification
	// Notify queues the email for a subject's most severe threshold of the
	// month (decision 8). The sweep calls it only in mode on, after the
	// subject's quotas are written, and again on later sweeps until the
	// threshold is mailed or a day has passed. Nil sends no email.
	Notify func(ctx context.Context, event CapEvent) error
	// PlanExcessMode suspends databases past the subject's plan
	// (ADR-20261102125208): off (the zero value), shadow or on. On writes
	// only while Mode is on too.
	PlanExcessMode CapEnforcementMode
	// NotifyPlanExcess queues the email for a database the sweep suspended
	// past its plan (ADR-20261102125208 step 3). It is retried each sweep
	// until it links an email to the notice or the notice is a day old, so
	// it must mail a notice at most once. Nil sends no email.
	NotifyPlanExcess func(ctx context.Context, notice PlanExcessNotice) error
	// Egress returns a subject's plan egress allowance and what its apps
	// already sent this UTC month (ADR-20261110160000). Database egress
	// counts toward the same allowance: once the two reach it, the sweep
	// holds every database of the subject until the month ends, the plan
	// grows or egress overage lifts the ceiling. Nil leaves egress unchecked.
	Egress EgressAllowanceFunc
}

// EgressAllowance is a subject's monthly egress allowance shared by its apps
// and databases (ADR-20261110160000). CeilingBytes zero means no ceiling: the
// subject is gone or its plan has no egress allowance.
type EgressAllowance struct {
	CeilingBytes   int64
	AppEgressBytes int64
}

// EgressAllowanceFunc reads a subject's EgressAllowance at now.
type EgressAllowanceFunc func(ctx context.Context, subjectID string, now time.Time) (EgressAllowance, error)

// uncappedHold is the hold reason for a subject without a compute ceiling:
// egress, once its databases and apps spent the egress allowance, else none.
func (e *CapEnforcer) uncappedHold(ctx context.Context, subjectID string) (string, error) {
	if e.egress == nil {
		return capHoldNone, nil
	}
	now := e.now().UTC()
	snapshot, err := e.store.UsageSnapshot(ctx, subjectID, now)
	if err != nil {
		return capHoldNone, err
	}
	spent, err := e.egressSpent(ctx, subjectID, snapshot, now)
	if err != nil || !spent {
		return capHoldNone, err
	}
	return capHoldEgress, nil
}

// egressSpent reports whether the subject's databases and apps together sent
// its whole egress allowance this month. A read failure is the subject's
// failure for this sweep; the previous hold stays as it is.
func (e *CapEnforcer) egressSpent(ctx context.Context, subjectID string, snapshot UsageSnapshot, now time.Time) (bool, error) {
	if e.egress == nil {
		return false, nil
	}
	allowance, err := e.egress(ctx, subjectID, now)
	if err != nil || allowance.CeilingBytes <= 0 {
		return false, err
	}
	databaseEgress := max(snapshot.EgressBytes, 0)
	appEgress := max(allowance.AppEgressBytes, 0)
	if databaseEgress > math.MaxInt64-appEgress {
		return true, nil
	}
	return databaseEgress+appEgress >= allowance.CeilingBytes, nil
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
	NotifyFailed      int
	CompletedAt       time.Time
	// The storage pool pass, counted apart so its failures never mark the
	// compute sweep stale.
	StorageMode            CapEnforcementMode
	StorageAccounts        int
	StorageBlockedAccounts int
	StorageUncapped        int
	StorageUnsupported     int
	StorageFailed          int
	StorageScopesUnchanged int
	StorageScopesShadowed  int
	StorageScopesApplied   int

	// Databases past their subject's plan (ADR-20261102125208). Shared
	// counts scopes left running because a database inside the plan shares
	// them.
	PlanExcessMode            CapEnforcementMode
	PlanExcessDatabases       int
	PlanExcessScopesSuspended int
	PlanExcessScopesShadowed  int
	PlanExcessScopesShared    int
	PlanExcessNoticed         int
	PlanExcessNotifyFailed    int

	// Compute holds (ADR-20261025094915 amendment 2026-11-05): databases
	// held after the sweep, and the holds it placed, released or only
	// logged in shadow mode.
	HeldDatabases int
	HoldsApplied  int
	HoldsReleased int
	HoldsShadowed int
}

// CapEnforcer applies compute ceilings to running databases.
type CapEnforcer struct {
	registry    *Registry
	store       UsageStore
	mode        CapEnforcementMode
	storageMode CapEnforcementMode
	ceiling     CapCeiling
	now         func() time.Time
	logger      *slog.Logger
	observe     func(CapSweepSummary)
	notify      func(context.Context, CapEvent) error
	batchSize   int
	planExcess  CapEnforcementMode
	planNotify  func(context.Context, PlanExcessNotice) error
	egress      EgressAllowanceFunc
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
	switch options.StorageMode {
	case "":
		options.StorageMode = CapEnforcementOff
	case CapEnforcementOff, CapEnforcementShadow:
	case CapEnforcementOn:
		if options.StorageQualification == nil {
			return nil, fmt.Errorf("%s=on needs a storage cap qualification at %s: %w", StorageCapEnforcementEnv, StorageCapQualificationPathEnv, ErrInvalid)
		}
		if err := registry.VerifyStorageCapQualification(*options.StorageQualification); err != nil {
			return nil, fmt.Errorf("%s=on: %w", StorageCapEnforcementEnv, err)
		}
	default:
		return nil, ErrInvalid
	}
	switch options.PlanExcessMode {
	case "":
		options.PlanExcessMode = CapEnforcementOff
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
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
		registry: registry, store: store, mode: options.Mode, storageMode: options.StorageMode, ceiling: options.Ceiling,
		now: options.Now, logger: options.Logger, observe: options.Observe, notify: options.Notify,
		batchSize: options.BatchSize, planExcess: options.PlanExcessMode, planNotify: options.NotifyPlanExcess,
		egress: options.Egress,
	}, nil
}

// Enforce runs one sweep. A failure for one account never stops the others;
// the returned error joins them.
func (e *CapEnforcer) Enforce(ctx context.Context) (summary CapSweepSummary, err error) {
	ctx = WithBackgroundRequests(ctx)
	summary.Mode, summary.StorageMode, summary.PlanExcessMode = e.mode, e.storageMode, e.planExcess
	defer func() {
		summary.CompletedAt = e.now().UTC()
		if e.observe != nil {
			e.observe(summary)
		}
	}()
	if e.mode == CapEnforcementOff && e.storageMode == CapEnforcementOff {
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
		limits, limitsErr := e.ceiling(ctx, accountID)
		if e.mode != CapEnforcementOff {
			err := limitsErr
			summary.Accounts++
			if err == nil {
				err = e.enforceAccount(ctx, accountID, accounts[accountID], limits, &summary)
			}
			if err != nil {
				summary.Failed++
				failures = append(failures, fmt.Errorf("account %s: %w", accountID, err))
			}
		}
		if e.storageMode != CapEnforcementOff {
			// The two guards share entitlements, but a failed compute read or
			// write must not prevent an independent storage limit update.
			err := limitsErr
			summary.StorageAccounts++
			if err == nil {
				err = e.enforceStoragePool(ctx, accountID, accounts[accountID], limits, &summary)
			}
			if err != nil {
				summary.StorageFailed++
				e.logger.Warn("managed postgres storage cap sweep failed", "subject_id", accountID, "error", err)
			}
		}
	}
	e.mailPlanExcessNotices(ctx, &summary)
	return summary, errors.Join(failures...)
}

// liveDatabasesBySubject groups running databases and retained source storage
// by the ledger's usage subject. Organization ownership pools every namespace's
// databases under its own ID.
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
			if seen[database.ID] {
				continue
			}
			seen[database.ID] = true
			included, err := e.includeCapDatabase(ctx, database)
			if err != nil {
				return nil, err
			}
			if !included {
				continue
			}
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

// A deleted source kept for its copies still holds storage. Include it in
// storage planning even though its endpoint must stay out of compute work.
func (e *CapEnforcer) includeCapDatabase(ctx context.Context, database Database) (bool, error) {
	if capEnforceable(database) {
		return true, nil
	}
	if database.State != StateDeleting || database.ProviderResourceID == "" || database.DeletedAt != nil {
		return false, nil
	}
	store, ok := e.store.(RestoreCopiesStore)
	if !ok {
		return false, nil
	}
	return store.HasRestoreCopies(ctx, database.ID)
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

func (e *CapEnforcer) enforceAccount(ctx context.Context, accountID string, databases []Database, limits CapLimits, summary *CapSweepSummary) error {
	ceiling := limits.Ceiling
	switch {
	case limits.Inactive:
		// One compute-unit second: nothing remains once any is used, and a
		// quiet database is held to a single second.
		ceiling = 1
	case ceiling <= 0:
		summary.Uncapped++
		// An uncapped subject has no compute to stop, but its egress
		// allowance still binds; otherwise a hold left from before is
		// released.
		reason, err := e.uncappedHold(ctx, accountID)
		if err != nil {
			return err
		}
		return e.holdDatabases(ctx, accountID, databases, reason, nil, summary)
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
	if err := e.readUnreportedUse(ctx, databases, snapshot, now, scopes, providers); err != nil {
		return err
	}
	plan, err := planComputeCaps(ceiling, snapshot.ComputeUnitSeconds, scopes)
	if err != nil {
		return err
	}
	if plan.suspended() {
		summary.SuspendedAccounts++
	}
	suspended := e.suspendPlanExcess(accountID, databases, limits, &plan, summary)
	var failures []error
	if e.mode == CapEnforcementOn && !limits.Inactive {
		if err := e.recordCapEvents(ctx, accountID, now, plan, limits, summary); err != nil {
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
			"target_unit_seconds", decision.target, "suspend", decision.target <= decision.used, "preview", decision.preview}
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
	// The hold is what stops a database; the quota is a backstop, so a
	// failed quota write does not skip it.
	all := capHoldNone
	egressSpent, egressErr := e.egressSpent(ctx, accountID, snapshot, now)
	if egressErr != nil {
		return errors.Join(append(failures, egressErr)...)
	}
	switch {
	case limits.Inactive:
		all = capHoldInactive
	case plan.suspended():
		all = capHoldCeiling
	case egressSpent:
		all = capHoldEgress
	}
	if err := e.holdDatabases(ctx, accountID, databases, all, capHeldDatabases(databases, plan, suspended), summary); err != nil {
		failures = append(failures, err)
	}
	if e.mode == CapEnforcementOn && len(failures) == 0 && !limits.Inactive {
		e.notifyCapEvent(ctx, accountID, now, summary)
	}
	if e.planExcessEnforced() && len(failures) == 0 {
		e.recordPlanExcessNotices(ctx, databases, suspended, now, summary)
	}
	return errors.Join(failures...)
}

// notifyCapEvent mails the subject's most severe threshold of the month once:
// a sweep that jumps past several thresholds sends one email. A failed email
// does not fail the sweep; the next sweep retries it.
func (e *CapEnforcer) notifyCapEvent(ctx context.Context, subjectID string, now time.Time, summary *CapSweepSummary) {
	store, ok := e.store.(CapEventStore)
	if !ok || e.notify == nil {
		return
	}
	event, err := store.MostSevereCapEvent(ctx, subjectID, now)
	if err == nil && (event == nil || event.Mailed || now.Sub(event.ReachedAt) >= capMailWindow) {
		return
	}
	if err == nil {
		err = e.notify(ctx, *event)
	}
	if err != nil {
		summary.NotifyFailed++
		e.logger.Warn("managed postgres compute cap email not queued", "subject_id", subjectID, "error", err)
	}
}

// recordCapEvents records each threshold the subject reached this month the
// first time a sweep sees it. Shadow mode records nothing: no customer is
// told about a suspension that is not happening.
func (e *CapEnforcer) recordCapEvents(ctx context.Context, subjectID string, now time.Time, plan computeCapPlan, limits CapLimits, summary *CapSweepSummary) error {
	store, ok := e.store.(CapEventStore)
	if !ok {
		return nil
	}
	used, ceiling := plan.used, plan.ceiling
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
		if !capEnforceable(database) {
			continue
		}
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
			scope = &computeCapScope{resourceID: database.ProviderResourceID, used: observed.UsedUnitSeconds, quota: observed.QuotaUnitSeconds, preview: true}
			byScope[observed.Scope] = scope
			providers[database.ProviderResourceID] = provider
			order = append(order, observed.Scope)
		}
		scope.databaseIDs = append(scope.databaseIDs, database.ID)
		// A scope shared with a production database (a copy branched in its
		// source's project) keeps production's quota.
		scope.preview = scope.preview && !database.Preview.IsZero()
		_, scope.reported = provider.(ComputeUseReporter)
	}
	scopes := make([]computeCapScope, 0, len(order))
	for _, key := range order {
		scopes = append(scopes, *byScope[key])
	}
	return scopes, providers, nil
}

// readUnreportedUse asks each reporting scope's provider for its use since
// the ledger's coverage of it (ADR-20261025094915 amendment 2026-11-05). A
// scope's coverage is the furthest any of its databases is collected: a
// restore inside its source's project resolves to the source's coverage. A
// scope the ledger has not collected this month reads from the month start,
// or from the hour its first database was created.
func (e *CapEnforcer) readUnreportedUse(ctx context.Context, databases []Database, snapshot UsageSnapshot, now time.Time, scopes []computeCapScope, providers map[string]ComputeCapProvider) error {
	collected := make(map[string]time.Time, len(snapshot.Databases))
	for _, progress := range snapshot.Databases {
		if progress.DatabaseID != "" && progress.CollectedUntil.After(collected[progress.DatabaseID]) {
			collected[progress.DatabaseID] = progress.CollectedUntil
		}
	}
	created := make(map[string]time.Time, len(databases))
	for _, database := range databases {
		created[database.ID] = database.CreatedAt
	}
	month := monthStart(now)
	for i := range scopes {
		scope := &scopes[i]
		if !scope.reported {
			continue
		}
		var from, first time.Time
		for _, id := range scope.databaseIDs {
			if collected[id].After(from) {
				from = collected[id]
			}
			if at := created[id]; !at.IsZero() && (first.IsZero() || at.Before(first)) {
				first = at
			}
		}
		if from.IsZero() {
			from = first.UTC().Truncate(time.Hour)
		}
		if from.Before(month) {
			from = month
		}
		if open := now.UTC().Truncate(time.Hour); from.After(open) {
			from = open
		}
		used, err := providers[scope.resourceID].(ComputeUseReporter).ComputeUnitSecondsSince(ctx, scope.resourceID, from.Truncate(time.Hour))
		if err != nil {
			return err
		}
		scope.unreported = used
	}
	return nil
}
