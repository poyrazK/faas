package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ InstanceApplicationStandardBootStore = (*MemStore)(nil)
var _ ComputeNodeRuntimeIdentityStore = (*MemStore)(nil)

func (m *MemStore) RegisterComputeNodeRuntimeIdentity(ctx context.Context, identity runtimeadmission.Identity) error {
	if err := identity.Validate(); err != nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[identity.NodeID]; !ok {
		return ErrNotFound
	}
	if m.computeNodeRuntimeIncarnations == nil {
		m.computeNodeRuntimeIncarnations = map[string]string{}
	}
	m.computeNodeRuntimeIncarnations[identity.NodeID] = identity.Incarnation
	return nil
}

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
	if !ok || ins.State != expectedState || (expectedState != string(StateWaking) && expectedState != string(StateColdBooting)) {
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
	if !bytes.Equal(input, capture.inputs) || m.computeNodeRuntimeIncarnations[ins.NodeID] == "" {
		return Instance{}, InstanceApplicationStandardAdmission{}, ErrApplicationStandardRuntimeStale
	}
	return ins, capture, nil
}

func (m *MemStore) IssueInstanceApplicationStandardBoot(ctx context.Context, expectedState string, binding runtimeadmission.Binding) (runtimeadmission.Binding, error) {
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
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := validateStandardBootBinding(binding, capture, m.computeNodeRuntimeIncarnations[capture.NodeID], now); err != nil {
		return runtimeadmission.Binding{}, err
	}
	if old, ok := m.instanceApplicationStandardBoots[binding.Token]; ok {
		copy := binding
		copy.IssuedAtUnixNano, copy.ExpiresAtUnixNano = old.Binding.IssuedAtUnixNano, old.Binding.ExpiresAtUnixNano
		if copy != old.Binding || old.ExpectedState != expectedState || old.Binding.Validate(now) != nil {
			return runtimeadmission.Binding{}, ErrConflict
		}
		return old.Binding, nil
	}
	for _, old := range m.instanceApplicationStandardBoots {
		if old.Binding.InstanceID == binding.InstanceID {
			return runtimeadmission.Binding{}, ErrConflict
		}
	}
	binding.IssuedAtUnixNano, binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
	if m.instanceApplicationStandardBoots == nil {
		m.instanceApplicationStandardBoots = map[string]instanceStandardBoot{}
	}
	m.instanceApplicationStandardBoots[binding.Token] = instanceStandardBoot{Binding: binding, ExpectedState: expectedState}
	return binding, nil
}

func (m *MemStore) PublishInstanceApplicationStandardRuntime(ctx context.Context, expectedState string, next State, receipt runtimeadmission.Receipt) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if !standardRuntimeReceiptTarget(next, receipt) {
		return Instance{}, ErrInvalidArgument
	}
	if receipt.Check(receipt.Binding, time.Now()) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, capture, err := m.lockNativeBootInputsLocked(receipt.Binding.InstanceID, expectedState)
	if err != nil {
		return Instance{}, err
	}
	boot, ok := m.instanceApplicationStandardBoots[receipt.Binding.Token]
	if !ok || boot.ExpectedState != expectedState || boot.Binding != receipt.Binding {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := validateStandardBootBinding(boot.Binding, capture, m.computeNodeRuntimeIncarnations[capture.NodeID], now); err != nil {
		return Instance{}, err
	}
	if receipt.Check(boot.Binding, now) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if boot.Receipt != nil && *boot.Receipt != receipt {
		return Instance{}, ErrConflict
	}
	copy := receipt
	boot.Receipt = &copy
	boot.ReceivedAt = now
	// One mutex commit for receipt + runtime tuple + state; no observation is
	// advanced. All refusal checks finish before changing any owned map.
	ins.Netns, ins.HostIP, ins.GuestUID, ins.State, ins.StartedAt = receipt.Netns, receipt.HostIP, int(receipt.LeaseUID), string(next), now
	m.instanceApplicationStandardBoots[boot.Binding.Token] = boot
	if m.instanceApplicationStandardBootTokens == nil {
		m.instanceApplicationStandardBootTokens = map[string]string{}
	}
	m.instanceApplicationStandardBootTokens[ins.ID] = boot.Binding.Token
	m.instances[ins.ID] = ins
	return ins, nil
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
	if !ok || boot.Receipt == nil || boot.Binding.NodeID != ins.NodeID || boot.Binding.Incarnation != m.computeNodeRuntimeIncarnations[ins.NodeID] || boot.Binding.CapturedInputHash != capture.NativeInputHash {
		return ErrApplicationStandardRuntimeStale
	}
	r := boot.Receipt
	if r.Netns != ins.Netns || r.HostIP != ins.HostIP || int(r.LeaseUID) != ins.GuestUID || r.Paused != (ins.State == string(StateWarm)) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func (m *MemStore) deleteNativeInstanceInputsLocked(id string) {
	delete(m.instanceApplicationStandardAdmissions, id)
	delete(m.instanceApplicationStandardBootTokens, id)
	for token, boot := range m.instanceApplicationStandardBoots {
		if boot.Binding.InstanceID == id {
			delete(m.instanceApplicationStandardBoots, token)
		}
	}
}
