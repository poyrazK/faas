package state

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 797
func TestProfileCanaryStepRejectsDatabaseIntegerOverflow(t *testing.T) {
	const deploymentID = "00000000-0000-4000-8000-000000000001"
	overflow := int64(math.MaxInt32) + 1
	for _, step := range []int{-1, int(overflow)} {
		key := ProfileCanaryCheckKey{DeploymentID: deploymentID, CanaryStep: step}
		for _, store := range []ProfileCanaryCheckStore{&PgStore{}, NewMemStore()} {
			_, err := store.GetProfileCanaryCheck(context.Background(), "account", "app", key)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("%T step %d: got %v, want invalid argument before querying storage", store, step, err)
			}
		}
		cursor := encodeProfileCanaryHistoryCursor(deploymentID, api.CanaryProfileSignal{
			CanaryStep: step, CanaryStepStartedAt: time.Now().UTC(), PolicyRevision: 1,
		})
		if _, err := decodeProfileCanaryHistoryCursor(cursor, deploymentID); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("cursor step %d: got %v, want invalid argument", step, err)
		}
	}
}
