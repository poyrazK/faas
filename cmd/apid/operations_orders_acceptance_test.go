// ADR-713: source-declared transactions recover committed order fulfillment without repeating business writes.
//go:build !no_pg

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/sched" //nolint:depguard // ADR-713 portable acceptance drives the scheduler; production apid only records intent.
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type orderAcceptanceFixture struct {
	store                               *state.PgStore
	account                             state.Account
	app                                 state.App
	definition                          api.OperationDefinitionResponse
	logger                              *slog.Logger
	preview                             *operations.PreviewAdmission
	ownerKey, apiURL, identityURL, node string
	tenants                             []state.PlatformTenant
	customerTokens, ingressKeys         []string
	call                                func(string, string, string, any) *httptest.ResponseRecorder
}

func newOrderAcceptance(t *testing.T) *orderAcceptanceFixture {
	t.Helper()
	if os.Getenv("GREGALE_OPERATIONS_ACCEPTANCE") != "1" {
		t.Skip("set GREGALE_OPERATIONS_ACCEPTANCE=1 with DATABASE_URL and built sdk/node")
	}
	ctx := t.Context()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../sdk/node/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sdk); err != nil {
		t.Fatal("build sdk/node before Operations acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "operations-orders@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "acceptance", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer(store, logger, "gregale.dev", noopNotifier{})
	srv.operationsAdmissionEnabled = true
	owner := httptest.NewServer(srv.handler())
	t.Cleanup(owner.Close)
	call := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		srv.handler().ServeHTTP(w, r)
		return w
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "operations-orders", Type: state.AppTypeApp, Status: state.AppActive, ConsumerAuthMode: api.ConsumerAuthModeRequired, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	nodeRow, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, nodeRow.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := orderAcceptanceSpec(t)
	w := call("PUT", "/v1/apps/"+app.Slug+"/deployments/"+dep.ID+"/operation-definitions/fulfill-order", key, spec)
	if w.Code != 200 {
		t.Fatalf("define: %d %s", w.Code, w.Body.String())
	}
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	var customerTokens, ingressKeys []string
	var tenants []state.PlatformTenant
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, name, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantConsumer(ctx, acct.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		plain, prefix, hash, err := api.GenerateConsumerKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateConsumerKeyForConsumer(ctx, acct.ID, consumer.ID, name, prefix, hash, []string{"read", "write"}, nil); err != nil {
			t.Fatal(err)
		}
		w := call("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
		if w.Code != 201 {
			t.Fatalf("token: %d %s", w.Code, w.Body.String())
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		customerTokens = append(customerTokens, token.Token)
		ingressKeys = append(ingressKeys, plain)
		tenants = append(tenants, tenant)
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "orders-acceptance", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	srv.operationsWorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(t.TempDir(), "preview.json")
	writeOperationPreviewPolicy(t, policyPath, acct.ID, app.ID, def.Scope, tenants[0].ID)
	jwksPath := filepath.Join(t.TempDir(), "workload-public-jwks.json")
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jwksPath, jwks, 0600); err != nil {
		t.Fatal(err)
	}
	if err := srv.configureOperations(Config{OperationsPreviewPolicyPath: policyPath, OperationsWorkloadJWKSPath: jwksPath}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	srv.operationsAdmissionEnabled = false
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != workloadidentity.OperationsAudience {
			http.Error(w, "audience", 400)
			return
		}
		token, err := signer.Mint(time.Now(), acct.ID, app.ID, instance.ID, workloadidentity.OperationsAudience)
		if err != nil {
			http.Error(w, "identity", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(token)
	}))
	t.Cleanup(identity.Close)

	return &orderAcceptanceFixture{store: store, account: acct, app: app, definition: def, logger: logger,
		preview: srv.operationsPreview, ownerKey: key, apiURL: owner.URL, identityURL: identity.URL, node: node, tenants: tenants,
		customerTokens: customerTokens, ingressKeys: ingressKeys, call: call}
}

func orderAcceptanceSpec(t *testing.T) api.OperationDefinitionSpec {
	t.Helper()
	root := "../../examples/customer-operation-orders"
	manifest, err := os.ReadFile(filepath.Join(root, "gregale.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := gregalemanifest.ParseBytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var entries []operationSchemaEntry
	for _, file := range []string{"gregale.yaml", "schemas/order-input.json", "schemas/order-output.json", "schemas/order-fulfilled.json"} {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, operationSchemaEntry{name: file, body: string(body), kind: tar.TypeReg})
	}
	if err := resolveSourceOperations(operationSchemaArchive(t, entries), "gregale.yaml", "operations-orders", api.PlanPro, m); err != nil {
		t.Fatal(err)
	}
	if len(m.ResolvedOperations) != 1 || m.ResolvedOperations[0].HTTPTransactionVersion != 1 || len(m.ResolvedOperations[0].WorkflowSteps) != 1 {
		t.Fatal("source bundle lost transaction negotiation")
	}
	return m.ResolvedOperations[0]
}

type orderAcceptanceResponse struct {
	Body      string `json:"body"`
	Replayed  bool   `json:"replayed"`
	Callbacks int    `json:"callbacks"`
	Status    int    `json:"status"`
}

type orderAcceptanceProcess struct {
	address   string
	responses <-chan orderAcceptanceResponse
	cancel    context.CancelFunc
}

func startOrderAcceptanceProcess(t *testing.T, f *orderAcceptanceFixture, database string, loseReply bool) *orderAcceptanceProcess {
	t.Helper()
	config, err := json.Marshal(map[string]any{"api": f.apiURL, "identity": f.identityURL, "database": database, "loseReply": loseReply})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, f.node, "testdata/operation-orders.mjs")
	cmd.Env = append(os.Environ(), "GREGALE_ORDERS_TEST_CONFIG="+string(config))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	responses := make(chan orderAcceptanceResponse, 2)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
		for scanner.Scan() {
			var response orderAcceptanceResponse
			if err := json.Unmarshal(scanner.Bytes(), &response); err == nil {
				responses <- response
			}
		}
		close(responses)
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("order handler failed to stop")
		}
	})
	var address string
	select {
	case address = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("order handler startup timed out")
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatal("order handler startup failed")
	}
	return &orderAcceptanceProcess{address: address, responses: responses, cancel: cancel}
}

func awaitOrderResponse(t *testing.T, process *orderAcceptanceProcess) orderAcceptanceResponse {
	t.Helper()
	select {
	case response, ok := <-process.responses:
		if !ok {
			t.Fatal("order handler exited without a committed response")
		}
		return response
	case <-time.After(20 * time.Second):
		t.Fatal("order handler response timed out")
	}
	return orderAcceptanceResponse{}
}

// Only the VM HTTP bridge is local. Ingress, definition bundling, ownership,
// workload reports, scheduler claims, completion fences and recovery use production code.
func TestOperationsOrderHTTPPostgresRecoveryAcceptance(t *testing.T) {
	f := newOrderAcceptance(t)
	ctx := t.Context()
	applicationDB := pgtest.OpenDatabase(t)
	for _, file := range []string{"../../examples/customer-operation-orders/schema.sql", "../../pkg/operationinbox/customer_schema.sql"} {
		schema, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := applicationDB.Exec(ctx, string(schema)); err != nil {
			t.Fatal(err)
		}
	}
	orderID := uuid.NewString()
	workflowRunID := uuid.NewString()
	if _, err := applicationDB.Exec(ctx, "INSERT INTO public.example_orders(id, platform_tenant_id) VALUES ($1, $2)", orderID, f.tenants[0].ID); err != nil {
		t.Fatal(err)
	}
	input := fmt.Sprintf(`{"order_id":%q,"workflow_run_id":%q}`, orderID, workflowRunID)
	backend := &operationsAcceptanceBackend{store: f.store, app: f.app, plan: f.account.Plan}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), f.logger).WithConsumerAuth(operationsAcceptanceConsumerStore{f.store}).WithOperationRoutes(gateway.DurableOperationRoutes{Store: f.store, Admission: f.preview})
	submit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "https://"+f.app.Slug+".gregale.dev/orders/fulfill", strings.NewReader(input))
		req.Header.Set("Authorization", "Bearer "+f.ingressKeys[0])
		req.Header.Set("Idempotency-Key", "fulfill-order-once")
		req.Header.Set(api.PlatformTenantIDHeader, f.tenants[1].ID)
		req.Header.Set(api.OperationTransactionVersionHeader, "999")
		req.Header.Set(api.OperationMilestoneVersionHeader, "999")
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, req)
		return w
	}
	accepted := submit()
	if accepted.Code != 202 {
		t.Fatalf("order admission: %d %s", accepted.Code, accepted.Body.String())
	}
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	duplicate := submit()
	if duplicate.Code != 202 || duplicate.Body.String() != accepted.Body.String() {
		t.Fatal("duplicate order submission created different logical work")
	}
	// ConnString retains pgx's original DSN even after OpenDatabase changes the
	// parsed database name. Point Node explicitly at this private application DB.
	applicationURL, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil || (applicationURL.Scheme != "postgres" && applicationURL.Scheme != "postgresql") {
		t.Fatal("order acceptance requires a PostgreSQL URL")
	}
	applicationURL.Path = "/" + applicationDB.Config().ConnConfig.Database
	first := startOrderAcceptanceProcess(t, f, applicationURL.String(), true)
	dispatcher := &operationsAcceptanceDispatcher{store: f.store, address: first.address}
	synthServer := httptest.NewServer(gateway.NewSynthServer("", dispatcher, f.logger).Mux())
	t.Cleanup(synthServer.Close)
	synth, err := sched.DialGatewaySynthTarget("tcp://"+strings.TrimPrefix(synthServer.URL, "http://"), nil, f.logger)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := sched.NewEngine(ctx, f.store, sched.NewNodeLedger(), nil, noopNotifier{}, "order-acceptance", f.logger)
	if err != nil {
		t.Fatal(err)
	}
	drain := sched.NewDrain(f.store, engine, sched.WithDrainGatewaySynth(synth), sched.WithDrainLogger(f.logger), sched.WithDrainWakeLease(1))
	tickDone := make(chan struct{})
	go func() { drain.Tick(ctx); close(tickDone) }()
	committed := awaitOrderResponse(t, first)
	if committed.Status != 200 || committed.Replayed || committed.Callbacks != 1 {
		t.Fatalf("initial transaction: %+v", committed)
	}
	assertOrderAcceptanceReceipt(t, applicationDB, f, receipt.ID, orderID, committed.Body)
	assertOrderMilestoneOutbox(t, applicationDB, receipt.ID, false)
	pending, err := f.store.ListAccountOperationMilestones(ctx, f.account.ID, api.OperationMilestoneListOptions{AppID: f.app.ID, Scope: f.definition.Scope, OperationID: receipt.ID})
	if err != nil || len(pending.Milestones) != 0 {
		t.Fatalf("published before failure window: %+v %v", pending, err)
	}
	first.cancel() // SIGKILL after commit, before milestone publication or HTTP result.
	select {
	case <-tickDone:
	case <-time.After(20 * time.Second):
		t.Fatal("lost response did not finish the first execution")
	}
	op, err := f.store.OperationByID(ctx, f.account.ID, f.tenants[0].ID, receipt.ID)
	if err != nil || op.State != api.OperationRequiresReconciliation || op.Generation != 1 {
		t.Fatalf("unknown outcome: %+v %v", op, err)
	}
	originalInvocation := op.CurrentInvocationID
	recovery := api.OperationRecoveryRequest{RecoveryID: "committed-order-receipt", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "Application PostgreSQL receipt and fulfilled order verified; same-operation replay returns the committed result"}
	path := "/v1/apps/" + f.app.Slug + "/operations/" + receipt.ID + "/recover"
	if denied := f.call("POST", path, f.customerTokens[0], recovery); denied.Code != 403 {
		t.Fatalf("customer authorized account recovery: %d", denied.Code)
	}
	recovered := f.call("POST", path, f.ownerKey, recovery)
	if recovered.Code != 200 {
		t.Fatalf("account recovery: %d %s", recovered.Code, recovered.Body.String())
	}
	replayRecovery := f.call("POST", path, f.ownerKey, recovery)
	if replayRecovery.Code != 200 {
		t.Fatalf("stable recovery replay: %d", replayRecovery.Code)
	}
	second := startOrderAcceptanceProcess(t, f, applicationURL.String(), false)
	dispatcher.address = second.address // First drain is complete, so no transport races with the replacement.
	drain.Tick(ctx)
	replayed := awaitOrderResponse(t, second)
	if !replayed.Replayed || replayed.Callbacks != 0 || replayed.Body != committed.Body {
		t.Fatalf("committed result was not replayed without business execution: %+v", replayed)
	}
	op, err = f.store.OperationByID(ctx, f.account.ID, f.tenants[0].ID, receipt.ID)
	if op.Subject == nil || op.Subject.Type != "order" || op.Subject.ID != orderID {
		t.Fatalf("lost business reference: %+v", op.Subject)
	}
	if err != nil || op.State != api.OperationSucceeded || op.Generation != 2 || op.CurrentInvocationID == originalInvocation || op.Progress == nil || op.Progress.Stage != "complete" || op.Progress.Attempt != 1 {
		t.Fatalf("recovered completion: %+v %v", op, err)
	}
	var actual, expected map[string]any
	if err := json.Unmarshal(op.Result, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(committed.Body), &expected); err != nil {
		t.Fatal(err)
	}
	if actual["order_id"] != expected["order_id"] || actual["status"] != expected["status"] {
		t.Fatalf("retained business result changed: %s", op.Result)
	}
	assertOrderAcceptanceReceipt(t, applicationDB, f, receipt.ID, orderID, committed.Body)
	assertOrderMilestoneOutbox(t, applicationDB, receipt.ID, true)
	assertOrderMilestoneAPI(t, f, receipt.ID, orderID)
	executions, err := f.store.OperationExecutions(ctx, f.account.ID, receipt.ID, 0, 10)
	if err != nil || len(executions.Executions) != 2 || executions.Executions[0].Generation != 1 || executions.Executions[1].Generation != 2 || executions.Executions[0].InvocationID == executions.Executions[1].InvocationID {
		t.Fatalf("execution history: %+v %v", executions, err)
	}
	selfPath := "/v1/platform-tenant-self/customer-operations/" + receipt.ID
	view := f.call("GET", selfPath, f.customerTokens[0], nil)
	if view.Code != 200 {
		t.Fatalf("customer result inspection: %d %s", view.Code, view.Body.String())
	}
	var customerView api.OperationResponse
	if err := json.Unmarshal(view.Body.Bytes(), &customerView); err != nil {
		t.Fatal(err)
	}
	var inspected map[string]any
	if err := json.Unmarshal(customerView.Result, &inspected); err != nil {
		t.Fatal(err)
	}
	if customerView.ID != receipt.ID || customerView.Subject == nil || *customerView.Subject != *op.Subject || customerView.State != api.OperationSucceeded || !reflect.DeepEqual(inspected, expected) {
		t.Fatal("customer inspection lost the committed business result")
	}
	if other := f.call("GET", selfPath, f.customerTokens[1], nil); other.Code != 404 {
		t.Fatalf("foreign customer read result: %d", other.Code)
	}
	historyQuery := url.Values{"app_id": {f.app.ID}, "scope": {f.definition.Scope}, "subject_type": {"order"}, "subject_id": {orderID}}.Encode()
	for customer, token := range f.customerTokens {
		history := f.call("GET", "/v1/platform-tenant-self/customer-operations?"+historyQuery, token, nil)
		if history.Code != 200 {
			t.Fatalf("customer history: %d %s", history.Code, history.Body.String())
		}
		var page api.OperationListResponse
		if err := json.Unmarshal(history.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if customer == 0 && (len(page.Operations) != 1 || page.Operations[0].ID != receipt.ID || page.Operations[0].Subject == nil || *page.Operations[0].Subject != *op.Subject) {
			t.Fatalf("logical operation history: %+v", page)
		}
		if customer == 1 && len(page.Operations) != 0 {
			t.Fatal("foreign customer discovered another customer's operation")
		}
	}
	testOrderAcceptanceBusinessAuthorization(t, second.address, f, receipt.ID, input)
}

func assertOrderAcceptanceReceipt(t *testing.T, pool *pgxpool.Pool, f *orderAcceptanceFixture, operationID, orderID, body string) {
	t.Helper()
	var status, saved, account, app, tenant string
	var count, receipts int
	if err := pool.QueryRow(t.Context(), "SELECT status, fulfillment_count FROM public.example_orders WHERE id = $1", orderID).Scan(&status, &count); err != nil {
		t.Fatal(err)
	}
	if status != "fulfilled" || count != 1 {
		t.Fatalf("business change: status=%s count=%d", status, count)
	}
	if err := pool.QueryRow(t.Context(), "SELECT response_body, account_id::text, app_id::text, platform_tenant_id::text FROM public.gregale_customer_operation_inbox WHERE operation_id = $1", operationID).Scan(&saved, &account, &app, &tenant); err != nil {
		t.Fatal(err)
	}
	if saved != body || account != f.account.ID || app != f.app.ID || tenant != f.tenants[0].ID {
		t.Fatal("receipt bytes or verified ownership changed")
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM public.gregale_customer_operation_inbox").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("business receipt count=%d", receipts)
	}
}

func testOrderAcceptanceBusinessAuthorization(t *testing.T, address string, f *orderAcceptanceFixture, operationID, input string) {
	t.Helper()
	// Simulate a trusted guest request from a different customer. Syntactically
	// valid execution metadata supplies no authorization to another customer's order.
	req, err := http.NewRequestWithContext(t.Context(), "POST", "http://"+address+"/orders/fulfill", strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{api.AppIDHeader: f.app.ID, api.TenantIDHeader: f.account.ID,
		api.PlatformTenantIDHeader: f.tenants[1].ID, api.OperationIDHeader: operationID,
		api.InvocationIDHeader: uuid.NewString(), api.OperationAttemptHeader: "1",
		api.OperationCapabilityHeader: strings.Repeat("a", 64), api.OperationTransactionVersionHeader: "1", api.OperationResultMaxBytesHeader: "1024"} {
		req.Header.Set(name, value)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 404 || strings.Contains(string(body), "fulfilled") {
		t.Fatalf("business authorization leaked the saved result: %d %s", res.StatusCode, body)
	}
}

func assertOrderMilestoneOutbox(t *testing.T, pool *pgxpool.Pool, operationID string, acknowledged bool) {
	t.Helper()
	var total, ack int
	if err := pool.QueryRow(t.Context(), "SELECT count(*),count(acknowledged_at) FROM public.gregale_customer_operation_milestones WHERE operation_id=$1", operationID).Scan(&total, &ack); err != nil {
		t.Fatal(err)
	}
	expected := 0
	if acknowledged {
		expected = 1
	}
	if total != 1 || ack != expected {
		t.Fatalf("outbox total=%d acknowledged=%d want=%d", total, ack, expected)
	}
}

func assertOrderMilestoneAPI(t *testing.T, f *orderAcceptanceFixture, operationID, orderID string) {
	t.Helper()
	query := url.Values{"app_id": {f.app.ID}, "scope": {f.definition.Scope}, "subject_type": {"order"}, "subject_id": {orderID}}
	var originalID string
	for _, path := range []string{"/v1/platform-tenant-self/customer-operations/" + operationID + "/milestones", "/v1/platform-tenant-self/customer-operation-milestones?" + query.Encode(), "/v1/apps/" + f.app.Slug + "/operations/" + operationID + "/milestones"} {
		token := f.customerTokens[0]
		if strings.HasPrefix(path, "/v1/apps/") {
			token = f.ownerKey
		}
		w := f.call("GET", path, token, nil)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("milestone feed may be cached across credentials")
		}
		var page api.OperationMilestonesResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Milestones) != 1 {
			t.Fatalf("milestone timeline: %d %s", w.Code, w.Body.String())
		}
		fact := page.Milestones[0]
		var payload map[string]string
		if json.Unmarshal(fact.Payload, &payload) != nil || payload["order_id"] != orderID || payload["status"] != "fulfilled" || fact.OperationID != operationID || fact.Subject == nil || fact.Subject.ID != orderID || fact.Name != "order-fulfilled" {
			t.Fatalf("incorrect business fact: %+v", fact)
		}
		if originalID != "" && originalID != fact.ID {
			t.Fatal("different milestone identity across timelines")
		}
		originalID = fact.ID
	}
	foreign := f.call("GET", "/v1/platform-tenant-self/customer-operations/"+operationID+"/milestones", f.customerTokens[1], nil)
	if foreign.Code != 404 {
		t.Fatalf("foreign operation milestones: %d", foreign.Code)
	}
	foreign = f.call("GET", "/v1/platform-tenant-self/customer-operation-milestones?"+query.Encode(), f.customerTokens[1], nil)
	var empty api.OperationMilestonesResponse
	if foreign.Code != 200 || json.Unmarshal(foreign.Body.Bytes(), &empty) != nil || len(empty.Milestones) != 0 {
		t.Fatalf("foreign business reference: %d %s", foreign.Code, foreign.Body.String())
	}
}
