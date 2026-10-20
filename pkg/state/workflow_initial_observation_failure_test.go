package state_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1279
func TestWorkflowInitialObservationFailureIsolation(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		ctx := context.Background()
		seed := func() (state.App, api.WorkflowInitialRecoveryReview) {
			_, app, failure, p := state.SeedInitialReviewWithContext(t, ctx, s)
			reviews := s.(state.WorkflowInitialRecoveryReviewStore)
			r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
			if err != nil {
				t.Fatal(err)
			}
			r, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve recovery"})
			if err != nil {
				t.Fatal(err)
			}
			return app, r
		}
		badApp, bad := seed()
		healthyApp, healthy := seed()
		originalRun := bad.Target.RunID
		state.MutateInitialReviewWithContext(t, ctx, s, bad.ID, func(r *api.WorkflowInitialRecoveryReview) { r.Target.RunID = "invalid" })
		now := time.Now().UTC().Truncate(time.Microsecond)
		if m, ok := s.(*state.MemStore); ok {
			(*m.InitialCompletionNext()) = map[string]time.Time{bad.ID: now.Add(-time.Hour), healthy.ID: now}
		} else if _, err := (*s.(*state.PgStore).Pool()).Exec(ctx, "UPDATE workflow_initial_recovery_reviews SET completion_next_observe_at=$2 WHERE id=$1::uuid", bad.ID, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		poll := func(at time.Time) bool {
			t.Helper()
			did, err := s.(state.WorkflowInitialManualCompletionStore).ProcessNextWorkflowInitialManualCompletion(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			return did
		}
		if !poll(now) {
			t.Fatal("poison review not deferred")
		}
		failed := state.InitialAutomaticReviewWithContext(t, ctx, s, badApp, bad.ID)
		f := failed.ObservationFailure
		if f == nil || f.Code != "invalid_recovery_identity" || f.Attempts != 1 || !f.RetryAt.Equal(now.Add(time.Minute)) || failed.Attention != nil || failed.Result != nil {
			t.Fatalf("failure state %+v", failed)
		}
		if !poll(now) {
			t.Fatal("healthy review starved")
		}
		good := state.InitialAutomaticReviewWithContext(t, ctx, s, healthyApp, healthy.ID)
		if good.Attention == nil || good.ObservationFailure != nil {
			t.Fatal("healthy review not observed")
		}
		if poll(now) {
			t.Fatal("failed review retried before due")
		}
		if pg, ok := s.(*state.PgStore); ok {
			s = state.NewPgStore((*pg.Pool()).Pool)
		}
		if got := state.InitialAutomaticReviewWithContext(t, ctx, s, badApp, bad.ID).ObservationFailure; !reflect.DeepEqual(got, f) {
			t.Fatal("failure lost on restart")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.(state.WorkflowInitialManualCompletionStore).ProcessNextWorkflowInitialManualCompletion(cancelled, f.RetryAt); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation %v", err)
		}
		if got := state.InitialAutomaticReviewWithContext(t, ctx, s, badApp, bad.ID).ObservationFailure; !reflect.DeepEqual(got, f) {
			t.Fatal("cancellation changed retry state")
		}
		for range 2 {
			poll(f.RetryAt)
		}
		failed = state.InitialAutomaticReviewWithContext(t, ctx, s, badApp, bad.ID)
		second := failed.ObservationFailure
		if second == nil || second.Attempts != 2 || !second.RetryAt.Equal(f.RetryAt.Add(2*time.Minute)) {
			t.Fatalf("backoff %+v", second)
		}
		state.MutateInitialReviewWithContext(t, ctx, s, bad.ID, func(r *api.WorkflowInitialRecoveryReview) { r.Target.RunID = originalRun })
		for range 2 {
			poll(second.RetryAt)
		}
		recovered := state.InitialAutomaticReviewWithContext(t, ctx, s, badApp, bad.ID)
		if recovered.ObservationFailure != nil || recovered.Attention == nil || recovered.Result != nil || recovered.JobID != bad.JobID {
			t.Fatal("successful observation did not reset failure safely")
		}
	})
}

func TestWorkflowInitialObservationFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		code     string
		isolated bool
	}{{"23514", true}, {"22007", true}, {"22P02", true}, {"40001", false}, {"08006", false}, {"57014", false}, {"53100", false}, {"XX000", false}} {
		t.Run(tc.code, func(t *testing.T) {
			err := fmt.Errorf("observe: %w", &pgconn.PgError{Code: tc.code, Message: "sensitive database detail"})
			if (state.ManualObservationFailureCode(err) != "") != tc.isolated {
				t.Fatal("classification", tc.code)
			}
		})
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("unknown error")} {
		if state.ManualObservationFailureCode(err) != "" {
			t.Fatal("unexpected isolation", err)
		}
	}
	now := time.Now().UTC()
	var previous *api.WorkflowInitialObservationFailure
	for range 100 {
		previous = state.NextManualObservationFailure(previous, "invalid_observation_data", now)
		if previous.RetryAt.Sub(previous.FailedAt) > api.WorkflowInitialObservationMaxBackoff {
			t.Fatal("unbounded backoff")
		}
	}
	if previous.Attempts != api.WorkflowInitialObservationMaxAttempts || previous.RetryAt.Sub(previous.FailedAt) != api.WorkflowInitialObservationMaxBackoff {
		t.Fatal("backoff cap", previous)
	}
}

func TestWorkflowInitialObservationFailureRollback(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, s state.Store) {
		pg, ok := s.(*state.PgStore)
		if !ok {
			t.Skip("PostgreSQL savepoint regression")
		}
		ctx := context.Background()
		run, app, failure, p := state.SeedInitialReviewWithContext(t, ctx, s)
		reviews := s.(state.WorkflowInitialRecoveryReviewStore)
		r, err := reviews.PrepareWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, failure, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		r, err = reviews.DecideWorkflowInitialRecoveryReview(ctx, app.AccountID, app.ID, r.ID, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: "approve recovery"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.(state.EventRecoveryStore).ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
			t.Fatal(err)
		}
		// Fail after the observer appends result history, proving the savepoint
		// rolls it back before committing the separate polling failure metadata.
		if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_initial_recovery_reviews ADD CONSTRAINT test_observation_result_failure CHECK (NOT review ? 'result')"); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if did, e := pg.ProcessNextWorkflowInitialManualCompletion(ctx, now); e != nil || !did {
			t.Fatalf("defer %v %v", did, e)
		}
		saved := state.InitialAutomaticReviewWithContext(t, ctx, s, app, r.ID)
		if saved.ObservationFailure == nil || saved.ObservationFailure.Code != "invalid_observation_data" || saved.Result != nil {
			t.Fatalf("failed transaction leaked result %+v", saved)
		}
		var count int
		if err = (*pg.Pool()).QueryRow(ctx, "SELECT count(*) FROM workflow_recovery_history_events WHERE app_id=$1::uuid AND entry->>'kind'='initial_recovery_result'", app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("failed observation leaked history", count)
		}
		if _, err = (*pg.Pool()).Exec(ctx, "ALTER TABLE workflow_initial_recovery_reviews DROP CONSTRAINT test_observation_result_failure"); err != nil {
			t.Fatal(err)
		}
		pg = state.NewPgStore((*pg.Pool()).Pool)
		if did, e := pg.ProcessNextWorkflowInitialManualCompletion(ctx, saved.ObservationFailure.RetryAt); e != nil || !did {
			t.Fatalf("retry %v %v", did, e)
		}
		saved = state.InitialAutomaticReviewWithContext(t, ctx, pg, app, r.ID)
		if saved.ObservationFailure != nil || saved.Result == nil || saved.Result.Outcome != "failed" {
			t.Fatal("verified result missing after retry")
		}
		if err = (*pg.Pool()).QueryRow(ctx, "SELECT count(*) FROM workflow_recovery_history_events WHERE app_id=$1::uuid AND entry->>'kind'='initial_recovery_result'", app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatal("result history duplication", count)
		}
	})
}
