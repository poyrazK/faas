package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) postgresBindingInventory(ctx context.Context, accountID, appID, scope string) bindingInventorySection {
	if s.managedPostgresBindings == nil {
		return bindingInventoryUnavailable(api.BindingTypePostgres, "managed_postgres_unavailable", "warning", "Managed PostgreSQL is unavailable; PostgreSQL bindings could not be fully listed.")
	}
	rows, err := s.managedPostgresBindings.ListForApp(ctx, accountID, appID, scope)
	if err != nil {
		return bindingInventoryUnavailable(api.BindingTypePostgres, "query_failed", "error", "PostgreSQL bindings could not be listed.")
	}
	section := bindingInventorySection{revisions: make(map[string]string), refreshWakeIDs: make(map[string]string), adoptionSelectors: make(map[string]state.BindingAdoptionSelector)}
	for _, row := range rows {
		item := inventoryItem(api.BindingTypePostgres, row.DatabaseName, row.EnvironmentKey, row.Scope, row.Access, row.State)
		section.adoptionSelectors[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = state.BindingAdoptionSelector{Type: item.Type, BindingID: row.BindingID, Scope: item.Scope, Keys: api.BindingCredentialSecretKeys(item.Type, item.Binding)}
		generation, pending := row.CredentialGeneration, row.RotationPending
		item.CredentialGeneration, item.RotationPending = &generation, &pending
		section.revisions[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = bindingMetadataRevision(item, row.BindingID)
		if pending && row.RotationWakeID != "" {
			section.refreshWakeIDs[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = row.RotationWakeID
		}
		section.items = append(section.items, item)
	}
	return section
}

func (s *server) objectStorageBindingInventory(ctx context.Context, accountID, appID, scope string) bindingInventorySection {
	store, ok := s.store.(state.ObjectStorageBindingInventoryStore)
	if !ok {
		return bindingInventoryUnavailable(api.BindingTypeObjectStorage, "unavailable", "error", "Object-storage binding metadata is unavailable.")
	}
	rows, err := store.ListObjectStorageBindingsForApp(ctx, accountID, appID, scope)
	if err != nil {
		return bindingInventoryUnavailable(api.BindingTypeObjectStorage, "query_failed", "error", "Object-storage bindings could not be listed.")
	}
	section := bindingInventorySection{revisions: make(map[string]string), refreshWakeIDs: make(map[string]string), adoptionSelectors: make(map[string]state.BindingAdoptionSelector)}
	for _, row := range rows {
		item := inventoryItem(api.BindingTypeObjectStorage, row.BucketName, row.Prefix, row.Scope, row.Permission, row.State)
		section.revisions[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = bindingMetadataRevision(item, row.BindingID+"\x00"+row.RotationRevisionID)
		section.adoptionSelectors[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = state.BindingAdoptionSelector{Type: item.Type, BindingID: row.BindingID, Scope: item.Scope, Keys: api.BindingCredentialSecretKeys(item.Type, item.Binding)}
		pending := row.RotationPending
		item.RotationPending = &pending
		if pending && row.RotationWakeID != "" {
			section.refreshWakeIDs[bindingVerificationKey(item.Type, item.Binding, item.Scope)] = row.RotationWakeID
		}
		section.items = append(section.items, item)
	}
	return section
}

func (s *server) outboundBindingInventory(ctx context.Context, accountID, appID string) bindingInventorySection {
	store, ok := s.store.(state.OutboundBindingStore)
	if !ok {
		return bindingInventoryUnavailable(api.BindingTypeOutbound, "unavailable", "error", "Outbound binding metadata is unavailable.")
	}
	rows, err := store.ListOutboundAppBindings(ctx, accountID, appID)
	if err != nil {
		return bindingInventoryUnavailable(api.BindingTypeOutbound, "query_failed", "error", "Outbound bindings could not be listed.")
	}
	section := bindingInventorySection{revisions: make(map[string]string), privateBindingIDs: make(map[string]string)}
	probes, probeErr := s.outboundProbeSnapshots(ctx, accountID, appID, len(rows))
	if probeErr != nil {
		return bindingInventoryUnavailable(api.BindingTypeOutbound, "query_failed", "error", "Outbound probe metadata could not be read.")
	}
	for _, row := range rows {
		config := "disabled"
		if row.Enabled {
			config = "enabled"
		}
		item := inventoryItem(api.BindingTypeOutbound, row.Name, "", "app", "invoke", config)
		section.privateBindingIDs[bindingVerificationKey(item.Type, item.Name, item.Scope)] = row.ID
		configured := row.CredentialConfigured
		item.CredentialConfigured = &configured
		item.AllowedMethods = append([]string(nil), row.RouteMethods...)
		item.AllowedPathPrefixes = append([]string(nil), row.RoutePathPrefixes...)
		if probe, ok := probes[row.ID]; ok && probe.Policy != nil && api.ValidOutboundProbeGateway(s.outboundProbeGatewayURL) {
			item.OutboundProbe = probe.Policy
			section.revisions[bindingVerificationKey(item.Type, row.ID, item.Scope)] = bindingMetadataRevision(item, probe.Revision+"\x00"+s.outboundProbeGatewayURL)
		}
		section.items = append(section.items, item)
	}
	return section
}

func (s *server) queueBindingInventory(ctx context.Context, accountID, appID string, now time.Time) bindingInventorySection {
	bindings, err := s.store.ListQueueBindingsForApp(ctx, accountID, appID)
	if err != nil {
		return bindingInventoryUnavailable(api.BindingTypeQueue, "query_failed", "error", "Queue bindings could not be listed.")
	}
	var section bindingInventorySection
	if len(bindings) == 0 {
		return section
	}
	consumers, available := s.bindingConsumerInventory(ctx, accountID, appID)
	for _, binding := range bindings {
		config := "disabled"
		if binding.Enabled {
			config = "enabled"
		}
		item := inventoryItem(api.BindingTypeQueue, binding.QueueName, binding.Name, "app", binding.Mode, config)
		item.ConsumerState, item.ConsumerLiveness = "unknown", "unknown"
		consumer, found := consumers[binding.ID]
		if binding.Mode == "pull" {
			item.ConsumerState, item.ConsumerLiveness = "external", "external"
		} else if available && found {
			applyConsumerInventory(&item, binding.Enabled, consumer, now)
		} else if len(section.issues) == 0 {
			section.issues = bindingInventoryUnavailable(api.BindingTypeQueue, "consumer_status_unavailable", "error", "Queue consumer status could not be read.").issues
		}
		section.items = append(section.items, item)
	}
	return section
}

func (s *server) bindingConsumerInventory(ctx context.Context, accountID, appID string) (map[string]state.QueueBindingConsumerInventory, bool) {
	store, ok := s.store.(state.QueueBindingConsumerInventoryStore)
	if !ok {
		return nil, false
	}
	rows, err := store.ListQueueBindingConsumersForApp(ctx, accountID, appID)
	if err != nil {
		return nil, false
	}
	items := make(map[string]state.QueueBindingConsumerInventory, len(rows))
	for _, row := range rows {
		items[row.BindingID] = row
	}
	return items, true
}

func applyConsumerInventory(item *api.AppBindingInventoryItem, enabled bool, consumer state.QueueBindingConsumerInventory, now time.Time) {
	item.ConsumerState, item.ConsumerLiveness = "not_configured", "not_observed"
	item.ConsumerStateReason = "push_consumer_not_provisioned"
	if consumer.ConsumerEnabled == nil {
		return
	}
	item.ObservedAt = consumer.LastPollAt
	if !enabled || !*consumer.ConsumerEnabled {
		item.ConsumerState, item.ConsumerStateReason = "paused", "push_consumer_disabled"
		return
	}
	item.ConsumerState, item.ConsumerStateReason = "active", "push_consumer_enabled"
	item.ConsumerLiveness = queueBindingConsumerLiveness(now, state.TriggerConsumerHealth{
		LastPollAt: consumer.LastPollAt, LastSuccessAt: consumer.LastSuccessAt, LastErrorAt: consumer.LastErrorAt,
	})
	if item.ConsumerLiveness != "not_observed" {
		item.RuntimeStatus = item.ConsumerLiveness
	}
}
