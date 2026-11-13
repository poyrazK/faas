package datapipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestDiscoveryFindsTablesBeyondDefaultPage(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	for i := 0; i < api.DataPipelineDiscoveryTablesMax+1; i++ {
		if _, err := pool.Exec(t.Context(), fmt.Sprintf("CREATE TABLE public.a_%03d(id integer PRIMARY KEY)", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `CREATE TABLE public.zz_orders(id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	e := &Engine{openSource: func(ctx context.Context, _ string) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, pool.Config().Copy())
	}}
	defaultCheck := e.Check(t.Context(), ConnectionCheck{}, "")
	if !defaultCheck.Truncated || slices.ContainsFunc(defaultCheck.Tables, func(table api.DataSourceTable) bool { return table.Name == "zz_orders" }) {
		t.Fatal("fixture did not exceed default discovery page")
	}
	for _, selection := range []api.DataConnectionCheck{{Search: "zz_"}, {RequestedTables: []string{"zz_orders"}}} {
		result := e.Check(t.Context(), ConnectionCheck{DataConnectionCheck: selection}, "")
		if len(result.Tables) != 1 || result.Tables[0].Name != "zz_orders" || !result.Tables[0].Supported || result.Truncated {
			t.Fatalf("selected table not discovered: %+v", result)
		}
	}
}

func TestDiscoverySelectionCoalescingAndWorkerFence(t *testing.T) {
	s, store, _, account := testService(t)
	c, err := s.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.RequestCheck(t.Context(), account, c.ID, c.Revision, "", api.RequestDataConnectionCheck{Search: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.RequestCheck(t.Context(), account, c.ID, c.Revision, "", api.RequestDataConnectionCheck{Search: "orders"})
	if err != nil || first.ID != same.ID {
		t.Fatal("same search replaced active check", err)
	}
	next, err := store.RequestCheck(t.Context(), account, c.ID, c.Revision, "", api.RequestDataConnectionCheck{Tables: []string{"zz_orders"}})
	if err != nil || next.ID == first.ID || !slices.Equal(next.RequestedTables, []string{"zz_orders"}) {
		t.Fatal("selection not saved", next, err)
	}
	if err = store.FinishCheck(t.Context(), old, api.DataConnectionCheck{State: "passed"}); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced worker published", err)
	}
	job, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishCheck(t.Context(), job, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "zz_orders", Supported: true}}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Check(t.Context(), account, c.ID)
	if err != nil || !slices.Equal(result.RequestedTables, job.RequestedTables) || checkSelection(result.DataConnectionCheck, []string{"zz_orders"}) != nil {
		t.Fatal("selection evidence was lost", err)
	}
	for _, request := range []api.RequestDataConnectionCheck{{Search: "%"}, {Search: "orders", Tables: []string{"orders"}}, {Tables: []string{"orders", "orders"}}, {Tables: []string{"orders;drop"}}} {
		if _, err = s.RequestCheck(t.Context(), account, c.ID, api.RequestDataConnectionCheck{ExpectedRevision: c.Revision, Search: request.Search, Tables: request.Tables}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid discovery admitted", request, err)
		}
	}
}
