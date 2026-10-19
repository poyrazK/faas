package datapipeline

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

func readerPredicate(subjects []string) string {
	quoted := make([]string, len(subjects))
	for i, subject := range subjects {
		quoted[i] = literal(subject)
	}
	return "(coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->>'sub') = ANY(ARRAY[" + strings.Join(quoted, ",") + "]::text[])"
}

// This private receipt is the authority for every later gate opening, including
// when the policy commit succeeded but the catalog acknowledgement was lost.
func readerAuthority(ctx context.Context, p Pipeline, tx receiptSQL, exclusive bool) ([]string, string, int64, error) {
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.reader_access(
 pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,revision bigint NOT NULL DEFAULT 0,change_id text NOT NULL DEFAULT '',subjects text[] NOT NULL)`); err != nil {
		return nil, "", 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO gregale_pipeline.reader_access(pipeline_id,generation,subjects)
 SELECT pipeline_id,generation,$3::text[] FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring
 ON CONFLICT DO NOTHING`, p.ID, p.Generation, p.ReadSubjects); err != nil {
		return nil, "", 0, err
	}
	lock := " FOR SHARE"
	if exclusive {
		lock = " FOR UPDATE"
	}
	var subjects []string
	var id string
	var revision int64
	err := tx.QueryRow(ctx, `SELECT subjects,change_id,revision FROM gregale_pipeline.reader_access WHERE pipeline_id=$1 AND generation=$2`+lock, p.ID, p.Generation).Scan(&subjects, &id, &revision)
	return subjects, id, revision, err
}

// Called under the ordinary per-pipeline session lock, before connecting to the
// source. Access revocation does not require source connectivity or copy slots.
func applyReaders(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) (*ReaderChange, error) {
	c := p.ReaderChange
	if c == nil || c.State != "approved" || p.DesiredState == "deleted" {
		return nil, nil
	}
	if c.Generation != p.Generation || c.ID != readerChangeID(p, *c) || !validReaders(c.After) {
		return nil, ErrConflict
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return nil, err
	}
	subjects, id, revision, err := readerAuthority(ctx, p, tx, true)
	if err != nil {
		return nil, err
	}
	if revision > p.Revision || id == c.ID && !slices.Equal(subjects, c.After) {
		return nil, ErrConflict
	}
	if id != c.ID {
		if !slices.Equal(subjects, c.Before) {
			return nil, ErrConflict
		}
		// Missing future tables are allowed: their CREATE and final gate opening
		// use this same authority. Retained private tables are never included.
		rows, err := tx.Query(ctx, `SELECT c.relname::text,c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relname=ANY($1::text[]) ORDER BY c.relname`, p.Tables)
		if err != nil {
			return nil, err
		}
		type ownedTable struct {
			name string
			oid  uint32
		}
		var tables []ownedTable
		for rows.Next() {
			var t ownedTable
			if err = rows.Scan(&t.name, &t.oid); err != nil {
				break
			}
			tables = append(tables, t)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, table := range tables {
			if err = validateOwnedReadPolicy(ctx, p, tx, table.name, table.oid); err != nil {
				return nil, err
			}
			var closed bool
			if err = tx.QueryRow(ctx, `SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid=$1 AND polname='gregale_pipeline_read'`, table.oid).Scan(&closed); err != nil {
				return nil, err
			}
			// Do not acquire a DDL lock on COPY's long-running transaction.
			if closed {
				continue
			}
			if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(table.name)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				return nil, err
			}
			if err = validateOwnedReadPolicy(ctx, p, tx, table.name, table.oid); err != nil {
				return nil, err
			}
			if err = tx.QueryRow(ctx, `SELECT pg_get_expr(polqual,polrelid)='false' FROM pg_policy WHERE polrelid=$1 AND polname='gregale_pipeline_read'`, table.oid).Scan(&closed); err != nil {
				return nil, err
			}
			if !closed {
				if _, err = tx.Exec(ctx, "ALTER POLICY gregale_pipeline_read ON "+tableIdent(table.name)+" USING ("+readerPredicate(c.After)+")"); err != nil {
					return nil, err
				}
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.reader_access SET subjects=$3,change_id=$4,revision=$5 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, c.After, c.ID, p.Revision); err != nil {
			return nil, err
		}
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	applied := *c
	applied.State = "applied"
	return &applied, nil
}
