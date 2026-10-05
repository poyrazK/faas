package copycontents

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type name struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

func (n name) key() string { return n.Schema + "\x00" + n.Name }

type column struct {
	Name     string `json:"name"`
	Type     name   `json:"type"`
	Modifier int32  `json:"modifier"`
}
type typeShape struct {
	Name                   name   `json:"name"`
	Kind                   string `json:"kind"`
	Element, Base, Subtype name
	Columns                []column `json:"columns"`
	Labels                 []string `json:"labels"`
}
type relationShape struct {
	Name        name     `json:"name"`
	Kind        string   `json:"kind"`
	Persistence string   `json:"persistence"`
	Populated   bool     `json:"populated"`
	Columns     []column `json:"columns"`
}
type relationRead struct {
	OID   uint32
	Shape relationShape
}
type catalogue struct {
	relations    []relationRead
	types        []typeShape
	largeObjects []uint32
}

// The registry deliberately qualifies type output, not arbitrary casts to text.
// Composite/domain/array/range output recursively uses these same type functions.
// An extension or custom base output requires its own reviewed strategy.
var builtinOutputs = map[string]string{
	"bool": "boolout", "bytea": "byteaout", "char": "charout", "name": "nameout",
	"int2": "int2out", "int4": "int4out", "int8": "int8out", "text": "textout",
	"oid": "oidout", "float4": "float4out", "float8": "float8out", "numeric": "numeric_out",
	"bpchar": "bpcharout", "varchar": "varcharout", "date": "date_out", "time": "time_out",
	"timetz": "timetz_out", "timestamp": "timestamp_out", "timestamptz": "timestamptz_out", "interval": "interval_out",
	"uuid": "uuid_out", "json": "json_out", "jsonb": "jsonb_out", "xml": "xml_out",
	"bit": "bit_out", "varbit": "varbit_out", "inet": "inet_out", "cidr": "cidr_out",
	"macaddr": "macaddr_out", "macaddr8": "macaddr8_out", "money": "cash_out",
	"point": "point_out", "line": "line_out", "lseg": "lseg_out", "box": "box_out", "path": "path_out", "polygon": "poly_out", "circle": "circle_out",
	"tsvector": "tsvectorout", "tsquery": "tsqueryout", "pg_lsn": "pg_lsn_out",
}

func readCatalogue(ctx context.Context, tx pgx.Tx, q *sqlc.Queries) (catalogue, error) {
	var result catalogue
	relations, e := q.CopyContentsRelations(ctx, tx, api.PostgresCopyContentsRelationsMax+1)
	if e != nil {
		return result, classify(ctx, e)
	}
	if len(relations) > api.PostgresCopyContentsRelationsMax {
		return result, pgerrors.ErrQuotaExceeded
	}
	types, e := q.CopyContentsTypes(ctx, tx, api.PostgresCopyContentsTypesMax+1)
	if e != nil {
		return result, classify(ctx, e)
	}
	if len(types) > api.PostgresCopyContentsTypesMax {
		return result, pgerrors.ErrQuotaExceeded
	}
	attrs, e := q.CopyContentsColumns(ctx, tx, api.PostgresCopyContentsColumnsMax+1)
	if e != nil {
		return result, classify(ctx, e)
	}
	if len(attrs) > api.PostgresCopyContentsColumnsMax {
		return result, pgerrors.ErrQuotaExceeded
	}
	objects, e := q.CopyContentsLargeObjects(ctx, tx, api.PostgresCopyContentsLargeObjectsMax+1)
	if e != nil {
		return result, classify(ctx, e)
	}
	if len(objects) > api.PostgresCopyContentsLargeObjectsMax {
		return result, pgerrors.ErrQuotaExceeded
	}
	byOID := map[uint32]sqlc.CopyContentsTypesRow{}
	columns := map[uint32][]sqlc.CopyContentsColumnsRow{}
	seen := map[uint32]bool{}
	for _, t := range types {
		if !t.Oid.Valid || t.Oid.Uint32 == 0 {
			return result, pgerrors.ErrConflict
		}
		byOID[t.Oid.Uint32] = t
	}
	for _, a := range attrs {
		columns[a.Attrelid.Uint32] = append(columns[a.Attrelid.Uint32], a)
	}
	typeName := func(id uint32) name { t := byOID[id]; return name{t.SchemaName, t.TypeName} }
	var qualify func(uint32, int) error
	qualify = func(id uint32, depth int) error {
		if depth > api.PostgresCopyContentsTypeDepthMax {
			return pgerrors.ErrQuotaExceeded
		}
		t, ok := byOID[id]
		if !ok || !validName(typeName(id)) || t.OutputSchema != "pg_catalog" || !t.OutputOid.Valid || t.OutputOid.Uint32 == 0 || t.OutputOid.Uint32 >= 16384 {
			return unsupported("type_output", typeName(id))
		}
		if seen[id] {
			return nil
		}
		seen[id] = true
		var deps []uint32
		switch {
		case t.Kind == "d":
			deps = append(deps, t.Base.Uint32)
		case t.Kind == "e":
			if t.OutputName != "enum_out" {
				return unsupported("type_output", typeName(id))
			}
		case t.Kind == "c":
			if t.OutputName != "record_out" || !t.Relation.Valid || t.Relation.Uint32 == 0 {
				return unsupported("type_output", typeName(id))
			}
			for _, a := range columns[t.Relation.Uint32] {
				deps = append(deps, a.Atttypid.Uint32)
			}
		case t.Category == "A":
			if t.OutputName != "array_out" || !t.Element.Valid || t.Element.Uint32 == 0 {
				return unsupported("type_output", typeName(id))
			}
			deps = append(deps, t.Element.Uint32)
		case t.Kind == "r" || t.Kind == "m":
			expected := "range_out"
			if t.Kind == "m" {
				expected = "multirange_out"
			}
			if t.OutputName != expected || t.RangeSubtype.Uint32 == 0 {
				return unsupported("type_output", typeName(id))
			}
			deps = append(deps, t.RangeSubtype.Uint32)
		case t.Kind == "b":
			if t.SchemaName != "pg_catalog" || id >= 16384 || builtinOutputs[t.TypeName] == "" || builtinOutputs[t.TypeName] != t.OutputName {
				return unsupported("type_output", typeName(id))
			}
		default:
			return unsupported("type_output", typeName(id))
		}
		for _, dep := range deps {
			if e := qualify(dep, depth+1); e != nil {
				return e
			}
		}
		// A domain must use its qualified base's output, not arbitrary custom code.
		if t.Kind == "d" && t.OutputOid.Uint32 != byOID[t.Base.Uint32].OutputOid.Uint32 {
			return unsupported("type_output", typeName(id))
		}
		return nil
	}
	shapeColumns := func(attrs []sqlc.CopyContentsColumnsRow) ([]column, error) {
		out := make([]column, 0, len(attrs))
		for _, a := range attrs {
			if e := qualify(a.Atttypid.Uint32, 0); e != nil {
				return nil, e
			}
			out = append(out, column{a.ColumnName, typeName(a.Atttypid.Uint32), a.Atttypmod})
		}
		return out, nil
	}
	for _, r := range relations {
		if !r.Oid.Valid || r.Oid.Uint32 == 0 || !validName(name{r.SchemaName, r.RelationName}) {
			return result, pgerrors.ErrConflict
		}
		if r.Kind == "f" {
			return result, unsupported("foreign_table", name{r.SchemaName, r.RelationName})
		}
		if r.Persistence == "t" {
			return result, unsupported("temporary_relation", name{r.SchemaName, r.RelationName})
		}
		cs, e := shapeColumns(columns[r.Oid.Uint32])
		if e != nil {
			return result, e
		}
		if r.Kind == "S" {
			cs = []column{{"last_value", name{"pg_catalog", "int8"}, -1}, {"is_called", name{"pg_catalog", "bool"}, -1}}
		}
		result.relations = append(result.relations, relationRead{r.Oid.Uint32, relationShape{name{r.SchemaName, r.RelationName}, r.Kind, r.Persistence, r.Populated, cs}})
	}
	for id := range seen {
		t := byOID[id]
		cs := []column{}
		if t.Kind == "c" {
			var e error
			cs, e = shapeColumns(columns[t.Relation.Uint32])
			if e != nil {
				return result, e
			}
		}
		labels := []string{}
		if json.Unmarshal(t.Labels, &labels) != nil {
			return result, pgerrors.ErrConflict
		}
		result.types = append(result.types, typeShape{typeName(id), t.Kind, typeName(t.Element.Uint32), typeName(t.Base.Uint32), typeName(t.RangeSubtype.Uint32), cs, labels})
	}
	slices.SortFunc(result.types, func(a, b typeShape) int { return strings.Compare(a.Name.key(), b.Name.key()) })
	for _, o := range objects {
		if !o.Valid || o.Uint32 == 0 {
			return result, pgerrors.ErrConflict
		}
		result.largeObjects = append(result.largeObjects, o.Uint32)
	}
	// Bound the private logical catalogue before reading any row data.
	raw, e := json.Marshal(struct {
		Relations []relationRead
		Types     []typeShape
		Objects   []uint32
	}{result.relations, result.types, result.largeObjects})
	if e != nil {
		return result, pgerrors.ErrUnavailable
	}
	if len(raw) > api.PostgresCopyInventoryMaxBytes {
		return result, pgerrors.ErrQuotaExceeded
	}
	return result, nil
}
func validName(n name) bool {
	return n.Schema != "" && n.Name != "" && !strings.ContainsRune(n.Schema, 0) && !strings.ContainsRune(n.Name, 0)
}
