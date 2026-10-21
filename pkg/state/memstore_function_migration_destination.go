package state

// adr: 20261018030615

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ FunctionMigrationDestinationStore = (*MemStore)(nil)

func (m *MemStore) RecordFunctionMigrationCapture(ctx context.Context, g runtimeadmission.FunctionMigrationGrant, c runtimeadmission.SnapshotCapture) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkFunctionMigrationGrantLocked(ctx, g, false); err != nil {
		return err
	}
	if err := checkFunctionMigrationCaptureResult(g, c, time.Now()); err != nil {
		return err
	}
	p := m.functionMigrationPermissions[g.LeaseToken]
	owned := c.Clone()
	p.result = &owned
	s := m.functionMigrationSources[g.Parent.Binding.InstanceID]
	s.Phase = FunctionMigrationPrepared
	m.functionMigrationPermissions[g.LeaseToken], m.functionMigrationSources[s.InstanceID] = p, s
	return nil
}

func (m *MemStore) functionDestinationInputsLocked(ctx context.Context, id, token string) (Instance, runtimeadmission.Receipt, time.Time, FunctionMigrationSource, MigrationLease, error) {
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, id, string(StateMigrating))
	if err != nil {
		return ins, parent, deadline, FunctionMigrationSource{}, MigrationLease{}, err
	}
	source, found := m.functionMigrationSources[id]
	lease, held := m.migrationLeases[token]
	if !found || !held || source.LeaseToken != token {
		return ins, parent, deadline, source, lease, ErrNotFound
	}
	return ins, parent, deadline, source, lease, functionDestinationOwner(ins, source, lease)
}

func (m *MemStore) IssueFunctionMigrationDestination(ctx context.Context, req FunctionMigrationDestinationRequest) (runtimeadmission.FunctionMigrationDestinationGrant, error) {
	if err := ctx.Err(); err != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, parent, deadline, source, lease, err := m.functionDestinationInputsLocked(ctx, req.InstanceID, req.LeaseToken)
	if err != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, err
	}
	p, ok := m.functionMigrationPermissions[req.LeaseToken]
	if !ok || p.result == nil || p.result.Memory.Bytes != int64(ins.RAMMB)<<20 || !p.capture.Parent.Equal(parent) || p.capture.SourceStartedAtUnixNano != ins.StartedAt.UnixNano() || source.CaptureToken != p.capture.CaptureToken ||
		req.Identity.Incarnation != m.computeNodeRuntimeIncarnations[req.Identity.NodeID] || req.Identity.ProtocolVersion != m.computeNodeRuntimeProtocols[req.Identity.NodeID] {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, ErrApplicationStandardRuntimeStale
	}
	capture := m.instanceApplicationStandardAdmissions[ins.ID]
	g, err := newFunctionMigrationDestination(req, p.capture, *p.result, lease, memNativeCaptureHash(capture.inputs, req.Identity.NodeID), time.Now().UTC().Truncate(time.Microsecond), deadline)
	if err != nil {
		return g, err
	}
	if old, found := m.functionMigrationDestinations[req.LeaseToken]; found {
		candidate := g.Clone()
		candidate.Binding.Token = old.Grant.Binding.Token
		candidate.Binding.IssuedAtUnixNano, candidate.Binding.ExpiresAtUnixNano = old.Grant.Binding.IssuedAtUnixNano, old.Grant.Binding.ExpiresAtUnixNano
		if !candidate.Equal(old.Grant) || old.Phase != FunctionDestinationIssued && old.Phase != FunctionDestinationReady || m.checkFunctionDestinationLocked(ctx, old.Grant) != nil {
			return g, ErrConflict
		}
		return old.Grant.Clone(), nil
	}
	for _, prior := range m.functionMigrationDestinations {
		if prior.Grant.Binding.InstanceID == ins.ID && prior.Grant.WakeID == req.WakeID {
			return runtimeadmission.FunctionMigrationDestinationGrant{}, ErrConflict
		}
	}
	if err := m.functionDestinationCapacityLocked(req.Identity.NodeID, "", m.functionDestinationShapeLocked(ins)); err != nil {
		return runtimeadmission.FunctionMigrationDestinationGrant{}, err
	}
	if m.functionMigrationDestinations == nil {
		m.functionMigrationDestinations = make(map[string]FunctionMigrationDestination)
	}
	m.functionMigrationDestinations[req.LeaseToken] = FunctionMigrationDestination{Grant: g.Clone(), Phase: FunctionDestinationIssued}
	return g, nil
}

func (m *MemStore) GetFunctionMigrationDestination(ctx context.Context, id, token string) (FunctionMigrationDestination, error) {
	if err := ctx.Err(); err != nil {
		return FunctionMigrationDestination{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.functionMigrationDestinations[token]
	if !ok || d.Grant.Binding.InstanceID != id {
		return d, ErrNotFound
	}
	return d.Clone(), nil
}

func (m *MemStore) checkFunctionDestinationLocked(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	ins, parent, deadline, _, lease, err := m.functionDestinationInputsLocked(ctx, g.Binding.InstanceID, g.Source.LeaseToken)
	if err != nil {
		return err
	}
	d, ok := m.functionMigrationDestinations[g.Source.LeaseToken]
	p := m.functionMigrationPermissions[g.Source.LeaseToken]
	if !ok || !d.Grant.Equal(g) || d.Phase != FunctionDestinationIssued && d.Phase != FunctionDestinationReady || p.result == nil || !p.result.Equal(g.Restore.Capture) || !p.capture.Equal(g.Source) ||
		g.Validate(time.Now()) != nil || !g.Source.Parent.Equal(parent) || g.Source.SourceStartedAtUnixNano != ins.StartedAt.UnixNano() ||
		g.Source.LeaseCreatedAtUnixNano != lease.CreatedAt.UnixNano() || g.Source.MemoryKey != lease.MemStorageKey || g.Source.VMStateKey != lease.VMStateStorageKey ||
		g.Binding.ExpiresAtUnixNano > lease.LeaseExpiresAt.UnixNano() || !standardNativeGrantWithinArtifactLease(g.Binding.ExpiresAtUnixNano, deadline) ||
		g.Binding.Incarnation != m.computeNodeRuntimeIncarnations[g.Binding.NodeID] || g.Binding.ProtocolVersion != m.computeNodeRuntimeProtocols[g.Binding.NodeID] ||
		g.Binding.CapturedInputHash != memNativeCaptureHash(m.instanceApplicationStandardAdmissions[ins.ID].inputs, g.Binding.NodeID) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (m *MemStore) CheckFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkFunctionDestinationLocked(ctx, g)
}
func (m *MemStore) ReadyFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant, r runtimeadmission.Receipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkFunctionDestinationLocked(ctx, g); err != nil {
		return err
	}
	if g.CheckReceipt(r, time.Now()) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	d := m.functionMigrationDestinations[g.Source.LeaseToken]
	if d.Receipt != nil && !d.Receipt.Equal(r) {
		return ErrConflict
	}
	owned := r.Clone()
	d.Phase, d.Receipt = FunctionDestinationReady, &owned
	m.functionMigrationDestinations[g.Source.LeaseToken] = d
	return nil
}

func functionDestinationRetirement(ins Instance, source FunctionMigrationSource, d FunctionMigrationDestination, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if !d.Grant.Equal(g) || d.Phase == FunctionDestinationCommitted {
		return ErrConflict
	}
	if d.Phase == FunctionDestinationRetired {
		return nil
	}
	if ins.ID == g.Binding.InstanceID && ins.NodeID == g.Source.Parent.Binding.NodeID && ins.WakeID == g.Source.SourceWakeID &&
		(source.Phase == FunctionMigrationRecovering && ins.State == string(StateMigrating) && ins.LeaseToken == FunctionMigrationRollbackToken(g.Source.LeaseToken) ||
			ins.State == string(StateStopped) || ins.State == string(StateFailed) || ins.State == string(StateEvictingAccountDeleting)) {
		return nil
	}
	return ErrConflict
}
func (m *MemStore) CheckFunctionMigrationDestinationRetirement(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.functionMigrationDestinations[g.Source.LeaseToken]
	if !ok {
		return ErrNotFound
	}
	return functionDestinationRetirement(m.instances[g.Binding.InstanceID], m.functionMigrationSources[g.Binding.InstanceID], d, g)
}
func (m *MemStore) RetireFunctionMigrationDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.functionMigrationDestinations[g.Source.LeaseToken]
	if !ok {
		return ErrNotFound
	}
	if err := functionDestinationRetirement(m.instances[g.Binding.InstanceID], m.functionMigrationSources[g.Binding.InstanceID], d, g); err != nil {
		return err
	}
	d.Phase = FunctionDestinationRetired
	m.functionMigrationDestinations[g.Source.LeaseToken] = d
	return nil
}
func (m *MemStore) CheckFunctionMigrationDestinationCommitted(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.functionMigrationDestinations[g.Source.LeaseToken]
	ins := m.instances[g.Binding.InstanceID]
	if !ok || !d.Grant.Equal(g) || d.Phase != FunctionDestinationCommitted || ins.State != string(StateRunning) || ins.NodeID != g.Binding.NodeID || ins.WakeID != g.WakeID {
		return ErrConflict
	}
	_, parent, _, err := m.lockStandardSnapshotCaptureLocked(ctx, ins.ID, string(StateRunning))
	if err != nil {
		return err
	}
	if d.Receipt == nil || !parent.Equal(*d.Receipt) {
		return ErrConflict
	}
	return nil
}

// The original capture remains immutable. A committed physical epoch supplies
// its node-specific hash and receipt to subsequent native lifecycle checks.
func (m *MemStore) functionDestinationReceiptLocked(ins Instance) *runtimeadmission.Receipt {
	for _, d := range m.functionMigrationDestinations {
		if d.Phase == FunctionDestinationCommitted && d.Grant.Binding.InstanceID == ins.ID && d.Grant.Binding.NodeID == ins.NodeID && d.Grant.WakeID == ins.WakeID && d.Receipt != nil {
			r := d.Receipt.Clone()
			return &r
		}
	}
	return nil
}
func (m *MemStore) effectiveNativeCaptureLocked(ins Instance, capture InstanceApplicationStandardAdmission) InstanceApplicationStandardAdmission {
	if m.functionDestinationReceiptLocked(ins) != nil {
		capture.NodeID = ins.NodeID
		capture.NativeInputHash = memNativeCaptureHash(capture.inputs, ins.NodeID)
	}
	return capture
}

func (m *MemStore) commitFunctionDestinationLocked(ctx context.Context, id, from, to, token string, input RuntimeConfigMigration) error {
	d, ok := m.functionMigrationDestinations[token]
	if !ok || d.Grant.Binding.InstanceID != id || d.Grant.Source.Parent.Binding.NodeID != from || d.Grant.Binding.NodeID != to {
		return ErrConflict
	}
	if err := m.checkFunctionDestinationLocked(ctx, d.Grant); err != nil {
		return err
	}
	if err := checkFunctionDestinationRuntime(input, d); err != nil {
		return err
	}
	if normalizedDeploymentScope(m.deployments[m.instances[id].DeploymentID].Scope) != input.Inputs.Scope {
		return ErrConflict
	}
	if !m.runtimeConfigInputsFreshLocked(m.instances[id].AppID, *input.Inputs) {
		return ErrApplicationStandardRuntimeStale
	}
	if err := m.checkNodeReservationLocked(to, string(StateRunning), m.instances[id].RAMMB); err != nil {
		return err
	}
	old := m.instances[id]
	next := old
	now := time.Now().UTC()
	next.NodeID, next.WakeID, next.State, next.StartedAt = to, input.WakeID, string(StateRunning), now
	next.Netns, next.HostIP, next.GuestUID = input.Netns, input.HostIP, input.GuestUID
	next.MigratedFromNodeID, next.MigratedAt, next.MigrationStartedAt = &from, &now, nil
	d.Phase = FunctionDestinationCommitted
	m.functionMigrationDestinations[token] = d
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, old, next); err != nil {
		d.Phase = FunctionDestinationReady
		m.functionMigrationDestinations[token] = d
		return err
	}
	m.instances[id] = next
	if m.instanceRuntimeConfigReceipts == nil {
		m.instanceRuntimeConfigReceipts = make(map[string]instanceRuntimeConfigReceipt)
	}
	m.instanceRuntimeConfigReceipts[id] = instanceRuntimeConfigReceipt{WakeID: next.WakeID, Inputs: cloneRuntimeConfigInputs(*input.Inputs)}
	app := m.apps[next.AppID]
	app.MigratedAt = &now
	m.apps[app.ID] = app
	return nil
}

func (m *MemStore) functionDestinationRetiredLocked(token string) bool {
	d, ok := m.functionMigrationDestinations[token]
	return !ok || d.Phase == FunctionDestinationRetired
}

func (m *MemStore) GetFunctionMigrationCapture(ctx context.Context, id, token string) (runtimeadmission.SnapshotRestoreEvidence, error) {
	if err := ctx.Err(); err != nil {
		return runtimeadmission.SnapshotRestoreEvidence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.functionMigrationSources[id]
	p, found := m.functionMigrationPermissions[token]
	if !ok || !found || s.LeaseToken != token || p.result == nil {
		return runtimeadmission.SnapshotRestoreEvidence{}, ErrNotFound
	}
	return runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: p.capture.CaptureToken, FCVersion: p.capture.FCVersion, Capture: p.result.Clone()}, nil
}

func (m *MemStore) functionDestinationCapacityLocked(nodeID, excludeID string, extra capacityResources) error {
	node, ok := m.computeNodes[nodeID]
	if !ok || !node.Active || node.Lifecycle != "active" || node.AdmissionCeilingMB <= 0 || node.VPCPUs <= 0 {
		return ErrNodeCapacity
	}
	used := m.migrationNodeUsedLocked(nodeID, excludeID)
	budget := node.VCPUBudget
	if budget <= 0 {
		budget = api.VCPUSlots
	}
	if used.RAM+extra.RAM > int64(node.AdmissionCeilingMB) || used.CPU+extra.CPU > int64(node.VPCPUs)*1000*api.CPUOvercommit || used.VCPU+extra.VCPU > int64(budget) {
		return ErrNodeCapacity
	}
	return nil
}

// Both protocols reserve the same node budget. After ownership commits, the
// original function source remains physical residency until its lease cleanup.
func (m *MemStore) functionMigrationNodePendingLocked(node string) bool {
	u := m.functionMigrationNodeUsageLocked(node)
	return u.RAM != 0 || u.CPU != 0 || u.VCPU != 0
}

func (m *MemStore) functionMigrationNodeUsageLocked(node string) capacityResources {
	var used capacityResources
	for token, d := range m.functionMigrationDestinations {
		source, held := m.functionMigrationSources[d.Grant.Binding.InstanceID]
		pendingDestination := d.Grant.Binding.NodeID == node && (d.Phase == FunctionDestinationIssued || d.Phase == FunctionDestinationReady)
		pendingSource := d.Phase == FunctionDestinationCommitted && held && source.LeaseToken == token && source.SourceNodeID == node && source.Phase != FunctionMigrationRecovered && source.Phase != FunctionMigrationRetired
		if pendingDestination || pendingSource {
			c := m.functionDestinationShapeLocked(m.instances[d.Grant.Binding.InstanceID])
			used.RAM += c.RAM
			used.CPU += c.CPU
			used.VCPU += c.VCPU
		}
	}
	return used
}
func (m *MemStore) functionDestinationShapeLocked(ins Instance) capacityResources {
	if shape, ok := m.capacityInstanceResources[ins.ID]; ok {
		return shape
	}
	app := m.apps[ins.AppID]
	ram, cpu := capacitySidecarResources(m.deployments[ins.DeploymentID].Sidecars)
	limits, ok := api.LimitsFor(m.accounts[app.AccountID].Plan)
	if !ok {
		return capacityResources{RAM: int64(api.RAMAdmissionCeilingMB), CPU: int64(api.RAMAdmissionCeilingMB), VCPU: int64(api.VCPUSlots)}
	}
	return capacityResources{RAM: int64(ins.RAMMB+api.PerVMOverheadMB) + ram, CPU: int64(max(app.CPUMillicores, api.DefaultAppCPUMillicores)) + cpu, VCPU: int64(limits.VCPU)}
}
func (m *MemStore) functionDestinationInstanceCapacityLocked(ins Instance) error {
	if !capacityResident(State(ins.State)) && State(ins.State) != StateFrozen {
		return nil
	}
	for _, d := range m.functionMigrationDestinations {
		if d.Grant.Binding.NodeID == ins.NodeID && (d.Phase == FunctionDestinationIssued || d.Phase == FunctionDestinationReady) {
			return m.functionDestinationCapacityLocked(ins.NodeID, ins.ID, m.functionDestinationShapeLocked(ins))
		}
	}
	return nil
}
