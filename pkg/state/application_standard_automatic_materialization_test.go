package state

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardAutomaticTestStore interface {
	standardMaterializationTestStore
	ApplicationStandardAutomaticMaterializationStore
}

type standardAutomaticFixture struct {
	owner       CreateAccountWithPersonalOrgResult
	project     Project
	version     ApplicationStandardVersion
	destination ApplicationStandardLogDestination
}

func standardAutomaticSetup(t *testing.T, s standardAutomaticTestStore, projectScope, defaultsOnly bool) standardAutomaticFixture {
	t.Helper()
	ctx := context.Background()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "automatic-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	f := standardAutomaticFixture{owner: owner}
	if projectScope {
		f.project, err = s.CreateProject(ctx, Project{AccountID: owner.Account.ID, Slug: "automatic-production"})
		if err != nil {
			t.Fatal(err)
		}
	}
	definition := json.RawMessage(`{"security_policy":{"mode":"default","value":"warn"}}`)
	if !defaultsOnly {
		f.destination, err = s.CreateApplicationStandardLogDestination(ctx, ApplicationStandardLogDestinationCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: "Company logs", Kind: "http_json", TargetURL: "https://automatic.example.com/logs", AuthHeaderSealed: []byte("automatic-sealed-credential")})
		if err != nil {
			t.Fatal(err)
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		publisher, err := s.CreateApplicationStandardPublisher(ctx, ApplicationStandardPublisherCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: "Company CI", PublicKeyDER: der})
		if err != nil {
			t.Fatal(err)
		}
		definition = mustMaterializationJSON(t, appstandards.Definition{
			appstandards.LogDestinations:   {Mode: appstandards.Mandatory, Value: mustMaterializationJSON(t, []string{f.destination.ID})},
			appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Value: mustMaterializationJSON(t, []string{publisher.ID})},
			appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
			appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Value: json.RawMessage(`"warn"`)},
			appstandards.EgressCIDRs:       {Mode: appstandards.Mandatory, Value: json.RawMessage(`["8.8.8.0/24"]`)},
			appstandards.EgressExtraPorts:  {Mode: appstandards.Mandatory, Value: json.RawMessage(`[8443]`)},
		})
	}
	f.version, err = s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "automatic-company", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	request := ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
	if projectScope {
		request.Scope, request.ScopeID = "project", f.project.ID
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, request)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("admission preview: %+v %v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	// No services exist yet: this is genuine no-target work, with no runtime
	// completion or observation fabricated for any application.
	c, err := s.ClaimApplicationStandardOperation(ctx, "initial-admission")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || operation.State != "completed" || len(operation.Targets) != 0 {
		t.Fatalf("no-target admission: %+v %v", operation, err)
	}
	return f
}

func automaticCreate(t *testing.T, s standardAutomaticTestStore, f standardAutomaticFixture, slug string) App {
	t.Helper()
	app, err := s.CreateApp(context.Background(), App{AccountID: f.owner.Account.ID, OrgID: f.owner.PersonalOrg.ID, ProjectID: f.project.ID, WorkloadName: slug, Slug: slug, RAMMB: 128, EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("1.1.1.0/24")}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func automaticRepair(t *testing.T, s standardAutomaticTestStore) ApplicationStandardEnrollment {
	t.Helper()
	ctx := context.Background()
	claim, err := s.ClaimApplicationStandardEnrollment(ctx, "automatic-test-worker")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.MaterializeApplicationStandardEnrollment(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestMemApplicationStandardAutomaticOnboarding(t *testing.T) {
	standardAutomaticOnboarding(t, NewMemStore())
}
func standardAutomaticOnboarding(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, false)
	first := automaticCreate(t, s, f, "automatic-a")
	second := automaticCreate(t, s, f, "automatic-b")
	for _, app := range []App{first, second} {
		if _, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage}); !errors.Is(err, ErrApplicationStandardsPending) {
			t.Fatalf("unrepaired enrollment admitted deployment: %v", err)
		}
	}
	for range 2 {
		e := automaticRepair(t, s)
		if e.State != "persisted" || e.DesiredRevision != 1 || e.PersistedRevision != 1 || e.ObservedRevision != 0 || len(e.MaterializedFields) != 6 {
			t.Fatalf("automatic projection checkpoint: %+v", e)
		}
	}
	for _, app := range []App{first, second} {
		actual, err := s.AppByID(ctx, app.ID)
		if err != nil || !actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyWarn || len(actual.EgressAllowlist) != 1 || actual.EgressAllowlist[0].String() != "8.8.8.0/24" || len(actual.EgressPorts) != 1 || actual.EgressPorts[0] != 8443 {
			t.Fatalf("new service did not receive actual controls: %+v %v", actual, err)
		}
		drains, err := s.ListAppLogDrainsForApp(ctx, app.ID)
		if err != nil || len(drains) != 1 || drains[0].TargetURL != f.destination.TargetURL || string(drains[0].AuthHeaderSealed) != string(f.destination.AuthHeaderSealed) || !drains[0].Enabled {
			t.Fatalf("new service logging missing: %+v %v", drains, err)
		}
		signers, err := s.ListAppTrustedSignersForApp(ctx, app.ID)
		if err != nil || len(signers) != 1 {
			t.Fatalf("new service publishers missing: %+v %v", signers, err)
		}
		// This proves the durable insertion gate was satisfied, not imaged
		// verification, readiness, delivery or native network convergence.
		if _, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage}); err != nil {
			t.Fatalf("persisted enrollment stayed stuck at insertion: %v", err)
		}
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "finished-worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("persisted services were claimed again: %v", err)
	}
}

func TestMemApplicationStandardAutomaticDetach(t *testing.T) {
	standardAutomaticDetach(t, NewMemStore())
}
func standardAutomaticDetach(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, true, false)
	app := automaticCreate(t, s, f, "automatic-detach")
	automaticRepair(t, s)
	if err := s.DeleteProject(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, app.ID)
	if err != nil || pending.State != "pending" || len(pending.MaterializedFields) != 6 || len(pending.Adoptions) != 0 {
		t.Fatalf("scope removal forgot installed fields: %+v %v", pending, err)
	}
	unsigned := false
	if _, err := s.UpdateApp(ctx, app.ID, UpdateAppParams{SetRequireSigned: true, RequireSigned: &unsigned}); !errors.Is(err, ErrApplicationStandardManagedControl) {
		t.Fatalf("pending ownership repair allowed managed edit: %v", err)
	}
	e := automaticRepair(t, s)
	actual, err := s.AppByID(ctx, app.ID)
	if err != nil || actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyOff || len(actual.EgressAllowlist) != 1 || actual.EgressAllowlist[0].String() != "1.1.1.0/24" || len(actual.EgressPorts) != 0 {
		t.Fatalf("scope removal retained old company values: %+v %v", actual, err)
	}
	drains, err := s.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil || len(drains) != 0 {
		t.Fatalf("scope removal retained company logging: %+v %v", drains, err)
	}
	signers, err := s.ListAppTrustedSignersForApp(ctx, app.ID)
	if err != nil || len(signers) != 0 {
		t.Fatalf("scope removal retained company publisher: %+v %v", signers, err)
	}
	if e.State != "persisted" || e.DesiredRevision != 2 || e.PersistedRevision != 2 || e.ObservedRevision != 0 || len(e.MaterializedFields) != 0 {
		t.Fatalf("scope removal checkpoint: %+v", e)
	}
}

func TestMemApplicationStandardAutomaticRestoreDefault(t *testing.T) {
	standardAutomaticRestoreDefault(t, NewMemStore())
}
func standardAutomaticRestoreDefault(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, true, true)
	app := automaticCreate(t, s, f, "automatic-default")
	automaticRepair(t, s)
	if _, err := s.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreApp(ctx, app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	e := automaticRepair(t, s)
	if len(e.LocalSettings) != 0 || string(e.BaseSettings[appstandards.SecurityPolicy]) != `"off"` || len(e.MaterializedFields) != 1 {
		t.Fatalf("restored inherited default became local intent: %+v", e)
	}
	if err := s.DeleteProject(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	automaticRepair(t, s)
	actual, err := s.AppByID(ctx, app.ID)
	if err != nil || actual.SecurityPolicy != api.AppSecurityPolicyOff {
		t.Fatalf("default survived scope removal as local intent: %+v %v", actual, err)
	}
}

func TestMemApplicationStandardAutomaticCreatorPlan(t *testing.T) {
	standardAutomaticCreatorPlan(t, NewMemStore())
}
func standardAutomaticCreatorPlan(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, false)
	creator, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "automatic-free-creator@example.com", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: creator.Account.ID, OrgID: f.owner.PersonalOrg.ID, Slug: "automatic-plan-boundary", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	e := automaticRepair(t, s)
	if e.State != "blocked" || e.ErrorCode != "plan_log_drain_app_limit" || e.PersistedRevision != 0 || e.DesiredRevision != 1 {
		t.Fatalf("creator received organization plan benefits: %+v", e)
	}
	actual, err := s.AppByID(ctx, app.ID)
	if err != nil || actual.RequireSigned {
		t.Fatalf("blocked enrollment partly projected: %+v %v", actual, err)
	}
	if _, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("blocked enrollment admitted deployment: %v", err)
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "retry-throttle-worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blocked app spun without its retry delay: %v", err)
	}
}

func TestMemApplicationStandardAutomaticReviewPrecedence(t *testing.T) {
	standardAutomaticReviewPrecedence(t, NewMemStore())
}
func standardAutomaticReviewPrecedence(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, true)
	app := automaticCreate(t, s, f, "automatic-review-precedence")
	automatic, err := s.ClaimApplicationStandardEnrollment(ctx, "already-claimed-repair")
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Slug: f.version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"off"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := s.ListApplicationStandardAssignments(ctx, f.owner.PersonalOrg.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{AssignmentID: assignments[0].ID, ExpectedRevision: 1, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: version.StandardID, AdmissionVersion: 2, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("pending target review: %+v %v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, automatic); !errors.Is(err, ErrApplicationStandardOperationInProgress) {
		t.Fatalf("automatic repair ignored reviewed authority: %v", err)
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "later-repair"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("queued reviewed target was automatically claimed: %v", err)
	}
	claim, err := s.ClaimApplicationStandardOperation(ctx, "reviewed-worker")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.MaterializeNextApplicationStandardTarget(ctx, claim)
	if err != nil || operation.Targets[0].State != "persisted" {
		t.Fatalf("reviewed projection was made stale by repair: %+v %v", operation, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, p.OrgID, app.ID)
	if err != nil || e.Adoptions[0].Version != 2 || e.DesiredRevision != 2 || e.PersistedRevision != 2 {
		t.Fatalf("reviewed target version lost: %+v %v", e, err)
	}
}

func TestMemApplicationStandardAutomaticLeaseFencing(t *testing.T) {
	m := NewMemStore()
	standardAutomaticLeaseFencing(t, m, func(c ApplicationStandardEnrollmentClaim) {
		m.mu.Lock()
		defer m.mu.Unlock()
		held := m.applicationStandardEnrollmentClaims[c.AppID]
		held.Until = time.Now().Add(-time.Second)
		m.applicationStandardEnrollmentClaims[c.AppID] = held
	})
}

func standardAutomaticLeaseFencing(t *testing.T, s standardAutomaticTestStore, expire func(ApplicationStandardEnrollmentClaim)) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, true)
	app := automaticCreate(t, s, f, "automatic-lease")
	old, err := s.ClaimApplicationStandardEnrollment(ctx, "dead-enrollment-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "concurrent-enrollment-worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("live enrollment lease was stolen: %v", err)
	}
	expire(old)
	old.Until = time.Now().Add(time.Hour)
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("caller extended an expired storage lease: %v", err)
	}
	replacement, err := s.ClaimApplicationStandardEnrollment(ctx, "replacement-enrollment-worker")
	if err != nil || replacement.Generation != old.Generation+1 {
		t.Fatalf("restart did not fence old worker: %+v %v", replacement, err)
	}
	// UUID spelling and a caller's timestamp are not lease authority.
	replacement.AppID, replacement.OrgID = strings.ToUpper(replacement.AppID), strings.ToUpper(replacement.OrgID)
	replacement.Until = time.Now().Add(-time.Hour)
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("completed lease was replayed: %v", err)
	}
	// Restoring again changes durable intent and revokes an already claimed
	// repair, even though organization ownership and selected version match.
	for range 2 {
		if _, err := s.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RestoreApp(ctx, app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
			t.Fatal(err)
		}
		if old.Owner == "dead-enrollment-worker" {
			old, err = s.ClaimApplicationStandardEnrollment(ctx, "pre-restore-worker")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("restore failed to revoke claimed intent: %v", err)
	}
	e := automaticRepair(t, s)
	if e.DesiredRevision != 3 || e.PersistedRevision != 3 || e.ObservedRevision != 0 || len(e.LocalSettings) != 0 {
		t.Fatalf("restored repair reset intent or fabricated observation: %+v", e)
	}
}

func TestMemApplicationStandardAutomaticBlockedRecovery(t *testing.T) {
	m := NewMemStore()
	standardAutomaticBlockedRecovery(t, m, func(appID string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		e := m.applicationStandardEnrollments[appID]
		e.UpdatedAt = time.Now().Add(-time.Minute)
		m.applicationStandardEnrollments[appID] = e
	})
}

func standardAutomaticBlockedRecovery(t *testing.T, s standardAutomaticTestStore, age func(string)) {
	t.Helper()
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, false)
	if err := s.UpdateAccountPlan(ctx, f.owner.Account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	app := automaticCreate(t, s, f, "automatic-entitlement-recovery")
	blocked := automaticRepair(t, s)
	if blocked.State != "blocked" || blocked.PersistedRevision != 0 {
		t.Fatalf("current entitlement was ignored: %+v", blocked)
	}
	if err := s.UpdateAccountPlan(ctx, f.owner.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	age(app.ID) // Simulate the retry window without sleeping through it.
	e := automaticRepair(t, s)
	actual, err := s.AppByID(ctx, app.ID)
	if err != nil || !actual.RequireSigned || e.State != "persisted" || e.ErrorCode != "" || e.DesiredRevision != 1 || e.PersistedRevision != 1 {
		t.Fatalf("durable blocked service did not recover: %+v %+v %v", e, actual, err)
	}
}

func TestMemApplicationStandardWorkerFairness(t *testing.T) {
	standardWorkerFairness(t, NewMemStore())
}

func standardWorkerFairness(t *testing.T, s standardAutomaticTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	operations := []ApplicationStandardOperation{}
	for _, app := range f.apps {
		request := f.plan.Request
		request.AssignmentID, request.Scope, request.ScopeID = "", "application", app.ID
		plan, err := s.PreviewApplicationStandardAssignment(ctx, app.OrgID, f.owner.Account.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		op, err := s.ApproveApplicationStandardReview(ctx, plan.OrgID, f.owner.Account.ID, plan.ID, plan.ApprovalHash)
		if err != nil {
			t.Fatal(err)
		}
		operations = append(operations, op)
	}
	if len(operations) < 2 {
		t.Fatal("fairness fixture needs two independent operations")
	}
	first, err := s.ClaimApplicationStandardOperation(ctx, "round-robin-worker")
	if err != nil || first.OperationID != operations[0].ID {
		t.Fatalf("oldest operation was not selected: %+v %v", first, err)
	}
	waiting, err := s.MaterializeNextApplicationStandardTarget(ctx, first)
	if err != nil || waiting.State != "waiting" {
		t.Fatalf("first operation did not wait for real observation: %+v %v", waiting, err)
	}
	next, err := s.ClaimApplicationStandardOperation(ctx, "round-robin-worker")
	if err != nil || next.OperationID != operations[1].ID {
		t.Fatalf("waiting operation starved independent queued work: %+v %v", next, err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, next); err != nil {
		t.Fatal(err)
	}
}
