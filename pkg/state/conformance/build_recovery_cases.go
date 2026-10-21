package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func testOperatorBuildRecoverySnapshot(t *testing.T, fx *Fixture) {
	reader, ok := fx.Store.(state.BuildRecoveryObservationStore)
	if !ok {
		t.Fatal("store lacks recovery observation")
	}
	cleanup, ok := fx.Store.(state.BuildVMCleanupStore)
	if !ok {
		t.Fatal("store lacks durable VM cleanup")
	}
	check := func(running int64, started time.Time, pending, retried int64) state.BuildRecoverySnapshot {
		t.Helper()
		s, err := reader.BuildRecoverySnapshot(fx.Ctx)
		if err != nil || s.Running != running || !s.OldestStartedAt.Equal(started) || s.PendingCleanup != pending || s.RetriedCleanup != retried {
			t.Fatalf("snapshot = %+v, %v; want running=%d oldest=%v cleanup=%d retried=%d", s, err, running, started, pending, retried)
		}
		if pending == 0 && !s.OldestCleanupCreatedAt.IsZero() || pending > 0 && s.OldestCleanupCreatedAt.IsZero() {
			t.Fatalf("cleanup age inconsistent: %+v", s)
		}
		return s
	}
	check(0, time.Time{}, 0, 0)
	first, second := seedQueuedBuild(t, fx), seedQueuedBuild(t, fx)
	check(0, time.Time{}, 0, 0) // Queued work is excluded.
	first, err := fx.Store.ClaimQueuedBuild(fx.Ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(1, first.StartedAt, 0, 0)
	second, err = fx.Store.ClaimQueuedBuild(fx.Ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(2, first.StartedAt, 0, 0)
	if err := fx.Store.RequeueBuild(fx.Ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	check(1, second.StartedAt, 0, 0)
	before := time.Now().UTC().Add(-time.Second)
	if _, _, err := fx.Store.CancelDeploymentTx(fx.Ctx, second.DeploymentID, "operator:test", state.CancelReasonUser); err != nil {
		t.Fatal(err)
	}
	s := check(0, time.Time{}, 1, 0)
	created := s.OldestCleanupCreatedAt
	if created.Before(before) || created.After(time.Now().UTC()) {
		t.Fatalf("unexpected creation time %v", created)
	}
	claim := func() state.BuildVMCleanupClaim {
		t.Helper()
		claims, err := cleanup.ClaimBuildVMCleanup(fx.Ctx, 1)
		if err != nil || len(claims) != 1 || claims[0].BuildID != second.ID {
			t.Fatalf("claims=%+v, %v", claims, err)
		}
		return claims[0]
	}
	firstClaim := claim()
	if got := check(0, time.Time{}, 1, 0); !got.OldestCleanupCreatedAt.Equal(created) {
		t.Fatal("claim reset cleanup age")
	}
	// Even an empty error message is a recorded failed attempt, not success.
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, second.ID, firstClaim.ClaimToken, errors.New("")); err != nil {
		t.Fatal(err)
	}
	if got := check(0, time.Time{}, 1, 1); !got.OldestCleanupCreatedAt.Equal(created) {
		t.Fatal("retry reset cleanup age")
	}
	secondClaim := claim()
	if got := check(0, time.Time{}, 1, 1); !got.OldestCleanupCreatedAt.Equal(created) {
		t.Fatal("retry claim reset cleanup age")
	}
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, second.ID, firstClaim.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	check(0, time.Time{}, 1, 1) // A fenced late result cannot hide pending cleanup.
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, second.ID, secondClaim.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	check(0, time.Time{}, 0, 0)
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := fx.Store.SweepStuckRunningBuilds(fx.Ctx, time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("sweep=%d,%v", n, err)
	}
	check(0, time.Time{}, 1, 0) // Failed running rows leave durable cleanup evidence.
	claims, err := cleanup.ClaimBuildVMCleanup(fx.Ctx, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("timeout cleanup claims=%+v, %v", claims, err)
	}
	if err := cleanup.CompleteBuildVMCleanup(fx.Ctx, claims[0].BuildID, claims[0].ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	third := seedQueuedBuild(t, fx)
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	before = time.Now().UTC().Add(-time.Second)
	// The event's timestamp may predate the write. Cleanup age begins when
	// the obligation is created, not at that historical event timestamp.
	if err := fx.Store.MarkBuildCancelled(fx.Ctx, third.ID, "operator:test", false, before.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := check(0, time.Time{}, 1, 0); got.OldestCleanupCreatedAt.Before(before) || got.OldestCleanupCreatedAt.After(time.Now().UTC()) {
		t.Fatalf("cleanup creation followed historical cancellation: %+v", got)
	}
}
