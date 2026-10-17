package managedpostgres

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
)

// RequestSourceFence is explicit internal controller intent. It is not called
// by Prepare, Verify or Preflight, and has no customer HTTP route in this slice.
func (s *CutoverService) RequestSourceFence(ctx context.Context, account, id string) (CutoverSourceFence, error) {
	store, ok := s.store.(CutoverSourceFenceStore)
	if !ok || s.checkpoint == nil {
		return CutoverSourceFence{}, ErrUnsupported
	}
	if !s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, account) {
		return CutoverSourceFence{}, ErrProvisioningClosed
	}
	c, err := s.store.GetCutover(ctx, account, id)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	d, err := s.bindings.databases.Get(ctx, account, c.Source.ID)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	if d.DataResourceID == "" {
		return CutoverSourceFence{}, ErrUnsupported
	}
	b, err := s.bindings.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	_, maintenance := b.Provider.(CheckpointMaintenanceProvider)
	_, discovery := b.Provider.(CheckpointConnectionDiscoveryProvider)
	_, closure := b.Provider.(CheckpointConnectionClosureProvider)
	_, recovery := b.Provider.(CheckpointConnectionRecoveryProvider)
	if !maintenance || !discovery || !closure || !recovery {
		return CutoverSourceFence{}, ErrUnsupported
	}
	return store.ReserveCutoverSourceFence(ctx, account, id, s.bindings.now())
}

// ReconcileSourceFence advances one recoverable phase or refreshes one closed
// observation. Lease expiry never discards the original source/operation hold.
func (s *CutoverService) ReconcileSourceFence(ctx context.Context, account, id string) (CutoverSourceFence, error) {
	store, ok := s.store.(CutoverSourceFenceStore)
	if !ok || s.checkpoint == nil {
		return CutoverSourceFence{}, ErrUnsupported
	}
	now := s.bindings.now()
	f, err := store.ClaimCutoverSourceFence(ctx, account, id, uuid.NewString(), now, now.Add(s.bindings.leaseDuration))
	if err != nil || f.State == "released" {
		return f, err
	}
	if f.State == "abandoning" {
		return s.abandonSourceFence(ctx, f)
	}
	if f.State == "pending" {
		if !s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, account) {
			return f, s.failSourceFence(ctx, f, "source_fence_provisioning_closed", ErrProvisioningClosed)
		}
		return s.prepareSourceFence(ctx, f)
	}
	return s.observeSourceFence(ctx, f, false)
}

func (s *CutoverService) saveSourceFence(ctx context.Context, before, next CutoverSourceFence, keepLease bool) (CutoverSourceFence, error) {
	now := s.bindings.now()
	retry := now
	if !keepLease && next.State != "released" {
		retry = now.Add(s.bindings.retryInterval)
	}
	return s.store.(CutoverSourceFenceStore).SaveCutoverSourceFence(ctx, before, next, keepLease, now, retry)
}

func (s *CutoverService) failSourceFence(ctx context.Context, f CutoverSourceFence, code string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	next := f
	next.LastErrorCode = code
	now := s.bindings.now()
	_, err := s.store.(CutoverSourceFenceStore).SaveCutoverSourceFence(ctx, f, next, false, now, now.Add(retryDelay(normalizeProviderError(cause), f.AttemptCount)))
	return errors.Join(normalizeProviderError(cause), err)
}

func (s *CutoverService) sourceFenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, min(s.bindings.providerTimeout, s.bindings.leaseDuration/2))
}

func (s *CutoverService) prepareSourceFence(ctx context.Context, f CutoverSourceFence) (CutoverSourceFence, error) {
	if f.MaintenanceStage != "ready" {
		return s.prepareSourceMaintenance(ctx, f)
	}
	if len(f.DatabaseNames) == 0 {
		probe, cancel := s.sourceFenceContext(ctx)
		defer cancel()
		selected, err := s.checkpoint.DiscoverCheckpointConnections(probe, f.definition(), f.Maintenance, f.identity())
		if err != nil {
			return f, s.failSourceFence(ctx, f, "source_fence_selection_failed", err)
		}
		next := f
		next.DatabaseNames, next.LastErrorCode = slices.Clone(selected.DatabaseNames), ""
		return s.saveSourceFence(ctx, f, next, false)
	}
	// Persist possible dispatch before opening a provider connection. Even an
	// early missing remote row cannot later authorize local-only cancellation.
	next := f
	next.State, next.CloseRequestedAt, next.LastErrorCode = "closing", s.bindings.now(), ""
	marked, err := s.saveSourceFence(ctx, f, next, true)
	if err != nil {
		return f, err
	}
	return s.observeSourceFence(ctx, marked, true)
}

func (s *CutoverService) prepareSourceMaintenance(ctx context.Context, f CutoverSourceFence) (CutoverSourceFence, error) {
	phase, requested, complete := "", "", ""
	switch f.MaintenanceStage {
	case "reserved", "role_requested":
		phase, requested, complete = "role", "role_requested", "role_reserved"
	case "role_reserved", "database_requested":
		phase, requested, complete = "database", "database_requested", "database_created"
	case "database_created", "activation_requested":
		phase, requested, complete = "activation", "activation_requested", "ready"
	default:
		return f, s.failSourceFence(ctx, f, "source_fence_maintenance_invalid", ErrConflict)
	}
	if f.MaintenanceStage != requested {
		next := f
		next.MaintenanceStage, next.LastErrorCode = requested, ""
		marked, err := s.saveSourceFence(ctx, f, next, true)
		if err != nil {
			return f, err
		}
		f = marked
	}
	probe, cancel := s.sourceFenceContext(ctx)
	defer cancel()
	actual, err := s.checkpoint.ReconcileCheckpointMaintenance(probe, f.definition(), CheckpointMaintenanceRequest{
		OwnerToken: f.Maintenance.OwnerToken, SourceResourceID: f.Source.DataResourceID, Phase: phase,
		OwnerOID: f.Maintenance.OwnerOID, DatabaseOID: f.Maintenance.DatabaseOID})
	if err == nil {
		err = probe.Err()
	}
	if err != nil {
		return f, s.failSourceFence(ctx, f, "source_fence_maintenance_failed", err)
	}
	next := f
	next.Maintenance, next.MaintenanceStage, next.LastErrorCode = actual, complete, ""
	return s.saveSourceFence(ctx, f, next, false)
}

func (s *CutoverService) observeSourceFence(ctx context.Context, f CutoverSourceFence, dispatch bool) (CutoverSourceFence, error) {
	probe, cancel := s.sourceFenceContext(ctx)
	defer cancel()
	var first *sourceFenceObservation
	if !dispatch {
		actual, err := s.checkpoint.ObserveCheckpointConnectionClosure(probe, f.definition(), f.Maintenance, f.request())
		if err == nil {
			return s.recordSourceClosure(ctx, probe, f, actual)
		}
		if f.State != "closing" || !errors.Is(err, ErrNotFound) {
			return f, s.failSourceFence(ctx, f, "source_fence_observation_failed", err)
		}
	}
	closed, err := s.checkpoint.CloseCheckpointConnections(probe, f.definition(), f.Maintenance, f.request())
	if err != nil {
		return f, s.failSourceFence(ctx, f, "source_fence_dispatch_failed", err)
	}
	first = &sourceFenceObservation{ClosedAt: closed.ClosedAt, Databases: closed.Databases}
	actual, err := s.checkpoint.ObserveCheckpointConnectionClosure(probe, f.definition(), f.Maintenance, f.request())
	if err != nil {
		return f, s.failSourceFence(ctx, f, "source_fence_observation_failed", err)
	}
	observed := &sourceFenceObservation{ClosedAt: actual.ClosedAt, Databases: actual.Databases}
	if !sameSourceFenceClosurePins(first, observed) {
		return f, s.failSourceFence(ctx, f, "source_fence_identity_changed", ErrConflict)
	}
	return s.recordSourceClosure(ctx, probe, f, actual)
}

func (s *CutoverService) recordSourceClosure(ctx, probe context.Context, f CutoverSourceFence, actual CheckpointConnectionClosure) (CutoverSourceFence, error) {
	next := f
	next.Observation = &sourceFenceObservation{ClosedAt: actual.ClosedAt, Databases: slices.Clone(actual.Databases),
		UnselectedDatabases: actual.UnselectedDatabases, Drained: actual.Drained}
	if actual.WriterInventory != nil {
		inventory := *actual.WriterInventory
		next.Observation.WriterInventory = &inventory
	}
	if f.Observation != nil && !sameSourceFenceClosurePins(f.Observation, next.Observation) {
		return f, s.failSourceFence(ctx, f, "source_fence_identity_changed", ErrConflict)
	}
	next.State, next.LastErrorCode, next.ObservedAt = "closed", "", s.bindings.now()
	if next.CatalogInvalidatedAt.IsZero() && (actual.UnselectedDatabases > 0 || next.Observation.WriterInventory != nil &&
		(!next.Observation.WriterInventory.CatalogComplete || next.CatalogFingerprint != "" && next.Observation.WriterInventory.CatalogFingerprint != next.CatalogFingerprint)) {
		next.CatalogInvalidatedAt = s.bindings.now()
	}
	if next.CatalogFingerprint == "" && next.CatalogInvalidatedAt.IsZero() && sourceWriterCatalogComplete(next) {
		next.CatalogFingerprint = next.Observation.WriterInventory.CatalogFingerprint
	}
	s.observeSourceWriterIsolation(probe, f, &next)
	return s.saveSourceFence(ctx, f, next, false)
}

func (s *CutoverService) abandonSourceFence(ctx context.Context, f CutoverSourceFence) (CutoverSourceFence, error) {
	next := f
	next.State, next.LastErrorCode = "released", ""
	if f.CloseRequestedAt.IsZero() {
		next.RemoteTerminalState = "not_dispatched"
		return s.saveSourceFence(ctx, f, next, false)
	}
	probe, cancel := s.sourceFenceContext(ctx)
	defer cancel()
	terminal, err := s.checkpoint.AbandonCheckpointConnections(probe, f.definition(), f.Maintenance, f.identity())
	if err != nil {
		return f, s.failSourceFence(ctx, f, "source_fence_abandon_failed", err)
	}
	observed, err := s.checkpoint.ObserveCheckpointConnections(probe, f.definition(), f.Maintenance, f.identity())
	if err == nil {
		err = probe.Err()
	}
	if err != nil || terminal.State != observed.State || !terminal.ReleasedAt.Equal(observed.ReleasedAt) {
		if err == nil {
			err = ErrConflict
		}
		return f, s.failSourceFence(ctx, f, "source_fence_release_unconfirmed", err)
	}
	next.RemoteTerminalState, next.RemoteReleasedAt = observed.State, observed.ReleasedAt
	return s.saveSourceFence(ctx, f, next, false)
}

func (s *CutoverService) cleanupSourceFence(ctx context.Context, account, id string) error {
	if _, ok := s.store.(CutoverSourceFenceStore); !ok {
		return nil
	}
	f, err := s.ReconcileSourceFence(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if f.State != "released" {
		return ErrUnavailable
	}
	return nil
}

func (s *CutoverService) sweepSourceFences(ctx context.Context, limit int) error {
	store, ok := s.store.(CutoverSourceFenceStore)
	if !ok {
		return nil
	}
	rows, err := store.DueCutoverSourceFences(ctx, true, limit, s.bindings.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range rows {
		_, err := s.ReconcileSourceFence(ctx, c.AccountID, c.ID)
		if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrProvisioningClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
