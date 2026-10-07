// ADR-639: subject queries use the existing authenticated history boundary.
package main

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationSubjectHistoryOptions(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"app_id=app&scope=default&subject_type=order&subject_id=ord%2F42%26", true},
		{"app_id=app&scope=default&subject_type=order&subject_id=", false},
		{"app_id=app&scope=default&subject_type=order&subject_type=invoice&subject_id=42", false},
		{"app_id=app&scope=default&subject_type=order&subject_id=42&owner=bob", false},
	} {
		opts, err := operationHistoryOptions(httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil))
		if (err == nil) != tc.want {
			t.Fatalf("query %s: %+v %v", tc.query, opts, err)
		}
		if tc.want && (opts.SubjectType != "order" || opts.SubjectID != "ord/42&") {
			t.Fatalf("opaque reference changed: %+v", opts)
		}
	}
}

func TestDashboardOperationSubjects(t *testing.T) {
	f := newDashboardOperationFixture(t)
	dep, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := f.def.Spec
	spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
	def, err := f.store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: f.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: f.app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "ord</code><script>alert(42)</script>&é"
	var ids []string
	for _, id := range []string{ref, ref, "another"} {
		input, _ := json.Marshal(map[string]string{"order_id": id})
		op, _, err := f.store.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: f.account.ID, DefinitionID: def.ID, PlatformTenantID: f.tenant.ID, IdempotencyKey: id + strings.Repeat("x", len(ids)), Input: input})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, op.ID)
	}
	base := dashboardCustomerOperationsURL(f.app.Slug)
	query := url.Values{"scope": {dep.Scope}, "subject_type": {"order"}, "subject_id": {ref}, "limit": {"1"}}
	list := f.get(t, base+"?"+query.Encode(), http.StatusOK)
	body := list.Body.String()
	if !strings.Contains(body, html.EscapeString(ref)) || strings.Contains(body, "<script>alert(42)</script>") || strings.Contains(body, ids[2]) {
		t.Fatal("reference display was unsafe or lookup was ignored")
	}
	// Next-page projection preserves both selectors and does not leak the other entity.
	nextLink := regexp.MustCompile(`<a href="([^"]+)">Older operations`).FindStringSubmatch(body)
	if len(nextLink) != 2 {
		t.Fatal("missing reference pagination")
	}
	nextURL, err := url.Parse(html.UnescapeString(nextLink[1]))
	if err != nil || nextURL.Query().Get("subject_type") != "order" || nextURL.Query().Get("subject_id") != ref {
		t.Fatal("pagination lost reference filters")
	}
	nextBody := f.get(t, nextURL.String(), http.StatusOK).Body.String()
	if strings.Contains(nextBody, ids[2]) || !strings.Contains(nextBody, html.EscapeString(ref)) {
		t.Fatal("reference pagination exposed another entity")
	}
	opts, err := dashboardOperationListOptions(httptest.NewRequest(http.MethodGet, base+"?"+query.Encode(), nil), f.app.ID)
	if err != nil || opts.SubjectType != "order" || opts.SubjectID != ref {
		t.Fatalf("dashboard lost selector: %+v %v", opts, err)
	}
	related := dashboardOperationSubjectURL(f.app.Slug, dep.Scope, f.tenant.ID, &api.OperationSubject{Type: "order", ID: ref})
	u, err := url.Parse(related)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("tenant_id") != f.tenant.ID || u.Query().Get("scope") != dep.Scope || u.Query().Get("subject_id") != ref {
		t.Fatalf("related link lost owner/environment/reference: %s", related)
	}
	detail := f.get(t, base+"/"+ids[0], http.StatusOK)
	if !strings.Contains(detail.Body.String(), "Business reference") || !strings.Contains(detail.Body.String(), html.EscapeString(related)) {
		t.Fatal("detail omitted scoped related-operation link")
	}
	query.Del("subject_id")
	f.get(t, base+"?"+query.Encode(), http.StatusBadRequest)
}

func TestOperationSubjectPairedFiltersAtStoreBoundary(t *testing.T) {
	// The HTTP parser never substitutes ownership from a reference. Pair validation
	// occurs at the store boundary, shared by account and customer routes.
	f := newDashboardOperationFixture(t)
	for _, opts := range []api.OperationListOptions{
		{AppID: f.app.ID, Scope: f.def.Scope, SubjectType: "order"},
		{AppID: f.app.ID, Scope: f.def.Scope, SubjectID: "42"},
	} {
		if _, err := f.store.ListAccountOperations(t.Context(), f.account.ID, opts); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("partial operator reference: %v", err)
		}
		if _, err := f.store.ListPlatformTenantOperations(t.Context(), f.account.ID, f.tenant.ID, opts); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("partial customer reference: %v", err)
		}
	}
}
