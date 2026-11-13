//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunRegressionReplayGapAndQueueCursor(t *testing.T) {
	for _, driver := range []string{"mem", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if driver == "postgres" {
				pg, _, _ := pgStoreWithPool(t)
				store = pg
			}
			ctx := context.Background()
			var accounts []state.Account
			var executions []state.Execution
			req := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}
			resolved, problem := req.Resolve(api.PlanPro)
			if problem != nil {
				t.Fatal(problem)
			}
			for i := 0; i < 3; i++ {
				a, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				accounts = append(accounts, a)
				now := time.Now().UTC()
				e, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: a.ID, Request: resolved, SourceBytes: len(req.Source),
					AdmittedAt: now, DeadlineAt: now.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond), SealedPayload: []byte("sealed"), PayloadKID: "test"})
				if err != nil {
					t.Fatal(err)
				}
				executions = append(executions, e)
			}
			sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
			cursorStore := store.(state.ExecutionQueueCursorStore)
			page, err := cursorStore.ListExecutionQueueAccountsAfter(ctx, time.Now(), accounts[0].ID, 1)
			if err != nil || len(page) != 1 || page[0].AccountID != accounts[1].ID {
				t.Fatalf("cursor page=%v err=%v", page, err)
			}
			events := store.(state.ExecutionEventStore)
			row := executions[0]
			for i := 0; i < 1005; i++ {
				if _, err := events.AppendExecutionEvent(ctx, row.AccountID, row.ID, state.ExecutionEventStdout, json.RawMessage(`{"chunk":"x"}`), time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			first, err := events.ListExecutionEvents(ctx, row.AccountID, row.ID, 0, 1)
			if err != nil || len(first) != 1 || first[0].Type != state.ExecutionEventGap {
				t.Fatalf("missing bounded replay gap: %v %v", first, err)
			}
			var gap struct {
				Through int64 `json:"through_sequence"`
			}
			if err := json.Unmarshal(first[0].Payload, &gap); err != nil || gap.Through != first[0].Sequence {
				t.Fatalf("gap payload=%s err=%v", first[0].Payload, err)
			}
			next, err := events.ListExecutionEvents(ctx, row.AccountID, row.ID, gap.Through, 2)
			if err != nil || len(next) != 2 || next[0].Type != state.ExecutionEventStdout || next[0].Sequence <= gap.Through {
				t.Fatalf("resume after gap=%v %v", next, err)
			}
			for _, account := range accounts {
				if account.ID != row.AccountID {
					other, _ := events.ListExecutionEvents(ctx, account.ID, row.ID, 0, 100)
					if len(other) != 0 {
						t.Fatal("gap or output crossed accounts")
					}
					break
				}
			}
		})
	}
}
