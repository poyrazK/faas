//go:build !no_pg

// adr: 20261018183000
package apidserver_test

// A workflow step fans out: it starts children with its workflow step proof,
// the run parks on operation.children.settled, and the platform resumes it
// only once every child has settled. Customers cannot send that event.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestOperationWorkflowChildrenFanIn(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) { testWorkflowChildrenFanIn(t, kind) })
	}
}

func testWorkflowChildrenFanIn(t *testing.T, kind string) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if kind == "postgres" {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatal(err)
		}
		store = state.NewPgStore(pool)
	}
	acct, err := store.CreateAccount(ctx, "workflow-children@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", acct.ID)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "workflow-children", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]api.WorkflowSpec{{Name: "fan-chain", Steps: []api.WorkflowStepSpec{
		{Name: "fan", Path: "/fan"},
		{Name: "gather", WaitForEvent: api.OperationChildrenSettledEvent, Timeout: time.Hour, DependsOn: []string{"fan"}},
		{Name: "finish", Path: "/finish", DependsOn: []string{"gather"}},
	}}})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenants := store.(state.PlatformTenantStore)
	tenant, _, err := tenants.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(acct.Plan))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	ops := store.(state.OperationStore)
	define := func(spec api.OperationDefinitionSpec) state.OperationDefinition {
		spec.Owner, spec.Method = api.OperationOwnerPlatformTenant, "POST"
		spec.InputSchema, spec.OutputSchema = []byte(`{"type":"object"}`), []byte(`{"type":"object"}`)
		def, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	parentDef := define(api.OperationDefinitionSpec{Name: "fan", Workflow: "fan-chain", Path: "/fans", ProgressStages: []string{"fan", "gather", "finish"}})
	define(api.OperationDefinitionSpec{Name: "item", Path: "/items", ProgressStages: []string{"working"}})
	parent, _, err := ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: parentDef.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "fan-1", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.NewString()
	if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
		nodeID = node.ID
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled = true
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "workflow-children", workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if (*srv.Operation()).WorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	assertion, err := signer.Mint(time.Now(), acct.ID, app.ID, instance.ID, workloadidentity.OperationsAudience)
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	startChild := func(h map[string]string, key string) int {
		body, _ := json.Marshal(api.OperationChildRequest{Operation: "item", Key: key, Input: json.RawMessage(`{}`)})
		req := httptest.NewRequest(http.MethodPost, "/v1/runtime/workflow-operations/"+parent.ID+"/children", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+assertion.AccessToken)
		req.Header.Set(api.OperationExecutionKindHeader, "workflow")
		for _, name := range []string{api.OperationWorkflowRunHeader, api.OperationWorkflowStepHeader, api.OperationWorkflowCapabilityHeader, api.OperationGenerationHeader, api.OperationAttemptHeader} {
			req.Header.Set(name, h[name])
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code >= 400 {
			t.Logf("child %s: HTTP %d %s", key, rec.Code, rec.Body.String())
		}
		return rec.Code
	}
	fanned := false
	executor := workflowArtifactHTTPExecutor{call: func(h map[string]string) (int, []byte, error) {
		if h[api.OperationWorkflowStepHeader] != "fan" {
			return http.StatusOK, []byte(`{}`), nil
		}
		for _, key := range []string{"a", "b", "a"} {
			if code := startChild(h, key); code != http.StatusCreated && code != http.StatusOK {
				return 0, nil, errors.New("fan step could not start child " + key + ": " + strconv.Itoa(code))
			}
		}
		fanned = true
		return http.StatusOK, []byte(`{}`), nil
	}}
	orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	waiting := func() bool {
		op, err := ops.OperationByID(ctx, acct.ID, "", parent.ID)
		return err == nil && op.State == api.OperationWaiting && op.Waiting != nil && op.Waiting.Event == api.OperationChildrenSettledEvent
	}
	childrenEventually(t, 30*time.Second, "parent parked on children", func() bool {
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		return fanned && waiting()
	})
	children, err := store.(state.OperationChildStore).OperationChildren(ctx, acct.ID, "", parent.ID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children = %+v %v", children, err)
	}

	// A customer cannot fake the fan-in.
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "workflow-children", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	token, err := api.NewClient(host.URL, key).CreatePlatformTenantAccessToken(ctx, tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.NewClient(host.URL, token.Token).SignalPlatformTenantSelfOperation(ctx, parent.ID, api.OperationSignalRequest{SignalID: "fake", Event: api.OperationChildrenSettledEvent})
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("customer sent the reserved event: %v", err)
	}

	signals, cancel := context.WithCancel(ctx)
	defer cancel()
	go srv.Operation().RunChildSettlementSignals(signals)
	tick := func() {
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Second)
	tick()
	if !waiting() {
		t.Fatal("parent resumed while its children were still pending")
	}
	if _, err := ops.CancelOperation(ctx, acct.ID, "", children[0].Operation.ID, children[0].Operation.Generation); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	tick()
	if !waiting() {
		t.Fatal("parent resumed with one child pending")
	}
	if _, err := ops.CancelOperation(ctx, acct.ID, "", children[1].Operation.ID, children[1].Operation.Generation); err != nil {
		t.Fatal(err)
	}
	childrenEventually(t, 15*time.Second, "parent succeeded after its children settled", func() bool {
		tick()
		op, err := ops.OperationByID(ctx, acct.ID, "", parent.ID)
		return err == nil && op.State == api.OperationSucceeded
	})
	page, err := ops.OperationEvents(ctx, acct.ID, "", parent.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var signal *api.OperationEvent
	for i := range page.Events {
		if page.Events[i].Type == "signal_received" {
			signal = &page.Events[i]
		}
	}
	if signal == nil {
		t.Fatalf("parent events lack the children-settled signal: %+v", page.Events)
	}
}

func childrenEventually(t *testing.T, budget time.Duration, label string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
