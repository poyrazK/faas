package managedpostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type recoveryReader interface {
	CheckRecoveryTarget(ctx context.Context, id string, major int, target time.Time, latest bool) (api.ManagedPostgresRecoveryStatus, error)
}

type recoverySettingsStore interface {
	setRecoveryRegion(context.Context, Database, string) error
}

func (s *Service) recoveryBackend(source Database, region string) (Backend, recoveryReader, error) {
	home, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Backend{}, nil, err
	}
	target, ok := s.registry.backends[s.registry.recoveryTargets[source.Spec.Region]]
	if home.Driver != "firecracker" || region == "" || !ok || target.Region != region {
		return Backend{}, nil, ErrUnsupported
	}
	reader, ok := target.Provider.(recoveryReader)
	if !ok {
		return Backend{}, nil, ErrUnsupported
	}
	return target, reader, nil
}

// SetRecoveryRegion opts a database into the configured recovery destination.
func (s *Service) SetRecoveryRegion(ctx context.Context, account, id, region string) error {
	if !s.operationsEnabled() {
		return ErrProvisioningClosed
	}
	source, err := customerDatabase(ctx, s.store, account, id)
	if err != nil {
		return err
	}
	if source.State != StateReady || source.ProviderResourceID == "" || !source.Preview.IsZero() {
		return ErrConflict
	}
	if region != "" {
		if _, _, err := s.recoveryBackend(source, region); err != nil {
			return err
		}
	}
	store, ok := s.store.(recoverySettingsStore)
	if !ok {
		return ErrUnsupported
	}
	return store.setRecoveryRegion(ctx, source, region)
}

func (s *PostgresStore) setRecoveryRegion(ctx context.Context, database Database, region string) error {
	count, err := sqlc.New().SetManagedPostgresRecoveryRegion(ctx, s.pool, sqlc.SetManagedPostgresRecoveryRegionParams{
		ID: databaseNullableUUID(database.ID), AccountID: databaseNullableUUID(database.AccountID),
		ProviderResourceID: databaseNullableText(database.ProviderResourceID),
		SourceRegion:       database.Spec.Region, PreviousRegion: database.RecoveryRegion, RecoveryRegion: region,
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *MemoryStore) setRecoveryRegion(_ context.Context, database Database, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.databases[database.ID]
	if !ok || current.AccountID != database.AccountID || current.State != StateReady ||
		current.ProviderResourceID != database.ProviderResourceID || current.RecoveryRegion != database.RecoveryRegion {
		return ErrConflict
	}
	current.RecoveryRegion = region
	s.databases[database.ID] = current
	return nil
}

func (s *Service) regionalRecoveryStatus(ctx context.Context, source Database) *api.ManagedPostgresRecoveryStatus {
	if source.RecoveryRegion == "" {
		return nil
	}
	unknown := &api.ManagedPostgresRecoveryStatus{Region: source.RecoveryRegion, Outcome: "unverified", Reason: "recovery_archive_unavailable"}
	if source.State != StateReady || source.ObservedGeneration != source.DesiredGeneration {
		unknown.Reason = "source_not_ready"
		return unknown
	}
	_, reader, err := s.recoveryBackend(source, source.RecoveryRegion)
	if err != nil {
		return unknown
	}
	now := s.now().UTC()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, _, err := s.latestRegionalRecovery(ctx, reader, source, now)
	if err != nil {
		return unknown
	}
	if result.RecoveryPoint != nil {
		if !withinRecoveryWindow(source, now, *result.RecoveryPoint) {
			unknown.Reason = "recovery_point_outside_window"
			return unknown
		}
		result.RecoveryLagSeconds = int64(now.Sub(*result.RecoveryPoint).Seconds())
	}
	return &result
}

func (s *Service) prepareRegionalRestore(ctx context.Context, source Database, request RestoreDatabaseRequest, now time.Time) (Database, time.Time, error) {
	var target Database
	point := request.PointInTime
	if source.RecoveryRegion != request.RecoveryRegion || source.ObservedGeneration != source.DesiredGeneration {
		return target, point, fmt.Errorf("%w: enable this recovery region for the source database first", ErrConflict)
	}
	backend, reader, err := s.recoveryBackend(source, request.RecoveryRegion)
	if err != nil {
		return target, point, err
	}
	spec := source.Spec
	var result api.ManagedPostgresRecoveryStatus
	if request.Latest {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var major int
		result, major, err = s.latestRegionalRecovery(ctx, reader, source, now)
		spec.PostgresMajor = major
		if result.RecoveryPoint != nil {
			point = result.RecoveryPoint.UTC().Truncate(time.Microsecond)
		}
	} else {
		if !withinRecoveryWindow(source, now, point) {
			return target, point, ErrInvalid
		}
		// Read the major history from the catalog without waking the source.
		spec, err = s.resolveRestoreSpec(ctx, source, point, false)
		if err != nil {
			return target, point, err
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result, err = reader.CheckRecoveryTarget(ctx, databaseDataResource(source), spec.PostgresMajor, point, false)
	}
	if err != nil {
		return target, point, err
	}
	if result.Outcome != "covered" || result.RecoveryTimeline == 0 || !withinRecoveryWindow(source, now, point) {
		return target, point, fmt.Errorf("%w: no verified recovery point in %s (%s)", ErrConflict, backend.Region, result.Reason)
	}
	target = source
	target.Spec = spec
	target.Spec.Region = backend.Region
	target.RestoreRecoveryTimeline = result.RecoveryTimeline
	target.BackendID, target.BackendFingerprint = backend.ID, backend.Fingerprint
	return target, point, nil
}

// latestRegionalRecovery tries the current archive first. If the destination
// is behind an upgrade, probe each retained major before its upgrade boundary
// so a newer base backup cannot hide the older one.
func (s *Service) latestRegionalRecovery(ctx context.Context, reader recoveryReader, source Database, now time.Time) (api.ManagedPostgresRecoveryStatus, int, error) {
	major := source.Spec.PostgresMajor
	latest := now.Add(-time.Second)
	resource := databaseDataResource(source)
	result, err := reader.CheckRecoveryTarget(ctx, resource, major, latest, true)
	if err == nil && usableRegionalRecoveryPoint(source, now, result, latest) {
		return result, major, nil
	}
	if result.Outcome == "covered" {
		result.Outcome = "unverified"
		result.Reason = "recovery_point_outside_window"
		result.RecoveryPoint = nil
	}
	home, resolveErr := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if resolveErr != nil {
		return result, major, err
	}
	historyReader, ok := home.Provider.(RestoreMajorHistoryReader)
	if !ok {
		return result, major, err
	}
	history, historyErr := historyReader.RestoreMajorHistory(ctx, restoreSourceDefinition(source))
	if historyErr != nil {
		return result, major, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		span := history[i]
		checkAt := latest
		if !checkAt.Before(span.Until) {
			checkAt = span.Until.Add(-time.Microsecond)
		}
		if !withinRecoveryWindow(source, now, checkAt) {
			break
		}
		candidate, probeErr := reader.CheckRecoveryTarget(ctx, resource, span.PostgresMajor, checkAt, true)
		if probeErr != nil || !usableRegionalRecoveryPoint(source, now, candidate, checkAt) {
			continue
		}
		if i > 0 && candidate.RecoveryPoint.Before(history[i-1].Until) {
			continue
		}
		return candidate, span.PostgresMajor, nil
	}
	return result, major, err
}

func usableRegionalRecoveryPoint(source Database, now time.Time, result api.ManagedPostgresRecoveryStatus, checkAt time.Time) bool {
	return result.Outcome == "covered" && result.RecoveryPoint != nil && !result.RecoveryPoint.After(checkAt) &&
		withinRecoveryWindow(source, now, *result.RecoveryPoint)
}

func withinRecoveryWindow(source Database, now, point time.Time) bool {
	return !point.IsZero() && point.Before(now) && !point.Before(source.CreatedAt) &&
		source.Spec.RestoreWindowSeconds > 0 && now.Sub(point) <= time.Duration(source.Spec.RestoreWindowSeconds)*time.Second
}
