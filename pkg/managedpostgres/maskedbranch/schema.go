package maskedbranch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type column struct {
	name, action, sqlType string
	number                int16
	identity              string
	notNull               bool
}

type table struct {
	name        string
	oid         uint32
	columns     []column
	byNumber    map[int16]column
	constraints []constraint
}

type constraint struct {
	name, kind                                                       string
	columns, references                                              []int16
	refSchema, refTable                                              string
	match, onUpdate, onDelete                                        string
	validated, deferrable, deferred, nullsNotDistinct, partialDelete bool
}

func publicTable(name string) pgx.Identifier { return pgx.Identifier{"public", name} }

func readTables(ctx context.Context, tx pgx.Tx, policy []api.ManagedPostgresMaskedTable) ([]table, error) {
	tables := make([]table, 0, len(policy))
	for _, p := range policy {
		t := table{name: p.Name, byNumber: make(map[int16]column)}
		if err := tx.QueryRow(ctx, `SELECT c.oid FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relname = $1 AND c.relkind = 'r' AND c.relpersistence = 'p'
				AND NOT c.relrowsecurity AND NOT c.relispartition AND NOT c.relhassubclass
				AND NOT EXISTS (SELECT FROM pg_catalog.pg_inherits i WHERE i.inhrelid = c.oid)
				AND NOT EXISTS (SELECT FROM pg_catalog.pg_trigger g WHERE g.tgrelid = c.oid AND NOT g.tgisinternal)
				AND NOT EXISTS (SELECT FROM pg_catalog.pg_index i WHERE i.indrelid = c.oid AND i.indisunique
					AND NOT EXISTS (SELECT FROM pg_catalog.pg_constraint k WHERE k.conindid = i.indexrelid AND k.conrelid = c.oid AND k.contype IN ('u','p')))
		`, p.Name).Scan(&t.oid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, unsupported(t.name, "select a regular table without inheritance, triggers, row security or standalone unique indexes")
			}
			return nil, unavailable(ctx, "read source table")
		}
		if _, err := tx.Exec(ctx, "LOCK TABLE ONLY "+publicTable(t.name).Sanitize()+" IN ACCESS SHARE MODE"); err != nil {
			return nil, unavailable(ctx, "lock source table")
		}
		if err := readColumns(ctx, tx, &t, p); err != nil {
			return nil, err
		}
		if err := readConstraints(ctx, tx, &t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, nil
}

func readColumns(ctx context.Context, tx pgx.Tx, t *table, p api.ManagedPostgresMaskedTable) error {
	actions := make(map[string]string, len(p.Columns))
	for _, c := range p.Columns {
		actions[c.Name] = c.Action
	}
	rows, err := tx.Query(ctx, `SELECT a.attnum, a.attname, typ.typname, n.nspname, a.atttypmod, a.attnotnull,
		a.attidentity::text, a.attgenerated::text, a.atthasdef, a.attcollation = typ.typcollation,
		COALESCE(seq.seqincrement = 1 AND seq.seqmin = 1 AND seq.seqstart = 1 AND NOT seq.seqcycle, false),
		COALESCE(pg_catalog.pg_get_expr(def.adbin, def.adrelid) = pg_catalog.format('nextval(%L::regclass)', seq.seqrelid::regclass::text), false)
		FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type typ ON typ.oid = a.atttypid
		JOIN pg_catalog.pg_namespace n ON n.oid = typ.typnamespace
		LEFT JOIN pg_catalog.pg_attrdef def ON def.adrelid = a.attrelid AND def.adnum = a.attnum
		LEFT JOIN pg_catalog.pg_sequence seq ON seq.seqrelid = pg_catalog.pg_get_serial_sequence($2, a.attname)::regclass
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, t.oid, publicTable(t.name).Sanitize())
	if err != nil {
		return unavailable(ctx, "read source columns")
	}
	defer rows.Close()
	for rows.Next() {
		var c column
		var typ, namespace, generated string
		var modifier int32
		var hasDefault, defaultCollation, standardSequence, serial bool
		if err := rows.Scan(&c.number, &c.name, &typ, &namespace, &modifier, &c.notNull, &c.identity, &generated,
			&hasDefault, &defaultCollation, &standardSequence, &serial); err != nil {
			return unavailable(ctx, "read source columns")
		}
		c.action = actions[c.name]
		if c.action == "" {
			continue
		}
		c.sqlType = columnSQLType(typ, modifier)
		if namespace != "pg_catalog" || c.sqlType == "" || !defaultCollation || generated != "" || hasDefault && !serial {
			return unsupported(t.name+"."+c.name, "column has an unsupported type, collation, default or generated expression")
		}
		if serial {
			c.identity = "d"
		}
		if c.identity != "" && (!standardSequence || c.action != "keep") {
			return unsupported(t.name+"."+c.name, "use keep for identity and serial columns with standard sequence settings")
		}
		if c.action == "null" && c.notNull || (c.action == "email" || c.action == "token") && !maskTextType(typ, modifier) {
			return unsupported(t.name+"."+c.name, "masking action is incompatible with the column type or nullability")
		}
		t.columns = append(t.columns, c)
		t.byNumber[c.number] = c
	}
	if err := rows.Err(); err != nil {
		return unavailable(ctx, "read source columns")
	}
	if len(t.columns) != len(p.Columns) {
		return unsupported(t.name, "the policy names a missing column")
	}
	return nil
}

func columnSQLType(typ string, modifier int32) string {
	switch typ {
	case "bool", "int2", "int4", "int8", "text", "uuid", "bytea", "date", "float4", "float8":
		if modifier == -1 {
			return "pg_catalog." + typ
		}
	case "varchar", "bpchar":
		if modifier == -1 {
			return "pg_catalog." + typ
		}
		if modifier > 4 {
			return "pg_catalog." + typ + "(" + strconv.Itoa(int(modifier-4)) + ")"
		}
	case "timestamp", "timestamptz", "time", "timetz":
		if modifier == -1 {
			return "pg_catalog." + typ
		}
		if modifier >= 0 && modifier <= 6 {
			return "pg_catalog." + typ + "(" + strconv.Itoa(int(modifier)) + ")"
		}
	case "numeric":
		if modifier == -1 {
			return "pg_catalog.numeric"
		}
		if modifier >= 4 {
			precision := int((modifier - 4) >> 16)
			scale := int((modifier - 4) & 2047)
			if scale >= 1024 {
				scale -= 2048
			}
			return fmt.Sprintf("pg_catalog.numeric(%d,%d)", precision, scale)
		}
	}
	return ""
}

func maskTextType(typ string, modifier int32) bool {
	return typ == "text" || typ == "varchar" && (modifier == -1 || modifier >= 49)
}

func readConstraints(ctx context.Context, tx pgx.Tx, t *table) error {
	rows, err := tx.Query(ctx, `SELECT k.conname, k.contype::text, k.conkey, k.confkey,
		COALESCE(n.nspname, ''), COALESCE(r.relname, ''), k.confmatchtype::text, k.confupdtype::text, k.confdeltype::text,
		k.convalidated, k.condeferrable, k.condeferred, COALESCE(i.indnullsnotdistinct, false), k.confdelsetcols IS NOT NULL
		FROM pg_catalog.pg_constraint k LEFT JOIN pg_catalog.pg_class r ON r.oid = k.confrelid
		LEFT JOIN pg_catalog.pg_namespace n ON n.oid = r.relnamespace
		LEFT JOIN pg_catalog.pg_index i ON i.indexrelid = k.conindid
		WHERE k.conrelid = $1 AND k.contype <> 'n' ORDER BY k.contype, k.conname LIMIT 257`, t.oid)
	if err != nil {
		return unavailable(ctx, "read source constraints")
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		count++
		if count > MaxColumns {
			return unsupported(t.name, "table has too many constraints")
		}
		var c constraint
		if err := rows.Scan(&c.name, &c.kind, &c.columns, &c.references, &c.refSchema, &c.refTable,
			&c.match, &c.onUpdate, &c.onDelete, &c.validated, &c.deferrable, &c.deferred, &c.nullsNotDistinct, &c.partialDelete); err != nil {
			return unavailable(ctx, "read source constraints")
		}
		var selected int
		for _, number := range c.columns {
			if _, ok := t.byNumber[number]; ok {
				selected++
			}
		}
		if selected == 0 && len(c.columns) != 0 {
			continue
		}
		if selected == 0 || selected != len(c.columns) || !c.validated || !namePattern.MatchString(c.name) ||
			(c.kind != "p" && c.kind != "u" && c.kind != "f") || c.partialDelete {
			return unsupported(t.name, "select complete keys and use ordinary primary, unique and foreign-key constraints")
		}
		t.constraints = append(t.constraints, c)
	}
	if err := rows.Err(); err != nil {
		return unavailable(ctx, "read source constraints")
	}
	return nil
}

func tableDDL(t table) string {
	definitions := make([]string, 0, len(t.columns))
	for _, c := range t.columns {
		definition := pgx.Identifier{c.name}.Sanitize() + " " + c.sqlType
		switch c.identity {
		case "a":
			definition += " GENERATED ALWAYS AS IDENTITY"
		case "d":
			definition += " GENERATED BY DEFAULT AS IDENTITY"
		}
		if c.notNull {
			definition += " NOT NULL"
		}
		definitions = append(definitions, definition)
	}
	return "CREATE TABLE " + publicTable(t.name).Sanitize() + " (" + strings.Join(definitions, ", ") + ")"
}

func constraintDDL(t table, c constraint, tables map[string]table) (string, error) {
	columns := func(t table, numbers []int16) string {
		names := make([]string, 0, len(numbers))
		for _, number := range numbers {
			names = append(names, pgx.Identifier{t.byNumber[number].name}.Sanitize())
		}
		return "(" + strings.Join(names, ", ") + ")"
	}
	definition := "ALTER TABLE " + publicTable(t.name).Sanitize() + " ADD CONSTRAINT " + pgx.Identifier{c.name}.Sanitize() + " "
	switch c.kind {
	case "p":
		definition += "PRIMARY KEY " + columns(t, c.columns)
	case "u":
		definition += "UNIQUE "
		if c.nullsNotDistinct {
			definition += "NULLS NOT DISTINCT "
		}
		definition += columns(t, c.columns)
	case "f":
		parent, ok := tables[c.refTable]
		if !ok || c.refSchema != "public" || len(c.references) != len(c.columns) {
			return "", unsupported(t.name, "select the referenced table and every foreign-key column")
		}
		for i, number := range c.references {
			ref, ok := parent.byNumber[number]
			if !ok || ref.action != t.byNumber[c.columns[i]].action || ref.action == "null" {
				return "", unsupported(t.name, "apply the same non-null action to both sides of each foreign key")
			}
		}
		definition += "FOREIGN KEY " + columns(t, c.columns) + " REFERENCES " + publicTable(parent.name).Sanitize() + " " + columns(parent, c.references)
		switch c.match {
		case "s":
		case "f":
			definition += " MATCH FULL"
		default:
			return "", unsupported(t.name, "unsupported foreign-key match type")
		}
		update, undo := foreignAction(c.onUpdate), foreignAction(c.onDelete)
		if update == "" || undo == "" {
			return "", unsupported(t.name, "unsupported foreign-key action")
		}
		definition += " ON UPDATE " + update + " ON DELETE " + undo
	}
	if c.deferrable {
		definition += " DEFERRABLE"
		if c.deferred {
			definition += " INITIALLY DEFERRED"
		}
	}
	return definition, nil
}

func foreignAction(code string) string {
	switch code {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	}
	return ""
}

func unsupported(object, reason string) error {
	return fmt.Errorf("%w: %s: %s", pgerrors.ErrUnsupported, object, reason)
}
