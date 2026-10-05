// adr: 593
package sched

// adr: 435, 581. Composed portable acceptance uses real state and scheduler
// contracts with explicitly simulated scanner, provider and native consumers.
// It does not certify physical ext4 composition, firewall rules or KVM effects.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type composedWaveStore interface {
	state.Store
	state.ApplicationStandardStore
	state.ApplicationStandardResourceStore
	state.ApplicationStandardReviewStore
	state.ApplicationStandardOperationStore
	state.ApplicationStandardMaterializationStore
	state.ApplicationStandardAutomaticMaterializationStore
	state.ApplicationStandardAutomaticObservationStore
	state.ApplicationStandardObservationStore
	state.ApplicationStandardEnrollmentStore
	state.ApplicationStandardConsumerRosterStore
	state.ApplicationStandardLogInventoryStore
	state.ApplicationStandardLogDeliveryStore
	state.ApplicationStandardLogHealthStore
	state.ApplicationStandardEgressStore
	state.ApplicationStandardRuntimeRefreshStore
	state.ComputeNodeRuntimeIdentityStore
	state.InstanceApplicationStandardAdmissionStore
	state.InstanceApplicationStandardRuntimeReceiptStore
	state.ApplicationStandardSnapshotCaptureStore
	state.BaseImageProducerStore
	state.BaseImageScanStore
	state.DeploymentRegistryVerificationStore
	state.DeploymentRegistryRootfsStore
	state.DeploymentArtifactScanStore
	state.DeploymentRuntimeProducerInputStore
	state.DeploymentRuntimeScanStore
}

type composedWaveFixture struct {
	s            composedWaveStore
	owner        state.CreateAccountWithPersonalOrgResult
	key          *ecdsa.PrivateKey
	publisher    api.ApplicationStandardPublisher
	destinations []state.ApplicationStandardLogDestination
	version      state.ApplicationStandardVersion
	assignmentID string
	apps         map[string]state.App
	deps         map[string]state.Deployment
	base         state.BaseImageProducer
	vmm          *composedWaveNativeVMM
	engine       *Engine
	sessions     map[string]state.ApplicationStandardLogConsumerSession
	events       map[string]int64
	refresh      func(*testing.T, state.ApplicationStandardRuntimeRefreshRequest)
}

func composedWaveJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newComposedWaveFixture(t *testing.T, s composedWaveStore) *composedWaveFixture {
	t.Helper()
	f := &composedWaveFixture{s: s, apps: map[string]state.App{}, deps: map[string]state.Deployment{}, sessions: map[string]state.ApplicationStandardLogConsumerSession{}, events: map[string]int64{}}
	var err error
	f.owner, err = s.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	f.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	f.publisher, err = s.CreateApplicationStandardPublisher(t.Context(), state.ApplicationStandardPublisherCreate{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Name: "Company CI", PublicKeyDER: der})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"https://logs.example.com/v1", "https://logs.example.com/v2"} {
		d, err := s.CreateApplicationStandardLogDestination(t.Context(), state.ApplicationStandardLogDestinationCreate{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Name: target, Kind: "http_json", TargetURL: target, AuthHeaderSealed: []byte("sealed-portable-fixture")})
		if err != nil {
			t.Fatal(err)
		}
		f.destinations = append(f.destinations, d)
	}
	f.publish(t, 0, 0)
	op := f.reviewAndApprove(t, 1, 0, 0)
	f.assignmentID = op.AssignmentID
	op = f.materialize(t, op.ID)
	if op.State != "completed" || len(op.Targets) != 0 {
		t.Fatal("initial empty admission did not finish", op)
	}
	f.publishBase(t)
	f.vmm = newComposedWaveNativeVMM(t, s)
	f.engine = newEngine(t, s, f.vmm, &fakeNotifier{}, "1.10.0")
	return f
}

func (f *composedWaveFixture) publish(t *testing.T, expected int64, index int) {
	t.Helper()
	cidr := []string{"8.8.8.0/24", "1.1.1.0/24"}[index]
	definition := map[string]any{}
	for field, value := range map[string]any{"log_destinations": []string{f.destinations[index].ID}, "trusted_publishers": []string{f.publisher.ID}, "require_signed": true, "security_policy": "enforce", "egress_cidrs": []string{cidr}, "egress_extra_ports": []int{8443}} {
		definition[field] = map[string]any{"mode": "mandatory", "value": value}
	}
	v, err := f.s.PublishApplicationStandardVersion(t.Context(), state.ApplicationStandardPublish{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Slug: "composed-company", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: expected, Definition: composedWaveJSON(t, definition)}})
	if err != nil {
		t.Fatal(err)
	}
	f.version = v
}

func (f *composedWaveFixture) reviewAndApprove(t *testing.T, version, revision int64, targets int) state.ApplicationStandardOperation {
	t.Helper()
	p, err := f.s.PreviewApplicationStandardAssignment(t.Context(), f.owner.PersonalOrg.ID, f.owner.Account.ID, state.ApplicationStandardReviewRequest{AssignmentID: f.assignmentID, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: version, ExpectedRevision: revision, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 || len(p.Applications) != targets {
		t.Fatalf("review: targets=%d blockers=%+v error=%v", len(p.Applications), p.Blockers, err)
	}
	for _, app := range p.Applications {
		current, err := f.s.GetApplicationStandardEnrollment(t.Context(), p.OrgID, app.AppID)
		if err != nil || current.DesiredRevision != revision || current.ObservedRevision != revision {
			t.Fatal("preview changed an existing service", current, err)
		}
	}
	op, err := f.s.ApproveApplicationStandardReview(t.Context(), p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash)
	if err != nil || len(op.Targets) != targets {
		t.Fatal("approve", err)
	}
	for _, target := range op.Targets {
		current, err := f.s.GetApplicationStandardEnrollment(t.Context(), p.OrgID, target.AppID)
		if err != nil || current.DesiredRevision != revision || target.State != "queued" {
			t.Fatal("approval installed an existing service", target, current, err)
		}
	}
	return op
}

func (f *composedWaveFixture) work(t *testing.T, id string, observe bool) state.ApplicationStandardOperation {
	t.Helper()
	c, err := f.s.ClaimApplicationStandardOperation(t.Context(), "composed-wave-"+uuid.NewString())
	if err != nil || c.OperationID != id {
		t.Fatal("claim", c, err)
	}
	var op state.ApplicationStandardOperation
	if observe {
		op, err = f.s.ObserveApplicationStandardOperation(t.Context(), c)
	} else {
		op, err = f.s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	}
	if err != nil {
		t.Fatal("work", err)
	}
	for _, target := range op.Targets {
		t.Logf("observe=%t wave=%d state=%s revision=%d reason=%s", observe, target.Position, target.State, target.DesiredRevision, target.ErrorCode)
	}
	if op.State == "running" {
		if err := f.s.ReleaseApplicationStandardOperationWorker(t.Context(), c); err != nil {
			t.Fatal("release", err)
		}
	}
	return op
}

func (f *composedWaveFixture) materialize(t *testing.T, id string) state.ApplicationStandardOperation {
	t.Helper()
	return f.work(t, id, false)
}

func (f *composedWaveFixture) onboard(t *testing.T) {
	t.Helper()
	for _, slug := range []string{"composed-a", "composed-b", "composed-c"} {
		app, err := f.s.CreateApp(t.Context(), state.App{AccountID: f.owner.Account.ID, OrgID: f.owner.PersonalOrg.ID, Slug: slug, RAMMB: 128, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage}); !errors.Is(err, state.ErrApplicationStandardsPending) {
			t.Fatal("onboarding admitted an uninstalled standard", err)
		}
		claim, err := f.s.ClaimApplicationStandardEnrollment(t.Context(), "composed-onboard")
		if err != nil {
			t.Fatal(err)
		}
		e, err := f.s.MaterializeApplicationStandardEnrollment(t.Context(), claim)
		if err != nil || e.PersistedRevision != 1 || e.ObservedRevision != 0 {
			t.Fatal("onboarding invented observation", e, err)
		}
		app, err = f.s.AppByID(t.Context(), app.ID)
		if err != nil || !app.RequireSigned || app.SecurityPolicy != api.AppSecurityPolicyEnforce {
			t.Fatal("company security was not inherited", err)
		}
		id := uuid.MustParse(app.ID).String()
		f.apps[id] = app
		f.deps[id] = f.publishDeployment(t, app)
		if _, err := f.engine.Wake(t.Context(), app.ID, f.deps[id].ID, "", TriggerGateway); err != nil {
			t.Fatal("native admitted onboarding", err)
		}
		f.assertRuntime(t, id, 1)
		request, err := f.s.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.refreshRuntime(t, request)
	}
	f.consume(t)
	seen := map[string]bool{}
	for range f.apps {
		claim, err := f.s.ClaimApplicationStandardObservation(t.Context(), "composed-onboarding-observer")
		if err != nil || seen[claim.AppID] {
			t.Fatal("automatic observer did not visit each service", claim, err)
		}
		e, err := f.s.ObserveApplicationStandardEnrollment(t.Context(), claim)
		if err != nil || e.State != "observed" || e.ObservedRevision != 1 {
			t.Fatal("composed onboarding did not qualify", e, err)
		}
		seen[claim.AppID] = true
	}
}

func (f *composedWaveFixture) assertWave(t *testing.T, op state.ApplicationStandardOperation, observed, persisted int) {
	t.Helper()
	for i, target := range op.Targets {
		want := "queued"
		if i < observed {
			want = "observed"
		} else if i < persisted {
			want = "persisted"
		}
		if target.State != want {
			t.Fatalf("wave %d: state=%s reason=%s want=%s", i, target.State, target.ErrorCode, want)
		}
	}
}

func (f *composedWaveFixture) replace(t *testing.T, id string, revision int64) {
	t.Helper()
	old := f.assertRuntime(t, id, revision-1)
	request, err := f.s.GetApplicationStandardRuntimeRefresh(t.Context(), f.owner.PersonalOrg.ID, id)
	if err != nil || request.Standard.DesiredRevision != revision {
		t.Fatal("missing durable replacement request", request, err)
	}
	f.refreshRuntime(t, request)
	current := f.assertRuntime(t, id, revision)
	stopped, err := f.s.InstanceByID(t.Context(), old.ID)
	if err != nil || old.ID == current.ID || stopped.State != string(state.StateStopped) {
		t.Fatal("replacement did not retire the old process", stopped, err)
	}
	e, err := f.s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, id)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("runtime handoff fabricated full observation", e, err)
	}
	boots := f.vmm.coldBoots
	f.refreshRuntime(t, request)
	if f.vmm.coldBoots != boots {
		t.Fatal("durable replay unnecessarily replaced a current runtime")
	}
}

func (f *composedWaveFixture) refreshRuntime(t *testing.T, request state.ApplicationStandardRuntimeRefreshRequest) {
	t.Helper()
	if f.refresh != nil {
		f.refresh(t, request)
		return
	}
	if _, err := f.engine.RefreshApplicationStandard(t.Context(), request); err != nil {
		t.Fatal("replace current standard runtime", err)
	}
}

func exerciseApplicationStandardComposedWaves(t *testing.T, s composedWaveStore, configure ...func(*composedWaveFixture)) {
	t.Helper()
	f := newComposedWaveFixture(t, s)
	for _, setup := range configure {
		setup(f)
	}
	f.onboard(t)
	f.publish(t, 1, 1)
	op := f.reviewAndApprove(t, 2, 1, 3)
	op = f.materialize(t, op.ID)
	f.assertWave(t, op, 0, 1)
	op = f.work(t, op.ID, true)
	if op.Targets[0].ErrorCode == "" {
		t.Fatal("configuration alone qualified the first wave")
	}
	f.assertWave(t, f.materialize(t, op.ID), 0, 1)
	f.replace(t, op.Targets[0].AppID, 2)
	f.consume(t)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 1, 1)
	// A new gateway process invalidates its predecessor's loaded inventory and
	// provider report. The saved first-wave bit must not admit the second wave.
	f.restartLogging(t)
	op = f.materialize(t, op.ID)
	f.assertWave(t, op, 0, 1)
	if op.Targets[0].ErrorCode != "logging_inventory_pending" {
		t.Fatal("gateway restart was not visible", op.Targets[0])
	}
	f.consume(t)
	// Recovered receipts do not change the checkpoint by themselves: the
	// owning observer must qualify them before another wave can be installed.
	f.assertWave(t, f.materialize(t, op.ID), 0, 1)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 1, 1)
	op = f.materialize(t, op.ID)
	f.assertWave(t, op, 1, 2)
	f.replace(t, op.Targets[1].AppID, 2)
	f.consume(t)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 2, 2)
	// A newer failed scan supersedes historical success while producer inputs
	// remain stable. Later waves require a fresh successful composed report.
	f.publishComposedScan(t, op.Targets[0].AppID, true)
	op = f.materialize(t, op.ID)
	if op.Targets[0].State != "persisted" || op.Targets[0].ErrorCode == "" || op.Targets[2].State != "queued" {
		t.Fatal("failed rescan opened the last wave", op)
	}
	f.publishComposedScan(t, op.Targets[0].AppID, false)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 2, 2)
	op = f.materialize(t, op.ID)
	f.assertWave(t, op, 2, 3)
	f.replace(t, op.Targets[2].AppID, 2)
	f.consume(t)
	op = f.work(t, op.ID, true)
	f.assertWave(t, op, 3, 3)
	if op.State != "completed" {
		t.Fatal("all qualified services did not finish", op)
	}
	f.assertAdoption(t, 2, 2)
	f.rollback(t)
}

func (f *composedWaveFixture) assertAdoption(t *testing.T, version, revision int64) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for id := range f.apps {
		e, err := f.s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, id)
		if err != nil || len(e.Adoptions) != 1 || e.Adoptions[0].Version != version || e.DesiredRevision != revision || e.PersistedRevision != revision || e.ObservedRevision != revision || e.State != "observed" {
			t.Fatal("service lost its reviewed adoption or observation", e, err)
		}
		app, err := f.s.AppByID(t.Context(), id)
		cidr := []string{"8.8.8.0/24", "1.1.1.0/24"}[version-1]
		if err != nil || !app.RequireSigned || app.SecurityPolicy != api.AppSecurityPolicyEnforce || len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != cidr || len(app.EgressPorts) != 1 || app.EgressPorts[0] != 8443 {
			t.Fatal("service controls differ from its reviewed version", id, err)
		}
		drains, err := f.s.ListAppLogDrainsForApp(t.Context(), app.ID)
		if err != nil || len(drains) != 1 || !drains[0].Enabled || drains[0].TargetURL != f.destinations[version-1].TargetURL {
			t.Fatal("service log destination differs from its reviewed version", id, err)
		}
		signers, err := f.s.ListAppTrustedSignersForApp(t.Context(), app.ID)
		if err != nil || len(signers) != 1 || !bytes.Equal(signers[0].CosignPublicKey, der) {
			t.Fatal("service publisher differs from the inherited company key", id, err)
		}
	}
}

func (f *composedWaveFixture) rollback(t *testing.T) {
	t.Helper()
	op := f.reviewAndApprove(t, 1, 2, 3)
	for i := range op.Targets {
		op = f.materialize(t, op.ID)
		f.assertWave(t, op, i, i+1)
		f.replace(t, op.Targets[i].AppID, 3)
		f.consume(t)
		op = f.work(t, op.ID, true)
		f.assertWave(t, op, i+1, i+1)
	}
	if op.State != "completed" || f.vmm.coldBoots != 9 || f.vmm.destroys != 6 {
		t.Fatalf("rollback did not converge through replacement: state=%s boots=%d destroys=%d", op.State, f.vmm.coldBoots, f.vmm.destroys)
	}
	f.assertAdoption(t, 1, 3)
}

func TestMemApplicationStandardComposedWaves(t *testing.T) {
	exerciseApplicationStandardComposedWaves(t, state.NewMemStore())
}
