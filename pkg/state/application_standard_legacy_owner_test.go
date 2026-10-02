package state

// adr: 430. Raw legacy model fixtures prove scope/enrollment boundaries only.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type legacyStandardOwnerTestStore interface {
	Store
	ApplicationStandardStore
	ApplicationStandardEnrollmentStore
	ApplicationStandardReviewStore
	InstanceApplicationStandardAdmissionStore
	AccountAbuseHoldStore
}

type legacyOwnerWrites struct {
	create func(App) (App, error)
	attach func(string, string) error
	seed   func(appstandards.Assignment, string) error
	erase  func(string) error
}

func legacyOwnerFixture(t *testing.T, s legacyStandardOwnerTestStore) (CreateAccountWithPersonalOrgResult, Project, appstandards.Assignment) {
	t.Helper()
	owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: "legacy-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(t.Context(), Project{AccountID: owner.Account.ID, Slug: "legacy-owner-project"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "owner-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return owner, project, appstandards.Assignment{ID: uuid.NewString(), OrgID: owner.PersonalOrg.ID, Scope: "project", ScopeID: project.ID, StandardID: version.StandardID, AdmissionVersion: 1}
}

func legacyOwnerProjectReviewAndRestore(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites) {
	owner, project, assignment := legacyOwnerFixture(t, s)
	app, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "unowned-project-member", RAMMB: 128, Status: AppActive})
	if err != nil || app.OrgID != "" {
		t.Fatalf("legacy raw insertion lost unowned compatibility: %v", err)
	}
	request := ApplicationStandardReviewRequest{Scope: "project", ScopeID: project.ID, StandardID: assignment.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
	if _, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("review authorized an unowned project member: %v", err)
	}
	if err := s.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, request); err != nil {
		t.Fatalf("unowned tombstone prevented review of current project: %v", err)
	}
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unowned tombstone bypassed assigned scope on restore: %v", err)
	}
	if _, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "new-unowned-member", RAMMB: 128, Status: AppActive}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unowned insertion bypassed assigned project: %v", err)
	}
	stored, err := s.AppByID(t.Context(), app.ID)
	if err != nil || stored.Status != AppDeleted || stored.OrgID != "" {
		t.Fatalf("refused restore changed the tombstone: %v", err)
	}
}

func legacyOwnerAttachmentCapturesStandards(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites) {
	owner, _, assignment := legacyOwnerFixture(t, s)
	assignment.Scope, assignment.ScopeID = "organization", owner.PersonalOrg.ID
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, Slug: "legacy-attach", RAMMB: 128, Status: AppActive})
	if err != nil || legacy.OrgID != "" {
		t.Fatalf("unowned app outside assigned scopes was rejected: %v", err)
	}
	if _, err := s.GetApplicationStandardEnrollment(t.Context(), owner.PersonalOrg.ID, legacy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy insertion invented an enrollment owner: %v", err)
	}
	if err := writes.attach(legacy.ID, owner.PersonalOrg.ID); err != nil {
		t.Fatal(err)
	}
	value, err := s.GetApplicationStandardEnrollment(t.Context(), owner.PersonalOrg.ID, legacy.ID)
	if err != nil || value.State != "pending" || value.DesiredRevision != 1 || value.PersistedRevision != 0 || value.ObservedRevision != 0 || len(value.Adoptions) != 1 || value.Adoptions[0].AssignmentID != assignment.ID {
		t.Fatalf("verified owner attachment did not capture admission pins: %+v %v", value, err)
	}
	if _, err := s.CreateDeployment(t.Context(), Deployment{AppID: legacy.ID, Kind: DeploymentKindImage, Status: DeployPending}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("owner attachment admitted deployment before installation: %v", err)
	}
	if err := writes.attach(legacy.ID, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("owned app cleared ownership to evade standards: %v", err)
	}
	again, err := s.GetApplicationStandardEnrollment(t.Context(), owner.PersonalOrg.ID, legacy.ID)
	if err != nil || again.DesiredRevision != value.DesiredRevision || again.Adoptions[0].AssignmentID != assignment.ID {
		t.Fatalf("refused owner removal changed captured adoption: %v", err)
	}
}

func memoryLegacyOwnerWrites(m *MemStore) legacyOwnerWrites {
	return legacyOwnerWrites{
		create: func(app App) (App, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			app.CreatedAt, app.SecurityPolicy = time.Now().UTC(), api.AppSecurityPolicyOff
			if err := m.initializeApplicationStandardEnrollmentLocked(app); err != nil {
				return App{}, err
			}
			m.apps[app.ID] = app
			return app, nil
		},
		attach: func(appID, orgID string) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			app := m.apps[appID]
			app.OrgID = orgID
			if err := m.initializeApplicationStandardEnrollmentLocked(app); err != nil {
				return err
			}
			m.apps[appID] = app
			return nil
		},
		seed: func(a appstandards.Assignment, actor string) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.applicationStandardAssignments == nil {
				m.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{}
			}
			m.applicationStandardAssignments[a.ID] = applicationStandardAssignmentRecord{Assignment: a, Active: true, Revision: 1, CreatedBy: actor}
			return nil
		},
		erase: func(appID string) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.apps, appID)
			delete(m.applicationStandardEnrollments, appID)
			return nil
		},
	}
}

func TestMemLegacyStandardOwnerProjectReviewAndRestore(t *testing.T) {
	m := NewMemStore()
	legacyOwnerProjectReviewAndRestore(t, m, memoryLegacyOwnerWrites(m))
}

func TestMemLegacyStandardOwnerAttachment(t *testing.T) {
	m := NewMemStore()
	legacyOwnerAttachmentCapturesStandards(t, m, memoryLegacyOwnerWrites(m))
}

func legacyOwnerUnmanagedRemoval(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites) {
	owner, _, _ := legacyOwnerFixture(t, s)
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "owned-unmanaged", RAMMB: 128, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || before.State != "unmanaged" {
		t.Fatalf("fixture unexpectedly adopted a standard: %v", err)
	}
	if err := writes.attach(app.ID, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unmanaged owned app cleared its organization: %v", err)
	}
	again, err := s.AppByID(t.Context(), app.ID)
	if err != nil || again.OrgID != app.OrgID {
		t.Fatalf("refused removal changed app owner: %v", err)
	}
	enrollment, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || enrollment.DesiredRevision != before.DesiredRevision || enrollment.State != before.State {
		t.Fatalf("refused removal changed enrollment: %v", err)
	}
}

func TestMemLegacyStandardOwnerUnmanagedRemoval(t *testing.T) {
	m := NewMemStore()
	legacyOwnerUnmanagedRemoval(t, m, memoryLegacyOwnerWrites(m))
}

func legacyOwnerRetainedApplicationScope(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites) {
	owner, _, assignment := legacyOwnerFixture(t, s)
	app, err := s.CreateApp(t.Context(), App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "retained-app-scope", RAMMB: 128, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	assignment.Scope, assignment.ScopeID = "application", app.ID
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	if err := writes.erase(app.ID); err != nil {
		t.Fatal(err)
	}
	// Scope assignments retain their identity through parent app erasure.
	// Reusing that UUID cannot create an unowned exemption from the assignment.
	if _, err := writes.create(App{ID: app.ID, AccountID: owner.Account.ID, Slug: "unowned-reused-scope", RAMMB: 128, Status: AppActive}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unowned app identity bypassed retained application scope: %v", err)
	}
}

func TestMemLegacyStandardOwnerRetainedApplicationScope(t *testing.T) {
	m := NewMemStore()
	legacyOwnerRetainedApplicationScope(t, m, memoryLegacyOwnerWrites(m))
}

func legacyOwnerRuntimeCompatibility(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites, checkUnowned func(App, Instance)) {
	t.Helper()
	owner, _, assignment := legacyOwnerFixture(t, s)
	assignment.Scope, assignment.ScopeID = "organization", owner.PersonalOrg.ID
	if err := writes.seed(assignment, owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	app, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, Slug: "legacy-runtime", RAMMB: 128, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "legacy-owner-node", TargetURL: "unix:///tmp/legacy-owner.sock", VPCPUs: 4, VCPUBudget: api.VCPUSlots, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, ImageDigest: "sha256:legacy-owner"})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("unowned legacy runtime failed: %v", err)
	}
	if _, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unowned legacy runtime invented an admission capture: %v", err)
	}
	checkUnowned(app, ins)
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateRunning)); err != nil {
		t.Fatalf("unowned legacy residency failed: %v", err)
	}
	if err := writes.attach(app.ID, owner.PersonalOrg.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateRunning)); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("new company owner escaped runtime enrollment: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateParked)); err != nil {
		t.Fatalf("pending standards prevented nonresident cleanup: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateWaking)); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("new company owner escaped wake enrollment: %v", err)
	}
	if _, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner attachment fabricated historical admission: %v", err)
	}
}

func TestMemLegacyStandardOwnerRuntimeCompatibility(t *testing.T) {
	m := NewMemStore()
	legacyOwnerRuntimeCompatibility(t, m, memoryLegacyOwnerWrites(m), func(app App, ins Instance) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, err := m.standardRuntimeSnapshotLocked(ins); !errors.Is(err, ErrApplicationStandardsPending) {
			t.Fatalf("unowned app acquired runtime authority: %v", err)
		}
		// Retained company intent, even if a corrupt fixture loses ownership,
		// prevents the unowned compatibility path from granting residency.
		if m.applicationStandardEnrollments == nil {
			m.applicationStandardEnrollments = map[string]ApplicationStandardEnrollment{}
		}
		m.applicationStandardEnrollments[app.ID] = ApplicationStandardEnrollment{AppID: app.ID, OrgID: uuid.NewString(), State: "pending"}
		if err := m.guardInstanceStandardRuntimeLocked(ins, false); !errors.Is(err, ErrApplicationStandardsPending) {
			t.Fatalf("retained company intent bypassed runtime guard: %v", err)
		}
		delete(m.applicationStandardEnrollments, app.ID)
	})
}

func legacyOwnerRuntimeEligibility(t *testing.T, s legacyStandardOwnerTestStore, writes legacyOwnerWrites) {
	t.Helper()
	owner, _, _ := legacyOwnerFixture(t, s)
	app, err := writes.create(App{ID: uuid.NewString(), AccountID: owner.Account.ID, Slug: "unowned-eligibility", RAMMB: 128, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "legacy-eligibility-node", TargetURL: "unix:///tmp/legacy-eligibility.sock", VPCPUs: 4, VCPUBudget: api.VCPUSlots, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	wake := func() error {
		_, err := s.CreateInstance(t.Context(), app.ID, "", string(StateWaking), 128, node.ID, uuid.NewString())
		return err
	}
	if err := s.UpdateAccountStatus(t.Context(), owner.Account.ID, AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if err := wake(); err != nil {
		t.Fatalf("unowned account lost serving grace: %v", err)
	}
	if err := s.UpdateAccountStatus(t.Context(), owner.Account.ID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if err := wake(); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("unowned app bypassed suspended account: %v", err)
	}
	if err := s.UpdateAccountStatus(t.Context(), owner.Account.ID, AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAccountAbuseHold(t.Context(), owner.Account.ID, AccountAbuseHoldOperator, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := wake(); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("unowned app bypassed account abuse hold: %v", err)
	}
	if _, err := s.ReleaseAccountAbuseHold(t.Context(), owner.Account.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := wake(); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("unowned app bypassed deletion: %v", err)
	}
}

func TestMemLegacyStandardOwnerRuntimeEligibility(t *testing.T) {
	m := NewMemStore()
	legacyOwnerRuntimeEligibility(t, m, memoryLegacyOwnerWrites(m))
}
