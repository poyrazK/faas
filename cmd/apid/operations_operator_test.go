// adr: 521
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	operationcontracts "github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationOperatorTestStore interface {
	state.Store
	state.OperationStore
	state.PlatformTenantStore
	state.PlatformTenantAccessStore
}

func testOperationOperatorHTTP(t *testing.T, store operationOperatorTestStore) {
	t.Helper()
	ctx := t.Context()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	acct, err := store.CreateAccount(ctx, "operator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "operator-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(account string, scopes []string) string {
		t.Helper()
		raw, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(ctx, account, hash, "operator", scopes); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	owner, read, other := key(acct.ID, api.ScopesAdminOnly), key(acct.ID, []string{api.ScopeAppsRead}), key(foreign.ID, api.ScopesAdminOnly)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "operator-exports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/operation", SecretSealed: []byte("sealed-test-secret"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{HTTPTransactionVersion: 1, Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`), CompletionWebhookID: hook.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	alice, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := store.CreatePlatformTenant(ctx, acct.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{AccountID: acct.ID, TenantID: alice.ID, Name: "customer", Prefix: prefix, TokenHash: hash, Scopes: []string{api.ScopePlatformTenantOperationsRead}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	operations := []state.Operation{}
	for _, tenant := range []string{alice.ID, alice.ID, bob.ID} {
		op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant, IdempotencyKey: "op-" + string(rune('a'+len(operations))), Input: []byte(`{"private":"do not list"}`)})
		if err != nil {
			t.Fatal(err)
		}
		operations = append(operations, op)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	handler := srv.handler()
	base := "/v1/apps/" + app.Slug + "/operations"
	idPath := base + "/" + operations[0].ID
	call := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var input io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, input)
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	check := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("HTTP %d want %d: %s", w.Code, code, w.Body.String())
		}
		if code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("operation evidence cacheable")
		}
	}
	// Discovery reads immutable deployment pins without opening admission.
	definitionPath := "/v1/apps/" + app.Slug + "/deployments/" + dep.ID + "/operation-definitions"
	alpha := def
	alpha.ID, alpha.Spec.Name, alpha.Spec.Path = "", "alpha-export", "/alpha-export"
	if _, err := store.PutOperationDefinition(ctx, alpha); err != nil {
		t.Fatal(err)
	}
	discovery := call("GET", definitionPath, read, nil)
	check(discovery, 200)
	var definitions api.OperationDefinitionsResponse
	if err := json.Unmarshal(discovery.Body.Bytes(), &definitions); err != nil {
		t.Fatal(err)
	}
	if len(definitions.Definitions) != 2 || definitions.Definitions[0].Name != "alpha-export" || definitions.Definitions[1].ID != def.ID || definitions.Definitions[1].DeploymentID != dep.ID || definitions.Definitions[1].CompletionWebhookID != hook.ID || definitions.Definitions[1].HTTPTransactionVersion != 1 {
		t.Fatalf("definition discovery: %s", discovery.Body.String())
	}
	if strings.Contains(discovery.Body.String(), "input_schema") || strings.Contains(discovery.Body.String(), "output_schema") {
		t.Fatal("collection includes unbounded schema documents")
	}
	detail := call("GET", definitionPath+"/export", read, nil)
	check(detail, 200)
	var definition api.OperationDefinitionResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	contract, err := operationcontracts.Compile(definition.Spec, api.MustLimitsFor(acct.Plan).Operations)
	if err != nil || definition.ID != def.ID || definition.Revision != def.Revision || contract.Revision != def.Revision {
		t.Fatal("immutable contract changed during read")
	}
	for _, path := range []string{definitionPath, definitionPath + "/export"} {
		check(call("GET", path, other, nil), 404)
		check(call("GET", path, token, nil), 403)
		check(call("GET", path+"?scope=staging", read, nil), 400)
	}
	check(call("GET", definitionPath+"/missing", read, nil), 404)
	secondApp, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "other-exports", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	check(call("GET", "/v1/apps/"+secondApp.Slug+"/deployments/"+dep.ID+"/operation-definitions", read, nil), 404)
	emptyDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	emptyList := call("GET", "/v1/apps/"+app.Slug+"/deployments/"+emptyDep.ID+"/operation-definitions", read, nil)
	check(emptyList, 200)
	if emptyList.Body.String() != "{\"definitions\":[]}\n" {
		t.Fatal("empty collection is not an array", emptyList.Body.String())
	}
	check(call("GET", "/v1/apps/"+app.Slug+"/deployments/"+emptyDep.ID+"/operation-definitions/export", read, nil), 404)
	manageToken, managePrefix, manageHash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlatformTenantAccessToken(ctx, state.PlatformTenantAccessTokenInput{AccountID: acct.ID, TenantID: alice.ID, Name: "submission", Prefix: managePrefix, TokenHash: manageHash, Scopes: []string{api.ScopePlatformTenantOperationsManage}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	identityPath := "/v1/platform-tenant-self/customer-operations/identity"
	verified := call("GET", identityPath, manageToken, nil)
	check(verified, 200)
	var identity api.OperationTenantIdentity
	if err := json.Unmarshal(verified.Body.Bytes(), &identity); err != nil || identity.AccountID != acct.ID || identity.PlatformTenantID != alice.ID {
		t.Fatal("tenant identity not credential-derived", verified.Body.String(), err)
	}
	check(call("GET", identityPath, owner, nil), 403)
	check(call("GET", identityPath, token, nil), 403)
	check(call("GET", identityPath+"?tenant_id="+bob.ID, manageToken, nil), 400)
	if strings.Contains(verified.Body.String(), manageToken) {
		t.Fatal("identity response exposes a credential")
	}
	discoveryServer := httptest.NewServer(handler)
	discoveryClient := api.NewClient(discoveryServer.URL, read).SetCompletionCache(nil)
	if page, err := discoveryClient.ListOperationDefinitions(ctx, app.Slug, dep.ID); err != nil || len(page.Definitions) != 2 {
		t.Fatal("Go discovery client", err)
	}
	if got, err := discoveryClient.GetOperationDefinition(ctx, app.Slug, dep.ID, "export"); err != nil || got.ID != def.ID {
		t.Fatal("Go definition client", err)
	}
	if got, err := api.NewClient(discoveryServer.URL, manageToken).SetCompletionCache(nil).GetPlatformTenantSelfOperationIdentity(ctx); err != nil || got != identity {
		t.Fatal("Go identity client", err)
	}
	testOperationDoctorHTTP(t, srv, store, acct, dep, alice, hook, read, other, token, call)
	discoveryServer.Close()
	q := "?scope=" + url.QueryEscape(dep.Scope) + "&limit=1"
	w := call("GET", base+q, read, nil)
	check(w, 200)
	page := api.OperationListResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Operations) != 1 || page.NextCursor == "" || page.Operations[0].PlatformTenantID == "" {
		t.Fatalf("operator listing: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "input") || strings.Contains(w.Body.String(), "capability") {
		t.Fatal("private execution data leaked")
	}
	seen := map[string]bool{page.Operations[0].ID: true}
	for page.NextCursor != "" {
		w = call("GET", base+q+"&cursor="+url.QueryEscape(page.NextCursor), read, nil)
		check(w, 200)
		page = api.OperationListResponse{}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Operations {
			if seen[row.ID] {
				t.Fatal("duplicate keyset row")
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatal("cross-tenant account history incomplete")
	}
	w = call("GET", base+"?scope="+dep.Scope+"&tenant_id="+alice.ID, read, nil)
	check(w, 200)
	page = api.OperationListResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Operations) != 2 {
		t.Fatal("tenant filter lost")
	}
	for _, path := range []string{base + q, idPath + "/events", idPath + "/executions"} {
		check(call("GET", path, token, nil), 403)
		check(call("GET", path, other, nil), 404)
	}
	for _, suffix := range []string{"", "?scope=", "?scope=" + dep.Scope + "&tenant_id=bad", "?scope=" + dep.Scope + "&limit=101", "?scope=" + dep.Scope + "&scope=staging", "?scope=" + dep.Scope + "&app_id=" + app.ID, "?scope=" + dep.Scope + "&state=bogus"} {
		check(call("GET", base+suffix, read, nil), 400)
	}
	w = call("GET", idPath+"/events?after=0", read, nil)
	check(w, 200)
	var events api.OperationEventsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil || len(events.Events) != 1 {
		t.Fatalf("durable initial event: %s %v", w.Body.String(), err)
	}
	check(call("GET", idPath+"/events?after=-1", read, nil), 400)
	check(call("GET", idPath+"/events?after=0&after=1", read, nil), 400)
	check(call("GET", idPath+"/executions?limit=0", read, nil), 400)
	// Two fenced reconciliation decisions preserve the operation identity and
	// execution generation history. Stale decisions and changed receipt payloads fail.
	op := operations[0]
	claim, err := store.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, claim.ID, "unknown external effect", time.Second, 10, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
	req := api.OperationRecoveryRequest{RecoveryID: "provider-check-1", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger proves no effect"}
	check(call("POST", idPath+"/recover", read, req), 403)
	w = call("POST", idPath+"/recover", owner, req)
	check(w, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &op.OperationResponse); err != nil {
		t.Fatal(err)
	}
	check(call("POST", idPath+"/recover", owner, req), 200)
	changed := req
	changed.Evidence = "different decision"
	check(call("POST", idPath+"/recover", owner, changed), 409)
	current, err := store.OperationByID(ctx, acct.ID, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != 2 {
		t.Fatal("recovery did not advance generation")
	}
	claim, err = store.ClaimInvocation(ctx, current.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, claim.ID, "lost output receipt", time.Second, 10, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
	req = api.OperationRecoveryRequest{RecoveryID: "provider-check-2", ExpectedGeneration: 1, Resolution: "succeeded", Evidence: "provider proves export exists", Result: []byte(`{"file":"export.csv"}`)}
	check(call("POST", idPath+"/recover", owner, req), 409)
	req.ExpectedGeneration = 2
	bad := req
	bad.Result = []byte(`{"wrong":"file"}`)
	check(call("POST", idPath+"/recover", owner, bad), 400)
	w = call("POST", idPath+"/recover", owner, req)
	check(w, 200)
	current, err = store.OperationByID(ctx, acct.ID, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != api.OperationSucceeded || current.CompletionDelivery.State != "pending" {
		t.Fatalf("separate outcome %v", current.OperationResponse)
	}
	w = call("GET", idPath+"/executions?limit=1", read, nil)
	check(w, 200)
	var executions api.OperationExecutionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &executions); err != nil {
		t.Fatal(err)
	}
	if len(executions.Executions) != 1 || executions.Executions[0].Generation != 1 || executions.Executions[0].Attempts != 1 || executions.NextGeneration != 1 {
		t.Fatalf("execution history %+v", executions)
	}
	w = call("GET", idPath+"/executions?limit=1&after=1", read, nil)
	check(w, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &executions); err != nil {
		t.Fatal(err)
	}
	if len(executions.Executions) != 1 || executions.Executions[0].Generation != 2 {
		t.Fatal("recovered generation missing")
	}
	check(call("POST", idPath+"/retry-delivery", read, nil), 403)
	check(call("POST", idPath+"/retry-delivery", owner, nil), 400)
	deliveryID := current.CompletionDelivery.DeliveryID
	claims, err := store.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, delivery := range claims {
		if delivery.ID == deliveryID {
			matched = true
			if err := store.MarkAppWebhookDeliveryDead(ctx, delivery.ID, delivery.Attempt, delivery.NextAttemptAt, "receiver rejected", state.AppWebhookAttemptMetadata{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !matched {
		t.Fatal("completion notification not claimable")
	}
	// Concurrent operators cannot both redrive the same delivery. Business state,
	// pinned output and generation are unchanged by the winning notification retry.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- call("POST", idPath+"/retry-delivery", owner, nil).Code }()
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code == 400 {
			conflict++
		} else {
			t.Fatalf("delivery retry HTTP %d", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("duplicate delivery retry was not fenced")
	}
	got, err := store.OperationByID(ctx, acct.ID, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != api.OperationSucceeded || got.Generation != current.Generation || got.CurrentInvocationID != current.CurrentInvocationID || !bytes.Equal(got.Result, current.Result) || got.CompletionDelivery.State != "pending" {
		t.Fatal("delivery retry changed business work")
	}
	testOperationDeliveryHTTP(t, store, got, acct.ID, hook.ID, idPath, owner, read, other, token, call)

	if _, err := store.OperationExecutions(ctx, foreign.ID, op.ID, 0, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign execution read: %v", err)
	}
	// The actual Go client consumes these account-owned routes with admission shut.
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	client := api.NewClient(httpServer.URL, read)
	history, err := client.ListAccountOperations(ctx, app.Slug, api.OperationListOptions{Scope: dep.Scope, TenantID: alice.ID})
	if err != nil || len(history.Operations) != 2 {
		t.Fatalf("Go client list: %+v %v", history, err)
	}
	if _, err := client.GetAccountOperationEvents(ctx, app.Slug, op.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetOperationExecutions(ctx, app.Slug, op.ID, 0, 100); err != nil {
		t.Fatal(err)
	}
	observed, err := client.GetOperationDelivery(ctx, app.Slug, op.ID)
	if err != nil || observed.State != "delivery_expired" || observed.BusinessState != api.OperationSucceeded {
		t.Fatalf("Go client delivery observation: %+v %v", observed, err)
	}
	generation := 1
	decision, err := api.NewClient(httpServer.URL, owner).RetryOperationDeliveryWithReceipt(ctx, app.Slug, op.ID, api.OperationDeliveryRetryRequest{RetryID: "stable-retry", DeliveryID: deliveryID, ExpectedReplayGeneration: &generation})
	if err != nil || decision.ReplayGeneration != 2 || decision.State != "queued" {
		t.Fatalf("Go client retained retry: %+v %v", decision, err)
	}

}

func TestOperationOperatorHTTPMem(t *testing.T) { testOperationOperatorHTTP(t, state.NewMemStore()) }
