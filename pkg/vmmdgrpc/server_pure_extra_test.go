// server_pure_extra_test.go — fill pkg/vmmdgrpc/server.go coverage of
// the small pure / nil-safe-delegate surface that the bufconn test
// only partially exercises. Targets WithEvents (chainable), the
// nil-events noop branch of emitBootStartedMirror, the nil-cache
// branches of ForgetNet/ForgetActivity, exportDirFor's interface
// assertion (hit + miss), and Heartbeat (pure RPC).
//
// Whitebox `package vmmdgrpc`.

package vmmdgrpc

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- WithEvents (chainable setter) -------------------------------

func TestServer_WithEvents_Chainable(t *testing.T) {
	s := &Server{}
	if got := s.WithEvents(nil); got != s {
		t.Error("WithEvents(nil): not chainable")
	}
	if s.events != nil {
		t.Error("WithEvents(nil): events not nil")
	}
}

// --- emitBootStartedMirror ---------------------------------------

func TestServer_EmitBootStartedMirror_NilEventsNoOp(t *testing.T) {
	// Default Server.events == nil → the mirror short-circuits.
	s := &Server{}
	s.emitBootStartedMirror(context.Background(), "inst-1", "restore")
	// No assertion needed beyond "no panic".
}

func TestServer_EmitBootStartedMirror_WithContextFields(t *testing.T) {
	// When wire.FromContext carries an envelope, the mirror reads
	// it. Without an events.Platform wired we can't observe the
	// emit, so this test pins only the context-field read path:
	// WithEvents(nil) + emit with a context carrying wire fields
	// must still short-circuit safely.
	ctx := context.Background()
	ctx = wire.WithContext(ctx, wire.CorrelationFields{
		WakeID:             "wake-1",
		AppID:              "app-1",
		Trigger:            "http",
		QueuedCount:        3,
		ConcurrencyAtAdmit: 7,
	})
	s := &Server{}
	s.emitBootStartedMirror(ctx, "inst-1", "cold_boot")
	// No panic, no observers wired. Pass.
}

// --- ForgetNet / ForgetActivity (nil-safe) -----------------------

func TestServer_ForgetNet_NilCacheNoOp(t *testing.T) {
	s := &Server{} // no netCache wired
	s.ForgetNet("inst-1")
	// Pass: no panic on nil cache.
}

func TestServer_ForgetActivity_NilCacheNoOp(t *testing.T) {
	s := &Server{} // no activity wired
	s.ForgetActivity("inst-1")
	// Pass: no panic on nil cache.
}

func TestServer_ForgetActivity_WiredCallsTracker(t *testing.T) {
	tr := activity.New(nil)
	tr.Begin("inst-1")
	s := &Server{activity: tr}
	if tr.Size() != 1 {
		t.Errorf("pre-Forget: size = %d, want 1", tr.Size())
	}
	s.ForgetActivity("inst-1")
	if tr.Size() != 0 {
		t.Errorf("post-Forget: size = %d, want 0", tr.Size())
	}
}

// --- exportDirFor (interface assertion) --------------------------

// vmmStubBase provides the no-op VmmdAPI surface; embed it in
// vmmPlain / vmmWithExportDir so each only declares what it
// specifically wants to customise.
type vmmStubBase struct{}

func (vmmStubBase) Wake(_ context.Context, _ fcvm.WakeRequest) (*fcvm.Instance, error) {
	return nil, nil
}
func (vmmStubBase) Park(_ context.Context, _ string, _ fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	return fcvm.SnapshotInfo{}, nil
}
func (vmmStubBase) WarmSnapshot(_ context.Context, _ string, _ fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	return fcvm.SnapshotInfo{}, nil
}
func (vmmStubBase) Destroy(_ context.Context, _ string) error { return nil }
func (vmmStubBase) DestroyWithExport(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// SignalAndKill (M-2 / ADR-138 §Decision 1) is the
// graceful stop sequence. Test fakes default to
// no-op + (false, 0, nil) — parse-failure tests do not
// reach the inner Manager. Behavioural coverage lives in
// pkg/fcvm/vmm_signal_kill_test.go (portable) and the
// //go:build metal test pkg/fcvm/vmm_signal_kill_metal_test.go.
func (vmmStubBase) SignalAndKill(_ context.Context, _ string, _ int32, _ int32) (bool, int32, error) {
	return false, 0, nil
}
func (vmmStubBase) UpdateEgressAllowlist(_ context.Context, _ string, _ []netip.Prefix) error {
	return nil
}
func (vmmStubBase) LiveCount() int                   { return 0 }
func (vmmStubBase) LeasedCount() int                 { return 0 }
func (vmmStubBase) InstancePID(_ string) (int, bool) { return 0, false }
func (vmmStubBase) LogRing(_ string) *logbuf.Ring    { return nil }
func (vmmStubBase) NetnsFor(_ string) (string, bool) { return "", false }
func (vmmStubBase) MountParentExt4(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (vmmStubBase) MaterializeParentExt4(_ context.Context, _, _ string) error {
	return nil
}
func (vmmStubBase) UmountParentExt4(_ context.Context, _ string) error { return nil }
func (vmmStubBase) MountOverlayParent(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (vmmStubBase) UmountOverlayParent(_ context.Context, _ string) error {
	return nil
}
func (vmmStubBase) MarkInstanceFrameworkReady(_ context.Context, _ string, _ int64) (bool, string, string, error) {
	return false, "", "", nil
}

// vmmWithExportDir implements VmmdAPI + ExportDirFor. The Server
// type-asserts vmm against the inline interface in exportDirFor.
type vmmWithExportDir struct {
	vmmStubBase
	dir string
}

func (v vmmWithExportDir) ExportDirFor(_ string) string { return v.dir }

// vmmPlain is a VmmdAPI implementation without ExportDirFor. Used
// to exercise the type-assertion-miss branch.
type vmmPlain struct{ vmmStubBase }

func TestServer_ExportDirFor_ImplementsInterface(t *testing.T) {
	s := &Server{vmm: vmmWithExportDir{dir: "/tmp/build-export"}}
	if got := s.exportDirFor("inst-1"); got != "/tmp/build-export" {
		t.Errorf("got %q, want /tmp/build-export", got)
	}
}

func TestServer_ExportDirFor_NoImplReturnsEmpty(t *testing.T) {
	// vmmPlain does NOT implement ExportDirFor → server returns "".
	s := &Server{vmm: vmmPlain{}}
	if got := s.exportDirFor("inst-1"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- Heartbeat (pure RPC) ---------------------------------------

func TestServer_Heartbeat_ReturnsEmptyResponse(t *testing.T) {
	s := &Server{ops: wire.NewOpsMetrics("vmmd_test")}
	resp, err := s.Heartbeat(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp == nil {
		t.Fatal("resp nil")
	}
	// Heartbeat returns a non-nil empty response — no fields asserted.
}
