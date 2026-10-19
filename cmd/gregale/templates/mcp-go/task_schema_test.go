package main

import (
	"context"
	neturl "net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskMigrationsRequireExplicitBinding(t *testing.T) {
	t.Setenv("MCP_TASK_MIGRATION_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "postgres://runtime:never-used@127.0.0.1/runtime")
	if runTaskMigrationCommand([]string{"tasks-migrate", "apply"}) != 1 {
		t.Fatal("fell back to runtime credentials")
	}
}

func TestTaskSchemaRestrictedRuntimePostgres(t *testing.T) {
	url := os.Getenv("MCP_TASKS_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("MCP_TASKS_REQUIRE_POSTGRES") == "1" {
			t.Fatal("PostgreSQL test binding required")
		}
		t.Skip("set MCP_TASKS_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	id, err := newTaskUUID()
	if err != nil {
		t.Fatal(err)
	}
	name := "mcp_parity_" + strings.ReplaceAll(id, "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted+"; CREATE ROLE "+quoted+" LOGIN PASSWORD 'mcp-parity-test-only'"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE; DROP ROLE "+quoted) }()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["search_path"] = name + ",pg_catalog"
	migration, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer migration.Close()
	if err = checkTaskSchema(ctx, migration); err == nil {
		t.Fatal("accepted missing schema")
	}
	if err = migrateTaskSchema(ctx, migration); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(ctx, `INSERT INTO gregale_mcp_tasks (namespace,task_id,owner_hash,tool_name,handler_version,arguments_encrypted,status,expires_at) VALUES ('parity',$1,$2,'migration_probe','1',$2,'completed',clock_timestamp()+interval '1 hour')`, id, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = migrateTaskSchema(ctx, migration); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = migration.QueryRow(ctx, `SELECT count(*) FROM gregale_mcp_tasks`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration lost existing tasks: count=%d err=%v", count, err)
	}
	if _, err = admin.Exec(ctx, "GRANT USAGE ON SCHEMA "+quoted+" TO "+quoted+"; GRANT SELECT,INSERT,UPDATE,DELETE ON "+quoted+".gregale_mcp_tasks,"+quoted+".gregale_mcp_task_worker_handlers TO "+quoted+"; GRANT SELECT ON "+quoted+".gregale_mcp_task_starter_schema TO "+quoted); err != nil {
		t.Fatal(err)
	}
	runtimeURL, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL.User = neturl.UserPassword(name, "mcp-parity-test-only")
	query := runtimeURL.Query()
	query.Set("search_path", name+",pg_catalog")
	runtimeURL.RawQuery = query.Encode()
	t.Setenv("DATABASE_URL", runtimeURL.String())
	t.Setenv("MCP_TASK_OWNER_KEY", strings.Repeat("x", 32))
	t.Setenv("MCP_TASK_NAMESPACE", "parity")
	t.Setenv("MCP_TASK_MIGRATION_DATABASE_URL", "invalid-and-never-used")
	config := mcpConfig{Tasks: []byte(`{"enabled":true}`)}
	runtime, err := startMCPTaskRuntime(config, "web")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	var canCreate bool
	if err = runtime.pool.QueryRow(ctx, `SELECT has_schema_privilege(current_user,current_schema(),'CREATE')`).Scan(&canCreate); err != nil || canCreate {
		t.Fatalf("runtime CREATE = %v, error=%v", canCreate, err)
	}
	if _, err = runtime.pool.Exec(ctx, `ALTER TABLE gregale_mcp_tasks ADD COLUMN forbidden integer`); err == nil {
		t.Fatal("runtime could alter schema")
	}
	if err = checkTaskSchema(ctx, runtime.pool); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(ctx, `UPDATE gregale_mcp_task_starter_schema SET version=2`); err != nil {
		t.Fatal(err)
	}
	if err = migrateTaskSchema(ctx, migration); err == nil {
		t.Fatal("migration downgraded future schema")
	}
	if err = checkTaskSchema(ctx, runtime.pool); err == nil {
		t.Fatal("runtime accepted future schema")
	}
	if _, err = migration.Exec(ctx, `UPDATE gregale_mcp_task_starter_schema SET version=1; ALTER TABLE gregale_mcp_tasks DISABLE TRIGGER gregale_mcp_tasks_notify`); err != nil {
		t.Fatal(err)
	}
	if err = checkTaskSchema(ctx, runtime.pool); err == nil {
		t.Fatal("accepted disabled notification trigger")
	}
}
