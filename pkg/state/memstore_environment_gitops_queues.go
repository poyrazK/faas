package state

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) gitOpsQueueIntentLocked(binding QueueBinding) gitOpsQueueIntent {
	row := gitOpsQueueIntent{ID: binding.ID, Name: binding.Name, Intent: gitOpsQueueContract(binding), RetiredAt: binding.RetiredAt}
	for _, trigger := range m.triggers {
		if trigger.QueueBindingID.Valid && trigger.QueueBindingID.String() == canonicalMemUUID(binding.ID) {
			row.Consumers = append(row.Consumers, gitOpsQueueConsumer{ID: trigger.ID.String(), Slug: trigger.Slug, Enabled: trigger.Enabled, Config: trigger.Config,
				BatchSize: trigger.BatchSizeMax, BatchWindow: trigger.BatchWindowMs, MaxAttempts: trigger.MaxAttempts, PayloadMax: trigger.PayloadMaxBytes, BrokerPoisonStrategy: trigger.BrokerPoisonStrategy, FilterCriteria: trigger.FilterCriteria})
		}
	}
	return row
}

// Queue intents and consumer projections validate on detached maps so a later
// quota/identity failure rolls back the entire multi-field memory transaction.
func (m *MemStore) prepareGitOpsQueuesLocked(source EnvironmentGitSource, definition api.EnvironmentDefinition, plan environmentsync.Plan, ids map[string]string) (*MemStore, map[string]string, error) {
	var staged *MemStore
	identities := map[string]string{}
	for _, change := range gitOpsQueueRetireFirst(plan.Changes) {
		if !strings.HasPrefix(change.Path, "queue_bindings/") || change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden" {
			continue
		}
		if err := gitOpsQueueMutationAllowed(change, definition); err != nil {
			return nil, nil, err
		}
		if staged == nil {
			staged = &MemStore{accounts: m.accounts, apps: m.apps, projectEnvironments: m.projectEnvironments, queueBindings: map[string]QueueBinding{}, triggers: map[string]sqlc.Trigger{}}
			for id, binding := range m.queueBindings {
				staged.queueBindings[id] = cloneQueueBinding(binding)
			}
			for id, trigger := range m.triggers {
				staged.triggers[id] = trigger
			}
		}
		id := ids[gitOpsQueueResource(change.Resource, change.Path)]
		appID := ids[change.Resource]
		if change.Action == "remove" {
			current, exists := staged.queueBindings[id]
			if !exists || current.AppID != appID || current.EnvironmentID != source.EnvironmentID {
				return nil, nil, ErrConflict
			}
			if current.RetiredAt == nil {
				if _, err := staged.mutateQueueBindingConsumerLocked(source.AccountID, appID, id, nil, nil, true, true); err != nil {
					return nil, nil, err
				}
			}
			continue
		}
		binding, err := decodeGitOpsQueue(change.Path, change.After, source.EnvironmentID, source.EnvironmentSlug, ids[change.Resource], source.AccountID)
		if err != nil || binding.AppID == "" {
			return nil, nil, ErrInvalidArgument
		}
		if id == "" {
			if change.Action != "create" {
				return nil, nil, ErrConflict
			}
			binding.ID = newID()
			_, err = staged.mutateQueueBindingConsumerLocked(source.AccountID, binding.AppID, binding.ID, &binding, nil, false, true)
			id = binding.ID
		} else {
			current, exists := staged.queueBindings[id]
			if !exists || current.EnvironmentID != source.EnvironmentID || current.AppID != appID {
				return nil, nil, ErrConflict
			}
			if current.RetiredAt != nil {
				if !gitOpsQueueRecoveryMatches(definition.Workloads[strings.TrimPrefix(change.Resource, "workload/")].QueueRecoveries[binding.Name], id) {
					return nil, nil, ErrConflict
				}
				if binding.Mode == "push" {
					if err := staged.gitOpsQueueRecoveryQuotaLocked(source.AccountID, appID); err != nil {
						return nil, nil, err
					}
				}
				current.RetiredAt = nil
				staged.queueBindings[id] = current
			}
			patch := gitOpsQueuePatch(binding)
			_, err = staged.mutateQueueBindingConsumerLocked(source.AccountID, binding.AppID, id, nil, &patch, false, true)
		}
		if err != nil {
			return nil, nil, err
		}
		identities[(environmentsync.Field{Resource: change.Resource, Path: change.Path}).Key()] = id
	}
	return staged, identities, nil
}

func (m *MemStore) gitOpsQueueRecoveryQuotaLocked(accountID, appID string) error {
	limits := api.MustLimitsFor(m.accounts[accountID].Plan)
	appCount, accountCount := 0, 0
	for _, trigger := range m.triggers {
		if !m.triggerConsumesQuotaLocked(trigger) {
			continue
		}
		if trigger.AppID.String() == canonicalMemUUID(appID) {
			appCount++
		}
		if app, ok := m.triggerAppLocked(trigger); ok && app.AccountID == accountID && app.Status != AppDeleted {
			accountCount++
		}
	}
	if appCount >= limits.TriggerLimitPerApp {
		return &TriggerQuotaError{Scope: TriggerQuotaScopeApp, Limit: limits.TriggerLimitPerApp, Observed: appCount}
	}
	if accountCount >= limits.TriggerLimitPerAccount {
		return &TriggerQuotaError{Scope: TriggerQuotaScopeAccount, Limit: limits.TriggerLimitPerAccount, Observed: accountCount}
	}
	return nil
}
