// adr: 20261104144000
package state_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAbortReceiptsMem(t *testing.T) {
	s := state.NewMemStore()
	serviceAbortReceiptsSuite(t, s, func(ctx context.Context, id string) error {
		return s.SetDeploymentCanaryState(ctx, id, "none", 0, 0, time.Now(), "rolling_out")
	})
}

func TestServiceAbortReceiptsPG(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	serviceAbortReceiptsSuite(t, s, func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, id)
		return err
	})
}

func TestServiceAbortReceiptPGFailureRollsBack(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, app, predecessor, candidate := bindingPromotionFixture(t, s)
	if _, err := pool.Exec(ctx, `UPDATE deployments SET canary_total_steps=0,canary_step=0,rollout_state='rolling_out' WHERE id=$1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReadReleaseReview(ctx, app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_service_abort_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'service abort audit storage fault'; END $$;
 CREATE TRIGGER reject_service_abort_audit BEFORE INSERT ON deployment_audit FOR EACH ROW EXECUTE FUNCTION reject_service_abort_audit();`)
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("c", 32)
	writeCtx := state.ContextWithServiceAbortAttempt(state.WithServiceRecoveryReview(ctx, review), attempt)
	before, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, predecessor.ID, "fault"); err == nil {
		t.Fatal("failed audit allowed intent commit")
	}
	after, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || after.ServiceRolloutHandoff.Action != before.ServiceRolloutHandoff.Action || after.ServiceRolloutHandoff.BindingsCheck != nil || after.TrafficPercent != before.TrafficPercent {
		t.Fatalf("intent survived failed audit: %+v %v", after, err)
	}
	if _, err := s.ServiceAbortReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed intent has receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_service_abort_audit ON deployment_audit`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, predecessor.ID, "fault"); err != nil {
		t.Fatal(err)
	}
	// A new native store instance recovers committed evidence from the database.
	if _, err := state.NewPgStore(pool).ServiceAbortReceipt(ctx, app.ID, attempt); err != nil {
		t.Fatal(err)
	}
}

func serviceAbortReceiptsSuite(t *testing.T, s state.Store, mark func(context.Context, string) error) {
	t.Helper()
	ctx := t.Context()
	a, app, predecessor, candidate := bindingPromotionFixture(t, s)
	if err := mark(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	reviews := s.(state.ReleaseReviewStore)
	receipts := s.(state.ServiceAbortReceiptStore)
	aborter := s.(state.ServiceRolloutBindingStore)
	review, err := reviews.ReadReleaseReview(ctx, app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.Repeat("b", 32)
	writeCtx := state.ContextWithServiceAbortAttempt(state.WithServiceRecoveryReview(ctx, review), attempt)
	// Failure to match either exact selector or reviewed configuration must not
	// leave an acceptance record behind.
	if _, _, err := aborter.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, candidate.ID, "recover"); err == nil {
		t.Fatal("wrong pair admitted")
	}
	stale := review
	stale.ConfigurationHash = "stale"
	staleCtx := state.ContextWithServiceAbortAttempt(state.WithServiceRecoveryReview(ctx, stale), attempt)
	if _, _, err := aborter.RequestServiceRolloutAbort(staleCtx, app.ID, candidate.ID, predecessor.ID, "recover"); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("stale review: %v", err)
	}
	if _, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected intent has receipt: %v", err)
	}
	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			_, _, err := aborter.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, predecessor.ID, "recover")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
			t.Fatalf("duplicate: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d accepted duplicate writes", accepted)
	}
	receipt, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt)
	req := api.RecoverRolloutRequest{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID, Reason: "recover", ExpectedReview: &review}
	if err != nil || receipt.RequestHash != api.ServiceAbortRequestHash(app.ID, req) || receipt.AcceptedAt.IsZero() || receipt.RequestID == "" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	current, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || current.ServiceRolloutHandoff.BindingsCheck.RequestID != receipt.RequestID || current.TrafficPercent != 0 || current.RolloutState != "rolling_out" {
		t.Fatalf("receipt/intent mismatch: %+v %v", current, err)
	}
	if _, _, err := aborter.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, predecessor.ID, "changed"); !errors.Is(err, state.ErrServiceAbortAttemptConflict) {
		t.Fatalf("different body reused attempt: %v", err)
	}
	if err := s.UpsertAppEnv(ctx, a.ID, app.ID, "AFTER_ACCEPTANCE", "changed"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := aborter.RequestServiceRolloutAbort(writeCtx, app.ID, candidate.ID, predecessor.ID, "recover"); !errors.Is(err, state.ErrServiceAbortAttemptAccepted) {
		t.Fatalf("review change masked accepted receipt: %v", err)
	}
	again, err := receipts.ServiceAbortReceipt(ctx, app.ID, attempt)
	if err != nil || again != receipt {
		t.Fatalf("historical evidence changed: %+v %v", again, err)
	}
	if _, err := receipts.ServiceAbortReceipt(ctx, predecessor.ID, attempt); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign app exposed receipt: %v", err)
	}
}
