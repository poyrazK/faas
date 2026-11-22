package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Only hashes derived from an authenticated principal may be associated. Never
// accept aliases from the request. All runtimes use the execution lock to merge
// groups atomically with respect to claims. Unregistered retained owners share
// one conservative capacity bucket until their caller next authenticates.
func (runtime *mcpTaskRuntime) associateOwners(ctx context.Context, owners [][]byte) error {
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := runtime.associateOwnersTx(ctx, tx, owners); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (runtime *mcpTaskRuntime) associateOwnersTx(ctx context.Context, tx pgx.Tx, owners [][]byte) error {
	var ready bool
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)=cardinality($2::bytea[]) AND COUNT(DISTINCT owner_group)=1
 FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[])`, runtime.namespace, owners).Scan(&ready); err != nil {
		return err
	}
	if ready {
		return nil
	}
	if err := runtime.lockExecution(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE gregale_mcp_task_owners SET owner_group=$3 WHERE namespace=$1 AND owner_group IN
 (SELECT owner_group FROM gregale_mcp_task_owners WHERE namespace=$1 AND owner_hash=ANY($2::bytea[]))`, runtime.namespace, owners, owners[0]); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO gregale_mcp_task_owners(namespace,owner_hash,owner_group)
 SELECT $1,unnest($2::bytea[]),$3 ON CONFLICT(namespace,owner_hash) DO UPDATE SET owner_group=EXCLUDED.owner_group`, runtime.namespace, owners, owners[0])
	return err
}

// Use the same admission -> execution lock order as creation. Retain every
// alias of a group while any unexpired task in that group remains readable.
func (runtime *mcpTaskRuntime) cleanupExpiredTasks(ctx context.Context) error {
	tx, err := runtime.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, taskTable+":admission:"+runtime.namespace); err != nil {
		return err
	}
	if err := runtime.lockExecution(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id IN (SELECT task_id FROM gregale_mcp_tasks WHERE namespace=$1 AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 250)`, runtime.namespace); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH unused AS (
 SELECT DISTINCT owners.owner_group FROM gregale_mcp_task_owners AS owners WHERE owners.namespace=$1
 AND NOT EXISTS (SELECT 1 FROM gregale_mcp_tasks AS task JOIN gregale_mcp_task_owners AS known
 ON known.namespace=task.namespace AND known.owner_hash=task.owner_hash
 WHERE task.namespace=$1 AND known.owner_group=owners.owner_group AND task.expires_at>clock_timestamp()) LIMIT 250)
 DELETE FROM gregale_mcp_task_owners AS owners USING unused WHERE owners.namespace=$1 AND owners.owner_group=unused.owner_group`, runtime.namespace); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
