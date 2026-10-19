package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func liveTableRepair(plan *SchemaChange) bool {
	return plan != nil && plan.Recovery != nil && plan.Recovery.Kind == "live"
}

// CopiedTables is the scope of the current copy/proof, including an existing
// table's repair. A repair never changes the customer's selected tables.
func CopiedTables(plan *SchemaChange) []string {
	if liveTableRepair(plan) {
		return []string{plan.Recovery.Table}
	}
	if plan == nil {
		return nil
	}
	return plan.AddedTables
}

func liveRepairPlan(p Pipeline, relations []relation, run, table string) SchemaChange {
	p.SchemaChange = &SchemaChange{SelectionID: run}
	plan := tablePlan(p, relations, relations)
	plan.State, plan.Supported = "verifying", true
	plan.Recovery = &TableRecovery{Kind: "live", Table: table, State: "detected", Stage: "quarantined"}
	plan.Changes = []SchemaColumnChange{{Table: table, Column: "*", Kind: "repair", Safe: true, Reason: "Replace only this table's imported rows with a fresh source copy, then verify before restoring reads."}}
	return plan
}

func ensureLiveRepairReceipt(ctx context.Context, target receiptSQL) error {
	_, err := target.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.live_repairs(
 pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,change_id text NOT NULL,plan jsonb NOT NULL,
 stage text NOT NULL CHECK(stage IN ('quarantined','complete')))`)
	return err
}

func loadLiveRepair(ctx context.Context, p Pipeline, target receiptSQL) (*SchemaChange, string, error) {
	var present bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.live_repairs') IS NOT NULL`).Scan(&present); err != nil || !present {
		return nil, "", err
	}
	var encoded []byte
	var id, stage string
	err := target.QueryRow(ctx, `SELECT change_id,plan,stage FROM gregale_pipeline.live_repairs WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&id, &encoded, &stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if stage == "complete" && (p.SchemaChange == nil || p.SchemaChange.ID != id) {
		return nil, "", nil
	}
	var plan SchemaChange
	if json.Unmarshal(encoded, &plan) != nil || !liveTableRepair(&plan) || plan.ID != id || len(plan.SelectionID) != 64 || !slices.Equal(plan.RequestedTables, p.Tables) || !reflect.DeepEqual(plan.Before, plan.After) || !slices.Equal(relationNames(plan.After), p.Tables) || !containsTable(p.Tables, plan.Recovery.Table) || liveRepairPlan(p, plan.Before, plan.SelectionID, plan.Recovery.Table).ID != id {
		return nil, "", ErrConflict
	}
	return &plan, stage, nil
}

// This runs before ordinary schema/read reconciliation, so a lost catalog
// acknowledgement cannot reopen the quarantined table or start a new proof.
func (e *Engine) reconcileLiveRepair(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) (bool, error) {
	base, stage, err := loadLiveRepair(ctx, p, target)
	if err != nil || base == nil {
		return false, err
	}
	if stage == "complete" {
		if !liveTableRepair(p.SchemaChange) || p.SchemaChange.ID != base.ID || p.SchemaChange.State == "complete" {
			return false, nil
		}
		return true, e.replayLiveRepair(ctx, p, raw, source, target, owner, ob, current)
	}
	plan := base
	if liveTableRepair(p.SchemaChange) && p.SchemaChange.ID == base.ID {
		plan = p.SchemaChange
	}
	if plan.Recovery.Kind != "live" || plan.Recovery.Table != base.Recovery.Table || plan.SelectionID != base.SelectionID || !reflect.DeepEqual(plan.Before, base.Before) || !reflect.DeepEqual(plan.After, base.After) {
		return true, ErrConflict
	}
	p.SchemaChange, p.SchemaApproval = plan, plan.ID
	ob.SchemaChange, ob.State, ob.Reason = plan, "blocked", "live_table_mismatch"
	actual, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return true, err
	}
	if !reflect.DeepEqual(actual, base.After) {
		return true, &blocked{"source_schema_changed", true}
	}
	if err = checkRetention(ctx, p, source, target, ob); err != nil {
		return true, err
	}
	if err = validateLiveRepairMirror(ctx, p, target); err != nil {
		return true, err
	}
	identities, err := tableRecoveryIdentities(ctx, p, source, target)
	if err != nil {
		return true, err
	}
	r, original := identities, base.Recovery
	if r.TableOID != original.TableOID || r.SubscriptionOID != original.SubscriptionOID || r.PublicationOID != original.PublicationOID {
		return true, &blocked{"table_recovery_conflict", false}
	}
	conninfo, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return true, err
	}
	if err = ensureSubscription(ctx, p, source, target, conninfo); err != nil {
		return true, err
	}
	if plan.Recovery.State == "detected" {
		if err = ensurePublication(ctx, p, source); err != nil {
			return true, err
		}
		present, ready, err := recoveredRelationState(ctx, p, target)
		if err != nil {
			return true, err
		}
		if !present || !ready {
			return true, &blocked{"replication_state_lost", true}
		}
		checkpoint, err := loadVerification(ctx, target, p)
		if err != nil {
			return true, err
		}
		if checkpoint == nil || checkpoint.Progress.ID != plan.SelectionID || checkpoint.Progress.State != "mismatch" || checkpoint.Progress.Table != r.Table {
			return true, ErrConflict
		}
		ob.VerificationProgress, ob.Verification = &checkpoint.Progress, "mismatch"
		ob.TablesReady = len(p.Tables) - 1
		return true, observeAppliedProgress(ctx, p, source, target, ob)
	}
	return true, e.reconcileTableRecovery(ctx, p, raw, source, target, *plan, ob, current)
}

func validateLiveRepairMirror(ctx context.Context, p Pipeline, target receiptSQL) error {
	var valid bool
	err := target.QueryRow(ctx, `SELECT readable AND NOT retiring AND schema_change=$3 AND schema_phase<>'' AND schema_hash=$4 AND tables=$5
 FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, p.SchemaApproval, p.SchemaChange.BeforeHash, p.Tables).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return gateRecoveredTable(ctx, p, target)
}

func (e *Engine) replayLiveRepair(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) error {
	stage, err := loadTableRecovery(ctx, p, target)
	if err != nil || stage != "complete" {
		return ErrConflict
	}
	checkpoint, err := loadVerification(ctx, target, p)
	if err != nil {
		return err
	}
	if checkpoint == nil || checkpoint.Progress.State != "passed" || !slices.Equal(checkpoint.Tables, CopiedTables(p.SchemaChange)) {
		return ErrConflict
	}
	scope := p
	scope.Tables = CopiedTables(p.SchemaChange)
	relations := slices.DeleteFunc(slices.Clone(p.SchemaChange.After), func(r relation) bool { return !containsTable(scope.Tables, r.Name) })
	intent, spec := verificationID(scope, relations)
	if checkpoint.Progress.IntentID != intent || checkpoint.Spec != spec {
		return ErrConflict
	}
	plan, recovery := *p.SchemaChange, *p.SchemaChange.Recovery
	plan.State, recovery.State, recovery.Stage = "complete", "complete", "complete"
	plan.Recovery = &recovery
	p.SchemaChange, p.VerifyRequested = &plan, false
	ob.SchemaChange, ob.VerificationProgress = &plan, &checkpoint.Progress
	ob.Verification, ob.VerifiedAt = "unknown", time.Time{}
	return e.reconcile(ctx, p, raw, source, target, owner, ob, current)
}
