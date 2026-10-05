// adr: 210
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt #3: `secrets set --restart` admitted a cold boot, `secrets unset`
// landed during the boot, and the boot still delivered the deleted secret
// (sealed at admission). Its started_at (readiness) came after the delete, so
// the restart path's init capture passed the freshness guard and every later
// restore restored the deleted secret. Freshness must be anchored on when the
// configuration was resolved: the wake's admission.
func TestRuntimeConfigStaleAnchorsOnWakeAdmission(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, deployment := seedApp(t, store, api.PlanPro, 256, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	admittedWake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	readyAfterChange := time.Now()

	booting := state.Instance{ID: "ins-booting", AppID: app.ID, DeploymentID: deployment.ID, WakeID: admittedWake.String(), StartedAt: readyAfterChange}
	if !e.runtimeConfigStale(ctx, booting) {
		t.Fatal("instance whose wake was admitted before the change was judged fresh; its capture would publish the old secrets")
	}

	time.Sleep(5 * time.Millisecond)
	laterWake, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	fresh := state.Instance{ID: "ins-fresh", AppID: app.ID, DeploymentID: deployment.ID, WakeID: laterWake.String(), StartedAt: time.Now()}
	if e.runtimeConfigStale(ctx, fresh) {
		t.Fatal("instance admitted after the change was judged stale")
	}

	// Legacy or non-v7 wake ids fall back to started_at.
	legacy := state.Instance{ID: "ins-legacy", AppID: app.ID, DeploymentID: deployment.ID, WakeID: "not-a-uuid", StartedAt: readyAfterChange}
	if e.runtimeConfigStale(ctx, legacy) {
		t.Fatal("non-v7 wake id must fall back to started_at")
	}
}
