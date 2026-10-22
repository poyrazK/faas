package datapipeline

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTableAdditionSelectionAndApprovalAreOwnedFreshAndFenced(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := t.Context()
	s.CapacityStatus = func(context.Context, string, string) (*api.DataPipelineCapacity, error) {
		return &api.DataPipelineCapacity{State: "ready"}, nil
	}
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@source.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	created, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live", Initialized: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.RequestTables(ctx, account, created.ID, created.Revision, []string{"customers"}); !errors.Is(err, ErrCheckRequired) {
		t.Fatal("undiscovered table accepted", err)
	}
	if _, err = store.RequestCheck(ctx, account, c.ID, c.Revision, ""); err != nil {
		t.Fatal(err)
	}
	// Replace the very recent completed check through its normal fenced finish.
	if _, err = store.pool.Exec(ctx, `UPDATE data_connection_checks SET state='pending' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimCheck(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCheck(ctx, job, api.DataConnectionCheck{State: "blocked", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}, {Name: "customers", Supported: true}}, Checks: []api.DataConnectionIssue{{Code: "destination_not_empty", State: "blocked"}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		account string
		rev     int64
		tables  []string
	}{{uuid.NewString(), 1, []string{"customers"}}, {account, 2, []string{"customers"}}, {account, 1, []string{"orders"}}, {account, 1, []string{"customers", "customers"}}} {
		if err = s.RequestTables(ctx, tc.account, created.ID, tc.rev, tc.tables); err == nil {
			t.Fatal("foreign, stale or duplicate selection accepted")
		}
	}
	if err = s.RequestTables(ctx, account, created.ID, 1, []string{"customers"}); err != nil {
		t.Fatal(err)
	}
	p, err := store.Get(ctx, account, created.ID)
	if err != nil || !slices.Equal(p.Tables, []string{"orders"}) || p.SchemaChange.State != "requested" {
		t.Fatal("selection changed replication before approval", err)
	}
	before := schemaTestRelations()
	after := cloneSchema(before)
	newTable := relation{Name: "customers", OID: 30, Columns: before[0].Columns}
	after = append([]relation{newTable}, after...)
	plan := tablePlan(p, before, after)
	if !plan.Supported || !slices.Equal(plan.AddedTables, []string{"customers"}) {
		t.Fatal("additive plan rejected", plan)
	}
	leased, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live", Reason: "table_addition_pending", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	p, _ = store.Get(ctx, account, created.ID)
	if err = s.ApproveSchema(ctx, account, p.ID, p.Revision, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	approved, _ := store.Get(ctx, account, p.ID)
	if !slices.Equal(approved.Tables, []string{"customers", "orders"}) || approved.Generation != created.Generation {
		t.Fatal("approval failed to preserve the generation", approved)
	}
	if err = s.CancelTables(ctx, account, p.ID, approved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("approved selection was discarded", err)
	}
	if err = store.Finish(ctx, leased, Observation{State: "live"}); !errors.Is(err, ErrConflict) {
		t.Fatal("preapproval worker overwrote selection", err)
	}
	for _, mutate := range []func([]relation){func(r []relation) { r[1].OID++ }, func(r []relation) { r[1].Columns[0].Type = "text" }, func(r []relation) { r[0].Unsupported = true }} {
		copy := cloneSchema(after)
		mutate(copy)
		if tablePlan(p, before, copy).Supported {
			t.Fatal("existing drift or unsupported table accepted")
		}
	}
}
