package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type receiptSQL interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureSchemaReceipt(ctx context.Context, target receiptSQL) error {
	var present bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='gregale_pipeline.mirrors'::regclass AND attname='schema_spec' AND NOT attisdropped)`).Scan(&present); err != nil {
		return err
	}
	if present {
		return nil
	}
	_, err := target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors ADD COLUMN schema_spec jsonb NOT NULL DEFAULT '[]', ADD COLUMN schema_change text NOT NULL DEFAULT '', ADD COLUMN schema_phase text NOT NULL DEFAULT '' CHECK (schema_phase IN ('','verifying','refreshing'))`)
	return err
}

func reconcileSchema(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, owner string, relations []relation, hash string, ob *Observation, current func(context.Context) error) (bool, bool, error) {
	var exists bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&exists); err != nil {
		return false, false, err
	}
	if !exists {
		return false, false, nil
	}
	if err := ensureSchemaReceipt(ctx, target); err != nil {
		return false, false, err
	}
	var generation int64
	var saved, change, phase string
	var spec []byte
	var retiring bool
	err := target.QueryRow(ctx, `SELECT generation,schema_hash,schema_spec,schema_change,schema_phase,retiring FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&generation, &saved, &spec, &change, &phase, &retiring)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if generation != p.Generation || retiring {
		return false, false, &blocked{"replication_state_lost", true}
	}
	var baseline []relation
	if json.Unmarshal(spec, &baseline) != nil {
		return false, false, ErrConflict
	}
	if len(baseline) == 0 && saved == hash {
		encoded, _ := json.Marshal(relations)
		_, err = target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_spec=$3 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, encoded)
		return false, false, err
	}
	same := saved == hash && reflect.DeepEqual(baseline, relations)
	if same {
		if phase == "" && p.SchemaChange != nil && p.SchemaChange.State == "pending" && p.SchemaChange.ID != change {
			copy := *p.SchemaChange
			copy.State = "withdrawn"
			ob.SchemaChange = &copy
			return false, false, nil
		}
		if phase == "" && (p.SchemaChange == nil || p.SchemaChange.ID != change || p.SchemaChange.State == "complete") {
			return false, false, nil
		}
		if p.SchemaChange == nil || p.SchemaChange.ID != change || p.SchemaApproval != change {
			return false, false, &blocked{"schema_receipt_conflict", false}
		}
		copy := *p.SchemaChange
		if phase == "" {
			checkpoint, err := loadVerification(ctx, target, p)
			if err != nil {
				return false, false, err
			}
			if checkpoint != nil && checkpoint.Progress.State == "passed" {
				ob.VerificationProgress = &checkpoint.Progress
			}
			copy.State = "complete"
			ob.SchemaChange = &copy
			return false, false, nil
		}
		ob.SchemaChange = &copy
		return true, false, nil
	}
	plan := schemaPlan(p, baseline, relations)
	if p.SchemaChange != nil && p.SchemaChange.ID == plan.ID {
		plan.Retry = p.SchemaChange.Retry
	}
	ob.SchemaChange = &plan
	if err = allowMirrorReads(ctx, p, target, false); err != nil {
		return false, false, err
	}
	if err = setSubscriptionEnabled(ctx, p, target, false); err != nil {
		return false, false, err
	}
	var slot bool
	if err = source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1 AND plugin='pgoutput' AND database=current_database())`, pipelineName(p)).Scan(&slot); err != nil {
		return false, false, err
	}
	if !slot {
		return false, false, &blocked{"replication_state_lost", true}
	}
	if err = ensurePublication(ctx, p, source); err != nil {
		return false, false, err
	}
	var subscription bool
	if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()))`, pipelineName(p)).Scan(&subscription); err != nil {
		return false, false, err
	}
	if !subscription {
		return false, false, &blocked{"replication_state_lost", true}
	}
	if err = checkRetention(ctx, p, source, target, ob); err != nil {
		return false, false, err
	}
	// A later plan must not turn partially backfilled columns into an assumed
	// complete baseline. Finish or explicitly recover the unfinished migration.
	if phase == "verifying" {
		return false, false, &blocked{"schema_receipt_conflict", true}
	}
	if !plan.Supported {
		return false, false, &blocked{"source_schema_changed", true}
	}
	if p.SchemaApproval != plan.ID {
		ob.State, ob.Reason = "blocked", "schema_change_pending"
		return false, true, nil
	}
	if err = applySchema(ctx, p, source, target, owner, plan, current); err != nil {
		return false, false, err
	}
	plan.State = "verifying"
	return true, false, nil
}

func applySchema(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, owner string, plan SchemaChange, current func(context.Context) error) error {
	if p.SchemaApproval != plan.ID || !plan.Supported {
		return ErrConflict
	}
	// Drain source writes and prevent further DDL while checking the approved
	// definition and committing the target DDL. Row backfill follows in bounded
	// verification transactions; columns remain nullable until it finishes.
	sourceTx, err := lockSchemaSource(ctx, p, source)
	if err != nil {
		return err
	}
	defer func() { _ = sourceTx.Rollback(ctx) }()
	actual, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	if schemaPlan(p, plan.Before, actual).ID != plan.ID {
		return ErrConflict
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var saved string
	if err = tx.QueryRow(ctx, `SELECT schema_hash FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring FOR UPDATE`, p.ID, p.Generation).Scan(&saved); err != nil {
		return err
	}
	if saved != plan.BeforeHash {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
		return err
	}
	for _, r := range plan.After {
		var owned bool
		if err = tx.QueryRow(ctx, `SELECT coalesce(obj_description(c.oid,'pg_class'),'')=$2 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, r.Name, marker(p)).Scan(&owned); err != nil || !owned {
			if err != nil {
				return err
			}
			return ErrConflict
		}
		for _, delta := range plan.Changes {
			if delta.Table != r.Name || delta.Kind != "added" {
				continue
			}
			if _, err = tx.Exec(ctx, "ALTER TABLE "+tableIdent(r.Name)+" ADD COLUMN "+ident(delta.Column)+" "+delta.AfterType); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
		return err
	}
	encoded, _ := json.Marshal(plan.After)
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_hash=$3,schema_spec=$4,schema_change=$5,schema_phase='verifying',barrier='0/0' WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, plan.AfterHash, encoded, plan.ID); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *Engine) finishSchema(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, ob *Observation, current func(context.Context) error) (bool, error) {
	if ob.SchemaChange == nil {
		return false, ErrConflict
	}
	plan := ob.SchemaChange
	ready, err := verifyResumable(ctx, p, source, target, relations, ob, current)
	if verificationTransient(err) {
		ob.State, ob.Reason = "blocked", "schema_change_verifying"
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ob.VerificationProgress != nil {
		switch ob.VerificationProgress.State {
		case "mismatch":
			plan.State = "unsupported"
			return false, &blocked{"replica_mismatch", true}
		case "cancelled":
			plan.State = "verifying"
			ob.State, ob.Reason = "blocked", "verification_cancelled"
			return false, nil
		case "limited":
			plan.State = "verifying"
			ob.State, ob.Reason = "blocked", ob.VerificationProgress.Reason
			return false, nil
		}
	}
	if !ready {
		plan.State = "verifying"
		ob.State, ob.Reason = "blocked", "schema_change_verifying"
		return false, nil
	}
	plan.State = "refreshing"
	if err = prepareSchemaRefresh(ctx, p, target, *plan, current); err != nil {
		if verificationTransient(err) {
			plan.State = "verifying"
			ob.State, ob.Reason = "blocked", "schema_constraints_pending"
			return false, nil
		}
		return false, err
	}
	if e.RefreshSchema == nil {
		ob.State, ob.Reason = "blocked", "schema_api_refresh_unavailable"
		return false, nil
	}
	refreshes, ready, err := e.RefreshSchema(ctx, p, *plan)
	plan.Refreshes = refreshes
	if err != nil {
		ob.State, ob.Reason = "blocked", "schema_api_refresh_failed"
		return false, nil //nolint:nilerr // Persist sanitized recovery state without exposing provider diagnostics.
	}
	if !ready {
		ob.State, ob.Reason = "blocked", "schema_api_refresh_pending"
		return false, nil
	}
	sealed, err := sealVerification(ctx, p, source, target, relations, ob, current, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_phase='' WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3`, p.ID, p.Generation, plan.ID); err != nil {
			return err
		}
		return updateMirrorReads(ctx, p, tx, true)
	})
	if err != nil && !verificationTransient(err) {
		return false, err
	}
	if !sealed {
		if ob.VerificationProgress != nil && ob.VerificationProgress.State == "mismatch" {
			plan.State = "unsupported"
			return false, &blocked{"replica_mismatch", true}
		}
		ob.State, ob.Reason = "blocked", "schema_change_verifying"
		return false, nil
	}
	plan.State = "complete"
	return true, nil
}

func lockSchemaSource(ctx context.Context, p Pipeline, source *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := source.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = verificationSetting(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	names := make([]string, len(p.Tables))
	for i, name := range p.Tables {
		names[i] = tableIdent(name)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(names, ",")+" IN SHARE MODE"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
