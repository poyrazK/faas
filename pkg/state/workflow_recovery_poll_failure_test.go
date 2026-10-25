package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1280
func TestWorkflowRecoveryAutomaticPollingIsolation(t *testing.T) {
	for _, mode := range []string{"automatic_initial", "automatic_retry"} {
		t.Run(mode, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				ctx := context.Background()
				seed := func() (state.App, string) {
					if mode == "automatic_initial" {
						_, app, r, _ := state.SeedInitialAutomatic(t, s)
						return app, r.ID
					}
					r, app, _, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
					return app, r.ID
				}
				bad, badReview := seed()
				good, goodReview := seed()
				policies := s.(state.WorkflowRecoveryPolicyStore)
				original, err := policies.GetWorkflowRecoveryPolicy(ctx, bad.AccountID, bad.ID)
				if err != nil {
					t.Fatal(err)
				}
				mutate := func(policy api.WorkflowRecoveryPolicy) {
					t.Helper()
					if m, ok := s.(*state.MemStore); ok {
						(*m.Mu()).Lock()
						(*m.RecoveryPolicies())[state.CanonicalMemUUID(bad.ID)].Response.Policy = policy
						(*m.Mu()).Unlock()
						return
					}
					raw, e := json.Marshal(policy)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET policy=$2::jsonb WHERE app_id=$1::uuid", bad.ID, raw); e != nil {
						t.Fatal(e)
					}
				}
				poisoned := original.Policy
				poisoned.HourlyExecutionBudget = 0
				mutate(poisoned)
				now := time.Now().UTC().Add(3 * time.Minute).Truncate(time.Microsecond)
				if m, ok := s.(*state.MemStore); ok {
					(*m.RecoveryPolicies())[state.CanonicalMemUUID(bad.ID)].NextExecute = now.Add(-time.Hour)
					(*m.RecoveryPolicies())[state.CanonicalMemUUID(good.ID)].NextExecute = now
				} else if _, err = (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET next_execute_at=CASE WHEN app_id=$1::uuid THEN $3::timestamptz ELSE $4::timestamptz END WHERE app_id IN ($1::uuid,$2::uuid)", bad.ID, good.ID, now.Add(-time.Hour), now); err != nil {
					t.Fatal(err)
				}
				get := func() api.WorkflowRecoveryPolicyResponse {
					t.Helper()
					p, e := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicy(ctx, bad.AccountID, bad.ID)
					if e != nil {
						t.Fatal(e)
					}
					return p
				}
				poll := func(at time.Time) bool {
					t.Helper()
					did, e := s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, at)
					if e != nil {
						t.Fatal(e)
					}
					return did
				}
				if !poll(now) {
					t.Fatal("bad policy not deferred")
				}
				failed := get()
				f := failed.AutomaticExecution.PollingFailure
				if f == nil || f.Code != "invalid_policy_authority" || f.Attempts != 1 || !f.RetryAt.Equal(now.Add(time.Minute)) || failed.Version != original.Version || !failed.UpdatedAt.Equal(original.UpdatedAt) || failed.AutomaticExecution.ConsecutiveFailures != 0 || failed.AutomaticExecution.PausedAt != nil {
					t.Fatalf("failure state %+v", failed)
				}
				if !poll(now) {
					t.Fatal("healthy policy starved")
				}
				if mode == "automatic_initial" {
					if r := state.InitialAutomaticReview(t, s, good, goodReview); r.JobID == "" {
						t.Fatal("healthy submission missing")
					}
					if r := state.InitialAutomaticReview(t, s, bad, badReview); r.Status != "prepared" || r.JobID != "" {
						t.Fatal("failed poll submitted job")
					}
				} else {
					r, e := s.(state.WorkflowRecoveryPolicyStore).GetWorkflowRecoveryPolicyReview(ctx, good.AccountID, good.ID, goodReview)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = state.AutomaticChild(t, s, good, r); e != nil {
						t.Fatal("healthy retry missing", e)
					}
				}
				if poll(now) {
					t.Fatal("failure retried before due")
				}
				if pg, ok := s.(*state.PgStore); ok {
					s = state.NewPgStore((*pg.Pool()).Pool)
				}
				if !reflect.DeepEqual(get().AutomaticExecution.PollingFailure, f) {
					t.Fatal("failure lost after restart")
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, e := s.(state.WorkflowRecoveryAutomaticPolicyStore).ProcessNextWorkflowRecoveryAutomaticPolicy(canceled, f.RetryAt); !errors.Is(e, context.Canceled) {
					t.Fatalf("cancellation %v", e)
				}
				for range 2 {
					poll(f.RetryAt)
				}
				second := get().AutomaticExecution.PollingFailure
				if second == nil || second.Attempts != 2 || !second.RetryAt.Equal(f.RetryAt.Add(2*time.Minute)) {
					t.Fatalf("backoff %+v", second)
				}
				mutate(original.Policy)
				for range 2 {
					poll(second.RetryAt)
				}
				if get().AutomaticExecution.PollingFailure != nil {
					t.Fatal("successful poll did not clear error")
				}
				// Policy replacement is the operator-authorized reset boundary.
				mutate(poisoned)
				for range 2 {
					poll(second.RetryAt.Add(time.Minute))
				}
				current := get()
				current, err = s.(state.WorkflowRecoveryPolicyStore).SetWorkflowRecoveryPolicy(ctx, bad.AccountID, bad.ID, api.SetWorkflowRecoveryPolicyRequest{ExpectedVersion: current.Version, Policy: original.Policy})
				if err != nil {
					t.Fatal(err)
				}
				if current.AutomaticExecution.PollingFailure != nil {
					t.Fatal("policy update retained old polling failure")
				}
			})
		})
	}
}

func TestWorkflowRecoveryAutomaticPollingClassification(t *testing.T) {
	for _, code := range []string{"08006", "40001", "57014", "53100", "XX000"} {
		if state.AutomaticPollingFailureCode(fmt.Errorf("poll: %w", &pgconn.PgError{Code: code})) != "" {
			t.Fatal("infrastructure deferred", code)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("unknown"), errors.Join((*state.ErrAutomaticPollingData), context.Canceled)} {
		if state.AutomaticPollingFailureCode(err) != "" {
			t.Fatal("unexpected deferral", err)
		}
	}
	f := state.NextAutomaticPollingFailure(nil, "invalid_polling_data", time.Now().UTC())
	for range 100 {
		f = state.NextAutomaticPollingFailure(f, "invalid_polling_data", f.RetryAt)
	}
	if f.Attempts != api.WorkflowAutomaticPollingMaxAttempts || f.RetryAt.Sub(f.FailedAt) != api.WorkflowAutomaticPollingMaxBackoff {
		t.Fatal("unbounded backoff", f)
	}
}

func TestWorkflowRecoveryAutomaticPollingRollback(t *testing.T) {
	for _, mode := range []string{"automatic_initial", "automatic_retry"} {
		t.Run(mode, func(t *testing.T) {
			state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
				pg, ok := s.(*state.PgStore)
				if !ok {
					t.Skip("PostgreSQL atomic job and budget regression")
				}
				ctx := context.Background()
				var app state.App
				var reviewID string
				if mode == "automatic_initial" {
					_, a, r, _ := state.SeedInitialAutomatic(t, s)
					app, reviewID = a, r.ID
				} else {
					r, a, _, _ := state.SeedAutomaticRecoveryReview(t, s, nil)
					app, reviewID = a, r.ID
				}
				count := func(query string) int {
					t.Helper()
					var n int
					if err := (*pg.Pool()).QueryRow(ctx, query, app.ID).Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n
				}
				jobsQuery := "SELECT count(*) FROM event_recovery_jobs WHERE app_id=$1::uuid"
				budgetQuery := "SELECT count(*) FROM (SELECT app_id FROM workflow_recovery_policy_execution_charges UNION ALL SELECT app_id FROM workflow_initial_recovery_reviews WHERE submitted_at IS NOT NULL AND review->'policy'->>'mode'='automatic_initial') charges WHERE app_id=$1::uuid"
				historyQuery := "SELECT count(*) FROM workflow_recovery_history_events WHERE app_id=$1::uuid"
				jobs, budget, history := count(jobsQuery), count(budgetQuery), count(historyQuery)
				if _, err := (*pg.Pool()).Exec(ctx, "UPDATE workflow_recovery_policies SET next_execute_at='epoch'::timestamptz WHERE app_id=$1::uuid", app.ID); err != nil {
					t.Fatal(err)
				}
				// Reject final advancement after native submission and accounting writes.
				if _, err := (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_recovery_policies ADD CONSTRAINT test_automatic_poll_advance CHECK (next_execute_at='epoch'::timestamptz OR automatic_poll_failure IS NOT NULL)"); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Add(3 * time.Minute).Truncate(time.Microsecond)
				if did, err := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, now); err != nil || !did {
					t.Fatalf("defer %v %v", did, err)
				}
				if count(jobsQuery) != jobs || count(budgetQuery) != budget || count(historyQuery) != history {
					t.Fatal("failed poll leaked jobs, accounting or history")
				}
				if mode == "automatic_initial" {
					r := state.InitialAutomaticReview(t, s, app, reviewID)
					if r.Status != "prepared" || r.JobID != "" || r.AutomaticEvaluation != nil {
						t.Fatal("failed poll leaked initial submission")
					}
				} else {
					r, err := pg.GetWorkflowRecoveryPolicyReview(ctx, app.AccountID, app.ID, reviewID)
					if err != nil {
						t.Fatal(err)
					}
					if r.AutomaticExecution.Actions[0].Status != "pending" || r.AutomaticExecution.Actions[0].ChildJobID != "" {
						t.Fatal("failed poll leaked retry application")
					}
				}
				p, err := pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil {
					t.Fatal(err)
				}
				if p.AutomaticExecution.PollingFailure == nil || p.AutomaticExecution.ConsecutiveFailures != 0 || p.AutomaticExecution.PausedAt != nil {
					t.Fatal("failure state missing or execution counter changed")
				}
				if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_recovery_policies DROP CONSTRAINT test_automatic_poll_advance"); err != nil {
					t.Fatal(err)
				}
				pg = state.NewPgStore((*pg.Pool()).Pool)
				if did, e := pg.ProcessNextWorkflowRecoveryAutomaticPolicy(ctx, p.AutomaticExecution.PollingFailure.RetryAt); e != nil || !did {
					t.Fatalf("retry %v %v", did, e)
				}
				if count(jobsQuery) != jobs+1 || count(budgetQuery) != budget+1 {
					t.Fatal("retry did not create and charge exactly one job")
				}
				p, err = pg.GetWorkflowRecoveryPolicy(ctx, app.AccountID, app.ID)
				if err != nil || p.AutomaticExecution.PollingFailure != nil {
					t.Fatal("successful retry retained polling failure", err)
				}
			})
		})
	}
}
