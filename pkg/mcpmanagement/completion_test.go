package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func completionParams(field, prefix string, args map[string]string) *mcp.CompleteParams {
	return &mcp.CompleteParams{Ref: &mcp.CompleteReference{Type: "ref/prompt", Name: "gregale_rollout_diagnosis"}, Argument: mcp.CompleteParamsArgument{Name: field, Value: prefix}, Context: &mcp.CompleteContext{Arguments: args}}
}

func TestCompletionNamedConsentAndDeploymentScope(t *testing.T) {
	f, _, opts := namedStageOptions(t)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/deployments" {
			if r.URL.Query().Get("limit") != "100" {
				t.Error("history request is not bounded")
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "stage-base", AppID: "app-one", Scope: "staging"}, {ID: "prod-release", AppID: "app-one", Scope: "production"}, {ID: "default-release", AppID: "app-one", Scope: "default"}}, NextBefore: "next-page"})
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	for _, version := range []string{"2025-06-18", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			cs, ctx := connectConfiguredTestServer(t, b, opts, version)
			if cs.InitializeResult().Capabilities.Completions == nil {
				t.Fatal("completion capability missing")
			}
			for _, tc := range []struct {
				field, prefix string
				args          map[string]string
				want          []string
				more          bool
			}{
				{"project", "sh", nil, []string{"shop"}, false},
				{"environment", "", map[string]string{"project": "shop"}, []string{"staging"}, false},
				{"environment", "prod", map[string]string{"project": "shop"}, []string{}, false},
				{"environment", "", nil, []string{}, false},
				{"app", "a", map[string]string{"project": "shop", "environment": "staging"}, []string{"alpha"}, false},
				{"app", "", map[string]string{"project": "shop"}, []string{}, false},
				{"deployment_id", "stage", map[string]string{"app": "alpha", "project": "shop", "environment": "staging"}, []string{"stage-base"}, true},
				{"deployment_id", "", nil, []string{}, false},
			} {
				res, err := cs.Complete(ctx, completionParams(tc.field, tc.prefix, tc.args))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(res.Completion.Values, tc.want) || res.Completion.HasMore != tc.more {
					t.Fatalf("%s: %+v", tc.field, res.Completion)
				}
				if tc.more && res.Completion.Total != 0 {
					t.Fatal("partial history claimed an exact total")
				}
			}
			denied := completionParams("app", "", map[string]string{"project": "shop", "environment": "production"})
			if _, err := cs.Complete(ctx, denied); err == nil {
				t.Fatal("unconsented environment suggested apps")
			}
			for _, ref := range []string{"gregale://projects/{project}/environments/{environment}/apps/{app}/health", "gregale://projects/{project}/environments/{environment}/apps/{app}/release", "gregale://projects/{project}/environments/{environment}/apps/{app}/budgets"} {
				p := completionParams("app", "", nil)
				p.Ref = &mcp.CompleteReference{Type: "ref/resource", URI: ref}
				res, err := cs.Complete(ctx, p)
				if err != nil || len(res.Completion.Values) != 0 {
					t.Fatalf("incomplete named selector: %+v %v", res, err)
				}
				p.Context.Arguments = map[string]string{"project": "shop", "environment": "staging"}
				res, err = cs.Complete(ctx, p)
				if err != nil || !reflect.DeepEqual(res.Completion.Values, []string{"alpha"}) {
					t.Fatalf("template: %+v %v", res, err)
				}
			}
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 {
		t.Fatal("completion performed writes")
	}
}

func TestCompletionRejectsInvalidAndRecreatedTargets(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	invalid := []*mcp.CompleteParams{
		completionParams("secret", "", nil), completionParams("app", "../", nil), completionParams("app", "", map[string]string{"token": "secret"}),
		completionParams("app", "", map[string]string{"project": "shop\n"}),
	}
	p := completionParams("app", "", nil)
	p.Ref.Name = "unknown"
	invalid = append(invalid, p)
	p = completionParams("app", "", nil)
	p.Ref = &mcp.CompleteReference{Type: "ref/resource", URI: "gregale://default/apps/alpha/health"}
	invalid = append(invalid, p)
	for _, p := range invalid {
		if _, err := cs.Complete(ctx, p); err == nil {
			t.Fatal("invalid completion accepted")
		}
	}
	f.mu.Lock()
	f.envID = "replacement"
	f.mu.Unlock()
	if _, err := cs.Complete(ctx, completionParams("environment", "", map[string]string{"project": "shop"})); err == nil {
		t.Fatal("recreated environment exposed")
	}
}

func TestCompletionDefaultFilteringBoundsAndSorting(t *testing.T) {
	apps := []api.AppResponse{{ID: "forbidden", Slug: "hidden"}}
	for i := range 105 {
		apps = append(apps, api.AppResponse{ID: fmt.Sprintf("id-%03d", i), Slug: fmt.Sprintf("app-%03d", 104-i)})
	}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(apps)
	}))
	t.Cleanup(b.Close)
	cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL}, "2026-07-28")
	res, err := cs.Complete(ctx, completionParams("app", "app-", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Completion.Values) != 100 || !res.Completion.HasMore || res.Completion.Total != 0 || res.Completion.Values[0] != "app-000" || res.Completion.Values[99] != "app-099" {
		t.Fatalf("bounded completion: %+v", res.Completion)
	}
	cs, ctx = connectConfiguredTestServer(t, b, Options{APIBase: b.URL, AllowedApps: map[string]string{"app-104": "id-000"}}, "2026-07-28")
	res, err = cs.Complete(ctx, completionParams("app", "", nil))
	if err != nil || !reflect.DeepEqual(res.Completion.Values, []string{"app-104"}) {
		t.Fatalf("consent filter: %+v %v", res, err)
	}
}

func TestCompletionHTTPAndInvalidHistory(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid_history_%v", bad), func(t *testing.T) {
			f, _, opts := namedStageOptions(t)
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha/deployments" {
					appID := "app-one"
					if bad {
						appID = "another-app"
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "stage-base", AppID: appID, Scope: "staging"}}})
					return
				}
				f.serve(w, r)
			}))
			t.Cleanup(b.Close)
			opts.APIBase = b.URL
			h := connectSubscriptionHarness(t, b, opts, "2026-07-28", true)
			res, err := h.client.Complete(h.ctx, completionParams("deployment_id", "", map[string]string{"app": "alpha", "project": "shop", "environment": "staging"}))
			if bad {
				if err == nil {
					t.Fatal("foreign app history exposed")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(res.Completion.Values, []string{"stage-base"}) {
				t.Fatalf("HTTP completion: %+v %v", res, err)
			}
		})
	}
}

func TestCompletionLocalRegistryAndSelectedApps(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	for _, tc := range []struct {
		apps map[string]string
		want []string
	}{
		{nil, []string{"shop"}}, {map[string]string{"alpha": "app-one"}, []string{"shop"}}, {map[string]string{"beta": "app-other"}, []string{}},
	} {
		cs, ctx := connectConfiguredTestServer(t, b, Options{APIBase: b.URL, AllowedApps: tc.apps}, "2026-07-28")
		res, err := cs.Complete(ctx, completionParams("project", "", nil))
		if err != nil || !reflect.DeepEqual(res.Completion.Values, tc.want) {
			t.Fatalf("local registry: %+v %v", res, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commits) != 0 {
		t.Fatal("local discovery wrote intent")
	}
}
