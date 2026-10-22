package mcpmanagement

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type environmentFixture struct {
	mu                                   sync.Mutex
	projectID, productionID, hash, appID string
	account, stagingID                   string
	protected, ambiguous, extra          bool
	writes, reads                        int
	keys, bodies                         []string
	commits                              map[string]string
	approved                             bool
	approvalFailure                      bool
	reviewRequests, approvalPolls        int
}

func newEnvironmentFixture(t *testing.T) (*environmentFixture, *httptest.Server) {
	f := &environmentFixture{projectID: "project-one", productionID: "env-production", hash: strings.Repeat("a", 64), appID: "app-one", account: "account-one", stagingID: "env-staging"}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	return f, b
}

func (f *environmentFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/approval-requests") {
		f.reviewRequests++
		_ = json.NewEncoder(w).Encode(api.BrowserPromotionApprovalResponse{ReviewURL: "https://console.example/cli-auth?approval=public-ticket", Ticket: "public-ticket", ExchangeCode: "private-poll-code", ExpiresAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339Nano)})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/approval-requests/exchange") {
		f.approvalPolls++
		var input api.BrowserPromotionApprovalExchange
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.ExchangeCode != "private-poll-code" || input.Ticket != "public-ticket" {
			w.WriteHeader(403)
			return
		}
		if f.approvalFailure {
			api.WriteProblem(w, api.NewProblem(403, api.CodeBrowserApprovalUnavailable, "Rejected private-poll-code", "private-human-approval"))
			return
		}
		if !f.approved {
			api.WriteProblem(w, api.NewProblem(404, api.CodeBrowserApprovalPending, "Pending", "Human review pending"))
			return
		}
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentApprovalResponse{ApprovalToken: "private-human-approval", Environment: "production", TokenKind: "promotion", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339)})
		return
	}
	if r.Method != http.MethodGet {
		f.writes++
		data, _ := io.ReadAll(r.Body)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		f.bodies = append(f.bodies, string(data))
		if f.commits == nil {
			f.commits = make(map[string]string)
		}
		key := r.Header.Get("Idempotency-Key")
		if previous, exists := f.commits[key]; exists && previous != string(data) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		// Model a committed native admission before a lost HTTP response.
		f.commits[key] = string(data)
		if f.ambiguous {
			f.ambiguous = false
			w.WriteHeader(504)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/rollback") {
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "promotion-one", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", Status: "completed", RollbackStatus: "rolled_back"})
			return
		}
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionResponse{PromotionID: "promotion-one", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production"})
		return
	}
	f.reads++
	switch r.URL.Path {
	case "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: f.account})
	case "/v1/projects":
		_ = json.NewEncoder(w).Encode([]api.ProjectSummaryResponse{{ID: f.projectID, Slug: "shop"}})
	case "/v1/apps":
		_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: f.appID, Slug: "alpha"}, {ID: "app-other", Slug: "beta"}})
	case "/v1/apps/alpha":
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: f.appID, Slug: "alpha"})
	case "/v1/apps/beta":
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-other", Slug: "beta"})
	case "/v1/projects/shop":
		workloads := []api.ProjectWorkloadResponse{{Slug: "alpha"}}
		if f.extra {
			workloads = append(workloads, api.ProjectWorkloadResponse{Slug: "beta"})
		}
		_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: f.projectID, Slug: "shop"}, Workloads: workloads})
	case "/v1/projects/shop/environments":
		_ = json.NewEncoder(w).Encode([]api.ProjectEnvironmentResponse{{ID: f.stagingID, ProjectID: f.projectID, Slug: "staging"}, {ID: f.productionID, ProjectID: f.projectID, Slug: "production", Protected: f.protected}})
	case "/v1/projects/shop/environments/staging", "/v1/projects/shop/environments/production":
		slug, id := "staging", f.stagingID
		if strings.HasSuffix(r.URL.Path, "production") {
			slug, id = "production", f.productionID
		}
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: id, ProjectID: f.projectID, Slug: slug, Protected: f.protected && slug == "production"})
	case "/v1/projects/shop/environments/production/promotion-preview":
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", CanPromote: true, ApprovalRequired: f.protected, PromotionHash: f.hash, PromotionToken: "exact-preview-token", Changes: []api.ProjectEnvironmentPromotionChange{{WorkloadSlug: "alpha", SourceDeploymentID: "source-release", TargetDeploymentID: "target-release"}}})
	case "/v1/projects/shop/environments/production/releases":
		rows := []api.ProjectEnvironmentReleaseWorkloadResponse{{WorkloadSlug: "alpha", DeploymentID: "target-release"}}
		if f.extra {
			rows = append(rows, api.ProjectEnvironmentReleaseWorkloadResponse{WorkloadSlug: "beta", DeploymentID: "other-release"})
		}
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentReleaseListResponse{ProjectSlug: "shop", Environment: "production", Workloads: rows})
	case "/v1/projects/shop/environments/production/promotions/promotion-one":
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "promotion-one", ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: f.hash, Status: "completed"})
	case "/v1/deployments/production-release":
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "production-release", AppID: f.appID, Scope: "production"})
	case "/v1/deployments":
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "production-release", AppID: f.appID, Scope: "production"}, {ID: "default-release", AppID: f.appID, Scope: "default"}}})
	default:
		w.WriteHeader(404)
	}
}

func environmentTestOptions(b *httptest.Server, write bool) Options {
	return Options{APIBase: b.URL, AllowWrites: true, AllowedApps: map[string]string{"alpha": "app-one"}, Environments: &EnvironmentConsent{Named: map[string]EnvironmentGrant{
		"shop/staging":    {Project: "shop", ProjectID: "project-one", Environment: "staging", EnvironmentID: "env-staging", Apps: map[string]string{"alpha": "app-one"}},
		"shop/production": {Project: "shop", ProjectID: "project-one", Environment: "production", EnvironmentID: "env-production", Write: write, Apps: map[string]string{"alpha": "app-one"}},
	}}}
}

func environmentPlanArgs() map[string]any {
	return map[string]any{"project": "shop", "from_environment": "staging", "to_environment": "production", "reason": "promote reviewed staging release"}
}
func environmentPlanID(t *testing.T, p map[string]json.RawMessage) string {
	t.Helper()
	var id string
	if err := json.Unmarshal(p["plan_id"], &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEnvironmentConsentSeparatesReadAndWriteTargets(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	opts := environmentTestOptions(b, false)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	p := callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
	result := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": environmentPlanID(t, p)}, true)
	if string(result["code"]) != `"environment_access_denied"` {
		t.Fatal("read grant allowed a write")
	}
	result = callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": "anything"}, true)
	if string(result["code"]) != `"environment_access_denied"` {
		t.Fatal("named writes inherited default writes")
	}
	callTool(t, ctx, cs, "gregale_get_environment", map[string]any{"project": "shop", "environment": "production"}, false)
	// The caller cannot widen permissions by changing the original map later.
	delete(opts.Environments.Named, "shop/production")
	callTool(t, ctx, cs, "gregale_get_environment", map[string]any{"project": "shop", "environment": "production"}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 0 {
		t.Fatal("unconsented write reached API")
	}
}

func TestEnvironmentIdentityAndWorkloadChangesInvalidatePlans(t *testing.T) {
	for _, change := range []string{"environment", "project", "app", "membership", "configuration"} {
		t.Run(change, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, true), "2026-07-28")
			p := callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
			f.mu.Lock()
			switch change {
			case "environment":
				f.productionID = "replacement-env"
			case "project":
				f.projectID = "replacement-project"
			case "app":
				f.appID = "replacement-app"
			case "membership":
				f.extra = true
			case "configuration":
				f.hash = strings.Repeat("b", 64)
			}
			f.mu.Unlock()
			callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": environmentPlanID(t, p)}, true)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 {
				t.Fatal("stale plan reached mutation API")
			}
		})
	}
}

func TestEnvironmentRestrictedInspectionFiltersAndGuardsIDs(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	opts := environmentTestOptions(b, false)
	delete(opts.Environments.Named, "shop/production")
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	result := callTool(t, ctx, cs, "gregale_list_deployments", map[string]any{}, false)
	if strings.Contains(string(result["items"]), "production") {
		t.Fatal("production inventory escaped consent")
	}
	callTool(t, ctx, cs, "gregale_get_deployment", map[string]any{"deployment_id": "production-release"}, true)
	callTool(t, ctx, cs, "gregale_get_environment", map[string]any{"project": "shop", "environment": "production"}, true)
	callTool(t, ctx, cs, "gregale_get_logs", map[string]any{"app": "alpha"}, true)
	callTool(t, ctx, cs, "gregale_get_metrics", map[string]any{"app": "alpha"}, true)
	// Per-workload inspection filters new/unconsented members. Whole-project
	// promotion refuses them, rather than mutating a partially consented project.
	opts = environmentTestOptions(b, true)
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	f.mu.Lock()
	f.extra = true
	f.mu.Unlock()
	result = callTool(t, ctx, cs, "gregale_get_environment", map[string]any{"project": "shop", "environment": "production"}, false)
	if strings.Contains(string(result["workloads"]), "beta") {
		t.Fatal("unconsented workload escaped inspection")
	}
	callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), true)
}

func TestEnvironmentPromotionRequiresHumanApprovalAndRetriesDurably(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	f.protected = true
	store, path, key := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore = store
	opts.planNamespace = "grant-one"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	p := callTool(t, ctx, cs, "gregale_plan_environment_promotion", environmentPlanArgs(), false)
	id := environmentPlanID(t, p)
	if string(p["promotion_token"]) != `"exact-preview-token"` || string(p["approval_required"]) != "true" {
		t.Fatal("plan cannot be reviewed for exact approval")
	}
	result := callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	if string(result["status"]) != `"approval_pending"` {
		t.Fatal("protected target did not require human approval")
	}
	f.mu.Lock()
	if f.writes != 0 {
		t.Fatal("approval absence reached API")
	}
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id, "approval_token": "human-only-approval"}, true)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	opts.planStore = restored
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	result = callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	if string(result["promotion_id"]) != `"promotion-one"` {
		t.Fatal("retry did not return promotion")
	}
	callTool(t, ctx, cs, "gregale_apply_environment_promotion", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 2 || f.keys[0] == "" || f.keys[0] != f.keys[1] || f.bodies[0] != f.bodies[1] || !strings.Contains(f.bodies[1], "human-only-approval") {
		t.Fatal("durable retry changed request identity or approval")
	}
}

func TestEnvironmentRollbackUsesCapturedPromotionAndStableKey(t *testing.T) {
	f, b := newEnvironmentFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, environmentTestOptions(b, true), "2026-07-28")
	p := callTool(t, ctx, cs, "gregale_plan_environment_rollback", map[string]any{"project": "shop", "environment": "production", "promotion_id": "promotion-one", "reason": "restore prior production release"}, false)
	id := environmentPlanID(t, p)
	callTool(t, ctx, cs, "gregale_apply_environment_rollback", map[string]any{"plan_id": id}, false)
	callTool(t, ctx, cs, "gregale_apply_environment_rollback", map[string]any{"plan_id": id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 || f.keys[0] != "gmcp-env-rollback-"+id {
		t.Fatal("rollback was not idempotent")
	}
}

func TestEnvironmentConsentRejectsUnreviewedWriteAndNarrowsApps(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	available, err := discoverEnvironments(t.Context(), api.NewClient(b.URL, testToken))
	if err != nil {
		t.Fatal(err)
	}
	req := &http.Request{PostForm: url.Values{"environments": {"shop/staging", "shop/production"}, "environment_writes": {"shop/staging"}}}
	c, err := selectedEnvironments(req, available, map[string]string{"alpha": "app-one"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultWrite || c.Named["shop/production"].Write || !c.Named["shop/staging"].Write {
		t.Fatal("environment writes widened")
	}
	req.PostForm["environment_writes"] = []string{"shop/unknown"}
	if _, err := selectedEnvironments(req, available, nil, true); err == nil {
		t.Fatal("unreviewed environment accepted")
	}
	req.PostForm["environment_writes"] = []string{"shop/staging"}
	if _, err := selectedEnvironments(req, available, nil, false); err == nil {
		t.Fatal("read-only connection accepted writes")
	}
}

func TestEnvironmentPlansExpireAndRevocationRemovesJournal(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	store, _, _ := testStateStore(t)
	opts := environmentTestOptions(b, true)
	opts.planStore = store
	opts.planNamespace = "revoked-grant"
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planEnvironmentPromotion(t.Context(), planEnvironmentPromotionArgs{Project: "shop", FromEnvironment: "staging", ToEnvironment: "production", Reason: "test expiry"})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(environmentPlan).ID
	m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
	if _, err := m.applyEnvironmentPromotion(t.Context(), applyEnvironmentPromotionArgs{PlanID: id}); err == nil {
		t.Fatal("expired plan accepted")
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	var records map[string]storedEnvironmentPlan
	if err := store.read("environment-plans/revoked-grant", &records); err != nil || len(records) != 0 {
		t.Fatal("revoked grant retained environment plans")
	}
}

func TestEnvironmentApprovalReflectionIsRedacted(t *testing.T) {
	secret := "human-only-approval"
	original := &api.APIError{Problem: api.Problem{Code: "approval_rejected", Status: 403, Title: secret, Detail: "reflected " + secret, Hint: secret}}
	err := redactApprovalError(original, secret)
	if strings.Contains(err.Error(), secret) || original.Problem.Detail != "reflected "+secret {
		t.Fatal("approval reflection leaked or changed the original API error")
	}
}
