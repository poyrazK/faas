package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func tableRecoveryIdentities(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) (TableRecovery, error) {
	r := *p.SchemaChange.Recovery
	err := target.QueryRow(ctx, `SELECT to_regclass($1)::oid,s.oid FROM pg_subscription s
 WHERE s.subname=$2 AND s.subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3`, tableIdent(r.Table), pipelineName(p), marker(p)).Scan(&r.TableOID, &r.SubscriptionOID)
	if err != nil {
		return r, err
	}
	err = source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1 AND coalesce(obj_description(oid,'pg_publication'),'')=$2`, pipelineName(p), marker(p)).Scan(&r.PublicationOID)
	return r, err
}

// Baseline relations stay ready and other new tables stay registered. Inspect
// the entire native relation set, including unexpected schemas and names.
func recoveredRelationState(ctx context.Context, p Pipeline, target *pgx.Conn) (present, ready bool, err error) {
	r := p.SchemaChange.Recovery
	relations, err := nativeCopyRelations(ctx, p, target)
	if err != nil {
		return false, false, err
	}
	others := 0
	for _, native := range relations {
		if native.name == r.Table {
			if native.oid != r.TableOID {
				return false, false, ErrConflict
			}
			present, ready = true, native.state == "r"
		} else {
			others++
		}
	}
	if others != len(p.Tables)-1 {
		// REFRESH must not silently register another missing table without a
		// snapshot. Baseline tracking loss was already treated as shared failure.
		return false, false, &blocked{"table_recovery_other_tracking_lost", false}
	}
	return present, ready, nil
}

// Both memberships are exact and ownership-checked. Recognizing the post-ALTER
// set recovers a lost source commit acknowledgement without repeating the DDL.
func recoveryPublication(ctx context.Context, p Pipeline, source *pgxpool.Pool, include bool, current func(context.Context) error) error {
	r := p.SchemaChange.Recovery
	without := p
	without.Tables = slices.DeleteFunc(slices.Clone(p.Tables), func(name string) bool { return name == r.Table })
	var members []string
	if err := source.QueryRow(ctx, `SELECT coalesce(array_agg(tablename::text ORDER BY tablename COLLATE "C"),'{}') FROM pg_publication_tables WHERE pubname=$1`, pipelineName(p)).Scan(&members); err != nil {
		return err
	}
	actual := p
	if slices.Equal(members, without.Tables) {
		actual = without
	} else if !slices.Equal(members, p.Tables) {
		return &blocked{"source_publication_changed", true}
	}
	if err := ensurePublication(ctx, actual, source); err != nil {
		return err
	}
	if include == containsTable(members, r.Table) {
		return nil
	}
	tx, err := lockSchemaSource(ctx, p, source)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	relations, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	if tablePlan(p, p.SchemaChange.Before, relations).ID != p.SchemaApproval {
		return &blocked{"table_addition_source_changed", false}
	}
	action := "DROP TABLE "
	if include {
		action = "ADD TABLE "
	}
	if _, err = tx.Exec(ctx, "ALTER PUBLICATION "+ident(pipelineName(p))+" "+action+tableIdent(r.Table)); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retireRecoveredRelation(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	if err := recoveryPublication(ctx, p, source, false, current); err != nil {
		return err
	}
	present, _, err := recoveredRelationState(ctx, p, target)
	if err != nil {
		return err
	}
	if present {
		if err = current(ctx); err != nil {
			return err
		}
		// PostgreSQL owns relation and table-sync slot retirement. The shared
		// subscription and main slot continue applying healthy tables.
		if _, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" REFRESH PUBLICATION WITH (copy_data=false)"); err != nil {
			return err
		}
	}
	var active bool
	if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$1 AND relid=$2 AND pid IS NOT NULL)`, p.SchemaChange.Recovery.SubscriptionOID, p.SchemaChange.Recovery.TableOID).Scan(&active); err != nil {
		return err
	}
	present, _, err = recoveredRelationState(ctx, p, target)
	if err != nil {
		return err
	}
	if active || present {
		return &blocked{"table_recovery_retiring", false}
	}
	return saveTableRecoveryStage(ctx, p, target, "detached", current)
}

func copyRecoveredRelation(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	if err := ensurePublication(ctx, p, source); err != nil {
		return err
	}
	present, _, err := recoveredRelationState(ctx, p, target)
	if err != nil {
		return err
	}
	if !present {
		if err = admitAddedCopy(ctx, source, target, 1); err != nil {
			return err
		}
		if err = current(ctx); err != nil {
			return err
		}
		if _, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" REFRESH PUBLICATION WITH (copy_data=true)"); err != nil {
			return err
		}
	}
	return saveTableRecoveryStage(ctx, p, target, "copying", current)
}
