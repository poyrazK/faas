package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRosterTestStore interface {
	standardLogClosureTestStore
	ComputeNodeRuntimeIdentityStore
}

func standardRosterRead(t *testing.T, s ApplicationStandardConsumerRosterStore, f standardLocalIntentFixture) ApplicationStandardConsumerRoster {
	t.Helper()
	r, err := s.GetApplicationStandardConsumerRoster(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || !sameStandardUUID(r.AppID, f.app.ID) || !sameStandardUUID(r.OrgID, f.owner.PersonalOrg.ID) || r.Nodes == nil || r.LiveInstances == nil || r.ReadAt.IsZero() || r.ReadAt.After(time.Now()) {
		t.Fatalf("roster: %+v %v", r, err)
	}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), "tcp://") || strings.Contains(string(encoded), "unix://") || strings.Contains(string(encoded), "https://") || strings.Contains(string(encoded), "sealed-") {
		t.Fatal("roster exposed consumer configuration")
	}
	return r
}

func standardRosterNode(t *testing.T, r ApplicationStandardConsumerRoster, id string) ApplicationStandardConsumerNode {
	t.Helper()
	for _, n := range r.Nodes {
		if sameStandardUUID(n.NodeID, id) {
			return n
		}
	}
	t.Fatalf("required node %s omitted: %+v", id, r.Nodes)
	return ApplicationStandardConsumerNode{}
}

func TestMemApplicationStandardConsumerRoster(t *testing.T) {
	standardConsumerRosterLifecycle(t, NewMemStore())
}

func standardConsumerRosterLifecycle(t *testing.T, s standardRosterTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	n := standardLogInventoryNode(t, s)
	initial := standardRosterRead(t, s, f)
	node := standardRosterNode(t, initial, n.ID)
	if !initial.EnrollmentCurrent || !node.Present || !node.LoggingRequired || node.NativeRequired || node.LoggingSession != nil || node.NativeIncarnation != "" || node.NativeProtocol != 0 {
		t.Fatal("absent consumer/capability disappeared or became evidence")
	}
	if initial.Fingerprint() != standardRosterRead(t, s, f).Fingerprint() {
		t.Fatal("read clock changed roster fingerprint")
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.app.ID, Kind: DeploymentKindImage, Status: DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, f.app.ID, dep.ID, "waking", 128, n.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	placed := standardRosterRead(t, s, f)
	if len(placed.LiveInstances) != 1 || !sameStandardUUID(placed.LiveInstances[0].InstanceID, ins.ID) || !standardRosterNode(t, placed, n.ID).NativeRequired || placed.Fingerprint() == initial.Fingerprint() {
		t.Fatal("placement missing from authoritative obligations")
	}
	c := standardLogInventorySession(t, s, n.ID)
	native := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, native); err != nil {
		t.Fatal(err)
	}
	ready := standardRosterRead(t, s, f)
	node = standardRosterNode(t, ready, n.ID)
	if node.LoggingSession == nil || *node.LoggingSession != c || node.NativeIncarnation != native.Incarnation || node.NativeProtocol != native.ProtocolVersion || ready.Fingerprint() == placed.Fingerprint() {
		t.Fatal("roster omitted current startup/capability identity")
	}
	if err := s.SetComputeNodeActive(ctx, n.ID, false); err != nil {
		t.Fatal(err)
	}
	draining := standardRosterRead(t, s, f)
	node = standardRosterNode(t, draining, n.ID)
	if node.Active || !node.NativeRequired || !node.LoggingRequired {
		t.Fatal("placement drain removed a live consumer obligation")
	}
	closure, err := s.CloseApplicationStandardLogConsumer(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	closed := standardRosterRead(t, s, f)
	node = standardRosterNode(t, closed, n.ID)
	if node.LoggingStoppedAt == nil || !node.LoggingStoppedAt.Equal(closure.StoppedAt) || !node.LoggingRequired || closed.Fingerprint() == draining.Fingerprint() {
		t.Fatal("shutdown was hidden or removed an obligation")
	}
	standardConsumerRosterRefusalsAndPending(t, s, f, closed)
}

func standardConsumerRosterRefusalsAndPending(t *testing.T, s standardRosterTestStore, f standardLocalIntentFixture, old ApplicationStandardConsumerRoster) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.GetApplicationStandardConsumerRoster(ctx, uuid.NewString(), f.app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-organization roster exposed")
	}
	if _, err := s.GetApplicationStandardConsumerRoster(ctx, "bad", f.app.ID); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalid roster input accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.GetApplicationStandardConsumerRoster(canceled, f.owner.PersonalOrg.ID, f.app.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled roster read: %v", err)
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{f.extra.ID}}); err != nil {
		t.Fatal(err)
	}
	pending := standardRosterRead(t, s, f)
	if pending.EnrollmentCurrent || pending.DesiredRevision == old.DesiredRevision || len(pending.LiveInstances) != len(old.LiveInstances) || pending.Fingerprint() == old.Fingerprint() {
		t.Fatal("pending intent hid obligations or remained current")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("roster read advanced whole-application observation")
	}
}

func TestMemApplicationStandardConsumerRosterStatesMissingNodesAndOwnership(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	m.mu.Lock()
	for id, n := range m.computeNodes {
		role := "control-plane"
		n.Role = &role
		n.Active = false
		n.Lifecycle = NodeLifecycleRetired
		n.GatewayTargetURL = nil
		m.computeNodes[id] = n
	}
	m.mu.Unlock()
	empty := standardRosterRead(t, m, f)
	if len(empty.Nodes) != 0 || len(empty.LiveInstances) != 0 {
		t.Fatal("unexpected fleet before registration")
	}
	cp := "control-plane"
	target := "tcp://192.0.2.8:8080"
	m.mu.Lock()
	retired, gateway, missing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	m.computeNodes[retired] = ComputeNode{ID: retired, Role: &cp, Lifecycle: NodeLifecycleRetired}
	m.computeNodes[gateway] = ComputeNode{ID: gateway, GatewayTargetURL: &target, Lifecycle: NodeLifecycleRetired, LastHeartbeatAt: time.Now().Add(-time.Hour)}
	states := []string{"waking", "cold_booting", "running", "snapshotting", "migrating", "warm", "draining", "parked", "stopped", "failed"}
	for _, state := range states {
		id := uuid.NewString()
		node := retired
		if state == "warm" {
			node = missing
		}
		m.instances[id] = Instance{ID: id, AppID: f.app.ID, State: state, NodeID: node}
	}
	m.mu.Unlock()
	r := standardRosterRead(t, m, f)
	if len(r.LiveInstances) != 7 {
		t.Fatalf("live obligations: %+v", r.LiveInstances)
	}
	node := standardRosterNode(t, r, retired)
	if !node.NativeRequired || node.LoggingRequired || node.Role != cp || node.Lifecycle != NodeLifecycleRetired {
		t.Fatal("unexpected live placement on control plane/retired node disappeared")
	}
	node = standardRosterNode(t, r, missing)
	if node.Present || !node.NativeRequired {
		t.Fatal("missing node was silently treated as quiescent")
	}
	node = standardRosterNode(t, r, gateway)
	if !node.LoggingRequired || !node.GatewayConfigured || node.HeartbeatFresh {
		t.Fatal("retired endpoint was silently treated as stopped")
	}
}

func TestMemApplicationStandardConsumerRosterCopyAndCancellation(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	c := standardLogInventorySession(t, m, standardLogInventoryNode(t, m).ID)
	if _, err := m.CloseApplicationStandardLogConsumer(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	first := standardRosterRead(t, m, f)
	fingerprint := first.Fingerprint()
	for n := range first.Nodes {
		if first.Nodes[n].LoggingSession != nil {
			first.Nodes[n].LoggingSession.SessionID = uuid.NewString()
			*first.Nodes[n].LoggingStoppedAt = time.Time{}
		}
	}
	if standardRosterRead(t, m, f).Fingerprint() != fingerprint {
		t.Fatal("returned pointers mutated store-owned identity/closure")
	}
	ctx, cancel := context.WithCancel(t.Context())
	entered, finished := make(chan struct{}), make(chan error, 1)
	m.mu.Lock()
	go func() {
		close(entered)
		_, err := m.GetApplicationStandardConsumerRoster(ctx, f.owner.PersonalOrg.ID, f.app.ID)
		finished <- err
	}()
	<-entered
	cancel()
	m.mu.Unlock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled mutex wait returned roster")
	}
}

func TestMemApplicationStandardConsumerRosterInactiveLoggingOnly(t *testing.T) {
	standardConsumerRosterInactiveLoggingOnly(t, NewMemStore())
}

func standardConsumerRosterInactiveLoggingOnly(t *testing.T, s standardRosterTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(t.Context(), t, s)
	registered := standardLogInventoryNode(t, s)
	c := standardLogInventorySession(t, s, registered.ID)
	unknown := standardLogInventoryNode(t, s)
	for _, n := range []ComputeNode{registered, unknown} {
		if err := s.SetComputeNodeActive(t.Context(), n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	r := standardRosterRead(t, s, f)
	for _, id := range []string{registered.ID, unknown.ID} {
		n := standardRosterNode(t, r, id)
		if n.Active || n.GatewayConfigured || n.NativeRequired || !n.LoggingRequired {
			t.Fatal("inactive logging-only node disappeared")
		}
	}
	n := standardRosterNode(t, r, registered.ID)
	if n.LoggingSession == nil || *n.LoggingSession != c || n.LoggingStoppedAt != nil {
		t.Fatal("open registered startup disappeared")
	}
	n = standardRosterNode(t, r, unknown.ID)
	if n.LoggingSession != nil {
		t.Fatal("missing startup became reporting evidence")
	}
	closure, err := s.CloseApplicationStandardLogConsumer(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	after := standardRosterRead(t, s, f)
	n = standardRosterNode(t, after, registered.ID)
	if !n.LoggingRequired || n.LoggingStoppedAt == nil || !n.LoggingStoppedAt.Equal(closure.StoppedAt) || after.Fingerprint() == r.Fingerprint() {
		t.Fatal("closure removed platform membership")
	}
}

func TestMemApplicationStandardConsumerRosterRoleChangeRetainsRegisteredLogger(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	c := standardLogInventorySession(t, m, standardLogInventoryNode(t, m).ID)
	m.mu.Lock()
	for id, n := range m.computeNodes {
		if sameStandardUUID(id, c.NodeID) {
			role := "control-plane"
			n.Role = &role
			n.Lifecycle = NodeLifecycleRetired
			n.Active = false
			m.computeNodes[id] = n
		}
	}
	m.mu.Unlock()
	r := standardRosterRead(t, m, f)
	n := standardRosterNode(t, r, c.NodeID)
	if n.Role != "control-plane" || !n.LoggingRequired || n.NativeRequired || n.LoggingSession == nil || *n.LoggingSession != c {
		t.Fatal("role change removed registered logging obligation")
	}
	if _, err := m.CloseApplicationStandardLogConsumer(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if standardRosterNode(t, standardRosterRead(t, m, f), c.NodeID).LoggingStoppedAt == nil {
		t.Fatal("changed-role shutdown disappeared")
	}
}
