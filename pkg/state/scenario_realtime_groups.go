// adr: 20261022183526
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioRealtimeGroup is a consumer group's configuration as a contained
// worker sees it. The run never claims from it, so it has no counters.
type ScenarioRealtimeGroup struct {
	GroupID, EndpointID, Channel, Name                     string
	EndpointEnabled                                        bool
	LeaseSeconds, MaxAttempts, DeadLetterRetentionSeconds  int
	OrderingMetadataKey, DeadLetterPolicy, DeadlineMetaKey string
	TriggerID                                              string
	Filter                                                 RealtimeConsumerGroupFilter
	CreatedAt                                              time.Time
}

// ScenarioRealtimeGroupStore resolves the consumer group a contained worker
// addresses (ADR-20261022183526). By name, the endpoint must belong to the
// addressed app or run workload, as for a captured publish; by ID, the group
// must belong to the run's account. Anything else is ErrNotFound.
type ScenarioRealtimeGroupStore interface {
	ScenarioRealtimeConsumerGroup(ctx context.Context, accountID, runID, slug, endpointID, channel, name string) (ScenarioRealtimeGroup, error)
	ScenarioRealtimeWorkerGroup(ctx context.Context, accountID, groupID string) (ScenarioRealtimeGroup, error)
}

// ValidRealtimeConsumerGroupClaim mirrors a claim's worker and batch limit
// validation; a single claim has limit 1.
func ValidRealtimeConsumerGroupClaim(worker string, limit, waitSeconds int) bool {
	return validRealtimeWorkerName(worker, api.RealtimeConsumerGroupWorkerMaxBytes) && limit >= 1 && limit <= api.RealtimeConsumerGroupBatchMax &&
		waitSeconds >= 0 && waitSeconds <= api.RealtimeConsumerGroupWaitMaxSeconds
}

// ValidRealtimeConsumerGroupSettlement mirrors a settlement's validation.
func ValidRealtimeConsumerGroupSettlement(req RealtimeConsumerGroupSettlement) bool {
	_, _, err := validateRealtimeGroupSettlement(req)
	return err == nil
}

// ValidRealtimeConsumerGroupBatchSettlement mirrors a settlement batch's
// validation.
func ValidRealtimeConsumerGroupBatchSettlement(req RealtimeConsumerGroupBatchSettlementRequest) bool {
	_, err := validateRealtimeBatchSettlements(req)
	return err == nil
}

// ValidRealtimeWorkerHeartbeat mirrors a heartbeat's validation.
func ValidRealtimeWorkerHeartbeat(req RealtimeWorkerHeartbeatRequest) bool {
	return req.Ready != nil && validRealtimeWorkerName(req.Worker, api.RealtimeConsumerGroupWorkerMaxBytes) && len(req.StatusCode) <= api.RealtimeConsumerGroupFailureMaxBytes
}

func (s *PgStore) ScenarioRealtimeConsumerGroup(ctx context.Context, accountID, runID, slug, endpointID, channel, name string) (ScenarioRealtimeGroup, error) {
	endpoint, err := uuid.Parse(endpointID)
	if err != nil || uuid.Validate(accountID) != nil || !api.ValidAppSlug(slug) {
		return ScenarioRealtimeGroup{}, ErrNotFound
	}
	q := sqlc.New()
	target, err := q.ScenarioRealtimeEndpointTarget(ctx, s.pool, sqlc.ScenarioRealtimeEndpointTargetParams{AccountID: accountID, RunID: runID, Slug: slug, EndpointID: endpoint.String()})
	if err != nil {
		return ScenarioRealtimeGroup{}, fmt.Errorf("resolve rehearsal realtime endpoint: %w", err)
	}
	if target == "" {
		return ScenarioRealtimeGroup{}, ErrNotFound
	}
	row, err := q.ScenarioRealtimeConsumerGroupByName(ctx, s.pool, sqlc.ScenarioRealtimeConsumerGroupByNameParams{AccountID: accountID, EndpointID: endpoint.String(), Channel: channel, Name: name})
	return scenarioRealtimeGroup(sqlc.ScenarioRealtimeConsumerGroupByIDRow(row), err)
}

func (s *PgStore) ScenarioRealtimeWorkerGroup(ctx context.Context, accountID, groupID string) (ScenarioRealtimeGroup, error) {
	id, err := uuid.Parse(groupID)
	if err != nil || uuid.Validate(accountID) != nil {
		return ScenarioRealtimeGroup{}, ErrNotFound
	}
	row, err := sqlc.New().ScenarioRealtimeConsumerGroupByID(ctx, s.pool, sqlc.ScenarioRealtimeConsumerGroupByIDParams{AccountID: accountID, ID: id.String()})
	return scenarioRealtimeGroup(row, err)
}

func scenarioRealtimeGroup(row sqlc.ScenarioRealtimeConsumerGroupByIDRow, err error) (ScenarioRealtimeGroup, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return ScenarioRealtimeGroup{}, ErrNotFound
	}
	if err != nil {
		return ScenarioRealtimeGroup{}, fmt.Errorf("read rehearsal realtime consumer group: %w", err)
	}
	group := ScenarioRealtimeGroup{GroupID: row.GroupID, EndpointID: row.EndpointID, Channel: row.Channel, Name: row.Name, EndpointEnabled: row.EndpointEnabled,
		LeaseSeconds: int(row.LeaseSeconds), MaxAttempts: int(row.MaxAttempts), DeadLetterRetentionSeconds: int(row.DeadLetterRetentionSeconds),
		OrderingMetadataKey: row.OrderingMetadataKey, DeadLetterPolicy: row.DeadLetterPolicy, DeadlineMetaKey: row.DeadlineMetadataKey, TriggerID: row.TriggerID,
		CreatedAt: row.CreatedAt.Time}
	if err := json.Unmarshal(row.MessageFilter, &group.Filter); err != nil {
		return ScenarioRealtimeGroup{}, fmt.Errorf("decode rehearsal realtime consumer group filter: %w", err)
	}
	return group, nil
}

// Consumer groups require durable PostgreSQL storage, so the memory store has
// none to resolve.
func (m *MemStore) ScenarioRealtimeConsumerGroup(ctx context.Context, _, _, _, _, _, _ string) (ScenarioRealtimeGroup, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioRealtimeGroup{}, err
	}
	return ScenarioRealtimeGroup{}, ErrNotFound
}

func (m *MemStore) ScenarioRealtimeWorkerGroup(ctx context.Context, _, _ string) (ScenarioRealtimeGroup, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioRealtimeGroup{}, err
	}
	return ScenarioRealtimeGroup{}, ErrNotFound
}
