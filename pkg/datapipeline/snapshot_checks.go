package datapipeline

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func inspectCheckSettings(ctx context.Context, source *pgxpool.Pool, mode string) ([]api.DataConnectionIssue, error) {
	if mode != "snapshot" {
		return inspectSourceSettings(ctx, source)
	}
	var version int
	err := source.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version)
	return []api.DataConnectionIssue{inspectionIssue("source_postgres_16_required", "PostgreSQL version", "Use a PostgreSQL 16 source.", version >= 160000 && version < 170000)}, err
}
func inspectCheckCapacity(ctx context.Context, source *pgxpool.Pool, mode string) ([]api.DataConnectionIssue, error) {
	if mode == "snapshot" {
		return nil, nil
	}
	return inspectSourceCapacity(ctx, source)
}
func inspectCheckTargetCapacity(ctx context.Context, target *pgx.Conn, mode string) ([]api.DataConnectionIssue, error) {
	if mode != "snapshot" {
		return inspectTargetCapacity(ctx, target)
	}
	var version int
	err := target.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version)
	return []api.DataConnectionIssue{inspectionIssue("destination_postgres_16_required", "Mirror version", "Use a PostgreSQL 16 managed mirror.", version >= 160000 && version < 170000)}, err
}
