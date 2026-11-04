package state

// ADR-595 extension (docs/adr/20261018-runtime-admission-rebind.md): an
// admitted runtime keeps its authority across vmmd restarts through
// append-only re-binds. Both stores run the same scenario.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRebindTestStore interface {
	standardSnapshotTestStore
	ApplicationStandardRebindStore
	ComputeNodeVMMDEpochStore
}

// restartStandardNode registers a fresh vmmd incarnation for the receipt's
// node and claims the next ownership epoch for it, as a restarted vmmd does.
func restartStandardNode(t *testing.T, s standardRebindTestStore, parent runtimeadmission.Binding) (string, uint64) {
	t.Helper()
	next := parent
	next.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, next, runtimeadmission.ArtifactProtocolVersion)
	cur, err := s.ComputeNodeVMMDEpoch(t.Context(), parent.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := s.ClaimComputeNodeVMMDEpoch(t.Context(), parent.NodeID, cur.Epoch, next.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	return next.Incarnation, epoch
}

func freshCaptureRequest(req ApplicationStandardSnapshotCaptureRequest, deploymentID string) ApplicationStandardSnapshotCaptureRequest {
	req.Token = uuid.NewString()
	prefix := strings.TrimSuffix(SnapshotCaptureMemKey(deploymentID, req.Mode, req.Token), "mem")
	req.MemoryKey, req.VMStateKey, req.PrivateDriveKey = prefix+"mem", prefix+"vmstate", prefix+"drive"
	return req
}

func rebindObserved(r runtimeadmission.Receipt) ApplicationStandardRebindObserved {
	return ApplicationStandardRebindObserved{Token: r.Binding.Token, CompletedAtUnixNano: r.CompletedAtUnixNano, Netns: r.Netns, HostIP: r.HostIP, LeaseUID: r.LeaseUID, Paused: r.Paused}
}

func standardRebindAcrossRestart(t *testing.T, s standardRebindTestStore) {
	t.Helper()
	ctx := t.Context()
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	parent := g.Parent
	node, a := parent.Binding.NodeID, parent.Binding.Incarnation
	if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, node, 0, a); err != nil {
		t.Fatal(err)
	}

	b, epoch := restartStandardNode(t, s, parent.Binding)
	if _, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, freshCaptureRequest(req, parent.Binding.DeploymentID)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("restarted node captured without a re-bind: %v", err)
	}

	hop := ApplicationStandardRebind{InstanceID: ins.ID, NodeID: node, FromIncarnation: a, ToIncarnation: b, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	for name, mutate := range map[string]func(*ApplicationStandardRebind){
		"an earlier epoch":                func(r *ApplicationStandardRebind) { r.VMMDEpoch = epoch - 1 },
		"a process that is not current":   func(r *ApplicationStandardRebind) { r.ToIncarnation = uuid.NewString() },
		"a source that is not the holder": func(r *ApplicationStandardRebind) { r.FromIncarnation = uuid.NewString() },
		"another lease":                   func(r *ApplicationStandardRebind) { r.Observed.LeaseUID++ },
		"another namespace":               func(r *ApplicationStandardRebind) { r.Observed.Netns += "-x" },
		"another receipt":                 func(r *ApplicationStandardRebind) { r.Observed.Token = uuid.NewString() },
		"a paused claim":                  func(r *ApplicationStandardRebind) { r.Observed.Paused = true },
		"another instance":                func(r *ApplicationStandardRebind) { r.InstanceID = uuid.NewString() },
	} {
		bad := hop
		mutate(&bad)
		if err := s.RecordApplicationStandardRebind(ctx, bad); err == nil {
			t.Fatalf("re-bind from %s recorded", name)
		}
	}
	if err := s.RecordApplicationStandardRebind(ctx, ApplicationStandardRebind{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty re-bind: %v", err)
	}
	if err := s.RecordApplicationStandardRebind(ctx, hop); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordApplicationStandardRebind(ctx, hop); err != nil {
		t.Fatalf("retried re-bind: %v", err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, freshCaptureRequest(req, parent.Binding.DeploymentID)); err != nil {
		t.Fatalf("re-bound runtime cannot capture: %v", err)
	}
	if err := s.RecordApplicationStandardRebind(ctx, hop); err != nil {
		t.Fatalf("re-bind retried after use: %v", err)
	}

	// A second restart: the holder is b now, and only b may be the source.
	c, epoch := restartStandardNode(t, s, parent.Binding)
	if _, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, freshCaptureRequest(req, parent.Binding.DeploymentID)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("second restart captured without a re-bind: %v", err)
	}
	stale := ApplicationStandardRebind{InstanceID: ins.ID, NodeID: node, FromIncarnation: a, ToIncarnation: c, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	if err := s.RecordApplicationStandardRebind(ctx, stale); err == nil {
		t.Fatal("re-bind from a superseded holder recorded")
	}
	// An incarnation that died before its re-bind is skipped: d never held it.
	restartStandardNode(t, s, parent.Binding)
	e, epoch := restartStandardNode(t, s, parent.Binding)
	skip := ApplicationStandardRebind{InstanceID: ins.ID, NodeID: node, FromIncarnation: b, ToIncarnation: e, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	if err := s.RecordApplicationStandardRebind(ctx, skip); err != nil {
		t.Fatalf("re-bind across an incarnation that never held it: %v", err)
	}
	if _, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, freshCaptureRequest(req, parent.Binding.DeploymentID)); err != nil {
		t.Fatalf("re-bound twice, cannot capture: %v", err)
	}
	hops, err := s.ListApplicationStandardRebinds(ctx, ins.ID)
	if err != nil || len(hops) != 2 || hops[0].ToIncarnation != b || hops[1].FromIncarnation != b || hops[1].ToIncarnation != e || hops[1].CreatedAt.IsZero() {
		t.Fatalf("re-binds = %+v, %v", hops, err)
	}
}

// The publication guard follows the re-bind too: an admitted row returns to
// running after a restart only once its admission is re-bound.
func standardRebindPublication(t *testing.T, s standardRebindTestStore) {
	t.Helper()
	ctx := t.Context()
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	parent := g.Parent
	if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, parent.Binding.NodeID, 0, parent.Binding.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(StateSnapshotting), time.Now()); err != nil {
		t.Fatal(err)
	}
	b, epoch := restartStandardNode(t, s, parent.Binding)
	if err := s.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(StateRunning), time.Now()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("returned to running under a restarted node without a re-bind: %v", err)
	}
	// The re-bind needs a serving row; a capture that ends restores one.
	if err := s.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(StateFailed), time.Now()); err != nil {
		t.Fatal(err)
	}
	hop := ApplicationStandardRebind{InstanceID: ins.ID, NodeID: parent.Binding.NodeID, FromIncarnation: parent.Binding.Incarnation, ToIncarnation: b, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	if err := s.RecordApplicationStandardRebind(ctx, hop); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("re-bind for a row that no longer serves: %v", err)
	}
}

func TestMemStandardRebindAcrossRestart(t *testing.T) { standardRebindAcrossRestart(t, NewMemStore()) }
func TestMemStandardRebindPublication(t *testing.T)   { standardRebindPublication(t, NewMemStore()) }

// A re-bound warm admission is promoted by a grant naming the successor; one
// naming the predecessor is refused, and without the re-bind the parent is
// not eligible at all (slice 2).
func standardRebindPromotion(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	ctx := t.Context()
	rs, ok := s.(interface {
		ApplicationStandardRebindStore
		ComputeNodeVMMDEpochStore
	})
	if !ok {
		t.Fatal("store keeps no re-binds")
	}
	f, warm, parent := promotionTestParent(t, s)
	a := parent.Binding.Incarnation
	if _, err := rs.ClaimComputeNodeVMMDEpoch(ctx, f.nodeID, 0, a); err != nil {
		t.Fatal(err)
	}
	b := uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, runtimeadmission.Identity{ProtocolVersion: parent.Binding.ProtocolVersion, NodeID: f.nodeID, Incarnation: b}); err != nil {
		t.Fatal(err)
	}
	epoch, err := rs.ClaimComputeNodeVMMDEpoch(ctx, f.nodeID, 1, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardWarmParent(ctx, warm.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("warm parent eligible after a restart without a re-bind: %v", err)
	}
	hop := ApplicationStandardRebind{InstanceID: warm.ID, NodeID: f.nodeID, FromIncarnation: a, ToIncarnation: b, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	if err := rs.RecordApplicationStandardRebind(ctx, hop); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInstanceApplicationStandardWarmParent(ctx, warm.ID)
	if err != nil || !got.Equal(parent) {
		t.Fatalf("re-bound warm parent = %+v, %v", got, err)
	}
	if _, err := s.IssueInstanceApplicationStandardPromotion(ctx, promotionTestGrant(t, parent)); err == nil {
		t.Fatal("a promotion naming the predecessor was issued")
	}
	fresh := runtimeadmission.Promotion{Binding: parent.Binding, Parent: parent}
	now := time.Now()
	fresh.Binding.Token, fresh.Binding.Incarnation = uuid.NewString(), b
	fresh.Binding.IssuedAtUnixNano, fresh.Binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(time.Minute).UnixNano()
	fresh.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(fresh.ToProto())
	p, err := s.IssueInstanceApplicationStandardPromotion(ctx, fresh)
	if err != nil {
		t.Fatalf("successor's promotion: %v", err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	running, err := s.PublishInstanceApplicationStandardPromotion(ctx, r)
	if err != nil || running.State != string(StateRunning) {
		t.Fatalf("published re-bound promotion = %+v, %v", running, err)
	}
}

func TestMemStandardRebindPromotion(t *testing.T) { standardRebindPromotion(t, NewMemStore()) }

type standardObservationRebindTestStore interface {
	standardObservationTestStore
	ApplicationStandardRebindStore
	ComputeNodeVMMDEpochStore
	InstanceApplicationStandardRuntimeReceiptStore
}

// Observation follows the re-bind (open question 3): a re-bound receipt is
// current, and a capture the successor takes records it as the producer
// although the parent names the issuer, so the snapshot stays current while
// that process runs, also once the instance and its re-binds are gone.
// A producer that restarted is unavailable as before (ADR-595).
func standardObservationRebindSnapshots(t *testing.T, s standardObservationRebindTestStore) {
	ctx := t.Context()
	ins, req := standardSnapshotFixture(t, s, "warm")
	app, err := s.AppByID(ctx, ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	parent := g.Parent
	if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, parent.Binding.NodeID, 0, parent.Binding.Incarnation); err != nil {
		t.Fatal(err)
	}
	b, epoch := restartStandardNode(t, s, parent.Binding)
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("receipt current under a restarted node without a re-bind: %v", err)
	}
	hop := ApplicationStandardRebind{InstanceID: ins.ID, NodeID: parent.Binding.NodeID, FromIncarnation: parent.Binding.Incarnation, ToIncarnation: b, VMMDEpoch: epoch, Observed: rebindObserved(parent)}
	if err := s.RecordApplicationStandardRebind(ctx, hop); err != nil {
		t.Fatal(err)
	}
	if r, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID); err != nil || !r.Equal(parent) {
		t.Fatalf("re-bound receipt not current: %v", err)
	}
	g, err = s.IssueApplicationStandardSnapshotCapture(ctx, ins.State, freshCaptureRequest(req, parent.Binding.DeploymentID))
	if err != nil {
		t.Fatal(err)
	}
	ack := standardSnapshotAck(g)
	if err := s.PublishApplicationStandardSnapshotCapture(ctx, ack); err != nil {
		t.Fatal(err)
	}
	if got := standardSnapshotGet(t, s, g).ProducerIncarnation; got != b || g.Parent.Binding.Incarnation == b {
		t.Fatalf("producer = %q, want the successor %q", got, b)
	}
	if _, err := s.CreateSnapshot(ctx, standardSnapshotCacheRow(ins.DeploymentID, g, ack)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(ctx, ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(ctx, ins.ID); err != nil {
		t.Fatal(err)
	}
	// The parent names a process the node no longer runs; the producer is
	// the current one, and the instance's re-binds are gone.
	standardObservationLoadLogging(t, s, app)
	standardObservationAttempt(t, s, app, "")
}

func TestMemStandardObservationRebindSnapshots(t *testing.T) {
	standardObservationRebindSnapshots(t, NewMemStore())
}
