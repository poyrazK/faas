package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestLegacyDeploymentCronOwnershipMemory(t *testing.T) {
	testLegacyCronOwnership(t, state.NewMemStore())
}
func testLegacyCronOwnership(t *testing.T, store state.Store) {
	app, p, _ := cronStageFixture(t, store)
	staging := store.(state.DeploymentCronStagingStore)
	legacy := store.(state.LegacyDeploymentCronStore)
	p.Request.Operation = "stage"
	if _, err := staging.ApplyDeploymentCronStaging(t.Context(), app.ID, p); err != nil {
		t.Fatal(err)
	}
	ctx := state.ContextWithDeploymentSubmission(t.Context(), p.Request.SessionID)
	if owned, err := legacy.LegacyDeploymentCrons(ctx, app.ID); err != nil || !owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
	for _, ctx := range []string{"", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"} {
		if owned, err := legacy.LegacyDeploymentCrons(state.ContextWithDeploymentSubmission(t.Context(), ctx), app.ID); err != nil || owned {
			t.Fatalf("another attempt owns journal: %t err=%v", owned, err)
		}
	}
	p.Request.Operation = "restore"
	if _, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p); err != nil {
		t.Fatal(err)
	}
}
