package datapipeline

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func liveRemovalPlan(p Pipeline, before []relation) SchemaChange {
	seed := *p.SchemaChange
	seed.SelectionID = seed.Cancellation.RequestID
	p.SchemaChange = &seed
	plan := tablePlan(p, before, before)
	plan.State, plan.Supported, plan.Cancellation = "removal_pending", true, seed.Cancellation
	return plan
}

func (e *Engine) reconcileTableRemoval(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) error {
	if err := ensureSchemaReceipt(ctx, target); err != nil {
		return err
	}
	if err := ensureTableReceipt(ctx, target); err != nil {
		return err
	}
	c := p.SchemaChange.Cancellation
	if len(p.Tables) < 2 || !containsTable(p.Tables, c.Table) || len(c.RequestID) == 0 {
		return &blocked{"table_cancellation_conflict", false}
	}
	if c.State == "requested" {
		var spec []byte
		var hash string
		var tables []string
		if err := target.QueryRow(ctx, `SELECT schema_spec,schema_hash,tables FROM gregale_pipeline.mirrors
 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND readable AND schema_phase=''`, p.ID, p.Generation).Scan(&spec, &hash, &tables); err != nil {
			return err
		}
		var before []relation
		if json.Unmarshal(spec, &before) != nil || !slices.Equal(tables, p.Tables) || !slices.Equal(relationNames(before), p.Tables) || relationHash(before) != hash {
			return &blocked{"table_cancellation_conflict", false}
		}
		plan := liveRemovalPlan(p, before)
		p.SchemaChange = &plan
	}
	plan := p.SchemaChange
	if !slices.Equal(plan.RequestedTables, p.Tables) || !slices.Equal(relationNames(plan.Before), p.Tables) || !reflect.DeepEqual(plan.Before, plan.After) || liveRemovalPlan(p, plan.Before).ID != plan.ID {
		return &blocked{"table_cancellation_conflict", false}
	}
	// Preview uses the proposed approval identity. The catalog adopts it only
	// after exact confirmation; no remote changes occur while previewing.
	p.SchemaApproval = plan.ID
	return e.reconcileTableCancellation(ctx, p, raw, source, target, owner, ob, current)
}

func validateLiveRemovalTable(ctx context.Context, p Pipeline, target receiptSQL) error {
	c := p.SchemaChange.Cancellation
	return validateOwnedReadPolicy(ctx, p, target, c.Table, c.TableOID)
}

func validateOwnedReadPolicy(ctx context.Context, p Pipeline, target receiptSQL, table string, oid uint32) error {
	var valid bool
	err := target.QueryRow(ctx, `SELECT coalesce((SELECT c.oid=$2 AND c.relrowsecurity AND coalesce(obj_description(c.oid,'pg_class'),'')=$3
 AND EXISTS(SELECT 1 FROM pg_policy pol WHERE pol.polrelid=c.oid AND pol.polname='gregale_pipeline_read' AND pol.polcmd='r')
 AND NOT EXISTS(SELECT 1 FROM pg_policy pol WHERE pol.polrelid=c.oid AND pol.polname<>'gregale_pipeline_read' AND pol.polcmd IN ('r','*'))
 FROM pg_class c WHERE c.oid=to_regclass($1)),false)`, tableIdent(table), oid, marker(p)).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return &blocked{"table_cancellation_conflict", false}
	}
	return nil
}

func previewLiveRemoval(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) error {
	members, err := cancellationMembership(ctx, p, source)
	if err != nil {
		return err
	}
	if !slices.Equal(members, p.Tables) {
		return &blocked{"table_cancellation_conflict", false}
	}
	native, err := cancellationNativeRelations(ctx, p, target)
	if err != nil {
		return err
	}
	if len(native) != len(p.Tables) {
		return &blocked{"table_cancellation_conflict", false}
	}
	for _, r := range native {
		if r.state != "r" {
			return &blocked{"table_cancellation_conflict", false}
		}
	}
	return validateLiveRemovalTable(ctx, p, target)
}

func tableRetirementReason(c TableCancellation, phase string) string {
	if c.Kind == "live" {
		return "table_removal_" + phase
	}
	return "table_cancellation_" + phase
}
