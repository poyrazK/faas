// adr: 20261017233500
package state_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"testing"
	"time"
)

func TestSelectedTraceMemFiltersBeforeLimit(t *testing.T) {
	m := state.NewMemStore()
	ctx := t.Context()
	account, err := m.CreateAccount(ctx, "selected@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "selected", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	enqueue := func(app string) state.Invocation {
		inv, err := m.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app, Source: state.InvocationAsyncInvoke, DueAt: time.Now(), Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"` + trace + `","secret":"excluded"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	for i := 0; i < 25; i++ {
		enqueue(other.ID)
	}
	inv := enqueue(selected.ID)
	q := sqlc.ListSelectedTraceInvocationsParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true}, AppIds: []pgtype.UUID{{Bytes: uuid.MustParse(selected.ID), Valid: true}}, TraceID: trace, RowLimit: 1}
	rows, err := m.ListSelectedTraceInvocations(ctx, q)
	if err != nil || len(rows) != 1 || uuid.UUID(rows[0].ID.Bytes).String() != uuid.MustParse(inv.ID).String() {
		t.Fatal("unselected invocations consumed the selected row limit")
	}
	q.AccountID.Bytes = uuid.New()
	rows, err = m.ListSelectedTraceInvocations(ctx, q)
	if err != nil || len(rows) != 0 {
		t.Fatal("cross-account invocation leaked")
	}
	q.AppIds = nil
	if _, err = m.ListSelectedTraceInvocations(ctx, q); err == nil {
		t.Fatal("empty selection widened to account")
	}
}
