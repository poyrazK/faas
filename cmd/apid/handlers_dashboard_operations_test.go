// adr: 521
package main

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

type dashboardOperationFixture struct {
	handler http.Handler
	cookie  *http.Cookie
	store   *state.MemStore
	mgr     *session.Manager
	account state.Account
	app     state.App
	def     state.OperationDefinition
	tenant  state.PlatformTenant
}

func newDashboardOperationFixture(t *testing.T) dashboardOperationFixture {
	t.Helper()
	h, cookie, store, mgr := newAuthedDashboardServerFullFull(t, string(api.PlanPro), "alice@example.com")
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "business-exports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: acct.ID, AppID: app.ID,
		TargetURL: "https://receiver.example.test/completion", SecretSealed: []byte("private-hook-secret"),
		EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def := dashboardOperationTestDefinition(t, store, app, api.DefaultEnvScope, hook.ID)
	tenant, _, err := store.CreatePlatformTenant(t.Context(), acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	return dashboardOperationFixture{handler: h, cookie: cookie, store: store, mgr: mgr, account: acct, app: app, def: def, tenant: tenant}
}

func dashboardOperationTestDefinition(t *testing.T, store *state.MemStore, app state.App, scope, webhookID string) state.OperationDefinition {
	t.Helper()
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: app.AccountID,
		OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID,
			Spec: api.OperationDefinitionSpec{Name: "customer-export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
				InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
				ProgressStages: []string{"generating", "storing"}, CompletionWebhookID: webhookID}}})
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func (f dashboardOperationFixture) admit(t *testing.T, def state.OperationDefinition, tenant string) state.Operation {
	t.Helper()
	op, _, err := f.store.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: def.AccountID,
		DefinitionID: def.ID, PlatformTenantID: tenant, IdempotencyKey: uuid.NewString(), Input: []byte(`{"secret":"private-operation-input"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f dashboardOperationFixture) get(t *testing.T, path string, code int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(f.cookie)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != code {
		t.Fatalf("GET %s: HTTP %d want %d: %s", path, rec.Code, code, rec.Body.String())
	}
	if code == http.StatusOK && rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("operation evidence is cacheable")
	}
	return rec
}

func TestDashboardCustomerOperationsFiltersAndPagination(t *testing.T) {
	f := newDashboardOperationFixture(t)
	a := f.admit(t, f.def, f.tenant.ID)
	b := f.admit(t, f.def, f.tenant.ID)
	bob, _, err := f.store.CreatePlatformTenant(t.Context(), f.account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	otherCustomer := f.admit(t, f.def, bob.ID)
	staging := dashboardOperationTestDefinition(t, f.store, f.app, "staging", "")
	otherScope := f.admit(t, staging, f.tenant.ID)
	base := dashboardCustomerOperationsURL(f.app.Slug)
	query := url.Values{"scope": {api.DefaultEnvScope}, "tenant_id": {f.tenant.ID}, "name": {f.def.Spec.Name}, "state": {"accepted"}, "limit": {"1"}}
	first := f.get(t, base+"?"+query.Encode(), http.StatusOK).Body.String()
	link := regexp.MustCompile(`href="([^"]+)">Older operations`).FindStringSubmatch(first)
	if len(link) != 2 {
		t.Fatalf("missing next page: %s", first)
	}
	nextURL := html.UnescapeString(link[1])
	parsed, err := url.Parse(nextURL)
	if err != nil {
		t.Fatal(err)
	}
	for key := range query {
		if parsed.Query().Get(key) != query.Get(key) {
			t.Errorf("pagination lost %s filter", key)
		}
	}
	second := f.get(t, nextURL, http.StatusOK).Body.String()
	for _, id := range []string{a.ID, b.ID} {
		if strings.Contains(first, id) == strings.Contains(second, id) {
			t.Errorf("operation %s must appear on exactly one page", id)
		}
	}
	for _, body := range []string{first, second} {
		for _, excluded := range []string{otherCustomer.ID, otherScope.ID, "private-operation-input", "private-hook-secret"} {
			if strings.Contains(body, excluded) {
				t.Errorf("history leaked excluded value %q", excluded)
			}
		}
	}
	changed := parsed.Query()
	changed.Set("scope", "staging")
	f.get(t, base+"?"+changed.Encode(), http.StatusBadRequest)
	// Browsers submit blank optional controls. The explicit environment and
	// duplicate-control checks still apply after these controls are omitted.
	f.get(t, base+"?scope=default&tenant_id=&name=&state=", http.StatusOK)
	for _, suffix := range []string{"?scope=", "?scope=default&scope=staging", "?tenant_id=bad", "?state=bogus", "?limit=101", "?limit=0", "?app_id=" + f.app.ID, "?name=&name=customer-export", "?cursor="} {
		t.Run(suffix, func(t *testing.T) { f.get(t, base+suffix, http.StatusBadRequest) })
	}
	empty := f.get(t, base+"?scope=unused", http.StatusOK).Body.String()
	if !strings.Contains(empty, "No retained operations match these filters") {
		t.Fatal("missing empty state")
	}
}

func TestDashboardCustomerOperationBusinessOutcomeAndExecutionEvidence(t *testing.T) {
	f := newDashboardOperationFixture(t)
	op := f.admit(t, f.def, f.tenant.ID)
	claim, err := f.store.ClaimInvocation(t.Context(), op.CurrentInvocationID, uuid.NewString(), 60)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err := json.Unmarshal(claim.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	authority := state.OperationExecutionAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: claim.InstanceID,
		InvocationID: claim.ID, Attempt: claim.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	if _, err := f.store.ReportOperationProgress(t.Context(), op.ID, authority, api.OperationReportRequest{ReportID: "progress-1", Stage: "generating", Completed: 4, Total: 10}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteKeyedInvocation(t.Context(), claim.ID, claim.Attempts, []byte(`{"secret":"private-operation-result"}`)); err != nil {
		t.Fatal(err)
	}
	deliveries, err := f.store.ClaimDueAppWebhookDeliveries(t.Context(), 10, time.Now().Add(time.Minute))
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("completion delivery: %v %v", deliveries, err)
	}
	delivery := deliveries[0]
	if err := f.store.MarkAppWebhookDeliveryDead(t.Context(), delivery.ID, delivery.Attempt, delivery.NextAttemptAt,
		"private-notification-error", state.AppWebhookAttemptMetadata{}); err != nil {
		t.Fatal(err)
	}
	base := dashboardCustomerOperationsURL(f.app.Slug)
	body := f.get(t, base+"/"+op.ID, http.StatusOK).Body.String()
	for _, want := range []string{"Succeeded", "Dead letter", "generating", "storing", "4 of 10", "Current reported stage",
		"Business work succeeded", "Execution started", "Progress reported", "A retained JSON result is available",
		f.tenant.ID, f.def.Revision, "/dashboard/apps/" + f.app.Slug + "/deployments/" + f.def.DeploymentID,
		dashboardAsyncInvocationPath + claim.ID, delivery.ID} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	for _, private := range []string{"private-operation-input", "private-operation-result", "private-notification-error", "private-hook-secret", authority.Capability} {
		if private != "" && strings.Contains(body, private) {
			t.Errorf("detail leaked %q", private)
		}
	}
	// Following the evidence link reaches the existing scoped invocation view.
	f.get(t, dashboardAsyncInvocationPath+claim.ID, http.StatusOK)
	// Reading a future event cursor shows the current snapshot and an explicit
	// history gap; it never turns missing events into a failed business result.
	resync := f.get(t, base+"/"+op.ID+"?event_after=999", http.StatusOK).Body.String()
	if !strings.Contains(resync, "Succeeded") || !strings.Contains(resync, "Retained events cannot cover the selected cursor") {
		t.Fatal("missing resynchronization state")
	}
	current, err := f.store.OperationByID(t.Context(), f.account.ID, "", op.ID)
	if err != nil || current.State != api.OperationSucceeded || current.CompletionDelivery.State != "dead" || current.Generation != 1 {
		t.Fatalf("read changed operation: %+v %v", current, err)
	}
}

func TestDashboardCustomerOperationsOwnershipAndSession(t *testing.T) {
	f := newDashboardOperationFixture(t)
	op := f.admit(t, f.def, f.tenant.ID)
	foreignAccount, err := f.store.CreateAccount(t.Context(), "foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignApp, err := f.store.CreateApp(t.Context(), state.App{AccountID: foreignAccount.ID, Slug: "foreign-business", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, _, err := f.store.CreatePlatformTenant(t.Context(), foreignAccount.ID, "foreign", "Foreign", 100)
	if err != nil {
		t.Fatal(err)
	}
	foreignDef := dashboardOperationTestDefinition(t, f.store, foreignApp, api.DefaultEnvScope, "")
	foreignOp := f.admit(t, foreignDef, foreignTenant.ID)
	otherApp, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "other-business", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	base := dashboardCustomerOperationsURL(f.app.Slug)
	for _, path := range []string{dashboardCustomerOperationsURL(foreignApp.Slug), base + "/" + foreignOp.ID,
		dashboardCustomerOperationsURL(otherApp.Slug) + "/" + op.ID, base + "/missing", base + "/" + op.ID + "/extra"} {
		t.Run(path, func(t *testing.T) { f.get(t, path, http.StatusNotFound) })
	}
	for _, query := range []string{"?event_after=-1", "?execution_after=2147483648", "?event_after=1&event_after=2", "?execution_after=", "?tenant_id=" + f.tenant.ID} {
		t.Run(query, func(t *testing.T) { f.get(t, base+"/"+op.ID+query, http.StatusBadRequest) })
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base+"/"+op.ID, nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatal("unauthenticated operation view did not require a session")
	}
}

func TestDashboardCustomerOperationHistoryCursorsRemainIndependent(t *testing.T) {
	f := newDashboardOperationFixture(t)
	op := f.admit(t, f.def, f.tenant.ID)
	for range 35 {
		claim, err := f.store.ClaimInvocation(t.Context(), op.CurrentInvocationID, "", 60)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.FailInvocation(t.Context(), claim.ID, "uncertain response", time.Second, 10, state.WithClaimAttempt(claim.Attempts)); err != nil {
			t.Fatal(err)
		}
		op, err = f.store.RecoverOperation(t.Context(), f.account.ID, "", op.ID, api.OperationRecoveryRequest{
			RecoveryID: uuid.NewString(), ExpectedGeneration: op.Generation, Resolution: "safe_to_retry", Evidence: "provider confirms no effects"})
		if err != nil {
			t.Fatal(err)
		}
	}
	base := dashboardCustomerOperationsURL(f.app.Slug) + "/" + op.ID
	body := f.get(t, base+"?event_after=4&execution_after=1", http.StatusOK).Body.String()
	for _, test := range []struct {
		text     string
		preserve string
		value    string
	}{
		{"More events", "execution_after", "1"},
		{"More generations", "event_after", "4"},
	} {
		link := regexp.MustCompile(`href="([^"]+)">` + test.text).FindStringSubmatch(body)
		if len(link) != 2 {
			t.Fatalf("missing %s link", test.text)
		}
		path := html.UnescapeString(link[1])
		parsed, err := url.Parse(path)
		if err != nil || parsed.Query().Get(test.preserve) != test.value {
			t.Fatalf("%s changed other cursor: %s %v", test.text, path, err)
		}
		parsed.Fragment = ""
		f.get(t, parsed.String(), http.StatusOK)
	}
	if !strings.Contains(body, "Recovery requested") || !strings.Contains(body, "Accepted") {
		t.Fatal("recovery lost logical operation context")
	}
}

type dashboardOperationEvidenceFailureStore struct {
	state.Store
	state.OperationStore
}

func (s dashboardOperationEvidenceFailureStore) OperationEvents(context.Context, string, string, string, int64, int) (api.OperationEventsResponse, error) {
	return api.OperationEventsResponse{}, errors.New("private event backend error")
}

func (s dashboardOperationEvidenceFailureStore) OperationExecutions(context.Context, string, string, int, int) (api.OperationExecutionsResponse, error) {
	return api.OperationExecutionsResponse{}, errors.New("private execution backend error")
}

func (s dashboardOperationEvidenceFailureStore) OperationDefinitionByID(context.Context, string, string) (state.OperationDefinition, error) {
	return state.OperationDefinition{}, state.ErrNotFound
}

func TestDashboardCustomerOperationEvidenceFailurePreservesBusinessSnapshot(t *testing.T) {
	f := newDashboardOperationFixture(t)
	op := f.admit(t, f.def, f.tenant.ID)
	store := dashboardOperationEvidenceFailureStore{Store: f.store, OperationStore: f.store}
	srv := newServerWithDeps(store, discardLogger(), "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, f.mgr, nil, time.Minute, "")
	f.handler = srv.handler()
	body := f.get(t, dashboardCustomerOperationsURL(f.app.Slug)+"/"+op.ID, http.StatusOK).Body.String()
	for _, want := range []string{"Accepted", "The pinned definition is unavailable", "Execution history is temporarily unavailable", "Progress history is temporarily unavailable"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing evidence failure state %q", want)
		}
	}
	if strings.Contains(body, "private event backend error") || strings.Contains(body, "private execution backend error") || strings.Contains(body, "No events retained") {
		t.Fatal("evidence failures leaked details or appeared as empty history")
	}
}

func TestDashboardCustomerOperationEventProjectionDoesNotRenderArbitraryPayloads(t *testing.T) {
	for _, kind := range []string{"artifact_attached", "recovery_requested", "future_event", "progress"} {
		t.Run(kind, func(t *testing.T) {
			event := api.OperationEvent{Sequence: 1, Type: kind, Data: []byte(`{"uri":"private-storage-location","capability":"private-authority","stage":"<script>bad()</script>","completed":1,"total":2}`)}
			item := projectDashboardOperationEvent(event)
			data := dashboard.CustomerOperationsData{Detail: &dashboard.CustomerOperationDetail{Events: []dashboard.CustomerOperationEvent{item}}}
			rec := httptest.NewRecorder()
			if err := dashboard.Render(rec, discardLogger(), "test-nonce", dashboard.Page{Body: "customer_operations", Data: data}); err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			if strings.Contains(body, "private-storage-location") || strings.Contains(body, "private-authority") || strings.Contains(body, "<script>bad()") {
				t.Fatal("event payload was exposed or escaped incorrectly")
			}
			if kind == "progress" && !strings.Contains(body, "&lt;script&gt;") {
				t.Fatal("structured progress was not rendered safely")
			}
		})
	}
}
