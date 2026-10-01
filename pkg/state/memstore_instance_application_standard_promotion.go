package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ InstanceApplicationStandardPromotionStore = (*MemStore)(nil)

func (m *MemStore) lockNativePromotionLocked(id string, allowRunning bool) (Instance, InstanceApplicationStandardAdmission, runtimeadmission.Receipt, error) {
	ins, ok := m.instances[id]
	if !ok || (ins.State != string(StateWarm) && !(allowRunning && ins.State == string(StateRunning))) {
		return Instance{}, InstanceApplicationStandardAdmission{}, runtimeadmission.Receipt{}, ErrConflict
	}
	ins, capture, err := m.lockNativeBootInputsLocked(id, ins.State)
	if err != nil {
		return Instance{}, capture, runtimeadmission.Receipt{}, err
	}
	boot, ok := m.instanceApplicationStandardBoots[m.instanceApplicationStandardBootTokens[id]]
	if !ok || boot.Receipt == nil || !boot.Receipt.Paused || boot.Receipt.Netns != ins.Netns || boot.Receipt.HostIP != ins.HostIP || int(boot.Receipt.LeaseUID) != ins.GuestUID || (ins.State == string(StateWarm) && m.instanceApplicationStandardPromotionTokens[id] != "") {
		return Instance{}, capture, runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	parent := *boot.Receipt
	if validateStandardBootBinding(parent.Binding, capture, m.computeNodeRuntimeIncarnations[ins.NodeID], time.Unix(0, parent.CompletedAtUnixNano)) != nil {
		return Instance{}, capture, runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	return ins, capture, parent, nil
}

func (m *MemStore) GetInstanceApplicationStandardWarmParent(ctx context.Context, id string) (runtimeadmission.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return runtimeadmission.Receipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _, parent, err := m.lockNativePromotionLocked(id, false)
	return parent, err
}

func (m *MemStore) IssueInstanceApplicationStandardPromotion(ctx context.Context, p runtimeadmission.Promotion) (runtimeadmission.Promotion, error) {
	if err := ctx.Err(); err != nil {
		return runtimeadmission.Promotion{}, err
	}
	if p.Validate(time.Now()) != nil {
		return runtimeadmission.Promotion{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _, parent, err := m.lockNativePromotionLocked(p.Binding.InstanceID, false)
	if err != nil {
		return runtimeadmission.Promotion{}, err
	}
	if p.Parent != parent {
		return runtimeadmission.Promotion{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if old, ok := m.instanceApplicationStandardPromotions[p.Binding.Token]; ok {
		copy := p
		copy.Binding.IssuedAtUnixNano, copy.Binding.ExpiresAtUnixNano = old.Grant.Binding.IssuedAtUnixNano, old.Grant.Binding.ExpiresAtUnixNano
		if copy != old.Grant || old.Grant.Validate(now) != nil {
			return runtimeadmission.Promotion{}, ErrConflict
		}
		return old.Grant, nil
	}
	for _, old := range m.instanceApplicationStandardPromotions {
		if old.Grant.Binding.InstanceID == p.Binding.InstanceID {
			return runtimeadmission.Promotion{}, ErrConflict
		}
	}
	p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
	if m.instanceApplicationStandardPromotions == nil {
		m.instanceApplicationStandardPromotions = map[string]instanceStandardPromotion{}
	}
	m.instanceApplicationStandardPromotions[p.Binding.Token] = instanceStandardPromotion{Grant: p}
	return p, nil
}

func (m *MemStore) PublishInstanceApplicationStandardPromotion(ctx context.Context, r runtimeadmission.Receipt) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil || r.Paused {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, _, parent, err := m.lockNativePromotionLocked(r.Binding.InstanceID, true)
	if err != nil {
		return Instance{}, err
	}
	p, ok := m.instanceApplicationStandardPromotions[r.Binding.Token]
	if !ok || p.Grant.Parent != parent || p.Grant.CheckReceipt(r, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if ins.State == string(StateRunning) {
		if m.instanceApplicationStandardPromotionTokens[ins.ID] != r.Binding.Token || p.Receipt == nil || *p.Receipt != r {
			return Instance{}, ErrConflict
		}
		return ins, nil // Retry a committed publication without reusing its grant.
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if p.Grant.CheckReceipt(r, now) != nil {
		return Instance{}, ErrApplicationStandardRuntimeStale
	}
	if p.Receipt != nil && *p.Receipt != r {
		return Instance{}, ErrConflict
	}
	copy := r
	p.Receipt, p.ReceivedAt = &copy, now
	ins.State, ins.StartedAt = string(StateRunning), now
	if m.instanceApplicationStandardPromotionTokens == nil {
		m.instanceApplicationStandardPromotionTokens = map[string]string{}
	}
	m.instanceApplicationStandardPromotions[r.Binding.Token], m.instanceApplicationStandardPromotionTokens[ins.ID], m.instances[ins.ID] = p, r.Binding.Token, ins
	return ins, nil
}
