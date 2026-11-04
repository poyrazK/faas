package imaged

// adr: 20261029174438

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestImagePrepWorkersRunDeploymentsConcurrently(t *testing.T) {
	w := newImagePrepWorkers(2)
	started, release := make(chan string, 3), make(chan struct{})
	work := func(id string) func(context.Context) {
		return func(context.Context) { started <- id; <-release }
	}
	for _, id := range []string{"a", "b"} {
		if !w.start(t.Context(), id, work(id)) {
			t.Fatalf("start %s refused", id)
		}
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("two deployments did not prepare at the same time")
		}
	}
	third := make(chan bool, 1)
	go func() { third <- w.start(t.Context(), "c", work("c")) }()
	select {
	case <-third:
		t.Fatal("a third deployment started while both workers were busy")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if !<-third {
		t.Fatal("the third deployment never started")
	}
	w.wait()
	if got := <-started; got != "c" {
		t.Fatalf("started %q, want c", got)
	}
}

func TestImagePrepWorkersNeverPrepareADeploymentTwice(t *testing.T) {
	w := newImagePrepWorkers(2)
	release := make(chan struct{})
	if !w.start(t.Context(), "a", func(context.Context) { <-release }) {
		t.Fatal("start refused")
	}
	if w.start(t.Context(), "a", func(context.Context) { t.Error("duplicate preparation ran") }) {
		t.Fatal("a deployment already preparing started again")
	}
	close(release)
	w.wait()
	ran := false
	if !w.start(t.Context(), "a", func(context.Context) { ran = true }) {
		t.Fatal("a finished deployment could not start again")
	}
	w.wait()
	if !ran {
		t.Fatal("restarted preparation did not run")
	}
}

func TestImagePrepWorkersStopWaitingWhenCancelled(t *testing.T) {
	w := newImagePrepWorkers(2)
	release := make(chan struct{})
	for _, id := range []string{"a", "b"} {
		w.start(t.Context(), id, func(context.Context) { <-release })
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if w.start(ctx, "c", func(context.Context) { t.Error("cancelled preparation ran") }) {
		t.Fatal("start succeeded after its context ended")
	}
	close(release)
	w.wait()
	if !w.start(t.Context(), "c", func(context.Context) {}) {
		t.Fatal("a cancelled wait left the deployment marked as preparing")
	}
	w.wait()
}

func TestImagePrepWorkersOffPreparesInline(t *testing.T) {
	for _, n := range []int{0, 1} {
		if w := newImagePrepWorkers(n); w != nil {
			t.Fatalf("newImagePrepWorkers(%d) = %v, want inline preparation", n, w)
		}
	}
	var off *imagePrepWorkers
	off.wait()
}

func TestParallelImagePrepTakesOnlyThisNodesSnapshotBoot(t *testing.T) {
	payload := func(dep, node string) string {
		raw, err := json.Marshal(snapshotBootPayload{AppID: "app", DeploymentID: dep, NodeID: node})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	h := (&Handler{}).WithNodeName("node-a")
	on := NewLoop(LoopConfig{Handler: h, ImagePrepWorkers: 2, Log: silentLogger()})
	off := NewLoop(LoopConfig{Handler: h, Log: silentLogger()})
	for _, tc := range []struct {
		name string
		loop *Loop
		n    db.Notification
		want bool
	}{
		{name: "this node", loop: on, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: payload("dep-1", "node-a")}, want: true},
		{name: "unassigned", loop: on, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: payload("dep-1", "")}, want: handlesSnapshotBoot("node-a", "")},
		{name: "sibling node", loop: on, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: payload("dep-1", "node-b")}},
		{name: "no deployment", loop: on, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: payload("", "node-a")}},
		{name: "undecodable", loop: on, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: "{"}},
		{name: "other channel", loop: on, n: db.Notification{Channel: db.NotifyDeploymentChanged, Payload: payload("dep-1", "node-a")}},
		{name: "workers off", loop: off, n: db.Notification{Channel: db.NotifySnapshotBoot, Payload: payload("dep-1", "node-a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.loop.parallelImagePrep(tc.n)
			if ok != tc.want || (ok && got != "dep-1") {
				t.Fatalf("parallelImagePrep = %q, %v; want dep-1, %v", got, ok, tc.want)
			}
		})
	}
}

// Recovery hands a deployment to the workers, skips one already preparing,
// and the preparation still reaches the snapshot handoff.
func TestRecoveryHandsImagePreparationToWorkers(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	builder := &fakeBuilder{bytesOut: 9}
	scans := 0
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans)
	loop := NewLoop(LoopConfig{Handler: h, Store: store, ImagePrepWorkers: 2, Log: silentLogger()})

	work := []state.BuildImageWork{{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"}}
	release := make(chan struct{})
	if !loop.prep.start(t.Context(), dep.ID, func(context.Context) { <-release }) {
		t.Fatal("start refused")
	}
	loop.recoverImageWork(t.Context(), work, "awaiting")
	close(release)
	loop.prep.wait()
	if len(builder.calls) != 0 {
		t.Fatal("recovery prepared a deployment that was already preparing")
	}

	loop.recoverImageWork(t.Context(), work, "awaiting")
	loop.prep.wait()
	got, err := store.DeploymentByID(t.Context(), dep.ID)
	if err != nil || got.Status != state.DeploySnapshotting {
		t.Fatalf("worker preparation did not hand off: %s %v", got.Status, err)
	}
	if len(builder.calls) != 1 || scans != 1 {
		t.Fatalf("builds=%d scans=%d, want 1 each", len(builder.calls), scans)
	}
}
