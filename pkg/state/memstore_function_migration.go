package state

// adr: 20261018030613

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ FunctionMigrationSourceStore = (*MemStore)(nil)

func (m *MemStore) RegisterFunctionMigrationSource(_ context.Context, source FunctionMigrationSource, memory, vmstate string) error {
	if err := validateFunctionMigrationSource(source); err != nil {
		return err
	}
	if memory == "" || vmstate == "" || memory == vmstate {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerFunctionMigrationSourceLocked(source, memory, vmstate)
}

func (m *MemStore) registerFunctionMigrationSourceLocked(source FunctionMigrationSource, memory, vmstate string) error {
	lease, ok := m.migrationLeases[source.LeaseToken]
	ins, exists := m.instances[source.InstanceID]
	if !ok || !exists || lease.InstanceID != source.InstanceID || lease.SourceNodeID != source.SourceNodeID ||
		ins.NodeID != source.SourceNodeID || ins.WakeID != source.SourceWakeID || ins.State != string(StateRunning) || ins.LeaseToken != "" {
		return ErrConflict
	}
	if m.functionMigrationSources == nil {
		m.functionMigrationSources = make(map[string]FunctionMigrationSource)
	}
	if _, exists := m.functionMigrationSources[source.InstanceID]; exists {
		return ErrConflict
	}
	lease.MemStorageKey, lease.VMStateStorageKey = memory, vmstate
	m.migrationLeases[source.LeaseToken] = lease
	source.Phase = FunctionMigrationPreparing
	m.functionMigrationSources[source.InstanceID] = source
	return nil
}

func (m *MemStore) PrepareFunctionMigrationSource(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.functionMigrationSources[id]
	if !ok || source.LeaseToken != token {
		return ErrNotFound
	}
	if source.Phase == FunctionMigrationRecovering {
		return nil
	}
	if source.Phase != FunctionMigrationPreparing {
		return ErrConflict
	}
	source.Phase = FunctionMigrationPrepared
	m.functionMigrationSources[id] = source
	return nil
}

func (m *MemStore) GetFunctionMigrationSource(_ context.Context, id, token string) (FunctionMigrationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.functionMigrationSources[id]
	if !ok || source.LeaseToken != token {
		return FunctionMigrationSource{}, ErrNotFound
	}
	return source, nil
}

func (m *MemStore) GetFunctionMigrationSourceByInstance(_ context.Context, id string) (FunctionMigrationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.functionMigrationSources[id]
	if !ok {
		return FunctionMigrationSource{}, ErrNotFound
	}
	return source, nil
}

func (m *MemStore) ListFunctionMigrationSourceInstances(_ context.Context, app string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []string
	for id := range m.functionMigrationSources {
		if m.instances[id].AppID == app {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (m *MemStore) markFunctionMigrationSourceLocked(ctx context.Context, ins Instance, token string) error {
	source, ok := m.functionMigrationSources[ins.ID]
	if !ok && !strings.HasPrefix(token, FunctionMigrationTokenPrefix) {
		return nil
	}
	lease, held := m.migrationLeases[token]
	if !ok || !held || source.LeaseToken != token || source.Phase != FunctionMigrationPrepared || source.SourceWakeID != ins.WakeID ||
		source.SourceNodeID != ins.NodeID || lease.Pending || !lease.LeaseExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	if source.CaptureToken != "" {
		permission, admitted := m.functionMigrationPermissions[token]
		if !admitted || permission.capture.Validate(time.Now()) != nil {
			return ErrConflict
		}
		_, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, ins.ID, string(StateRunning))
		if err != nil {
			return err
		}
		if err := checkFunctionMigrationPermission(permission.capture, parent, ins.StartedAt.UnixNano(), lease, time.Now(), deadline); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) FenceFunctionMigrationSource(ctx context.Context, id, token string) (MigrationCommitResolution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, exists := m.instances[id]
	if !exists {
		return MigrationCommitObsolete, nil
	}
	source, ok := m.functionMigrationSources[id]
	if !ok || source.LeaseToken != token {
		return MigrationCommitRetained, ErrNotFound
	}
	resolution := classifyFunctionMigrationSource(ins, source)
	if resolution != MigrationCommitAborted {
		return resolution, nil
	}
	if source.Phase == FunctionMigrationRecovered || source.Phase == FunctionMigrationRetired {
		return resolution, nil
	}
	previous := ins
	ins.State, ins.LeaseToken = string(StateMigrating), FunctionMigrationRollbackToken(token)
	if ins.MigrationStartedAt == nil {
		now := time.Now().UTC()
		ins.MigrationStartedAt = &now
	}
	if previous.State != string(StateMigrating) {
		if err := m.guardInstanceRuntimeTransitionLocked(ctx, previous, ins); err != nil {
			return MigrationCommitRetained, err
		}
	}
	source.Phase = FunctionMigrationRecovering
	m.instances[id], m.functionMigrationSources[id] = ins, source
	return MigrationCommitAborted, nil
}

func (m *MemStore) CompleteFunctionMigrationSource(ctx context.Context, id, token string, retired bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, exists := m.instances[id]
	if !exists {
		return ErrNotFound
	}
	source, ok := m.functionMigrationSources[id]
	if !ok || source.LeaseToken != token {
		return ErrNotFound
	}
	phase, target := FunctionMigrationRecovered, string(StateRunning)
	if retired {
		phase, target = FunctionMigrationRetired, string(StateStopped)
	}
	if ins.WakeID != source.SourceWakeID || ins.NodeID != source.SourceNodeID {
		return ErrConflict
	}
	if source.Phase == phase && ins.State == target && ins.LeaseToken == "" {
		return nil
	}
	if source.Phase != FunctionMigrationRecovering || ins.State != string(StateMigrating) || ins.LeaseToken != FunctionMigrationRollbackToken(token) {
		return ErrConflict
	}
	if !retired && source.CaptureToken != "" {
		permission := m.functionMigrationPermissions[token].recovery
		if permission == nil || permission.Purpose != runtimeadmission.FunctionMigrationRecover {
			return ErrConflict
		}
		if err := m.checkFunctionMigrationGrantLocked(ctx, permission.Clone(), true); err != nil {
			return err
		}
	}
	if !retired {
		if err := m.checkNodeReservationLocked(source.SourceNodeID, string(StateRunning), ins.RAMMB); err != nil {
			return err
		}
	}
	next := ins
	next.State, next.LeaseToken, next.MigrationStartedAt = target, "", nil
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, ins, next); err != nil {
		return err
	}
	source.Phase = phase
	m.instances[id], m.functionMigrationSources[id] = next, source
	return nil
}

func (m *MemStore) ListExpiredFunctionMigrationSources(_ context.Context, now time.Time, maxRows int) ([]FunctionMigrationSource, error) {
	if maxRows <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FunctionMigrationSource
	for _, source := range m.functionMigrationSources {
		lease, ok := m.migrationLeases[source.LeaseToken]
		if ok && !lease.LeaseExpiresAt.After(now) {
			out = append(out, source)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := m.migrationLeases[out[i].LeaseToken].LeaseExpiresAt, m.migrationLeases[out[j].LeaseToken].LeaseExpiresAt
		if a.Equal(b) {
			return out[i].InstanceID < out[j].InstanceID
		}
		return a.Before(b)
	})
	if len(out) > maxRows {
		out = out[:maxRows]
	}
	return out, nil
}
