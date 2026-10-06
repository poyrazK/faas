package managedpostgres

import (
	"context"
	"errors"
	"math"
	"time"
)

// RestoreSourceObserver reads the exact pinned dataset and its current recovery
// limits. It must not retrieve credentials, connect to SQL, wake compute or
// mutate resources. Necessary limits are not proof that every WAL point exists.
type RestoreSourceObserver interface {
	ObserveRestoreSource(context.Context, RestoreSourceDefinition) (RestoreSourceObservation, error)
}

type RestoreHistoryBounds struct{ From, Through time.Time }

type RestoreSourceObservation struct {
	ProviderResourceID, DataResourceID string
	Status                             ProviderStatus
	RetentionSeconds                   int64
	// HistoryNotBefore is a known necessary limit, not the earliest retained WAL.
	HistoryNotBefore time.Time
	Lineage          *RestoreLineage
	// HistoryBounds is optional authoritative provider evidence of retained
	// history. An adapter must leave it nil when its API cannot establish it.
	HistoryBounds *RestoreHistoryBounds
}

type RecoveryStatus struct {
	DatabaseID, Status, LastErrorCode        string
	CheckedAt                                time.Time
	Fresh, HistoryBoundsKnown                bool
	RetentionSeconds                         int64
	EarliestPossibleTime, LatestPossibleTime time.Time
}

func (s *Service) GetRecoveryStatus(ctx context.Context, account, id string) (RecoveryStatus, error) {
	database, err := s.Get(ctx, account, id)
	if err != nil {
		return RecoveryStatus{}, err
	}
	return s.observeRecovery(ctx, database)
}

// PreflightRestore authenticates a frozen source definition for internal PITR
// reservation writers. They must still reserve atomically with live source
// pins and skip this check when replaying an existing durable receipt.
func (s *Service) PreflightRestore(ctx context.Context, account, sourceID string, definition RestoreSourceDefinition, point time.Time) error {
	source, err := s.Get(ctx, account, sourceID)
	if err != nil {
		return err
	}
	if definition.Spec.Validate() != nil || definition.BackendID != source.BackendID || definition.BackendFingerprint != source.BackendFingerprint ||
		definition.ProviderResourceID != source.ProviderResourceID || definition.DataResourceID != source.DataResourceID {
		return ErrConflict
	}
	retention := min(source.Spec.RestoreWindowSeconds, definition.Spec.RestoreWindowSeconds)
	source.Spec = definition.Spec
	source.Spec.RestoreWindowSeconds = retention
	status, err := s.observeRecovery(ctx, source)
	if err != nil {
		return err
	}
	return status.admits(point)
}

func (s *Service) observeRecovery(ctx context.Context, database Database) (RecoveryStatus, error) {
	if err := ctx.Err(); err != nil {
		return RecoveryStatus{}, err
	}
	result := RecoveryStatus{DatabaseID: database.ID, Status: "unknown"}
	if database.State != StateReady || database.DesiredGeneration != database.ObservedGeneration {
		result.Status, result.LastErrorCode = "unavailable", "source_not_ready"
		return result, nil
	}
	if database.Spec.RestoreWindowSeconds == 0 {
		result.Status, result.LastErrorCode = "unavailable", "retention_disabled"
		return result, nil
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		result.LastErrorCode = "backend_unavailable"
		return result, nil
	}
	observer, ok := backend.Provider.(RestoreSourceObserver)
	if !backend.Capabilities.PointInTimeRestore || !backend.Capabilities.RestorePreflight || !ok ||
		!validDataResourceID(database.ProviderResourceID) || !validDataResourceID(database.DataResourceID) {
		result.Status, result.LastErrorCode = "unsupported", "preflight_unsupported"
		return result, nil
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	observation, err := observer.ObserveRestoreSource(providerCtx, RestoreSourceDefinition{Spec: database.Spec,
		BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint,
		ProviderResourceID: database.ProviderResourceID, DataResourceID: database.DataResourceID})
	result.CheckedAt = s.now().UTC()
	if ctx.Err() != nil {
		return RecoveryStatus{}, ctx.Err()
	}
	if providerCtx.Err() != nil {
		err = providerCtx.Err()
	}
	if err != nil {
		result.LastErrorCode = "provider_unavailable"
		if errors.Is(err, ErrNotFound) {
			result.Status, result.LastErrorCode = "unavailable", "resource_missing"
		} else if errors.Is(err, ErrUnsupported) {
			result.Status, result.LastErrorCode = "unsupported", "preflight_unsupported"
		} else if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) {
			result.LastErrorCode = "observation_invalid"
		}
		return result, nil
	}
	return recoveryStatusFromObservation(database, observation, result.CheckedAt), nil
}

func recoveryStatusFromObservation(database Database, observed RestoreSourceObservation, now time.Time) RecoveryStatus {
	out := RecoveryStatus{DatabaseID: database.ID, Status: "unknown", CheckedAt: now, LastErrorCode: "observation_invalid"}
	if now.IsZero() || !validDataResourceID(database.ProviderResourceID) || !validDataResourceID(database.DataResourceID) ||
		observed.ProviderResourceID != database.ProviderResourceID || observed.DataResourceID != database.DataResourceID ||
		!validRestoreRetention(observed.RetentionSeconds) || !validRestoreRetention(database.Spec.RestoreWindowSeconds) ||
		observed.HistoryNotBefore.IsZero() || observed.HistoryNotBefore.After(now) {
		return out
	}
	if database.RestoreSourceDatabaseID != "" && (observed.Lineage == nil || observed.Lineage.SourceResourceID != database.RestoreSourceResourceID || !observed.Lineage.PointInTime.Equal(database.RestorePointInTime)) {
		return out
	}
	switch observed.Status {
	case ProviderStatusReady:
	case ProviderStatusPending, ProviderStatusDeleting, ProviderStatusFailed:
		out.Status, out.LastErrorCode, out.Fresh = "unavailable", "source_not_ready", true
		return out
	default:
		return out
	}
	out.Fresh, out.LastErrorCode = true, ""
	out.RetentionSeconds = min(database.Spec.RestoreWindowSeconds, observed.RetentionSeconds)
	if out.RetentionSeconds == 0 {
		out.Status, out.LastErrorCode = "unavailable", "retention_disabled"
		return out
	}
	out.EarliestPossibleTime = now.Add(-time.Duration(out.RetentionSeconds) * time.Second)
	if observed.HistoryNotBefore.After(out.EarliestPossibleTime) {
		out.EarliestPossibleTime = observed.HistoryNotBefore.UTC()
	}
	out.LatestPossibleTime, out.Status = now, "limits_known"
	if bounds := observed.HistoryBounds; bounds != nil {
		if bounds.From.IsZero() || bounds.Through.IsZero() || bounds.From.After(bounds.Through) || bounds.From.Before(observed.HistoryNotBefore) || bounds.Through.After(now) {
			return RecoveryStatus{DatabaseID: database.ID, Status: "unknown", CheckedAt: now, LastErrorCode: "observation_invalid"}
		}
		out.HistoryBoundsKnown, out.Status = true, "available"
		if bounds.From.After(out.EarliestPossibleTime) {
			out.EarliestPossibleTime = bounds.From.UTC()
		}
		out.LatestPossibleTime = bounds.Through.UTC()
	}
	if out.EarliestPossibleTime.After(out.LatestPossibleTime) {
		out.Status, out.LastErrorCode = "unavailable", "history_unavailable"
		out.EarliestPossibleTime, out.LatestPossibleTime = time.Time{}, time.Time{}
	}
	return out
}

func validRestoreRetention(seconds int64) bool {
	return seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second)
}

func (r RecoveryStatus) admits(point time.Time) error {
	switch r.Status {
	case "unsupported":
		return ErrUnsupported
	case "unavailable":
		if r.LastErrorCode == "retention_disabled" || r.LastErrorCode == "history_unavailable" {
			return ErrInvalid
		}
		return ErrUnavailable
	case "limits_known", "available":
		if !r.Fresh || point.IsZero() || point.Before(r.EarliestPossibleTime) || point.After(r.LatestPossibleTime) || !point.Before(r.CheckedAt) {
			return ErrInvalid
		}
		return nil
	default:
		return ErrUnavailable
	}
}
