package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func standardEnrollmentAPIFixture(ctx context.Context, t *testing.T, e testEnv) (state.Org, state.App) {
	t.Helper()
	org, err := e.store.CreateOrg(ctx, state.Org{Slug: "enrollment-view", Name: "Enrollment view", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	d, err := e.store.CreateApplicationStandardLogDestination(ctx, state.ApplicationStandardLogDestinationCreate{OrgID: org.ID, ActorID: e.acct.ID, Name: "Central logs", Kind: "http_json", TargetURL: "https://private-logs.example.com/ingest", AuthHeaderSealed: []byte("sealed-private-credential")})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := json.Marshal(appstandards.Definition{
		appstandards.LogDestinations: {Mode: appstandards.Mandatory, Value: json.RawMessage(`["` + d.ID + `"]`)},
		appstandards.EgressCIDRs:     {Mode: appstandards.Restricted, Value: json.RawMessage(`["8.8.8.0/24"]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: org.ID, ActorID: e.acct.ID, Slug: "view-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.store.PreviewApplicationStandardAssignment(ctx, org.ID, e.acct.ID, state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: org.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("view fixture review: %+v %v", p.Blockers, err)
	}
	if _, err := e.store.ApproveApplicationStandardReview(ctx, org.ID, e.acct.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "enrollment-service", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return org, app
}

func TestApplicationStandardEnrollmentAPIAndSDK(t *testing.T) {
	ctx := t.Context()
	e := setup(t, api.PlanPro)
	org, app := standardEnrollmentAPIFixture(ctx, t, e)
	server := httptest.NewServer(e.h)
	defer server.Close()
	client := api.NewClient(server.URL, e.key)
	got, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, app.ID)
	if err != nil || got.State != "pending" || got.DesiredRevision != 1 || got.PersistedRevision != 0 || got.ObservedRevision != 0 || got.InstalledEffective != nil || len(got.Adoptions) != 1 {
		t.Fatalf("pending enrollment view: %+v %v", got, err)
	}
	claim, err := e.store.ClaimApplicationStandardEnrollment(ctx, "enrollment-view-worker")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := e.store.MaterializeApplicationStandardEnrollment(ctx, claim)
	if err != nil || installed.State != "persisted" {
		t.Fatalf("view fixture installation: %+v %v", installed, err)
	}
	got, err = client.GetApplicationStandardEnrollment(ctx, org.Slug, app.ID)
	if err != nil || got.InstalledEffective == nil || got.PersistedRevision != got.DesiredRevision || got.ObservedRevision != 0 || len(got.InstalledEffective.Sources[appstandards.LogDestinations]) != 1 {
		t.Fatalf("installed enrollment view: %+v %v", got, err)
	}
	if _, err := e.store.SetApplicationStandardLocalIntent(ctx, org.ID, e.acct.ID, app.ID, state.ApplicationStandardLocalIntentRequest{ExpectedRevision: got.DesiredRevision, Settings: json.RawMessage(`{"egress_cidrs":["8.8.8.8/32"]}`)}); err != nil {
		t.Fatal(err)
	}
	got, err = client.GetApplicationStandardEnrollment(ctx, org.Slug, app.ID)
	if err != nil || got.State != "pending" || got.DesiredRevision != 2 || got.PersistedRevision != 1 || got.ObservedRevision != 0 || got.InstalledEffective == nil || string(got.LocalSettings[appstandards.EgressCIDRs]) != `["8.8.8.8/32"]` || string(got.InstalledEffective.Values[appstandards.EgressCIDRs]) != `["8.8.8.0/24"]` {
		t.Fatalf("desired intent replaced installed truth: %+v %v", got, err)
	}
	response := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/application-standard-enrollments/"+app.ID, nil, nil)
	for _, secret := range []string{"private-logs.example.com", "sealed-private-credential", "auth_header", "lease_owner", "base_settings"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("enrollment view exposed %s", secret)
		}
	}
}

func TestApplicationStandardEnrollmentAPIScope(t *testing.T) {
	ctx := t.Context()
	e := setup(t, api.PlanPro)
	org, app := standardEnrollmentAPIFixture(ctx, t, e)
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/"
	for _, id := range []string{"invalid", uuid.Nil.String()} {
		assertProblem(t, e.do(t, http.MethodGet, base+id, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	assertProblem(t, e.do(t, http.MethodGet, base+uuid.NewString(), nil, nil), http.StatusNotFound, api.CodeNotFound)
	other := seedSharedOrgWithOwner(t, e, "view-other", "Other", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standard-enrollments/"+app.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	if _, err := e.store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodGet, base+app.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestApplicationStandardEnrollmentAPIReadAuthority(t *testing.T) {
	ctx := t.Context()
	e := setup(t, api.PlanPro)
	org, app := standardEnrollmentAPIFixture(ctx, t, e)
	reader, err := e.store.CreateAccount(ctx, "enrollment-reader@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, reader.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, reader.ID, hash, "enrollment read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	read := e
	read.acct, read.key = reader, plain
	path := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID
	for _, role := range []state.OrgRole{state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		if err := e.store.UpdateOrgMemberRole(ctx, org.ID, reader.ID, role); err != nil {
			t.Fatal(err)
		}
		if got := read.do(t, http.MethodGet, path, nil, nil); got.Code != http.StatusOK {
			t.Fatalf("%s cannot inspect enrollment: %d %s", role, got.Code, got.Body)
		}
	}
	plain, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, reader.ID, hash, "write without read", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	read.key = plain
	if got := read.do(t, http.MethodGet, path, nil, nil); got.Code != http.StatusForbidden {
		t.Fatalf("write scope bypassed enrollment read scope: %d %s", got.Code, got.Body)
	}
}
