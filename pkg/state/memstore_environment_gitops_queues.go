package state

import (
	"strings"

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
func (m *MemStore) prepareGitOpsQueuesLocked(source EnvironmentGitSource, plan environmentsync.Plan, ids map[string]string) (*MemStore, map[string]string, error) {
	var staged *MemStore
	identities := map[string]string{}
	for _, change := range plan.Changes {
		if !strings.HasPrefix(change.Path, "queue_bindings/") || change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden" {
			continue
		}
		if err := gitOpsQueueMutationAllowed(change); err != nil {
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
		binding, err := decodeGitOpsQueue(change.Path, change.After, source.EnvironmentID, source.EnvironmentSlug, ids[change.Resource], source.AccountID)
		if err != nil || binding.AppID == "" {
			return nil, nil, ErrInvalidArgument
		}
		id := ids[gitOpsQueueResource(change.Resource, change.Path)]
		if id == "" {
			if change.Action != "create" {
				return nil, nil, ErrConflict
			}
			binding.ID = newID()
			_, err = staged.mutateQueueBindingConsumerLocked(source.AccountID, binding.AppID, binding.ID, &binding, nil, false, true)
			id = binding.ID
		} else {
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
