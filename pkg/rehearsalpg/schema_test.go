// adr: 20261020180000
package rehearsalpg

import (
	"strings"
	"testing"
)

func schemaEvidence(objects ...SchemaObject) *Evidence {
	spec := Spec{Version: Version, FixtureSHA256: strings.Repeat("a", 64), Tables: []Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 10}
	before := []Snapshot{{Table: "public.imports", Rows: nil}}
	b, _ := CanonicalSnapshots(before)
	return &Evidence{Receipt: Receipt{Version: Version, RunID: strings.Repeat("b", 32), AppID: "app", ResourceID: "resource", FixtureSHA256: spec.FixtureSHA256,
		ContractSHA256: spec.ContractSHA256(), InitialSHA256: Hash(b)}, Status: "complete", Provenance: Provenance, DeploymentID: "dep", Spec: spec,
		Before: b, After: b, Changes: Changes(b, b), LockSamples: 1, LockBudgetPassed: true, CleanupConfirmed: true,
		SchemaInventoryVersion: SchemaInventoryVersion, Schema: objects}
}

func rows(n int64) *int64 { return &n }

func TestSchemaInventoryComparesMigrationsAndUndeclaredData(t *testing.T) {
	base := []SchemaObject{
		{Kind: "index", Name: "public.imports_pkey", Definition: "CREATE UNIQUE INDEX imports_pkey ON public.imports USING btree (item)"},
		{Kind: "table", Name: "public.imports", Definition: "item text not null"},
		{Kind: "table", Name: "public.schema_migrations", Definition: "version bigint not null", Rows: rows(3)},
	}
	clone := func() []SchemaObject { return append([]SchemaObject(nil), base...) }
	for _, tc := range []struct {
		name, status string
		after        func() []SchemaObject
		changes      int
	}{
		{"identical schemas", "passed", clone, 0},
		{"migration adds a table", "failed", func() []SchemaObject {
			s := clone()
			return append(s, SchemaObject{Kind: "table", Name: "public.users", Definition: "id uuid not null", Rows: rows(0)})
		}, 1},
		{"migration adds a column", "failed", func() []SchemaObject {
			s := clone()
			s[1].Definition = "item text not null, priority integer"
			return s
		}, 1},
		{"migration drops an index", "failed", func() []SchemaObject { return clone()[1:] }, 1},
		{"undeclared table gets more rows", "failed", func() []SchemaObject {
			s := clone()
			s[2].Rows = rows(4)
			return s
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := schemaEvidence(base...), schemaEvidence(tc.after()...)
			if err := after.Validate(); err != nil {
				t.Fatalf("valid evidence rejected: %v", err)
			}
			got := Compare(before, after)
			if got.Status != tc.status || len(got.SchemaChanges) != tc.changes {
				t.Fatalf("comparison = %+v, want %s with %d schema changes", got, tc.status, tc.changes)
			}
		})
	}
}

func TestSchemaInventoryNeverPassesAmbiguousEvidence(t *testing.T) {
	ok := []SchemaObject{{Kind: "table", Name: "public.imports", Definition: "item text"}}
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"unknown kind", func(e *Evidence) { e.Schema = []SchemaObject{{Kind: "extension", Name: "x"}} }},
		{"unsorted", func(e *Evidence) {
			e.Schema = []SchemaObject{{Kind: "table", Name: "public.b"}, {Kind: "table", Name: "public.a"}}
		}},
		{"duplicate", func(e *Evidence) { e.Schema = append(append([]SchemaObject{}, ok...), ok...) }},
		{"rows on a non-table", func(e *Evidence) { e.Schema = []SchemaObject{{Kind: "view", Name: "public.v", Rows: rows(1)}} }},
		{"oversized definition", func(e *Evidence) {
			e.Schema = []SchemaObject{{Kind: "table", Name: "public.t", Definition: strings.Repeat("x", MaxSchemaDefinitionBytes+100)}}
		}},
		{"inventory without version", func(e *Evidence) { e.SchemaInventoryVersion = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := schemaEvidence(ok...)
			tc.mutate(e)
			if e.Validate() == nil {
				t.Fatal("ambiguous schema inventory validated")
			}
		})
	}
	older := schemaEvidence()
	older.SchemaInventoryVersion, older.Schema = 0, nil
	if got := Compare(older, schemaEvidence(ok...)); got.Status != "inconclusive" {
		t.Fatalf("mixed inventory versions = %+v, want inconclusive", got)
	}
	if got := SchemaDefinition(strings.Repeat("y", MaxSchemaDefinitionBytes+1)); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("long definition kept verbatim: %q", got)
	}
}
