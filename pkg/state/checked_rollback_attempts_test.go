// adr: 20261026090000
package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCheckedRollbackAttemptMem(t *testing.T) { checkedRollbackAttemptSuite(t, state.NewMemStore()) }
func TestCheckedRollbackAttemptPG(t *testing.T) {
	s, _ := pgStore(t)
	checkedRollbackAttemptSuite(t, s)
}
func checkedRollbackAttemptSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	acct, app, target, current := checkedRollbackFixture(t, s, false)
	store := s.(state.CheckedRollbackStore)
	attempt := strings.ReplaceAll(uuid.NewString(), "-", "")
	id := uuid.MustParse(attempt).String()
	attemptCtx := state.ContextWithCheckedRollbackAttempt(ctx, attempt)
	if _, err := store.CreateCheckedRollback(attemptCtx, acct.ID, app.ID, target.ID, uuid.NewString(), "original"); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("stale CAS: %v", err)
	}
	if _, err := store.GetCheckedRollback(ctx, acct.ID, app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed request left a receipt")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := store.CreateCheckedRollback(attemptCtx, acct.ID, app.ID, target.ID, current.ID, "original")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, state.ErrRollbackAttemptAccepted) {
			t.Fatalf("concurrent attempt: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d", accepted)
	}
	op, err := store.GetCheckedRollback(ctx, acct.ID, app.ID, id)
	if err != nil || op.ID != id || op.TargetDeploymentID != target.ID || op.CurrentDeploymentID != current.ID || op.Status != "preparing" || op.Reason != "original" || op.CreatedAt.IsZero() {
		t.Fatalf("atomic receipt: %+v %v", op, err)
	}
	got, _ := s.DeploymentByID(ctx, current.ID)
	if got.TrafficPercent != 100 {
		t.Fatal("acceptance implied routing")
	}
	if err = store.UpdateCheckedRollback(ctx, op, "failed", "later_failure", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCheckedRollback(attemptCtx, acct.ID, app.ID, target.ID, current.ID, "original"); !errors.Is(err, state.ErrRollbackAttemptAccepted) {
		t.Fatalf("terminal attempt replayed: %v", err)
	}
	alternateTarget, alternateCurrent := uuid.MustParse(target.ID).String(), uuid.MustParse(current.ID).String()
	if target.ID == alternateTarget {
		alternateTarget = strings.ReplaceAll(target.ID, "-", "")
	}
	if current.ID == alternateCurrent {
		alternateCurrent = strings.ReplaceAll(current.ID, "-", "")
	}
	if _, err = store.CreateCheckedRollback(attemptCtx, acct.ID, app.ID, alternateTarget, alternateCurrent, "original"); !errors.Is(err, state.ErrRollbackAttemptAccepted) {
		t.Fatalf("UUID spelling changed accepted request: %v", err)
	}
	if _, err = store.CreateCheckedRollback(attemptCtx, acct.ID, app.ID, target.ID, current.ID, "changed"); !errors.Is(err, state.ErrRollbackAttemptConflict) {
		t.Fatalf("changed request accepted: %v", err)
	}
	if _, err = store.GetCheckedRollback(ctx, uuid.NewString(), app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign owner read receipt")
	}
	if _, err = store.CreateCheckedRollback(state.ContextWithCheckedRollbackAttempt(ctx, "bad"), acct.ID, app.ID, target.ID, current.ID, "original"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid attempt: %v", err)
	}
}
func TestCheckedRollbackAttemptRejectsService(t *testing.T) {
	s := state.NewMemStore()
	a, app, target, current := checkedRollbackFixture(t, s, true)
	ctx := state.ContextWithCheckedRollbackAttempt(t.Context(), strings.Repeat("a", 32))
	if _, err := s.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "service"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("service request attempt accepted: %v", err)
	}
}

func TestCheckedRollbackAttemptPGInsertFailure(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := t.Context()
	a, app, target, current := checkedRollbackFixture(t, s, false)
	before, err := s.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE FUNCTION refuse_rollback_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt storage failure'; END $$; CREATE TRIGGER refuse_rollback_receipt BEFORE INSERT ON deployment_rollback_operations FOR EACH ROW EXECUTE FUNCTION refuse_rollback_receipt()`)
	if err != nil {
		t.Fatal(err)
	}
	attempt := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = s.CreateCheckedRollback(state.ContextWithCheckedRollbackAttempt(ctx, attempt), a.ID, app.ID, target.ID, current.ID, "original"); err == nil {
		t.Fatal("receipt insertion failure accepted")
	}
	after, err := s.DeploymentByID(ctx, target.ID)
	if err != nil || after.Status != before.Status || after.TrafficPercent != before.TrafficPercent {
		t.Fatalf("transaction changed readiness despite receipt failure: %+v %v", after, err)
	}
	if _, err = s.GetCheckedRollback(ctx, a.ID, app.ID, uuid.MustParse(attempt).String()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed transaction retained attempt")
	}
}
