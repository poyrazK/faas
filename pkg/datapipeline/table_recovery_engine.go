package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (e *Engine) reconcileTableRecovery(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, plan SchemaChange, ob *Observation, current func(context.Context) error) error {
	r := *plan.Recovery
	plan.Recovery, p.SchemaChange = &r, &plan
	plan.State = "verifying"
	ob.SchemaChange, ob.State, ob.Reason = &plan, "blocked", "table_recovery_pending"
	if !containsTable(CopiedTables(&plan), r.Table) {
		return ErrConflict
	}
	conninfo, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return err
	}
	if err = ensureSubscription(ctx, p, source, target, conninfo); err != nil {
		return err
	}
	actual, err := tableRecoveryIdentities(ctx, p, source, target)
	if err != nil {
		return err
	}
	readyCount, err := observeTableCopies(ctx, p, target, current)
	if err != nil {
		return err
	}
	ob.TablesReady = readyCount
	if r.State == "requested" || r.State == "pending" {
		if r.Kind == "copy" && !copyRecoveryNeeded(plan, r.Table) {
			return e.withdrawCopyRecovery(ctx, p, source, target, plan, readyCount, ob, current)
		}
		return previewTableRecovery(ctx, p, source, target, actual, ob)
	}
	if r.State != "approved" && r.State != "blocked" || r.ID != tableRecoveryID(p, r) || r.ID != tableRecoveryID(p, actual) || r.Attempt > plan.Retry {
		return &blocked{"table_recovery_conflict", false}
	}
	if err = ensureTableRecoveryReceipt(ctx, target); err != nil {
		return err
	}
	stage, err := loadTableRecovery(ctx, p, target)
	if err != nil {
		return err
	}
	ob.Reason = "table_recovery_copying"
	if stage == "" {
		if r.Kind == "copy" && !copyRecoveryNeeded(plan, r.Table) {
			return e.withdrawCopyRecovery(ctx, p, source, target, plan, readyCount, ob, current)
		}
		if err = ensurePublication(ctx, p, source); err != nil {
			return err
		}
		present, ready, stateErr := recoveredRelationState(ctx, p, target)
		if stateErr != nil {
			return stateErr
		}
		if r.Kind != "copy" && (!present || !ready) || r.Kind == "copy" && ready {
			return &blocked{"table_recovery_conflict", false}
		}
		err = beginTableRecovery(ctx, p, target, current)
		stage = "retiring"
	} else {
		switch stage {
		case "retiring":
			err = retireRecoveredRelation(ctx, p, source, target, current)
			stage = "detached"
		case "detached":
			without := p
			without.Tables = slices.DeleteFunc(slices.Clone(p.Tables), func(name string) bool { return name == r.Table })
			if err = ensurePublication(ctx, without, source); err == nil {
				err = clearRecoveredTable(ctx, p, source, target, current)
			}
			stage = "cleared"
		case "cleared":
			if err = recoveryPublication(ctx, p, source, true, current); err == nil {
				err = saveTableRecoveryStage(ctx, p, target, "published", current)
			}
			stage = "published"
		case "published":
			err = copyRecoveredRelation(ctx, p, source, target, current)
			stage = "copying"
		case "copying":
			if err = ensurePublication(ctx, p, source); err != nil {
				return err
			}
			present, ready, stateErr := recoveredRelationState(ctx, p, target)
			if stateErr != nil {
				return stateErr
			}
			if !present {
				r.State = "blocked"
				return &blocked{"table_addition_tracking_lost", false}
			}
			if ready {
				r.Stage = "verifying"
				return e.finishTableCopy(ctx, p, source, target, plan, readyCount, ob, current)
			}
			if copyRecoveryNeeded(plan, r.Table) {
				r.State = "blocked"
				ob.Reason = "table_copy_stalled"
				return observeAppliedProgress(ctx, p, source, target, ob)
			}
		default:
			return ErrConflict
		}
	}
	if err != nil {
		return err
	}
	if liveTableRepair(&plan) {
		ob.Reason = "live_table_repair_copying"
	}
	r.Stage = stage
	return observeAppliedProgress(ctx, p, source, target, ob)
}

func (e *Engine) withdrawCopyRecovery(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, plan SchemaChange, ready int, ob *Observation, current func(context.Context) error) error {
	if err := ensurePublication(ctx, p, source); err != nil {
		return err
	}
	plan.Recovery = nil
	p.SchemaChange = &plan
	return e.finishTableCopy(ctx, p, source, target, plan, ready, ob, current)
}

func previewTableRecovery(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, actual TableRecovery, ob *Observation) error {
	expected := *p.SchemaChange.Recovery
	p.SchemaChange.Recovery = &actual
	if err := ensurePublication(ctx, p, source); err != nil {
		return err
	}
	present, ready, err := recoveredRelationState(ctx, p, target)
	if err != nil {
		return err
	}
	if actual.Kind != "copy" && (!present || !ready) || actual.Kind == "copy" && ready {
		return &blocked{"table_recovery_conflict", false}
	}
	if actual.Kind == "copy" {
		if !copyRecoveryNeeded(*p.SchemaChange, actual.Table) {
			return ErrConflict
		}
	} else {
		c, err := loadVerification(ctx, target, p)
		if err != nil {
			return err
		}
		if c == nil || c.Progress.State != "mismatch" || c.Progress.Table != actual.Table {
			return ErrConflict
		}
		ob.VerificationProgress = &c.Progress
	}
	actual.Attempt = p.SchemaChange.Retry + 1
	actual.ID = tableRecoveryID(p, actual)
	if expected.State == "pending" && expected.ID != actual.ID {
		return &blocked{"table_recovery_conflict", false}
	}
	actual.State, actual.Stage = "pending", "preview"
	p.SchemaChange.Recovery = &actual
	return observeAppliedProgress(ctx, p, source, target, ob)
}

func copyRecoveryNeeded(plan SchemaChange, name string) bool {
	return slices.ContainsFunc(plan.Copies, func(c TableCopyProgress) bool {
		return c.Table == name && c.NativeState != "r" && (c.Stalled || c.Phase == "missing")
	})
}
