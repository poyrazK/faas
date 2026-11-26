package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxReleaseInviteAndRevocation(t *testing.T) {
	e, parent, source := sandboxReleaseFixture(t)
	ctx := t.Context()
	id := uuid.NewString()
	base := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID + "/api-sandboxes"
	sandboxPath := base + "/" + id
	invitePath := "/v1/api-sandboxes/" + id + "/invite"

	if invalid := e.do(t, http.MethodPut, sandboxPath, api.APISandboxCreateRequest{SmokePath: "//other.example/notes"}, nil); invalid.Code != http.StatusBadRequest {
		t.Fatalf("accepted a cross-host smoke path: %d", invalid.Code)
	}
	created := e.do(t, http.MethodPut, sandboxPath, api.APISandboxCreateRequest{SmokePath: "/notes"}, nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create sandbox: %d %s", created.Code, created.Body)
	}
	var sandbox api.APISandboxResponse
	if err := json.Unmarshal(created.Body.Bytes(), &sandbox); err != nil {
		t.Fatal(err)
	}
	if sandbox.Status != state.APISandboxPreparingDatabase || sandbox.SmokePath != "/notes" {
		t.Fatalf("queued sandbox = %+v", sandbox)
	}
	if conflict := e.do(t, http.MethodPut, sandboxPath, api.APISandboxCreateRequest{SmokePath: "/another"}, nil); conflict.Code != http.StatusConflict {
		t.Fatalf("a retry changed the smoke route: %d %s", conflict.Code, conflict.Body)
	}
	app, err := e.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil {
		t.Fatal(err)
	}
	if databases, err := e.s.managedPostgres.List(ctx, e.acct.ID); err != nil || len(databases) != 0 {
		t.Fatalf("the handler provisioned a database: %v, %v", databases, err)
	}

	// Use new worker attempts and the listing route from here on. There is
	// no follow-up PUT to advance the database, release, or verification.
	worker := func() {
		t.Helper()
		if err := e.s.processAPISandboxOperation(ctx, dueSandboxOperations{e.store}); err != nil {
			t.Fatal(err)
		}
	}
	readSandbox := func() api.APISandboxResponse {
		t.Helper()
		list := e.do(t, http.MethodGet, base, nil, nil)
		var out api.APISandboxListResponse
		if err := json.Unmarshal(list.Body.Bytes(), &out); err != nil || list.Code != http.StatusOK || len(out.Sandboxes) != 1 {
			t.Fatalf("list sandboxes: %d %s (%v)", list.Code, list.Body, err)
		}
		return out.Sandboxes[0]
	}
	worker()
	postgres, err := e.s.describeDevPostgres(ctx, app)
	if err != nil || postgres == nil {
		t.Fatalf("sandbox database receipt: %+v, %v", postgres, err)
	}
	if _, err := e.s.managedPostgres.Reconcile(ctx, e.acct.ID, postgres.DatabaseID); err != nil {
		t.Fatal(err)
	}
	bindings, err := e.s.managedPostgresBindings.List(ctx, e.acct.ID, postgres.DatabaseID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("sandbox database bindings: %+v, %v", bindings, err)
	}
	for _, binding := range bindings {
		if _, err := e.s.managedPostgresBindings.Reconcile(ctx, e.acct.ID, binding.ID); err != nil {
			t.Fatal(err)
		}
	}
	worker()
	sandbox = readSandbox()
	if sandbox.SourceDeploymentID != source.ID || sandbox.Status != state.APISandboxRunningMigrations || sandbox.DeploymentID == "" {
		t.Fatalf("sandbox receipt = %+v", sandbox)
	}
	if err != nil || app.Manifest.Env["PRODUCTION_ONLY"] != "" || app.RequireAuthn ||
		app.ConsumerAuthMode != api.ConsumerAuthModeRequired || len(app.EgressAllowlist) != 1 {
		t.Fatalf("sandbox must use isolated runtime inputs: %+v, %v", app, err)
	}
	env, err := e.store.ListAppEnvInScope(ctx, e.acct.ID, app.ID, api.DefaultEnvScope)
	if err != nil || len(env) != 1 || env[0].Key != "GREGALE_API_SANDBOX" || env[0].Value != "1" {
		t.Fatalf("sandbox release environment: %+v, %v", env, err)
	}
	if time.Until(sandbox.ExpiresAt) < 23*time.Hour {
		t.Fatalf("unexpected expiry: %s", sandbox.ExpiresAt)
	}
	deployment, err := e.store.DeploymentByID(ctx, sandbox.DeploymentID)
	if err != nil || deployment.RootfsKey != "/apps/sandbox-source.ext4" ||
		strings.Contains(string(deployment.OverrideEnv), "PRODUCTION_ONLY") || len(deployment.ReleaseCommand) != 2 {
		t.Fatalf("unsafe sandbox deployment: %+v, %v", deployment, err)
	}
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("sandbox accepted an unrelated release: %v", err)
	}
	retry := e.do(t, http.MethodPut, sandboxPath, nil, nil)
	if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), sandbox.DeploymentID) {
		t.Fatalf("retry must resume exact deployment: %d %s", retry.Code, retry.Body)
	}
	if response := e.do(t, http.MethodPost, sandboxPath+"/invites", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("invite before readiness: %d %s", response.Code, response.Body)
	}

	// Simulate the terminal deployment state reported by the release worker.
	if err := e.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	worker()
	if checking := readSandbox(); checking.Status != state.APISandboxVerifyingAPI || checking.Verification != nil {
		t.Fatalf("a live release was ready without public verification: %+v", checking)
	}
	if response := e.do(t, http.MethodPost, sandboxPath+"/invites", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("invite without public verification: %d %s", response.Code, response.Body)
	}
	var attempts []string
	var verifyToken string
	e.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != apiSandboxSlug(id)+".gregale.dev" {
			t.Fatalf("unexpected probe: %s %s", r.Method, r.URL)
		}
		verifyToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !api.ValidConsumerKeyFormat(verifyToken) {
			t.Fatal("missing scoped verification bearer")
		}
		attempts = append(attempts, r.URL.Path)
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, deployment.ID)
		if len(attempts) == 1 {
			headers.Set(api.RevisionHeader, source.ID)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(
			`{"openapi":"3.1.0","info":{"title":"Notes","version":"1"},"paths":{"/notes":{"get":{}}}}`))}, nil
	})}
	worker()
	if checking := readSandbox(); checking.Status != state.APISandboxVerifyingAPI || checking.Verification != nil {
		t.Fatalf("a different deployment passed verification: %+v", checking)
	}
	worker()
	sandbox = readSandbox()
	if sandbox.Status != state.APISandboxLive || sandbox.Verification == nil ||
		sandbox.Verification.DeploymentID != deployment.ID || sandbox.Verification.OpenAPIStatus != 200 ||
		sandbox.Verification.SmokeStatus != 200 || strings.Join(attempts, ",") != "/openapi.json,/openapi.json,/notes" {
		t.Fatalf("sandbox verification = %+v, probes=%v", sandbox, attempts)
	}
	verifyPrefix, _, _ := strings.Cut(strings.TrimPrefix(verifyToken, api.ConsumerKeyPrefix), "_")
	if key, err := e.store.ConsumerKeyByAppAndPrefix(ctx, e.acct.ID, app.ID, verifyPrefix); err == nil && key.Active(time.Now()) {
		t.Fatal("the readiness bearer remained active")
	}
	issued := e.do(t, http.MethodPost, sandboxPath+"/invites", nil, nil)
	if issued.Code != http.StatusCreated || issued.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("issue invite: %d %s", issued.Code, issued.Body)
	}
	var invite api.APISandboxInviteResponse
	if err := json.Unmarshal(issued.Body.Bytes(), &invite); err != nil || !api.ValidConsumerKeyFormat(invite.Token) {
		t.Fatalf("invalid invite response: %v", err)
	}
	headers := map[string]string{"Authorization": "Bearer " + invite.Token}
	for _, check := range []struct {
		method, path string
		headers      map[string]string
		status       int
	}{
		{http.MethodGet, invitePath, nil, http.StatusUnauthorized},
		{http.MethodGet, invitePath, headers, http.StatusOK},
		{http.MethodGet, "/v1/api-sandboxes/" + uuid.NewString() + "/invite", headers, http.StatusUnauthorized},
		{http.MethodGet, base, headers, http.StatusUnauthorized},
		{http.MethodGet, base, nil, http.StatusOK},
	} {
		response := e.do(t, check.method, check.path, nil, check.headers)
		if response.Code != check.status || strings.Contains(response.Body.String(), invite.Token) {
			t.Errorf("%s %s: got %d, want %d or leaked token", check.method, check.path, response.Code, check.status)
		}
	}
	ended := e.do(t, http.MethodDelete, sandboxPath, nil, nil)
	if ended.Code != http.StatusNoContent {
		t.Fatalf("end sandbox: %d %s", ended.Code, ended.Body)
	}
	if revoked := e.do(t, http.MethodGet, invitePath, nil, headers); revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked invite: %d %s", revoked.Code, revoked.Body)
	}
	if retry := e.do(t, http.MethodPut, sandboxPath, nil, nil); retry.Code != http.StatusGone {
		t.Fatalf("retry resurrected ended sandbox: %d %s", retry.Code, retry.Body)
	}
	production, err := e.store.DeploymentByID(ctx, source.ID)
	if err != nil || production.Status != state.DeployLive {
		t.Fatalf("production release changed: %v, %v", production.Status, err)
	}
}

type dueSandboxOperations struct{ *state.MemStore }

func (o dueSandboxOperations) ClaimAPISandboxOperation(ctx context.Context, now time.Time, lease time.Duration) (state.APISandboxOperation, error) {
	return o.MemStore.ClaimAPISandboxOperation(ctx, now.Add(apiSandboxRetryInterval), lease)
}

type sandboxRoundTripper func(*http.Request) (*http.Response, error)

func (f sandboxRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sandboxReleaseFixture(t *testing.T) (testEnv, state.App, state.Deployment) {
	t.Helper()
	e := setup(t, api.PlanPro)
	attachTestManagedPostgres(t, e.s, 3)
	ctx := t.Context()
	parent, err := e.store.CreateApp(ctx, state.App{
		AccountID: e.acct.ID, Slug: "sandbox-source", Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 512, WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{Env: map[string]string{"PRODUCTION_ONLY": "private"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := e.store.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive,
		OverrideEnv: json.RawMessage(`{"PRODUCTION_ONLY":"private"}`), ReleaseCommand: []string{"python", "migrate.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, source.ID, "/apps/sandbox-source.ext4", "/apps/sandbox-source.ext4", 1048576); err != nil {
		t.Fatal(err)
	}
	return e, parent, source
}
