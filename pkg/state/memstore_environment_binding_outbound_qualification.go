package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentQualificationOutboundStore = (*MemStore)(nil)

// candidateOutboundBindingsLocked mirrors environment_binding_outbound_revision
// for each customer binding of the app.
func (m *MemStore) candidateOutboundBindingsLocked(appID string) []candidateOutboundBinding {
	var bindings []candidateOutboundBinding
	for _, binding := range m.outboundAppBindings {
		if binding.AppID != appID {
			continue
		}
		offer, known := m.outboundIntegrationOffers[binding.ID]
		b := candidateOutboundBinding{IntegrationID: binding.ID, Active: true}
		if known && offer.AccountID == binding.AccountID {
			b.Active = offer.Enabled && len(offer.AllowedMethods) > 0 && len(offer.AllowedPathPrefixes) > 0
			policy, configured := m.outboundProbePolicies[binding.ID]
			credential := m.outboundCredentials[binding.ID]
			if app, ok := m.apps[appID]; ok && app.AccountID == binding.AccountID && configured && offer.CredentialSource == "customer_sealed" && len(credential) > 0 {
				sealed := sha256.Sum256(credential)
				b.Policy = policy
				b.Revision = outboundProbeRevision(offer, binding, m.accounts[binding.AccountID].Plan, hex.EncodeToString(sealed[:]),
					m.outboundCredentialRevisions[binding.ID], m.outboundCredentialKeys[binding.ID], policy)
			}
		}
		bindings = append(bindings, b)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].IntegrationID < bindings[j].IntegrationID })
	return bindings
}

func (m *MemStore) PendingEnvironmentQualificationOutboundProbes(ctx context.Context, limit int) ([]api.OutboundQualificationProbe, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var probes []api.OutboundQualificationProbe
	now := time.Now()
	for _, memory := range m.environmentGitOps {
		for _, r := range memory.qualifications {
			if r.BindingContext == nil || r.LeaseUntil == nil || !now.Before(*r.LeaseUntil) {
				continue
			}
			for _, b := range m.candidateOutboundBindingsLocked(r.AppID) {
				if _, recorded := memory.bindingOutbound[qualificationOutboundReceiptKey(r.ID, r.Attempt, b.IntegrationID, b.Revision)]; recorded || !b.Active || b.Revision == "" {
					continue
				}
				probes = append(probes, api.OutboundQualificationProbe{RequestID: r.ID, Attempt: r.Attempt, AccountID: m.outboundIntegrationOffers[b.IntegrationID].AccountID,
					AppID: r.AppID, IntegrationID: b.IntegrationID, Revision: b.Revision, Policy: b.Policy})
			}
		}
	}
	sort.Slice(probes, func(i, j int) bool {
		if probes[i].RequestID != probes[j].RequestID {
			return probes[i].RequestID < probes[j].RequestID
		}
		return probes[i].IntegrationID < probes[j].IntegrationID
	})
	return probes[:min(len(probes), limit)], nil
}

func (m *MemStore) RecordEnvironmentQualificationOutboundReceipt(ctx context.Context, probe api.OutboundQualificationProbe, status int) error {
	if !validOutboundQualificationReceipt(probe, status) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, request, err := m.qualificationLocked(probe.RequestID)
	if err != nil {
		return err
	}
	if request.Attempt != probe.Attempt || request.AppID != probe.AppID || request.BindingContext == nil || request.LeaseUntil == nil ||
		!time.Now().Before(*request.LeaseUntil) {
		return ErrBindingPromotionChanged
	}
	current := false
	for _, b := range m.candidateOutboundBindingsLocked(request.AppID) {
		current = current || b.IntegrationID == probe.IntegrationID && b.Active && b.Revision == probe.Revision && b.Policy == probe.Policy
	}
	if !current {
		return ErrBindingPromotionChanged
	}
	if memory.bindingOutbound == nil {
		memory.bindingOutbound = map[string]time.Time{}
	}
	key := qualificationOutboundReceiptKey(probe.RequestID, probe.Attempt, probe.IntegrationID, probe.Revision)
	if _, exists := memory.bindingOutbound[key]; !exists {
		memory.bindingOutbound[key] = time.Now().UTC()
	}
	return nil
}
