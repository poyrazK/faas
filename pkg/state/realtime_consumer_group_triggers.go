package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrRealtimeConsumerGroupBoundToTrigger rejects claims and settlements on a
// group that a realtime trigger consumes, and a trigger dispatch on a group
// it is no longer bound to (ADR-1322).
var ErrRealtimeConsumerGroupBoundToTrigger = errors.New("state: realtime consumer group is bound to a trigger")

// RealtimeTriggerLeaseError reports a group whose lease is too short for a
// trigger invocation plus wake.
type RealtimeTriggerLeaseError struct{ Limit, Observed int }

func (e *RealtimeTriggerLeaseError) Error() string {
	return fmt.Sprintf("state: consumer group lease %ds is below the trigger minimum %ds", e.Observed, e.Limit)
}

// RealtimeTriggerBindingStore binds a realtime trigger to its consumer group.
type RealtimeTriggerBindingStore interface {
	BindRealtimeConsumerGroupTrigger(ctx context.Context, accountID, triggerID string, cfg api.RealtimeTriggerConfig) error
}

type realtimeTriggerDispatchKey struct{}

// WithRealtimeTriggerDispatch marks schedd's claims and settlements for one
// trigger, the only consumer a bound group accepts.
func WithRealtimeTriggerDispatch(ctx context.Context, triggerID string) context.Context {
	return context.WithValue(ctx, realtimeTriggerDispatchKey{}, triggerID)
}

func requireRealtimeGroupConsumer(ctx context.Context, g realtimeGroupRow) error {
	if triggerID, ok := ctx.Value(realtimeTriggerDispatchKey{}).(string); ok {
		if triggerID == "" || g.triggerID != triggerID {
			return ErrRealtimeConsumerGroupBoundToTrigger
		}
		return nil
	}
	if g.triggerID != "" {
		return ErrRealtimeConsumerGroupBoundToTrigger
	}
	return nil
}

// BindRealtimeConsumerGroupTrigger binds the group named by cfg to triggerID.
// The endpoint must belong to accountID, the group's lease must cover a
// trigger invocation, and a group binds to at most one trigger.
func (s *PgStore) BindRealtimeConsumerGroupTrigger(ctx context.Context, accountID, triggerID string, cfg api.RealtimeTriggerConfig) error {
	if err := validateRealtimeConsumerGroup(cfg.EndpointID, cfg.Channel, cfg.Group); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: bind realtime trigger begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var groupID, bound string
	var lease int
	err = tx.QueryRow(ctx, `select g.id,g.lease_seconds,coalesce(g.trigger_id::text,'')
 from managed_realtime_consumer_groups g join managed_realtime_endpoints e on e.id=g.endpoint_id
 where e.id=$1 and e.account_id=$2 and g.channel=$3 and g.name=$4 for update of g`,
		cfg.EndpointID, accountID, cfg.Channel, cfg.Group).Scan(&groupID, &lease, &bound)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: bind realtime trigger lookup: %w", err)
	}
	if lease < api.RealtimeTriggerMinLeaseSeconds {
		return &RealtimeTriggerLeaseError{Limit: api.RealtimeTriggerMinLeaseSeconds, Observed: lease}
	}
	if bound != "" && bound != triggerID {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set trigger_id=$2 where id=$1`, groupID, triggerID); err != nil {
		return fmt.Errorf("state: bind realtime trigger: %w", err)
	}
	return tx.Commit(ctx)
}
