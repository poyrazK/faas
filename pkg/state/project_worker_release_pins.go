package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type projectWorkerReleasePin struct {
	AppID, BindingID, ReleaseID, DeploymentID string
}

type projectWorkerReleasePinStore interface {
	ResolveProjectWorkerJobRelease(context.Context, Invocation, string, string) (InvocationVersion, bool, error)
}

// ResolveProjectWorkerJobRelease captures one immutable graph per job lineage.
// Only a live durable claim can establish/read a pin. Retained graph expiry is
// still authoritative: an expired pin fails closed, never resolves today's graph.
func (s *PgStore) ResolveProjectWorkerJobRelease(ctx context.Context, inv Invocation, revision, requested string) (InvocationVersion, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvocationVersion{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockProjectWorkerReleasePinOwner(ctx, tx, sqlc.LockProjectWorkerReleasePinOwnerParams{AppID: mustPgUUID(inv.AppID), AccountID: mustPgUUID(inv.AccountID)}); err != nil {
		return InvocationVersion{}, false, mapErr(err)
	}
	if err = lockInvocationAttemptClaim(ctx, tx, inv.ID, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}); err != nil {
		return InvocationVersion{}, false, projectWorkerPinClaimError(err)
	}
	row, err := q.ReadProjectWorkerReleasePinInvocation(ctx, tx, sqlc.ReadProjectWorkerReleasePinInvocationParams{InvocationID: mustPgUUID(inv.ID), AppID: mustPgUUID(inv.AppID), AccountID: mustPgUUID(inv.AccountID)})
	if err != nil {
		return InvocationVersion{}, false, mapErr(err)
	}
	if pgUUIDString(row.QueueBindingID) != inv.QueueBindingID {
		return InvocationVersion{}, false, ErrConflict
	}
	root, err := q.LockProjectWorkerReleasePinRoot(ctx, tx, mustPgUUID(inv.ID))
	if err != nil {
		return InvocationVersion{}, false, mapErr(err)
	}
	pin, err := q.ReadProjectWorkerReleasePin(ctx, tx, root.ID)
	if err == nil {
		if pgUUIDString(pin.AppID) != inv.AppID || pgUUIDString(pin.BindingID) != inv.QueueBindingID || revision != "" || requested != "" && requested != pgUUIDString(pin.ReleaseID) {
			return InvocationVersion{}, false, ErrConflict
		}
		requested = pgUUIDString(pin.ReleaseID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return InvocationVersion{}, false, err
	} else if err = validateRootWorkerPinHeaders(root.Headers, revision, requested); err != nil {
		return InvocationVersion{}, false, err
	}
	if revision != "" {
		return InvocationVersion{}, false, nil
	}
	if !pin.RootInvocationID.Valid && requested == "" {
		requested, err = selectProjectCanaryDB(ctx, tx, inv.AppID, pgUUIDString(root.ID))
		if err != nil {
			return InvocationVersion{}, false, err
		}
	}
	release, err := optionalOperationUUID(requested)
	if err != nil {
		return InvocationVersion{}, false, err
	}
	selected, err := q.ResolvePublicProjectRelease(ctx, tx, sqlc.ResolvePublicProjectReleaseParams{AppID: row.AppID, Scope: "production", ReleaseID: release})
	if errors.Is(err, pgx.ErrNoRows) && requested == "" {
		return InvocationVersion{Scope: "production"}, true, nil
	}
	if err != nil {
		return InvocationVersion{}, false, mapErr(err)
	}
	if selected.DeploymentID == "" || pin.DeploymentID.Valid && pgUUIDString(pin.DeploymentID) != selected.DeploymentID {
		return InvocationVersion{}, false, ErrConflict
	}
	usable, err := q.RetainedReleaseTargetUsable(ctx, tx, sqlc.RetainedReleaseTargetUsableParams{AppID: row.AppID, DeploymentID: mustPgUUID(selected.DeploymentID)})
	if err != nil {
		return InvocationVersion{}, false, err
	}
	if !usable {
		return InvocationVersion{}, false, ErrConflict
	}
	if !pin.RootInvocationID.Valid {
		if err = q.InsertProjectWorkerReleasePin(ctx, tx, sqlc.InsertProjectWorkerReleasePinParams{RootInvocationID: root.ID, AppID: row.AppID, BindingID: row.QueueBindingID, ReleaseID: mustPgUUID(selected.ReleaseID), DeploymentID: mustPgUUID(selected.DeploymentID)}); err != nil {
			return InvocationVersion{}, false, err
		}
	}
	// Recheck the lease after lock waits/selection; expired claims cannot commit
	// an admission decision even if they were valid at the beginning of the tx.
	if err = lockInvocationAttemptClaim(ctx, tx, inv.ID, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}); err != nil {
		return InvocationVersion{}, false, projectWorkerPinClaimError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return InvocationVersion{}, false, err
	}
	return InvocationVersion{Scope: "production", ReleaseID: selected.ReleaseID, DeploymentID: selected.DeploymentID}, true, nil
}

func projectWorkerPinClaimError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	return err
}

func validateRootWorkerPinHeaders(raw json.RawMessage, revision, release string) error {
	headers := map[string]string{}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &headers); err != nil {
			return ErrInvalidArgument
		}
	}
	r, graph, err := invocationPinHeaders(headers)
	if err != nil {
		return err
	}
	if r != revision || graph != release {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) ResolveProjectWorkerJobRelease(_ context.Context, inv Invocation, revision, requested string) (InvocationVersion, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, exists := m.invocations[inv.ID]
	app, appOK := m.apps[inv.AppID]
	binding, bindingOK := m.queueBindings[stored.QueueBindingID]
	if !exists || !appOK || !bindingOK || app.ProjectID == "" || app.AccountID != inv.AccountID || stored.AccountID != inv.AccountID || stored.AppID != inv.AppID || stored.QueueBindingID != inv.QueueBindingID || stored.DeploymentScope != "production" || stored.EnvironmentID != "" || (stored.Source != InvocationQueue && stored.Source != InvocationReplay) || !validInvocationAttemptClaim(stored, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}) || binding.AppID != inv.AppID || binding.AccountID != inv.AccountID || binding.DeploymentScope != "production" || binding.Mode != "push" || binding.WorkloadClass != WorkloadClassWorker || !binding.Enabled || binding.RetiredAt != nil {
		return InvocationVersion{}, false, ErrConflict
	}
	rootID := stored.ReplayRootInvocationID
	if rootID == "" {
		rootID = stored.ID
	}
	root, ok := m.invocations[rootID]
	if !ok || root.AccountID != stored.AccountID || root.AppID != stored.AppID || root.QueueBindingID != stored.QueueBindingID || root.DeploymentScope != stored.DeploymentScope || root.EnvironmentID != "" {
		return InvocationVersion{}, false, ErrConflict
	}
	pin, pinned := m.workerReleasePins[rootID]
	if pinned {
		if pin.AppID != inv.AppID || pin.BindingID != inv.QueueBindingID || revision != "" || requested != "" && requested != pin.ReleaseID {
			return InvocationVersion{}, false, ErrConflict
		}
		requested = pin.ReleaseID
	} else if err := validateRootWorkerPinHeaders(root.Headers, revision, requested); err != nil {
		return InvocationVersion{}, false, err
	}
	if revision != "" {
		return InvocationVersion{}, false, nil
	}
	if !pinned && requested == "" {
		var err error
		requested, err = m.selectProjectCanaryLocked(inv.AppID, rootID)
		if err != nil {
			return InvocationVersion{}, false, err
		}
	}
	graphID, depID, err := m.resolveProjectReleaseLocked(inv.AppID, "production", requested)
	if err != nil {
		return InvocationVersion{}, false, err
	}
	if graphID == "" {
		return InvocationVersion{Scope: "production"}, true, nil
	}
	if pinned && pin.DeploymentID != depID {
		return InvocationVersion{}, false, ErrConflict
	}
	if !pinned {
		if m.workerReleasePins == nil {
			m.workerReleasePins = map[string]projectWorkerReleasePin{}
		}
		m.workerReleasePins[rootID] = projectWorkerReleasePin{AppID: inv.AppID, BindingID: inv.QueueBindingID, ReleaseID: graphID, DeploymentID: depID}
	}
	return InvocationVersion{Scope: "production", ReleaseID: graphID, DeploymentID: depID}, true, nil
}
