package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The plan_changed_at trigger stamps a plan change on any write path and
// leaves it alone otherwise; PlanChangedAt reads it (ADR-20261103121407).
func TestPg_PlanChangedAt(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-plan-changed-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PlanChangedAt(ctx, account.ID); err != nil || ok {
		t.Fatalf("new account change time = ok %v, err %v; want unknown", ok, err)
	}
	before := time.Now().Add(-time.Minute)
	if err := s.UpdateAccountPlan(ctx, account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	changed, ok, err := s.PlanChangedAt(ctx, account.ID)
	if err != nil || !ok || changed.Before(before) {
		t.Fatalf("after upgrade = %s ok %v err %v, want stamped", changed, ok, err)
	}
	// A write that leaves plan unchanged keeps the stamp, also from raw SQL.
	if _, err := pool.Exec(ctx, `update accounts set plan = plan, email = email where id = $1`, account.ID); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.PlanChangedAt(ctx, account.ID)
	if err != nil || !again.Equal(changed) {
		t.Fatalf("unchanged plan restamped: %s -> %s (%v)", changed, again, err)
	}
	if _, ok, err := s.PlanChangedAt(ctx, uuid.NewString()); err != nil || ok {
		t.Fatalf("unknown subject = ok %v err %v", ok, err)
	}
	var _ state.PlanChangeReader = s
}
