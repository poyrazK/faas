package datapipeline

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

func (e *Engine) connectSource(ctx context.Context, raw string) (*pgxpool.Pool, error) {
	if e.openSource != nil {
		return e.openSource(ctx, raw)
	}
	return commit.OpenPool(ctx, raw, e.Policy)
}

func blockedCheck(code, message, remediation string) api.DataConnectionCheck {
	return api.DataConnectionCheck{State: "blocked", Checks: []api.DataConnectionIssue{inspectionIssue(code, message, remediation, false)}, Tables: []api.DataSourceTable{}}
}

// Check inspects bounded metadata with verified TLS and the replication network
// policy. It creates no publications, slots, tables, or subscriptions.
func (e *Engine) Check(ctx context.Context, job ConnectionCheck, raw string) api.DataConnectionCheck {
	result := blockedCheck("source_connection_unavailable", "Source connection failed", "Check the credential, verified TLS certificate, and approved source network access.")
	source, err := e.connectSource(ctx, raw)
	if err != nil {
		return result
	}
	defer source.Close()
	if err = source.Ping(ctx); err != nil {
		return result
	}
	checks, err := inspectSourceSettings(ctx, source)
	if err != nil {
		return blockedCheck("source_metadata_unavailable", "Source settings could not be inspected", "Allow the source role to inspect replication settings and run the check again.")
	}
	result.Checks = append([]api.DataConnectionIssue{inspectionIssue("source_connection", "Verified source connection", "", true)}, checks...)
	capacity, err := inspectSourceCapacity(ctx, source)
	if err != nil {
		return blockedCheck("source_capacity_unavailable", "Source capacity could not be inspected", "Allow replication capacity inspection and run the check again.")
	}
	result.Checks = append(result.Checks, capacity...)
	allocation, allocationErr := e.Capacity.source(ctx, job, raw, source)
	result.Capacity = &api.DataPipelineCapacity{State: "needs_qualification", Source: capacityReport(allocation, connectioncapacity.PipelineSourceDemand(), allocationErr)}
	result.Checks = append(result.Checks, capacityIssue("source", result.Capacity.Source))
	rows, err := source.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f') ORDER BY c.relname COLLATE "C" LIMIT $1`, api.DataPipelineDiscoveryTablesMax+1)
	if err != nil {
		return blockedCheck("source_metadata_unavailable", "Source tables could not be discovered", "Check source catalog access and run the check again.")
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			break
		}
		names = append(names, name)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return blockedCheck("source_metadata_unavailable", "Source discovery did not finish", "Run the check again after restoring source connectivity.")
	}
	if len(names) > api.DataPipelineDiscoveryTablesMax {
		result.Truncated = true
		names = names[:api.DataPipelineDiscoveryTablesMax]
	}
	metadataBytes := 0
	for _, name := range names {
		_, table, err := inspectSourceTable(ctx, source, name)
		if err != nil {
			return blockedCheck("source_metadata_unavailable", "Source discovery did not finish", "Run the check again after restoring source connectivity.")
		}
		// Indented JSON conservatively covers PostgreSQL jsonb's whitespace
		// expansion. Encode each table once to keep wide discovery linear.
		encoded, err := json.MarshalIndent(table, "  ", " ")
		if err != nil || metadataBytes+len(encoded) > api.DataPipelineCheckMaxBytes-api.DataPipelineCheckEnvelopeBytes {
			result.Truncated = true
			break
		}
		metadataBytes += len(encoded)
		result.Tables = append(result.Tables, table)
	}
	if job.DatabaseID != "" {
		if e.Target == nil {
			return blockedCheck("destination_unavailable", "Mirror could not be checked", "Choose a supported, ready managed database.")
		}
		err = e.Target(ctx, job.AccountID, job.DatabaseID, func(target *pgx.Conn, _ string) error {
			allocation, allocationErr := e.Capacity.target(ctx, job.AccountID, job.DatabaseID, target)
			result.Capacity.Destination = capacityReport(allocation, connectioncapacity.PipelineTargetDemand(), allocationErr)
			result.Checks = append(result.Checks, capacityIssue("mirror", result.Capacity.Destination))
			checks, err := inspectTargetCapacity(ctx, target)
			result.Checks = append(result.Checks, checks...)
			if err != nil {
				return err
			}
			occupied, err := targetOccupied(ctx, target)
			if err != nil {
				return err
			}
			result.Checks = append(result.Checks, inspectionIssue("destination_not_empty", "Empty dedicated mirror", "Choose an empty managed database without public application relations or functions.", !occupied))
			return nil
		})
		if err != nil {
			return blockedCheck("destination_unavailable", "Mirror could not be checked", "Check the managed database provider and retry.")
		}
	}
	if result.Capacity.Source.State == "ready" && result.Capacity.Destination != nil && result.Capacity.Destination.State == "ready" {
		result.Capacity.State = "ready"
	}
	if result.Capacity.Source.State == "insufficient_capacity" || (result.Capacity.Destination != nil && result.Capacity.Destination.State == "insufficient_capacity") {
		result.Capacity.State = "insufficient_capacity"
	}
	result.State = "passed"
	for _, check := range result.Checks {
		if check.State == "blocked" {
			result.State = "blocked"
		}
	}
	return result
}

func targetOccupied(ctx context.Context, target *pgx.Conn) (bool, error) {
	var occupied bool
	err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f')) OR EXISTS(SELECT 1 FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='public')`).Scan(&occupied)
	return occupied, err
}

func inspectSourceCapacity(ctx context.Context, source *pgxpool.Pool) ([]api.DataConnectionIssue, error) {
	var slots, senders int64
	err := source.QueryRow(ctx, `SELECT current_setting('max_replication_slots')::bigint-(SELECT count(*) FROM pg_replication_slots),
 current_setting('max_wal_senders')::bigint-(SELECT count(*) FROM pg_stat_replication)`).Scan(&slots, &senders)
	if err != nil {
		return nil, err
	}
	peak := int64(api.DataPipelineCopyWorkersMax + 1)
	return []api.DataConnectionIssue{
		inspectionIssue("source_replication_slots_capacity", "Replication slot capacity", "Free at least three replication slots for the apply worker and initial-copy workers.", slots >= peak),
		inspectionIssue("source_wal_senders_capacity", "Replication sender capacity", "Free at least three WAL sender connections for initial copying and continued replication.", senders >= peak),
	}, nil
}

func inspectTargetCapacity(ctx context.Context, target *pgx.Conn) ([]api.DataConnectionIssue, error) {
	var version int
	if err := target.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil {
		return nil, err
	}
	if version < 160000 || version >= 170000 {
		return []api.DataConnectionIssue{inspectionIssue("destination_postgres_16_required", "Mirror version is unsupported", "Choose a PostgreSQL 16 managed mirror.", false)}, nil
	}
	var subscribe bool
	var copyWorkers, logicalWorkers, processes, activeWorkers int64
	err := target.QueryRow(ctx, `SELECT (r.rolsuper OR pg_has_role(current_user,'pg_create_subscription','USAGE')) AND has_database_privilege(current_database(),'CREATE'),
 current_setting('max_sync_workers_per_subscription')::bigint,current_setting('max_logical_replication_workers')::bigint,
 current_setting('max_worker_processes')::bigint,(SELECT count(*) FROM pg_stat_subscription WHERE pid IS NOT NULL)
 FROM pg_roles r WHERE r.rolname=current_user`).Scan(&subscribe, &copyWorkers, &logicalWorkers, &processes, &activeWorkers)
	if err != nil {
		return nil, err
	}
	return []api.DataConnectionIssue{
		inspectionIssue("destination_subscription_permission_required", "Native subscription permission", "Use a provider qualified for native PostgreSQL subscriptions.", subscribe),
		inspectionIssue("destination_copy_workers_limit", "Initial-copy worker limit", "Configure the mirror with one or two initial-copy workers per subscription.", copyWorkers >= 1 && copyWorkers <= api.DataPipelineCopyWorkersMax),
		inspectionIssue("destination_replication_workers_capacity", "Replication worker capacity", "Free capacity for the apply worker and configured initial-copy workers.", logicalWorkers-activeWorkers >= copyWorkers+1 && processes >= logicalWorkers),
	}, nil
}

func firstBlocked(checks []api.DataConnectionIssue) error {
	for _, check := range checks {
		if check.State == "blocked" {
			return &blocked{code: check.Code}
		}
	}
	return nil
}

// Capacity is checked again immediately before creating native resources.
// Existing generations retain their workers and do not reserve a second copy.
func admitNativeCopy(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) error {
	var slotExists bool
	if err := source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1)`, pipelineName(p)).Scan(&slotExists); err != nil {
		return err
	}
	if p.Initialized || slotExists {
		return nil
	}
	checks, err := inspectSourceCapacity(ctx, source)
	if err != nil {
		return err
	}
	if err = firstBlocked(checks); err != nil {
		return err
	}
	checks, err = inspectTargetCapacity(ctx, target)
	if err != nil {
		return err
	}
	return firstBlocked(checks)
}
