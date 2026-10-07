package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardOperationTestStore interface {
	standardReviewTestStore
	ApplicationStandardOperationStore
}

func TestMemApplicationStandardApprovalLegacyAliases(t *testing.T) {
	m := NewMemStore()
	f := newStandardApprovalFixture(t, m)
	ctx := context.Background()
	o, err := m.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	operation := m.applicationStandardOperations[o.ID]
	operation.State = "completed"
	m.applicationStandardOperations[o.ID] = operation
	prior := m.applicationStandardAssignments[o.AssignmentID]
	delete(m.applicationStandardAssignments, o.AssignmentID)
	alias := strings.ToUpper(strings.ReplaceAll(o.AssignmentID, "-", ""))
	prior.ID = alias
	m.applicationStandardAssignments[alias] = prior
	m.mu.Unlock()
	r := f.plan.Request
	r.AssignmentID, r.ExpectedRevision, r.Active = alias, 1, false
	p, err := m.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.applicationStandardAssignments[alias]
	if len(m.applicationStandardAssignments) != 1 || a.ID != alias || a.StandardID != prior.StandardID || a.ScopeID != prior.ScopeID || a.Revision != 2 || a.Active || a.CreatedBy != prior.CreatedBy || !a.CreatedAt.Equal(prior.CreatedAt) {
		t.Fatalf("legacy identity lost on approval: %+v", a)
	}
}

func TestMemApplicationStandardApprovalExpired(t *testing.T) {
	m := NewMemStore()
	standardApprovalExpired(t, m, func(p ApplicationStandardReviewPlan) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.applicationStandardReviewPlans[p.ID] = cloneStandardReviewPlan(p)
	})
}

func standardApprovalExpired(t *testing.T, s standardOperationTestStore, seed func(ApplicationStandardReviewPlan)) {
	t.Helper()
	f := newStandardApprovalFixture(t, s)
	p := f.plan
	p.ID, p.CreatedAt, p.ExpiresAt = uuid.NewString(), time.Now().UTC().Add(-2*api.ApplicationStandardReviewTTL), time.Now().UTC().Add(-api.ApplicationStandardReviewTTL)
	p.approvalInputs.PlanID = p.ID
	var err error
	p.ApprovalHash, err = appStandardReviewProofHash(p)
	if err != nil {
		t.Fatal(err)
	}
	seed(p)
	if _, err := s.ApproveApplicationStandardReview(context.Background(), p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewExpired) {
		t.Fatalf("expired review approved: %v", err)
	}
	assignments, err := s.ListApplicationStandardAssignments(context.Background(), p.OrgID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("expired review activated admission: %+v %v", assignments, err)
	}
}

func TestMemApplicationStandardReviewRetainedArtifacts(t *testing.T) {
	standardReviewRetainedArtifacts(t, NewMemStore())
}

func standardReviewRetainedArtifacts(t *testing.T, s standardOperationTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.apps[0].ID, Kind: DeploymentKindImage, Status: DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, dep.ID, DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "standard-review-fixture", TargetURL: "unix:///tmp/standards-review-fixture", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, f.apps[0].ID, dep.ID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	check := func(want error) {
		t.Helper()
		_, err := s.ValidateApplicationStandardReview(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
		if !errors.Is(err, want) {
			t.Fatalf("retained artifact freshness got %v want %v", err, want)
		}
	}
	check(nil)
	if err := s.UpdateInstanceState(ctx, instance.ID, "running"); err != nil {
		t.Fatal(err)
	}
	check(ErrApplicationStandardReviewStale)
	if err := s.UpdateInstanceState(ctx, instance.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	check(nil)
}

type standardApprovalFixture struct {
	owner   CreateAccountWithPersonalOrgResult
	apps    []App
	version ApplicationStandardVersion
	plan    ApplicationStandardReviewPlan
}

func newStandardApprovalFixture(t *testing.T, s standardOperationTestStore) standardApprovalFixture {
	t.Helper()
	ctx := context.Background()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "approval-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	f := standardApprovalFixture{owner: owner}
	for _, slug := range []string{"approval-a", "approval-b"} {
		app, err := s.CreateApp(ctx, App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: slug, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		f.apps = append(f.apps, app)
	}
	f.version, err = s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "approval-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	f.plan, err = s.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(f.plan.Blockers) != 0 {
		t.Fatalf("approval fixture: %+v %v", f.plan, err)
	}
	return f
}

func TestMemApplicationStandardApprovalLifecycle(t *testing.T) {
	m := NewMemStore()
	standardApprovalLifecycle(t, m, func(id string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		o := m.applicationStandardOperations[id]
		o.State = "completed" // Fixture only; no consumer convergence is claimed.
		m.applicationStandardOperations[id] = o
	})
}

func standardApprovalLifecycle(t *testing.T, s standardOperationTestStore, finishFixture func(string)) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	orgID, actorID := f.owner.PersonalOrg.ID, f.owner.Account.ID
	o, err := s.ApproveApplicationStandardReview(ctx, strings.ToUpper(orgID), strings.ReplaceAll(actorID, "-", ""), f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	if o.State != "queued" || o.PlanID != f.plan.ID || o.ApprovedBy != canonicalStandardUUID(actorID) || o.BatchSize != 1 || len(o.Targets) != 2 {
		t.Fatalf("approved operation: %+v", o)
	}
	for i, target := range o.Targets {
		if target.Position != i || target.State != "queued" || target.DesiredRevision != 0 || target.ApprovedApp.DesiredRevision != 1 || target.approvalInput.SnapshotHash == "" || target.approvalInput.AppID != target.AppID || target.ApprovedApp.AfterAdoptions[0].Version != 1 {
			t.Fatalf("frozen target: %+v", target)
		}
	}
	assignments, err := s.ListApplicationStandardAssignments(ctx, orgID)
	if err != nil || len(assignments) != 1 || assignments[0].ID != f.plan.Request.AssignmentID || assignments[0].AdmissionVersion != 1 {
		t.Fatalf("admission: %+v %v", assignments, err)
	}
	for _, app := range f.apps {
		e, err := s.GetApplicationStandardEnrollment(ctx, orgID, app.ID)
		if err != nil || len(e.Adoptions) != 0 || e.State != "unmanaged" || e.DesiredRevision != 1 || e.PersistedRevision != 0 || e.ObservedRevision != 0 {
			t.Fatalf("approval moved an existing adoption: %+v %v", e, err)
		}
	}
	create := func(slug string, expectedVersion int64) App {
		t.Helper()
		app, err := s.CreateApp(ctx, App{AccountID: actorID, OrgID: orgID, Slug: slug, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		e, err := s.GetApplicationStandardEnrollment(ctx, orgID, app.ID)
		if err != nil || len(e.Adoptions) != 1 || e.Adoptions[0].Version != expectedVersion || e.State != "pending" || e.ObservedRevision != 0 {
			t.Fatalf("new admission: %+v %v", e, err)
		}
		if _, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployPending}); !errors.Is(err, ErrApplicationStandardsPending) {
			t.Fatalf("pending service bypassed enrollment: %v", err)
		}
		return app
	}
	newV1 := create("during-first-rollout", 1)
	// The returned proof/projection cannot mutate history. Idempotent retries
	// work despite later membership changes and do not restart the operation.
	o.Targets[0].ApprovedApp.Effective.Values[appstandards.SecurityPolicy][0] = 'X'
	o.Targets[0].approvalInput.BeforeAdoptions = []appstandards.Adoption{{AssignmentID: uuid.NewString(), Version: 99}}
	got, err := s.GetApplicationStandardOperation(ctx, orgID, o.ID)
	if err != nil || string(got.Targets[0].ApprovedApp.Effective.Values[appstandards.SecurityPolicy]) != `"warn"` || len(got.Targets[0].approvalInput.BeforeAdoptions) != 0 {
		t.Fatalf("aliased operation: %+v %v", got, err)
	}
	retry, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil || retry.ID != o.ID || len(retry.Targets) != 2 || !retry.CreatedAt.Equal(o.CreatedAt) || !retry.Targets[0].UpdatedAt.Equal(o.Targets[0].UpdatedAt) {
		t.Fatalf("idempotent approval: %+v %v", retry, err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, f.plan.ID, strings.Repeat("0", 64)); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("wrong replay hash: %v", err)
	}
	if _, err := s.GetApplicationStandardOperation(ctx, uuid.NewString(), o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign operation read: %v", err)
	}
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "approval-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, foreign.Account.ID, f.plan.ID, f.plan.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("nonmember replay: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, actorID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, f.plan.ID, f.plan.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("suspended actor replay: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, actorID, AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: orgID, ActorID: actorID, Slug: f.version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"off"}}`)}}); err != nil {
		t.Fatal(err)
	}
	r := f.plan.Request
	r.AdmissionVersion, r.ExpectedRevision = 2, 1
	preview := func() ApplicationStandardReviewPlan {
		t.Helper()
		p, err := s.PreviewApplicationStandardAssignment(ctx, orgID, actorID, r)
		if err != nil || len(p.Blockers) != 0 {
			t.Fatalf("next review: %+v %v", p, err)
		}
		return p
	}
	p := preview()
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardOperationInProgress) {
		t.Fatalf("overlapping operation: %v", err)
	}
	assignments, _ = s.ListApplicationStandardAssignments(ctx, orgID)
	if assignments[0].AdmissionVersion != 1 {
		t.Fatal("rejected overlap changed admission")
	}
	finishFixture(o.ID)
	second, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, p.ID, p.ApprovalHash)
	if err != nil || len(second.Targets) != 3 {
		t.Fatalf("second version approval: %+v %v", second, err)
	}
	newV2 := create("during-second-rollout", 2)
	e, _ := s.GetApplicationStandardEnrollment(ctx, orgID, newV1.ID)
	if e.Adoptions[0].Version != 1 {
		t.Fatal("new pointer moved an unvisited adoption")
	}
	finishFixture(second.ID)
	r.Active, r.ExpectedRevision = false, 2
	p = preview()
	removed, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, p.ID, p.ApprovalHash)
	if err != nil || removed.State != "queued" {
		t.Fatalf("reviewed removal: %+v %v", removed, err)
	}
	assignments, _ = s.ListApplicationStandardAssignments(ctx, orgID)
	if len(assignments) != 0 {
		t.Fatal("removed assignment still admits new apps")
	}
	for _, a := range []App{newV1, newV2} {
		e, _ := s.GetApplicationStandardEnrollment(ctx, orgID, a.ID)
		if len(e.Adoptions) != 1 {
			t.Fatal("disabling admission prematurely removed existing adoption")
		}
	}
	if slices.ContainsFunc(removed.Targets, func(t ApplicationStandardOperationTarget) bool { return len(t.ApprovedApp.AfterAdoptions) != 0 }) {
		t.Fatal("removal target retains assignment")
	}
	app, err := s.CreateApp(ctx, App{AccountID: actorID, OrgID: orgID, Slug: "after-removal", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetApplicationStandardEnrollment(ctx, orgID, app.ID)
	if len(e.Adoptions) != 0 || e.State != "unmanaged" {
		t.Fatalf("removed admission enrolled app: %+v", e)
	}
}

func TestMemApplicationStandardApprovalRejectsChangedInputs(t *testing.T) {
	m := NewMemStore()
	standardApprovalRejectsChangedInputs(t, m, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.applicationStandardOperations) != 0 || len(m.applicationStandardAssignments) != 0 || len(m.auditLog) != 0 {
			t.Fatal("rejected approval wrote intent or audit")
		}
	})
}

func standardApprovalRejectsChangedInputs(t *testing.T, s standardOperationTestStore, assertNoWrites func()) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	orgID, actorID := f.owner.PersonalOrg.ID, f.owner.Account.ID
	p := f.plan
	refresh := func() {
		t.Helper()
		var err error
		p, err = s.PreviewApplicationStandardAssignment(ctx, orgID, actorID, p.Request)
		if err != nil {
			t.Fatal(err)
		}
	}
	reject := func(want error) {
		t.Helper()
		if _, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, p.ID, p.ApprovalHash); !errors.Is(err, want) {
			t.Fatalf("approval got %v want %v", err, want)
		}
		assertNoWrites()
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, actorID, p.ID, ""); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("empty hash: %v", err)
	}
	assertNoWrites()
	if _, err := s.UpdateApp(ctx, f.apps[0].ID, UpdateAppParams{SetEgressPorts: true, EgressPorts: []int{8443}}); err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	refresh()
	drain, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: f.apps[0].ID, AccountID: actorID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://legacy.example/logs?token=secret", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	refresh()
	url := "https://changed.example/logs"
	if _, err := s.UpdateAppLogDrain(ctx, drain.ID, UpdateAppLogDrainParams{TargetURL: &url}); err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	refresh()
	if _, err := s.CreateDeployment(ctx, Deployment{AppID: f.apps[0].ID, Kind: DeploymentKindImage, Status: DeployPending}); err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	refresh()
	if err := s.UpdateAccountPlan(ctx, actorID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	if err := s.UpdateAccountPlan(ctx, actorID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	refresh()
	if _, err := s.CreateApp(ctx, App{AccountID: actorID, OrgID: orgID, Slug: "new-approval-member", RAMMB: 128}); err != nil {
		t.Fatal(err)
	}
	reject(ErrApplicationStandardReviewStale)
	refresh()
	// A legal standard definition can still be impossible on the platform.
	v, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: orgID, ActorID: actorID, Slug: "blocked-approval", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_extra_ports":{"mode":"mandatory","value":[25]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.PreviewApplicationStandardAssignment(ctx, orgID, actorID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: orgID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) == 0 {
		t.Fatalf("blocked review: %+v %v", p, err)
	}
	reject(ErrApplicationStandardReviewBlocked)
	foreign, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "approval-reader@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddOrgMember(ctx, orgID, foreign.Account.ID, OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, orgID, foreign.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("viewer approval: %v", err)
	}
	assertNoWrites()
}
