// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
//go:build !no_pg

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched" //nolint:depguard // ADR-521 acceptance drives the real scheduler; production apid only records intent.
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// This runs the HTTP API, PostgreSQL, ingress, scheduler drain, synthetic
// protocol and real Node/Go SDKs with a Node handler. Only the VM bridge is replaced by a local
// HTTP transport; it does not qualify park/restore or native KVM acceptance.
func TestOperationsHTTPPostgresSDKAcceptance(t *testing.T) {
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
	goInspector := buildOperationsGoSDKInspector(t)
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "operations-sdk@example.com", api.PlanPro)
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
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "operations-sdk", Type: state.AppTypeApp, Status: state.AppActive, ConsumerAuthMode: api.ConsumerAuthModeRequired, PlatformTenantRequired: true})
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
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/completed", SecretSealed: []byte("acceptance-sealed"), EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:  []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
		OutputSchema: []byte(`{"type":"object","required":["file","operation_id"],"properties":{"file":{"type":"string"},"operation_id":{"type":"string"}},"additionalProperties":false}`), ProgressStages: []string{"generating", "uploading"}, CompletionWebhookID: hook.ID}
	w := call("PUT", "/v1/apps/"+app.Slug+"/deployments/"+dep.ID+"/operation-definitions/export", key, spec)
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
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "operations-acceptance", time.Minute)
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
	provider, artifact := operationArtifactFixture(t, srv, store, acct, app, def.Scope)
	sessionModule, err := filepath.Abs("../../examples/customer-operation-export/public/session.mjs")
	if err != nil {
		t.Fatal(err)
	}
	configuration, _ := json.Marshal(map[string]any{"sdk": "file://" + sdk, "api": owner.URL, "identity": identity.URL, "tokens": customerTokens, "artifact": artifact, "session": "file://" + sessionModule, "app": app.ID, "scope": def.Scope})
	code := `import http from 'node:http';
const config=JSON.parse(process.env.GREGALE_OPERATIONS_TEST_CONFIG);
const {GregaleOperations,GregaleOperationClient}=await import(config.sdk);
const {ExportSession}=await import(config.session);
const runtime=new GregaleOperations({apiURL:config.api,identityEndpoint:config.identity});
const customers=config.tokens.map(token=>new GregaleOperationClient({apiURL:config.api,credential:()=>token}));
let executions=0;
const server=http.createServer(async(req,res)=>{
 try {
  const url=new URL(req.url,'http://localhost');
  if(url.pathname==='/inspect'){
   const client=customers[Number(url.searchParams.get('customer')??0)], id=url.searchParams.get('id');
   // A brand-new client has no stored operation IDs. Discover from server history.
   const session=new ExportSession({client,appID:config.app,scope:config.scope,name:'export',definitionID:'unused'});
   const history=await session.history();
   const selected=history.find(row=>row.id===id);
   if(selected)await session.open(selected.id);
   const status=await client.get(id), events=await client.events(id,2);
   const downloaded=status.artifacts?.length?await (await session.download()).blob.text():'';
   const frames=[];
   for await(const frame of client.subscribe(id,{after:2,signal:AbortSignal.timeout(5000)})){
    if(frame.event)frames.push(frame.event.sequence);
    if(frame.event?.sequence>=status.latest_sequence)break;
   }
   session.close();res.end(JSON.stringify({status,events,frames,executions,downloaded,history}));return;
  }
  await runtime.runRequest(req.headers,async()=>{
   const context=runtime.context();if(!context)throw Error('missing context');executions++;
   await runtime.progress({report_id:'generated',stage:'generating',completed:1,total:1});
   // Deliberately outlive the initial one-second scheduler claim. The next
   // authenticated report and final result require production lease renewal.
   await new Promise(resolve=>setTimeout(resolve,1100));
   await runtime.progress({report_id:'uploaded',stage:'uploading',completed:1,total:1});
   const attachment=await runtime.artifact(config.artifact);
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({file:attachment.artifacts[0].id,operation_id:context.id}));
  });
 }catch(err){res.statusCode=err.status??500;res.end(JSON.stringify({error:err.code??err.message}));}
});server.listen(0,'127.0.0.1',()=>console.log('127.0.0.1:'+server.address().port));`
	childCtx, cancel := context.WithTimeout(ctx, time.Minute)
	cmd := exec.CommandContext(childCtx, node, "--input-type=module", "-e", code)
	cmd.Env = append(os.Environ(), "GREGALE_OPERATIONS_TEST_CONFIG="+string(configuration))
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
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	var address string
	select {
	case address = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("Node Operations startup timed out")
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatal("Node Operations startup failed")
	}
	backend := &operationsAcceptanceBackend{store: store, app: app, plan: acct.Plan}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), logger).WithConsumerAuth(operationsAcceptanceConsumerStore{store}).WithOperationRoutes(gateway.DurableOperationRoutes{Store: store, Admission: srv.operationsPreview})
	request := func(payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://"+app.Slug+".gregale.dev/exports", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+ingressKeys[0])
		r.Header.Set("Idempotency-Key", "customer-export")
		r.Header.Set(api.PlatformTenantIDHeader, tenants[1].ID)
		r.Header.Set(api.OperationCapabilityHeader, "forged")
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, r)
		return w
	}
	w = request(`{"count":1}`)
	if w.Code != 202 {
		t.Fatalf("ingress: %d %s", w.Code, w.Body.String())
	}
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	duplicate := request(`{"count":1.0}`)
	if duplicate.Code != 202 || duplicate.Body.String() != w.Body.String() {
		t.Fatal("ingress did not reuse logical operation")
	}
	if conflict := request(`{"count":2}`); conflict.Code != 409 {
		t.Fatalf("payload conflict: %d", conflict.Code)
	}
	op, err := store.OperationByID(ctx, acct.ID, tenants[0].ID, receipt.ID)
	if err != nil || op.State != api.OperationAccepted {
		t.Fatalf("ownership/admission: %+v %v", op, err)
	}
	// Rollback takes effect before dispatch; already admitted work still has
	// usable workload reporting authority, retained results and delivery.
	if err := os.WriteFile(policyPath, []byte(`{"version":1,"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if denied := request(`{"count":1}`); denied.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed ingress fell through to handler: %d", denied.Code)
	}
	if denied := call(http.MethodPost, "/v1/platform-tenant-self/customer-operations", customerTokens[0], api.OperationStartRequest{DefinitionID: def.ID, Input: []byte(`{"count":1}`)}); denied.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed API admission: %d", denied.Code)
	}
	engine, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), nil, noopNotifier{}, "acceptance", logger)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &operationsAcceptanceDispatcher{store: store, address: address}
	synthServer := httptest.NewServer(gateway.NewSynthServer("", dispatcher, logger).Mux())
	t.Cleanup(synthServer.Close)
	synth, err := sched.DialGatewaySynthTarget("tcp://"+strings.TrimPrefix(synthServer.URL, "http://"), nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	drain := sched.NewDrain(store, engine, sched.WithDrainGatewaySynth(synth), sched.WithDrainLogger(logger), sched.WithDrainWakeLease(1))
	drain.Tick(ctx)
	op, err = store.OperationByID(ctx, acct.ID, tenants[0].ID, receipt.ID)
	if err != nil || op.State != api.OperationSucceeded || op.Progress == nil || op.Progress.Stage != "uploading" || op.CompletionDelivery.State != "pending" {
		t.Fatalf("handler completion: %+v %v", op, err)
	}
	inspect := func(customer int) (int, []byte) {
		t.Helper()
		r, err := http.Get("http://" + address + "/inspect?id=" + receipt.ID + "&customer=" + strconv.Itoa(customer))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, body
	}
	status, body := inspect(0)
	var view struct {
		Status     api.OperationResponse  `json:"status"`
		Frames     []int64                `json:"frames"`
		Executions int                    `json:"executions"`
		Downloaded string                 `json:"downloaded"`
		History    []api.OperationSummary `json:"history"`
	}
	if err := json.Unmarshal(body, &view); err != nil || status != 200 || view.Executions != 1 || len(view.Frames) != 4 || view.Frames[0] != 3 || view.Frames[3] != 6 || view.Downloaded != "id,count\nalice,1\n" || len(view.History) != 1 || view.History[0].ID != receipt.ID {
		t.Fatalf("browser status/stream: %d %s %v", status, body, err)
	}
	if status, _ := inspect(1); status != 404 {
		t.Fatalf("cross-customer SDK read: %d", status)
	}
	deliveries, err := store.ClaimDueAppWebhookDeliveries(ctx, 10, time.Now())
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("atomic outbox: %d %v", len(deliveries), err)
	}
	delivery := deliveries[0]
	if err := store.MarkAppWebhookDeliveryFailed(ctx, delivery.ID, 503, delivery.Attempt, delivery.NextAttemptAt, "receiver unavailable", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	drain.Tick(ctx)
	status, body = inspect(0)
	if err := json.Unmarshal(body, &view); err != nil || status != 200 || view.Status.State != api.OperationSucceeded || view.Status.CompletionDelivery.LastError == "" || view.Executions != 1 || len(view.History) != 1 || view.History[0].State != api.OperationSucceeded || view.History[0].CompletionDelivery.State != view.Status.CompletionDelivery.State {
		t.Fatalf("delivery retry regenerated work: %d %s %v", status, body, err)
	}
	provider.replace("changed export")
	provider.mu.Lock()
	provider.missing = true
	provider.mu.Unlock()
	if status, _ := inspect(0); status != http.StatusOK {
		t.Fatalf("retained artifact SDK download after source deletion: %d", status)
	}
	if retained, err := store.OperationByID(ctx, acct.ID, tenants[0].ID, receipt.ID); err != nil || retained.State != api.OperationSucceeded {
		t.Fatalf("artifact failure replaced business success: %+v %v", retained, err)
	}
	writeOperationPreviewPolicy(t, policyPath, acct.ID, app.ID, def.Scope, tenants[0].ID)
	inspectOperationWithGoSDK(t, goInspector, owner.URL, customerTokens[0], customerTokens[1], def.ID, receipt.ID)
	if err := os.WriteFile(policyPath, []byte(`{"version":1,"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	status, body = inspect(0)
	if err := json.Unmarshal(body, &view); err != nil || status != http.StatusOK || view.Executions != 1 || view.Status.State != api.OperationSucceeded {
		t.Fatal("Go SDK replay changed business execution", err)
	}
}

// The local transport substitutes for vmmd's HTTP bridge. Admission and the
// synthetic envelope are production code, and durable request reload must
// preserve only the current attempt's ephemeral operation proof.
type operationsAcceptanceDispatcher struct {
	store   *state.PgStore
	address string
}

func (*operationsAcceptanceDispatcher) Wake(context.Context, string) error {
	return errors.New("unexpected wake")
}
func (d *operationsAcceptanceDispatcher) Invoke(ctx context.Context, app string, inv state.Invocation) (state.Invocation, error) {
	out, _, err := d.InvokeWithTargetStatus(ctx, app, inv, gateway.Target{})
	return out, err
}
func (d *operationsAcceptanceDispatcher) InvokeWithTargetStatus(ctx context.Context, app string, inv state.Invocation, target gateway.Target) (state.Invocation, int, error) {
	inv, err := state.AdmitPlatformTenantInvocation(ctx, d.store, app, inv)
	if err != nil {
		return inv, 0, err
	}
	if target.InstanceID != inv.InstanceID {
		return inv, 0, fmt.Errorf("dispatch target changed")
	}
	r, err := http.NewRequestWithContext(ctx, inv.Method, "http://"+d.address+inv.Path, bytes.NewReader(inv.Payload))
	if err != nil {
		return inv, 0, err
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		return inv, 0, err
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	// Match the trusted guest boundary: durable ownership supplies identity,
	// and only the validated current execution proof survives header clearing.
	proof := r.Header.Clone()
	api.PlatformIdentity{AppID: inv.AppID, TenantID: inv.AccountID, PlatformTenantID: inv.PlatformTenantID, InstanceID: inv.InstanceID}.ApplyGuestHeaders(r.Header)
	for _, name := range []string{api.OperationIDHeader, api.OperationAttemptHeader, api.OperationCapabilityHeader, api.OperationTransactionVersionHeader, api.OperationResultMaxBytesHeader, api.OperationMilestoneVersionHeader} {
		if value := proof.Get(name); value != "" {
			r.Header.Set(name, value)
		}
	}
	r.Header.Set(api.InvocationIDHeader, inv.ID)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return inv, 0, err
	}
	defer res.Body.Close()
	inv.Result, err = io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return inv, res.StatusCode, err
}

type operationsAcceptanceBackend struct {
	store *state.PgStore
	app   state.App
	plan  api.Plan
}

func (b *operationsAcceptanceBackend) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{ID: b.app.ID, AccountID: b.app.AccountID, Slug: b.app.Slug, Plan: b.plan, PublicAuth: gateway.PublicAuthConfig{Mode: "open"}, ConsumerAuthMode: string(api.ConsumerAuthModeRequired), PlatformTenantRequired: true, MaxConcurrency: 1, RequestInvocationsEnabled: true}, true
}
func (*operationsAcceptanceBackend) Pick(string) gateway.PickResult {
	panic("operation admission reached instance picker")
}
func (*operationsAcceptanceBackend) HealthyCount(string) int { return 1 }
func (*operationsAcceptanceBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	panic("operation admission woke VM")
}
func (*operationsAcceptanceBackend) LookupMirrorRules(context.Context, string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}
func (*operationsAcceptanceBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "", "", errors.New("unexpected mirror")
}

type operationsAcceptanceConsumerStore struct{ *state.PgStore }

func (s operationsAcceptanceConsumerStore) ConsumerKeyByAppAndPrefix(ctx context.Context, account, app, prefix string) (gateway.ConsumerAuthKey, error) {
	k, err := s.PgStore.ConsumerKeyByAppAndPrefix(ctx, account, app, prefix)
	if errors.Is(err, state.ErrNotFound) {
		err = gateway.ErrConsumerAuthNotFound
	}
	return gateway.ConsumerAuthKey{ID: k.ID, AccountID: k.AccountID, AppID: k.AppID, ConsumerID: k.ConsumerID, Prefix: k.Prefix, Hash: k.Hash, Scopes: k.Scopes, ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt}, err
}
func (s operationsAcceptanceConsumerStore) GetAPIConsumerByID(ctx context.Context, account, id string) (gateway.ConsumerAuthConsumer, error) {
	c, err := s.PgStore.GetAPIConsumerByID(ctx, account, id)
	if errors.Is(err, state.ErrNotFound) {
		err = gateway.ErrConsumerAuthNotFound
	}
	return gateway.ConsumerAuthConsumer{ID: c.ID, AccountID: c.AccountID, AppID: c.AppID, PlatformTenantID: c.PlatformTenantID, Status: string(c.Status), RevokedAt: c.RevokedAt}, err
}
