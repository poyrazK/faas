// adr: 462 — skipped LISTEN/replay delivery cannot consume another node's work.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSnapshotBootSiblingReturnsSkippedOutcome(t *testing.T) {
	h := &Handler{nodeName: "node-b", log: silentLogger()}
	n := db.Notification{Channel: db.NotifySnapshotBoot, Payload: "{\"node_id\":\"node-a\",\"deployment_id\":\"deployment\"}"}
	if err := h.HandleNotification(context.Background(), n); !errors.Is(err, db.ErrNotificationNotOwned) {
		t.Fatalf("sibling handling looked successful: %v", err)
	}
	l := &Loop{handler: h}
	if err := l.HandleNotification(context.Background(), n); !errors.Is(err, db.ErrNotificationNotOwned) {
		t.Fatalf("replay lost skipped outcome: %v", err)
	}
	if err := l.dispatchNotification(context.Background(), n); err != nil {
		t.Fatalf("expected sibling LISTEN delivery was logged as a failure: %v", err)
	}
}

func TestPgSnapshotBootSiblingSkipSurvivesOwnerRestart(t *testing.T) {
	pool, f, n := snapshotOwnershipFixture(t)
	ctx := context.Background()
	sibling := snapshotOwnershipLoop(f, pool, "node-b", &fakeBuilder{})
	if err := sibling.dispatchNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	delivered, err := db.DrainNotificationOutboxOnceForNode(ctx, pool, "imaged", "node-b", []string{db.NotifySnapshotBoot}, sibling.HandleNotification, nil)
	if err != nil || delivered != 0 {
		t.Fatalf("sibling replay delivered owner work: delivered=%d err=%v", delivered, err)
	}
	assertSnapshotHandoffState(t, pool, n.OutboxID, "pending", 0)
	dep, err := f.store.DeploymentByID(ctx, f.dep.ID)
	if err != nil || dep.Status != state.DeployPending || len(f.bld.calls) != 0 {
		t.Fatalf("sibling advanced candidate: status=%s builds=%d err=%v", dep.Status, len(f.bld.calls), err)
	}
	// A new daemon object represents the owner returning after missing LISTEN.
	owner := snapshotOwnershipLoop(f, pool, "node-a", f.bld)
	delivered, err = db.DrainNotificationOutboxOnceForNode(ctx, pool, "restarted-imaged", "node-a", []string{db.NotifySnapshotBoot}, owner.HandleNotification, nil)
	if err != nil || delivered != 1 {
		t.Fatalf("owner recovery: delivered=%d err=%v", delivered, err)
	}
	assertSnapshotHandoffState(t, pool, n.OutboxID, "delivered", 1)
	dep, err = f.store.DeploymentByID(ctx, f.dep.ID)
	if err != nil || dep.Status != state.DeploySnapshotting || len(f.bld.calls) != 1 || findNotify(f.notif, db.NotifySnapshotPrime) == nil {
		t.Fatalf("owner did not perform handoff: status=%s builds=%d err=%v", dep.Status, len(f.bld.calls), err)
	}
	if err := owner.dispatchNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	delivered, err = db.DrainNotificationOutboxOnceForNode(ctx, pool, "imaged", "node-a", []string{db.NotifySnapshotBoot}, owner.HandleNotification, nil)
	if err != nil || delivered != 0 || len(f.bld.calls) != 1 {
		t.Fatalf("redelivery rebuilt the candidate: delivered=%d builds=%d err=%v", delivered, len(f.bld.calls), err)
	}
}

func TestSnapshotBootLegacyNotificationStillRunsDirectly(t *testing.T) {
	pool, f, n := snapshotOwnershipFixture(t)
	outboxID := n.OutboxID
	n.OutboxID = 0
	owner := snapshotOwnershipLoop(f, pool, "node-a", f.bld)
	if err := owner.dispatchNotification(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(f.bld.calls) != 1 || findNotify(f.notif, db.NotifySnapshotPrime) == nil {
		t.Fatal("legacy notification did not prepare and hand off the image")
	}
	assertSnapshotHandoffState(t, pool, outboxID, "pending", 0)
}

func TestPgSnapshotBootSiblingCannotAckInFlightOwner(t *testing.T) {
	pool, f, n := snapshotOwnershipFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.bld.buildHook = func() { close(entered); <-release }
	owner := snapshotOwnershipLoop(f, pool, "node-a", f.bld)
	result := make(chan error, 1)
	go func() { result <- owner.dispatchNotification(ctx, n) }()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("owner ended before build: %v", err)
	case <-ctx.Done():
		t.Fatal("owner did not start build")
	}
	sibling := snapshotOwnershipLoop(f, pool, "node-b", &fakeBuilder{})
	if err := sibling.dispatchNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	// The owner is still working; receiving the broadcast on a sibling cannot
	// preemptively acknowledge its durable handoff.
	duplicateBuilder := &fakeBuilder{}
	duplicate := snapshotOwnershipLoop(f, pool, "node-a", duplicateBuilder)
	if err := duplicate.dispatchNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	delivered, err := db.DrainNotificationOutboxOnceForNode(ctx, pool, "replay", "node-a", []string{db.NotifySnapshotBoot}, duplicate.HandleNotification, nil)
	if err != nil || delivered != 0 || len(duplicateBuilder.calls) != 0 {
		t.Fatalf("immediate delivery overlapped another owner: delivered=%d builds=%d err=%v", delivered, len(duplicateBuilder.calls), err)
	}
	assertSnapshotHandoffState(t, pool, n.OutboxID, "processing", 1)
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertSnapshotHandoffState(t, pool, n.OutboxID, "delivered", 1)
	if len(f.bld.calls) != 1 {
		t.Fatalf("builds=%d, want one", len(f.bld.calls))
	}
}

func snapshotOwnershipFixture(t *testing.T) (*pgxpool.Pool, *testHarness, db.Notification) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	f := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
	if err := f.store.SetDeploymentRootfs(ctx, f.dep.ID, "example.test/app:latest", "build", 8); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(snapshotBootPayload{AppID: f.app.ID, DeploymentID: f.dep.ID, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Notify(ctx, pool, db.NotifySnapshotBoot, string(payload)); err != nil {
		t.Fatal(err)
	}
	n := db.Notification{Channel: db.NotifySnapshotBoot, Payload: string(payload)}
	if err := pool.QueryRow(ctx, "UPDATE notification_outbox SET available_at=now() WHERE channel=$1 AND payload=$2 RETURNING id", n.Channel, n.Payload).Scan(&n.OutboxID); err != nil {
		t.Fatal(err)
	}
	return pool, f, n
}

func snapshotOwnershipLoop(f *testHarness, pool *pgxpool.Pool, node string, builder *fakeBuilder) *Loop {
	h := New(f.store, f.notif, fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}}, builder, "./init", f.appsR, silentLogger()).WithNodeName(node)
	return NewLoop(LoopConfig{Handler: h, Store: f.store, Pool: pool, Log: silentLogger()})
}

func assertSnapshotHandoffState(t *testing.T, pool *pgxpool.Pool, id int64, wantState string, wantAttempts int) {
	t.Helper()
	var status string
	var attempts int
	if err := pool.QueryRow(context.Background(), "SELECT state, attempts FROM notification_outbox WHERE id=$1", id).Scan(&status, &attempts); err != nil || status != wantState || attempts != wantAttempts {
		t.Fatalf("handoff state=%s attempts=%d err=%v, want %s/%d", status, attempts, err, wantState, wantAttempts)
	}
}
