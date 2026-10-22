package datapipeline

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (e *Engine) reconcileTableCancellation(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) error {
	plan, c := *p.SchemaChange, *p.SchemaChange.Cancellation
	plan.Cancellation, p.SchemaChange = &c, &plan
	ob.SchemaChange, ob.State, ob.Reason = &plan, "blocked", tableRetirementReason(c, "pending")
	validTable := c.Kind == "" && containsTable(plan.AddedTables, c.Table) || c.Kind == "live" && len(plan.Before) > 1 && containsTable(relationNames(plan.Before), c.Table)
	if !validTable || c.Retention != "retain_private" {
		return &blocked{"table_cancellation_conflict", false}
	}
	if err := ensureTableCancellationReceipt(ctx, target); err != nil {
		return err
	}
	if err := ensureTableCopyReceipt(ctx, target); err != nil {
		return err
	}
	stage, completed, err := loadTableCancellation(ctx, p, target)
	if err != nil {
		return err
	}
	if stage == "" && c.State != "approved" && c.Kind == "" {
		var finished bool
		if err = target.QueryRow(ctx, `SELECT table_stage='complete' AND schema_change=$2 AND schema_phase='' FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$3`, p.ID, p.SchemaApproval, p.Generation).Scan(&finished); err != nil {
			return err
		}
		if finished {
			plan.State, c.State, c.Stage = "complete", "withdrawn", "complete"
			return e.reconcile(ctx, p, raw, source, target, owner, ob, current)
		}
	}
	if err = checkRetention(ctx, p, source, target, ob); err != nil {
		return err
	}
	if err = cancellationSubscription(ctx, p, target); err != nil {
		return err
	}
	conninfo, err := e.sourceConninfo(ctx, raw)
	if err != nil {
		return err
	}
	if err = ensureSubscription(ctx, p, source, target, conninfo); err != nil {
		return err
	}
	native, err := cancellationNativeRelations(ctx, p, target)
	if err != nil {
		return err
	}
	for _, r := range native {
		if r.state == "r" && r.name != c.Table {
			ob.TablesReady++
		}
	}
	if stage == "complete" {
		members, err := cancellationMembership(ctx, p, source)
		if err != nil {
			return err
		}
		present, ready, err := cancellationRelations(ctx, p, target, members)
		if err != nil {
			return err
		}
		if present || slices.Contains(members, c.Table) {
			return ErrConflict
		}
		ob.TablesReady = ready
		return observeCancellationResult(completed, ob)
	}
	if stage != "archived" {
		actual, identityErr := cancellationIdentities(ctx, p, source, target)
		if identityErr != nil {
			return identityErr
		}
		if c.State == "requested" || c.State == "pending" {
			members, memberErr := cancellationMembership(ctx, cancellationWith(p, actual), source)
			if memberErr != nil {
				return memberErr
			}
			if _, _, err = cancellationRelations(ctx, cancellationWith(p, actual), target, members); err != nil {
				return err
			}
			if c.Kind == "live" {
				if err = previewLiveRemoval(ctx, cancellationWith(p, actual), source, target); err != nil {
					return err
				}
			} else {
				if err = cancellationGate(ctx, cancellationWith(p, actual), target); err != nil {
					return err
				}
			}
			if actual.TableOID != 0 {
				if err = cancellationRetentionCapacity(ctx, target); err != nil {
					return err
				}
			}
			actual.ID = tableCancellationID(p, actual)
			if c.State == "pending" && c.ID != actual.ID {
				return &blocked{"table_cancellation_conflict", false}
			}
			actual.State, actual.Stage = "pending", "preview"
			plan.Cancellation = &actual
			return observeAppliedProgress(ctx, p, source, target, ob)
		}
		// An absent publication member is an expected committed withdrawal.
		if actual.SourceTableOID != 0 && actual.SourceTableOID != c.SourceTableOID {
			return &blocked{"table_cancellation_conflict", false}
		}
		actual.SourceTableOID = c.SourceTableOID
		if c.State != "approved" || c.ID != tableCancellationID(p, c) || c.ID != tableCancellationID(p, actual) {
			return &blocked{"table_cancellation_conflict", false}
		}
	}
	ob.Reason = tableRetirementReason(c, "retiring")
	switch stage {
	case "":
		if c.Kind == "live" {
			if err = previewLiveRemoval(ctx, p, source, target); err != nil {
				return err
			}
		}
		err = beginTableCancellation(ctx, p, target, current)
		c.Stage = "retiring"
	case "retiring":
		err = retireCancelledRelation(ctx, p, source, target, current)
		c.Stage = "detached"
	case "detached":
		members, memberErr := cancellationMembership(ctx, p, source)
		if memberErr != nil {
			return memberErr
		}
		if slices.Contains(members, c.Table) {
			return ErrConflict
		}
		err = archiveCancelledTable(ctx, p, target, current)
		c.Stage = "archived"
	case "archived":
		return e.finishTableCancellation(ctx, p, source, target, ob, current)
	default:
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return observeAppliedProgress(ctx, p, source, target, ob)
}

func cancellationWith(p Pipeline, c TableCancellation) Pipeline {
	plan := *p.SchemaChange
	plan.Cancellation = &c
	p.SchemaChange = &plan
	return p
}

func (e *Engine) finishTableCancellation(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, ob *Observation, current func(context.Context) error) error {
	members, err := cancellationMembership(ctx, p, source)
	if err != nil {
		return err
	}
	if slices.Contains(members, p.SchemaChange.Cancellation.Table) {
		return ErrConflict
	}
	present, ready, err := cancellationRelations(ctx, p, target, members)
	if err != nil {
		return err
	}
	if present {
		return ErrConflict
	}
	result := cancellationResult(p)
	ob.Reason = tableRetirementReason(*p.SchemaChange.Cancellation, "refreshing")
	if e.RefreshSchema == nil {
		return &blocked{"schema_api_refresh_unavailable", false}
	}
	refresh := SchemaChange{ID: p.SchemaChange.Cancellation.ID}
	refreshes, refreshed, err := e.RefreshSchema(ctx, p, refresh)
	ob.SchemaChange.Refreshes = refreshes
	if err != nil {
		return &blocked{"schema_api_refresh_failed", false}
	}
	if !refreshed {
		ob.Reason = "schema_api_refresh_pending"
		return nil
	}
	var stage string
	if err = target.QueryRow(ctx, `SELECT table_stage FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&stage); err != nil {
		return err
	}
	// Preserve admission history. Missing tracking after a committed initial
	// copy must not become a new copy merely because another table was cancelled.
	if stage == "refreshing" {
		stage = "copying"
	}
	if err = completeTableCancellation(ctx, p, target, &result, stage, current); err != nil {
		return err
	}
	ob.TablesReady = ready
	return observeCancellationResult(&result, ob)
}

func observeCancellationResult(result *SchemaChange, ob *Observation) error {
	ob.SchemaChange, ob.VerificationProgress = result, &VerificationProgress{}
	ob.Verification, ob.VerifiedAt = "unknown", time.Time{}
	ob.State, ob.Reason = "copying", "table_addition_copying"
	if len(result.AddedTables) == 0 {
		ob.State, ob.Reason, ob.TablesReady = "live", "", len(result.RequestedTables)
	}
	return nil
}
