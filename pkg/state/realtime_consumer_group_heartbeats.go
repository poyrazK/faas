package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	healthsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
	"time"
)

type RealtimeWorkerHeartbeatRequest struct {
	Worker     string `json:"worker,omitempty"`
	Ready      *bool  `json:"ready"`
	StatusCode string `json:"status_code,omitempty"`
}
type RealtimeWorkerHeartbeat struct {
	ID         string    `json:"id"`
	Worker     string    `json:"worker"`
	Ready      bool      `json:"ready"`
	StatusCode string    `json:"status_code"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Lost       bool      `json:"lost"`
	Live       bool      `json:"live"`
}
type RealtimeWorkerHealth struct {
	Workers        []RealtimeWorkerHeartbeat `json:"workers"`
	LiveWorkers    int                       `json:"live_workers"`
	ReadyWorkers   int                       `json:"ready_workers"`
	TimeoutSeconds int                       `json:"timeout_seconds"`
}
type RealtimeConsumerGroupHeartbeatStore interface {
	HeartbeatRealtimeConsumerGroup(context.Context, string, string, string, RealtimeWorkerHeartbeatRequest) (RealtimeWorkerHeartbeat, error)
	ReadRealtimeConsumerGroupWorkerHealth(context.Context, string, string, string) (RealtimeWorkerHealth, error)
	EvaluateRealtimeConsumerGroupWorkerHealth(context.Context) (int, error)
}

func maintainRealtimeWorkerHealth(ctx context.Context, tx pgx.Tx, id string) error {
	q := healthsqlc.New()
	if err := q.ExpireWorkerHeartbeats(ctx, tx, healthsqlc.ExpireWorkerHeartbeatsParams{GroupID: id, TimeoutSeconds: api.RealtimeWorkerHeartbeatTimeoutSeconds}); err != nil {
		return err
	}
	return q.PruneWorkerHeartbeats(ctx, tx, healthsqlc.PruneWorkerHeartbeatsParams{GroupID: id, HistorySeconds: api.RealtimeWorkerHeartbeatHistorySeconds})
}
func (s *PgStore) HeartbeatRealtimeConsumerGroup(ctx context.Context, ep, ch, name string, req RealtimeWorkerHeartbeatRequest) (RealtimeWorkerHeartbeat, error) {
	var result RealtimeWorkerHeartbeat
	if err := requireRealtimeWorkerOperation(ctx, "heartbeat"); err != nil {
		return result, err
	}
	principal := "account"
	var credential pgtype.UUID
	if worker, ok := realtimeWorkerFromContext(ctx); ok {
		if req.Worker != "" && req.Worker != worker.grant.Info.Name {
			return result, ErrRealtimeWorkerCredentialDenied
		}
		req.Worker = worker.grant.Info.Name
		principal = worker.grant.Info.ID
		if err := credential.Scan(principal); err != nil {
			return result, err
		}
	}
	if req.Ready == nil || !validRealtimeWorkerName(req.Worker, api.RealtimeConsumerGroupWorkerMaxBytes) || len(req.StatusCode) > api.RealtimeConsumerGroupFailureMaxBytes {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	for _, c := range req.StatusCode {
		if c < 32 || c > 126 {
			return result, ErrRealtimeConsumerGroupInvalid
		}
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = maintainRealtimeWorkerHealth(ctx, tx, g.id); err != nil {
		return result, err
	}
	q := healthsqlc.New()
	exists, err := q.HasWorkerHeartbeat(ctx, tx, healthsqlc.HasWorkerHeartbeatParams{GroupID: g.id, Worker: req.Worker, Principal: principal})
	if err != nil {
		return result, err
	}
	if !exists {
		count, err := q.CountWorkerHeartbeats(ctx, tx, g.id)
		if err != nil {
			return result, err
		}
		if count >= api.RealtimeWorkerHeartbeatsMaxPerGroup {
			evicted, err := q.EvictLostHeartbeat(ctx, tx, g.id)
			if err != nil {
				return result, err
			}
			if evicted == 0 {
				return result, &RealtimeConsumerGroupCapacityError{Resource: "tracked heartbeat workers per group", Limit: api.RealtimeWorkerHeartbeatsMaxPerGroup, Observed: int(count)}
			}
		}

	}
	row, err := q.WriteWorkerHeartbeat(ctx, tx, healthsqlc.WriteWorkerHeartbeatParams{GroupID: g.id, Worker: req.Worker, Principal: principal, CredentialID: credential, Ready: *req.Ready, StatusCode: req.StatusCode})
	if err != nil {
		return result, err
	}
	result = RealtimeWorkerHeartbeat{ID: row.ID, Worker: row.Worker, Ready: row.Ready, StatusCode: row.StatusCode, LastSeenAt: row.LastSeenAt.Time.UTC(), Lost: row.Lost, Live: true}
	return result, tx.Commit(ctx)
}
func (s *PgStore) ReadRealtimeConsumerGroupWorkerHealth(ctx context.Context, ep, ch, name string) (RealtimeWorkerHealth, error) {
	result := RealtimeWorkerHealth{Workers: []RealtimeWorkerHeartbeat{}, TimeoutSeconds: api.RealtimeWorkerHeartbeatTimeoutSeconds}
	if err := requireRealtimeWorkerOperation(ctx, "status"); err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := healthsqlc.New().ReadWorkerHeartbeats(ctx, tx, healthsqlc.ReadWorkerHeartbeatsParams{GroupID: g.id, TimeoutSeconds: api.RealtimeWorkerHeartbeatTimeoutSeconds})
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		w := RealtimeWorkerHeartbeat{ID: row.ID, Worker: row.Worker, Ready: row.Ready, StatusCode: row.StatusCode, LastSeenAt: row.LastSeenAt.Time.UTC(), Lost: row.Lost, Live: row.Live}
		result.Workers = append(result.Workers, w)
		if w.Live {
			result.LiveWorkers++
			if w.Ready {
				result.ReadyWorkers++
			}
		}
	}
	return result, tx.Commit(ctx)
}
func (s *PgStore) EvaluateRealtimeConsumerGroupWorkerHealth(ctx context.Context) (int, error) {
	ids, err := healthsqlc.New().DueWorkerHealthGroups(ctx, s.pool, api.RealtimeWorkerHeartbeatScanMax)
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if err = s.scanRealtimeWorkerHealth(ctx, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
func (s *PgStore) scanRealtimeWorkerHealth(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := healthsqlc.New()
	plan, err := q.LockWorkerHealthGroup(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if limits, ok := api.RealtimeLimitsFor(api.Plan(plan)); ok && limits.EndpointsPerApp > 0 {
		if err = maintainRealtimeWorkerHealth(ctx, tx, id); err != nil {
			return err
		}
	}
	if err = q.AdvanceWorkerHealthScan(ctx, tx, healthsqlc.AdvanceWorkerHealthScanParams{GroupID: id, ScanSeconds: api.RealtimeWorkerHeartbeatScanSeconds}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
