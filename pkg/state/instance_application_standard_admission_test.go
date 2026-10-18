package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRuntimeCaptureTestStore interface {
	Store
	ApplicationStandardStore
	ApplicationStandardReviewStore
	ApplicationStandardOperationStore
	ApplicationStandardMaterializationStore
	ApplicationStandardAutomaticMaterializationStore
	ApplicationStandardEnrollmentStore
	InstanceApplicationStandardAdmissionStore
	InstanceApplicationStandardBootStore
	InstanceApplicationStandardPromotionStore
	ComputeNodeRuntimeIdentityStore
}

type runtimeCaptureFixture struct {
	owner  CreateAccountWithPersonalOrgResult
	app    App
	dep    Deployment
	nodeID string
}

func newRuntimeCaptureFixture(t *testing.T, s standardRuntimeCaptureTestStore, managed bool) runtimeCaptureFixture {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "capture-" + uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		v, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "runtime-capture", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
		if err != nil {
			t.Fatal(err)
		}
		p, err := s.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
		if err != nil || len(p.Blockers) != 0 {
			t.Fatalf("preview: %+v %v", p.Blockers, err)
		}
		if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
			t.Fatal(err)
		}
		c, err := s.ClaimApplicationStandardOperation(ctx, "runtime-capture-empty-approval")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.MaterializeNextApplicationStandardTarget(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	app, err := s.CreateApp(ctx, App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "capture-app", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if errors.Is(err, ErrNotFound) {
		node, err = s.UpsertComputeNode(ctx, ComputeNode{Name: DefaultLocalNodeName, TargetURL: "unix:///tmp/capture-vmmd.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	}
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		if _, err := s.CreateInstance(ctx, app.ID, "", string(StateWaking), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardsPending) {
			t.Fatalf("raw admission bypassed pending: %v", err)
		}
		c, err := s.ClaimApplicationStandardEnrollment(ctx, "runtime-capture-onboarding")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.MaterializeApplicationStandardEnrollment(ctx, c); err != nil {
			t.Fatal(err)
		}
		app, err = s.AppByID(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, ImageDigest: "sha256:runtime-capture"})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeCaptureFixture{owner: owner, app: app, dep: dep, nodeID: node.ID}
}

func TestMemInstanceApplicationStandardCapture(t *testing.T) {
	standardRuntimeCaptureLifecycle(t, NewMemStore())
}

func standardRuntimeCaptureLifecycle(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	drain, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: f.app.ID, AccountID: f.app.AccountID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://secret-capture.example.com/path", AuthHeaderSealed: []byte("private-capture-ciphertext"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstanceWithMode(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString(), string(InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	capture, err := s.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil || capture.DesiredRevision != 1 || capture.PersistedRevision != 1 || len(capture.InputHash) != 64 || capture.EffectiveHash == "" || capture.CapturedAt.IsZero() {
		t.Fatalf("capture=%+v err=%v", capture, err)
	}
	if strings.Contains(string(capture.inputs), drain.TargetURL) || strings.Contains(string(capture.inputs), string(drain.AuthHeaderSealed)) {
		t.Fatal("capture exposed log credentials or URL")
	}
	if err := CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, f.app, f.owner.Account, f.dep); err != nil {
		t.Fatalf("fresh scheduler inputs: %v", err)
	}
	old := f.app
	old.RequireSigned = !old.RequireSigned
	if err := CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, old, f.owner.Account, f.dep); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old cached app accepted: %v", err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, ins.ID, string(StateColdBooting), "fc-capture", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("legacy publication acquired native authority: %v", err)
	}
	publishRuntimeCaptureTestReceipt(t, s, ins, StateRunning, "fc-capture", "10.100.0.8", 20008)
	e, err := s.GetApplicationStandardEnrollment(ctx, f.app.OrgID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 || e.State != "persisted" {
		t.Fatalf("capture fabricated observation: %+v %v", e, err)
	}
	// The getter cannot mutate immutable history through a shared byte slice.
	capture.inputs[0] = 'x'
	after, err := s.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil || capture.InputHash != after.InputHash {
		t.Fatal("capture was mutable through its reader")
	}
	for _, st := range []State{StateRunning, StateWarm, StateMigrating} {
		if _, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(st), 128, f.nodeID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("direct %s insert bypassed boot capture: %v", st, err)
		}
	}
}

func TestMemInstanceApplicationStandardReenrollmentDuringBoot(t *testing.T) {
	standardRuntimeReenrollmentDuringBoot(t, NewMemStore())
}

func standardRuntimeReenrollmentDuringBoot(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, f.app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreApp(ctx, f.app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, ins.ID, string(StateColdBooting), "stale", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("pending published runtime: %v", err)
	}
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "runtime-capture-restored")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func() error{
		func() error {
			_, err := s.PublishInstanceRuntime(ctx, ins.ID, string(StateColdBooting), "stale", "10.100.0.8", 20008)
			return err
		},
		func() error { return s.SetInstanceRuntime(ctx, ins.ID, "stale", "10.100.0.8", 20008) },
		func() error { return s.UpdateInstanceState(ctx, ins.ID, string(StateRunning)) },
		func() error { return s.UpdateInstanceStateIf(ctx, ins.ID, string(StateColdBooting), string(StateWarm)) },
		func() error { return s.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(StateRunning), time.Now()) },
	} {
		if err := mutate(); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
			t.Fatalf("old capture published after repair: %v", err)
		}
	}
	actual, err := s.InstanceByID(ctx, ins.ID)
	if err != nil || actual.State != string(StateColdBooting) || actual.HostIP != "" || actual.Netns != "" {
		t.Fatalf("failed publication left partial runtime: %+v %v", actual, err)
	}
	if err := s.UpdateInstanceStateToTerminal(ctx, ins.ID, string(StateFailed), time.Now()); err != nil {
		t.Fatalf("cleanup fenced: %v", err)
	}
	if err := s.DeleteInstance(ctx, ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardAdmission(ctx, ins.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("capture outlived instance: %v", err)
	}
	fresh, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	publishRuntimeCaptureTestReceipt(t, s, fresh, StateRunning, "fresh", "10.100.0.9", 20009)
}

// This is an explicit simulated native consumer for storage contract tests.
// It does not claim that a VM booted, nor acknowledge any policy observation.
func runtimeCaptureTestBinding(t *testing.T, s standardRuntimeCaptureTestStore, ins Instance) runtimeadmission.Binding {
	t.Helper()
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: ins.NodeID, Incarnation: uuid.NewString()}
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: ins.ID, AppID: capture.AppID, DeploymentID: capture.DeploymentID, AccountID: capture.AccountID, NodeID: capture.NodeID, Incarnation: identity.Incarnation, DesiredRevision: capture.DesiredRevision, EffectiveHash: capture.EffectiveHash, CapturedInputHash: capture.NativeInputHash, EgressRevision: capture.EgressRevision, PayloadHash: strings.Repeat("a", 64), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()}
}

func publishRuntimeCaptureTestReceipt(t *testing.T, s standardRuntimeCaptureTestStore, ins Instance, next State, netns, hostIP string, uid int32) {
	t.Helper()
	binding, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, runtimeCaptureTestBinding(t, s, ins))
	if err != nil {
		capture, _ := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
		t.Fatalf("issue simulated native grant: capture=%+v: %v", capture, err)
	}
	receipt := runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("b", 64), Netns: netns, HostIP: hostIP, LeaseUID: uid, Paused: next == StateWarm, CompletedAtUnixNano: time.Now().UnixNano()}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, next, receipt); err != nil {
		t.Fatalf("simulated native publication denied: %v", err)
	}
}

func TestMemInstanceApplicationStandardArtifactChangeDuringBoot(t *testing.T) {
	standardRuntimeArtifactChangeDuringBoot(t, NewMemStore())
}

func TestMemInstanceApplicationStandardLoggingChangeDuringBoot(t *testing.T) {
	standardRuntimeLoggingChangeDuringBoot(t, NewMemStore())
}

func standardRuntimeLoggingChangeDuringBoot(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	drain, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: f.app.ID, AccountID: f.app.AccountID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://capture-logs.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := s.UpdateAppLogDrain(ctx, drain.ID, UpdateAppLogDrainParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, ins.ID, string(StateColdBooting), "stale", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("changed logging configuration published runtime: %v", err)
	}
}

func TestMemInstanceApplicationStandardAccountGrace(t *testing.T) {
	standardRuntimeAccountGrace(t, NewMemStore())
}

func standardRuntimeAccountGrace(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	if err := s.UpdateAccountStatus(t.Context(), f.app.AccountID, AccountPastDue); err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatalf("billing grace denied serving: %v", err)
	}
	if err := s.UpdateAccountStatus(t.Context(), f.app.AccountID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), ins.ID, string(StateColdBooting), "suspended", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("suspended account published runtime: %v", err)
	}
}

func TestInstanceApplicationStandardCaptureDigestKeepsIntegerPrecision(t *testing.T) {
	a, err := decodeInstanceStandardAdmission("instance", []byte(`{"desired_revision":9007199254740992}`), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := decodeInstanceStandardAdmission("instance", []byte(`{"desired_revision":9007199254740993}`), time.Time{})
	if err != nil || a.InputHash == b.InputHash {
		t.Fatalf("different revisions have the same digest: %v", err)
	}
}

func standardRuntimeArtifactChangeDuringBoot(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, f.dep.ID, "/changed.ext4", "layers/changed.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(ctx, ins.ID, string(StateColdBooting), "stale", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("changed artifact published runtime: %v", err)
	}
}
