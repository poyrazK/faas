package main

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Keep IDs and provider routing metadata private to execution. Inventory names
// alone cannot pin an outbound binding that is deleted and recreated mid-run.
func (s *bindingPolicySnapshotClient) readOutboundConfiguration(ctx context.Context) (map[string]string, string, error) {
	names := map[string]bool{}
	for _, item := range s.inventory.Bindings {
		if item.Type == api.BindingTypeOutbound && item.State != "disabled" && item.OutboundProbe != nil {
			names[item.Name] = true
		}
	}
	ids := map[string]string{}
	if len(names) == 0 {
		return ids, "", nil
	}
	bindings, err := s.ListOutboundAppBindings(ctx, s.slug)
	if err != nil {
		return nil, "", bindingPolicySnapshotFailure("outbound_identity_unconfirmed", "Could not confirm outbound binding identities.", err)
	}
	type routing struct {
		ID, Name, Origin, AppID, CredentialSource string
		Enabled, CredentialConfigured             bool
		IntegrationMethods, IntegrationPrefixes   []string
		BindingMethods, BindingPrefixes           []string
	}
	rows := []routing{}
	for _, binding := range bindings.Items {
		offer := binding.Integration
		if !names[offer.Name] {
			continue
		}
		id, err := uuid.Parse(offer.ID)
		if err != nil || id == uuid.Nil || !sameBindingDeployment(binding.AppID, s.policy.AppID) || ids[offer.Name] != "" {
			return nil, "", bindingPolicySnapshotFailure("outbound_identity_unconfirmed", "Each configured outbound name must identify one integration bound to the selected app.", nil)
		}
		ids[offer.Name] = id.String()
		rows = append(rows, routing{ID: id.String(), Name: offer.Name, Origin: offer.Origin, AppID: binding.AppID,
			CredentialSource: offer.CredentialSource, Enabled: offer.Enabled, CredentialConfigured: offer.CredentialConfigured,
			IntegrationMethods: sortedBindingPolicyStrings(offer.AllowedMethods), IntegrationPrefixes: sortedBindingPolicyStrings(offer.AllowedPathPrefixes),
			BindingMethods: sortedBindingPolicyStrings(binding.AllowedMethods), BindingPrefixes: sortedBindingPolicyStrings(binding.AllowedPathPrefixes)})
	}
	if len(ids) != len(names) {
		return nil, "", bindingPolicySnapshotFailure("outbound_identity_unconfirmed", "A configured outbound integration is missing from the app's binding roster.", nil)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	encoded, _ := json.Marshal(rows) // Strings, booleans and slices only.
	return ids, string(encoded), nil
}
