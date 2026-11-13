package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidIndexes reports indexes that Postgres marks invalid after a
// migration run.
var ErrInvalidIndexes = errors.New("db: invalid indexes after migrate")

// invalidIndexesQuery lists the application schema's invalid indexes. A
// concurrent build cancelled part way (statement_timeout, a lock timeout, a
// deadlock) leaves one behind: every write still maintains it, no read uses
// it, and CREATE INDEX CONCURRENTLY IF NOT EXISTS skips it on the next run
// (bug hunt H73).
const invalidIndexesQuery = `
select n.nspname || '.' || c.relname
  from pg_index i
  join pg_class c on c.oid = i.indexrelid
  join pg_namespace n on n.oid = c.relnamespace
 where not i.indisvalid
   and n.nspname not in ('pg_catalog', 'information_schema')
   and n.nspname not like 'pg_toast%'
 order by 1`

// CheckNoInvalidIndexes fails when any index is invalid, naming each one, so
// a deploy stops on a half-built index instead of recording its migration
// as applied.
func CheckNoInvalidIndexes(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, invalidIndexesQuery)
	if err != nil {
		return fmt.Errorf("db: list invalid indexes: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("db: scan invalid index: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("db: list invalid indexes: %w", err)
	}
	if len(names) > 0 {
		return fmt.Errorf("%w: %s; a concurrent build was cancelled part way. Rebuild each one "+
			"with REINDEX INDEX CONCURRENTLY, or drop it and rerun the deploy if its migration is still pending "+
			"(docs/runbooks/migration-rollback.md)", ErrInvalidIndexes, strings.Join(names, ", "))
	}
	return nil
}
