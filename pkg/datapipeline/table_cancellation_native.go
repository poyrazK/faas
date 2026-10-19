package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cancellationNativeRelations(ctx context.Context, p Pipeline, target *pgx.Conn) ([]nativeCopyRelation, error) {
	if c := p.SchemaChange.Cancellation; c.Kind == "live" {
		plan := *p.SchemaChange
		plan.Before = slices.DeleteFunc(slices.Clone(plan.Before), func(r relation) bool { return r.Name == c.Table })
		p.SchemaChange = &plan
	}
	return nativeCopyRelations(ctx, p, target)
}

func cancellationSubscription(ctx context.Context, p Pipeline, target *pgx.Conn) error {
	var present, owned bool
	err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())),
 EXISTS(SELECT 1 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND (oid=$2 OR $2::oid=0) AND coalesce(obj_description(oid,'pg_subscription'),'')=$3)`, pipelineName(p), p.SchemaChange.Cancellation.SubscriptionOID, marker(p)).Scan(&present, &owned)
	if err != nil {
		return err
	}
	if !present {
		return &blocked{"replication_state_lost", true}
	}
	if !owned {
		return &blocked{"table_cancellation_conflict", false}
	}
	return nil
}

func cancellationIdentities(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) (TableCancellation, error) {
	c := *p.SchemaChange.Cancellation
	var owned bool
	err := target.QueryRow(ctx, `SELECT coalesce(to_regclass($1)::oid,0),s.oid,
 to_regclass($1) IS NULL OR coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3
 FROM pg_subscription s WHERE s.subname=$2 AND s.subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, tableIdent(c.Table), pipelineName(p), marker(p)).Scan(&c.TableOID, &c.SubscriptionOID, &owned)
	if err != nil {
		return c, err
	}
	if !owned {
		return c, &blocked{"table_cancellation_conflict", false}
	}
	err = source.QueryRow(ctx, `SELECT oid,coalesce((SELECT pr.prrelid FROM pg_publication_rel pr WHERE pr.prpubid=p.oid AND pr.prrelid=to_regclass($3)),0)
 FROM pg_publication p WHERE pubname=$1 AND coalesce(obj_description(oid,'pg_publication'),'')=$2`, pipelineName(p), marker(p), tableIdent(c.Table)).Scan(&c.PublicationOID, &c.SourceTableOID)
	return c, err
}

func cancellationMembership(ctx context.Context, p Pipeline, source *pgxpool.Pool) ([]string, error) {
	var members []string
	var oid uint32
	if err := source.QueryRow(ctx, `SELECT oid FROM pg_publication WHERE pubname=$1 AND coalesce(obj_description(oid,'pg_publication'),'')=$2`, pipelineName(p), marker(p)).Scan(&oid); err != nil {
		return nil, err
	}
	if oid != p.SchemaChange.Cancellation.PublicationOID {
		return nil, &blocked{"table_cancellation_conflict", false}
	}
	if err := source.QueryRow(ctx, `SELECT coalesce(array_agg(tablename::text ORDER BY tablename COLLATE "C"),'{}') FROM pg_publication_tables WHERE pubname=$1`, pipelineName(p)).Scan(&members); err != nil {
		return nil, err
	}
	without := slices.DeleteFunc(slices.Clone(p.Tables), func(name string) bool { return name == p.SchemaChange.Cancellation.Table })
	if !slices.Equal(members, p.Tables) && !slices.Equal(members, without) && !slices.Equal(members, relationNames(p.SchemaChange.Before)) {
		return nil, &blocked{"table_cancellation_conflict", false}
	}
	actual := p
	actual.Tables = members
	return members, ensurePublication(ctx, actual, source)
}

// REFRESH(copy_data=false) must never introduce another untracked new table,
// or withdraw a table that this approval did not name.
func cancellationRelations(ctx context.Context, p Pipeline, target *pgx.Conn, members []string) (bool, int, error) {
	relations, err := cancellationNativeRelations(ctx, p, target)
	if err != nil {
		return false, 0, err
	}
	c := p.SchemaChange.Cancellation
	present, ready := false, 0
	for _, r := range relations {
		if r.state == "r" && r.name != c.Table {
			ready++
		}
		if r.name == c.Table {
			if r.oid != c.TableOID {
				return false, 0, ErrConflict
			}
			present = true
		} else if !slices.Contains(members, r.name) {
			return false, 0, &blocked{"table_cancellation_other_tracking_lost", false}
		}
	}
	if present && len(relations) != len(p.Tables) {
		return false, 0, &blocked{"table_cancellation_other_tracking_lost", false}
	}
	return present, ready, nil
}

func withdrawCancelledPublication(ctx context.Context, p Pipeline, source *pgxpool.Pool, current func(context.Context) error) error {
	members, err := cancellationMembership(ctx, p, source)
	if err != nil {
		return err
	}
	c := p.SchemaChange.Cancellation
	if !slices.Contains(members, c.Table) {
		return nil
	}
	tx, err := source.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(c.Table)+" IN SHARE MODE"); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass($1)::oid=$2 AND EXISTS(SELECT 1 FROM pg_publication WHERE oid=$3 AND pubname=$4 AND coalesce(obj_description(oid,'pg_publication'),'')=$5)`, tableIdent(c.Table), c.SourceTableOID, c.PublicationOID, pipelineName(p), marker(p)).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return &blocked{"table_cancellation_conflict", false}
	}
	if _, err = tx.Exec(ctx, "ALTER PUBLICATION "+ident(pipelineName(p))+" DROP TABLE "+tableIdent(c.Table)); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retireCancelledRelation(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	members, err := cancellationMembership(ctx, p, source)
	if err != nil {
		return err
	}
	present, _, err := cancellationRelations(ctx, p, target, members)
	if err != nil {
		return err
	}
	if err = withdrawCancelledPublication(ctx, p, source, current); err != nil {
		return err
	}
	if present {
		if err = current(ctx); err != nil {
			return err
		}
		if _, err = target.Exec(ctx, "ALTER SUBSCRIPTION "+ident(pipelineName(p))+" REFRESH PUBLICATION WITH (copy_data=false)"); err != nil {
			return err
		}
	}
	c := p.SchemaChange.Cancellation
	var active bool
	if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$1 AND srrelid=$2)
 OR EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$1 AND relid=$2 AND pid IS NOT NULL)`, c.SubscriptionOID, c.TableOID).Scan(&active); err != nil {
		return err
	}
	if active {
		return &blocked{"table_cancellation_retiring", false}
	}
	return saveCancellationStage(ctx, p, target, "detached", current)
}
