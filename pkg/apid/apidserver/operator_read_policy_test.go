package apidserver_test

// ADR 20261023214253: MFA is mandatory for platform operators, and an
// operator key reads fleet telemetry but no tenant's data.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	operatorPolicyEmail    = "ops@gregale.dev"
	operatorPolicyPassword = "operator-horse-battery-staple"
)

type operatorPolicyEnv struct {
	h     http.Handler
	store *state.MemStore
	acct  state.Account
	key   string
	issue func(t *testing.T, mfaPending bool) *http.Cookie
}

// newOperatorPolicyEnv is one operator with a password, an admin-scope key
// and no authenticator, on a server whose allowlist names them.
func newOperatorPolicyEnv(t *testing.T, bearerReads string) operatorPolicyEnv {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, operatorPolicyEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(operatorPolicyPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(ctx, acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operator-automation", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	srv.WithAdminAllowlist(operatorPolicyEmail).WithOperatorBearerReads(bearerReads)
	issue := func(t *testing.T, mfaPending bool) *http.Cookie {
		t.Helper()
		sid := uuid.NewString()
		if _, err := store.CreateSession(ctx, sid, acct.ID, "192.0.2.10", "operator-policy-test"); err != nil {
			t.Fatal(err)
		}
		token, err := (*srv.Sessions()).IssueWithSessionAndBindingHash(sid, acct.ID, "", mfaPending)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: apidserver.SessionCookie, Value: token}
	}
	return operatorPolicyEnv{h: srv.Handler(), store: store, acct: acct, key: key, issue: issue}
}

func (e operatorPolicyEnv) enroll(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := e.store.SetMFASecret(ctx, e.acct.ID, []byte("sealed-test-secret"), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkMFAEnrolled(ctx, e.acct.ID); err != nil {
		t.Fatal(err)
	}
}

// operatorSessionCookie enrolls accountID and issues it an MFA-completed
// session on the server whose session manager is sessions. Provider reads of
// customer data refuse keys (ADR 20261023214253), so tests read them this way.
func operatorSessionCookie(t *testing.T, store *state.MemStore, sessions **session.Manager, accountID string) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	acct, err := store.AccountByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.MFAEnrolled() {
		if err := store.SetMFASecret(ctx, accountID, []byte("sealed-test-secret"), nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMFAEnrolled(ctx, accountID); err != nil {
			t.Fatal(err)
		}
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(ctx, sid, accountID, "192.0.2.10", "operator-policy-test"); err != nil {
		t.Fatal(err)
	}
	token, err := (*sessions).IssueWithSessionAndBindingHash(sid, accountID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: apidserver.SessionCookie, Value: token}
}

func (e operatorPolicyEnv) get(t *testing.T, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	} else {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// sessionOnlyOperatorReads return customer data; bearerReadableOperatorReads
// are fleet telemetry. The ADR's table, one route per row.
func sessionOnlyOperatorReads(tenantID string) []string {
	id := uuid.NewString()
	return []string{
		"/v1/admin/obs/overview",
		"/v1/admin/obs/tenants",
		"/v1/admin/obs/tenants/" + tenantID,
		"/v1/admin/obs/tenants/" + tenantID + "/360",
		"/v1/admin/obs/tenants/" + tenantID + "/activity",
		"/v1/admin/obs/apps/" + id,
		"/v1/admin/obs/nodes/node-1/detail",
		"/v1/admin/obs/nodes/events",
		"/v1/admin/obs/anomalies",
		"/v1/admin/obs/rate-limits",
		"/v1/admin/obs/audit-log/search",
		"/v1/admin/obs/events",
		"/v1/admin/obs/traces/" + strings.Repeat("a", 32),
		"/v1/admin/obs/incidents",
		"/v1/admin/obs/incidents/deployment/" + id + "/evidence",
		"/v1/admin/obs/incidents/deployment/" + id + "/context",
		"/v1/admin/obs/incidents/platform_alert/recurrence",
		"/v1/admin/egress-flows",
		"/v1/admin/managed-postgres/usage/" + id,
		"/v1/admin/managed-postgres/accounting/" + id,
		"/v1/admin/ops/jobs/runs",
		"/v1/admin/ops/jobs/runs/" + id,
		"/v1/admin/ops/deployments",
		"/v1/admin/ops/deployments/" + id,
		"/v1/admin/accounts/" + id + "/flags",
		"/v1/audit-log/all",
		// Amendment 1: provider reads outside obs that name tenants.
		"/v1/admin/ops/data-repairs",
		"/v1/admin/operator-intents/" + id,
		"/v1/admin/ops/github/recovery",
		"/v1/admin/config",
		"/v1/admin/config-operations/" + id,
		"/v1/admin/config/platform_flag.example/revisions",
		"/v1/admin/flags",
		"/v1/admin/flags/example",
		"/v1/admin/flags/example/decision?account_id=" + tenantID,
	}
}

var bearerReadableOperatorReads = []string{
	"/v1/admin/obs/capacity",
	"/v1/admin/obs/nodes",
	"/v1/admin/obs/nodes/node-1/heartbeats",
	"/v1/admin/obs/nodes/wake-latency",
	"/v1/admin/obs/builder-heartbeats",
	"/v1/admin/obs/health",
	"/v1/admin/obs/notification-health",
	"/v1/admin/obs/rule-health",
	"/v1/admin/obs/rule-coverage",
	"/v1/admin/obs/recording-coverage",
	"/v1/admin/obs/input-health",
	"/v1/admin/obs/collection-history",
	"/v1/admin/obs/incidents/platform_alert/summary",
	"/v1/admin/obs/incidents/platform_alert/response-times",
	"/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("a", 64) + "/triage-history",
	// Amendment 1.
	"/v1/admin/billing-paddle-catalog",
	"/v1/admin/billing-paddle-overage/preflight",
	"/v1/admin/secrets/rekey-progress",
}

// strictOperatorSessionReads were session-only before the ADR and never take
// the FAAS_OPERATOR_BEARER_READS grace.
var strictOperatorSessionReads = map[string]bool{"GET /v1/admin/status/incidents": true}

// Every provider read picks a policy at the route table, so a new GET
// /v1/admin route cannot fall back to the admin scope alone, which every
// customer's own key and dashboard session hold.
func TestEveryAdminReadUsesAnOperatorReadPolicy(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`mux\.Handle(?:Func)?\(\s*"(GET /v1/admin/[^"]*)"`)
	seen := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		for _, loc := range route.FindAllStringSubmatchIndex(src, -1) {
			seen++
			pattern, call := src[loc[2]:loc[3]], registrationCall(src, loc[0])
			line := strings.Count(src[:loc[0]], "\n") + 1
			switch {
			case strictOperatorSessionReads[pattern]:
				for _, gate := range []string{"s.requireOperator(", "s.requireSessionPrincipal(", "s.requireEnrolledOperatorSession("} {
					if !strings.Contains(call, gate) {
						t.Errorf("%s:%d: %s lacks %s", file, line, pattern, gate)
					}
				}
			case strings.Contains(call, "s.requireOperatorSessionRead(") || strings.Contains(call, "s.requireOperatorRead("):
			default:
				t.Errorf("%s:%d: %s uses neither requireOperatorRead nor requireOperatorSessionRead", file, line, pattern)
			}
		}
	}
	if seen < 50 {
		t.Fatalf("found %d GET /v1/admin registrations; the scan no longer matches the route table", seen)
	}
}

// registrationCall is the text of the mux.Handle/HandleFunc call at start,
// through its closing parenthesis.
func registrationCall(src string, start int) string {
	open := strings.IndexByte(src[start:], '(') + start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	return src[start:]
}

func refusedForSession(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "operator session required")
}

func TestOperatorPasswordLoginIsConfinedToMFAEnrollment(t *testing.T) {
	e := newOperatorPolicyEnv(t, "")
	form := url.Values{"email": {operatorPolicyEmail}, "password": {operatorPolicyPassword}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login := httptest.NewRecorder()
	e.h.ServeHTTP(login, req)
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == apidserver.SessionCookie && c.Value != "" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("password login issued no session: %d %s", login.Code, login.Body)
	}
	for _, path := range []string{"/v1/admin/obs/tenants/" + e.acct.ID + "/360", "/v1/admin/obs/health"} {
		apidserver.AssertProblem(t, e.get(t, path, cookie), http.StatusForbidden, api.CodeMFARequired)
	}
	// The account page, which leads to enrollment, stays reachable.
	if rec := e.get(t, "/v1/account", cookie); rec.Code != http.StatusOK {
		t.Fatalf("account page for a pending operator session: %d %s", rec.Code, rec.Body)
	}
}

// A session issued without mfa_pending (before this release, or by an older
// apid during a deploy) is refused until the operator enrolls.
func TestOperatorSessionWithoutAuthenticatorIsRefused(t *testing.T) {
	e := newOperatorPolicyEnv(t, "")
	cookie := e.issue(t, false)
	for _, path := range []string{"/v1/admin/obs/tenants/" + e.acct.ID + "/360", "/v1/admin/obs/health", "/v1/admin/status/incidents"} {
		apidserver.AssertProblem(t, e.get(t, path, cookie), http.StatusForbidden, api.CodeMFARequired)
	}
	e.enroll(t)
	if rec := e.get(t, "/v1/admin/obs/tenants/"+e.acct.ID+"/360", cookie); rec.Code != http.StatusOK {
		t.Fatalf("enrolled operator session: %d %s", rec.Code, rec.Body)
	}
}

func TestOperatorReadTiers(t *testing.T) {
	e := newOperatorPolicyEnv(t, "")
	for _, path := range sessionOnlyOperatorReads(e.acct.ID) {
		if rec := e.get(t, path, nil); !refusedForSession(rec) {
			t.Errorf("bearer key on session-only %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, path := range bearerReadableOperatorReads {
		if rec := e.get(t, path, nil); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("bearer key on bearer-readable %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	e.enroll(t)
	cookie := e.issue(t, false)
	for _, path := range append(sessionOnlyOperatorReads(e.acct.ID), bearerReadableOperatorReads...) {
		if strings.HasSuffix(path, "/nodes/events") {
			continue // a stream; its session path is covered by the SSE tests
		}
		if rec := e.get(t, path, cookie); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("operator session on %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestOperatorBearerReadsDeprecatedGrace(t *testing.T) {
	e := newOperatorPolicyEnv(t, "deprecated")
	rec := e.get(t, "/v1/admin/obs/tenants/"+e.acct.ID+"/360", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Deprecation") != "true" || !strings.Contains(rec.Header().Get("Warning"), "299") {
		t.Fatalf("grace read: %d deprecation=%q warning=%q %s", rec.Code, rec.Header().Get("Deprecation"), rec.Header().Get("Warning"), rec.Body)
	}
	// The grace never reaches the route that was session-only before it.
	if rec := e.get(t, "/v1/admin/status/incidents", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("status incidents under grace: %d %s", rec.Code, rec.Body)
	}
}

func TestOperatorPasswordCannotMintKey(t *testing.T) {
	e := newOperatorPolicyEnv(t, "")
	login := func(email, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: password})
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	apidserver.AssertProblem(t, login(operatorPolicyEmail, operatorPolicyPassword), http.StatusForbidden, api.CodeMFARequired)
	keys, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("operator keys after a refused login = %d, want the 1 seeded", len(keys))
	}

	customer, err := e.store.CreateAccount(context.Background(), "customer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(operatorPolicyPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetAccountPassword(context.Background(), customer.ID, phc); err != nil {
		t.Fatal(err)
	}
	if rec := login("customer@example.com", operatorPolicyPassword); rec.Code != http.StatusOK {
		t.Fatalf("customer login: %d %s", rec.Code, rec.Body)
	}
}
