//go:build !no_pg

// adr: 20261018183000
package apidserver_test

// Child Operations: a running HTTP Operation starts children for its
// customer with its own runtime proof; children replay by key, are bounded by
// plan, cannot nest, and are cancelled with their parent.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type childFixture struct {
	t         *testing.T
	store     state.Store
	handler   http.Handler
	account   state.Account
	app       state.App
	instance  state.Instance
	signer    *workloadidentity.Signer
	parent    state.Operation
	proof     childProof
	ownerKey  string
	tenantTok string
}

type childProof struct {
	assertion, invocation, capability string
	attempt                           int
}

func newChildFixture(t *testing.T, kind string, plan api.Plan) *childFixture {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", "")
	ctx := t.Context()
	var store state.Store = state.NewMemStore()
	if kind == "postgres" {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatal(err)
		}
		store = state.NewPgStore(pool)
	}
	f := &childFixture{t: t, store: store}
	var err error
	if f.account, err = store.CreateAccount(ctx, "operation-children@example.test", plan); err != nil {
		t.Fatal(err)
	}
	if f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "imports", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, f.account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	ops := store.(state.OperationStore)
	for _, name := range []string{"import", "import-file"} {
		if _, err := ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: f.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: f.app.ID, Scope: dep.Scope, DeploymentID: dep.ID,
			Spec: api.OperationDefinitionSpec{Name: name, Method: "POST", Path: "/" + name, Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"working"}, Recovery: api.OperationRecoveryReconcile,
				InputSchema: []byte(`{"type":"object","properties":{"file":{"type":"string"}},"additionalProperties":false}`), OutputSchema: []byte(`{"type":"object"}`)}}}); err != nil {
			t.Fatal(err)
		}
	}
	def, err := ops.OperationDefinitionForDeployment(ctx, f.account.ID, f.app.ID, dep.ID, "import")
	if err != nil {
		t.Fatal(err)
	}
	if f.parent, _, err = ops.AdmitOperation(ctx, state.OperationAdmission{AccountID: f.account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "import-1", Input: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if f.instance, err = store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateRunning), 128, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if f.signer, err = workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "operation-children", workloadidentity.DefaultTokenTTL); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled = true
	if (*srv.Operation()).WorkloadVerifier, err = workloadidentity.NewVerifier(f.signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	f.handler = srv.Handler()
	f.proof = f.claim(f.parent)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, f.account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	f.ownerKey = key
	var token api.CreatePlatformTenantAccessTokenResponse
	f.decode(f.do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", f.ownerKey, nil, api.CreatePlatformTenantAccessTokenRequest{Name: "browser",
		Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}), http.StatusCreated, &token)
	f.tenantTok = token.Token
	return f
}

// claim dispatches op's current invocation to the fixture instance and
// returns the runtime proof its attempt holds.
func (f *childFixture) claim(op state.Operation) childProof {
	f.t.Helper()
	inv, err := f.store.ClaimInvocationWithCap(f.t.Context(), op.CurrentInvocationID, f.instance.ID, 60, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(inv.Headers, &metadata); err != nil {
		f.t.Fatal(err)
	}
	assertion, err := f.signer.Mint(time.Now(), f.account.ID, f.app.ID, f.instance.ID, workloadidentity.OperationsAudience)
	if err != nil {
		f.t.Fatal(err)
	}
	return childProof{assertion: assertion.AccessToken, invocation: inv.ID, capability: metadata[api.OperationCapabilityHeader], attempt: inv.Attempts}
}

func (f *childFixture) do(method, path, bearer string, proof *childProof, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if proof != nil {
		bearer = proof.assertion
		req.Header.Set(api.InvocationIDHeader, proof.invocation)
		req.Header.Set(api.OperationAttemptHeader, strconv.Itoa(proof.attempt))
		req.Header.Set(api.OperationCapabilityHeader, proof.capability)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *childFixture) decode(rec *httptest.ResponseRecorder, status int, out any) {
	f.t.Helper()
	if rec.Code != status {
		f.t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *childFixture) start(proof childProof, parentID, key, input string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do("POST", "/v1/runtime/operations/"+parentID+"/children", "", &proof, api.OperationChildRequest{Operation: "import-file", Key: key, Input: json.RawMessage(input)})
}

func forEachChildStore(t *testing.T, plan api.Plan, test func(t *testing.T, f *childFixture)) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) { test(t, newChildFixture(t, kind, plan)) })
	}
}

func TestOperationChildRoutesAreHiddenUntilAllowlisted(t *testing.T) {
	forEachChildStore(t, api.PlanPro, func(t *testing.T, f *childFixture) {
		for _, rec := range []*httptest.ResponseRecorder{
			f.start(f.proof, f.parent.ID, "a.csv", `{"file":"a.csv"}`),
			f.do("GET", "/v1/runtime/operations/"+f.parent.ID+"/children", "", &f.proof, nil),
			f.do("GET", "/v1/platform-tenant-self/customer-operations/"+f.parent.ID+"/children", f.tenantTok, nil, nil),
			f.do("GET", "/v1/apps/imports/operations/"+f.parent.ID+"/children", f.ownerKey, nil, nil),
		} {
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no such API route") {
				t.Fatalf("before the allowlist: HTTP %d %s", rec.Code, rec.Body.String())
			}
		}
		t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", f.account.ID)
		f.decode(f.start(f.proof, f.parent.ID, "a.csv", `{"file":"a.csv"}`), http.StatusCreated, nil)
	})
}

func TestOperationChildrenStartReplayListAndCancel(t *testing.T) {
	forEachChildStore(t, api.PlanPro, func(t *testing.T, f *childFixture) {
		t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", f.account.ID)
		var first, replay api.OperationChildStartResponse
		f.decode(f.start(f.proof, f.parent.ID, "a.csv", `{"file":"a.csv"}`), http.StatusCreated, &first)
		f.decode(f.start(f.proof, f.parent.ID, "a.csv", `{"file":"a.csv"}`), http.StatusOK, &replay)
		if first.Replayed || !replay.Replayed || replay.Operation.ID != first.Operation.ID || first.Key != "a.csv" {
			t.Fatalf("start=%+v replay=%+v", first, replay)
		}
		f.decode(f.start(f.proof, f.parent.ID, "a.csv", `{"file":"other.csv"}`), http.StatusConflict, nil)
		f.decode(f.start(f.proof, f.parent.ID, "b.csv", `{"file":7}`), http.StatusBadRequest, nil)
		f.decode(f.do("POST", "/v1/runtime/operations/"+f.parent.ID+"/children", "", &f.proof, api.OperationChildRequest{Operation: "missing", Key: "c.csv"}), http.StatusNotFound, nil)
		var second api.OperationChildStartResponse
		f.decode(f.start(f.proof, f.parent.ID, "b.csv", `{"file":"b.csv"}`), http.StatusCreated, &second)

		stale := f.proof
		stale.capability = "stale"
		if rec := f.start(stale, f.parent.ID, "c.csv", `{}`); rec.Code < 400 {
			t.Fatalf("stale capability started a child: %d %s", rec.Code, rec.Body.String())
		}

		// A child runs with its own proof but cannot start children.
		child, err := f.store.(state.OperationStore).OperationByID(t.Context(), f.account.ID, "", first.Operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.decode(f.start(f.claim(child), child.ID, "nested.csv", `{}`), http.StatusConflict, nil)

		var runtime, customer, owner api.OperationChildrenResponse
		f.decode(f.do("GET", "/v1/runtime/operations/"+f.parent.ID+"/children", "", &f.proof, nil), http.StatusOK, &runtime)
		f.decode(f.do("GET", "/v1/platform-tenant-self/customer-operations/"+f.parent.ID+"/children", f.tenantTok, nil, nil), http.StatusOK, &customer)
		f.decode(f.do("GET", "/v1/apps/imports/operations/"+f.parent.ID+"/children", f.ownerKey, nil, nil), http.StatusOK, &owner)
		for _, list := range []api.OperationChildrenResponse{runtime, customer, owner} {
			if len(list.Children) != 2 || list.Children[0].Key != "a.csv" || list.Children[1].Key != "b.csv" {
				t.Fatalf("children = %+v", list)
			}
		}

		// Cancelling the parent cancels or requests cancellation of every active child.
		f.decode(f.do("POST", "/v1/apps/imports/operations/"+f.parent.ID+"/cancel", f.ownerKey, nil, api.OperationCancellationRequest{ExpectedGeneration: f.parent.Generation}), http.StatusOK, nil)
		f.decode(f.do("GET", "/v1/apps/imports/operations/"+f.parent.ID+"/children", f.ownerKey, nil, nil), http.StatusOK, &owner)
		for _, c := range owner.Children {
			if c.Operation.State != api.OperationCancelled && !c.Operation.CancellationRequested {
				t.Fatalf("child %s outlived its parent's cancel: %+v", c.Key, c.Operation)
			}
		}
		f.decode(f.start(f.proof, f.parent.ID, "late.csv", `{}`), http.StatusConflict, nil)
	})
}

func TestOperationChildrenPlanLimit(t *testing.T) {
	forEachChildStore(t, api.PlanHobby, func(t *testing.T, f *childFixture) {
		t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", f.account.ID)
		limit := api.MustLimitsFor(api.PlanHobby).Operations.ChildrenPerOperation
		for i := 0; i < limit; i++ {
			f.decode(f.start(f.proof, f.parent.ID, "f"+strconv.Itoa(i), `{}`), http.StatusCreated, nil)
		}
		rec := f.start(f.proof, f.parent.ID, "one-more", `{}`)
		if rec.Code < 400 || !strings.Contains(rec.Body.String(), "children_per_operation") {
			t.Fatalf("child over the plan: %d %s", rec.Code, rec.Body.String())
		}
		f.decode(f.start(f.proof, f.parent.ID, "f0", `{}`), http.StatusOK, nil)
	})
}
