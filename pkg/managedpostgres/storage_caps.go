package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// The storage pool (ADR-20261025094915 decision 9). ADR-1430 pools a plan's
// storage per account, but each database may grow to its own spec limit, so an
// account with n databases could store n times its pool. After each usage
// sweep apid works out the per-branch storage limit that keeps the account
// inside its pool; at the pool, writes that would grow a database fail while
// reads, deletes and VACUUM keep working. Nothing is suspended or deleted.

// StorageCapEnforcementEnv selects off, shadow (the default) or on. Shadow
// computes and logs each limit without sending it; on sends it, and needs the
// staging qualification at StorageCapQualificationPathEnv.
const StorageCapEnforcementEnv = "FAAS_MANAGED_POSTGRES_STORAGE_CAP_ENFORCEMENT"

// ParseStorageCapEnforcementMode reads StorageCapEnforcementEnv; empty means
// shadow.
func ParseStorageCapEnforcementMode(value string) (CapEnforcementMode, error) {
	switch mode := CapEnforcementMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return CapEnforcementShadow, nil
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want off, shadow or on: %w", StorageCapEnforcementEnv, value, ErrInvalid)
	}
}

// StorageCapProvider is an optional Provider capability: a storage limit the
// provider applies to every branch of a provider object, failing writes that
// would grow a branch past it.
type StorageCapProvider interface {
	ObserveStorageCap(ctx context.Context, providerResourceID string) (StorageCapObservation, error)
	SetStorageCap(ctx context.Context, providerResourceID string, limitBytes int64) error
}

// StorageCapObservation is one database's size and the limit of the provider
// object it lives in.
type StorageCapObservation struct {
	// Scope identifies the object the limit is set on. A restore inside its
	// source's project shares the source's limit. It is opaque and must
	// never be logged.
	Scope string
	// SizeBytes is the database's current logical size.
	SizeBytes int64
	// LimitBytes is the per-branch limit; zero means none.
	LimitBytes int64
}

type storageCapScope struct {
	resourceID  string
	databaseIDs []string
	sizes       []int64
	current     int64
	// specLimit is the smallest spec storage limit among the scope's
	// databases. A provider limit above a database's spec reads as drift.
	specLimit int64
}

type storageCapDecision struct {
	resourceID  string
	databaseIDs []string
	current     int64
	target      int64
	blocked     int
	apply       bool
}

type storageCapPlan struct {
	pool, used, remaining int64
	decisions             []storageCapDecision
}

func (p storageCapPlan) blocked() bool { return p.remaining == 0 }

// planStorageCaps gives each scope the per-branch limit at which its branches
// together can grow by at most R, what the account has left of its pool. One
// database alone gets its size plus R, as the ADR states it. Between two
// sweeps several scopes can each use R; the next sweep shrinks R, so the
// overshoot never compounds.
func planStorageCaps(pool int64, scopes []storageCapScope) (storageCapPlan, error) {
	if pool <= 0 {
		return storageCapPlan{}, ErrInvalid
	}
	plan := storageCapPlan{pool: pool}
	for _, scope := range scopes {
		if len(scope.sizes) == 0 {
			return storageCapPlan{}, ErrInvalid
		}
		for _, size := range scope.sizes {
			var err error
			if size < 0 {
				return storageCapPlan{}, ErrInvalid
			}
			if plan.used, err = addUsage(plan.used, size); err != nil {
				return storageCapPlan{}, err
			}
		}
	}
	plan.remaining = max(0, pool-plan.used)
	hysteresis := storageCapHysteresis(pool)
	for _, scope := range scopes {
		target := storageWaterLevel(scope.sizes, plan.remaining)
		if scope.specLimit > 0 {
			target = min(target, scope.specLimit)
		}
		// Zero is no limit to the provider; one byte blocks every write.
		target = max(target, 1)
		blocked := storageBlockedBranches(scope.sizes, target)
		plan.decisions = append(plan.decisions, storageCapDecision{
			resourceID: scope.resourceID, databaseIDs: scope.databaseIDs,
			current: scope.current, target: target, blocked: blocked,
			apply: scope.current == 0 || blocked != storageBlockedBranches(scope.sizes, scope.current) ||
				absDiff(target, scope.current) >= hysteresis,
		})
	}
	return plan, nil
}

// storageWaterLevel returns the largest per-branch limit q at which branches
// of these sizes can together grow by at most budget: Σ max(0, q − s_j) ≤ budget.
func storageWaterLevel(sizes []int64, budget int64) int64 {
	sorted := slices.Sorted(slices.Values(sizes))
	level := sorted[0]
	for i := range sorted {
		width := int64(i + 1)
		if i+1 == len(sorted) || sorted[i+1]-level > budget/width {
			return saturatingAdd(level, budget/width)
		}
		budget -= (sorted[i+1] - level) * width
		level = sorted[i+1]
	}
	return level
}

// storageBlockedBranches counts the branches a limit stops from growing.
func storageBlockedBranches(sizes []int64, limit int64) int {
	if limit <= 0 {
		return 0
	}
	blocked := 0
	for _, size := range sizes {
		if size >= limit {
			blocked++
		}
	}
	return blocked
}

// storageCapHysteresis keeps a sweep from rewriting a limit for a small move:
// 1/64 of the pool, at least 1 MiB. Blocking or unblocking a branch always
// writes.
func storageCapHysteresis(pool int64) int64 {
	return max(pool/64, 1<<20)
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// enforceStoragePool runs the storage pass for one subject. Shadow mode logs
// each limit it would send; on sends it. A failure here never affects compute
// caps or their emails: the summary counts it apart.
func (e *CapEnforcer) enforceStoragePool(ctx context.Context, subjectID string, databases []Database, limits CapLimits, summary *CapSweepSummary) error {
	if limits.StoragePool <= 0 {
		summary.StorageUncapped++
		return nil
	}
	scopes, providers, err := e.observeStorageScopes(ctx, databases, summary)
	if err != nil || len(scopes) == 0 {
		return err
	}
	plan, err := planStorageCaps(limits.StoragePool, scopes)
	if err != nil {
		return err
	}
	if plan.blocked() {
		summary.StorageBlockedAccounts++
	}
	var failures []error
	for _, decision := range plan.decisions {
		if !decision.apply {
			summary.StorageScopesUnchanged++
			continue
		}
		attrs := []any{"subject_id", subjectID, "database_ids", decision.databaseIDs,
			"pool_bytes", plan.pool, "subject_used_bytes", plan.used, "limit_bytes", decision.current,
			"target_bytes", decision.target, "blocked_branches", decision.blocked}
		if e.storageMode != CapEnforcementOn {
			summary.StorageScopesShadowed++
			e.logger.Info("managed postgres storage cap (shadow)", attrs...)
			continue
		}
		if err := providers[decision.resourceID].SetStorageCap(ctx, decision.resourceID, decision.target); err != nil {
			failures = append(failures, err)
			continue
		}
		summary.StorageScopesApplied++
		e.logger.Info("managed postgres storage cap applied", attrs...)
	}
	return errors.Join(failures...)
}

// observeStorageScopes reads each database's size and its provider object's
// limit, grouping databases that share one. A subject is skipped, not failed,
// when its provider cannot hold a storage limit.
func (e *CapEnforcer) observeStorageScopes(ctx context.Context, databases []Database, summary *CapSweepSummary) ([]storageCapScope, map[string]StorageCapProvider, error) {
	byScope := make(map[string]*storageCapScope)
	providers := make(map[string]StorageCapProvider)
	var order []string
	for _, database := range databases {
		backend, err := e.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil {
			return nil, nil, err
		}
		provider, ok := backend.Provider.(StorageCapProvider)
		if !ok {
			summary.StorageUnsupported++
			return nil, nil, nil
		}
		observed, err := provider.ObserveStorageCap(ctx, database.ProviderResourceID)
		if err != nil {
			return nil, nil, err
		}
		if observed.Scope == "" || observed.SizeBytes < 0 || observed.LimitBytes < 0 {
			return nil, nil, fmt.Errorf("database %s: invalid storage observation: %w", database.ID, ErrUnavailable)
		}
		scope, ok := byScope[observed.Scope]
		if !ok {
			scope = &storageCapScope{resourceID: database.ProviderResourceID, current: observed.LimitBytes}
			byScope[observed.Scope] = scope
			providers[database.ProviderResourceID] = provider
			order = append(order, observed.Scope)
		}
		scope.databaseIDs = append(scope.databaseIDs, database.ID)
		scope.sizes = append(scope.sizes, observed.SizeBytes)
		if spec := database.Spec.StorageLimitBytes; spec > 0 && (scope.specLimit == 0 || spec < scope.specLimit) {
			scope.specLimit = spec
		}
	}
	scopes := make([]storageCapScope, 0, len(order))
	for _, key := range order {
		scopes = append(scopes, *byScope[key])
	}
	return scopes, providers, nil
}
