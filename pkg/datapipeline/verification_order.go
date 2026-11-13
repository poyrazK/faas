package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Approval descriptors intentionally omit planner metadata. Refresh key positions
// even when this relation came from an older durable table/repair plan.
func orderedVerificationRelations(ctx context.Context, source *pgxpool.Pool, relations []relation) ([]relation, error) {
	result := slices.Clone(relations)
	for i, r := range result {
		var names []string
		err := source.QueryRow(ctx, `SELECT array_agg(a.attname::text ORDER BY k.position)
 FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum,position)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum
 WHERE i.indrelid=$1 AND i.indisprimary AND i.indisvalid AND i.indimmediate AND k.position<=i.indnkeyatts`, r.OID).Scan(&names)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 || len(names) != len(primaryColumns(r)) {
			return nil, ErrConflict
		}
		result[i].Columns = slices.Clone(r.Columns)
		for j, c := range result[i].Columns {
			position := slices.Index(names, c.Name)
			if c.Key != (position >= 0) {
				return nil, ErrConflict
			}
			result[i].Columns[j].KeyPosition = int32(position + 1)
		}
	}
	return result, nil
}
