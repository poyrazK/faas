// adr: 20261024160000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func recoveryResourceView(t *testing.T, result *mcp.ReadResourceResult, uri string) savedPlanRecovery {
	t.Helper()
	if result == nil || len(result.Contents) != 1 || result.Contents[0].URI != uri || result.Contents[0].MIMEType != "application/json" {
		t.Fatal("missing canonical JSON recovery resource")
	}
	text := result.Contents[0].Text
	var view savedPlanRecovery
	if json.Unmarshal([]byte(text), &view) != nil || view.ReviewKind != "saved_plan_recovery_recipe" || view.Freshness != "not_checked" || view.NativeCompletion != "not_observed" {
		t.Fatal("recipe claimed fresh evidence or completion")
	}
	for _, private := range []string{testToken, "PRIVATE_BUDGET_REASON", "PRIVATE_ENV_REASON", "PRIVATE_TOKEN", "PRIVATE_APPROVAL", "PRIVATE_CONFIG", `"request_id"`, `"approval_token"`, `"promotion_hash"`, `"reason"`, `"proposed"`} {
		if strings.Contains(text, private) {
			t.Fatalf("resource exposed saved/native material %s", private)
		}
	}
	return view
}

func TestSavedRecoveryResourceMCPRecipes(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, kind := range []string{"default", "named", "environment"} {
			t.Run(protocol+"/"+kind, func(t *testing.T) {
				var cs *mcp.ClientSession
				var ctx = t.Context()
				var args getActionArgs
				var promptName string
				var input map[string]string
				var assertPassive func()
				switch kind {
				case "default":
					f, _, _ := newReleaseFixture(t, false)
					// Use the requested protocol for discovery/retrieval too.
					b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
					t.Cleanup(b.Close)
					cs, ctx = connectConfiguredTestServer(t, b, Options{APIBase: b.URL, Token: testToken}, protocol)
					id := planID(t, callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false))
					args = getActionArgs{App: "alpha", PlanID: id}
					promptName = "gregale_saved_plan_review"
					input = map[string]string{"app": "alpha", "plan_id": id}
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 {
							t.Fatal("recipe executed release")
						}
					}
				case "named":
					f, b, opts := newBudgetFixture(t)
					opts.AllowWrites = false
					cs, ctx = connectConfiguredTestServer(t, b, opts, protocol)
					id := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
					args = budgetHistorySelector(id)
					promptName = "gregale_saved_plan_review"
					input = map[string]string{"app": "alpha", "project": "shop", "environment": "staging", "plan_id": id}
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 {
							t.Fatal("recipe mutated policy")
						}
					}
				case "environment":
					f, b := newEnvironmentFixture(t)
					f.protected = true
					cs, ctx = connectConfiguredTestServer(t, b, environmentTestOptions(b, false), protocol)
					id := planID(t, callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false))
					args = getActionArgs{Project: "shop", Environment: "production", PlanID: id}
					promptName = "gregale_environment_plan_review"
					input = map[string]string{"project": "shop", "environment": "production", "plan_id": id}
					assertPassive = func() {
						f.mu.Lock()
						defer f.mu.Unlock()
						if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
							t.Fatal("recipe started native/approval activity")
						}
					}
				}
				uri := savedPlanRecoveryURI(args, kind == "environment")
				resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
				if err != nil {
					t.Fatal(err)
				}
				view := recoveryResourceView(t, resource, uri)
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: promptName, Arguments: input})
				if err != nil {
					t.Fatal(err)
				}
				var expected any = args
				if kind == "environment" {
					expected = getEnvironmentActionArgs{Project: args.Project, Environment: args.Environment, PlanID: args.PlanID}
				}
				calls := savedPromptCalls(t, prompt, expected, kind == "environment")
				if !reflect.DeepEqual(calls, view.Workflow) || !strings.Contains(view.Guidance, "preserve the original retry") || !strings.Contains(view.Guidance, "Do not apply changes") {
					t.Fatal("resource differs from guarded prompt workflow")
				}
				templates, err := cs.ListResourceTemplates(ctx, nil)
				if err != nil || len(templates.ResourceTemplates) != 11 {
					t.Fatal("recovery templates not discoverable")
				}
				template := savedDefaultPlanTemplate
				if kind == "named" {
					template = savedNamedPlanTemplate
				}
				if kind == "environment" {
					template = savedEnvironmentPlanTemplate
				}
				found := false
				for _, entry := range templates.ResourceTemplates {
					found = found || entry.URITemplate == template
				}
				if !found {
					t.Fatal("exact template missing")
				}
				complete := completionParams("plan_id", args.PlanID[:8], input)
				complete.Ref = &mcp.CompleteReference{Type: "ref/resource", URI: template}
				values, err := cs.Complete(ctx, complete)
				if err != nil || len(values.Completion.Values) != 0 {
					t.Fatal("private recovery IDs enumerated")
				}
				assertPassive()
			})
		}
	}
}

func TestSavedRecoveryResourceCanonicalURI(t *testing.T) {
	m := &management{}
	id := strings.Repeat("a", 64)
	good := []string{"gregale://default/apps/alpha/plans/" + id + "/review", "gregale://projects/shop/environments/staging/apps/alpha/plans/" + id + "/review", "gregale://projects/shop/environments/production/plans/" + id + "/review"}
	for _, uri := range good {
		a, environment, err := m.parseSavedPlanResource(uri)
		if err != nil || savedPlanRecoveryURI(a, environment) != uri {
			t.Fatal("canonical URI failed round trip")
		}
		for _, bad := range []string{uri + "?", uri + "?token=private", uri + "#", uri + "#fragment", uri + "/", strings.Replace(uri, "gregale://", "https://", 1), strings.Replace(uri, "gregale://", "gregale://user:password@", 1), strings.Replace(uri, id, strings.ToUpper(id), 1), strings.Replace(uri, id, "short", 1), strings.Replace(uri, "/plans/", "//plans/", 1), strings.Replace(uri, "/review", "/secrets", 1)} {
			if _, _, err := m.parseSavedPlanResource(bad); err == nil {
				t.Fatalf("accepted noncanonical URI: %s", bad)
			}
		}
	}
	for _, uri := range []string{"gregale://default/apps/../plans/" + id + "/review", "gregale://default/apps/al%70ha/plans/" + id + "/review", "gregale://projects//environments/production/plans/" + id + "/review", "gregale://default/apps/" + testToken + "/plans/" + id + "/review"} {
		m.opts.Token = testToken
		if _, _, err := m.parseSavedPlanResource(uri); err == nil {
			t.Fatal("unsafe recovery selector accepted")
		}
	}
}

func TestSavedRecoveryResourceRestartAmbiguityAndExpiry(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "recipe-resource"
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := budgetHistoryPlan(t, m)
	uri := savedPlanRecoveryURI(budgetHistorySelector(id), false)
	req := &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}}
	before, err := m.readSavedPlanResource(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	if _, err := m.applyRouteBudgets(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("expected lost response")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore, opts.AllowWrites = next, false
	_, restored, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.readSavedPlanResource(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	oldRecipe := recoveryResourceView(t, before, uri)
	newRecipe := recoveryResourceView(t, after, uri)
	if !reflect.DeepEqual(oldRecipe.Workflow.History, newRecipe.Workflow.History) || !reflect.DeepEqual(oldRecipe.Workflow.Review, newRecipe.Workflow.Review) || !reflect.DeepEqual(oldRecipe.Workflow.Check, newRecipe.Workflow.Check) || oldRecipe.Workflow.Outcome != nil || newRecipe.Workflow.Outcome == nil || newRecipe.Workflow.Observation == nil {
		t.Fatal("restart changed original calls or lost outcome lookup")
	}
	recoveryResourceView(t, after, uri)
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(snapshot, unchanged) {
		t.Fatal("resource persisted state")
	}
	restored.plans[id].ExpiresAt = time.Now().Add(-time.Second)
	expired, err := restored.readSavedPlanResource(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	expiredRecipe := recoveryResourceView(t, expired, uri)
	if expiredRecipe.Workflow.Outcome == nil || expiredRecipe.Workflow.Review.Tool != "" || expiredRecipe.Workflow.Check.Tool != "" {
		t.Fatal("expired recipe advertised mutation-plan recovery")
	}
	if restored.plans[id] == nil {
		t.Fatal("read pruned original plan")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatal("recipe read retried native mutation")
	}
}

func TestSavedRecoveryResourceFinalGuards(t *testing.T) {
	for _, change := range []string{"consent", "expiry", "removed", "storage"} {
		t.Run(change, func(t *testing.T) {
			_, b, opts := newBudgetFixture(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "recipe-guard"
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := budgetHistoryPlan(t, m)
			guard, err := m.savedPlanRecoveryResource(t.Context(), budgetHistorySelector(id), false)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "expiry":
				m.plans[id].ExpiresAt = time.Now().Add(-time.Second)
			case "removed":
				delete(m.plans, id)
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := checkedResourceValue(t.Context(), guard); err == nil {
				t.Fatal("final resource guard released unavailable recipe")
			}
		})
	}
}

func TestSavedRecoveryResourcePassiveRetrieval(t *testing.T) {
	f, _ := newEnvironmentFixture(t)
	f.protected = true
	var armed atomic.Bool
	var forbidden atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() && (r.Method != http.MethodGet || strings.Contains(r.URL.Path, "promotion-preview") || strings.Contains(r.URL.Path, "approval-requests") || strings.Contains(r.URL.Path, "/promotions/")) {
			forbidden.Add(1)
			http.Error(w, "evidence read forbidden", 503)
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(b.Close)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, false))
	id := planHistoryEnvironment(t, m, false)
	armed.Store(true)
	uri := savedPlanRecoveryURI(getActionArgs{Project: "shop", Environment: "production", PlanID: id}, true)
	resource, err := m.readSavedPlanResource(t.Context(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}})
	if err != nil {
		t.Fatal(err)
	}
	recoveryResourceView(t, resource, uri)
	if forbidden.Load() != 0 {
		t.Fatal("recipe fetched native evidence/approval")
	}
}

func TestSavedRecoveryResourceHTTPRestartIsolationAndRevocation(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			store, path, key := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
			response := f.exchange(f.authorizeAccount("fp_test_alice_remote_key", false), testVerifier, f.resource, f.clientID)
			var tokens tokenResponse
			decodeResponse(t, response, &tokens)
			_ = response.Body.Close()
			cs, ctx := f.connect(tokens, protocol)
			id := planID(t, callTool(t, ctx, cs, "gregale_plan_source_deploy", sourcePlanArgs(), false))
			uri := savedPlanRecoveryURI(getActionArgs{App: "alpha", PlanID: id}, false)
			params := &mcp.ReadResourceParams{URI: uri}
			first, err := cs.ReadResource(ctx, params)
			if err != nil {
				t.Fatal(err)
			}
			recoveryResourceView(t, first, uri)
			if err := cs.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = next.Close() })
			f.opts.StateStore = next
			handler, err := NewHTTPHandler(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(handler)
			resumed, ctx := f.connect(tokens, protocol)
			second, err := resumed.ReadResource(ctx, params)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatal("HTTP restart replaced original recipe")
			}
			response = f.exchange(f.authorizeAccount("fp_test_bob_remote_key", false), testVerifier, f.resource, f.clientID)
			var other tokenResponse
			decodeResponse(t, response, &other)
			_ = response.Body.Close()
			bob, bobCtx := f.connect(other, protocol)
			if _, err := bob.ReadResource(bobCtx, params); err == nil {
				t.Fatal("foreign grant inherited recipe")
			}
			response = f.request("POST", "/oauth/revoke", strings.NewReader(url.Values{"client_id": {f.clientID}, "token": {tokens.RefreshToken}}.Encode()), "application/x-www-form-urlencoded", "")
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("revocation failed")
			}
			if _, err := resumed.ReadResource(ctx, params); err == nil {
				t.Fatal("revoked grant retained recipe access")
			}
		})
	}
}

func TestSavedRecoveryResourceDiagnosticConsent(t *testing.T) {
	f, b, opts := newTriageFixture(t)
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planRegressionAction(t.Context(), regressionActionArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "acknowledge", Reason: "review diagnostic consent"})
	if err != nil {
		t.Fatal(err)
	}
	args := getActionArgs{App: "alpha", PlanID: value.(releasePlan).ID}
	uri := savedPlanRecoveryURI(args, false)
	req := &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}}
	result, err := m.readSavedPlanResource(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	recoveryResourceView(t, result, uri)
	guard, err := m.savedPlanRecoveryResource(t.Context(), args, false)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.opts.Environments.RequestReadApps, "alpha")
	if _, err := m.readSavedPlanResource(t.Context(), req); err == nil {
		t.Fatal("recipe bypassed diagnostic consent")
	}
	if _, err := checkedResourceValue(t.Context(), guard); err == nil {
		t.Fatal("final diagnostic guard lost")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 || f.reads != 1 {
		t.Fatal("recipe mutated triage or fetched diagnostic evidence")
	}
}

func TestSavedRecoveryResourceEnvironmentConsent(t *testing.T) {
	for _, change := range []string{"source", "target", "membership"} {
		t.Run(change, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, false))
			id := planHistoryEnvironment(t, m, false)
			args := getActionArgs{Project: "shop", Environment: "production", PlanID: id}
			guard, err := m.savedPlanRecoveryResource(t.Context(), args, true)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch change {
			case "source":
				f.stagingID = "replacement-source"
			case "target":
				f.productionID = "replacement-target"
			case "membership":
				f.extra = true
			}
			f.mu.Unlock()
			if _, err := checkedResourceValue(t.Context(), guard); err == nil {
				t.Fatal("changed source/target/workload membership released cached recipe")
			}
			uri := savedPlanRecoveryURI(args, true)
			if _, err := m.readSavedPlanResource(t.Context(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: uri}}); err == nil {
				t.Fatal("changed consent inherited original recipe")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
				t.Fatal("resource performed native/approval actions")
			}
		})
	}
}
