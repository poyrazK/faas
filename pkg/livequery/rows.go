package livequery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const keySep = "\x1f"

func rowID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(h[:16])
}

// encodeValues serializes text values with explicit NULL markers.
func encodeValues(values []*string) string {
	var b strings.Builder
	for _, v := range values {
		if v == nil {
			b.WriteString("\x00N")
			continue
		}
		b.WriteString("\x00V")
		b.WriteString(*v)
	}
	return b.String()
}

// keyedRow is a row tracked by lineage: one primary key per relation, ""
// where an outer join produced NULLs for that relation.
type keyedRow struct {
	values  []*string // visible values, group key values, or nil
	lineage []string
	enc     string // encodeValues(values)
}

// keyedSet indexes rows by each relation's lineage key.
type keyedSet struct {
	rows map[string]*keyedRow
	idx  []map[string]map[string]struct{}
}

func newKeyedSet(relations int) *keyedSet {
	s := &keyedSet{rows: map[string]*keyedRow{}, idx: make([]map[string]map[string]struct{}, relations)}
	for i := range s.idx {
		s.idx[i] = map[string]map[string]struct{}{}
	}
	return s
}

func (s *keyedSet) put(id string, r *keyedRow) {
	s.remove(id)
	s.rows[id] = r
	for i, key := range r.lineage {
		if key == "" {
			continue
		}
		ids := s.idx[i][key]
		if ids == nil {
			ids = map[string]struct{}{}
			s.idx[i][key] = ids
		}
		ids[id] = struct{}{}
	}
}

func (s *keyedSet) remove(id string) {
	r, ok := s.rows[id]
	if !ok {
		return
	}
	delete(s.rows, id)
	for i, key := range r.lineage {
		if key == "" {
			continue
		}
		delete(s.idx[i][key], id)
		if len(s.idx[i][key]) == 0 {
			delete(s.idx[i], key)
		}
	}
}

// matching returns the IDs of rows whose lineage for some relation i is in
// keys[i].
func (s *keyedSet) matching(keys []map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for i, set := range keys {
		for key := range set {
			for id := range s.idx[i][key] {
				out[id] = struct{}{}
			}
		}
	}
	return out
}

// replace swaps the rows matching keys for fresh, which must be exactly the
// current rows that match keys.
func (s *keyedSet) replace(keys []map[string]struct{}, fresh map[string]*keyedRow) {
	for id := range s.matching(keys) {
		s.remove(id)
	}
	for id, r := range fresh {
		s.put(id, r)
	}
}

// resultShape classifies a query's result columns.
type resultShape struct {
	visible []int
	names   []string
	lineage [][]int // relation -> key column positions
	groups  []int   // group key positions
}

func shapeOf(fields []string, relations []*Relation, groups int) (resultShape, error) {
	s := resultShape{lineage: make([][]int, len(relations)), groups: make([]int, groups)}
	for i := range s.groups {
		s.groups[i] = -1
	}
	for i, rel := range relations {
		s.lineage[i] = make([]int, len(rel.Keys))
		for k := range s.lineage[i] {
			s.lineage[i][k] = -1
		}
	}
	for pos, name := range fields {
		var rel, key, g int
		switch {
		case !strings.HasPrefix(name, hiddenPrefix):
			s.visible = append(s.visible, pos)
			s.names = append(s.names, name)
		case scan(name, hiddenPrefix+"l%d_%d", &rel, &key) && rel < len(relations) && key < len(relations[rel].Keys):
			s.lineage[rel][key] = pos
		case scan(name, hiddenPrefix+"g%d", &g) && g < groups:
			s.groups[g] = pos
		case scan(name, hiddenPrefix+"p%d", &g):
		default:
			return s, fmt.Errorf("livequery: unexpected hidden column %s", name)
		}
	}
	return s, nil
}

func scan(s, format string, args ...any) bool {
	n, err := fmt.Sscanf(s, format, args...)
	return err == nil && n == len(args)
}

func pick(values []*string, positions []int) []*string {
	out := make([]*string, len(positions))
	for i, p := range positions {
		out[i] = values[p]
	}
	return out
}

func (s resultShape) lineageOf(values []*string) []string {
	out := make([]string, len(s.lineage))
	for i, positions := range s.lineage {
		parts := make([]string, len(positions))
		null := false
		for k, p := range positions {
			if values[p] == nil {
				null = true
				break
			}
			parts[k] = *values[p]
		}
		if !null {
			out[i] = strings.Join(parts, keySep)
		}
	}
	return out
}

// queryText runs sql and returns field names and text-format rows.
func queryText(ctx context.Context, tx pgx.Tx, sql string, args []any) ([]string, [][]*string, error) {
	rows, err := tx.Query(ctx, sql, append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var names []string
	for _, f := range rows.FieldDescriptions() {
		names = append(names, f.Name)
	}
	var out [][]*string
	for rows.Next() {
		raw := rows.RawValues()
		row := make([]*string, len(raw))
		for i, b := range raw {
			if b != nil {
				v := string(b)
				row[i] = &v
			}
		}
		out = append(out, row)
	}
	return names, out, rows.Err()
}

// keyArrays turns encoded keys into one text array per key column.
func keyArrays(keys map[string]struct{}, columns int) []any {
	arrays := make([][]string, columns)
	for key := range keys {
		parts := strings.Split(key, keySep)
		for k := 0; k < columns && k < len(parts); k++ {
			arrays[k] = append(arrays[k], parts[k])
		}
	}
	out := make([]any, columns)
	for k := range arrays {
		out[k] = arrays[k]
	}
	return out
}
