package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runtime startup only reads schema state. Never use the migration binding here.
func checkTaskSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var version int
	if err := pool.QueryRow(ctx, `SELECT version FROM gregale_mcp_task_starter_schema WHERE id=1`).Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return errors.New("unsupported MCP Task starter schema version")
	}
	for _, query := range []string{
		`SELECT namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,
		result_encrypted,error_encrypted,input_state_encrypted,input_methods,created_at,updated_at,
		expires_at,attempt_count,resume_pending,lease_token,lease_expires_at,cancel_requested_at FROM gregale_mcp_tasks LIMIT 0`,
		`SELECT namespace,worker_id,tool_name,handler_version,deployment_id,heartbeat_at FROM gregale_mcp_task_worker_handlers LIMIT 0`,
	} {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			return err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_trigger
		WHERE tgrelid='gregale_mcp_tasks'::regclass AND tgname='gregale_mcp_tasks_notify'
		AND NOT tgisinternal AND tgenabled IN ('O','A'))`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("MCP Task notification trigger is unavailable")
	}
	return nil
}

func checkTaskMigrationSchema(ctx context.Context, tx pgx.Tx) error {
	var trusted bool
	err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n,
		LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a
		WHERE n.nspname=current_schema() AND a.grantee=0 AND a.privilege_type='CREATE')
		AND current_schema() IS NOT NULL`).Scan(&trusted)
	if err != nil {
		return err
	}
	if !trusted {
		return errors.New("MCP Task migrations require a schema without PUBLIC CREATE")
	}
	_, err = tx.Exec(ctx, `SELECT set_config('search_path',quote_ident(current_schema()) || ',pg_catalog',true)`)
	return err
}

func runTaskMigrationCommand(args []string) int {
	fail := func(detail string) int {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "detail": detail})
		return 1
	}
	if len(args) != 2 || args[0] != "tasks-migrate" || (args[1] != "apply" && args[1] != "status") {
		return fail("Usage: tasks-migrate apply|status")
	}
	databaseURL := os.Getenv("MCP_TASK_MIGRATION_DATABASE_URL")
	if databaseURL == "" {
		return fail("Set MCP_TASK_MIGRATION_DATABASE_URL to the schema owner account; no runtime binding fallback is used")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fail("Invalid Task migration database binding")
	}
	config.MaxConns = 1
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "60000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fail("Could not open Task migration database")
	}
	defer pool.Close()
	if args[1] == "apply" {
		err = migrateTaskSchema(ctx, pool)
	}
	if err == nil {
		err = checkTaskSchema(ctx, pool)
	}
	if err != nil {
		return fail("Task schema operation failed; check schema ownership, PUBLIC CREATE grants, connectivity and starter version")
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "schema_version": 1})
	return 0
}
