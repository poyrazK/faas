package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testRuntimeConfigChangeOrdersWithInstanceStart pins the contract schedd's
// park guard relies on (issue #3360): the runtime-config stamp and
// instances.started_at come from one clock, so an instance created before a
// change reports a start strictly before the stamp, and one created after
// reports a start no earlier than it. The stamp is per app.
func testRuntimeConfigChangeOrdersWithInstanceStart(t *testing.T, fx *Fixture) {
	create := func(label string) state.Instance {
		t.Helper()
		ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
			string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatalf("CreateInstance(%s): %v", label, err)
		}
		return ins
	}
	stamp := func() time.Time {
		t.Helper()
		if err := fx.Store.MarkAppRuntimeConfigChanged(fx.Ctx, fx.App.ID); err != nil {
			t.Fatalf("MarkAppRuntimeConfigChanged: %v", err)
		}
		changedAt, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, fx.App.ID)
		if err != nil || !ok {
			t.Fatalf("AppRuntimeConfigChangedAt = (ok=%v, err=%v), want stamped", ok, err)
		}
		return changedAt
	}

	if _, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, fx.App.ID); err != nil || ok {
		t.Fatalf("unchanged app = (ok=%v, err=%v), want no stamp", ok, err)
	}
	before := create("before")
	time.Sleep(2 * time.Millisecond)
	changedAt := stamp()
	if !changedAt.After(before.StartedAt) {
		t.Fatalf("stamp %v is not after the earlier instance start %v", changedAt, before.StartedAt)
	}
	time.Sleep(2 * time.Millisecond)
	after := create("after")
	if changedAt.After(after.StartedAt) {
		t.Fatalf("stamp %v is after the later instance start %v", changedAt, after.StartedAt)
	}
	if _, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, uuid.NewString()); err != nil || ok {
		t.Fatalf("other app = (ok=%v, err=%v), want no stamp", ok, err)
	}
}
