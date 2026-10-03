package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// EventWorkBinding makes one manifest-declared event subscription a producer
// of keyed invocations. The policy is app-scoped; the selector reads the
// canonical CloudEvents payload delivered to the invocation.
type EventWorkBinding struct {
	SubscriptionID   string
	AppID            string
	PolicyName       string
	KeySelector      string
	FairnessSelector string
	Action           string
}

const (
	EventWorkInvoke        = "invoke"
	EventWorkCancelPending = "cancel_pending"
)

type EventWorkBindingStore interface {
	SetEventWorkBinding(context.Context, string, string, string, string, ...EventWorkBindingOptions) (*EventWorkBinding, error)
	EventWorkBindingsByIDs(context.Context, []string) (map[string]EventWorkBinding, error)
}

type EventWorkBindingOptions struct {
	Action           string
	FairnessSelector string
}

func normalizeEventWorkOptions(options []EventWorkBindingOptions) (string, string, error) {
	if len(options) > 1 {
		return "", "", ErrInvalidArgument
	}
	mode := EventWorkInvoke
	if len(options) == 1 && options[0].Action != "" {
		mode = options[0].Action
	}
	if mode != EventWorkInvoke && mode != EventWorkCancelPending {
		return "", "", ErrInvalidArgument
	}
	var fairness string
	if len(options) == 1 {
		fairness = options[0].FairnessSelector
		if fairness != "" {
			if _, err := workpolicy.ParseSelector(fairness); err != nil {
				return "", "", err
			}
		}
	}
	return mode, fairness, nil
}

// Empty policyName removes a binding. The previous value is returned for
// source-deployment compensation if a later manifest step fails.
func (s *PgStore) SetEventWorkBinding(ctx context.Context, appID, subscriptionID, policyName, selector string, options ...EventWorkBindingOptions) (*EventWorkBinding, error) {
	mode, fairness, err := normalizeEventWorkOptions(options)
	if err != nil {
		return nil, err
	}
	if policyName != "" {
		if _, err := workpolicy.ParseSelector(selector); err != nil {
			return nil, err
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from event_subscriptions
		where id = $1 and app_id = $2 for update`, subscriptionID, appID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var current EventWorkBinding
	err = tx.QueryRow(ctx, `select subscription_id, app_id, policy_name, key_selector, fairness_key_selector, action
		from event_subscription_work_bindings where subscription_id = $1 and app_id = $2
		for update`, subscriptionID, appID).Scan(&current.SubscriptionID, &current.AppID,
		&current.PolicyName, &current.KeySelector, &current.FairnessSelector, &current.Action)
	var previous *EventWorkBinding
	if err == nil {
		previous = &current
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if policyName == "" {
		if _, err := tx.Exec(ctx, `delete from event_subscription_work_bindings
			where subscription_id = $1 and app_id = $2`, subscriptionID, appID); err != nil {
			return nil, err
		}
	} else {
		tag, err := tx.Exec(ctx, `insert into event_subscription_work_bindings
			(subscription_id, app_id, policy_name, key_selector, action, fairness_key_selector)
			values ($1, $2, $3, $4, $5, $6)
			on conflict (subscription_id) do update set
			policy_name = excluded.policy_name, key_selector = excluded.key_selector,
			action = excluded.action, fairness_key_selector = excluded.fairness_key_selector
			where event_subscription_work_bindings.app_id = excluded.app_id`,
			subscriptionID, appID, policyName, selector, mode, fairness)
		if err != nil {
			return nil, mapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return previous, nil
}

func (s *PgStore) EventWorkBindingsByIDs(ctx context.Context, ids []string) (map[string]EventWorkBinding, error) {
	out := make(map[string]EventWorkBinding)
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `select subscription_id, app_id, policy_name, key_selector, fairness_key_selector, action
		from event_subscription_work_bindings where subscription_id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var binding EventWorkBinding
		if err := rows.Scan(&binding.SubscriptionID, &binding.AppID,
			&binding.PolicyName, &binding.KeySelector, &binding.FairnessSelector, &binding.Action); err != nil {
			return nil, err
		}
		out[binding.SubscriptionID] = binding
	}
	return out, rows.Err()
}

func (m *MemStore) SetEventWorkBinding(_ context.Context, appID, subscriptionID, policyName, selector string, options ...EventWorkBindingOptions) (*EventWorkBinding, error) {
	mode, fairness, err := normalizeEventWorkOptions(options)
	if err != nil {
		return nil, err
	}
	if policyName != "" {
		if _, err := workpolicy.ParseSelector(selector); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var subscriptionFound bool
	for _, subscription := range m.eventSubscriptions {
		if subscription.ID == subscriptionID && sameMemUUID(subscription.AppID, appID) {
			subscriptionFound = true
			break
		}
	}
	if !subscriptionFound {
		return nil, ErrNotFound
	}
	if policyName != "" {
		if _, ok := m.workPolicies[memWorkPolicyKey(appID, policyName)]; !ok {
			return nil, ErrNotFound
		}
	}
	var previous *EventWorkBinding
	if old, ok := m.eventWorkBindings[subscriptionID]; ok {
		copy := old
		previous = &copy
	}
	if policyName == "" {
		delete(m.eventWorkBindings, subscriptionID)
	} else {
		m.eventWorkBindings[subscriptionID] = EventWorkBinding{
			SubscriptionID: subscriptionID, AppID: canonicalMemUUID(appID),
			PolicyName: policyName, KeySelector: selector,
			FairnessSelector: fairness, Action: mode}
	}
	return previous, nil
}

func (m *MemStore) EventWorkBindingsByIDs(_ context.Context, ids []string) (map[string]EventWorkBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]EventWorkBinding)
	for _, id := range ids {
		if binding, ok := m.eventWorkBindings[id]; ok {
			out[id] = binding
		}
	}
	return out, nil
}
