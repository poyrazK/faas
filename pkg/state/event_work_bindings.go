package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
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
	Ordered          bool
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
	Ordered          bool
}

func normalizeEventWorkOptions(options []EventWorkBindingOptions) (string, string, bool, error) {
	if len(options) > 1 {
		return "", "", false, ErrInvalidArgument
	}
	mode := EventWorkInvoke
	if len(options) == 1 && options[0].Action != "" {
		mode = options[0].Action
	}
	if mode != EventWorkInvoke && mode != EventWorkCancelPending {
		return "", "", false, ErrInvalidArgument
	}
	var fairness string
	var ordered bool
	if len(options) == 1 {
		fairness = options[0].FairnessSelector
		ordered = options[0].Ordered
		if fairness != "" {
			if _, err := workpolicy.ParseSelector(fairness); err != nil {
				return "", "", false, err
			}
		}
	}
	if ordered && mode != EventWorkInvoke {
		return "", "", false, ErrInvalidArgument
	}
	return mode, fairness, ordered, nil
}

func validateOrderedEventWorkPolicy(policy workpolicy.Policy) error {
	pending := policy.PendingUpdates
	if pending == "" {
		pending = workpolicy.PendingAll
	}
	if policy.MaxRunningPerKey != 1 || pending != workpolicy.PendingAll || policy.Debounce != 0 || policy.ExpiresAfter != 0 {
		return fmt.Errorf("ordered event delivery requires max_running_per_key=1, pending_updates=all, debounce=0, and expires_after=0")
	}
	return nil
}

// Empty policyName removes a binding. The previous value is returned for
// source-deployment compensation if a later manifest step fails.
func (s *PgStore) SetEventWorkBinding(ctx context.Context, appID, subscriptionID, policyName, selector string, options ...EventWorkBindingOptions) (*EventWorkBinding, error) {
	mode, fairness, ordered, err := normalizeEventWorkOptions(options)
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
	if err := tx.QueryRow(ctx, `select 1 from apps where id = $1 and status <> 'deleted' for share`, appID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := tx.QueryRow(ctx, `select 1 from event_subscriptions
		where id = $1 and app_id = $2 for update`, subscriptionID, appID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ordered {
		encoded, err := sqlc.New().EventAgeSubscriptionPolicy(ctx, tx, sqlc.EventAgeSubscriptionPolicyParams{SubscriptionID: mustPgUUID(subscriptionID), AppID: mustPgUUID(appID)})
		if err != nil {
			return nil, err
		}
		if p := decodeEventRoutingRetryPolicy(encoded); p != nil && p.MaxDeliveryAgeMS > 0 {
			return nil, ErrInvalidArgument
		}
	}
	var current EventWorkBinding
	err = tx.QueryRow(ctx, `select subscription_id, app_id, policy_name, key_selector, fairness_key_selector, action, ordered
		from event_subscription_work_bindings where subscription_id = $1 and app_id = $2
		for update`, subscriptionID, appID).Scan(&current.SubscriptionID, &current.AppID,
		&current.PolicyName, &current.KeySelector, &current.FairnessSelector, &current.Action, &current.Ordered)
	var previous *EventWorkBinding
	if err == nil {
		previous = &current
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if policyName == "" {
		if ordered {
			return nil, ErrInvalidArgument
		}
		if _, err := tx.Exec(ctx, `delete from event_subscription_work_bindings
			where subscription_id = $1 and app_id = $2`, subscriptionID, appID); err != nil {
			return nil, err
		}
	} else {
		if ordered {
			var pending string
			var maxRunningPerKey int
			var debounceMS, expiresAfterMS int64
			if err := tx.QueryRow(ctx, `select max_running_per_key, pending_updates, debounce_ms, expires_after_ms
				from app_work_policies where app_id=$1 and name=$2 for share`, appID, policyName).
				Scan(&maxRunningPerKey, &pending, &debounceMS, &expiresAfterMS); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, ErrNotFound
				}
				return nil, err
			}
			if maxRunningPerKey != 1 || pending != string(workpolicy.PendingAll) || debounceMS != 0 || expiresAfterMS != 0 {
				return nil, fmt.Errorf("%w: ordered event delivery requires max_running_per_key=1, pending_updates=all, debounce=0, and expires_after=0", ErrInvalidArgument)
			}
		}
		tag, err := tx.Exec(ctx, `insert into event_subscription_work_bindings
			(subscription_id, app_id, policy_name, key_selector, action, fairness_key_selector, ordered)
			values ($1, $2, $3, $4, $5, $6, $7)
			on conflict (subscription_id) do update set
			policy_name = excluded.policy_name, key_selector = excluded.key_selector,
			action = excluded.action, fairness_key_selector = excluded.fairness_key_selector,
			ordered = excluded.ordered
			where event_subscription_work_bindings.app_id = excluded.app_id`,
			subscriptionID, appID, policyName, selector, mode, fairness, ordered)
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
	rows, err := s.pool.Query(ctx, `select subscription_id, app_id, policy_name, key_selector, fairness_key_selector, action, ordered
		from event_subscription_work_bindings where subscription_id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var binding EventWorkBinding
		if err := rows.Scan(&binding.SubscriptionID, &binding.AppID,
			&binding.PolicyName, &binding.KeySelector, &binding.FairnessSelector, &binding.Action, &binding.Ordered); err != nil {
			return nil, err
		}
		out[binding.SubscriptionID] = binding
	}
	return out, rows.Err()
}

func (m *MemStore) SetEventWorkBinding(_ context.Context, appID, subscriptionID, policyName, selector string, options ...EventWorkBindingOptions) (*EventWorkBinding, error) {
	mode, fairness, ordered, err := normalizeEventWorkOptions(options)
	if err != nil {
		return nil, err
	}
	if policyName != "" {
		if _, err := workpolicy.ParseSelector(selector); err != nil {
			return nil, err
		}
	} else if ordered {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var subscriptionFound bool
	for _, subscription := range m.eventSubscriptions {
		if subscription.ID == subscriptionID && sameMemUUID(subscription.AppID, appID) {
			if ordered && subscription.RoutingRetryPolicy != nil && subscription.RoutingRetryPolicy.MaxDeliveryAgeMS > 0 {
				return nil, ErrInvalidArgument
			}
			subscriptionFound = true
			break
		}
	}
	if !subscriptionFound {
		return nil, ErrNotFound
	}
	if policyName != "" {
		policy, ok := m.workPolicies[memWorkPolicyKey(appID, policyName)]
		if !ok {
			return nil, ErrNotFound
		}
		if ordered {
			if err := validateOrderedEventWorkPolicy(policy.Policy); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
			}
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
			FairnessSelector: fairness, Action: mode, Ordered: ordered}
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
