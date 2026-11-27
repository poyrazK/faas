// Package maskedbranch copies an explicitly selected PostgreSQL projection
// into a new database. It builds SQL from qualified catalog metadata and
// fixed transformations. Source rows are never written to a temporary file.
package maskedbranch

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

const (
	MaxTables   = 32
	MaxColumns  = 256
	MaxRows     = 50_000
	MaxBytes    = 64 << 20
	maxRowBytes = 1 << 20
)

var namePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Normalize validates and orders a policy so retries can compare its intent.
func Normalize(tables []api.ManagedPostgresMaskedTable) ([]api.ManagedPostgresMaskedTable, error) {
	if len(tables) == 0 || len(tables) > MaxTables {
		return nil, fmt.Errorf("%w: select between 1 and %d tables", pgerrors.ErrInvalid, MaxTables)
	}
	out := slices.Clone(tables)
	slices.SortFunc(out, func(a, b api.ManagedPostgresMaskedTable) int { return cmp.Compare(a.Name, b.Name) })
	var count int
	for i := range out {
		t := &out[i]
		count += len(t.Columns)
		if !namePattern.MatchString(t.Name) || i > 0 && out[i-1].Name == t.Name || len(t.Columns) == 0 || count > MaxColumns {
			return nil, fmt.Errorf("%w: use unique lowercase table names and select at most %d columns in total", pgerrors.ErrInvalid, MaxColumns)
		}
		t.Columns = slices.Clone(t.Columns)
		slices.SortFunc(t.Columns, func(a, b api.ManagedPostgresMaskedColumn) int { return cmp.Compare(a.Name, b.Name) })
		for j, c := range t.Columns {
			if !namePattern.MatchString(c.Name) || j > 0 && t.Columns[j-1].Name == c.Name {
				return nil, fmt.Errorf("%w: use unique lowercase column names within each table", pgerrors.ErrInvalid)
			}
			switch c.Action {
			case "keep", "email", "token", "null":
			default:
				return nil, fmt.Errorf("%w: each column needs a keep, email, token or null action", pgerrors.ErrInvalid)
			}
		}
	}
	return out, nil
}
