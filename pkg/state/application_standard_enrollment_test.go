package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardEnrollmentTestStore interface {
	Store
	ApplicationStandardStore
	ApplicationStandardEnrollmentStore
	PRPreviewBatchStore
	OrgActivityAppLifecycleMutationStore
	ProjectReconcileStore
}

func TestMemApplicationStandardEnrollment(t *testing.T) {
	store := NewMemStore()
	standardEnrollmentLifecycle(t, store, func(assignment appstandards.Assignment, actor string) {
		store.mu.Lock()
		defer store.mu.Unlock()
		if store.applicationStandardAssignments == nil {
			store.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{}
		}
		store.applicationStandardAssignments[assignment.ID] = applicationStandardAssignmentRecord{Assignment: assignment, Active: true, Revision: 1, CreatedBy: actor}
	})
}

func TestMemApplicationStandardEnrollmentRollback(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	created, err := m.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "enrollment-rollback@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	actor, org := created.Account, created.PersonalOrg
	limits := api.MustLimitsFor(api.PlanPro)
	appID, projectID := uuid.NewString(), uuid.NewString()
	// The second raw insert fails the same foreign-scope boundary as PG.
	// Its earlier app, project and seeded environment must all roll back.
	m.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{"foreign": {Assignment: appstandards.Assignment{ID: uuid.NewString(), OrgID: uuid.NewString(), Scope: "application", ScopeID: appID, StandardID: uuid.NewString(), AdmissionVersion: 1}, Active: true}}
	_, _, _, err = m.ApplyProjectPlan(ctx, Project{ID: projectID, AccountID: actor.ID, Slug: "rollback-project"}, []App{{AccountID: actor.ID, OrgID: org.ID, Slug: "earlier-insert"}, {ID: appID, AccountID: actor.ID, OrgID: org.ID, Slug: "foreign-insert"}}, nil, limits)
	if !errors.Is(err, ErrInvalidArgument) || len(m.apps) != 0 || len(m.projects) != 0 || len(m.projectEnvironments) != 0 || len(m.applicationStandardEnrollments) != 0 {
		t.Fatalf("partial project transaction: %v apps=%d projects=%d environments=%d enrollments=%d", err, len(m.apps), len(m.projects), len(m.projectEnvironments), len(m.applicationStandardEnrollments))
	}
	_, _, err = m.CreateAppIfUnderQuotaWithActivity(ctx, App{ID: appID, AccountID: actor.ID, OrgID: org.ID, Slug: "activity-rollback"}, limits, OrgActivity{})
	if err == nil || len(m.apps) != 0 || len(m.applicationStandardEnrollments) != 0 {
		t.Fatalf("activity enrollment orphan: %v", err)
	}
	// Repeat without the foreign assignment so rejection occurs after insert.
	clear(m.applicationStandardAssignments)
	_, _, err = m.CreateAppIfUnderQuotaWithActivity(ctx, App{ID: appID, AccountID: actor.ID, OrgID: org.ID, Slug: "activity-rollback"}, limits, OrgActivity{})
	if err == nil || len(m.apps) != 0 || len(m.applicationStandardEnrollments) != 0 {
		t.Fatalf("activity rollback left enrollment: %v", err)
	}
}

// Assignment fixtures are inserted by the test, not a public activation API.
// Activation will require a reviewed plan; enrollment must already be safe for
// every insert path before that API can make an assignment active.
func standardEnrollmentLifecycle(t *testing.T, store standardEnrollmentTestStore, seed func(appstandards.Assignment, string)) {
	t.Helper()
	ctx := context.Background()
	created, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "enrollment@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	actor, org := created.Account, created.PersonalOrg
	limits := api.MustLimitsFor(api.PlanPro)
	input := func(slug string) App {
		return App{AccountID: actor.ID, OrgID: org.ID, Slug: slug, RAMMB: 128, Type: AppTypeApp, WorkloadClass: WorkloadClassHTTP, EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, EgressPorts: []int{8443}}
	}
	old, err := store.CreateApp(ctx, input("before-standard"))
	if err != nil {
		t.Fatal(err)
	}
	check := func(app App, version int64, state string, revision int64) ApplicationStandardEnrollment {
		t.Helper()
		row, err := store.GetApplicationStandardEnrollment(ctx, org.ID, app.ID)
		if err != nil || row.State != state || row.DesiredRevision != revision || row.PersistedRevision != 0 || row.ObservedRevision != 0 {
			t.Fatalf("enrollment %s: %+v %v", app.Slug, row, err)
		}
		if version == 0 && len(row.Adoptions) != 0 || version != 0 && (len(row.Adoptions) != 1 || row.Adoptions[0].Version != version) {
			t.Fatalf("admission %s: %+v", app.Slug, row.Adoptions)
		}
		base, _ := json.Marshal(row.BaseSettings)
		want, _ := json.Marshal(applicationStandardBaseSettings(app))
		if string(base) != string(want) {
			t.Fatalf("base intent %s: %+v", app.Slug, row.BaseSettings)
		}
		return row
	}
	check(old, 0, "unmanaged", 1)
	version, err := store.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: org.ID, ActorID: actor.ID, Slug: "baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	assignment := appstandards.Assignment{ID: uuid.NewString(), OrgID: org.ID, Scope: "organization", ScopeID: org.ID, StandardID: version.StandardID, AdmissionVersion: 1}
	seed(assignment, actor.ID)
	first, err := store.CreateApp(ctx, input("after-standard"))
	if err != nil {
		t.Fatal(err)
	}
	row := check(first, 1, "pending", 1)
	row.Adoptions[0].Version = 99
	row.BaseSettings[appstandards.EgressCIDRs][0] = 'X'
	check(first, 1, "pending", 1)
	alias, err := store.GetApplicationStandardEnrollment(ctx, strings.ToUpper(org.ID), strings.ReplaceAll(strings.ToUpper(first.ID), "-", ""))
	if err != nil || alias.DesiredRevision != 1 {
		t.Fatalf("UUID alias: %+v %v", alias, err)
	}
	if _, err := store.GetApplicationStandardEnrollment(ctx, uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org enrollment read: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, Deployment{AppID: first.ID, Kind: "image", Status: "pending"}); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("unapplied deployment admitted: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, Deployment{AppID: old.ID, Kind: "image", Status: "pending"}); err != nil {
		t.Fatalf("unmanaged deployment rejected: %v", err)
	}
	// Publishing alone cannot change the explicit admission pointer or old pins.
	_, err = store.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: org.ID, ActorID: actor.ID, Slug: "baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	quota, err := store.CreateAppIfUnderQuota(ctx, input("quota-standard"), limits)
	if err != nil {
		t.Fatal(err)
	}
	check(quota, 1, "pending", 1)
	assignment.AdmissionVersion = 2
	seed(assignment, actor.ID)
	check(first, 1, "pending", 1)
	project, apps, _, err := store.ApplyProjectPlan(ctx, Project{AccountID: actor.ID, Slug: "standard-project"}, []App{input("project-standard")}, nil, limits)
	if err != nil || len(apps) != 1 {
		t.Fatalf("project plan: %+v %v", apps, err)
	}
	check(apps[0], 2, "pending", 1)
	add := input("reconcile-standard")
	add.WorkloadName = "api"
	reconciled, err := store.ApplyProjectReconcile(ctx, project, []ProjectReconcileMutation{{Op: "create", App: add}}, nil, ProjectScanSourceUnknown, limits)
	if err != nil || len(reconciled.Added) != 1 {
		t.Fatalf("project reconcile: %+v %v", reconciled, err)
	}
	check(reconciled.Added[0], 2, "pending", 1)
	preview := input("preview-standard")
	preview.ProjectID, preview.PreviewOfSlug, preview.PreviewPrNumber = project.ID, apps[0].Slug, 17
	previews, err := store.CreatePRPreviewAppsIfUnderQuota(ctx, []App{preview}, limits)
	if err != nil || len(previews) != 1 {
		t.Fatalf("preview: %+v %v", previews, err)
	}
	check(previews[0], 2, "pending", 1)
	actorID := uuid.MustParse(actor.ID)
	entry := OrgActivity{OrgID: uuid.MustParse(org.ID), Kind: "app.created", ActorType: OrgActivityActorUser, ActorAccountID: &actorID, ActorLabel: actor.Email, ResourceType: "app", SourceType: "app.created", SourceID: "enrollment-create", Data: json.RawMessage(`{"created":true}`)}
	activity, _, err := store.CreateAppIfUnderQuotaWithActivity(ctx, input("activity-standard"), limits, entry)
	if err != nil {
		t.Fatal(err)
	}
	check(activity, 2, "pending", 1)
	if _, err := store.ScheduleAppDeletion(ctx, first.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreApp(ctx, first.ID, limits)
	if err != nil {
		t.Fatal(err)
	}
	check(restored, 2, "pending", 2)
	assignments, err := store.ListApplicationStandardAssignments(ctx, org.ID)
	if err != nil || len(assignments) != 1 || assignments[0].AdmissionVersion != 2 {
		t.Fatalf("assignment read: %+v %v", assignments, err)
	}
	// A tombstone may predate a project assignment. Restore must validate
	// current scope ownership rather than resurrecting that foreign member.
	foreign, err := store.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "enrollment-foreign@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	foreignInput := input("foreign-tombstone")
	foreignInput.AccountID, foreignInput.OrgID, foreignInput.ProjectID = foreign.Account.ID, foreign.PersonalOrg.ID, project.ID
	foreignInput.WorkloadName = "foreign"
	tombstone, err := store.CreateApp(ctx, foreignInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(ctx, tombstone.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	projectAssignment := assignment
	projectAssignment.ID, projectAssignment.Scope, projectAssignment.ScopeID = uuid.NewString(), "project", project.ID
	seed(projectAssignment, actor.ID)
	if _, err := store.RestoreApp(ctx, tombstone.ID, limits); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("restored foreign project member: %v", err)
	}
	if row, err := store.AppByID(ctx, tombstone.ID); err != nil || row.Status != AppDeleted {
		t.Fatalf("rejected restore changed tombstone: %+v %v", row, err)
	}
	foreignInput.Slug, foreignInput.WorkloadName = "foreign-new-member", "foreign-new"
	if _, err := store.CreateApp(ctx, foreignInput); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("new foreign project member: %v", err)
	}
}
