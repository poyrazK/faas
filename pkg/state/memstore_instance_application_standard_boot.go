package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ InstanceApplicationStandardBootStore = (*MemStore)(nil)
var _ ComputeNodeRuntimeIdentityStore = (*MemStore)(nil)

func (m *MemStore) LatestAppEgressPolicyRevision(ctx context.Context, id string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[id]; !ok {
		return 0, ErrNotFound
	}
	return m.appEgressRevisionLocked(id), nil
}

func (m *MemStore) appEgressRevisionLocked(id string) int64 {
	if revision := m.appEgressRevisions[canonicalStandardUUID(id)]; revision > 0 {
		return revision
	}
	return 1
}

func (m *MemStore) advanceAppEgressRevisionLocked(before, after App) {
	if slices.Equal(before.EgressAllowlist, after.EgressAllowlist) && slices.Equal(before.EgressPorts, after.EgressPorts) {
		return
	}
	if m.appEgressRevisions == nil {
		m.appEgressRevisions = map[string]int64{}
	}
	m.appEgressRevisions[canonicalStandardUUID(after.ID)] = m.appEgressRevisionLocked(before.ID) + 1
}

func memNativeCaptureHash(input []byte, nodeID string) string {
	hash := sha256.Sum256(append(append(append([]byte{}, input...), '\n'), []byte(nodeID)...))
	return hex.EncodeToString(hash[:])
}

func (m *MemStore) lockNativeBootInputsLocked(id, expectedState string) (Instance, InstanceApplicationStandardAdmission, error) {
	ins, ok := m.instances[id]
	if !ok || ins.State != expectedState {
		return Instance{}, InstanceApplicationStandardAdmission{}, ErrConflict
	}
	capture, ok := m.instanceApplicationStandardAdmissions[id]
	if !ok || capture.NodeID != ins.NodeID || !capture.Managed {
		return Instance{}, InstanceApplicationStandardAdmission{}, ErrApplicationStandardRuntimeStale
	}
	input, err := m.standardRuntimeSnapshotLocked(ins)
	if err != nil {
		return Instance{}, InstanceApplicationStandardAdmission{}, err
	}
	matches, err := standardNativeRuntimeInputsMatch(capture.inputs, input)
	if err != nil || !matches || m.computeNodeRuntimeIncarnations[ins.NodeID] == "" {
		return Instance{}, InstanceApplicationStandardAdmission{}, ErrApplicationStandardRuntimeStale
	}
	return ins, capture, nil
}

func (m *MemStore) IssueInstanceApplicationStandardBoot(ctx context.Context, expectedState string, binding runtimeadmission.Binding) (runtimeadmission.Binding, error) {
	if expectedState != string(StateWaking) && expectedState != string(StateColdBooting) {
		return runtimeadmission.Binding{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return runtimeadmission.Binding{}, err
	}
	if binding.Validate(time.Now()) != nil {
		return runtimeadmission.Binding{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, capture, err := m.lockNativeBootInputsLocked(binding.InstanceID, expectedState)
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	deadline, err := m.standardNativeArtifactDeadlineLocked(ctx, capture)
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := validateStandardBootBinding(binding, capture, m.computeNodeRuntimeIncarnations[capture.NodeID], m.computeNodeRuntimeProtocols[capture.NodeID], now); err != nil {
		return runtimeadmission.Binding{}, err
	}
	if err := m.checkStandardSnapshotRestoreLocked(binding, capture, now, nil); err != nil {
		return runtimeadmission.Binding{}, err
	}
	if old, ok := m.instanceApplicationStandardBoots[binding.Token]; ok {
		copy := binding
		copy.IssuedAtUnixNano, copy.ExpiresAtUnixNano = old.Binding.IssuedAtUnixNano, old.Binding.ExpiresAtUnixNano
		if copy != old.Binding || old.ExpectedState != expectedState || old.Binding.Validate(now) != nil || !standardNativeGrantWithinArtifactLease(old.Binding.ExpiresAtUnixNano, deadline) {
			return runtimeadmission.Binding{}, ErrConflict
		}
		return old.Binding, nil
	}
	for _, old := range m.instanceApplicationStandardBoots {
		if old.Binding.InstanceID == binding.InstanceID {
			return runtimeadmission.Binding{}, ErrConflict
		}
	}
	expires, err := standardNativeGrantExpiry(now, deadline)
	if err != nil {
		return runtimeadmission.Binding{}, err
	}
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = now.UnixNano(), expires.UnixNano()
	if m.instanceApplicationStandardBoots == nil {
		m.instanceApplicationStandardBoots = map[string]instanceStandardBoot{}
	}
	m.instanceApplicationStandardBoots[binding.Token] = instanceStandardBoot{Binding: binding, ExpectedState: expectedState}
	return binding, nil
}

func (m *MemStore) PublishInstanceApplicationStandardRuntime(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt) (Instance, error) {
	return m.publishStandardRuntimeWithConfig(ctx, expectedState, next, receipt, "", nil)
}

func (m *MemStore) PublishInstanceApplicationStandardRuntimeWithConfig(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt, wakeID string, inputs RuntimeConfigInputs) (Instance, error) {
	return m.publishStandardRuntimeWithConfig(ctx, expectedState, next, receipt, wakeID, &inputs)
}

func (m *MemStore) publishStandardRuntimeWithConfig(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt, wakeID string, inputs *RuntimeConfigInputs) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if !standardRuntimeReceiptTarget(next, receipt) {
		return Instance{}, ErrInvalidArgument
	}
	if receipt.Check(receipt.Binding, time.Now()) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if inputs != nil {
		if err := validateRuntimeConfigInputs(*inputs); err != nil {
			return Instance{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishStandardRuntimeLocked(ctx, expectedState, next, receipt, wakeID, inputs)
}

func (m *MemStore) prepareStandardRuntimeLocked(ctx context.Context, expectedState string, receipt runtimeadmission.Receipt) (Instance, instanceStandardBoot, error) {
	ins, capture, err := m.lockNativeBootInputsLocked(receipt.Binding.InstanceID, expectedState)
	if err != nil {
		return Instance{}, instanceStandardBoot{}, err
	}
	deadline, err := m.standardNativeArtifactDeadlineLocked(ctx, capture)
	if err != nil {
		return Instance{}, instanceStandardBoot{}, err
	}
	if !standardNativeGrantWithinArtifactLease(receipt.Binding.ExpiresAtUnixNano, deadline) {
		return Instance{}, instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
	}
	boot, ok := m.instanceApplicationStandardBoots[receipt.Binding.Token]
	if !ok || boot.ExpectedState != expectedState || boot.Binding != receipt.Binding {
		return Instance{}, instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := validateStandardBootBinding(boot.Binding, capture, m.computeNodeRuntimeIncarnations[capture.NodeID], m.computeNodeRuntimeProtocols[capture.NodeID], now); err != nil {
		return Instance{}, instanceStandardBoot{}, err
	}
	if err := m.checkStandardSnapshotRestoreLocked(boot.Binding, capture, now, &receipt); err != nil {
		return Instance{}, instanceStandardBoot{}, err
	}
	if receipt.Check(boot.Binding, now) != nil {
		return Instance{}, instanceStandardBoot{}, ErrApplicationStandardRuntimeStale
	}
	if boot.Receipt != nil && !boot.Receipt.Equal(receipt) {
		return Instance{}, instanceStandardBoot{}, ErrConflict
	}
	return ins, boot, nil
}

func (m *MemStore) publishStandardRuntimeLocked(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt, wakeID string, inputs *RuntimeConfigInputs) (Instance, error) {
	ins, boot, err := m.prepareStandardRuntimeLocked(ctx, expectedState, receipt)
	if err != nil {
		return Instance{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	published := ins
	published.Netns, published.HostIP, published.GuestUID, published.State, published.StartedAt = receipt.Netns, receipt.HostIP, int(receipt.LeaseUID), string(next), now
	if inputs != nil {
		if err := m.checkInstanceRuntimeConfigReceiptLocked(published, wakeID, *inputs); err != nil {
			return Instance{}, err
		}
	}
	if err := m.checkStandardNativeRuntimeTransitionLocked(ins, published); err != nil {
		return Instance{}, err
	}
	copy := receipt.Clone()
	boot.Receipt, boot.ReceivedAt = &copy, now
	// Every refusal finishes before the single mutex commit.
	m.instanceApplicationStandardBoots[boot.Binding.Token] = boot
	if m.instanceApplicationStandardBootTokens == nil {
		m.instanceApplicationStandardBootTokens = map[string]string{}
	}
	m.instanceApplicationStandardBootTokens[ins.ID] = boot.Binding.Token
	m.instances[ins.ID] = published
	if inputs != nil {
		m.installInstanceRuntimeConfigReceiptLocked(ins.ID, ins.WakeID, *inputs)
	}
	return published, nil
}

// ADR-435: native receipts authorize standards publication, while the common
// capacity and exclusive-owner guards still govern every lifecycle commit.
// Capacity preflight has no side effects; ownership can change only once all
// refusal checks have passed and the caller has no fallible work remaining.
func (m *MemStore) checkStandardNativeRuntimeTransitionLocked(old, next Instance) error {
	if err := m.guardQualificationRuntimeTransitionLocked(old, next); err != nil {
		return err
	}
	if err := m.checkServiceCapacityInstanceLocked(next); err != nil {
		return err
	}
	return m.exclusiveRuntimeTransitionLocked(old, next)
}

func (m *MemStore) guardNativeRuntimeReceiptLocked(ins Instance, capture InstanceApplicationStandardAdmission) error {
	if !capture.Managed {
		return nil
	}
	if ins.State == string(StateWaking) || ins.State == string(StateColdBooting) {
		for _, boot := range m.instanceApplicationStandardBoots {
			if boot.Binding.InstanceID == ins.ID && boot.Receipt != nil {
				return ErrApplicationStandardRuntimeStale
			}
		}
	}
	if ins.State != string(StateRunning) && ins.State != string(StateWarm) && ins.State != string(StateMigrating) && ins.Netns == "" && ins.HostIP == "" && ins.GuestUID <= 0 {
		return nil
	}
	boot, ok := m.instanceApplicationStandardBoots[m.instanceApplicationStandardBootTokens[ins.ID]]
	if !ok || boot.Receipt == nil || boot.Binding.NodeID != ins.NodeID || boot.Binding.Incarnation != m.computeNodeRuntimeIncarnations[ins.NodeID] || boot.Binding.ProtocolVersion > m.computeNodeRuntimeProtocols[ins.NodeID] || boot.Binding.CapturedInputHash != capture.NativeInputHash {
		return ErrApplicationStandardRuntimeStale
	}
	r := boot.Receipt
	if token := m.instanceApplicationStandardPromotionTokens[ins.ID]; token != "" {
		promotion, exists := m.instanceApplicationStandardPromotions[token]
		if !exists || promotion.Receipt == nil || !promotion.Grant.Parent.Equal(*boot.Receipt) || promotion.Grant.Binding.NodeID != ins.NodeID || promotion.Grant.Binding.Incarnation != m.computeNodeRuntimeIncarnations[ins.NodeID] {
			return ErrApplicationStandardRuntimeStale
		}
		r = promotion.Receipt
	}
	if r.Netns != ins.Netns || r.HostIP != ins.HostIP || int(r.LeaseUID) != ins.GuestUID || r.Paused != (ins.State == string(StateWarm)) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (m *MemStore) deleteNativeInstanceInputsLocked(id string) {
	delete(m.runtimeInstanceConfigProofs, id)
	delete(m.instanceApplicationStandardPromotionTokens, id)
	for token, promotion := range m.instanceApplicationStandardPromotions {
		if promotion.Grant.Binding.InstanceID == id {
			delete(m.instanceApplicationStandardPromotions, token)
		}
	}
	delete(m.instanceApplicationStandardAdmissions, id)
	delete(m.instanceApplicationStandardBootTokens, id)
	for token, boot := range m.instanceApplicationStandardBoots {
		if boot.Binding.InstanceID == id {
			delete(m.instanceApplicationStandardBoots, token)
		}
	}
}
