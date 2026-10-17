package datapipeline

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// observeAppliedProgress keeps a source WAL checkpoint in the generation-owned
// target receipt. Only the native subscription's durable replication origin can
// acknowledge it. Receiver heartbeats and publisher slot flushes are insufficient.
// An idle publication may never advance its origin: unknown is honest in that case.
func observeAppliedProgress(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, ob *Observation) error {
	var installed bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='gregale_pipeline.mirrors'::regclass AND attname='progress_barrier' AND NOT attisdropped)`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		if _, err := target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors ADD COLUMN progress_barrier pg_lsn, ADD COLUMN progress_sampled_at timestamptz, ADD COLUMN applied_through_at timestamptz`); err != nil {
			return err
		}
	}
	var barrier string
	var sampled, applied pgtype.Timestamptz
	var reached bool
	if err := target.QueryRow(ctx, `SELECT coalesce(m.progress_barrier::text,''),m.progress_sampled_at,m.applied_through_at,
 coalesce(o.remote_lsn>=m.progress_barrier AND o.local_lsn<=pg_current_wal_flush_lsn(),false)
 FROM gregale_pipeline.mirrors m
 JOIN pg_subscription s ON s.subname=$3 AND s.subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())
 LEFT JOIN pg_replication_origin_status o ON o.external_id='pg_'||s.oid::text
 WHERE m.pipeline_id=$1 AND m.generation=$2`, p.ID, p.Generation, pipelineName(p)).Scan(&barrier, &sampled, &applied, &reached); err != nil {
		return err
	}
	if reached && sampled.Valid {
		applied = sampled
	}
	ob.AppliedThroughAt = applied.Time
	if barrier != "" && !reached {
		return nil
	}
	// Sample the time before the WAL read, giving a conservative lower bound.
	sampledAt := time.Now().UTC()
	if err := source.QueryRow(ctx, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&barrier); err != nil {
		return err
	}
	_, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET progress_barrier=$3::pg_lsn,progress_sampled_at=$4,applied_through_at=$5 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, barrier, sampledAt, applied)
	return err
}

func pipelineHealth(p api.DataPipeline, now time.Time) *api.DataPipelineHealth {
	h := &api.DataPipelineHealth{State: "attention", Code: p.Reason}
	switch {
	case p.DesiredState == "deleted":
		h.State, h.Code, h.Message, h.Recovery = "inactive", "cleanup_pending", "Pipeline cleanup is pending.", "Keep the source reachable until cleanup completes."
		if p.State == "deleted" {
			h.Code, h.Message, h.Recovery = "deleted", "Pipeline deleted.", "Imported rows were retained."
		}
	case p.DesiredState == "paused" && p.State != "resync_required":
		h.State, h.Code, h.Message, h.Recovery, h.Action = "inactive", "paused", "Sync is paused.", "Resume to continue replication; monitor retained WAL while paused.", "resume"
	case !p.ObservationFresh:
		h.State, h.Code, h.Message, h.Recovery = "unknown", "monitoring_stale", "Monitoring is stale; data freshness is unknown.", "Check worker availability and refresh status. Native replication may still be running."
	case p.State == "resync_required":
		h.Code, h.Message, h.Recovery, h.Action, h.RequiresConfirmation = p.Reason, "Replication requires a new copy.", "Resolve the reported source problem, then confirm replacement of the imported tables. Reads can be interrupted.", "resync", true
	case p.Reason == "table_addition_pending":
		h.Message, h.Recovery, h.RequiresConfirmation = "New tables need approval.", "Review the table preview and reader access in Data connections. Existing tables remain readable.", true
	case p.Reason == "table_addition_unsupported":
		h.Message, h.Recovery = "A selected table is unsupported.", "Review discovery issues or discard the pending selection. Existing tables remain readable."
	case p.Reason == "table_addition_copying" || p.Reason == "table_addition_verifying":
		h.Message, h.Recovery = "New tables are copying or verifying.", "Review saved progress. Existing tables remain readable; new tables stay gated through API refresh."
	case p.Reason == "table_recovery_pending":
		h.Message, h.Recovery, h.RequiresConfirmation = "One failed table needs recovery approval.", "Review its exact preview to replace only that table's imported rows. Healthy tables remain readable and syncing.", true
	case p.Reason == "live_table_mismatch":
		h.Message, h.Recovery, h.RequiresConfirmation = "Verification found a mismatch in one live table.", "That table's reads are gated. Preview its repair in Data connections to replace only its imported rows. Other tables remain readable and syncing.", true
	case p.Reason == "live_table_repair_copying" || p.Reason == "live_table_repair_verifying":
		h.Message, h.Recovery = "One live table is being repaired.", "Its reads stay gated until the fresh copy, verification and API refresh complete. Saved stages resume after a restart; other tables remain readable and syncing."
	case p.Reason == "table_recovery_copying":
		h.Message, h.Recovery = "One failed new table is being copied again.", "Saved recovery stages resume after a restart. Healthy tables remain readable and syncing; recovered rows stay gated through verification and API refresh."
	case p.Reason == "table_copy_stalled":
		h.Message, h.Recovery = "A new table has no recent observed copy progress.", "Review per-table progress and native capacity or source access. You can preview recovery of the affected table; healthy tables remain readable and syncing."
	case p.Reason == "table_recovery_retiring":
		h.Message, h.Recovery = "A table's previous copy worker is retiring.", "Recovery waits for native relation and worker retirement before clearing any rows. Healthy tables remain readable and syncing."
	case p.Reason == "table_cancellation_pending":
		h.Message, h.Recovery, h.RequiresConfirmation = "A new table's onboarding cancellation needs approval.", "Review the named table. Cancellation stops its sync and retains imported rows privately; existing tables keep syncing.", true
	case p.Reason == "table_removal_pending":
		h.Message, h.Recovery, h.RequiresConfirmation = "Live-table removal needs approval.", "Review the exact table. Approval closes only its read gate, stops its sync and retains imported rows privately. Remaining tables keep syncing.", true
	case p.Reason == "table_removal_retiring" || p.Reason == "table_removal_refreshing":
		h.Message, h.Recovery = "The selected live table is being removed.", "Its reads are gated while native cleanup and API refresh resume from saved stages. Remaining tables keep syncing; imported rows are retained privately."
	case p.Reason == "table_cancellation_retiring" || p.Reason == "table_cancellation_refreshing":
		h.Message, h.Recovery = "A new table's onboarding is being cancelled.", "Saved cleanup stages resume after a restart. PostgreSQL retires only this table's copy worker; imported rows are retained privately and the API is refreshed."
	case p.Reason == "table_cancellation_conflict" || p.Reason == "table_cancellation_other_tracking_lost":
		h.Message, h.Recovery = "Table cancellation needs attention.", "Restore the saved native identities and tracking before retrying cleanup. Existing table reads remain available."
	case p.Reason == "table_cancellation_retention_limit":
		h.Message, h.Recovery = "The retained-table limit has been reached.", "Ask the database owner to export and remove unneeded private retained tables before retrying cancellation. No imported rows are deleted automatically."
	case p.Reason == "table_recovery_other_tracking_lost":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "Another new table also lost copy tracking.", "Restore native tracking or confirm full recovery. A table-only refresh cannot safely adopt another untracked table.", "resync", true
	case p.Reason == "table_recovery_conflict":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "Table recovery identities changed.", "Restore the approved native identities or confirm a full resync. Healthy tables remain readable until that resync.", "resync", true
	case p.Reason == "replica_mismatch" && p.State == "blocked":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "Verification found different imported data.", "For a failed newly added table, preview table recovery in Data connections with its approved source identity, schema and primary key. Otherwise confirm a full resync to replace the imported tables.", "resync", true
	case p.Reason == "table_addition_source_changed" || p.Reason == "table_addition_tracking_lost" || p.Reason == "table_addition_destination_conflict":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "New-table onboarding needs recovery.", "Restore the approved source and owned destination definition, or confirm a resync. Existing tables remain readable until recovery is requested.", "resync", true
	case p.Reason == "table_addition_copy_capacity":
		h.Message, h.Recovery = "New-table copy is waiting for capacity.", "Free native table-copy worker, sender or slot capacity. Existing tables remain readable."
	case p.Reason == "schema_change_pending":
		h.Message, h.Recovery, h.RequiresConfirmation = "Source schema changes need approval.", "Review the exact changes in Data connections. Replication and reads are gated; retained WAL remains bounded.", true
	case p.Reason == "schema_change_verifying":
		h.Message, h.Recovery = "The schema change is backfilling and verifying data.", "Review saved progress. Only approved new columns are backfilled; reads remain gated through verification and API refresh."
	case p.Reason == "schema_constraints_pending":
		h.Message, h.Recovery = "The schema change is waiting to enforce required columns.", "Saved backfill progress is retained. Constraint checks retry within the statement and lock budgets; reads remain gated."
	case p.Reason == "verification_cancelled":
		h.Message, h.Recovery = "Verification was stopped.", "Retry the approved schema workflow to start a fresh check. Reads remain gated."
	case p.Reason == "verification_row_limit" || p.Reason == "verification_range_limit":
		h.Message, h.Recovery = "Verification reached its work limit.", "Review the verification limits, then retry or confirm an explicit resync. Reads remain gated for schema changes."
	case p.Reason == "verification_tracking_lost":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "Verification tracking was lost.", "The destination no longer has complete change tracking. Confirm a resync to restore a trustworthy mirror.", "resync", true
	case p.Reason == "schema_receipt_conflict":
		h.Message, h.Recovery, h.Action, h.RequiresConfirmation = "Schema recovery needs attention.", "The saved approval no longer matches the mirror's unfinished migration. Confirm a resync to rebuild from the current source definition.", "resync", true
	case p.Reason == "schema_api_refresh_pending" || p.Reason == "schema_api_refresh_failed" || p.Reason == "schema_api_refresh_unavailable":
		h.Message, h.Recovery = "The schema change is waiting for API refresh.", "Review the saved refresh operations. Failed refreshes can be retried in Data connections; reads remain gated."
	case p.Reason == "credential_unavailable" || p.Reason == "source_authentication_failed":
		h.Message, h.Recovery, h.Action = "Source credentials need attention.", "Rotate the source connection credential, then wait for reconciliation.", "rotate_credentials"
	case p.State == "blocked":
		h.Message, h.Recovery = "Sync is blocked.", "Check source and mirror reachability, permissions, and capacity. Reconciliation retries automatically."
	case p.RetainedWALBytes != nil && *p.RetainedWALBytes >= api.DataPipelineRetentionWarningBytes:
		h.Code, h.Message, h.Recovery, h.Action = "retention_pressure", "Retained WAL is approaching the safety limit.", "Check destination apply progress and available capacity. Resume if paused; increasing retention requires operator review.", ""
	case p.State == "copying" || p.State == "validating":
		h.State, h.Code, h.Message, h.Recovery = "pending", "initial_copy", "The initial copy is not complete.", "Wait for all selected tables to become ready."
	default:
		applied, err := time.Parse(time.RFC3339Nano, p.AppliedThroughAt)
		if err != nil || now.Before(applied) || now.Sub(applied) >= api.DataPipelineObservationMaxAge {
			h.State, h.Code, h.Message, h.Recovery = "unknown", "apply_unconfirmed", "Recent applied progress is unconfirmed.", "A quiet source may leave the WAL checkpoint unconfirmed. If source rows are changing, check replication health; do not resync solely for this status."
		} else {
			h.State, h.Code, h.Message, h.Recovery = "healthy", "applied_progress_confirmed", "The destination applied a recent source WAL checkpoint.", "This is a confirmed lower bound, not a measured time lag or a row-by-row verification."
		}
	}
	return h
}
