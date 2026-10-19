package datapipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func schemaTestRelations() []relation {
	return []relation{{Name: "orders", OID: 10, Columns: []column{{Name: "id", Type: "int8", Key: true, NotNull: true, Modifier: -1, Ordinal: 1}}}}
}
func cloneSchema(r []relation) []relation {
	b, _ := json.Marshal(r)
	var result []relation
	_ = json.Unmarshal(b, &result)
	return result
}
func TestSchemaPlannerRequiresExactSafeAdditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]relation) []relation
		safe   bool
	}{
		{"nullable", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "note", Type: "text", Modifier: -1, Ordinal: 2})
			return r
		}, true},
		{"default", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "note", Type: "text", Modifier: -1, Ordinal: 2, Default: true})
			return r
		}, true},
		{"historical missing default", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "note", Type: "text", Modifier: -1, Ordinal: 2, Missing: true})
			return r
		}, true},
		{"not null", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "note", Type: "text", Modifier: -1, Ordinal: 2, NotNull: true})
			return r
		}, true},
		{"identity", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "serial", Type: "int8", Modifier: -1, Ordinal: 2, Identity: true})
			return r
		}, false},
		{"added key", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "key", Type: "int8", Modifier: -1, Ordinal: 2, Key: true})
			return r
		}, false},
		{"unsupported", func(r []relation) []relation {
			r[0].Columns = append(r[0].Columns, column{Name: "note", Type: "_text", Modifier: -1, Ordinal: 2, Unsupported: true})
			return r
		}, false},
		{"type change", func(r []relation) []relation { r[0].Columns[0].Type = "text"; return r }, false},
		{"rename", func(r []relation) []relation { r[0].Columns[0].Name = "renamed"; return r }, false},
		{"table replacement", func(r []relation) []relation { r[0].OID++; return r }, false},
		{"column replacement", func(r []relation) []relation { r[0].Columns[0].Ordinal++; return r }, false},
		{"key change", func(r []relation) []relation { r[0].Columns[0].Key = false; return r }, false},
		{"drop", func(r []relation) []relation { r[0].Columns = nil; return r }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := schemaTestRelations()
			after := tc.mutate(cloneSchema(before))
			p := Pipeline{DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1}}
			plan := schemaPlan(p, before, after)
			if plan.Supported != tc.safe || len(plan.Changes) == 0 {
				t.Fatalf("classification %+v", plan)
			}
			if schemaPlan(p, before, after).ID != plan.ID {
				t.Fatal("unstable plan")
			}
			p.Generation++
			if schemaPlan(p, before, after).ID == plan.ID {
				t.Fatal("approval survived generation change")
			}
		})
	}
}
func TestSchemaLegacyHashRemainsStableAcrossMetadataUpgrade(t *testing.T) {
	baseline := `[{"Name":"orders","Columns":[{"Name":"id","Type":"int8","NotNull":true,"Key":true,"Modifier":-1}]}]`
	sum := sha256.Sum256([]byte(baseline))
	r := schemaTestRelations()
	r[0].Columns[0].Default = true
	if relationHash(r) != hex.EncodeToString(sum[:]) {
		t.Fatal("upgrading metadata changed the original structural receipt")
	}
}

func schemaPipelineFixture(t *testing.T) (*Service, *PostgresStore, Pipeline, SchemaChange) {
	t.Helper()
	service, store, account, c, request := setupFixture(t)
	database := uuid.NewString()
	finishSetupCheck(t, store, account, c, database)
	created, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: request.Name, SourceConnectionID: c.ID, DatabaseID: database, Tables: request.Tables, ReadSubjects: request.ReadSubjects})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil || p.ID != created.ID {
		t.Fatal(err)
	}
	before := schemaTestRelations()
	after := cloneSchema(before)
	after[0].Columns = append(after[0].Columns, column{Name: "note", Type: "text", Modifier: -1, Ordinal: 2})
	plan := schemaPlan(p, before, after)
	if err = store.Finish(t.Context(), p, Observation{State: "blocked", Reason: "schema_change_pending", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	p, err = store.Get(t.Context(), account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, p, plan
}
func TestSchemaApprovalIsOwnedRevisionBoundAndFenced(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	for _, tc := range []struct {
		account, id string
		revision    int64
	}{{uuid.NewString(), plan.ID, p.Revision}, {p.AccountID, strings.Repeat("a", 64), p.Revision}, {p.AccountID, plan.ID, p.Revision + 1}} {
		if err := service.ApproveSchema(t.Context(), tc.account, p.ID, tc.revision, tc.id, false); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale/foreign approval accepted: %v", err)
		}
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApproveSchema(t.Context(), p.AccountID, p.ID, p.Revision, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	corrupt := plan
	corrupt.State = "complete"
	if err = store.Finish(t.Context(), stale, Observation{State: "live", SchemaChange: &corrupt}); !errors.Is(err, ErrConflict) {
		t.Fatal("pre-approval worker published completion", err)
	}
	current, err := store.Get(t.Context(), p.AccountID, p.ID)
	if err != nil || current.SchemaApproval != plan.ID || current.SchemaChange.State != "pending" {
		t.Fatal("approval was overwritten", err)
	}
	reset, err := store.Control(t.Context(), p.AccountID, p.ID, current.Revision, "resync")
	if err != nil || reset.SchemaApproval != "" || reset.SchemaChange != nil {
		t.Fatal("approval survived resync", err)
	}
}

func TestSchemaWithdrawalClearsApproval(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	if err := service.ApproveSchema(ctx, p.AccountID, p.ID, p.Revision, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	plan.State = "withdrawn"
	if err = store.Finish(ctx, leased, Observation{State: "live", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || saved.SchemaApproval != "" || saved.SchemaChange.State != "withdrawn" {
		t.Fatal("withdrawn definition retained approval", err)
	}
}
