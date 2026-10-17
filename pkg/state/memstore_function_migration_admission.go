package state

// adr: 1576

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ FunctionMigrationAdmissionStore = (*MemStore)(nil)

func (m *MemStore) IssueFunctionMigrationCapture(ctx context.Context, req FunctionMigrationCaptureRequest) (runtimeadmission.FunctionMigrationGrant, error) {
	if err := validateFunctionMigrationSource(req.Source); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, req.Source.InstanceID, string(StateRunning))
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	lease, found := m.migrationLeases[req.Source.LeaseToken]
	if !found {
		return runtimeadmission.FunctionMigrationGrant{}, ErrNotFound
	}
	if err := functionMigrationPermissionOwner(ins, req.Source, lease, false); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	grant, err := newFunctionMigrationCapture(req, lease, parent, ins.StartedAt.UnixNano(), now, deadline)
	if err != nil {
		return grant, err
	}
	if old, exists := m.functionMigrationSources[ins.ID]; exists {
		permissions, held := m.functionMigrationPermissions[old.LeaseToken]
		candidate := grant
		candidate.IssuedAtUnixNano, candidate.ExpiresAtUnixNano = permissions.capture.IssuedAtUnixNano, permissions.capture.ExpiresAtUnixNano
		if !held || old.LeaseToken != grant.LeaseToken || old.Phase != FunctionMigrationPreparing || !candidate.Equal(permissions.capture) ||
			checkFunctionMigrationPermission(permissions.capture, parent, ins.StartedAt.UnixNano(), lease, now, deadline) != nil {
			return runtimeadmission.FunctionMigrationGrant{}, ErrConflict
		}
		return permissions.capture.Clone(), nil
	}
	if err := m.registerFunctionMigrationSourceLocked(req.Source, req.MemoryKey, req.VMStateKey); err != nil {
		return grant, err
	}
	source := m.functionMigrationSources[ins.ID]
	source.CaptureToken = grant.CaptureToken
	m.functionMigrationSources[ins.ID] = source
	if m.functionMigrationPermissions == nil {
		m.functionMigrationPermissions = make(map[string]functionMigrationPermissions)
	}
	m.functionMigrationPermissions[source.LeaseToken] = functionMigrationPermissions{capture: grant.Clone()}
	return grant, nil
}

func (m *MemStore) checkFunctionMigrationGrantLocked(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant, recovering bool) error {
	expected := string(StateRunning)
	if recovering {
		expected = string(StateMigrating)
	}
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, grant.Parent.Binding.InstanceID, expected)
	if err != nil {
		return err
	}
	source, found := m.functionMigrationSources[ins.ID]
	lease, held := m.migrationLeases[grant.LeaseToken]
	permissions, admitted := m.functionMigrationPermissions[grant.LeaseToken]
	if !found || !held || !admitted || source.CaptureToken != grant.CaptureToken {
		return ErrApplicationStandardRuntimeStale
	}
	if err := functionMigrationPermissionOwner(ins, source, lease, recovering); err != nil {
		return err
	}
	if recovering && !m.functionDestinationRetiredLocked(grant.LeaseToken) {
		return ErrConflict
	}
	stored := permissions.capture
	if recovering {
		if permissions.recovery == nil {
			return ErrApplicationStandardRuntimeStale
		}
		stored = permissions.recovery.Clone()
	} else if source.Phase != FunctionMigrationPreparing {
		return ErrConflict
	}
	if !stored.Equal(grant) || (grant.Purpose == runtimeadmission.FunctionMigrationRecover) != recovering {
		return ErrApplicationStandardRuntimeStale
	}
	return checkFunctionMigrationPermission(grant, parent, ins.StartedAt.UnixNano(), lease, time.Now().UTC(), deadline)
}

func (m *MemStore) CheckFunctionMigrationCapture(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkFunctionMigrationGrantLocked(ctx, grant, false)
}

func (m *MemStore) CheckFunctionMigrationRecovery(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkFunctionMigrationGrantLocked(ctx, grant, true)
}

func (m *MemStore) IssueFunctionMigrationRecovery(ctx context.Context, id, token string) (runtimeadmission.FunctionMigrationGrant, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil || parsed.String() != id {
		return runtimeadmission.FunctionMigrationGrant{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, id, string(StateMigrating))
	if err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	source, found := m.functionMigrationSources[id]
	lease, held := m.migrationLeases[token]
	permissions, admitted := m.functionMigrationPermissions[token]
	if !found || !held || !admitted || source.LeaseToken != token {
		return runtimeadmission.FunctionMigrationGrant{}, ErrNotFound
	}
	if err := functionMigrationPermissionOwner(ins, source, lease, true); err != nil {
		return runtimeadmission.FunctionMigrationGrant{}, err
	}
	if !m.functionDestinationRetiredLocked(token) {
		return runtimeadmission.FunctionMigrationGrant{}, ErrConflict
	}
	capture := permissions.capture
	if !capture.Parent.Equal(parent) || capture.SourceStartedAtUnixNano != ins.StartedAt.UnixNano() ||
		capture.LeaseCreatedAtUnixNano != lease.CreatedAt.UnixNano() || capture.MemoryKey != lease.MemStorageKey || capture.VMStateKey != lease.VMStateStorageKey {
		return runtimeadmission.FunctionMigrationGrant{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if old := permissions.recovery; old != nil && old.SameSource(capture) &&
		checkFunctionMigrationPermission(*old, parent, ins.StartedAt.UnixNano(), lease, now, deadline) == nil {
		return old.Clone(), nil // Duplicate RPCs must not revoke an in-flight ACK.
	}
	grant, err := newFunctionMigrationRecovery(capture, lease, now, deadline)
	if err != nil {
		return grant, err
	}
	owned := grant.Clone()
	permissions.recovery = &owned
	m.functionMigrationPermissions[token] = permissions
	return grant, nil
}
