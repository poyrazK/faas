package apidserver_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func labelApp(t *testing.T, e apidserver.TestEnv, slug string, change api.LabelChange) map[string]string {
	t.Helper()
	rec := e.Do(t, http.MethodPatch, "/v1/apps/"+slug+"/labels", change, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("label %s: %d %s", slug, rec.Code, rec.Body)
	}
	var out api.LabelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Labels
}

func listedSlugs(t *testing.T, e apidserver.TestEnv, path string) []string {
	t.Helper()
	rec := e.Do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	var apps []api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, a := range apps {
		slugs = append(slugs, a.Slug)
	}
	slices.Sort(slugs)
	return slugs
}

// Apps carry labels (ADR-20261031100025): a PATCH sets and removes them in
// one write, the app list and app read show them, and ?selector= filters the
// app and domain lists.
func TestAppLabelsAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	apps := map[string]string{}
	for _, slug := range []string{"billing-web", "billing-api", "search-web"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		apps[slug] = app.ID
	}
	if _, err := store.CreateCustomDomain(ctx, "api.billing.example.com", apps["billing-api"], "tok-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "search.example.com", apps["search-web"], "tok-2"); err != nil {
		t.Fatal(err)
	}

	got := labelApp(t, e, "billing-web", api.LabelChange{Set: map[string]string{"team": "billing", "tier": "web", "canary": ""}})
	if want := map[string]string{"team": "billing", "tier": "web", "canary": ""}; !maps.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	got = labelApp(t, e, "billing-web", api.LabelChange{Remove: []string{"canary", "absent"}, Set: map[string]string{"tier": "web"}})
	if want := map[string]string{"team": "billing", "tier": "web"}; !maps.Equal(got, want) {
		t.Fatalf("labels after remove = %v, want %v", got, want)
	}
	labelApp(t, e, "billing-api", api.LabelChange{Set: map[string]string{"team": "billing", "tier": "api"}})
	labelApp(t, e, "search-web", api.LabelChange{Set: map[string]string{"team": "search", "tier": "web"}})

	rec := e.Do(t, http.MethodGet, "/v1/apps/billing-web", nil, nil)
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil || app.Labels["team"] != "billing" {
		t.Fatalf("app read labels = %v (%d %s)", app.Labels, rec.Code, rec.Body)
	}

	selections := map[string][]string{
		"":                                      {"billing-api", "billing-web", "search-web"},
		"team%3Dbilling":                        {"billing-api", "billing-web"},
		"tier+in+%28web%29%2Cteam%21%3Dbilling": {"search-web"},
		"%21tier":                               nil,
		"team%2Ctier+notin+%28api%29":           {"billing-web", "search-web"},
	}
	for query, want := range selections {
		if got := listedSlugs(t, e, "/v1/apps?selector="+query); !slices.Equal(got, want) {
			t.Errorf("selector %q lists %v, want %v", query, got, want)
		}
	}

	rec = e.Do(t, http.MethodGet, "/v1/domains?selector=team%3Dbilling", nil, nil)
	var domains []api.CustomDomainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &domains); err != nil || len(domains) != 1 || domains[0].Domain != "api.billing.example.com" {
		t.Fatalf("domains for team=billing = %+v (%d %s)", domains, rec.Code, rec.Body)
	}
}

// Invalid changes and selectors are 400s with stable codes that name the bad
// key or requirement, and a write never passes the per-app limit.
func TestAppLabelsAPI_Refusals(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "refusals", Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	full := map[string]string{}
	for i := range api.LabelsPerResourceMax {
		full["k"+strings.Repeat("x", i)] = "v"
	}
	labelApp(t, e, "refusals", api.LabelChange{Set: full})

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"bad key", http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"bad key": "v"}}, http.StatusBadRequest, labels.CodeInvalidKey},
		{"bad value", http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"team": "-x"}}, http.StatusBadRequest, labels.CodeInvalidValue},
		{"reserved key", http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"gregale.dev/owner": "me"}}, http.StatusBadRequest, labels.CodeReservedKey},
		{"set and removed", http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"a": "b"}, Remove: []string{"a"}}, http.StatusBadRequest, labels.CodeInvalidChange},
		{"over the limit", http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"one-more": "v"}}, http.StatusBadRequest, labels.CodeLimitExceeded},
		{"unknown field", http.MethodPatch, "/v1/apps/refusals/labels", map[string]any{"labels": map[string]string{"a": "b"}}, http.StatusBadRequest, api.CodeValidation},
		{"missing app", http.MethodPatch, "/v1/apps/missing/labels", api.LabelChange{Set: map[string]string{"a": "b"}}, http.StatusNotFound, ""},
		{"bad selector", http.MethodGet, "/v1/apps?selector=tier+in+%28web", nil, http.StatusBadRequest, labels.CodeInvalidSelector},
		{"bad domain selector", http.MethodGet, "/v1/domains?selector=%3Dx", nil, http.StatusBadRequest, labels.CodeInvalidSelector},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.Do(t, tc.method, tc.path, tc.body, nil)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("%d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
		})
	}

	rec := e.Do(t, http.MethodPatch, "/v1/apps/refusals/labels", api.LabelChange{Set: map[string]string{"one-more": "v"}}, nil)
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Limit == nil || *p.Limit != api.LabelsPerResourceMax || p.Observed == nil || *p.Observed != api.LabelsPerResourceMax+1 {
		t.Fatalf("limit problem = %s", rec.Body)
	}
}

// While resource-labels is off for the caller, the label route is hidden, a
// selector is refused, and a plain list works without labels.
func TestAppLabelsAPI_CapabilityOff(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "gated", Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/gated/labels", api.LabelChange{Set: map[string]string{"a": "b"}}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("label write with the capability off = %d %s, want 404", rec.Code, rec.Body)
	}
	for _, path := range []string{"/v1/apps?selector=a%3Db", "/v1/domains?selector=a%3Db"} {
		if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeCapabilityNotReleased) {
			t.Errorf("%s with the capability off = %d %s", path, rec.Code, rec.Body)
		}
	}
	if got := listedSlugs(t, e, "/v1/apps"); !slices.Equal(got, []string{"gated"}) {
		t.Errorf("plain list with the capability off = %v", got)
	}

	srv.SeedPlatformFlag(capabilityFlag(t, "resource-labels"), platformflags.Definition{Enabled: true, Default: true})
	labelApp(t, e, "gated", api.LabelChange{Set: map[string]string{"a": "b"}})
	if got := listedSlugs(t, e, "/v1/apps?selector=a%3Db"); !slices.Equal(got, []string{"gated"}) {
		t.Errorf("selector with the capability on = %v", got)
	}
}
