package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type browserPromotionFixture struct {
	server                 *cliAuthTestServer
	account                state.Account
	session, key, sourceID string
	preview                api.ProjectEnvironmentPromotionPreviewResponse
}

func newBrowserPromotionFixture(t *testing.T) browserPromotionFixture {
	t.Helper()
	srv, store := newCliAuthTestServer(t)
	acct, err := store.CreateAccount(t.Context(), "promotion-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	var sourceID string
	for _, scope := range []string{"staging", "production"} {
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, SourceSHA256: scope, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(t.Context(), deployment.ID, "/rootfs/"+scope, "apps/"+scope+".ext4", 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
			t.Fatal(err)
		}
		if scope == "staging" {
			sourceID = deployment.ID
		}
	}
	plan, problem := srv.apid.buildProjectEnvironmentPromotionPlan(t.Context(), acct, "shop", "staging", "production", false)
	if problem != nil {
		t.Fatal(problem)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "bridge", []string{api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	return browserPromotionFixture{server: srv, account: acct, session: srv.issueBrowserSessionForTest(t, acct.Email), key: key, sourceID: sourceID, preview: plan.Preview}
}

func (f browserPromotionFixture) apiCall(t *testing.T, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/projects/shop/environments/production/"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.key)
	if path == "promote" {
		r.Header.Set("Idempotency-Key", "browser-promotion-"+f.sourceID)
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func (f browserPromotionFixture) requestReview(t *testing.T) api.BrowserPromotionApprovalResponse {
	t.Helper()
	w := f.apiCall(t, "approval-requests", api.BrowserPromotionApprovalRequest{PromotionToken: f.preview.PromotionToken, Reason: "Release <script>alert('agent')</script>"})
	if w.Code != 201 {
		t.Fatalf("request=%d %s", w.Code, w.Body.String())
	}
	var request api.BrowserPromotionApprovalResponse
	if err := json.Unmarshal(w.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func (f browserPromotionFixture) reviewPage(t *testing.T, request api.BrowserPromotionApprovalResponse) (string, string) {
	t.Helper()
	r := httptest.NewRequest("GET", request.ReviewURL, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session})
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), f.preview.PromotionHash) || !strings.Contains(w.Body.String(), f.sourceID) || strings.Contains(w.Body.String(), "<script>alert") || strings.Contains(w.Body.String(), request.ExchangeCode) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe or missing review=%d %s", w.Code, w.Body.String())
	}
	csrf := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(csrf) != 2 {
		t.Fatal("review lacks CSRF field")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == middleware.CookieNameAuthenticated {
			return cookie.Value, csrf[1]
		}
	}
	t.Fatal("review lacks CSRF cookie")
	return "", ""
}

func (f browserPromotionFixture) submit(request api.BrowserPromotionApprovalResponse, cookie, csrf, session string) *httptest.ResponseRecorder {
	form := url.Values{"approval": {request.Ticket}, "csrf_token": {csrf}}
	r := httptest.NewRequest("POST", "/cli-auth", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func TestMCPBrowserPromotionApprovalRequiresHumanAndRetriesWithoutRenewal(t *testing.T) {
	f := newBrowserPromotionFixture(t)
	request := f.requestReview(t)
	input := api.BrowserPromotionApprovalExchange{Ticket: request.Ticket, ExchangeCode: request.ExchangeCode}
	if pending := f.apiCall(t, "approval-requests/exchange", input); pending.Code != 404 {
		t.Fatalf("unapproved exchange=%d", pending.Code)
	}
	cookie, csrf := f.reviewPage(t, request)
	if missing := f.submit(request, "", csrf, f.session); missing.Code != 403 {
		t.Fatal("missing browser CSRF accepted")
	}
	other := f.server.issueBrowserSessionForTest(t, "other-promotion@example.com")
	if cross := f.submit(request, cookie, csrf, other); cross.Code != 403 {
		t.Fatal("different account approved review")
	}
	otherRequest := f.requestReview(t)
	if swapped := f.submit(otherRequest, cookie, csrf, f.session); swapped.Code != 403 {
		t.Fatal("CSRF authorized a different promotion request")
	}
	if approved := f.submit(request, cookie, csrf, f.session); approved.Code != 200 || !strings.Contains(approved.Body.String(), "You approved") || strings.Contains(approved.Body.String(), request.ExchangeCode) {
		t.Fatalf("review=%d %s", approved.Code, approved.Body.String())
	}
	if replay := f.submit(request, cookie, csrf, f.session); replay.Code != 409 {
		t.Fatal("browser review claimed twice")
	}
	first := f.apiCall(t, "approval-requests/exchange", input)
	second := f.apiCall(t, "approval-requests/exchange", input)
	if first.Code != 200 || second.Code != 200 || first.Body.String() != second.Body.String() {
		t.Fatalf("exchange changed after retry: %d %d", first.Code, second.Code)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(first.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.store.ConsumeProjectEnvironmentApproval(t.Context(), f.account.ID, "shop", "production", hashProjectEnvironmentApprovalMaterial(f.preview.PromotionToken), hashProjectEnvironmentApprovalMaterial(approval.ApprovalToken), time.Now()); err != nil {
		t.Fatal(err)
	}
	if used := f.apiCall(t, "approval-requests/exchange", input); used.Code != 410 {
		t.Fatal("consumed approval revived by polling")
	}
}

func TestMCPBrowserPromotionApprovalRejectsStaleAndTamperedReview(t *testing.T) {
	f := newBrowserPromotionFixture(t)
	request := f.requestReview(t)
	cookie, csrf := f.reviewPage(t, request)
	if invalid := f.apiCall(t, "approval-requests/exchange", api.BrowserPromotionApprovalExchange{Ticket: request.Ticket, ExchangeCode: strings.Repeat("x", 43)}); invalid.Code != 410 {
		t.Fatal("review URL sufficed to exchange")
	}
	request.Ticket = "broken" + request.Ticket
	if invalid := f.submit(request, cookie, csrf, f.session); invalid.Code != 403 {
		t.Fatal("tampered ticket approved")
	}
	request = f.requestReview(t)
	cookie, csrf = f.reviewPage(t, request)
	if err := f.server.store.UpdateDeploymentStatus(t.Context(), f.sourceID, state.DeployFailed, "stale fixture"); err != nil {
		t.Fatal(err)
	}
	if stale := f.submit(request, cookie, csrf, f.session); stale.Code != 409 {
		t.Fatal("changed source approved")
	}
}

func TestMCPBrowserPromotionApprovalExpiresAndRequiresSession(t *testing.T) {
	f := newBrowserPromotionFixture(t)
	request := f.requestReview(t)
	unsigned := httptest.NewRecorder()
	f.server.ServeHTTP(unsigned, httptest.NewRequest("GET", request.ReviewURL, nil))
	if unsigned.Code != 302 || !strings.HasPrefix(unsigned.Header().Get("Location"), "/login?next=") {
		t.Fatal("review did not require dashboard sign-in")
	}
	ticket, err := f.server.apid.openBrowserApprovalTicket(request.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	ticket.ExpiresAt = time.Now().Add(-time.Minute)
	data, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.server.apid.sessions.SealForCSRF(data)
	if err != nil {
		t.Fatal(err)
	}
	response := f.apiCall(t, "approval-requests/exchange", api.BrowserPromotionApprovalExchange{Ticket: sealed, ExchangeCode: request.ExchangeCode})
	if response.Code != 410 {
		t.Fatal("expired review was exchangeable")
	}
	// The public review selector cannot be interpreted as a login code.
	if _, valid := normalizeCliAuthCode(request.ExchangeCode); valid {
		t.Fatal("approval poll secret entered CLI login domain")
	}
}

func TestMCPBrowserPromotionApprovalExecutesNativePromotion(t *testing.T) {
	f := newBrowserPromotionFixture(t)
	if err := f.server.store.MarkAccountEmailVerified(t.Context(), f.account.ID); err != nil {
		t.Fatal(err)
	}
	request := f.requestReview(t)
	cookie, csrf := f.reviewPage(t, request)
	if approved := f.submit(request, cookie, csrf, f.session); approved.Code != 200 {
		t.Fatal("browser did not approve promotion")
	}
	exchanged := f.apiCall(t, "approval-requests/exchange", api.BrowserPromotionApprovalExchange{Ticket: request.Ticket, ExchangeCode: request.ExchangeCode})
	if exchanged.Code != 200 {
		t.Fatalf("exchange=%d", exchanged.Code)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(exchanged.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	result := f.apiCall(t, "promote", api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: f.preview.PromotionToken, ApprovalToken: approval.ApprovalToken})
	if result.Code != 200 {
		t.Fatalf("native promotion=%d %s", result.Code, result.Body.String())
	}
	var promotion api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(result.Body.Bytes(), &promotion); err != nil {
		t.Fatal(err)
	}
	if promotion.PromotionID == "" || len(promotion.Workloads) != 1 || promotion.Workloads[0].Status != "promoted" {
		t.Fatal("native promotion did not accept browser approval")
	}
	stored, err := f.server.store.ProjectEnvironmentApprovalByID(t.Context(), f.account.ID, "shop", "production", approval.ApprovalID)
	if err != nil || stored.ConsumedAt == nil {
		t.Fatal("native promotion did not consume approval")
	}
}
