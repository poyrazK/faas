package main

import "context"

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
