package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// TriggerWorkBinding names the app policy and scalar payload selectors for
// one external broker mapping. Queue triggers use the invocation ledger.
type TriggerWorkBinding struct {
	TriggerID        string
	AppID            string
	PolicyName       string
	KeySelector      string
	FairnessSelector string
}

type TriggerWorkBindingStore interface {
	SetTriggerWorkBinding(context.Context, string, string, string, string, string) (*TriggerWorkBinding, error)
	TriggerWorkBindingByID(context.Context, string) (*TriggerWorkBinding, error)
}

func validateTriggerWorkBinding(policyName, keySelector, fairnessSelector string) error {
	if policyName == "" {
		if keySelector != "" || fairnessSelector != "" {
			return ErrInvalidArgument
		}
		return nil
	}
	if err := (workpolicy.Policy{Name: policyName, MaxRunningPerKey: 1}).Validate(); err != nil {
		return err
	}
	if _, err := workpolicy.ParseSelector(keySelector); err != nil {
		return err
	}
	if fairnessSelector != "" {
		if _, err := workpolicy.ParseSelector(fairnessSelector); err != nil {
			return err
		}
	}
	return nil
}

func brokerWorkKind(kind string) bool {
	switch kind {
	case "kafka", "nats", "redis_streams", "sqs_compat", "amqp", "rabbitmq":
		return true
	default:
		return false
	}
}

// An empty policy name removes the binding. The previous value is returned
// for manifest reconciliation compensation when a later step fails.
func (s *PgStore) SetTriggerWorkBinding(ctx context.Context, appID, triggerID, policyName, keySelector, fairnessSelector string) (*TriggerWorkBinding, error) {
	if err := validateTriggerWorkBinding(policyName, keySelector, fairnessSelector); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("state: trigger work binding begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var kind string
	var enabled bool
	err = tx.QueryRow(ctx, `select kind, enabled from triggers where id=$1 and app_id=$2 for update`,
		triggerID, appID).Scan(&kind, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("state: trigger work binding trigger: %w", err)
	}
	if !brokerWorkKind(kind) {
		return nil, ErrInvalidArgument
	}
	var old TriggerWorkBinding
	err = tx.QueryRow(ctx, `select trigger_id, app_id, policy_name, key_selector, fairness_key_selector
		from trigger_work_bindings where trigger_id=$1 for update`, triggerID).Scan(
		&old.TriggerID, &old.AppID, &old.PolicyName, &old.KeySelector, &old.FairnessSelector)
	var previous *TriggerWorkBinding
	if err == nil {
		previous = &old
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("state: trigger work binding previous: %w", err)
	}
	if previous == nil && policyName != "" {
		if enabled {
			return nil, ErrConflict
		}
		// Existing receipts may be keyed by a transient Kafka high-water
		// mark, SQS receipt handle, or AMQP delivery tag. A fresh trigger
		// starts with one durable identity scheme for every receipt.
		var hasRecords bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from trigger_records
			where trigger_id=$1)`, triggerID).Scan(&hasRecords); err != nil {
			return nil, fmt.Errorf("state: trigger work binding records: %w", err)
		}
		if hasRecords {
			return nil, ErrConflict
		}
	}
	if policyName == "" {
		if _, err := tx.Exec(ctx, `delete from trigger_work_bindings where trigger_id=$1`, triggerID); err != nil {
			return nil, fmt.Errorf("state: trigger work binding delete: %w", err)
		}
	} else {
		_, err := tx.Exec(ctx, `insert into trigger_work_bindings
			(trigger_id, app_id, policy_name, key_selector, fairness_key_selector)
			values ($1,$2,$3,$4,$5)
			on conflict (trigger_id) do update set policy_name=excluded.policy_name,
			key_selector=excluded.key_selector,
			fairness_key_selector=excluded.fairness_key_selector`,
			triggerID, appID, policyName, keySelector, fairnessSelector)
		if err != nil {
			return nil, fmt.Errorf("state: trigger work binding upsert: %w", mapErr(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: trigger work binding commit: %w", err)
	}
	return previous, nil
}

func (s *PgStore) TriggerWorkBindingByID(ctx context.Context, triggerID string) (*TriggerWorkBinding, error) {
	var binding TriggerWorkBinding
	err := s.pool.QueryRow(ctx, `select trigger_id, app_id, policy_name, key_selector, fairness_key_selector
		from trigger_work_bindings where trigger_id=$1`, triggerID).Scan(
		&binding.TriggerID, &binding.AppID, &binding.PolicyName,
		&binding.KeySelector, &binding.FairnessSelector)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: trigger work binding lookup: %w", err)
	}
	return &binding, nil
}

func (m *MemStore) SetTriggerWorkBinding(_ context.Context, appID, triggerID, policyName, keySelector, fairnessSelector string) (*TriggerWorkBinding, error) {
	if err := validateTriggerWorkBinding(policyName, keySelector, fairnessSelector); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	trigger, ok := m.triggers[triggerID]
	if !ok || !sameMemUUID(trigger.AppID.String(), appID) {
		return nil, ErrNotFound
	}
	if !brokerWorkKind(trigger.Kind) {
		return nil, ErrInvalidArgument
	}
	if policyName != "" {
		if _, ok := m.workPolicies[memWorkPolicyKey(appID, policyName)]; !ok {
			return nil, ErrNotFound
		}
	}
	var previous *TriggerWorkBinding
	if old, ok := m.triggerWorkBindings[triggerID]; ok {
		previous = &old
	}
	if previous == nil && policyName != "" {
		if trigger.Enabled {
			return nil, ErrConflict
		}
		for _, record := range m.records {
			if sameMemUUID(record.TriggerID.String(), triggerID) {
				return nil, ErrConflict
			}
		}
	}
	if policyName == "" {
		delete(m.triggerWorkBindings, triggerID)
	} else {
		m.triggerWorkBindings[triggerID] = TriggerWorkBinding{
			TriggerID: triggerID, AppID: canonicalMemUUID(appID), PolicyName: policyName,
			KeySelector: keySelector, FairnessSelector: fairnessSelector,
		}
	}
	return previous, nil
}

func (m *MemStore) TriggerWorkBindingByID(_ context.Context, triggerID string) (*TriggerWorkBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.triggerWorkBindings[triggerID]
	if !ok {
		return nil, nil
	}
	return &binding, nil
}
