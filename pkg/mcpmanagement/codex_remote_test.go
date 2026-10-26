// adr: 1618
// adr: 20261022161757
// adr: 20261022183756
// adr: 20261022192200
// adr: 20261023130958
// adr: 20261023184309
// adr: 20261024120000
// adr: 20261024123000
// adr: 20261024160000
// adr: 20261024180000
package mcpmanagement

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Opt-in acceptance runs the actual pinned Codex binary, with an isolated
// profile and real HTTPS/OAuth bridge. Test controls never enter production
// handlers. The API fixture performs no model or deployment operations.
func TestCodexRemoteOAuthAcceptance(t *testing.T) {
	runCodexRemoteOAuthAcceptance(t, "")
}

// ADR-1618: exercise consent, restart/refresh and revocation with the real client.
func TestCodexRequestDiagnosticsAcceptance(t *testing.T) {
	for _, mode := range []string{"denied", "approved"} {
		t.Run(mode, func(t *testing.T) { runCodexRemoteOAuthAcceptance(t, mode) })
	}
}

func runCodexRemoteOAuthAcceptance(t *testing.T, requestMode string) {
	t.Helper()
	binary := os.Getenv("GREGALE_MCP_CODEX_BINARY")
	if binary == "" {
		t.Skip("set GREGALE_MCP_CODEX_BINARY for real-client remote acceptance")
	}
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", StateStore: store, RedirectURIs: []string{testRedirect, "http://127.0.0.1/callback/*"}})
	var profileReads, historyPageReads, wakePageReads, wakeTimelineReads, wakeHistoryReads, errorGroupsReads, errorRequestsReads, timeseriesReads, analyticsReads, requestReads, performanceReads, criticalPathReads, comparisonReads, regressionReads, residencyReads, traceReads, nativeWrites atomic.Int32
	var replaced atomic.Bool
	var performanceEmpty, tracePartial atomic.Bool
	var historyWorker, planDrifted atomic.Bool
	var outcomeReceipt atomic.Pointer[api.EnvironmentRouteBudgetReceipt]
	var outcomeReads atomic.Int32
	var submissionReceipt atomic.Pointer[api.DeploymentSubmissionReceipt]
	var acceptedDeployment atomic.Pointer[api.DeploymentResponse]
	var submissionReads atomic.Int32
	var rollbackReceipt atomic.Pointer[api.RollbackOperation]
	var rollbackReads atomic.Int32
	var promotionReceipt atomic.Pointer[api.DeploymentPromotionReceipt]
	var promotedDeployment atomic.Pointer[api.DeploymentResponse]
	var promotionReads atomic.Int32
	var residencyEmpty atomic.Bool
	if requestMode != "" {
		f.mu.Lock()
		f.apiHook = func(w http.ResponseWriter, req *http.Request, account string) bool {
			if req.Method != http.MethodGet && req.URL.Path != "/v1/apps/alpha/source-review" && req.URL.Path != "/v1/apps/alpha/debug/compare" {
				nativeWrites.Add(1)
			}
			if req.URL.Path == "/v1/apps/alpha" && replaced.Load() {
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "replacement", "slug": "alpha"})
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/profiles" {
				profileReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || len(q) != 5 || q.Get("deployment_id") != discoveryDeploymentID || q.Get("runtime") != "go" || q.Get("route") != "GET /orders" || q.Get("start") != profileStart || q.Get("end") != profileEnd {
					t.Error("real client profile selection changed or output limit forwarded")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(profileFixture(performanceEmpty.Load()))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/wakes/wake-1/timeline" {
				wakeTimelineReads.Add(1)
				q := req.URL.Query()
				fromHistory := q.Get("since") == wakeTimelineSince && q.Get("limit") == "8" && q.Get("pagination") == "keyset_v1" && len(q) == 3
				fromRequest := q.Get("since") == "" && q.Get("limit") == "50" && q.Get("pagination") == "keyset_v1" && len(q) == 2
				if req.Method != http.MethodGet || !fromHistory && !fromRequest {
					t.Error("real client wake stage query changed or unsupported arguments forwarded")
					w.WriteHeader(400)
					return true
				}
				limit := 8
				if fromRequest {
					limit = 50
				}
				_ = json.NewEncoder(w).Encode(wakeTimelineFixture(account+"-app", limit, performanceEmpty.Load()))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/wakes/"+wakePageID+"/timeline" {
				wakePageReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("pagination") != "keyset_v1" || q.Get("since") != wakeTimelineSince || q.Get("limit") != "2" || q.Get("cursor") != "" && q.Get("cursor") != wakeNativePage2 && q.Get("cursor") != wakeNativePage3 {
					t.Error("real client keyset query changed or cursor envelope was forwarded")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(wakePagedFixture(account+"-app", q.Get("cursor")))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/wake-timeline" && req.URL.Query().Get("since") == historyPageSince {
				historyPageReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("pagination") != "keyset_v1" || q.Get("until") != historyPageUntil || q.Get("limit") != "2" || q.Get("cursor") != "" && q.Get("cursor") != historyNative2 && q.Get("cursor") != historyNative3 {
					t.Error("invalid real client history page query")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(historyPagedFixture(account+"-app", q.Get("cursor")))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/wake-timeline" {
				wakeHistoryReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("since") != wakeHistorySince || q.Get("until") != wakeHistoryUntil || len(q) != 4 || q.Get("pagination") != "keyset_v1" || q.Get("limit") != "2" {
					t.Error("real client wake window changed or output limit forwarded")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(wakeHistoryFixture(account+"-app", performanceEmpty.Load()))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/errors/summary" {
				errorGroupsReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("since") != errorSince || q.Get("until") != errorUntil || q.Get("limit") != "1" || len(q) != 3 && (len(q) != 4 || q.Get("cursor") != errorNativeGroupsNext) {
					t.Error("real client error summary changed selection or native cursor")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(errorGroupsFixture(account+"-app", q.Get("cursor"), 1, performanceEmpty.Load()))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/errors/"+errorFixtureFingerprint() {
				errorRequestsReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("limit") != "1" || len(q) != 1 && (len(q) != 2 || q.Get("cursor") != errorNativeRequestsNext) {
					t.Error("real client fingerprint read changed selection or native cursor")
					w.WriteHeader(400)
					return true
				}
				if performanceEmpty.Load() {
					api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "fingerprint not found"))
				} else {
					_ = json.NewEncoder(w).Encode(errorRequestsFixture(q.Get("cursor"), 1))
				}
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/requests/"+errorRequestID {
				requestReads.Add(1)
				_ = json.NewEncoder(w).Encode(api.DebugTelemetryRequestItem{RequestID: errorRequestID, EvidenceStatus: "request_id_only", ReceivedAt: "2026-10-15T08:00:00Z"})
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/analytics/timeseries" {
				timeseriesReads.Add(1)
				q := req.URL.Query()
				paired := q.Get("route") == "GET /orders" && q.Get("method") == "GET" && len(q) == 4
				appWide := q.Get("route") == "" && q.Get("method") == "" && len(q) == 2
				if req.Method != http.MethodGet || q.Get("since") != "3h45m" || q.Get("until") != timeseriesUntil || (!paired && !appWide) {
					t.Error("real client timeseries pair/window changed or limit/grouping forwarded")
					w.WriteHeader(400)
					return true
				}
				_ = json.NewEncoder(w).Encode(requestTimeseriesFixture(q.Get("route"), q.Get("method"), performanceEmpty.Load()))
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/analytics" {
				analyticsReads.Add(1)
				q := req.URL.Query()
				if req.Method != http.MethodGet || q.Get("since") != "1h" || q.Get("until") != analyticsUntil || q.Get("group_by") != "route" || len(q) != 3 {
					t.Error("real client analytics selection changed")
					w.WriteHeader(400)
					return true
				}
				native := requestAnalyticsFixture()
				if performanceEmpty.Load() {
					native = emptyRequestAnalyticsFixture()
				}
				_ = json.NewEncoder(w).Encode(native)
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/regressions" {
				regressionReads.Add(1)
				if req.Method != http.MethodGet || req.URL.Query().Get("since") != "1h" || len(req.URL.Query()) != 1 {
					t.Error("real client regression selection changed")
					w.WriteHeader(400)
					return true
				}
				native := regressionsFixture()
				if performanceEmpty.Load() {
					native.Regressions = nil
				}
				_ = json.NewEncoder(w).Encode(native)
				return true
			}
			if req.URL.Path == "/v1/account/traces/"+traceFixtureID+"/selected-apps" {
				traceReads.Add(1)
				if req.Method != "GET" || req.URL.Query().Get("app_id") != account+"-app" || len(req.URL.Query()["app_id"]) != 1 || req.URL.Query().Get("limit") != "10" || len(req.URL.Query()) != 2 {
					t.Error("Codex trace selection changed")
					w.WriteHeader(400)
					return true
				}
				native := selectedTraceFixture(account + "-app")
				if performanceEmpty.Load() {
					native.Requests = []api.SelectedTraceRequest{}
					native.Invocations = []api.SelectedTraceInvocation{}
				}
				if tracePartial.Load() {
					native.Requests = nil
					native.RequestsStatus = "unavailable"
					native.Partial = true
				}
				_ = json.NewEncoder(w).Encode(native)
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/compare" {
				comparisonReads.Add(1)
				var selection api.DebugCompareRequest
				if req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&selection) != nil || selection.Source != discoveryDeploymentID || selection.Mirror != comparisonTargetID || selection.Since != "1h" || (selection.Until != comparisonUntil && selection.Until != "") || selection.Route != "" {
					t.Error("real client comparison selection changed")
					w.WriteHeader(400)
					return true
				}
				native := comparisonFixture(account + "-app")
				if performanceEmpty.Load() {
					native.Routes = nil
				}
				_ = json.NewEncoder(w).Encode(native)
				return true
			}
			if req.URL.Path == "/v1/deployments/"+comparisonTargetID {
				_ = json.NewEncoder(w).Encode(map[string]string{"id": comparisonTargetID, "app_id": account + "-app", "scope": "production"})
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/running" {
				residencyReads.Add(1)
				if req.URL.Query().Get("since") != "1h" || req.URL.Query().Get("limit") != "1" {
					t.Error("real client residency selection lost")
					w.WriteHeader(400)
					return true
				}
				if residencyEmpty.Load() {
					_ = json.NewEncoder(w).Encode(emptyResidencyFixture(account + "-app"))
				} else {
					_ = json.NewEncoder(w).Encode(residencyFixture(account + "-app"))
				}
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/critical-paths" {
				criticalPathReads.Add(1)
				if req.URL.Query().Get("since") != "1h" || len(req.URL.Query()) != 1 {
					t.Error("real client critical-path selection changed")
					w.WriteHeader(400)
					return true
				}
				if performanceEmpty.Load() {
					_ = json.NewEncoder(w).Encode(emptyCriticalPathsFixture(account + "-app"))
				} else {
					_ = json.NewEncoder(w).Encode(criticalPathsFixture(account + "-app"))
				}
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/coverage" || req.URL.Path == "/v1/apps/alpha/debug/dependencies" {
				performanceReads.Add(1)
				if req.URL.Query().Get("since") != "1h" {
					t.Error("real client performance lookback lost")
					w.WriteHeader(400)
					return true
				}
				if performanceEmpty.Load() {
					if strings.HasSuffix(req.URL.Path, "/coverage") {
						_ = json.NewEncoder(w).Encode(emptyPerformanceCoverage(account + "-app"))
					} else {
						_ = json.NewEncoder(w).Encode(emptyPerformanceDependencies(account + "-app"))
					}
					return true
				}
				return performanceFixtureHook(w, req, account+"-app")
			}
			if req.URL.Path == "/v1/deployments/"+discoveryDeploymentID {
				_ = json.NewEncoder(w).Encode(map[string]string{"id": discoveryDeploymentID, "app_id": account + "-app", "scope": "staging"})
				return true
			}
			if req.URL.Path == "/v1/apps/alpha/debug/requests" {
				requestReads.Add(1)
				q := req.URL.Query()
				for key, value := range map[string]string{"since": "1h", "route": "/orders", "status": "503", "deployment_id": discoveryDeploymentID, "min_latency_ms": "50", "cold_boot": "false", "limit": "1"} {
					if q.Get(key) != value {
						t.Errorf("real client discovery lost %s", key)
						w.WriteHeader(400)
						return true
					}
				}
				page := discoveryFixturePage(q)
				if q.Get("cursor") == "" {
					page.Requests[0].WakeID = "wake-1"
				}
				_ = json.NewEncoder(w).Encode(page)
				return true
			}
			if strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/debug/requests/") {
				requestReads.Add(1)
				if strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/debug/requests/row-1") {
					e := withRequestGuest(withRequestPath(requestFixtureEvidence()))
					e.Request.DeploymentID, e.Request.ReceivedAt, e.Request.Count = discoveryDeploymentID, "2026-10-17T10:59:00Z", 7
					e.Request.WakeID = "wake-1"
					e = withRequestDependencyComparison(withRequestRegression(e))
					e.DependencyComparison.PreviousDeploymentID = comparisonTargetID
					if performanceEmpty.Load() {
						e.Explanation = api.DebugEvidenceExplanation{}
						e.CriticalPath, e.Spans = nil, nil
						e.Request.Guest, e.Regression, e.DependencyComparison = nil, nil, nil
						e.Request.WakeID = ""
						e.Correlation = requestFixtureEvidence().Correlation
					}
					switch req.URL.Path {
					case "/v1/apps/alpha/debug/requests/row-1-wake-missing/evidence":
						e.Request.WakeID = ""
					case "/v1/apps/alpha/debug/requests/row-1-wake-invalid/evidence":
						e.Request.WakeID = "../private-wake-" + testToken
					case "/v1/apps/alpha/debug/requests/row-1-wake-cold/evidence":
						e.Request.WakeID, e.Request.ColdBoot = "wake-1", true
						e.Explanation = api.DebugEvidenceExplanation{Status: "unobserved", GeneratedBy: "rules-v1", Findings: []api.DebugEvidenceFinding{{Code: "cold_boot", Confidence: "medium", EvidenceRefs: []string{"request"}}}, Recommendations: []api.DebugEvidenceRecommendation{{Action: "inspect_wake_timeline"}}}
					case "/v1/apps/alpha/debug/requests/row-1-comparison-truncated/evidence":
						e = withRequestDependencyComparison(e)
						e.DependencyComparison.PreviousDeploymentID, e.DependencyComparison.Truncated = comparisonTargetID, true
					case "/v1/apps/alpha/debug/requests/row-1-no-regression/evidence":
						e.Regression, e.Explanation = nil, api.DebugEvidenceExplanation{Status: "unobserved", GeneratedBy: "rules-v1"}
					case "/v1/apps/alpha/debug/requests/row-1-regression-unavailable/evidence":
						e.Regression, e.Explanation = nil, api.DebugEvidenceExplanation{Status: "regression_unavailable", GeneratedBy: "rules-v1"}
					}
					if strings.HasSuffix(req.URL.Path, "/evidence") {
						_ = json.NewEncoder(w).Encode(e)
					} else {
						_ = json.NewEncoder(w).Encode(e.Request)
					}
					return true
				}
				return requestFixtureHook(w, req, account)
			}
			return false
		}
		f.mu.Unlock()
	}
	f.mu.Lock()
	previousHook := f.apiHook
	f.apiHook = func(w http.ResponseWriter, req *http.Request, account string) bool {
		// Route-budget requests require UUIDs; retain the older alpha fixture's
		// identities and give budget planning its own consented app.
		if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/rollbacks/") {
			if receipt := rollbackReceipt.Load(); receipt != nil && req.URL.Path == "/v1/apps/alpha/rollbacks/"+receipt.ID {
				if req.Header.Get(api.RollbackAttemptHeader) != "" || req.Header.Get("Idempotency-Key") != "" {
					t.Error("rollback lookup forwarded write metadata")
				}
				rollbackReads.Add(1)
				_ = json.NewEncoder(w).Encode(receipt)
				return true
			}
		}
		if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/deployment-promotions/") {
			receipt := promotionReceipt.Load()
			if receipt == nil || req.URL.Path != "/v1/apps/alpha/deployment-promotions/"+receipt.AttemptID {
				w.WriteHeader(404)
				return true
			}
			promotionReads.Add(1)
			_ = json.NewEncoder(w).Encode(receipt)
			return true
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/deployments/promoted-source-deployment" {
			if d := promotedDeployment.Load(); d != nil {
				_ = json.NewEncoder(w).Encode(d)
				return true
			}
			w.WriteHeader(404)
			return true
		}
		if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/apps/alpha/deployment-submissions/") {
			receipt := submissionReceipt.Load()
			if receipt == nil || req.URL.Path != "/v1/apps/alpha/deployment-submissions/"+receipt.AttemptID {
				w.WriteHeader(404)
				return true
			}
			submissionReads.Add(1)
			_ = json.NewEncoder(w).Encode(receipt)
			return true
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/deployments/accepted-source-deployment" {
			if d := acceptedDeployment.Load(); d != nil {
				_ = json.NewEncoder(w).Encode(d)
				return true
			}
			w.WriteHeader(404)
			return true
		}
		const budgetAppID = "22222222-2222-4222-8222-222222222222"
		if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v1/apps/budget-history/route-budgets/receipts/") {
			receipt := outcomeReceipt.Load()
			if receipt == nil || req.URL.Path != "/v1/apps/budget-history/route-budgets/receipts/"+receipt.RequestID || req.URL.Query().Get("scope") != "default" {
				w.WriteHeader(404)
				return true
			}
			outcomeReads.Add(1)
			_ = json.NewEncoder(w).Encode(receipt)
			return true
		}

		if req.Method == http.MethodGet && req.URL.Path == "/v1/apps/budget-history" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: budgetAppID, Slug: "budget-history", Type: "app"})
			return true
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/apps/budget-history/route-budgets" && req.URL.Query().Get("scope") == "default" {
			var revision int64
			if outcomeReceipt.Load() != nil {
				revision = 2
			} // Synthetic later policy, separate from receipt revision 1.
			_ = json.NewEncoder(w).Encode(api.EnvironmentRouteBudgets{AppID: budgetAppID, Scope: "default", Revision: revision, BudgetSource: "app_policy", Config: api.RouteMonitorConfig{AppID: budgetAppID, Routes: []api.RouteMonitorRoute{}}})
			return true
		}
		if req.Method == http.MethodGet && req.URL.Path == "/v1/projects/shop/environments/production/promotion-preview" {
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentPromotionPreviewResponse{ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production", CanPromote: true, ApprovalRequired: true, PromotionHash: strings.Repeat("a", 64), PromotionToken: "PRIVATE_ENV_PREVIEW"})
			return true
		}
		if planDrifted.Load() && req.Method == http.MethodGet && req.URL.Path == "/v1/apps/alpha" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: account + "-app", Slug: "alpha", Type: "app", RAMMB: 512})
			return true
		}
		if historyWorker.Load() && req.Method == http.MethodGet && req.URL.Path == "/v1/apps/alpha" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: account + "-app", Slug: "alpha", Type: "app", Manifest: api.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
			return true
		}
		return previousHook != nil && previousHook(w, req, account)
	}
	f.mu.Unlock()
	certificate, ca := codexFixtureCertificate(t)
	f.server.TLS.Certificates = []tls.Certificate{certificate}
	f.client.Transport.(*http.Transport).TLSClientConfig.RootCAs.AppendCertsFromPEM(ca)
	var mu sync.Mutex
	var latest tokenResponse
	refreshes := 0
	var currentRemote *remoteServer
	install := func() {
		service, err := NewHTTPService(f.opts)
		if err != nil {
			t.Fatal(err)
		}
		currentRemote = service.remote
		h := service.Handler()
		f.setHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/oauth/token" {
				h.ServeHTTP(w, req)
				return
			}
			if err := req.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code == 200 {
				var tokens tokenResponse
				if err := json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				latest = tokens
				if req.PostForm.Get("grant_type") == "refresh_token" {
					refreshes++
				}
				mu.Unlock()
			}
			for k, values := range response.Header() {
				w.Header()[k] = values
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
		}))
	}
	install()
	t.Cleanup(func() { _ = f.opts.StateStore.Close() })
	controlToken, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer "+controlToken {
			w.WriteHeader(403)
			return
		}
		switch req.URL.Path {
		case "/rollback-outcome-history":
			receipt, err := seedCodexRollbackOutcome(currentRemote)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			rollbackReceipt.Store(&receipt)
			writeOAuthJSON(w, 200, map[string]string{"plan_id": strings.Repeat("b", 64), "operation_id": receipt.ID})
			return
		case "/promotion-outcome-history":
			receipt, deployment, err := seedCodexPromotionOutcome(currentRemote)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			promotionReceipt.Store(&receipt)
			promotedDeployment.Store(&deployment)
			writeOAuthJSON(w, 200, map[string]string{"plan_id": strings.Repeat("c", 64)})
			return
		case "/deployment-outcome-history":
			receipt, deployment, err := seedCodexDeploymentOutcome(currentRemote)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			submissionReceipt.Store(&receipt)
			acceptedDeployment.Store(&deployment)
			writeOAuthJSON(w, 200, map[string]string{"plan_id": strings.Repeat("d", 64)})
			return
		case "/outcome-history":
			receipt, err := seedCodexOutcomeHistory(currentRemote)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			outcomeReceipt.Store(&receipt)
			writeOAuthJSON(w, 200, map[string]string{"plan_id": strings.Repeat("e", 64)})
			return
		case "/plan-drift-on":
			planDrifted.Store(true)
		case "/plan-drift-off":
			planDrifted.Store(false)
		case "/history-worker":
			historyWorker.Store(true)
		case "/history-request":
			historyWorker.Store(false)
		case "/approve":
			var input struct {
				AuthorizationURL string `json:"authorization_url"`
			}
			if json.NewDecoder(req.Body).Decode(&input) != nil || !strings.HasPrefix(input.AuthorizationURL, f.server.URL+"/oauth/authorize?") {
				w.WriteHeader(400)
				return
			}
			flow := f.beginAt(req.Context(), input.AuthorizationURL)
			for _, decision := range []string{"start-login", "check-login", "approve"} {
				var response *http.Response
				if decision == "approve" {
					form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {decision}, "app_access": {"all"}, "environments": {"shop/staging", "shop/production"}}
					if requestMode == "approved" {
						form.Set("request_read_apps", "alpha")
					}
					response = f.submitConsent(flow, form, f.server.URL)
				} else {
					response = f.consent(flow, decision, "", f.server.URL, flow.csrf, false)
				}
				_ = response.Body.Close()
				if decision == "approve" {
					if response.StatusCode != 303 {
						t.Errorf("consent=%d", response.StatusCode)
						w.WriteHeader(500)
						return
					}
					// Deliver the real authorization code to Codex's own callback.
					callbackRequest, err := http.NewRequestWithContext(req.Context(), http.MethodGet, response.Header.Get("Location"), nil)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					callback, err := http.DefaultClient.Do(callbackRequest)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					_ = callback.Body.Close()
				} else if response.StatusCode != 200 {
					t.Errorf("sign-in=%d", response.StatusCode)
					w.WriteHeader(500)
					return
				}
			}
		case "/restart", "/expire":
			if req.URL.Path == "/expire" {
				var snapshot oauthSnapshot
				if err := f.opts.StateStore.read("oauth", &snapshot); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				for id, grant := range snapshot.Grants {
					grant.AccessExpires = time.Now().Add(-time.Minute)
					snapshot.Grants[id] = grant
				}
				if err := f.opts.StateStore.update("oauth", snapshot); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
			}
			if err := f.opts.StateStore.Close(); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			f.opts.StateStore = next
			install()
		case "/revoke":
			var snapshot oauthSnapshot
			if err := f.opts.StateStore.read("oauth", &snapshot); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			var clientID string
			for _, grant := range snapshot.Grants {
				if grant.Active {
					clientID = grant.ClientID
				}
			}
			mu.Lock()
			refresh := latest.RefreshToken
			mu.Unlock()
			response := f.request("POST", "/oauth/revoke", strings.NewReader(url.Values{"client_id": {clientID}, "token": {refresh}}.Encode()), "application/x-www-form-urlencoded", "")
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Errorf("revoke=%d", response.StatusCode)
				w.WriteHeader(500)
				return
			}
		case "/replace":
			replaced.Store(true)
		case "/trace-partial":
			tracePartial.Store(true)
		case "/performance-empty":
			performanceEmpty.Store(true)
		case "/residency-empty":
			residencyEmpty.Store(true)
		case "/evidence":
			mu.Lock()
			defer mu.Unlock()
			writeOAuthJSON(w, 200, map[string]int{"rollback_reads": int(rollbackReads.Load()), "promotion_reads": int(promotionReads.Load()), "submission_reads": int(submissionReads.Load()), "outcome_reads": int(outcomeReads.Load()), "profile_reads": int(profileReads.Load()), "refreshes": refreshes, "wake_history_reads": int(wakeHistoryReads.Load()), "wake_history_page_reads": int(historyPageReads.Load()), "wake_timeline_reads": int(wakeTimelineReads.Load()), "wake_page_reads": int(wakePageReads.Load()), "request_reads": int(requestReads.Load()), "analytics_reads": int(analyticsReads.Load()), "timeseries_reads": int(timeseriesReads.Load()), "error_groups_reads": int(errorGroupsReads.Load()), "error_requests_reads": int(errorRequestsReads.Load()), "performance_reads": int(performanceReads.Load()), "critical_path_reads": int(criticalPathReads.Load()), "comparison_reads": int(comparisonReads.Load()), "regression_reads": int(regressionReads.Load()), "residency_reads": int(residencyReads.Load()), "trace_reads": int(traceReads.Load()), "native_writes": int(nativeWrites.Load())})
			return
		default:
			w.WriteHeader(404)
			return
		}
		writeOAuthJSON(w, 200, map[string]bool{"ok": true})
	}))
	t.Cleanup(control.Close)
	certPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(certPath, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/ops/management-mcp-client-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", script, "--remote-fixture", "--codex-binary", binary)
	if diagnostics := os.Getenv("GREGALE_MCP_CODEX_DIAGNOSTICS"); diagnostics != "" {
		if requestMode != "" {
			diagnostics += "." + requestMode
		}
		command.Args = append(command.Args, "--diagnostics-file", diagnostics)
	}
	command.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath, "CODEX_CA_CERTIFICATE="+certPath, "GREGALE_MCP_TEST_RESOURCE="+f.resource, "GREGALE_MCP_TEST_CONTROL="+control.URL, "GREGALE_MCP_TEST_CONTROL_TOKEN="+controlToken, "GREGALE_MCP_TEST_REQUEST_MODE="+requestMode)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex remote acceptance failed: %v\n%s", err, output)
	}
	var report map[string]any
	if json.Unmarshal(output, &report) != nil || report["revocation"] != "passed" || report["refresh"] != "passed" || report["restart"] != "passed" {
		t.Fatal("incomplete real-client evidence")
	}
	if report["rollback_outcomes"] != "passed" || rollbackReads.Load() == 0 || report["promotion_outcomes"] != "passed" || promotionReads.Load() == 0 || report["deployment_outcomes"] != "passed" || submissionReads.Load() == 0 || report["outcome_discovery"] != "passed" || outcomeReads.Load() == 0 || report["action_outcomes"] != "passed" || report["action_history"] != "passed" || report["workload_action_history"] != "passed" || report["environment_action_history"] != "passed" || report["budget_action_history"] != "passed" || report["saved_plan_review"] != "passed" || report["plan_applicability"] != "passed" || report["plan_drift"] != "passed" || report["recovery_resources"] != "passed" || report["recovery_discovery"] != "passed" {
		t.Fatal("incomplete real-client action history restart/refresh evidence")
	}
	if requestMode != "" && (report["request_diagnostics"] != requestMode || report["request_discovery"] != "passed" || nativeWrites.Load() != 0 || requestReads.Load() != map[string]int32{"denied": 0, "approved": 32}[requestMode]) {
		t.Fatal("incomplete request consent evidence or unexpected native writes")
	}
	if requestMode != "" && (report["performance_diagnosis"] != "passed" || performanceReads.Load() != map[string]int32{"denied": 0, "approved": 12}[requestMode]) {
		t.Fatal("incomplete real-client performance consent/empty-evidence verification")
	}
	if requestMode != "" && (report["residency_diagnosis"] != "passed" || residencyReads.Load() != map[string]int32{"denied": 0, "approved": 4}[requestMode]) {
		t.Fatal("incomplete real-client residency consent/empty-evidence verification")
	}
	if requestMode != "" && (report["critical_paths"] != "passed" || criticalPathReads.Load() != map[string]int32{"denied": 0, "approved": 4}[requestMode]) {
		t.Fatal("incomplete real-client critical-path consent/empty-evidence verification")
	}
	if requestMode != "" && (report["deployment_comparison"] != "passed" || comparisonReads.Load() != map[string]int32{"denied": 0, "approved": 8}[requestMode]) {
		t.Fatal("incomplete real-client deployment comparison verification")
	}
	if requestMode != "" && (report["regression_inbox"] != "passed" || report["regression_resource"] != "passed" || report["fleet_regressions"] != "passed" || regressionReads.Load() != map[string]int32{"denied": 0, "approved": 16}[requestMode]) {
		t.Fatal("incomplete real-client regression inbox/resource/fleet verification")
	}
	if requestMode != "" && (report["selected_trace"] != "passed" || traceReads.Load() != map[string]int32{"denied": 0, "approved": 5}[requestMode] || requestMode == "approved" && report["partial_trace"] != "passed") {
		t.Fatal("incomplete real-client selected trace verification")
	}
	if requestMode != "" && report["incident_bundle"] != "passed" {
		t.Fatal("incomplete real-client incident bundle verification")
	}
	if requestMode != "" && (report["request_explanations"] != "passed" || requestMode == "approved" && report["empty_request_explanation"] != "passed") {
		t.Fatal("incomplete real-client structured request explanation verification")
	}
	if requestMode != "" && (report["request_critical_path"] != "passed" || requestMode == "approved" && report["empty_request_critical_path"] != "passed") {
		t.Fatal("incomplete real-client selected request path verification")
	}
	if requestMode != "" && (report["guest_execution"] != "passed" || requestMode == "approved" && report["missing_guest_execution"] != "passed") {
		t.Fatal("incomplete real-client guest evidence verification")
	}
	if requestMode != "" && (report["request_regression_context"] != "passed" || requestMode == "approved" && report["missing_request_regression"] != "passed") {
		t.Fatal("incomplete real-client request regression verification")
	}
	if requestMode != "" && (report["request_dependency_comparison"] != "passed" || requestMode == "approved" && (report["missing_request_dependency_comparison"] != "passed" || report["truncated_request_dependency_comparison"] != "passed")) {
		t.Fatal("incomplete real-client request dependency comparison verification")
	}
	if requestMode != "" && (report["dependency_impact"] != "passed" || requestMode == "approved" && report["empty_dependency_impact"] != "passed") {
		t.Fatal("incomplete real-client dependency impact verification")
	}
	if requestMode != "" && (report["request_analytics"] != "passed" || analyticsReads.Load() != map[string]int32{"denied": 0, "approved": 4}[requestMode] || requestMode == "approved" && report["empty_request_analytics"] != "passed") {
		t.Fatal("incomplete real-client analytics consent/window/measurement verification")
	}
	if requestMode != "" && (report["request_analytics_timeseries"] != "passed" || timeseriesReads.Load() != map[string]int32{"denied": 0, "approved": 8}[requestMode] || requestMode == "approved" && report["empty_request_analytics_timeseries"] != "passed") {
		t.Fatal("incomplete real-client timeseries consent/pair/bucket verification")
	}
	if requestMode != "" && (report["error_triage"] != "passed" || errorGroupsReads.Load() != map[string]int32{"denied": 0, "approved": 7}[requestMode] || errorRequestsReads.Load() != map[string]int32{"denied": 0, "approved": 7}[requestMode] || requestMode == "approved" && report["empty_error_triage"] != "passed") {
		t.Fatal("incomplete real-client error triage consent/pagination/lookup verification")
	}
	if requestMode != "" && (report["profiles"] != "passed" || profileReads.Load() != map[string]int32{"denied": 0, "approved": 4}[requestMode] || requestMode == "approved" && report["empty_profiles"] != "passed") {
		t.Fatal("incomplete real-client profile consent/selection/empty-evidence verification")
	}
	if requestMode != "" && (report["wake_history"] != "passed" || wakeHistoryReads.Load() != map[string]int32{"denied": 0, "approved": 4}[requestMode] || requestMode == "approved" && report["empty_wake_history"] != "passed") {
		t.Fatal("incomplete real-client wake consent/measurement/empty-history verification")
	}
	if requestMode != "" && (report["wake_timeline"] != "passed" || wakeTimelineReads.Load() != map[string]int32{"denied": 0, "approved": 7}[requestMode] || requestMode == "approved" && report["empty_wake_timeline"] != "passed") {
		t.Fatal("incomplete real-client wake stage consent/presence/pagination/empty-evidence verification")
	}
	if requestMode != "" && (report["request_wake"] != "passed" || requestMode == "approved" && (report["missing_request_wake"] != "passed" || report["invalid_request_wake"] != "passed" || report["request_wake_recommendation"] != "passed")) {
		t.Fatal("incomplete real-client request wake association/handoff/availability/recommendation verification")
	}
	if requestMode != "" && (report["wake_history_pagination"] != "passed" || historyPageReads.Load() != map[string]int32{"denied": 0, "approved": 9}[requestMode]) {
		t.Fatal("incomplete real-client history pagination/restart/refresh verification")
	}

	if requestMode != "" && (report["wake_pagination"] != "passed" || wakePageReads.Load() != map[string]int32{"denied": 0, "approved": 9}[requestMode]) {
		t.Fatal("incomplete real-client tied-time keyset/restart/refresh verification")
	}
	if reportPath := os.Getenv("GREGALE_MCP_CLIENT_REPORT"); reportPath != "" {
		if requestMode != "" {
			reportPath += "." + requestMode
		}
		if err := os.WriteFile(reportPath, output, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func codexFixtureCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gregale acceptance CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
}
