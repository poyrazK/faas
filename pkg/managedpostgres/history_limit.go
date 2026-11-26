package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The restore history limit (ADR-20261123062111 D2). Restore history cannot
// be refused at write time, so a subject with managed PostgreSQL overage off
// still costs the platform the history its writes leave behind. Once the
// subject's month-to-date restore history and snapshots pass
// api.ManagedPostgresHistoryLimitFactor times its included history, the cap
// sweep shortens the restore window of its databases to
// api.ManagedPostgresHistoryLimitedRestoreWindow. The provider drops history
// older than that window, so the cost stops growing; writes, reads and data
// are untouched. The next month's usage starts below the limit, and overage
// on never limits, so either one gives the spec's window back on the next
// sweep. History dropped while limited stays dropped.

// HistoryLimitEnv selects off, shadow (the default) or on. Shadow works out
// and logs each window it would set without sending it.
const HistoryLimitEnv = "FAAS_MANAGED_POSTGRES_HISTORY_LIMIT"

// ParseHistoryLimitMode reads HistoryLimitEnv; empty means shadow.
func ParseHistoryLimitMode(value string) (CapEnforcementMode, error) {
	switch mode := CapEnforcementMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		return CapEnforcementShadow, nil
	case CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn:
		return mode, nil
	default:
		return "", fmt.Errorf("%s=%q: want off, shadow or on: %w", HistoryLimitEnv, value, ErrInvalid)
	}
}

// HistoryRetentionProvider is an optional Provider capability: the restore
// window a provider object keeps history for, which every branch inside it
// shares.
type HistoryRetentionProvider interface {
	ObserveHistoryRetention(ctx context.Context, providerResourceID string) (HistoryRetentionObservation, error)
	// SetHistoryRetention writes the window and confirms it by reading it
	// back. A positive window only: zero would turn point-in-time restore
	// off.
	SetHistoryRetention(ctx context.Context, providerResourceID string, seconds int64) error
}

// HistoryRetentionObservation is the restore window of the provider object
// a database lives in.
type HistoryRetentionObservation struct {
	// Scope identifies the provider object; a restore inside its source's
	// project shares the source's window. It is opaque and must never be
	// logged.
	Scope   string
	Seconds int64
}

// historyLimitedSeconds is api.ManagedPostgresHistoryLimitedRestoreWindow in
// the seconds a spec and a provider carry.
var historyLimitedSeconds = int64(api.ManagedPostgresHistoryLimitedRestoreWindow / time.Second)

// HistoryLimitByteSeconds is a subject's history limit for the month:
// api.ManagedPostgresHistoryLimitFactor times its included history, held for
// the provider's 744-hour month. Zero means no limit.
func HistoryLimitByteSeconds(storageAllowance int64) int64 {
	included := poolByteSeconds(api.ManagedPostgresIncludedHistoryBytes(storageAllowance), api.ManagedPostgresHistoryMonthSeconds)
	if included <= 0 {
		return 0
	}
	if included > math.MaxInt64/api.ManagedPostgresHistoryLimitFactor {
		return math.MaxInt64
	}
	return included * api.ManagedPostgresHistoryLimitFactor
}

// HistoryLimited reports whether a subject's month-to-date restore history
// and snapshots reached its limit. A subject with overage on pays for its
// history and is never limited; an unknown plan (no storage allowance) is
// never limited either.
func HistoryLimited(snapshot UsageSnapshot, storageAllowance int64, overage bool) bool {
	limit := HistoryLimitByteSeconds(storageAllowance)
	return !overage && limit > 0 && snapshot.historyAndSnapshots() >= limit
}

type historyScope struct {
	resourceID  string
	databaseIDs []string
	current     int64
	// spec is the smallest positive spec restore window among the scope's
	// databases: the window the scope keeps while its subject is not limited.
	spec int64
}

// historyRetentionTarget is the window a scope keeps.
func historyRetentionTarget(spec int64, limited bool) int64 {
	if limited {
		return min(spec, historyLimitedSeconds)
	}
	return spec
}

// enforceHistoryLimit runs the history pass for one subject. It only moves a
// scope between its spec window and the limited one: any other observed
// window is drift the reconciler reports, and the pass leaves it alone.
func (e *CapEnforcer) enforceHistoryLimit(ctx context.Context, subjectID string, databases []Database, limits CapLimits, summary *CapSweepSummary) error {
	limit := HistoryLimitByteSeconds(limits.StorageAllowance)
	if limit <= 0 {
		summary.HistoryUncapped++
		return nil
	}
	snapshot, err := e.store.UsageSnapshot(ctx, subjectID, e.now())
	if err != nil {
		return err
	}
	limited := HistoryLimited(snapshot, limits.StorageAllowance, limits.Overage)
	if limited {
		summary.HistoryLimitedSubjects++
	}
	scopes, providers, err := e.observeHistoryScopes(ctx, databases, summary)
	if err != nil || len(scopes) == 0 {
		return err
	}
	var failures []error
	for _, scope := range scopes {
		target := historyRetentionTarget(scope.spec, limited)
		if scope.current == target {
			summary.HistoryScopesUnchanged++
			continue
		}
		attrs := []any{"subject_id", subjectID, "database_ids", scope.databaseIDs, "overage", limits.Overage,
			"history_byte_seconds", snapshot.HistoryByteSeconds, "snapshot_byte_seconds", snapshot.SnapshotByteSeconds,
			"limit_byte_seconds", limit, "restore_window_seconds", scope.current, "target_seconds", target}
		if scope.current != scope.spec && scope.current != historyRetentionTarget(scope.spec, true) {
			summary.HistoryScopesDrift++
			e.logger.Warn("managed postgres restore window differs from its spec; history limit leaves it", attrs...)
			continue
		}
		if e.historyMode != CapEnforcementOn {
			summary.HistoryScopesShadowed++
			e.logger.Info("managed postgres history limit (shadow)", attrs...)
			continue
		}
		if err := providers[scope.resourceID].SetHistoryRetention(ctx, scope.resourceID, target); err != nil {
			failures = append(failures, err)
			continue
		}
		summary.HistoryScopesApplied++
		if limited {
			e.logger.Info("managed postgres restore window limited", attrs...)
		} else {
			e.logger.Info("managed postgres restore window restored", attrs...)
		}
	}
	return errors.Join(failures...)
}

// observeHistoryScopes reads the restore window of each provider object the
// subject's ready databases live in. A subject is skipped, not failed, when
// its provider cannot change the window.
func (e *CapEnforcer) observeHistoryScopes(ctx context.Context, databases []Database, summary *CapSweepSummary) ([]historyScope, map[string]HistoryRetentionProvider, error) {
	byScope := make(map[string]*historyScope)
	providers := make(map[string]HistoryRetentionProvider)
	var order []string
	for _, database := range databases {
		if database.State != StateReady || database.ProviderResourceID == "" || database.Spec.RestoreWindowSeconds <= 0 {
			continue
		}
		backend, err := e.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil {
			return nil, nil, err
		}
		provider, ok := backend.Provider.(HistoryRetentionProvider)
		if !ok {
			summary.HistoryUnsupported++
			return nil, nil, nil
		}
		observed, err := provider.ObserveHistoryRetention(ctx, database.ProviderResourceID)
		if err != nil {
			return nil, nil, err
		}
		if observed.Scope == "" || observed.Seconds < 0 {
			return nil, nil, fmt.Errorf("database %s: invalid restore window observation: %w", database.ID, ErrUnavailable)
		}
		scope, ok := byScope[observed.Scope]
		if !ok {
			scope = &historyScope{resourceID: database.ProviderResourceID, current: observed.Seconds}
			byScope[observed.Scope] = scope
			providers[database.ProviderResourceID] = provider
			order = append(order, observed.Scope)
		}
		scope.databaseIDs = append(scope.databaseIDs, database.ID)
		if spec := database.Spec.RestoreWindowSeconds; scope.spec == 0 || spec < scope.spec {
			scope.spec = spec
		}
	}
	scopes := make([]historyScope, 0, len(order))
	for _, key := range order {
		scopes = append(scopes, *byScope[key])
	}
	return scopes, providers, nil
}

// HistoryLimitedRestoreWindow returns the restore window a source keeps
// while its subject's history is limited: the provider object's observed
// window when it is the limited one and shorter than the spec's, otherwise
// zero. A provider that cannot report the window, or a failed read, also
// returns zero: the provider still refuses a point past its own window.
func HistoryLimitedRestoreWindow(ctx context.Context, provider Provider, source Database) int64 {
	retention, ok := provider.(HistoryRetentionProvider)
	if !ok || source.ProviderResourceID == "" {
		return 0
	}
	observed, err := retention.ObserveHistoryRetention(ctx, source.ProviderResourceID)
	if err != nil || observed.Seconds != historyLimitedSeconds || observed.Seconds >= source.Spec.RestoreWindowSeconds {
		return 0
	}
	return observed.Seconds
}
