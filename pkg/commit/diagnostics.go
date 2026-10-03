package commit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ObservationMaxAge bounds how long diagnostics and alerts trust a source scan.
// A stale or absent observation is unknown, never an empty backlog.
const ObservationMaxAge = 5 * time.Minute

type DiagnosticCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Code   string `json:"code"`
	Action string `json:"action,omitempty"`
}

// CheckDatabase is a local, read-only diagnostic using an explicitly supplied
// credential. It does not register credentials, claim events, or establish
// connectivity from the scheduler's network. Driver errors never escape.
func CheckDatabase(ctx context.Context, raw, source string) []DiagnosticCheck {
	check := DiagnosticCheck{Name: "local_database", State: "fail", Code: "invalid_connection", Action: "Supply a PostgreSQL URL with sslmode=verify-full; use PGSSLROOTCERT for the trusted CA."}
	if err := ValidateConnection(raw); err != nil {
		return []DiagnosticCheck{check}
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return []DiagnosticCheck{check}
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	check.Code = "database_unavailable"
	check.Action = "Check local DNS, connectivity, the credential and the PostgreSQL CA; scheduler access is checked separately."
	if err != nil {
		return []DiagnosticCheck{check}
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return []DiagnosticCheck{check}
	}
	return CheckDatabasePool(ctx, pool, source)
}

// CheckDatabasePool checks the supported schema, owner binding and relay role
// grants without exercising any DML. It also supports real database fixtures.
func CheckDatabasePool(ctx context.Context, pool *pgxpool.Pool, source string) []DiagnosticCheck {
	checks := []DiagnosticCheck{{Name: "local_database", State: "pass", Code: "connected"}}
	schema := DiagnosticCheck{Name: "schema", State: "pass", Code: "qualified"}
	if err := QualifySchema(ctx, pool); err != nil {
		schema.State, schema.Code, schema.Action = "fail", "schema_unqualified", "Install the documented schema or explicit upgrade as the database owner."
		return append(checks, schema)
	}
	checks = append(checks, schema)
	binding := DiagnosticCheck{Name: "source_binding", State: "pass", Code: "qualified"}
	if err := QualifySource(ctx, pool, source); err != nil {
		binding.State, binding.Code, binding.Action = "fail", "source_binding_unqualified", "Bind this database outbox to the intended source as the database owner."
	}
	checks = append(checks, binding)
	permissions := DiagnosticCheck{Name: "relay_permissions", State: "pass", Code: "qualified"}
	var granted bool
	err := pool.QueryRow(ctx, `SELECT has_table_privilege(current_user,'public.gregale_outbox','SELECT')
 AND has_table_privilege(current_user,'public.gregale_outbox','INSERT')
 AND has_table_privilege(current_user,'public.gregale_outbox','UPDATE')
 AND has_table_privilege(current_user,'public.gregale_outbox','DELETE')
 AND has_table_privilege(current_user,'public.gregale_commit_binding','SELECT')`).Scan(&granted)
	if err != nil || !granted {
		permissions.State, permissions.Code, permissions.Action = "fail", "permissions_unqualified", "Grant the relay role SELECT/INSERT/UPDATE/DELETE on the outbox and SELECT on the binding."
	}
	return append(checks, permissions)
}
