package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func standardInventoryInstallOperation(t *testing.T, e testEnv, orgID, id string) {
	t.Helper()
	ctx := t.Context()
	op, err := e.store.GetApplicationStandardOperation(ctx, orgID, id)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := e.store.ClaimApplicationStandardOperation(ctx, "assignment-inventory-api-worker")
	if err != nil || claim.OperationID != id {
		t.Fatalf("operation claim: %+v %v", claim, err)
	}
	for range op.Targets {
		op, err = e.store.MaterializeNextApplicationStandardTarget(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.State != "waiting" {
		t.Fatalf("installation fabricated convergence: %+v", op)
	}
}

func TestApplicationStandardAssignmentInventoryAPIWorkflow(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, existing, req := standardReviewAPIFixture(t, e)
	ctx := t.Context()
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	client := api.NewClient(ts.URL, e.key)
	list, err := client.ListApplicationStandardAssignments(ctx, org.Slug, "", 1)
	if err != nil || list.Assignments == nil || len(list.Assignments) != 0 || list.NextPageAfter != "" {
		t.Fatalf("empty inventory: %+v %v", list, err)
	}
	plan, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, req)
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.ApproveApplicationStandardReview(ctx, org.Slug, plan.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: plan.ApprovalHash})
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.GetApplicationStandardAssignment(ctx, org.Slug, op.AssignmentID)
	if err != nil || first.Revision != 1 || !first.Active || first.AdmissionVersion != 1 || uuid.MustParse(first.CreatedBy) != uuid.MustParse(e.acct.ID) {
		t.Fatalf("current assignment: %+v %v", first, err)
	}
	standardInventoryInstallOperation(t, e, org.ID, op.ID)
	op, err = client.GetApplicationStandardOperation(ctx, org.Slug, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.AbortApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.PublishApplicationStandardVersion(ctx, org.Slug, "preview-baseline", api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"egress_cidrs":{"mode":"restricted","value":["8.8.8.0/24"]},"egress_extra_ports":{"mode":"mandatory","value":[8443]}}`)}); err != nil {
		t.Fatal(err)
	}
	update := plan.Request
	update.ExpectedRevision, update.AdmissionVersion = first.Revision, 2
	review, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, update)
	if err != nil {
		t.Fatal(err)
	}
	op, err = client.ApproveApplicationStandardReview(ctx, org.Slug, review.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: review.ApprovalHash})
	if err != nil {
		t.Fatal(err)
	}
	current, err := client.GetApplicationStandardAssignment(ctx, org.Slug, first.ID)
	if err != nil || current.Revision != 2 || current.AdmissionVersion != 2 || !current.Active || !current.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("reviewed update: %+v %v", current, err)
	}
	assertProblem(t, e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/application-standard-reviews", update, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	newly, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "new-version-service", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, newly.ID)
	if err != nil || len(inherited.Adoptions) != 1 || inherited.Adoptions[0].Version != 2 || inherited.ObservedRevision != 0 {
		t.Fatalf("new service admission: %+v %v", inherited, err)
	}
	older, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, existing.ID)
	if err != nil || len(older.Adoptions) != 1 || older.Adoptions[0].Version != 1 || older.ObservedRevision != 0 {
		t.Fatalf("inventory moved existing adoption: %+v %v", older, err)
	}
	if _, err = client.AbortApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	remove := review.Request
	remove.ExpectedRevision, remove.Active, remove.BatchSize = current.Revision, false, 2
	removal, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, remove)
	if err != nil {
		t.Fatal(err)
	}
	op, err = client.ApproveApplicationStandardReview(ctx, org.Slug, removal.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: removal.ApprovalHash})
	if err != nil {
		t.Fatal(err)
	}
	inactive, err := client.GetApplicationStandardAssignment(ctx, org.Slug, first.ID)
	if err != nil || inactive.Active || inactive.Revision != 3 || inactive.ID != first.ID || inactive.CreatedBy != first.CreatedBy || !inactive.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("retained deactivation: %+v %v", inactive, err)
	}
	list, err = client.ListApplicationStandardAssignments(ctx, org.Slug, "", 1)
	if err != nil || len(list.Assignments) != 1 || !reflect.DeepEqual(list.Assignments[0], inactive) || list.NextPageAfter != "" {
		t.Fatalf("inactive inventory: %+v %v", list, err)
	}
	next, err := client.ListApplicationStandardAssignments(ctx, org.Slug, inactive.ID, 1)
	if err != nil || next.Assignments == nil || len(next.Assignments) != 0 || next.NextPageAfter != "" {
		t.Fatalf("terminal page: %+v %v", next, err)
	}
	standardInventoryInstallOperation(t, e, org.ID, op.ID)
	for _, appID := range []string{existing.ID, newly.ID} {
		enrollment, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, appID)
		if err != nil || len(enrollment.Adoptions) != 0 || enrollment.ObservedRevision != 0 {
			t.Fatalf("reviewed removal: %+v %v", enrollment, err)
		}
	}
	afterRemoval, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "after-removal-service", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	unmanaged, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, afterRemoval.ID)
	if err != nil || len(unmanaged.Adoptions) != 0 || unmanaged.State != "unmanaged" || unmanaged.ObservedRevision != 0 {
		t.Fatalf("inactive assignment enrolled new service: %+v %v", unmanaged, err)
	}
	e.s.WithApplicationStandardMutationsEnabled(false)
	if _, err = client.GetApplicationStandardAssignment(ctx, org.Slug, first.ID); err != nil {
		t.Fatalf("mutation gate disabled reads: %v", err)
	}
	assertStandardMutationResponsePrivate(t, e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/application-standard-assignments/"+first.ID, nil, nil).Body.String())
}

func TestApplicationStandardAssignmentInventoryAPIAuthorityAndScope(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, _, req := standardReviewAPIFixture(t, e)
	p, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	op, err := e.store.ApproveApplicationStandardReview(t.Context(), org.ID, e.acct.ID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/application-standard-assignments"
	actor, err := e.store.CreateAccount(t.Context(), "inventory-reader@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	reader := standardAPIReader(t, e, actor, []string{api.ScopeAppsRead})
	for _, path := range []string{base, base + "/" + op.AssignmentID} {
		if got := e.do(t, http.MethodGet, path, nil, nil); got.Code != http.StatusOK {
			t.Fatalf("owner read: %d %s", got.Code, got.Body)
		}
	}
	for _, role := range []state.OrgRole{state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
		if err = e.store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, role); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{base, base + "/" + op.AssignmentID} {
			if got := reader.do(t, http.MethodGet, path, nil, nil); got.Code != http.StatusOK {
				t.Fatalf("role %s read: %d %s", role, got.Code, got.Body)
			}
		}
	}
	writer := standardAPIReader(t, e, actor, []string{api.ScopeDeployWrite})
	for _, path := range []string{base, base + "/" + op.AssignmentID} {
		assertProblem(t, writer.do(t, http.MethodGet, path, nil, nil), http.StatusForbidden, api.CodeForbidden)
	}
	other := seedSharedOrgWithOwner(t, e, "assignment-inventory-foreign", "Foreign", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodGet, "/v1/orgs/"+other.Slug+"/application-standard-assignments/"+op.AssignmentID, nil, nil), http.StatusNotFound, api.CodeNotFound)
	for _, id := range []string{"bad", uuid.Nil.String()} {
		assertProblem(t, e.do(t, http.MethodGet, base+"/"+id, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	for _, query := range []string{"?after=bad", "?after=" + uuid.Nil.String(), "?limit=0", "?limit=101"} {
		assertProblem(t, e.do(t, http.MethodGet, base+query, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
	if err = e.store.RemoveOrgMember(t.Context(), org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, base + "/" + op.AssignmentID} {
		if got := reader.do(t, http.MethodGet, path, nil, nil); got.Code == http.StatusOK {
			t.Fatal("removed member read inventory")
		}
	}
}

func TestApplicationStandardAssignmentInventoryAPIPaging(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, _, req := standardReviewAPIFixture(t, e)
	ctx := t.Context()
	e.s.WithApplicationStandardMutationsEnabled(true)
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	client := api.NewClient(ts.URL, e.key)
	ids := []string{}
	for _, name := range []string{"inventory-a", "inventory-b", "inventory-c"} {
		version, err := client.PublishApplicationStandardVersion(ctx, org.Slug, name, api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"}}`)})
		if err != nil {
			t.Fatal(err)
		}
		request := req
		request.StandardID = version.StandardID
		review, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, request)
		if err != nil {
			t.Fatal(err)
		}
		op, err := client.ApproveApplicationStandardReview(ctx, org.Slug, review.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: review.ApprovalHash})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, op.AssignmentID)
		if _, err = client.AbortApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(ids)
	first, err := client.ListApplicationStandardAssignments(ctx, org.Slug, "", 2)
	if err != nil || len(first.Assignments) != 2 || first.Assignments[0].ID != ids[0] || first.Assignments[1].ID != ids[1] || first.NextPageAfter != ids[1] {
		t.Fatalf("sentinel page: %+v %v", first, err)
	}
	last, err := client.ListApplicationStandardAssignments(ctx, org.Slug, strings.ToUpper(strings.ReplaceAll(first.NextPageAfter, "-", "")), 2)
	if err != nil || len(last.Assignments) != 1 || last.Assignments[0].ID != ids[2] || last.NextPageAfter != "" {
		t.Fatalf("last page: %+v %v", last, err)
	}
	terminal, err := client.ListApplicationStandardAssignments(ctx, org.Slug, ids[2], 2)
	if err != nil || terminal.Assignments == nil || len(terminal.Assignments) != 0 || terminal.NextPageAfter != "" {
		t.Fatalf("terminal page: %+v %v", terminal, err)
	}
}
