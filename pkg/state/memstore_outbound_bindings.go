package state

import (
	"context"
	"sort"
	"time"
)

var _ OutboundBindingStore = (*MemStore)(nil)

func copyOutboundOffer(in OutboundIntegrationOffer) OutboundIntegrationOffer {
	in.AllowedMethods = append([]string(nil), in.AllowedMethods...)
	in.AllowedPathPrefixes = append([]string(nil), in.AllowedPathPrefixes...)
	return in
}

// SeedOutboundIntegrationOffer mirrors operator provisioning in development
// and tests. Customer handlers cannot reach this method through the store
// interface; production provisioning remains owned by outboundd.
func (m *MemStore) SeedOutboundIntegrationOffer(offer OutboundIntegrationOffer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outboundIntegrationOffers[offer.ID] = copyOutboundOffer(offer)
}

func (m *MemStore) ListOutboundIntegrationOffers(_ context.Context, accountID string) ([]OutboundIntegrationOffer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundIntegrationOffer, 0)
	for _, offer := range m.outboundIntegrationOffers {
		if offer.AccountID == accountID && offer.Enabled && len(offer.AllowedMethods) > 0 && len(offer.AllowedPathPrefixes) > 0 {
			out = append(out, copyOutboundOffer(offer))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *MemStore) ListOutboundAppBindings(_ context.Context, accountID, appID string) ([]OutboundAppBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAppBinding, 0)
	for _, binding := range m.outboundAppBindings {
		if binding.AccountID != accountID || binding.AppID != appID {
			continue
		}
		if offer, ok := m.outboundIntegrationOffers[binding.ID]; ok && offer.AccountID == accountID {
			binding.OutboundIntegrationOffer = copyOutboundOffer(offer)
			binding.Enabled = offer.Enabled && len(offer.AllowedMethods) > 0 && len(offer.AllowedPathPrefixes) > 0
			out = append(out, binding)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *MemStore) BindOutboundIntegration(_ context.Context, accountID, appID, integrationID string) (OutboundAppBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, appOK := m.apps[appID]
	offer, offerOK := m.outboundIntegrationOffers[integrationID]
	if !appOK || app.AccountID != accountID || app.Status == AppDeleted || !offerOK || offer.AccountID != accountID || !offer.Enabled || len(offer.AllowedMethods) == 0 || len(offer.AllowedPathPrefixes) == 0 {
		return OutboundAppBinding{}, ErrNotFound
	}
	key := appID + "|" + integrationID
	if existing, ok := m.outboundAppBindings[key]; ok {
		existing.OutboundIntegrationOffer = copyOutboundOffer(offer)
		return existing, nil
	}
	binding := OutboundAppBinding{OutboundIntegrationOffer: copyOutboundOffer(offer), AppID: appID, CreatedAt: time.Now()}
	m.outboundAppBindings[key] = binding
	return binding, nil
}

func (m *MemStore) UnbindOutboundIntegration(_ context.Context, accountID, appID, integrationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := appID + "|" + integrationID
	if binding, ok := m.outboundAppBindings[key]; ok && binding.AccountID == accountID {
		delete(m.outboundAppBindings, key)
	}
	return nil
}
