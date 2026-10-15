//go:build !no_pg

// adr: 1151 — the notification commits with the publication, and clearing
// an unmarked deployment's snapshot backoff writes nothing.
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimePublicationBookkeeping(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testRuntimePublicationBookkeeping(t, store)
}

func TestPgRuntimePublicationNotifiesWhenItCommits(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `listen instance_changed`); err != nil {
		t.Fatal(err)
	}
	f := seedRuntimeAppEnv(t, store)
	node := runtimeSecretNodeForTest(t, store)
	notifying := func(p state.RuntimeInstancePublication) state.RuntimeInstancePublication {
		p.Bookkeeping = transitionBookkeeping()
		p.Bookkeeping.NotifyChannel, p.Bookkeeping.NotifyPayload = "instance_changed", `{"instance_id":"`+p.InstanceID+`"}`
		return p
	}

	refused := notifying(seedRuntimeInstancePublication(t, store, f, f.deployments["production"], node))
	refused.WakeID = uuid.NewString()
	if _, err := store.PublishOwnedInstanceRuntime(ctx, refused); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("publication with another wake id = %v, want ErrConflict", err)
	}
	quiet, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if n, err := listener.Conn().WaitForNotification(quiet); err == nil {
		t.Fatalf("a refused publication notified %q", n.Payload)
	}

	p := notifying(seedRuntimeInstancePublication(t, store, f, f.deployments["production"], node))
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil {
		t.Fatalf("publish: %v", err)
	}
	wait, cancelWait := context.WithTimeout(ctx, 3*time.Second)
	defer cancelWait()
	n, err := listener.Conn().WaitForNotification(wait)
	if err != nil || n.Channel != "instance_changed" || n.Payload != p.Bookkeeping.NotifyPayload {
		t.Fatalf("notification = %+v, %v; want %q", n, err, p.Bookkeeping.NotifyPayload)
	}
}

func TestPgClearSnapshotBackoffLeavesAnUnmarkedDeploymentAlone(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["production"]
	version := func() string {
		t.Helper()
		var xmin string
		if err := pool.QueryRow(ctx, `SELECT xmin::text FROM deployments WHERE id=$1`, dep.ID).Scan(&xmin); err != nil {
			t.Fatal(err)
		}
		return xmin
	}
	before := version()
	if err := store.DeploymentClearSnapshotBackoff(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if after := version(); after != before {
		t.Fatalf("clearing an unmarked deployment wrote a row version (%s -> %s)", before, after)
	}
	if err := store.DeploymentRecordSnapshotMiss(ctx, dep.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeploymentClearSnapshotBackoff(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, active, err := store.DeploymentSnapshotBackoffActive(ctx, dep.ID); err != nil || active {
		t.Fatalf("backoff after clear: active %v, err %v", active, err)
	}
}
