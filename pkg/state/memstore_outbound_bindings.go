package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

var _ OutboundBindingStore = (*MemStore)(nil)

func copyOutboundOffer(in OutboundIntegrationOffer) OutboundIntegrationOffer {
	in.AllowedMethods = append([]string(nil), in.AllowedMethods...)
	in.AllowedPathPrefixes = append([]string(nil), in.AllowedPathPrefixes...)
	if in.DailyRequestLimit != nil {
		limit := *in.DailyRequestLimit
		in.DailyRequestLimit = &limit
	}
	return in
}

func copyOutboundBinding(in OutboundAppBinding) OutboundAppBinding {
	in.OutboundIntegrationOffer = copyOutboundOffer(in.OutboundIntegrationOffer)
	in.RouteMethods = append([]string(nil), in.RouteMethods...)
	in.RoutePathPrefixes = append([]string(nil), in.RoutePathPrefixes...)
	return in
}

func effectiveMemOutboundOffer(offer OutboundIntegrationOffer, plan api.Plan) OutboundIntegrationOffer {
	if offer.OwnerKind == "customer" {
		if policy, ok := api.EffectiveOutboundRequestPolicyForPlan(plan, offer.RequestPolicy); ok {
			offer.RequestPolicy = policy
		}
	}
	if offer.DailyRequestLimit != nil {
		if maximum, ok := api.OutboundRequestsPerDayMaxForPlan(plan); ok && *offer.DailyRequestLimit > maximum {
			limit := maximum
			offer.DailyRequestLimit = &limit
		}
	}
	return copyOutboundOffer(offer)
}

func (m *MemStore) SetOutboundDailyRequestLimit(_ context.Context, accountID, integrationID string, limit *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" {
		return ErrNotFound
	}
	if limit != nil {
		account, exists := m.accounts[accountID]
		maximum, planKnown := api.OutboundRequestsPerDayMaxForPlan(account.Plan)
		if !exists || !planKnown || *limit < 1 || *limit > maximum {
			return ErrInvalidArgument
		}
	}
	if limit == nil {
		offer.DailyRequestLimit = nil
	} else {
		copy := *limit
		offer.DailyRequestLimit = &copy
	}
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

func (m *MemStore) SetOutboundRequestPolicy(_ context.Context, accountID, integrationID string, policy api.OutboundRequestPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	account, accountOK := m.accounts[accountID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !accountOK || !offer.Enabled {
		return ErrNotFound
	}
	if !api.OutboundRequestPolicyAllowedForPlan(account.Plan, policy) {
		return ErrInvalidArgument
	}
	offer.RequestPolicy = policy
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

func (m *MemStore) GetOutboundIntegrationUsage(_ context.Context, accountID, integrationID string) (OutboundIntegrationUsage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID {
		return OutboundIntegrationUsage{}, ErrNotFound
	}
	utcNow := time.Now().UTC()
	resetAt := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
	dailyLimit := copyOutboundOffer(offer).DailyRequestLimit
	if dailyLimit != nil {
		if account, exists := m.accounts[accountID]; exists {
			if maximum, ok := api.OutboundRequestsPerDayMaxForPlan(account.Plan); ok && *dailyLimit > maximum {
				*dailyLimit = maximum
			}
		}
	}
	return OutboundIntegrationUsage{
		DailyRequestCount: 0,
		DailyRequestLimit: dailyLimit,
		UsageDate:         utcNow.Format("2006-01-02"),
		ResetsAt:          resetAt,
	}, nil
}

// SeedOutboundIntegrationOffer mirrors operator provisioning in development
// and tests. Customer handlers cannot reach this method through the store
// interface; production provisioning remains owned by outboundd.
func (m *MemStore) SeedOutboundIntegrationOffer(offer OutboundIntegrationOffer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offer.OwnerKind == "" {
		offer.OwnerKind = "operator"
	}
	if offer.CredentialSource == "" {
		offer.CredentialSource = "operator_env"
		offer.CredentialConfigured = true
	}
	if offer.RequestPolicy == (api.OutboundRequestPolicy{}) {
		offer.RequestPolicy = api.DefaultOutboundRequestPolicy()
	}
	m.outboundIntegrationOffers[offer.ID] = copyOutboundOffer(offer)
}

func (m *MemStore) CreateOutboundIntegration(_ context.Context, offer OutboundIntegrationOffer) (OutboundIntegrationOffer, error) {
	if offer.RequestPolicy == (api.OutboundRequestPolicy{}) {
		offer.RequestPolicy = api.DefaultOutboundRequestPolicy()
	}
	if err := validateCustomerOutboundIntegration(offer); err != nil {
		return OutboundIntegrationOffer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[offer.AccountID]
	if !ok {
		return OutboundIntegrationOffer{}, ErrNotFound
	}
	if offer.DailyRequestLimit != nil {
		maximum, planKnown := api.OutboundRequestsPerDayMaxForPlan(account.Plan)
		if !planKnown || *offer.DailyRequestLimit > maximum {
			return OutboundIntegrationOffer{}, ErrInvalidArgument
		}
	}
	if !api.OutboundRequestPolicyAllowedForPlan(account.Plan, offer.RequestPolicy) {
		return OutboundIntegrationOffer{}, ErrInvalidArgument
	}
	active := 0
	for _, existing := range m.outboundIntegrationOffers {
		if existing.AccountID != offer.AccountID {
			continue
		}
		if existing.Name == offer.Name {
			return OutboundIntegrationOffer{}, ErrConflict
		}
		if existing.OwnerKind == "customer" && existing.Enabled {
			active++
		}
	}
	if active >= MaxCustomerOutboundIntegrations {
		return OutboundIntegrationOffer{}, ErrOutboundIntegrationLimit
	}
	if _, exists := m.outboundIntegrationOffers[offer.ID]; exists {
		return OutboundIntegrationOffer{}, ErrConflict
	}
	m.outboundIntegrationOffers[offer.ID] = copyOutboundOffer(offer)
	return copyOutboundOffer(offer), nil
}

func (m *MemStore) DeleteOutboundIntegration(_ context.Context, accountID, integrationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" {
		return ErrNotFound
	}
	delete(m.outboundIntegrationOffers, integrationID)
	delete(m.outboundCredentials, integrationID)
	for key, binding := range m.outboundAppBindings {
		if binding.ID == integrationID {
			delete(m.outboundAppBindings, key)
		}
	}
	return nil
}

func (m *MemStore) SetOutboundCredential(_ context.Context, accountID, integrationID string, sealed []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || !offer.Enabled || offer.CredentialSource != "customer_sealed" || len(sealed) == 0 {
		return ErrNotFound
	}
	m.outboundCredentials[integrationID] = append([]byte(nil), sealed...)
	offer.CredentialConfigured = true
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

func (m *MemStore) DeleteOutboundCredential(_ context.Context, accountID, integrationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.CredentialSource != "customer_sealed" {
		return ErrNotFound
	}
	delete(m.outboundCredentials, integrationID)
	offer.CredentialConfigured = false
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

func (m *MemStore) ListOutboundIntegrationOffers(_ context.Context, accountID string) ([]OutboundIntegrationOffer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundIntegrationOffer, 0)
	for _, offer := range m.outboundIntegrationOffers {
		if offer.AccountID == accountID && offer.Enabled && len(offer.AllowedMethods) > 0 && len(offer.AllowedPathPrefixes) > 0 {
			account, exists := m.accounts[accountID]
			if exists {
				out = append(out, effectiveMemOutboundOffer(offer, account.Plan))
			}
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
			account, exists := m.accounts[accountID]
			if !exists {
				continue
			}
			binding.OutboundIntegrationOffer = effectiveMemOutboundOffer(offer, account.Plan)
			binding.Enabled = offer.Enabled && len(offer.AllowedMethods) > 0 && len(offer.AllowedPathPrefixes) > 0
			out = append(out, copyOutboundBinding(binding))
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
		existing.OutboundIntegrationOffer = effectiveMemOutboundOffer(offer, m.accounts[accountID].Plan)
		return copyOutboundBinding(existing), nil
	}
	binding := OutboundAppBinding{OutboundIntegrationOffer: effectiveMemOutboundOffer(offer, m.accounts[accountID].Plan), AppID: appID,
		RouteMethods: append([]string(nil), offer.AllowedMethods...), RoutePathPrefixes: append([]string(nil), offer.AllowedPathPrefixes...), CreatedAt: time.Now()}
	m.outboundAppBindings[key] = binding
	return copyOutboundBinding(binding), nil
}

func (m *MemStore) UpdateOutboundBindingPolicy(_ context.Context, accountID, appID, integrationID string, methods, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := appID + "|" + integrationID
	binding, ok := m.outboundAppBindings[key]
	app, appOK := m.apps[appID]
	offer, offerOK := m.outboundIntegrationOffers[integrationID]
	if !ok || !appOK || app.AccountID != accountID || app.Status == AppDeleted ||
		!offerOK || offer.AccountID != accountID || !offer.Enabled {
		return ErrNotFound
	}
	if err := routepolicy.ValidateSubset(
		routepolicy.Policy{AllowedMethods: offer.AllowedMethods, AllowedPathPrefixes: offer.AllowedPathPrefixes},
		routepolicy.Policy{AllowedMethods: methods, AllowedPathPrefixes: paths}); err != nil {
		return ErrInvalidArgument
	}
	binding.RouteMethods = append([]string(nil), methods...)
	binding.RoutePathPrefixes = append([]string(nil), paths...)
	m.outboundAppBindings[key] = binding
	return nil
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
