package datapipeline

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Authorization covers only newly added columns in this exact approved plan.
// Manual verification and completed migrations can never repair existing data.
func approvedBackfillColumns(p Pipeline, r relation) ([]column, error) {
	c := p.SchemaChange
	if len(CopiedTables(c)) > 0 {
		return nil, nil // Native initial copy owns all values in these tables.
	}
	if c == nil || c.State == "complete" || c.State == "withdrawn" || p.SchemaApproval != c.ID {
		return nil, nil
	}
	plan := schemaPlan(p, c.Before, c.After)
	if !c.Supported || !plan.Supported || plan.ID != c.ID {
		return nil, ErrConflict
	}
	var before, after *relation
	for i := range c.Before {
		if c.Before[i].Name == r.Name {
			before, after = &c.Before[i], &c.After[i]
			break
		}
	}
	if before == nil || !sameVerificationRelation(*after, r) {
		return nil, ErrConflict
	}
	old := map[string]bool{}
	for _, col := range before.Columns {
		old[col.Name] = true
	}
	columns := []column{}
	for _, col := range r.Columns {
		if !old[col.Name] {
			columns = append(columns, col)
		}
	}
	return columns, nil
}

func checkBackfillReceipt(ctx context.Context, p Pipeline, target receiptSQL, plan SchemaChange) error {
	encoded, _ := json.Marshal(plan.After)
	var valid bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.mirrors
 WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3 AND schema_spec=$4::jsonb
 AND schema_phase IN ('verifying','refreshing') AND NOT readable AND NOT retiring)`, p.ID, p.Generation, plan.ID, encoded).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

func backfillOwner(ctx context.Context, p Pipeline, tx pgx.Tx, r relation) (string, error) {
	var owner string
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT pg_get_userbyid(c.relowner),coalesce(obj_description(c.oid,'pg_class'),'')=$2
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, r.Name, marker(p)).Scan(&owner, &owned); err != nil {
		return "", err
	}
	if !owned {
		return "", ErrConflict
	}
	return owner, nil
}

// Source values are canonical JSON from the locked, caught-up batch. Populate
// typed records on the mirror, but assign only approved new columns. A missing
// target row or an old-column mismatch still fails whole-row verification.
func backfillVerificationRows(ctx context.Context, p Pipeline, tx pgx.Tx, r relation, columns []column, data []json.RawMessage) (int64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := checkBackfillReceipt(ctx, p, tx, *p.SchemaChange); err != nil {
		return 0, err
	}
	owner, err := backfillOwner(ctx, p, tx, r)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
		return 0, err
	}
	assignments, old, next, keys := []string{}, []string{}, []string{}, []string{}
	for _, col := range columns {
		name := ident(col.Name)
		assignments = append(assignments, name+"=s."+name)
		old = append(old, "d."+name)
		next = append(next, "s."+name)
	}
	for _, key := range primaryColumns(r) {
		keys = append(keys, collated("d."+ident(key.Name), key)+"="+collated("s."+ident(key.Name), key))
	}
	encoded, _ := json.Marshal(data)
	query := "UPDATE " + tableIdent(r.Name) + " d SET " + strings.Join(assignments, ",") +
		" FROM pg_catalog.jsonb_populate_recordset(NULL::" + tableIdent(r.Name) + ",$1::jsonb) s WHERE " + strings.Join(keys, " AND ") +
		" AND pg_catalog.to_jsonb(ROW(" + strings.Join(old, ",") + ")) IS DISTINCT FROM pg_catalog.to_jsonb(ROW(" + strings.Join(next, ",") + "))"
	tag, err := tx.Exec(ctx, query, encoded)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Required columns must be reflected in the API contract before runtimes are
// refreshed. DDL and the phase receipt commit together, making retries harmless.
func prepareSchemaRefresh(ctx context.Context, p Pipeline, target *pgx.Conn, plan SchemaChange, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = checkBackfillReceipt(ctx, p, tx, plan); err != nil {
		return err
	}
	var phase string
	if err = tx.QueryRow(ctx, `SELECT schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 FOR UPDATE`, p.ID, p.Generation).Scan(&phase); err != nil {
		return err
	}
	if phase == "refreshing" {
		return nil
	}
	for _, r := range plan.After {
		columns, err := approvedBackfillColumns(p, r)
		if err != nil {
			return err
		}
		clauses := []string{}
		for _, col := range columns {
			if col.NotNull {
				clauses = append(clauses, "ALTER COLUMN "+ident(col.Name)+" SET NOT NULL")
			}
		}
		if len(clauses) == 0 {
			continue
		}
		owner, err := backfillOwner(ctx, p, tx, r)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "ALTER TABLE "+tableIdent(r.Name)+" "+strings.Join(clauses, ",")); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_phase='refreshing' WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3`, p.ID, p.Generation, plan.ID); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
