package state_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 801
func TestProfileCanaryStepRejectsDatabaseIntegerOverflow(t *testing.T) {
	const deploymentID = "00000000-0000-4000-8000-000000000001"
	overflow := int64(math.MaxInt32) + 1
	for _, step := range []int{-1, int(overflow)} {
		key := state.ProfileCanaryCheckKey{DeploymentID: deploymentID, CanaryStep: step}
		for _, store := range []state.ProfileCanaryCheckStore{&state.PgStore{}, state.NewMemStore()} {
			_, err := store.GetProfileCanaryCheck(context.Background(), "account", "app", key)
			if !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("%T step %d: got %v, want invalid argument before querying storage", store, step, err)
			}
		}
		cursor := state.EncodeProfileCanaryHistoryCursor(deploymentID, api.CanaryProfileSignal{
			CanaryStep: step, CanaryStepStartedAt: time.Now().UTC(), PolicyRevision: 1,
		})
		if _, err := state.DecodeProfileCanaryHistoryCursor(cursor, deploymentID); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("cursor step %d: got %v, want invalid argument", step, err)
		}
	}
}
