// Negative-path tests for cmd/apid handlers. The happy paths are covered by
// server_test.go; this file targets the error branches that the matrix
// doesn't hit.

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateApp_DuplicateSlug409(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dupe"}, nil); rec.Code != 201 {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dupe"}, nil)
	if rec.Code != 409 {
		t.Errorf("duplicate slug: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 409, api.CodeValidation)
}

func TestCreateApp_BadJSONBody(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := newRawRequest(t, "POST", "/v1/apps", "not json {{{", map[string]string{
		"Authorization": "Bearer " + e.key,
	})
	rec := serveRaw(e.h, req)
	if rec.Code != 400 {
		t.Errorf("bad json: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_InvalidType(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "weird", Type: "widget"}, nil)
	if rec.Code != 400 {
		t.Errorf("invalid type: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_FunctionMissingRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn-no-rt", Type: "function"}, nil)
	if rec.Code != 400 {
		t.Errorf("function without runtime: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_FunctionBadRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-bad-rt", Type: "function", Runtime: "ruby33"}, nil)
	if rec.Code != 400 {
		t.Errorf("function bad runtime: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateApp_FunctionGo124Runtime is the positive pin for the
// go124 runtime: a function app with Runtime: "go124" must be accepted
// by the apid server-side whitelist. The wire contract is
// cmd/apid/handlers.go (the buildApp allow-list) plus the DB CHECK
// widened in migrations/00035_app_runtime_go124.sql. If this test
// breaks, one of the two guards is out of sync.
func TestCreateApp_FunctionGo124Runtime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-go124-rt", Type: "function", Runtime: "go124"}, nil)
	if rec.Code != 201 {
		t.Fatalf("function with runtime=go124: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Runtime != "go124" {
		t.Errorf("runtime round-trip = %q, want go124", out.Runtime)
	}
}

// TestCreateApp_FunctionGo124BadRuntime is the negative pin: a
// runtime that LOOKS like go124 but is misspelled must still be
// rejected with 400. The allow-list is exhaustive, not "go124*".
func TestCreateApp_FunctionGo124BadRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-go124-bad", Type: "function", Runtime: "go124beta"}, nil)
	if rec.Code != 400 {
		t.Errorf("function with runtime=go124beta: %d %s, want 400", rec.Code, rec.Body)
	}
}

// TestCreateApp_FunctionNode24Runtime is the positive pin for the
// node24 runtime (Node 24 LTS, added in migrations/00075 + PR 1 Layer 4).
// Mirrors TestCreateApp_FunctionGo124Runtime above: the apid-side
// allow-list in cmd/apid/handlers.go and the DB CHECK widening must
// stay in lockstep; if either regresses this test fires.
func TestCreateApp_FunctionNode24Runtime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-node24-rt", Type: "function", Runtime: "node24"}, nil)
	if rec.Code != 201 {
		t.Fatalf("function with runtime=node24: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Runtime != "node24" {
		t.Errorf("runtime round-trip = %q, want node24", out.Runtime)
	}
}

// TestCreateApp_FunctionPython313Runtime is the positive pin for the
// python313 runtime (Python 3.13 default for RHEL/Fedora, added in
// migrations/00075). Python handlers stay version-neutral on the
// wire — `/app/handler.py` — but the runtime id distinguishes the
// function base.
func TestCreateApp_FunctionPython313Runtime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-py313-rt", Type: "function", Runtime: "python313"}, nil)
	if rec.Code != 201 {
		t.Fatalf("function with runtime=python313: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Runtime != "python313" {
		t.Errorf("runtime round-trip = %q, want python313", out.Runtime)
	}
}

// TestCreateApp_FunctionNode24BadRuntime is the negative pin for the
// widening: a runtime that LOOKS like node24 but is misspelled must
// still be rejected with 400. The allow-list is exhaustive over the
// six runtime ids, not "node*".
func TestCreateApp_FunctionNode24BadRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-node24-bad", Type: "function", Runtime: "node24beta"}, nil)
	if rec.Code != 400 {
		t.Errorf("function with runtime=node24beta: %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestCreateApp_AppliesDefaults(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "defaults-app"}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RAMMB != 512 {
		t.Errorf("RAM default = %d, want 512 (pro)", out.RAMMB)
	}
	if out.MaxConcurrency != 1 {
		t.Errorf("MaxConcurrency default = %d, want 1", out.MaxConcurrency)
	}
	if out.CPUMillicores != api.DefaultAppCPUMillicores || out.ConfiguredResources.CPUMillicores != api.DefaultAppCPUMillicores {
		t.Errorf("CPU default/configured resources = %+v, want %dm", out, api.DefaultAppCPUMillicores)
	}
}

func TestCreateApp_ExplicitRamAndConcur(t *testing.T) {
	e := setup(t, api.PlanScale) // highest limits
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "explicit", RAMMB: 256, MaxConcurrency: 4}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RAMMB != 256 || out.MaxConcurrency != 4 {
		t.Errorf("explicit values lost: %+v", out)
	}
}

func TestCreateApp_ExplicitCPU(t *testing.T) {
	e := setup(t, api.PlanScale)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "cpu-shape", CPUMillicores: 500}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.CPUMillicores != 500 || out.EffectiveLimits.CPULimitMillicores != 500 {
		t.Fatalf("CPU shape not reflected: %+v", out)
	}

	bad := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-cpu", CPUMillicores: 750}, nil)
	assertProblem(t, bad, http.StatusUnprocessableEntity, api.CodeInvalidAppCPU)
}

func TestCreateDeployment_AppNotOwned(t *testing.T) {
	// Owner-A creates an app; owner-B tries to deploy to it.
	store := state.NewMemStore()
	acctA, _ := store.CreateAccount(context.Background(), "a@x.com", api.PlanPro)
	_, hashA, _ := api.GenerateAPIKey()
	store.CreateAPIKey(context.Background(), acctA.ID, hashA, "a", api.ScopesAdminOnly)

	acctB, _ := store.CreateAccount(context.Background(), "b@x.com", api.PlanPro)
	keyB, hashB, _ := api.GenerateAPIKey()
	store.CreateAPIKey(context.Background(), acctB.ID, hashB, "b", api.ScopesAdminOnly)

	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acctA.ID, Slug: "a-app", Status: state.AppActive,
	})

	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).handler()
	digest := "sha256:" + repeat("a", 64)

	// B tries to deploy to A's slug.
	body := `{"image":"r/x@` + digest + `"}`
	req := newRawRequest(t, "POST", "/v1/apps/"+app.Slug+"/deployments",
		body,
		map[string]string{"Authorization": "Bearer " + keyB})
	rec := serveRaw(srv, req)
	if rec.Code != 404 {
		t.Errorf("cross-owner deploy should 404, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_BadJSONBody(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	req := newRawRequest(t, "POST", "/v1/apps/dep-app/deployments", "{not json",
		map[string]string{"Authorization": "Bearer " + e.key})
	rec := serveRaw(e.h, req)
	if rec.Code != 400 {
		t.Errorf("bad json deploy: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_ImageNoShaPrefix(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "registry.x/no-sha-prefix"}, nil)
	if rec.Code != 400 {
		t.Errorf("image without @sha256: should 400, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_ImageBadDigest(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	// Has @sha256: but the digest part is the wrong length / format.
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "r/x@sha256:short"}, nil)
	if rec.Code != 400 {
		t.Errorf("image with short digest should 400, got %d %s", rec.Code, rec.Body)
	}
}

func workflowDeploymentRequest() api.CreateDeploymentRequest {
	return api.CreateDeploymentRequest{
		Image: "registry.example.com/workflow@sha256:" + repeat("a", 64),
		Workflows: []api.WorkflowSpec{{
			Name:    "process_order",
			Trigger: &api.WorkflowTriggerSpec{Type: "manual"},
			Steps: []api.WorkflowStepSpec{{
				Name:    "charge",
				Run:     "charge_stripe",
				Input:   json.RawMessage(`{"order_id":"o-1"}`),
				Timeout: time.Second,
			}},
		}},
	}
}

func TestCreateDeployment_WorkflowPlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "workflow-app"}, nil)

	rec := e.do(t, "POST", "/v1/apps/workflow-app/deployments", workflowDeploymentRequest(), nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("workflow deployment on Free: status %d, want 402: %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanWorkflowsNotAllowed)
}

func TestCreateDeployment_WorkflowDefinitionsPersist(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "workflow-app"}, nil)

	rec := e.do(t, "POST", "/v1/apps/workflow-app/deployments", workflowDeploymentRequest(), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("workflow deployment on Hobby: status %d, want 202: %s", rec.Code, rec.Body)
	}

	app, err := e.store.AppBySlug(context.Background(), "workflow-app")
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	deployments, err := e.store.ListDeploymentsForApp(context.Background(), app.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(deployments) != 1 {
		t.Fatalf("deployment rows = %d, want 1", len(deployments))
	}
	if !strings.Contains(string(deployments[0].Workflows), "process_order") {
		t.Fatalf("persisted workflows = %s", deployments[0].Workflows)
	}
}

// TestCreateDeployment_Overrides_HappyPath pins the wire round-trip
// for the Fargate-shaped deploy override object (issue #460 /
// ADR-053). The handler must:
//   - accept a CreateDeploymentRequest with the optional `overrides`
//     field and decode it into the typed shape;
//   - validate the override against the plan's EnvVarsMax +
//     EnvValueMaxBytes caps;
//   - persist the six override_* columns on the deployments row;
//   - echo the override shape on the DeploymentResponse, NEVER
//     including the plaintext env values (only the key set on
//     override_env_keys).
func TestCreateDeployment_Overrides_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	digest := "sha256:" + repeat("a", 64)
	overrides := &api.CreateDeploymentOverrides{
		Entrypoint: []string{"/usr/bin/node", "/srv/app.js"},
		Cmd:        []string{"--port", "9090"},
		Env: map[string]string{
			"LOG_LEVEL":   "debug",
			"PORT_SECRET": "should-not-be-echoed",
		},
		EnvSecrets: map[string]string{
			"DB_URL": "secret:DB_URL",
		},
		Port: 9090,
		Healthcheck: &api.DeploymentHealthcheck{
			Path:      "/healthz",
			IntervalS: 5,
			TimeoutS:  2,
			Retries:   3,
		},
	}
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "r/x@" + digest, Overrides: overrides}, nil)
	if rec.Code != 202 {
		t.Fatalf("POST deploy: %d %s", rec.Code, rec.Body)
	}
	var resp api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body)
	}
	if !resp.HasOverrides {
		t.Errorf("response HasOverrides = false, want true")
	}
	if got := resp.OverridePort; got != 9090 {
		t.Errorf("OverridePort = %d, want 9090", got)
	}
	wantEntrypoint := []string{"/usr/bin/node", "/srv/app.js"}
	if !reflect.DeepEqual(resp.OverrideEntrypoint, wantEntrypoint) {
		t.Errorf("OverrideEntrypoint = %v, want %v", resp.OverrideEntrypoint, wantEntrypoint)
	}
	if got := resp.OverrideCmd; !reflect.DeepEqual(got, []string{"--port", "9090"}) {
		t.Errorf("OverrideCmd = %v, want [--port 9090]", got)
	}
	// Env keys echoed, values NEVER. Sorted alphabetically (handler
	// contract — see cmd/apid/handlers_ext.go::deploymentResponse).
	wantEnvKeys := []string{"LOG_LEVEL", "PORT_SECRET"}
	if !reflect.DeepEqual(resp.OverrideEnvKeys, wantEnvKeys) {
		t.Errorf("OverrideEnvKeys = %v, want %v", resp.OverrideEnvKeys, wantEnvKeys)
	}
	if strings.Contains(rec.Body.String(), "should-not-be-echoed") {
		t.Errorf("response body leaked env value 'should-not-be-echoed'; raw body = %s", rec.Body.String())
	}
	// Env-secret refs are echoed verbatim (refs are non-secret by
	// design — the customer needs to see which secret they bound).
	if got := resp.OverrideEnvSecretRefs["DB_URL"]; got != "secret:DB_URL" {
		t.Errorf("OverrideEnvSecretRefs[DB_URL] = %q, want secret:DB_URL", got)
	}
	if resp.OverrideHealthcheck == nil || resp.OverrideHealthcheck.Path != "/healthz" {
		t.Errorf("OverrideHealthcheck = %+v, want path=/healthz", resp.OverrideHealthcheck)
	}
}

// TestCreateDeployment_Overrides_RejectsInvalid pins the validation
// 400 path: a malformed override never silently drops — the whole
// request 400s with a code=validation_failed Problem (ADR-053
// §Decision 2).
func TestCreateDeployment_Overrides_RejectsInvalid(t *testing.T) {
	e := setup(t, api.PlanFree) // Free caps env at 8 entries / 4 KiB per value
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	digest := "sha256:" + repeat("a", 64)
	cases := []struct {
		name       string
		overrides  *api.CreateDeploymentOverrides
		wantInBody string
	}{
		{
			name: "port-out-of-range",
			overrides: &api.CreateDeploymentOverrides{
				Port: 70000,
			},
			wantInBody: "port 70000 out of range",
		},
		{
			name: "env-secrets-ref-missing-prefix",
			overrides: &api.CreateDeploymentOverrides{
				EnvSecrets: map[string]string{"DB_URL": "DB_URL"},
			},
			wantInBody: `must start with \"secret:\"`,
		},
		{
			name: "healthcheck-path-must-start-with-slash",
			overrides: &api.CreateDeploymentOverrides{
				Healthcheck: &api.DeploymentHealthcheck{Path: "healthz"},
			},
			wantInBody: `must start with \"/\"`,
		},
		{
			name: "env-key-violates-grammar",
			overrides: &api.CreateDeploymentOverrides{
				Env: map[string]string{"lower_case": "v"},
			},
			wantInBody: "Invalid env var key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
				api.CreateDeploymentRequest{Image: "r/x@" + digest, Overrides: tc.overrides}, nil)
			if rec.Code != 400 {
				t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.wantInBody) {
				t.Errorf("body = %s, want it to contain %q", rec.Body, tc.wantInBody)
			}
		})
	}
}

// TestCreateDeployment_Overrides_NoOverrideIsUnchanged pins the
// backward-compat path: a CreateDeploymentRequest without the
// `overrides` field is byte-for-byte the same as before issue #460
// landed (no behaviour change, no echo fields on the response).
func TestCreateDeployment_Overrides_NoOverrideIsUnchanged(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	digest := "sha256:" + repeat("a", 64)
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "r/x@" + digest}, nil)
	if rec.Code != 202 {
		t.Fatalf("POST deploy: %d %s", rec.Code, rec.Body)
	}
	var resp api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body)
	}
	if resp.HasOverrides {
		t.Errorf("response HasOverrides = true; want false when no override sent")
	}
	if len(resp.OverrideEntrypoint) > 0 {
		t.Errorf("OverrideEntrypoint = %v, want empty", resp.OverrideEntrypoint)
	}
	if resp.OverridePort != 0 {
		t.Errorf("OverridePort = %d, want 0", resp.OverridePort)
	}
	if resp.OverrideHealthcheck != nil {
		t.Errorf("OverrideHealthcheck = %+v, want nil", resp.OverrideHealthcheck)
	}
}

// TestCreateDeployment_Overrides_RedeployDifferentPort pins the
// per-deployment (not per-app) property of the override columns
// (ADR-053 §Decision 3). Two deploys of the same image to the same
// app with different override ports must:
//   - both be accepted (202),
//   - both echo their distinct override_port on the response,
//   - both rows exist in the deployments table (verified via
//     GET /v1/apps/{slug}/deployments, when present, or via the
//     underlying store directly).
//
// This is the load-bearing property that a per-app column would
// have violated: a customer who redeploys the same image with a
// different port must NOT have to PATCH the app AND redeploy.
func TestCreateDeployment_Overrides_RedeployDifferentPort(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	// Two distinct image digests. The OCI digest IS the only
	// difference between the two deploys — same app, same plan,
	// same override shape except the port.
	digestA := "sha256:" + repeat("a", 64)
	digestB := "sha256:" + repeat("b", 64)

	deployPort := func(digest string, port int) api.DeploymentResponse {
		rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
			api.CreateDeploymentRequest{
				Image: "r/x@" + digest,
				Overrides: &api.CreateDeploymentOverrides{
					Port: port,
				},
			}, nil)
		if rec.Code != 202 {
			t.Fatalf("POST deploy (port=%d): %d %s", port, rec.Code, rec.Body)
		}
		var resp api.DeploymentResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal (port=%d): %v body=%s", port, err, rec.Body)
		}
		return resp
	}

	respA := deployPort(digestA, 9090)
	respB := deployPort(digestB, 9091)

	if respA.OverridePort != 9090 {
		t.Errorf("deploy A OverridePort = %d, want 9090", respA.OverridePort)
	}
	if respB.OverridePort != 9091 {
		t.Errorf("deploy B OverridePort = %d, want 9091", respB.OverridePort)
	}
	if respA.ID == respB.ID {
		t.Errorf("deploy A and B share id %q; redeploy must mint a new row", respA.ID)
	}
	if !respA.HasOverrides || !respB.HasOverrides {
		t.Errorf("Both deploys must have HasOverrides=true; A=%v B=%v", respA.HasOverrides, respB.HasOverrides)
	}

	// Round-trip through the store to prove both rows persisted with
	// their distinct port values. This is the property that a
	// per-app column would fail — a per-app override_port would
	// overwrite on the second deploy.
	depA, err := e.store.DeploymentByID(context.Background(), respA.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(A): %v", err)
	}
	if depA.OverridePort != 9090 {
		t.Errorf("store.A OverridePort = %d, want 9090", depA.OverridePort)
	}
	depB, err := e.store.DeploymentByID(context.Background(), respB.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(B): %v", err)
	}
	if depB.OverridePort != 9091 {
		t.Errorf("store.B OverridePort = %d, want 9091", depB.OverridePort)
	}
}

func TestParseImageDigest_PureUnit(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := map[string]bool{
		// Happy paths — every host/repo shape imaged pulls from must work.
		"r/x@sha256:" + digest:                                true,
		"ghcr.io/onebox-faas/builder-base@sha256:" + digest:   true,
		"registry.example.com:5000/team/svc@sha256:" + digest: true,
		"127.0.0.1:5000/foo/bar@sha256:" + digest:             true,
		// Length / character errors in the digest.
		"r/x@sha256:" + digest[:63]:             false, // 63 hex chars
		"r/x@sha256:" + strings.Repeat("A", 64): false, // uppercase rejected
		"r/x@sha256:short":                      false,
		// Wrong algorithm / wrong tag form.
		"r/x@sha512:" + digest: false,
		"r/x:latest":           false,
		// Anchoring — the validator must reject non-OCI prefixes that
		// contain control chars, whitespace, or extra @-separators. These
		// cases are load-bearing for the slog log of req.Image in
		// createDeployment (CodeQL go/log-injection false-positive).
		"":                                false,
		"@sha256:" + digest:               false, // no host/repo
		"foo/@sha256:" + digest:           false, // empty repo
		"\nfoo/bar@sha256:" + digest:      false, // leading newline (log-injection)
		"foo bar@sha256:" + digest:        false, // whitespace in prefix
		"foo@@bar@sha256:" + digest:       false, // extra @ before the digest @-separator
		"foo/bar@sha256:" + digest + " ":  false, // trailing whitespace
		"foo/bar@sha256:" + digest + "\n": false, // trailing newline (log-injection)
	}
	for in, want := range cases {
		_, ok := parseImageDigest(in)
		if ok != want {
			t.Errorf("parseImageDigest(%q) ok=%v want=%v", in, ok, want)
		}
	}
}

// TestIsDigestPinned covers the validator used by createDeployment to gate
// log emission. It mirrors TestParseImageDigest_PureUnit's table so the two
// stay in lock-step — both call sites share digestPinnedRE.
func TestIsDigestPinned(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := map[string]bool{
		"r/x@sha256:" + digest:                           true,
		"ghcr.io/team/svc@sha256:" + digest:              true,
		"registry.example.com:5000/x/y@sha256:" + digest: true,
		// Rejections: same anchoring rules as parseImageDigest.
		"r/x@sha256:" + digest[:63]:             false,
		"r/x@sha256:" + strings.Repeat("A", 64): false,
		"r/x:latest":                            false,
		"":                                      false,
		"\nfoo/bar@sha256:" + digest:            false,
		"foo bar@sha256:" + digest:              false,
		"foo/bar@sha256:" + digest + "\n":       false,
	}
	for in, want := range cases {
		if got := isDigestPinned(in); got != want {
			t.Errorf("isDigestPinned(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestCreateDeployment_Overrides_RejectsUnknownField pins the wire-side
// enforcement of ADR-053 §Decision 1: the override field list is FROZEN.
// The handler's decoder uses DisallowUnknownFields, so a 7th field
// 400s the request — even one that isn't recognised by the override
// type. This complements pkg/api/dto_overrides_test.go which exercises
// the decoder in isolation; here the test goes through the live handler
// stack so a future refactor that drops the DisallowUnknownFields flag
// (or swaps the decoder for a more permissive one) fails loudly.
//
// Mirrors the dto_test.go "unknown-field-is-rejected" case but at the
// handler+transport layer.
func TestCreateDeployment_Overrides_RejectsUnknownField(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)

	// volume_mounts is a field shaped for a future ADR (mountPoints
	// mount type). Sending it on the override object today must 400.
	body := `{"image":"r/x@sha256:` + repeat("a", 64) +
		`","overrides":{"volume_mounts":[{"path":"/data","size_mb":1024}]}}`
	req := newRawRequest(t, "POST", "/v1/apps/dep-app/deployments", body,
		map[string]string{"Authorization": "Bearer " + e.key})
	rec := serveRaw(e.h, req)
	if rec.Code != 400 {
		t.Fatalf("unknown override field should 400, got %d %s", rec.Code, rec.Body)
	}
	// Body must reference the offending field so the customer can
	// fix the request. The wire-side error is produced by Go's
	// encoding/json "unknown field" sentinel.
	if !strings.Contains(rec.Body.String(), "volume_mounts") {
		t.Errorf("body = %s, want it to mention the unknown field volume_mounts", rec.Body)
	}
}

// helpers ---------------------------------------------------------------------

func newRawRequest(t *testing.T, method, path, body string, hdrs map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	return req
}

func serveRaw(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
