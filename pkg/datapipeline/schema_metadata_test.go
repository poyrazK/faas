package datapipeline

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestSchemaMetadataMaterializationPreservesSavedApprovals(t *testing.T) {
	p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1}}
	before := schemaTestRelations()
	after := cloneSchema(before)
	after[0].Columns = append(after[0].Columns, column{Name: "status", Type: "text", Modifier: -1, Ordinal: 2, Default: true, Missing: true, NotNull: true})
	plan := schemaPlan(p, before, after)
	p.SchemaChange, p.SchemaApproval = &plan, plan.ID
	materialized := cloneSchema(after)
	materialized[0].Columns[1].Missing = false
	if next := schemaPlan(p, before, materialized); !next.Supported || next.ID != plan.ID {
		t.Fatal("physical materialization changed an approved addition", next)
	}
	columns, err := approvedBackfillColumns(p, preserveMissingMetadata(materialized, after)[0])
	if err != nil || len(columns) != 1 || columns[0].Name != "status" {
		t.Fatal("materialized fast default lost backfill authority", columns, err)
	}
	p.SchemaChange, p.SchemaApproval = nil, ""
	if next := schemaPlan(p, after, materialized); !next.Supported || len(next.Changes) != 0 {
		t.Fatal("physical materialization became logical schema drift", next)
	}
	if !sameVerificationRelation(materialized[0], after[0]) || materialized[0].Columns[1].Missing || !after[0].Columns[1].Missing {
		t.Fatal("comparison changed its inputs or rejected materialized values")
	}
	added := relation{Name: "payments", OID: 20, Columns: []column{{Name: "id", Type: "int8", Key: true, Modifier: -1}}}
	expected := tablePlan(p, after, append(cloneSchema(after), added))
	actual := tablePlan(p, after, append(cloneSchema(materialized), added))
	if !actual.Supported || actual.ID != expected.ID {
		t.Fatal("materialization changed table onboarding approval", actual)
	}
}

func TestSchemaMetadataMaterializationDoesNotMaskLogicalChanges(t *testing.T) {
	before := schemaTestRelations()
	before[0].Columns[0].Missing = true
	for _, test := range []struct {
		name   string
		change func(*relation)
	}{
		{"type", func(r *relation) { r.Columns[0].Type = "text" }},
		{"default", func(r *relation) { r.Columns[0].Default = true }},
		{"identity", func(r *relation) { r.Columns[0].Identity = true }},
		{"ordinal", func(r *relation) { r.Columns[0].Ordinal++ }},
		{"key", func(r *relation) { r.Columns[0].Key = false }},
		{"required", func(r *relation) { r.Columns[0].NotNull = false }},
		{"replacement", func(r *relation) { r.OID++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := cloneSchema(before)
			after[0].Columns[0].Missing = false
			test.change(&after[0])
			if next := schemaPlan(Pipeline{}, before, after); next.Supported || sameVerificationRelation(after[0], before[0]) {
				t.Fatal("logical drift hidden by physical materialization", next)
			}
		})
	}
}

func TestSchemaMetadataVacuumFullKeepsLogicalDefinition(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.materialized_default(id int PRIMARY KEY);INSERT INTO public.materialized_default VALUES(1);ALTER TABLE public.materialized_default ADD COLUMN value text DEFAULT 'same value'`); err != nil {
		t.Fatal(err)
	}
	before, table, err := inspectSourceTable(ctx, pool, "materialized_default")
	if err != nil || !table.Supported || !before.Columns[1].Missing {
		t.Fatal("fast default fixture unavailable", before, table, err)
	}
	if _, err = pool.Exec(ctx, `VACUUM FULL public.materialized_default`); err != nil {
		t.Fatal(err)
	}
	after, table, err := inspectSourceTable(ctx, pool, "materialized_default")
	if err != nil || !table.Supported || after.Columns[1].Missing {
		t.Fatal("fast default was not materialized", after, table, err)
	}
	if !reflect.DeepEqual(preserveMissingMetadata([]relation{after}, []relation{before}), []relation{before}) {
		t.Fatal("vacuum changed the logical definition", before, after)
	}
	var value string
	if err = pool.QueryRow(ctx, `SELECT value FROM public.materialized_default WHERE id=1`).Scan(&value); err != nil || value != "same value" {
		t.Fatal("materialized source value changed", value, err)
	}
}
