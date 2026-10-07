package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func standardReviewAPIFixture(t *testing.T, e testEnv) (state.Org, state.App, api.ApplicationStandardReviewRequest) {
	t.Helper()
	ctx := t.Context()
	org := seedSharedOrgWithOwner(t, e, "review-view", "Review view", api.PlanPro)
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "preview-service", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: org.ID, ActorID: e.acct.ID, Slug: "preview-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"restricted","value":["8.8.8.0/24"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return org, app, api.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: org.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
}

func TestApplicationStandardReviewPublicPreviewDoesNotActivate(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, app, req := standardReviewAPIFixture(t, e)
	before, err := e.store.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	client := api.NewClient(server.URL, e.key)
	p, err := client.PreviewApplicationStandardAssignment(t.Context(), org.Slug, req)
	if err != nil || len(p.Applications) != 1 || p.Applications[0].AppID == "" || len(p.Blockers) != 0 || p.ApprovalHash == "" || !p.ExpiresAt.After(p.CreatedAt) {
		t.Fatalf("preview: %+v %v", p, err)
	}
	got, err := client.GetApplicationStandardReview(t.Context(), org.Slug, p.ID)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("saved review: %+v %v", got, err)
	}
	assignments, err := e.store.ListApplicationStandardAssignments(t.Context(), org.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("preview activated assignments: %+v %v", assignments, err)
	}
	after, err := e.store.AppByID(t.Context(), app.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("preview changed app: %v", err)
	}
	enrollment, err := e.store.GetApplicationStandardEnrollment(t.Context(), org.ID, app.ID)
	if err != nil || enrollment.State != "unmanaged" || len(enrollment.Adoptions) != 0 || enrollment.DesiredRevision != 1 || enrollment.PersistedRevision != 0 || enrollment.ObservedRevision != 0 {
		t.Fatalf("preview changed unmanaged enrollment: %+v %v", enrollment, err)
	}

	op, err := e.store.ApproveApplicationStandardReview(t.Context(), org.ID, e.acct.ID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := client.GetApplicationStandardOperation(t.Context(), org.Slug, op.ID)
	if err != nil || progress.State != "queued" || len(progress.Targets) != 1 || progress.Targets[0].State != "queued" || progress.Targets[0].DesiredRevision != 0 {
		t.Fatalf("queued progress: %+v %v", progress, err)
	}
	for _, path := range []string{"application-standard-reviews/" + p.ID, "application-standard-operations/" + op.ID} {
		body := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/"+path, nil, nil).Body.String()
		for _, private := range []string{"base_settings", "account_id", "approval_input", "lease_owner", "auth_header", "artifact"} {
			if strings.Contains(body, private) {
				t.Fatalf("public response exposed %s", private)
			}
		}
	}
	other := seedSharedOrgWithOwner(t, e, "review-foreign", "Foreign", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standard-reviews/"+p.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standard-operations/"+op.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	for _, kind := range []string{"reviews", "operations"} {
		for _, id := range []string{"bad", uuid.Nil.String()} {
			assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/application-standard-"+kind+"/"+id, nil, nil), http.StatusBadRequest, api.CodeValidation)
		}
	}
}

func TestApplicationStandardReviewPublicAuthority(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, _, req := standardReviewAPIFixture(t, e)
	p, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := e.store.CreateAccount(t.Context(), "standard-view-reader@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddOrgMember(t.Context(), org.ID, reader.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	read := standardAPIReader(t, e, reader, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	base := "/v1/orgs/" + org.Slug + "/application-standard-reviews"
	for _, role := range []state.OrgRole{state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		if err := e.store.UpdateOrgMemberRole(t.Context(), org.ID, reader.ID, role); err != nil {
			t.Fatal(err)
		}
		if r := read.do(t, http.MethodGet, base+"/"+p.ID, nil, nil); r.Code != http.StatusOK {
			t.Fatalf("%s read: %d %s", role, r.Code, r.Body)
		}
		r := read.do(t, http.MethodPost, base, req, nil)
		want := http.StatusForbidden
		if role == state.OrgRoleAdmin {
			want = http.StatusCreated
		}
		if r.Code != want {
			t.Fatalf("%s preview: %d %s", role, r.Code, r.Body)
		}
	}
	read = standardAPIReader(t, e, reader, []string{api.ScopeDeployWrite})
	assertProblem(t, read.do(t, http.MethodGet, base+"/"+p.ID, nil, nil), http.StatusForbidden, api.CodeForbidden)
	read = standardAPIReader(t, e, reader, []string{api.ScopeAppsRead})
	assertProblem(t, read.do(t, http.MethodPost, base, req, nil), http.StatusForbidden, api.CodeForbidden)
	if err := e.store.RemoveOrgMember(t.Context(), org.ID, reader.ID); err != nil {
		t.Fatal(err)
	}
	if r := read.do(t, http.MethodGet, base+"/"+p.ID, nil, nil); r.Code == http.StatusOK {
		t.Fatal("removed member read saved review")
	}
}

func standardAPIReader(t *testing.T, e testEnv, account state.Account, scopes []string) testEnv {
	t.Helper()
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), account.ID, hash, "standards reader", scopes); err != nil {
		t.Fatal(err)
	}
	e.acct, e.key = account, key
	return e
}

func TestApplicationStandardExceptionsPublicHistoryAndDeadline(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, app := standardEnrollmentAPIFixture(t.Context(), t, e)
	claim, err := e.store.ClaimApplicationStandardEnrollment(t.Context(), "history-worker")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := e.store.MaterializeApplicationStandardEnrollment(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := e.store.ListApplicationStandardAssignments(t.Context(), org.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("fixture assignments: %+v %v", assignments, err)
	}
	x, err := e.store.ApproveApplicationStandardException(t.Context(), org.ID, e.acct.ID, app.ID, state.ApplicationStandardExceptionRequest{ExpectedRevision: enrollment.DesiredRevision, StandardID: assignments[0].StandardID, Version: 1, Field: appstandards.EgressCIDRs, Value: json.RawMessage(`["8.8.8.8/32"]`), Reason: "Collector maintenance", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	claim, err = e.store.ClaimApplicationStandardEnrollment(t.Context(), "history-worker")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err = e.store.MaterializeApplicationStandardEnrollment(t.Context(), claim)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	client := api.NewClient(server.URL, e.key)
	list, err := client.ListApplicationStandardExceptions(t.Context(), org.Slug, app.ID, "", 1)
	if err != nil || len(list.Exceptions) != 1 || list.Exceptions[0].Status != "active" || list.Exceptions[0].Reason != x.Reason || list.NextPageAfter != x.ID || list.AsOf.IsZero() {
		t.Fatalf("approval history: %+v %v", list, err)
	}
	got, err := client.GetApplicationStandardEnrollment(t.Context(), org.Slug, app.ID)
	if err != nil || got.InstalledExceptionExpiresAt == nil || !got.InstalledExceptionExpiresAt.Equal(x.ExpiresAt) || got.ObservedRevision != 0 {
		t.Fatalf("deadline: %+v %v", got, err)
	}
	next, err := client.ListApplicationStandardExceptions(t.Context(), org.Slug, app.ID, list.NextPageAfter, 1)
	if err != nil || len(next.Exceptions) != 0 || next.NextPageAfter != "" {
		t.Fatalf("cursor: %+v %v", next, err)
	}
	revoked, err := e.store.RevokeApplicationStandardException(t.Context(), org.ID, e.acct.ID, app.ID, x.ID, enrollment.DesiredRevision)
	if err != nil {
		t.Fatal(err)
	}
	list, err = client.ListApplicationStandardExceptions(t.Context(), org.Slug, app.ID, "", 100)
	if err != nil || list.Exceptions[0].Status != "revoked" || list.Exceptions[0].RevokedAt == nil {
		t.Fatalf("revoked history: %+v %v", list, err)
	}
	atExpiry := applicationStandardExceptionResponse(x, x.ExpiresAt)
	if atExpiry.Status != "expired" || applicationStandardExceptionResponse(revoked, x.ExpiresAt).Status != "revoked" {
		t.Fatal("deadline or revocation precedence is wrong")
	}
	base := "/v1/orgs/" + org.Slug + "/application-standard-enrollments/" + app.ID + "/exceptions"
	for _, query := range []string{"?after=bad", "?after=" + uuid.Nil.String(), "?limit=0", "?limit=101"} {
		assertProblem(t, e.do(t, http.MethodGet, base+query, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	other := seedSharedOrgWithOwner(t, e, "history-foreign", "Foreign", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standard-enrollments/"+app.ID+"/exceptions", nil, nil), http.StatusNotFound, api.CodeNotFound)
	if _, err := e.store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodGet, base, nil, nil), http.StatusNotFound, api.CodeNotFound)
}
