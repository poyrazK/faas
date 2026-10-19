package datapipeline

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (e *Engine) reconcileTables(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) error {
	if err := ensureSchemaReceipt(ctx, target); err != nil {
		return err
	}
	if err := ensureTableReceipt(ctx, target); err != nil {
		return err
	}
	var generation int64
	var spec []byte
	var saved, change, phase, stage string
	var retiring bool
	if err := target.QueryRow(ctx, `SELECT generation,schema_hash,schema_spec,schema_change,schema_phase,table_stage,retiring FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&generation, &saved, &spec, &change, &phase, &stage, &retiring); err != nil {
		return err
	}
	if generation != p.Generation || retiring {
		return &blocked{"replication_state_lost", true}
	}
	if stage == "complete" && change == p.SchemaApproval && change == p.SchemaChange.ID {
		checkpoint, err := loadVerification(ctx, target, p)
		if err != nil {
			return err
		}
		if checkpoint != nil && checkpoint.Progress.State == "passed" {
			p.VerificationProgress, ob.VerificationProgress = &checkpoint.Progress, &checkpoint.Progress
		}
		copy := *p.SchemaChange
		copy.State = "complete"
		if copy.Recovery != nil {
			r := *copy.Recovery
			r.State, r.Stage = "complete", "complete"
			copy.Recovery = &r
		}
		p.SchemaChange, ob.SchemaChange = &copy, &copy
		// Recover lost completion acknowledgement, then use ordinary schema
		// reconciliation so later source drift still closes the appropriate gate.
		return e.reconcile(ctx, p, raw, source, target, owner, ob, current)
	}
	var before []relation
	if json.Unmarshal(spec, &before) != nil {
		return ErrConflict
	}
	if change == p.SchemaApproval && change == p.SchemaChange.ID && phase != "" {
		before = p.SchemaChange.Before
	} else if phase != "" {
		return &blocked{"schema_receipt_conflict", false}
	}
	actual := make([]relation, 0, len(p.SchemaChange.RequestedTables))
	for _, name := range p.SchemaChange.RequestedTables {
		r, table, err := inspectSourceTable(ctx, source, name)
		if err != nil {
			return err
		}
		r.Unsupported = !table.Supported
		actual = append(actual, r)
	}
	for _, old := range before {
		i := slices.IndexFunc(actual, func(r relation) bool { return r.Name == old.Name })
		if i < 0 || !reflect.DeepEqual(old, actual[i]) {
			return &blocked{"source_schema_changed", true}
		}
	}
	plan := tablePlan(p, before, actual)
	if p.SchemaChange.ID == plan.ID {
		plan.Retry = p.SchemaChange.Retry
		plan.Recovery = p.SchemaChange.Recovery
		plan.Copies = p.SchemaChange.Copies
		plan.Cancellation = p.SchemaChange.Cancellation
	}
	if p.SchemaApproval != "" && plan.ID != p.SchemaApproval {
		copy := *p.SchemaChange
		copy.State = "verifying"
		ob.SchemaChange = &copy
		return &blocked{"table_addition_source_changed", false}
	}
	if phase != "" && (plan.ID != p.SchemaApproval || !plan.Supported) {
		ob.SchemaChange = p.SchemaChange
		return &blocked{"table_addition_source_changed", false}
	}
	ob.SchemaChange = &plan
	if err := checkRetention(ctx, p, source, target, ob); err != nil {
		return err
	}
	if phase == "" {
		previous := p
		previous.Tables = relationNames(before)
		if saved != relationHash(before) || len(before) == 0 {
			return &blocked{"schema_receipt_conflict", false}
		}
		if err := ensurePublication(ctx, previous, source); err != nil {
			return err
		}
		conninfo, err := e.sourceConninfo(ctx, raw)
		if err != nil {
			return err
		}
		if err = ensureSubscription(ctx, previous, source, target, conninfo); err != nil {
			return err
		}
		ob.State, ob.Reason, ob.TablesReady = "live", "table_addition_pending", len(before)
		if err = observeAppliedProgress(ctx, previous, source, target, ob); err != nil {
			return err
		}
		if !plan.Supported {
			ob.Reason = "table_addition_unsupported"
			return nil
		}
		if p.SchemaApproval != plan.ID {
			return nil
		}
		if err = prepareAddedTables(ctx, p, source, target, owner, plan, current); err != nil {
			return err
		}
		stage = "prepared"
	}
	if !slices.Equal(p.Tables, plan.RequestedTables) || p.SchemaApproval != plan.ID {
		return ErrConflict
	}
	p.SchemaChange = &plan
	for _, name := range plan.AddedTables {
		var owned bool
		if err := target.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass($1),'pg_class'),'')=$2`, tableIdent(name), marker(p)).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return &blocked{"table_addition_destination_conflict", false}
		}
	}
	if plan.Recovery != nil {
		return e.reconcileTableRecovery(ctx, p, raw, source, target, plan, ob, current)
	}
	if stage == "prepared" {
		if err := publishAddedTables(ctx, p, source, plan, current); err != nil {
			return err
		}
		if err := saveTableStage(ctx, p, target, "published", current); err != nil {
			return err
		}
		stage = "published"
	} else if err := ensurePublication(ctx, p, source); err != nil {
		return err
	}
	conninfo, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return err
	}
	if err = ensureSubscription(ctx, p, source, target, conninfo); err != nil {
		return err
	}
	plan.State = "verifying"
	_, err = refreshAddedTables(ctx, p, source, target, stage, current)
	// Missing new-table tracking is observable and explicitly recoverable.
	if err != nil && !isBlockedReason(err, "table_addition_tracking_lost") {
		return err
	}
	ready, observeErr := observeTableCopies(ctx, p, target, current)
	if observeErr != nil {
		return observeErr
	}
	ob.TablesReady = ready
	if err != nil {
		return err
	}
	if err = observeAppliedProgress(ctx, p, source, target, ob); err != nil {
		return err
	}
	return e.finishTableCopy(ctx, p, source, target, plan, ready, ob, current)
}

func (e *Engine) finishTableCopy(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, plan SchemaChange, ready int, ob *Observation, current func(context.Context) error) error {
	ob.SchemaChange = &plan
	ob.TablesReady = ready
	if ready == len(p.Tables) {
		return e.finishTables(ctx, p, source, target, plan, ob, current)
	}
	ob.State, ob.Reason = "copying", "table_addition_copying"
	for _, c := range plan.Copies {
		if c.Phase == "missing" {
			ob.State, ob.Reason = "blocked", "table_addition_tracking_lost"
			break
		}
		if c.Stalled {
			ob.State, ob.Reason = "blocked", "table_copy_stalled"
		}
	}
	return nil
}

func (e *Engine) finishTables(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, plan SchemaChange, ob *Observation, current func(context.Context) error) error {
	scope := p
	scope.Tables = CopiedTables(&plan)
	relations := []relation{}
	for _, r := range plan.After {
		if containsTable(scope.Tables, r.Name) {
			relations = append(relations, r)
		}
	}
	ready, err := verifyResumable(ctx, scope, source, target, relations, ob, current)
	ob.SchemaChange = &plan
	ob.State, ob.Reason = "blocked", "table_addition_verifying"
	if liveTableRepair(&plan) {
		ob.Reason = "live_table_repair_verifying"
	}
	if verificationTransient(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if ob.VerificationProgress != nil {
		switch ob.VerificationProgress.State {
		case "cancelled":
			ob.Reason = "verification_cancelled"
			return nil
		case "mismatch", "limited":
			ob.Reason = ob.VerificationProgress.Reason
			return nil
		}
	}
	if !ready {
		return nil
	}
	plan.State = "refreshing"
	if err = saveTableStage(ctx, p, target, "refreshing", current); err != nil {
		return err
	}
	if e.RefreshSchema == nil {
		ob.Reason = "schema_api_refresh_unavailable"
		return nil
	}
	refreshes, ready, err := e.RefreshSchema(ctx, p, plan)
	plan.Refreshes = refreshes
	if err != nil {
		ob.Reason = "schema_api_refresh_failed"
		return nil //nolint:nilerr // Persist sanitized retry state, never provider diagnostics.
	}
	if !ready {
		ob.Reason = "schema_api_refresh_pending"
		return nil
	}
	sealed, err := sealVerification(ctx, scope, source, target, relations, ob, current, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET table_stage='complete',schema_phase='' WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3`, p.ID, p.Generation, plan.ID); err != nil {
			return err
		}
		var recoveryReceipt bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.table_recoveries') IS NOT NULL`).Scan(&recoveryReceipt); err != nil {
			return err
		}
		if recoveryReceipt {
			if _, err := tx.Exec(ctx, `UPDATE gregale_pipeline.table_recoveries SET stage='complete' WHERE pipeline_id=$1 AND generation=$2 AND change_id=$3`, p.ID, p.Generation, plan.ID); err != nil {
				return err
			}
		}
		if liveTableRepair(&plan) {
			tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.live_repairs SET stage='complete' WHERE pipeline_id=$1 AND generation=$2 AND change_id=$3 AND stage='quarantined'`, p.ID, p.Generation, plan.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrConflict
			}
		}
		return updateMirrorReads(ctx, scope, tx, true)
	})
	if err != nil && !verificationTransient(err) {
		return err
	}
	if sealed {
		plan.State = "complete"
		if plan.Recovery != nil {
			plan.Recovery.State, plan.Recovery.Stage = "complete", "complete"
		}
		ob.State, ob.Reason = "live", ""
		if liveTableRepair(&plan) {
			ob.Verification, ob.VerifiedAt = "unknown", time.Time{}
		}
	}
	return nil
}
