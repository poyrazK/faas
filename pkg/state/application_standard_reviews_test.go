package state_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardReviewTestStore = state.ContractTestStore

func TestMemApplicationStandardReviews(t *testing.T) {
	standardReviewLifecycle(t, state.NewMemStore())
}

func TestMemApplicationStandardReviewEmptyProject(t *testing.T) {
	m := state.NewMemStore()
	standardReviewEmptyProject(t, m, func(a appstandards.Assignment, actor string) {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		(*m.ApplicationStandardAssignments()) = map[string]state.ApplicationStandardAssignmentRecord{a.ID: {Assignment: a, Revision: 1, Active: true, CreatedBy: actor}}
	})
}

func TestMemApplicationStandardReviewBatchAccountQuota(t *testing.T) {
	standardReviewBatchAccountQuota(t, state.NewMemStore())
}

func TestMemApplicationStandardReviewSavedAdoptions(t *testing.T) {
	m := state.NewMemStore()
	standardReviewSavedAdoptions(t, m, func(a appstandards.Assignment, actor string, active bool) {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		if (*m.ApplicationStandardAssignments()) == nil {
			(*m.ApplicationStandardAssignments()) = map[string]state.ApplicationStandardAssignmentRecord{}
		}
		before := (*m.ApplicationStandardAssignments())[a.ID]
		(*m.ApplicationStandardAssignments())[a.ID] = state.ApplicationStandardAssignmentRecord{Assignment: a, Revision: before.Revision + 1, Active: active, CreatedBy: actor}
	})
}

func standardReviewSavedAdoptions(t *testing.T, store standardReviewTestStore, seed func(appstandards.Assignment, string, bool)) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-adoptions@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: slug, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	unmanaged := create("before-admission")
	b, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "other-standard", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: b.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"off"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	assignment := appstandards.Assignment{ID: uuid.NewString(), OrgID: owner.PersonalOrg.ID, Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: b.StandardID, AdmissionVersion: 2}
	seed(assignment, owner.Account.ID, true)
	pinned := create("after-admission")
	a, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "new-standard", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":false}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	preview := func(app state.App) state.ApplicationStandardReviewPlan {
		t.Helper()
		p, err := store.PreviewApplicationStandardAssignment(ctx, app.OrgID, app.AccountID, state.ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: a.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := preview(unmanaged)
	if len(p.Applications[0].BeforeAdoptions) != 0 || len(p.Applications[0].AfterAdoptions) != 1 || len(p.Applications[0].Effective.Sources[appstandards.SecurityPolicy]) != 0 {
		t.Fatalf("review enrolled an older service into an unrelated active assignment: %+v", p)
	}
	assignment.AdmissionVersion = 1
	seed(assignment, owner.Account.ID, false)
	p = preview(pinned)
	reviewed := p.Applications[0]
	if len(reviewed.BeforeAdoptions) != 1 || reviewed.BeforeAdoptions[0].Version != 2 || len(reviewed.AfterAdoptions) != 2 || len(reviewed.Effective.Sources[appstandards.SecurityPolicy]) != 1 || reviewed.Effective.Sources[appstandards.SecurityPolicy][0].Version != 2 {
		t.Fatalf("disabled admission or another assignment moved a saved pin: %+v", reviewed)
	}
	if _, err := store.ValidateApplicationStandardReview(ctx, pinned.OrgID, pinned.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

func standardReviewEmptyProject(t *testing.T, store standardReviewTestStore, seed func(appstandards.Assignment, string)) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-empty-project@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "empty-review-project"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "parent-egress", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	seed(appstandards.Assignment{ID: uuid.NewString(), OrgID: owner.PersonalOrg.ID, Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: parent.StandardID, AdmissionVersion: 1}, owner.Account.ID)
	child, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "child-egress", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["1.1.1.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r := state.ApplicationStandardReviewRequest{Scope: "project", ScopeID: project.ID, StandardID: child.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
	p, err := store.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Applications) != 0 || !slices.ContainsFunc(p.Blockers, func(b state.ApplicationStandardReviewBlocker) bool {
		return b.Scope == "project" && state.SameStandardUUID(b.ScopeID, project.ID) && b.Code == "standard_requirement_conflict"
	}) {
		t.Fatalf("empty project's future services would inherit a conflict: %+v", p)
	}
	if _, err := store.ValidateApplicationStandardReview(ctx, owner.PersonalOrg.ID, owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, state.ErrApplicationStandardReviewBlocked) {
		t.Fatalf("empty project accepted conflict: %v", err)
	}
	if _, err := store.CreateApp(ctx, state.App{OrgID: owner.PersonalOrg.ID, AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "first-project-service", WorkloadName: "first", RAMMB: 128}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateApplicationStandardReview(ctx, owner.PersonalOrg.ID, owner.Account.ID, p.ID, p.ApprovalHash); !errors.Is(err, state.ErrApplicationStandardReviewStale) {
		t.Fatalf("empty project membership not bound: %v", err)
	}
	// The project cannot become mixed-organization: another account's app is
	// refused (project_workload_owner), so a review never sees one.
	foreign, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-empty-project-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{OrgID: foreign.PersonalOrg.ID, AccountID: foreign.Account.ID, ProjectID: project.ID, Slug: "foreign-project-service", WorkloadName: "foreign", RAMMB: 128}); err == nil {
		t.Fatal("foreign account joined the reviewed project")
	}
}

func standardReviewBatchAccountQuota(t *testing.T, store standardReviewTestStore) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-account-quota@example.com", Plan: api.PlanHobby})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "quota-project"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		input := state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: fmt.Sprintf("quota-service-%d", i), RAMMB: 128}
		if i < 2 {
			input.ProjectID = project.ID
			input.WorkloadName = input.Slug
		}
		app, err := store.CreateApp(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: app.ID, AccountID: owner.Account.ID, Kind: state.AppLogDrainKindHTTPJSON, TargetURL: "https://legacy-quota.example/logs", Enabled: false}); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for i := range 3 {
		d, err := store.CreateApplicationStandardLogDestination(ctx, state.ApplicationStandardLogDestinationCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: fmt.Sprintf("Company logs %d", i), Kind: "http_json", TargetURL: fmt.Sprintf("https://company-quota.example/logs/%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	value, _ := json.Marshal(ids)
	definition, _ := json.Marshal(appstandards.Definition{appstandards.LogDestinations: {Mode: appstandards.Mandatory, Value: value}})
	v, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "quota-logs", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewApplicationStandardAssignment(ctx, owner.PersonalOrg.ID, owner.Account.ID, state.ApplicationStandardReviewRequest{Scope: "project", ScopeID: project.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Applications) != 2 || len(p.Blockers) != 2 {
		t.Fatalf("aggregate quota review: %+v", p)
	}
	for _, blocker := range p.Blockers {
		if blocker.Code != "plan_log_drain_account_limit" {
			t.Fatalf("per-app valid change did not account for whole batch and disabled drains: %+v", blocker)
		}
	}
}

func standardReviewLifecycle(t *testing.T, store standardReviewTestStore) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-owner@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	orgID, actorID := owner.PersonalOrg.ID, owner.Account.ID
	app, err := store.CreateApp(ctx, state.App{AccountID: actorID, OrgID: orgID, Slug: "review-app", RAMMB: 128, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: app.ID, AccountID: actorID, Kind: state.AppLogDrainKindHTTPJSON, TargetURL: "https://legacy.example/logs?token=legacy-secret", AuthHeaderSealed: []byte("sealed-legacy-secret"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := store.CreateApplicationStandardLogDestination(ctx, state.ApplicationStandardLogDestinationCreate{OrgID: orgID, ActorID: actorID, Name: "Production logs", Kind: "http_json", TargetURL: "https://company.example/logs", AuthHeaderSealed: []byte("sealed-company-secret")})
	if err != nil {
		t.Fatal(err)
	}
	definition := []byte(`{"log_destinations":{"mode":"mandatory","value":["` + destination.ID + `"]}}`)
	version, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: orgID, ActorID: actorID, Slug: "review-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	request := state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: orgID, StandardID: version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 2}
	preview := func() state.ApplicationStandardReviewPlan {
		t.Helper()
		p, err := store.PreviewApplicationStandardAssignment(ctx, orgID, actorID, request)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	plan := preview()
	if len(plan.Applications) != 1 || len(plan.Blockers) != 0 || plan.ExpiresAt.Sub(plan.CreatedAt) != api.ApplicationStandardReviewTTL {
		t.Fatalf("review: %+v", plan)
	}
	gotApp := plan.Applications[0]
	if len(gotApp.AfterAdoptions) != 1 || gotApp.AfterAdoptions[0].Version != 1 || !slices.Contains(gotApp.ChangedFields, appstandards.LogDestinations) || string(gotApp.Effective.Values[appstandards.LogDestinations]) != `["`+destination.ID+`"]` {
		t.Fatalf("reviewed application: %+v", gotApp)
	}
	if string(gotApp.BeforeSettings[appstandards.LogDestinations]) != `["`+state.CanonicalStandardUUID(legacy.ID)+`"]` {
		t.Fatalf("omitted legacy destination removal: %+v", gotApp.BeforeSettings)
	}
	for _, input := range []any{plan, (*plan.ApprovalInputs())} {
		raw, _ := json.Marshal(input)
		for _, private := range []string{"sealed-legacy-secret", "sealed-company-secret", "legacy-secret", "legacy.example", "company.example", "auth_hash", "target_hash"} {
			if strings.Contains(string(raw), private) {
				t.Fatalf("review leaked %s", private)
			}
		}
	}
	// A returned object cannot rewrite the durable reviewed body.
	plan.Applications[0].Effective.Values[appstandards.LogDestinations][0] = 'X'
	saved, err := store.GetApplicationStandardReviewPlan(ctx, strings.ToUpper(orgID), strings.ReplaceAll(strings.ToUpper(plan.ID), "-", ""))
	if err != nil || string(saved.Applications[0].Effective.Values[appstandards.LogDestinations]) != `["`+destination.ID+`"]` {
		t.Fatalf("aliased plan: %+v %v", saved, err)
	}
	check := func(p state.ApplicationStandardReviewPlan, want error) {
		t.Helper()
		_, err := store.ValidateApplicationStandardReview(ctx, orgID, actorID, p.ID, p.ApprovalHash)
		if !errors.Is(err, want) {
			t.Fatalf("freshness got %v want %v", err, want)
		}
	}
	check(saved, nil)
	if _, err := store.ValidateApplicationStandardReview(ctx, orgID, actorID, saved.ID, strings.Repeat("0", 64)); !errors.Is(err, state.ErrApplicationStandardReviewStale) {
		t.Fatalf("wrong approval: %v", err)
	}
	if _, err := store.GetApplicationStandardReviewPlan(ctx, uuid.NewString(), saved.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign plan read: %v", err)
	}
	assignments, err := store.ListApplicationStandardAssignments(ctx, orgID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("preview activated an assignment: %v %+v", err, assignments)
	}
	enrollment, err := store.GetApplicationStandardEnrollment(ctx, orgID, app.ID)
	if err != nil || enrollment.State != "unmanaged" || len(enrollment.Adoptions) != 0 {
		t.Fatalf("preview enrolled app: %v %+v", err, enrollment)
	}
	// Publishing the next candidate or an unused resource cannot invalidate v1.
	_, err = store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: orgID, ActorID: actorID, Slug: version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApplicationStandardLogDestination(ctx, state.ApplicationStandardLogDestinationCreate{OrgID: orgID, ActorID: actorID, Name: "Unused logs", Kind: "http_json", TargetURL: "https://unused.example/logs"})
	if err != nil {
		t.Fatal(err)
	}
	check(saved, nil)
	// Changes that affect the approval must be discovered from current storage.
	newTarget := "https://changed.example/logs?token=other-secret"
	if _, err := store.UpdateAppLogDrain(ctx, legacy.ID, state.UpdateAppLogDrainParams{TargetURL: &newTarget}); err != nil {
		t.Fatal(err)
	}
	check(saved, state.ErrApplicationStandardReviewStale)
	fresh := preview()
	ports := []int{8443}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{SetEgressPorts: true, EgressPorts: ports}); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertAppTrustedSigner(ctx, actorID, app.ID, "ci", der, actorID); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "apps/review/layer.ext4", 1234); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	if err := store.UpsertDeploymentScanResult(ctx, dep.ID, []byte(`{"critical":0}`), "complete"); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	if _, err := store.CreateApp(ctx, state.App{AccountID: actorID, OrgID: orgID, Slug: "new-review-member", RAMMB: 128}); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	fresh = preview()
	if err := store.UpdateAccountPlan(ctx, actorID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	check(fresh, state.ErrApplicationStandardReviewStale)
	if err := store.UpdateAccountPlan(ctx, actorID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	// Account/platform restrictions and missing artifact evidence are blockers,
	// never represented as a successful application of security flags.
	publisher, err := store.CreateApplicationStandardPublisher(ctx, state.ApplicationStandardPublisherCreate{OrgID: orgID, ActorID: actorID, Name: "Company CI", PublicKeyDER: der})
	if err != nil {
		t.Fatal(err)
	}
	securityDefinition := []byte(`{"require_signed":{"mode":"mandatory","value":true},"trusted_publishers":{"mode":"mandatory","value":["` + publisher.ID + `"]}}`)
	security, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: orgID, ActorID: actorID, Slug: "review-security", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: securityDefinition}})
	if err != nil {
		t.Fatal(err)
	}
	securityPlan, err := store.PreviewApplicationStandardAssignment(ctx, orgID, actorID, state.ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: security.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(securityPlan.Blockers, func(b state.ApplicationStandardReviewBlocker) bool {
		return b.Code == "current_artifact_verification_required"
	}) {
		t.Fatalf("unsigned current artifact was ignored: %+v", securityPlan.Blockers)
	}
	check(securityPlan, state.ErrApplicationStandardReviewBlocked)
	// Nonmember accounts cannot create a review even when they know the UUIDs.
	foreign, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PreviewApplicationStandardAssignment(ctx, orgID, foreign.Account.ID, request); !errors.Is(err, state.ErrApplicationStandardReviewForbidden) {
		t.Fatalf("nonmember review: %v", err)
	}
	if _, err := store.ValidateApplicationStandardReview(ctx, orgID, foreign.Account.ID, saved.ID, saved.ApprovalHash); !errors.Is(err, state.ErrApplicationStandardReviewForbidden) {
		t.Fatalf("nonmember approval probe: %v", err)
	}
}

func TestApplicationStandardReviewPreservesUnmanagedIntent(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	owner, err := m.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "review-default@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "explicit-local", RAMMB: 128, EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}, EgressPorts: []int{8443}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "default-egress", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"default","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.PreviewApplicationStandardAssignment(ctx, app.OrgID, app.AccountID, state.ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := p.Applications[0]
	if string(a.Effective.Values[appstandards.EgressCIDRs]) != `["1.1.1.1/32"]` || string(a.Effective.Values[appstandards.EgressExtraPorts]) != `[8443]` || string(a.LocalSettings[appstandards.EgressCIDRs]) != `["1.1.1.1/32"]` {
		t.Fatalf("default reset explicit intent: %+v", a)
	}
	(*
	// Heartbeats/consumer observation cannot invalidate a review of intent.
	m.Mu()).Lock()
	e := (*m.ApplicationStandardEnrollments())[app.ID]
	e.ObservedRevision = 1
	e.PersistedRevision = 1
	e.UpdatedAt = time.Now().UTC()
	(*m.ApplicationStandardEnrollments())[app.ID] = e
	(*m.Mu()).Unlock()
	if _, err := m.ValidateApplicationStandardReview(ctx, app.OrgID, app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	if err := state.ValidateStandardReviewHash(p, p, p.ApprovalHash, p.ExpiresAt); !errors.Is(err, state.ErrApplicationStandardReviewExpired) {
		t.Fatalf("expiry boundary: %v", err)
	}
}

func TestApplicationStandardReviewKeepsOnlyExplicitLogExtras(t *testing.T) {
	appID, orgID, assignmentID, standardID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	legacyID, oldCompanyID, newCompanyID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	layer := func(version int64, destination string) appstandards.Selection {
		return appstandards.Selection{Layers: []appstandards.Layer{{AssignmentID: assignmentID, StandardID: standardID, Version: version, Scope: "organization", ScopeID: orgID, Definition: appstandards.Definition{appstandards.LogDestinations: {Mode: appstandards.Mandatory, Override: appstandards.Extend, Value: json.RawMessage(`["` + destination + `"]`)}}}}, Adoptions: []appstandards.Adoption{{AssignmentID: assignmentID, Version: version}}}
	}
	input := state.StandardReviewAppSnapshot{AppID: appID, OrgID: orgID, Settings: state.ApplicationStandardBaseSettings(state.App{}), Drains: []state.StandardReviewDrain{{ID: legacyID}}, Enrollment: state.StandardReviewEnrollment{BaseSettings: state.ApplicationStandardBaseSettings(state.App{}), LocalSettings: appstandards.Settings{}}}
	first, err := state.ResolveStandardReviewedApp(input, appstandards.Selection{}, layer(1, oldCompanyID), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(state.StandardReviewStrings(first.Effective.Values[appstandards.LogDestinations]), legacyID) || !slices.Equal(first.AdditionalLogDestinations, []string{legacyID}) {
		t.Fatalf("first adoption removed permitted extra: %+v", first)
	}
	input.Enrollment.BaseSettings, input.Enrollment.LocalSettings, input.Enrollment.AdditionalLogDestinations = first.BaseSettings, first.LocalSettings, first.AdditionalLogDestinations
	// The first projection was installed. Admission pins alone do not prove
	// which fields currently belong to the company rather than local intent.
	input.Enrollment.MaterializedFields = state.StandardEffectiveFields(first.Effective)
	input.Drains = append(input.Drains, state.StandardReviewDrain{ID: oldCompanyID})
	next, err := state.ResolveStandardReviewedApp(input, layer(1, oldCompanyID), layer(2, newCompanyID), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	values := state.StandardReviewStrings(next.Effective.Values[appstandards.LogDestinations])
	if len(values) != 2 || !slices.Contains(values, legacyID) || !slices.Contains(values, newCompanyID) || slices.Contains(values, oldCompanyID) {
		t.Fatalf("old mandatory destination became a local extra: %+v", next)
	}
}
