package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/db"
)

func (s *PostgresStore) ApproveSchema(ctx context.Context, account, id string, revision int64, change string, retry bool) error {
	if !validID(account) || !validID(id) {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	r, err := s.q.PipelineGet(ctx, tx, sqlc.PipelineGetParams{AccountID: account, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return storeError(err)
	}
	p := pipelineRow(r)
	if liveTableRepair(p.SchemaChange) && !retry {
		return ErrConflict
	}
	if tableWorkflow(p) && !retry {
		if err = s.checkedTables(ctx, tx, p, p.SchemaChange.RequestedTables); err != nil {
			return err
		}
	}
	n, err := s.q.PipelineApproveSchema(ctx, tx, sqlc.PipelineApproveSchemaParams{AccountID: account, ID: id, Revision: revision, ChangeID: change, Retry: retry, MaxRetries: api.DataPipelineSchemaRetriesMax, MaxAgeSeconds: int64(api.DataPipelineObservationMaxAge.Seconds())})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return storeError(tx.Commit(ctx))
}

// RefreshSchema atomically stamps the existing runtime freshness boundary,
// invalidates cached snapshots and writes the ordinary scheduler outbox request.
// Replays reuse each wake ID and never move that boundary past a replacement.
func (s *PostgresStore) RefreshSchema(ctx context.Context, p Pipeline, c SchemaChange) ([]SchemaRefresh, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = s.q.PipelineSchemaRefreshLock(ctx, tx, sqlc.PipelineSchemaRefreshLockParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, LeaseToken: p.LeaseToken, ChangeID: c.ID}); err != nil {
		return nil, false, storeError(err)
	}
	return s.refreshApps(ctx, tx, p, c, false)
}

// RefreshReadGate uses the same freshness/outbox boundary as schema changes.
// Its receipts are independent, so schema pruning cannot re-stamp a delivered
// read-gate restart. Ordinary app restart recovery owns delivery failures.
func (s *PostgresStore) RefreshReadGate(ctx context.Context, p Pipeline, version string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = s.q.PipelineReadGateRefreshLock(ctx, tx, sqlc.PipelineReadGateRefreshLockParams{AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, LeaseToken: p.LeaseToken}); err != nil {
		return storeError(err)
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", p.ID, p.Generation, version))))
	_, _, err = s.refreshApps(ctx, tx, p, SchemaChange{ID: id}, true)
	return err
}

func (s *PostgresStore) refreshApps(ctx context.Context, tx pgx.Tx, p Pipeline, c SchemaChange, readGate bool) ([]SchemaRefresh, bool, error) {
	apps, err := s.q.PipelineSchemaApps(ctx, tx, sqlc.PipelineSchemaAppsParams{AccountID: p.AccountID, DatabaseID: p.DatabaseID, PageLimit: api.DataPipelineSchemaRefreshAppsMax + 1})
	if err != nil {
		return nil, false, storeError(err)
	}
	if len(apps) > api.DataPipelineSchemaRefreshAppsMax {
		return nil, false, ErrQuota
	}
	if err = s.q.PipelineSchemaRefreshPrune(ctx, tx, sqlc.PipelineSchemaRefreshPruneParams{PipelineID: p.ID, ChangeID: c.ID, ReadGate: readGate}); err != nil {
		return nil, false, storeError(err)
	}
	records := make([]SchemaRefresh, 0, len(apps))
	for _, app := range apps {
		if app.Status != "active" && app.Status != "evicted_cold" {
			return nil, false, ErrConflict
		}
		id := uid(app.ID)
		nonce, _ := json.Marshal(struct {
			Pipeline, Change, App string
			Retry                 int
		}{p.ID, c.ID, id, c.Retry})
		wake := uuid.NewSHA1(uuid.NameSpaceOID, append([]byte("gregale.pipeline.schema.refresh:"), nonce...)).String()
		n, err := s.q.PipelineSchemaRefreshInsert(ctx, tx, sqlc.PipelineSchemaRefreshInsertParams{PipelineID: p.ID, ChangeID: c.ID, Attempt: int32(c.Retry), AppID: id, WakeID: wake, ReadGate: readGate})
		if err != nil {
			return nil, false, storeError(err)
		}
		if n == 1 {
			if err = s.q.PipelineSchemaRefreshStamp(ctx, tx, id); err != nil {
				return nil, false, storeError(err)
			}
			if err = s.q.PipelineSchemaRefreshSnapshots(ctx, tx, id); err != nil {
				return nil, false, storeError(err)
			}
		}
		// A first deployment has no running API schema cache to refresh. Keep
		// its freshness stamp and snapshot invalidation, without a restart that
		// the scheduler cannot execute. A later serving deployment may enqueue
		// the same wake ID without moving that original boundary.
		if !app.HasLiveDeployment {
			records = append(records, SchemaRefresh{AppID: id, Slug: app.Slug, Status: "not_required"})
			continue
		}
		queued, err := s.q.PipelineSchemaRefreshQueued(ctx, tx, sqlc.PipelineSchemaRefreshQueuedParams{AppID: id, WakeID: wake})
		if err != nil {
			return nil, false, storeError(err)
		}
		if !queued {
			payload, _ := json.Marshal(map[string]string{"app_id": id, "wake_id": wake})
			if err = db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyRuntimeConfigRestart, string(payload)); err != nil {
				return nil, false, ErrUnavailable
			}
		}
		records = append(records, SchemaRefresh{AppID: id, Slug: app.Slug, WakeID: wake, Status: "pending"})
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, storeError(err)
	}
	if readGate {
		return records, true, nil
	}
	ready := true
	for i, r := range records {
		if r.Status == "not_required" {
			continue
		}
		status, err := db.GetRuntimeConfigRestartStatus(ctx, s.pool, r.AppID, r.WakeID)
		if err != nil {
			return records, false, ErrUnavailable
		}
		records[i].Status = status.State
		if status.State == "dead_letter" {
			return records, false, ErrUnavailable
		}
		ready = ready && status.State == "delivered" && status.CompletedAt != nil
	}
	return records, ready, nil
}
