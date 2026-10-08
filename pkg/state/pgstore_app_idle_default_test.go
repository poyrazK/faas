// spec: §4.3 — idle_timeout_s 0 restores the plan default.

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-41): the API documents idle_timeout_s=0 as "plan
// default", but UpdateApp wrote a literal 0 into a column whose CHECK allows
// NULL or >= 10, so `gregale app <slug> --idle 0` failed as 503 "could not
// update app".
func TestPgStoreUpdateAppIdleZeroRestoresThePlanDefault(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "idle-default@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "idle-default", RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{IdleTimeoutS: &zero, SetIdleTimeout: true}); err != nil {
		t.Fatalf("UpdateApp idle_timeout_s=0: %v", err)
	}
	got, err := s.AppByID(ctx, app.ID)
	if err != nil || got.IdleTimeoutS != 0 {
		t.Fatalf("idle_timeout_s = %d (%v), want 0 (plan default)", got.IdleTimeoutS, err)
	}
}
