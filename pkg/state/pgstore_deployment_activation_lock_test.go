//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreDeploymentActivationLockSerializesSubscribers(t *testing.T) {
	pool := pgtest.Open(t)
	first := state.NewPgStore(pool)
	second := state.NewPgStore(pool)
	deploymentID := uuid.NewString()
	ctx := context.Background()
	releaseFirst, err := first.AcquireDeploymentActivationLock(ctx, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst(ctx)

	acquired := make(chan error, 1)
	go func() {
		releaseSecond, err := second.AcquireDeploymentActivationLock(ctx, deploymentID)
		if err == nil {
			releaseSecond(ctx)
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second subscriber acquired lock early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst(ctx)
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second subscriber did not acquire after release")
	}
}
