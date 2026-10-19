//go:build !no_pg

// adr: 20261017233500
package state_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestSelectedTracePGFilteringBoundariesAndOrder(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "trace-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "trace-foreign-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	makeApp := func(acct, slug string) state.App {
		a, err := store.CreateApp(ctx, state.App{AccountID: acct, Slug: slug, Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	selected := makeApp(account.ID, "selected")
	other := makeApp(account.ID, "other")
	foreignApp := makeApp(foreign.ID, "foreign")
	deployments := map[string]string{}
	for _, a := range []state.App{selected, other, foreignApp} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: a.ID, Kind: "image", ImageDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Scope: "default", Status: "pending"})
		if err != nil {
			t.Fatal(err)
		}
		deployments[a.ID] = d.ID
	}
	trace := "4bf92f3577b34da6a3ce929d0e0e4736"
	start := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	end := start.Add(time.Hour)
	insert := func(id string, a state.App, at time.Time, traceID string) {
		_, err := pool.Exec(ctx, `INSERT INTO request_telemetry(id,account_id,app_id,deployment_id,route,method,status,latency_ms,count,received_at,trace_id) VALUES($1,$2,$3,$6,'/orders','GET',200,7,1,$4,$5)`, id, a.AccountID, a.ID, at, traceID, deployments[a.ID])
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		insert(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), other, start, trace)
	}
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"}
	for _, id := range ids {
		insert(id, selected, start, trace)
	}
	insert(uuid.NewString(), selected, start.Add(-time.Microsecond), trace)
	insert(uuid.NewString(), selected, end, trace)
	insert(uuid.NewString(), foreignApp, start, trace)
	insert(uuid.NewString(), selected, start, "ffffffffffffffffffffffffffffffff")
	q := sqlc.ListSelectedTraceRequestsParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(account.ID), Valid: true}, AppIds: []pgtype.UUID{{Bytes: uuid.MustParse(selected.ID), Valid: true}, {Bytes: uuid.MustParse(foreignApp.ID), Valid: true}}, TraceID: trace, ReceivedFrom: pgtype.Timestamptz{Time: start, Valid: true}, ReceivedUntil: pgtype.Timestamptz{Time: end, Valid: true}, RowLimit: 2}
	rows, err := store.ListSelectedTraceRequests(ctx, q)
	if err != nil || len(rows) != 2 {
		t.Fatalf("selected requests: %v %v", rows, err)
	}
	for i, row := range rows {
		if uuid.UUID(row.AppID.Bytes).String() != selected.ID || uuid.UUID(row.ID.Bytes).String() != ids[i] || !row.ReceivedAt.Time.Equal(start) {
			t.Fatal("SQL filtered after limit, crossed account/window or lost equal-time order")
		}
	}
	for _, a := range []state.App{other, other, selected, selected, foreignApp} {
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: a.AccountID, AppID: a.ID, Source: state.InvocationAsyncInvoke, DueAt: time.Now(), Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"` + trace + `","private":"excluded"}`)})
		if err != nil {
			t.Fatal(err)
		}
		created := start.Add(-30 * 24 * time.Hour)
		if a.ID != selected.ID {
			created = created.Add(-time.Hour)
		}
		if _, err = pool.Exec(ctx, `UPDATE invocations SET created_at=$2 WHERE id=$1`, inv.ID, created); err != nil {
			t.Fatal(err)
		}
	}
	invocations, err := store.ListSelectedTraceInvocations(ctx, sqlc.ListSelectedTraceInvocationsParams{AccountID: q.AccountID, AppIds: q.AppIds, TraceID: trace, RowLimit: 2})
	if err != nil || len(invocations) != 2 {
		t.Fatalf("selected invocations: %v %v", invocations, err)
	}
	for _, inv := range invocations {
		if uuid.UUID(inv.AppID.Bytes).String() != selected.ID || !inv.CreatedAt.Time.Before(start) {
			t.Fatal("durable invocation history widened selection or adopted request retention")
		}
	}
	if uuid.UUID(invocations[0].ID.Bytes).String() >= uuid.UUID(invocations[1].ID.Bytes).String() {
		t.Fatal("equal-time invocation order changed")
	}
	q.AppIds = nil
	if _, err := store.ListSelectedTraceRequests(ctx, q); err == nil {
		t.Fatal("empty SQL selection did not fail closed")
	}
}
