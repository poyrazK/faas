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

// Preserve the shared stream on a table-attributed mismatch. The false policy,
// original mismatch identity and private workflow commit under the same fence.
func quarantineLiveMismatch(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, relations []relation, ob *Observation, current func(context.Context) error) error {
	if !p.Initialized || len(relations) == 0 || tableWorkflow(p) || activeTableCancellation(p) || p.SchemaChange != nil && p.SchemaChange.State != "complete" && p.SchemaChange.State != "withdrawn" {
		return &blocked{"replica_mismatch", true}
	}
	checkpoint, err := loadVerification(ctx, target, p)
	if err != nil {
		return err
	}
	_, spec := verificationID(p, relations)
	if checkpoint == nil || checkpoint.Spec != spec || checkpoint.Progress.State != "mismatch" || !slices.Equal(checkpoint.Tables, p.Tables) || !containsTable(p.Tables, checkpoint.Progress.Table) {
		return &blocked{"verification_tracking_lost", true}
	}
	plan := liveRepairPlan(p, relations, checkpoint.Progress.ID, checkpoint.Progress.Table)
	p.SchemaChange, p.SchemaApproval = &plan, plan.ID
	identities, err := tableRecoveryIdentities(ctx, p, source, target)
	if err != nil {
		return err
	}
	plan.Recovery = &identities
	if err = ensurePublication(ctx, p, source); err != nil {
		return err
	}
	native, err := nativeCopyRelations(ctx, p, target)
	if err != nil {
		return err
	}
	if len(native) != len(p.Tables) || slices.ContainsFunc(native, func(r nativeCopyRelation) bool { return r.state != "r" }) {
		return &blocked{"replication_state_lost", true}
	}
	if err = ensureTableReceipt(ctx, target); err != nil {
		return err
	}
	if err = ensureLiveRepairReceipt(ctx, target); err != nil {
		return err
	}
	if err = commitLiveQuarantine(ctx, p, target, plan, current); err != nil {
		return err
	}
	ob.SchemaChange, ob.VerificationProgress = &plan, &checkpoint.Progress
	ob.State, ob.Reason, ob.TablesReady = "blocked", "live_table_mismatch", len(p.Tables)-1
	ob.Verification, ob.VerifiedAt = "mismatch", time.Time{}
	return nil
}

func commitLiveQuarantine(ctx context.Context, p Pipeline, target *pgx.Conn, plan SchemaChange, current func(context.Context) error) error {
	identities := plan.Recovery
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	var baseline []byte
	if err = tx.QueryRow(ctx, `SELECT schema_spec FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND readable
 AND schema_phase='' AND schema_hash=$3 AND tables=$4 FOR UPDATE`, p.ID, p.Generation, plan.BeforeHash, p.Tables).Scan(&baseline); err != nil {
		return err
	}
	var before []relation
	if json.Unmarshal(baseline, &before) != nil || !reflect.DeepEqual(before, plan.After) {
		return &blocked{"schema_receipt_conflict", true}
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(identities.Table)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	if err = validateOwnedReadPolicy(ctx, p, tx, identities.Table, identities.TableOID); err != nil {
		return err
	}
	scope := p
	scope.Tables = []string{identities.Table}
	if err = updateTableReads(ctx, scope, tx, false); err != nil {
		return err
	}
	encoded, _ := json.Marshal(plan)
	tag, err := tx.Exec(ctx, `INSERT INTO gregale_pipeline.live_repairs(pipeline_id,generation,change_id,plan,stage) VALUES($1,$2,$3,$4,'quarantined')
 ON CONFLICT(pipeline_id) DO UPDATE SET generation=excluded.generation,change_id=excluded.change_id,plan=excluded.plan,stage=excluded.stage
 WHERE live_repairs.stage='complete' AND live_repairs.change_id<>excluded.change_id`, p.ID, p.Generation, plan.ID, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_change=$3,schema_phase='verifying',table_stage='prepared' WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, plan.ID); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
