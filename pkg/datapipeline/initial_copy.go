package datapipeline

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A missing registration can never finish copying. Once every relation has
// reached ready, a regression to another native state is a shared stream failure.
// Persist that boundary independently of catalog replies and later read gates.
func observeInitialCopy(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) (int, bool, error) {
	inspection := p
	inspection.SchemaChange = &SchemaChange{}
	relations, err := nativeCopyRelations(ctx, inspection, target)
	if err != nil {
		return 0, false, err
	}
	if len(relations) != len(p.Tables) {
		return 0, false, &blocked{"replication_state_lost", true}
	}
	ready := 0
	for _, r := range relations {
		if r.state == "r" {
			ready++
		}
	}
	var completed bool
	if err = target.QueryRow(ctx, `SELECT readable OR coalesce((source_slot->>'Ready')::boolean,false) FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation).Scan(&completed); err != nil {
		return 0, false, err
	}
	if ready != len(p.Tables) {
		if completed {
			return 0, false, &blocked{"replication_state_lost", true}
		}
		return ready, true, nil
	}
	if completed {
		return ready, true, nil
	}
	if err = recordSourceSlot(ctx, p, source, target, false, current); err != nil {
		return 0, false, err
	}
	w, err := loadSourceSlot(ctx, p, target)
	if err != nil {
		return 0, false, err
	}
	if w == nil || w.Peer == "" {
		return ready, false, nil
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET source_slot=jsonb_set(source_slot,'{Ready}','true') WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation); err != nil {
		return 0, false, err
	}
	if err = current(ctx); err != nil {
		return 0, false, err
	}
	return ready, true, tx.Commit(ctx)
}
