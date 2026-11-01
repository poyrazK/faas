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
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
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

func labelPath(t *testing.T, e apidserver.TestEnv, path string, change api.LabelChange) map[string]string {
	t.Helper()
	rec := e.Do(t, http.MethodPatch, path, change, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s: %d %s", path, rec.Code, rec.Body)
	}
	var out api.LabelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Labels
}

// Crons and jobs carry labels as apps do (ADR-20261031100025 step 5): a PATCH
// writes them, the read and the list show them, and ?selector= filters the
// list. A selected job list is filtered whole and then paged.
func TestCronAndJobLabelsAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "labelled-crons", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	var crons []string
	for _, path := range []string{"/billing", "/search"} {
		c, err := store.CreateCron(ctx, app.ID, "* * * * *", path, true)
		if err != nil {
			t.Fatal(err)
		}
		crons = append(crons, c.ID)
	}
	for _, name := range []string{"billing-report", "billing-export", "search-index"} {
		if _, err := store.JobCreate(ctx, acct.ID, name, "batch", "registry.example/job:v1", []string{"/bin/true"}, 128, 30, 1, 0, nil); err != nil {
			t.Fatal(err)
		}
	}

	if got := labelPath(t, e, "/v1/crons/"+crons[0]+"/labels", api.LabelChange{Set: map[string]string{"team": "billing"}}); got["team"] != "billing" {
		t.Fatalf("cron labels = %v", got)
	}
	labelPath(t, e, "/v1/crons/"+crons[1]+"/labels", api.LabelChange{Set: map[string]string{"team": "search"}})
	labelPath(t, e, "/v1/jobs/billing-report/labels", api.LabelChange{Set: map[string]string{"team": "billing"}})
	labelPath(t, e, "/v1/jobs/billing-export/labels", api.LabelChange{Set: map[string]string{"team": "billing"}})
	labelPath(t, e, "/v1/jobs/search-index/labels", api.LabelChange{Set: map[string]string{"team": "search"}})

	rec := e.Do(t, http.MethodGet, "/v1/crons?selector=team%3Dbilling", nil, nil)
	var cronList []api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cronList); err != nil || len(cronList) != 1 || cronList[0].ID != crons[0] || cronList[0].Labels["team"] != "billing" {
		t.Fatalf("crons for team=billing = %+v (%d %s)", cronList, rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodGet, "/v1/crons/"+crons[1], nil, nil)
	var cron api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cron); err != nil || cron.Labels["team"] != "search" {
		t.Fatalf("cron read = %+v (%d %s)", cron, rec.Code, rec.Body)
	}

	rec = e.Do(t, http.MethodGet, "/v1/jobs?selector=team%3Dbilling&limit=1", nil, nil)
	var page api.ListJobsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Total != 2 || len(page.Jobs) != 1 || page.NextOffset != 1 || page.Jobs[0].Labels["team"] != "billing" {
		t.Fatalf("first selected job page = %+v (%d %s)", page, rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodGet, "/v1/jobs?selector=team%3Dbilling&limit=1&offset=1", nil, nil)
	page = api.ListJobsResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Total != 2 || len(page.Jobs) != 1 || page.NextOffset != -1 || !strings.HasPrefix(page.Jobs[0].Name, "billing-") {
		t.Fatalf("last selected job page = %+v (%d %s)", page, rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodGet, "/v1/jobs/search-index", nil, nil)
	var job api.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job.Labels["team"] != "search" {
		t.Fatalf("job read = %+v (%d %s)", job, rec.Code, rec.Body)
	}

	refusals := []struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		{http.MethodPatch, "/v1/crons/" + crons[0] + "/labels", api.LabelChange{Set: map[string]string{"gregale.dev/x": "y"}}, http.StatusBadRequest, labels.CodeReservedKey},
		{http.MethodPatch, "/v1/crons/00000000-0000-4000-8000-000000000009/labels", api.LabelChange{Set: map[string]string{"a": "b"}}, http.StatusNotFound, ""},
		{http.MethodPatch, "/v1/jobs/missing-job/labels", api.LabelChange{Set: map[string]string{"a": "b"}}, http.StatusNotFound, ""},
		{http.MethodGet, "/v1/jobs?selector=%3Dx", nil, http.StatusBadRequest, labels.CodeInvalidSelector},
		{http.MethodGet, "/v1/crons?selector=a+in+%28b", nil, http.StatusBadRequest, labels.CodeInvalidSelector},
	}
	for _, tc := range refusals {
		if rec := e.Do(t, tc.method, tc.path, tc.body, nil); rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
			t.Errorf("%s %s = %d %s, want %d %s", tc.method, tc.path, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
}

// A cron on another account's app is a 404, as on every cron route.
func TestCronLabelsAPI_OtherAccount(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	other, err := store.CreateAccount(t.Context(), "other-labels@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.CreateCron(t.Context(), app.ID, "* * * * *", "/x", true)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/crons/"+c.ID+"/labels", api.LabelChange{Set: map[string]string{"a": "b"}}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign cron label write = %d %s, want 404", rec.Code, rec.Body)
	}
}

// A source deploy sets the labels its manifest declares and removes none
// (ADR-20261031100025 §7); while the capability is off they are ignored.
func TestSourceRefManifestLabels(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "manifest-labels", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	labelApp(t, e, "manifest-labels", api.LabelChange{Set: map[string]string{"owner": "ops", "tier": "api"}})

	manifest := &gregalemanifest.Manifest{Labels: map[string]string{"team": "billing", "tier": "web"}}
	if _, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, manifest, state.DefaultEnvScope, false); problem != nil {
		t.Fatalf("apply manifest: %+v", problem)
	}
	rec := e.Do(t, http.MethodGet, "/v1/apps/manifest-labels", nil, nil)
	var got api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"owner": "ops", "team": "billing", "tier": "web"}; !maps.Equal(got.Labels, want) {
		t.Fatalf("labels after a manifest deploy = %v, want %v", got.Labels, want)
	}

	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	gated := &gregalemanifest.Manifest{Labels: map[string]string{"gated": "yes"}}
	if _, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, gated, state.DefaultEnvScope, false); problem != nil {
		t.Fatalf("apply manifest with the capability off: %+v", problem)
	}
	l, err := store.AppLabels(t.Context(), app.ID)
	if err != nil || l["gated"] != "" {
		t.Fatalf("labels with the capability off = %v, %v; want the manifest's ignored", l, err)
	}
}
