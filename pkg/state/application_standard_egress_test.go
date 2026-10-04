package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"strings"
	"testing"
	"time"
)

type standardEgressTestStore interface {
	Store
	standardLocalIntentTestStore
	ApplicationStandardEgressStore
	ComputeNodeRuntimeIdentityStore
}

type standardEgressFixture struct {
	local    standardLocalIntentFixture
	node     ComputeNode
	instance Instance
	target   ApplicationStandardEgressTarget
}

func newStandardEgressFixture(t *testing.T, s standardEgressTestStore) standardEgressFixture {
	t.Helper()
	ctx := t.Context()
	f := standardEgressFixture{local: newStandardLocalIntentFixture(ctx, t, s)}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.local.app.OrgID, f.local.app.AccountID, f.local.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.local.enrollment.DesiredRevision, Settings: json.RawMessage(`{"egress_cidrs":["8.8.8.0/24"],"egress_extra_ports":[8443]}`)}); err != nil {
		t.Fatal(err)
	}
	f.local.enrollment = standardLocalIntentMaterialize(ctx, t, s, f.local)
	var err error
	f.node, err = s.CreateComputeNode(ctx, ComputeNode{Name: "egress-consumer-" + uuid.NewString(), TargetURL: "unix:///tmp/standards-egress-consumer", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.local.app.ID, Kind: DeploymentKindImage, Status: DeployLive, ImageDigest: "sha256:egress-storage-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	f.instance, err = s.CreateInstance(ctx, f.local.app.ID, dep.ID, string(StateColdBooting), 128, f.node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListPendingApplicationStandardEgress(ctx, f.local.app.ID)
	if err != nil || len(pending) != 1 || pending[0].valid() {
		t.Fatalf("missing native identity did not remain pending: %+v %v", pending, err)
	}
	i := runtimeadmission.Identity{NodeID: f.node.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, i); err != nil {
		t.Fatal(err)
	}
	f.target = standardEgressPending(t, s, f.local.app.ID)
	for _, alias := range []string{strings.ToUpper(f.local.app.ID), strings.ReplaceAll(f.local.app.ID, "-", "")} {
		rows, err := s.ListPendingApplicationStandardEgress(ctx, alias)
		if err != nil || len(rows) != 1 || !sameStandardEgress(rows[0], f.target) {
			t.Fatalf("UUID alias lost repair target: %+v %v", rows, err)
		}
	}
	return f
}

func standardEgressPending(t *testing.T, s ApplicationStandardEgressStore, app string) ApplicationStandardEgressTarget {
	t.Helper()
	rows, err := s.ListPendingApplicationStandardEgress(t.Context(), app)
	if err != nil || len(rows) != 1 || !rows[0].valid() {
		t.Fatalf("pending target: %+v %v", rows, err)
	}
	return rows[0]
}

// Explicit simulated consumer for storage tests; no VM or firewall is asserted.
func standardEgressReceipt(t ApplicationStandardEgressTarget) runtimeadmission.EgressReceipt {
	return runtimeadmission.EgressReceipt{Identity: t.Identity, AppID: t.AppID, Revision: t.Policy.Revision, PolicyHash: t.PolicyHash}
}

func standardEgressWrite(t *testing.T, s standardEgressTestStore, target ApplicationStandardEgressTarget) {
	t.Helper()
	before := time.Now().Add(-time.Second)
	o, err := s.RecordApplicationStandardEgress(t.Context(), target, standardEgressReceipt(target))
	if err != nil || !sameStandardEgress(o.Target, target) || o.Receipt.Check(target.Identity, target.Policy) != nil || o.ObservedAt.Before(before) || o.ObservedAt.After(time.Now()) {
		t.Fatalf("record %+v: %v", o, err)
	}
	rows, err := s.ListApplicationStandardEgress(t.Context(), target.OrgID, target.AppID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("current fact: %+v %v", rows, err)
	}
	pending, err := s.ListPendingApplicationStandardEgress(t.Context(), target.AppID)
	if err != nil || len(pending) != 0 {
		t.Fatal("current acknowledgment not retained", err)
	}
	enrollment, err := s.GetApplicationStandardEnrollment(t.Context(), target.OrgID, target.AppID)
	if err != nil || enrollment.ObservedRevision != 0 || enrollment.State != "persisted" {
		t.Fatal("one consumer advanced application observation", err)
	}
	o.Target.Policy.Ports[0] = 9443
	rows[0].Target.Policy.Ports[0] = 9443
	fresh, err := s.ListApplicationStandardEgress(t.Context(), target.OrgID, target.AppID)
	if err != nil || !sameStandardEgress(fresh[0].Target, target) {
		t.Fatal("returned fact aliases stored input", err)
	}
}

func standardEgressLifecycle(t *testing.T, s standardEgressTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardEgressFixture(t, s)
	old := f.target
	r := standardEgressReceipt(old)
	r.PolicyHash = ""
	if _, err := s.RecordApplicationStandardEgress(ctx, old, r); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unbound acknowledgment accepted", err)
	}
	standardEgressWrite(t, s, old)
	if _, err := s.ListApplicationStandardEgress(ctx, uuid.NewString(), old.AppID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross org fact exposed", err)
	}
	next := old.Identity
	next.Incarnation = uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, old, standardEgressReceipt(old)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("late pre-restart acknowledgment accepted", err)
	}
	rows, err := s.ListApplicationStandardEgress(ctx, old.OrgID, old.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("restart inherited old observation", err)
	}
	f.target = standardEgressPending(t, s, old.AppID)
	standardEgressWrite(t, s, f.target)
	if _, err := s.SetApplicationStandardLocalIntent(ctx, old.OrgID, f.local.owner.Account.ID, old.AppID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.local.enrollment.DesiredRevision, Settings: json.RawMessage(`{"egress_cidrs":["8.8.8.0/24"],"egress_extra_ports":[9443]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, f.target, standardEgressReceipt(f.target)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("pending intent accepted stale receipt", err)
	}
	f.local.enrollment = standardLocalIntentMaterialize(ctx, t, s, f.local)
	nextTarget := standardEgressPending(t, s, old.AppID)
	if nextTarget.PolicyHash == f.target.PolicyHash || nextTarget.DesiredRevision == f.target.DesiredRevision {
		t.Fatal("updated policy did not change binding")
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, f.target, standardEgressReceipt(f.target)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("old policy acknowledged new projection", err)
	}
	standardEgressWrite(t, s, nextTarget)
	if err := s.SetComputeNodeActive(ctx, f.node.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, nextTarget, standardEgressReceipt(nextTarget)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("inactive node established convergence", err)
	}
	if err := s.SetComputeNodeActive(ctx, f.node.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(ctx, f.instance.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListApplicationStandardEgress(ctx, old.OrgID, old.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("non-serving node supplied current fact", err)
	}
	if _, err := s.RecordApplicationStandardEgress(ctx, nextTarget, standardEgressReceipt(nextTarget)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("non-serving node recorded fact", err)
	}
	if err := s.DeleteComputeNode(ctx, f.node.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemApplicationStandardEgress(t *testing.T) { standardEgressLifecycle(t, NewMemStore()) }

func TestMemApplicationStandardEgressFreshnessAndCancellation(t *testing.T) {
	m := NewMemStore()
	f := newStandardEgressFixture(t, m)
	standardEgressWrite(t, m, f.target)
	m.mu.Lock()
	for key, o := range m.applicationStandardEgressObservations {
		o.ObservedAt = time.Now().Add(-time.Hour)
		m.applicationStandardEgressObservations[key] = o
	}
	m.mu.Unlock()
	rows, err := m.ListApplicationStandardEgress(t.Context(), f.target.OrgID, f.target.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("old fact supplied liveness", err)
	}
	standardEgressPending(t, m, f.target.AppID)
	ctx, cancel := context.WithCancel(t.Context())
	entered, done := make(chan struct{}), make(chan error, 1)
	m.mu.Lock()
	go func() {
		close(entered)
		_, err := m.RecordApplicationStandardEgress(ctx, f.target, standardEgressReceipt(f.target))
		done <- err
	}()
	<-entered
	cancel()
	m.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled wait recorded fact", err)
	}
}

func standardEgressExceptionDeadline(t *testing.T, s interface {
	standardEgressTestStore
	ApplicationStandardExceptionStore
}) {
	t.Helper()
	ctx := t.Context()
	f := newStandardEgressFixture(t, s)
	request := standardExceptionRequest(f.local)
	request.ExpiresAt = time.Now().UTC().Add(3 * time.Second)
	exception, err := s.ApproveApplicationStandardException(ctx, f.target.OrgID, f.local.owner.Account.ID, f.target.AppID, request)
	if err != nil {
		t.Fatal(err)
	}
	f.local.enrollment = standardLocalIntentMaterialize(ctx, t, s, f.local)
	current := standardEgressPending(t, s, f.target.AppID)
	standardEgressWrite(t, s, current)
	waitStandardExceptionExpiry(t, exception)
	if _, err := s.RecordApplicationStandardEgress(ctx, current, standardEgressReceipt(current)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired exception established egress convergence", err)
	}
	rows, err := s.ListApplicationStandardEgress(ctx, current.OrgID, current.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("expired exception supplied current egress evidence", err)
	}
}

func TestMemApplicationStandardEgressExceptionDeadline(t *testing.T) {
	standardEgressExceptionDeadline(t, NewMemStore())
}
