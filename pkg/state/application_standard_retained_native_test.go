package state

// These tests simulate native receipts. They do not prove KVM or fleet convergence.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type retainedNativeTestStore interface {
	standardRuntimeQualificationTestStore
	ApplicationStandardOperationControlStore
}

func uninstalledNativeScopeFixture(t *testing.T, s retainedNativeTestStore, move func(App, Project) error) {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: "uninstalled-" + uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateProject(t.Context(), Project{AccountID: owner.Account.ID, Slug: "assigned-scope"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateProject(t.Context(), Project{AccountID: owner.Account.ID, Slug: "unassigned-scope"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "uninstalled-scope", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, ApplicationStandardReviewRequest{Scope: "project", ScopeID: first.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 || len(p.Applications) != 0 {
		t.Fatalf("empty-scope preview: %+v %v", p, err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), p.OrgID, owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: first.ID, Slug: "uninstalled-scope", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || before.State != "pending" || before.PersistedRevision != 0 || len(before.Adoptions) != 1 {
		t.Fatalf("fixture installed a standard before scope repair: %+v %v", before, err)
	}
	if err := move(app, second); err != nil {
		t.Fatal(err)
	}
	app.ProjectID = second.ID
	after, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || after.State != "unmanaged" || after.PersistedRevision != 0 || len(after.Adoptions) != 0 || !ApplicationStandardEnrollmentPermitsRuntime(app, after) {
		t.Fatalf("never-installed scope gained retained native history: %+v %v", after, err)
	}
}

func TestMemApplicationStandardUninstalledNativeScope(t *testing.T) {
	m := NewMemStore()
	uninstalledNativeScopeFixture(t, m, func(app App, project Project) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		app.ProjectID = project.ID
		if err := m.initializeApplicationStandardEnrollmentLocked(app); err != nil {
			return err
		}
		m.apps[app.ID] = app
		return nil
	})
}

func removeLastNativeStandard(t *testing.T, s retainedNativeTestStore, app App) ApplicationStandardEnrollment {
	t.Helper()
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "retained-removal-prepare")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.GetApplicationStandardOperation(t.Context(), app.OrgID, c.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort); err != nil {
		t.Fatal(err)
	}
	assignments, err := s.ListApplicationStandardAssignments(t.Context(), app.OrgID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("fixture assignment: %+v %v", assignments, err)
	}
	a := assignments[0]
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, ApplicationStandardReviewRequest{AssignmentID: a.ID, ExpectedRevision: 1, Scope: a.Scope, ScopeID: a.ScopeID, StandardID: a.StandardID, AdmissionVersion: a.AdmissionVersion, Active: false, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("removal preview: %+v %v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err = s.ClaimApplicationStandardOperation(t.Context(), "retained-removal-install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || len(e.Adoptions) != 0 || len(e.MaterializedFields) != 0 || e.State != "persisted" || e.PersistedRevision != e.DesiredRevision || e.ObservedRevision != 0 {
		t.Fatalf("removed projection: %+v %v", e, err)
	}
	return e
}

func retainedNativeFixture(t *testing.T, s retainedNativeTestStore) (App, Deployment, ApplicationStandardEnrollment) {
	t.Helper()
	old, receipt := issueConsumedNativeFixture(t, s)
	history, err := s.GetInstanceApplicationStandardAdmission(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.AppByID(t.Context(), old.AppID)
	if err != nil {
		t.Fatal(err)
	}
	e := removeLastNativeStandard(t, s, app)
	assertStandardCaptureHistory(t, s, history)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), old.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old controls published after removal: %v", err)
	}
	assertNativeBootUnpublished(t, s, old)
	if err := s.UpdateInstanceStateToTerminal(t.Context(), old.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	app, err = s.AppByID(t.Context(), old.AppID)
	if err != nil || len(app.EgressAllowlist) != 0 {
		t.Fatalf("baseline controls were not restored: %+v %v", app, err)
	}
	dep, err := s.DeploymentByID(t.Context(), old.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, e
}

func retainedNativeAdmissionLifecycle(t *testing.T, s retainedNativeTestStore) {
	t.Helper()
	app, dep, enrollment := retainedNativeFixture(t, s)
	if !ApplicationStandardEnrollmentPermitsRuntime(app, enrollment) {
		t.Fatal("reviewed removal could not boot to obtain its observations")
	}
	scan, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	publishNativeComposedScan(t, s, app, dep, &scan.Input.Reports[0].Report)
	ins, capture := createRuntimeArtifactCapture(t, s, app, dep)
	if !capture.Managed || capture.ArtifactInputHash == "" || capture.PersistedRevision != enrollment.PersistedRevision {
		t.Fatalf("last removal erased native admission: %+v", capture)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateRunning)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("legacy publication bypassed retained admission: %v", err)
	}
	grant := retainedConsumedGrant(t, s, ins, capture)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err == nil {
		t.Fatal("unmeasured receipt published removed standard")
	}
	ins, err = s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, consumedNativeReceipt(grant, capture))
	if err != nil {
		t.Fatal(err)
	}
	standardRuntimeQualificationRead(t, s, ins, "")
	if err := s.UpdateAccountPlan(t.Context(), app.AccountID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	standardRuntimeQualificationRead(t, s, ins, "runtime_inputs_stale")
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateWarm)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("retained admission regained legacy plan compatibility: %v", err)
	}
}

func retainedConsumedGrant(t *testing.T, s retainedNativeTestStore, ins Instance, capture InstanceApplicationStandardAdmission) runtimeadmission.Binding {
	t.Helper()
	b := runtimeCaptureTestBinding(t, s, ins)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("legacy artifact protocol gained retained authority: %v", err)
	}
	b.ProtocolVersion, b.Incarnation = runtimeadmission.ArtifactProtocolVersion, uuid.NewString()
	var err error
	b.ArtifactSourcesHash, err = standardCapturedArtifactSourceHash(capture)
	if err != nil {
		t.Fatal(err)
	}
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	b, err = s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMemApplicationStandardRetainedNativeAdmission(t *testing.T) {
	retainedNativeAdmissionLifecycle(t, NewMemStore())
}

func TestMemApplicationStandardRetainedNativeReenrollment(t *testing.T) {
	m := NewMemStore()
	app, _, before := retainedNativeFixture(t, m)
	project, err := m.CreateProject(t.Context(), Project{AccountID: app.AccountID, Slug: "retained-project"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	app.ProjectID = project.ID
	err = m.initializeApplicationStandardEnrollmentLocked(app)
	if err == nil {
		m.apps[app.ID] = app
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || after.State != "pending" || after.DesiredRevision != before.DesiredRevision+1 || after.PersistedRevision != before.PersistedRevision || after.EffectiveHash != before.EffectiveHash || !standardEnrollmentRequiresNative(after) || ApplicationStandardEnrollmentPermitsRuntime(app, after) {
		t.Fatalf("scope change erased native history or permitted stale inputs: %+v %v", after, err)
	}
}
