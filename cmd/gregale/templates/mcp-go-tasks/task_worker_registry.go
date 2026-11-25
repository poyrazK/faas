package main

import (
	"context"
	"encoding/json"
	"time"
)

// mcpTaskWorkerReceipt is the startup receipt `gregale mcp server tasks
// deploy` reads from worker logs to learn each replacement's registry ID and
// how long an older generation needs to drain. It is refreshed with every
// capability heartbeat so it outlives the bounded per-instance log ring.
// claimFence is false: this runtime has no database-side claim fence, so the
// CLI's quarantine and retirement flows (which need one) stay Node-only.
func mcpTaskWorkerReceipt(workerID string, drainTimeout time.Duration) string {
	body, _ := json.Marshal(struct {
		Event             string `json:"event"`
		WorkerID          string `json:"workerID"`
		ClaimFence        bool   `json:"claimFence"`
		ShutdownTimeoutMS int64  `json:"shutdownTimeoutMs"`
	}{"mcp_task_worker_started", workerID, false, drainTimeout.Milliseconds()})
	return string(body)
}

func (runtime *mcpTaskRuntime) markWorkerDraining(ctx context.Context) error {
	runtime.workerRegistryMu.Lock()
	defer runtime.workerRegistryMu.Unlock()
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO gregale_mcp_task_workers(namespace,worker_id,handlers,draining,heartbeat_at,expires_at)
		VALUES($1,$2::uuid,'[]'::jsonb,true,clock_timestamp(),clock_timestamp()+($3*interval '1 second'))
		ON CONFLICT(namespace,worker_id) DO UPDATE SET draining=true,heartbeat_at=EXCLUDED.heartbeat_at,expires_at=EXCLUDED.expires_at`, runtime.namespace, runtime.workerID, int(runtime.drainTimeout.Seconds())+90); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM gregale_mcp_task_worker_handlers WHERE namespace=$1 AND worker_id=$2::uuid`, runtime.namespace, runtime.workerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (runtime *mcpTaskRuntime) removeWorkerRegistration(ctx context.Context) error {
	runtime.workerRegistryMu.Lock()
	defer runtime.workerRegistryMu.Unlock()
	_, err := runtime.pool.Exec(ctx, `DELETE FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=$2::uuid`, runtime.namespace, runtime.workerID)
	return err
}
