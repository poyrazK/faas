package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundBindingProbeStore = (*MemStore)(nil)

func (m *MemStore) GetOutboundBindingProbePolicy(_ context.Context, accountID, id string) (api.OutboundBindingProbePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[id]
	policy, configured := m.outboundProbePolicies[id]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !configured {
		return api.OutboundBindingProbePolicy{}, ErrNotFound
	}
	return policy, nil
}
func (m *MemStore) SetOutboundBindingProbePolicy(_ context.Context, accountID, id string, policy *api.OutboundBindingProbePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[id]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !offer.Enabled {
		return ErrNotFound
	}
	if policy == nil {
		delete(m.outboundProbePolicies, id)
		return nil
	}
	if !policy.Valid() {
		return ErrInvalidArgument
	}
	m.outboundProbePolicies[id] = *policy
	return nil
}
func (m *MemStore) ListOutboundBindingProbeSnapshots(_ context.Context, accountID, appID string) ([]OutboundBindingProbeSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return nil, ErrNotFound
	}
	out := []OutboundBindingProbeSnapshot{}
	for _, binding := range m.outboundAppBindings {
		offer := m.outboundIntegrationOffers[binding.ID]
		if binding.AppID != appID || offer.AccountID != accountID {
			continue
		}
		row := OutboundBindingProbeSnapshot{IntegrationID: binding.ID}
		policy, configured := m.outboundProbePolicies[binding.ID]
		if configured && offer.CredentialSource == "customer_sealed" {
			row.Policy = &policy
			row.Revision = outboundProbeRevision(offer, binding, m.outboundCredentials[binding.ID], m.outboundCredentialRevisions[binding.ID], policy, m.accounts[accountID].Plan)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IntegrationID < out[j].IntegrationID })
	return out, nil
}
