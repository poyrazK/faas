package datapipeline

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func containsTable(tables []string, name string) bool { return slices.Contains(tables, name) }

func publishAddedTables(ctx context.Context, p Pipeline, source *pgxpool.Pool, plan SchemaChange, current func(context.Context) error) error {
	var members []string
	if err := source.QueryRow(ctx, `SELECT coalesce(array_agg(tablename::text ORDER BY tablename COLLATE "C"),'{}') FROM pg_publication_tables WHERE pubname=$1`, pipelineName(p)).Scan(&members); err != nil {
		return err
	}
	if slices.Equal(members, plan.RequestedTables) {
		return ensurePublication(ctx, p, source) // Recover a committed source ALTER.
	}
	previous := p
	previous.Tables = relationNames(plan.Before)
	if !slices.Equal(members, previous.Tables) {
		return &blocked{"source_publication_changed", true}
	}
	if err := ensurePublication(ctx, previous, source); err != nil {
		return err
	}
	tx, err := lockSchemaSource(ctx, p, source)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actual, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	if tablePlan(p, plan.Before, actual).ID != plan.ID {
		return ErrConflict
	}
	names := make([]string, len(plan.AddedTables))
	for i, name := range plan.AddedTables {
		names[i] = tableIdent(name)
	}
	if _, err = tx.Exec(ctx, "ALTER PUBLICATION "+ident(pipelineName(p))+" ADD TABLE "+strings.Join(names, ",")); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The apply worker already exists. Only the configured table-sync workers are
// additional demand, within the same reserved five connections/three slots.
func admitAddedCopy(ctx context.Context, source *pgxpool.Pool, target *pgx.Conn, tables int) error {
	var syncWorkers, logical, processes, active, freeSlots, freeSenders int64
	if err := target.QueryRow(ctx, `SELECT current_setting('max_sync_workers_per_subscription')::bigint,
 current_setting('max_logical_replication_workers')::bigint,current_setting('max_worker_processes')::bigint,
 (SELECT count(*) FROM pg_stat_subscription WHERE pid IS NOT NULL)`).Scan(&syncWorkers, &logical, &processes, &active); err != nil {
		return err
	}
	needed := min(syncWorkers, int64(tables))
	if needed < 1 || syncWorkers < 1 || syncWorkers > api.DataPipelineCopyWorkersMax || logical-active < needed || processes < logical {
		return &blocked{"table_addition_copy_capacity", false}
	}
	if err := source.QueryRow(ctx, `SELECT current_setting('max_replication_slots')::bigint-(SELECT count(*) FROM pg_replication_slots),
 current_setting('max_wal_senders')::bigint-(SELECT count(*) FROM pg_stat_replication)`).Scan(&freeSlots, &freeSenders); err != nil {
		return err
	}
	if freeSlots < needed || freeSenders < needed {
		return &blocked{"table_addition_copy_capacity", false}
	}
	return nil
}

func refreshAddedTables(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, stage string, current func(context.Context) error) (int, error) {
	var all, added, ready, previousReady int
	err := target.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE c.relname=ANY($2::text[])),
 count(*) FILTER(WHERE r.srsubstate='r'),count(*) FILTER(WHERE r.srsubstate='r' AND NOT(c.relname=ANY($2::text[])))
 FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid=r.srsubid
 JOIN pg_class c ON c.oid=r.srrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE s.subname=$1 AND n.nspname='public' AND c.relname=ANY($3::text[])`, pipelineName(p), p.SchemaChange.AddedTables, p.Tables).Scan(&all, &added, &ready, &previousReady)
	if err != nil {
		return 0, err
	}
	if previousReady != len(p.SchemaChange.Before) || all-added != previousReady {
		return 0, &blocked{"replication_state_lost", true}
	}
	if added == 0 && stage == "published" {
		if err = admitAddedCopy(ctx, source, target, len(p.SchemaChange.AddedTables)); err != nil {
			return 0, err
		}
		if err = current(ctx); err != nil {
			return 0, err
		}
		// REFRESH commits independently. Existing subscription relations retain
		// their 'r' state; only previously absent tables enter native initial copy.
		if _, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" REFRESH PUBLICATION WITH (copy_data=true)"); err != nil {
			return 0, err
		}
	} else if added != len(p.SchemaChange.AddedTables) {
		return 0, &blocked{"table_addition_tracking_lost", false}
	}
	if stage == "published" {
		if err = saveTableStage(ctx, p, target, "copying", current); err != nil {
			return 0, err
		}
	}
	return ready, nil
}
