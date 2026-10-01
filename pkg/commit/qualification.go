package commit

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// QualifySchema checks the supported table contract before the managed relay
// writes delivery metadata. Views and tables without stable unique event IDs
// cannot provide the recovery boundary. This never installs or repairs DDL.
func QualifySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("commit: outbox schema unavailable")
	}
	required := map[string]string{
		"event_id": "uuid", "event_type": "text", "payload": "jsonb", "created_at": "timestamptz",
		"accepted_at": "timestamptz", "receipt_id": "uuid", "invocation_id": "uuid",
		"lease_token": "uuid", "lease_until": "timestamptz", "next_attempt_at": "timestamptz",
		"attempts": "int4", "blocked_code": "text",
	}
	rows, err := pool.Query(ctx, `SELECT a.attname,t.typname FROM pg_attribute a
 JOIN pg_class c ON c.oid=a.attrelid JOIN pg_type t ON t.oid=a.atttypid
 WHERE c.oid=to_regclass('public.gregale_outbox') AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped`)
	if err != nil {
		return errors.New("commit: outbox schema unavailable")
	}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			rows.Close()
			return errors.New("commit: outbox schema unavailable")
		}
		if expected, ok := required[name]; ok {
			if expected != kind {
				rows.Close()
				return errors.New("commit: incompatible outbox column")
			}
			delete(required, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(required) > 0 {
		return errors.New("commit: outbox schema incomplete")
	}
	var unique bool
	err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attname='event_id'
 WHERE c.conrelid=to_regclass('public.gregale_outbox') AND c.contype='p' AND c.conkey=ARRAY[a.attnum]::smallint[])`).Scan(&unique)
	if err != nil || !unique {
		return errors.New("commit: event ID primary key is required")
	}
	return nil
}

// QualifySource requires the database owner's fixed source binding. Another
// source, even using another credential or hostname for this database, must
// not claim its events. Installing and binding the schema is an owner action.
func QualifySource(ctx context.Context, pool *pgxpool.Pool, sourceID string) error {
	var matches bool
	err := pool.QueryRow(ctx, `SELECT count(*)=1 AND COALESCE(bool_and(singleton AND source_id=$1::uuid),false)
 FROM public.gregale_commit_binding`, sourceID).Scan(&matches)
	if err != nil || !matches {
		return errors.New("commit: database source binding is not qualified")
	}
	return nil
}
