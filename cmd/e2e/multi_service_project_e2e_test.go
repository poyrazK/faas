// multi_service_project_e2e_test.go — the multi-service story end to end.
//
// One Compose repository deploys as a project, and what the multi-service
// guide (docs/multi-service-apps.md) promises about it holds on the real
// daemons over Postgres: apid applies the project, gatewayd-public and
// gatewayd-internal route the service calls, and schedd wakes a parked
// service. A service is called by the DNS form of its name
// (ADR-20261026170512), a Compose value that calls a project service imports
// as its Gregale address (ADR-20261026181020), the project lists what each
// service calls (ADR-20261026193018), a declared service calls nothing else,
// waking a service wakes the services it declared (ADR-20261025105812), and a
// PR preview calls its own sibling.
//
// Build tag: (none). Requires Postgres (skip via FAAS_SKIP_PG_TESTS). No KVM:
// the fake vmmd stands in for the guests, as in service_mesh_e2e_test.go.
package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

// multiServiceProjectTarball is a shop with a web front end that declares it
// calls billing_api, a ledger web does not declare, and an admin under the
// default project policy that calls ledger. The Dockerfiles carry suffixes so
// the convention detector adds no workloads of its own.
func multiServiceProjectTarball(t *testing.T) []byte {
	t.Helper()
	const compose = `x-gregale-env-import: true
services:
  web:
    build:
      context: .
    ports:
      - "3000:3000"
    depends_on: [billing_api]
    x-gregale-service-policy: declared
    environment:
      BILLING_URL: http://billing_api:8080/v1
      LOG_LEVEL: info
  billing_api:
    build:
      context: .
    ports:
      - "8080:8080"
  ledger:
    build:
      context: .
    ports:
      - "8090:8090"
  admin:
    build:
      context: .
    ports:
      - "8070:8070"
    depends_on: [ledger]
`
	files := []struct{ name, body string }{
		{"shop/docker-compose.yml", compose},
		{"shop/Dockerfile.web", "FROM alpine:3.19\nEXPOSE 3000\nCMD [\"./web\"]\n"},
		{"shop/Dockerfile.billing", "FROM alpine:3.19\nEXPOSE 8080\nCMD [\"./billing\"]\n"},
		{"shop/Dockerfile.ledger", "FROM alpine:3.19\nEXPOSE 8090\nCMD [\"./ledger\"]\n"},
		{"shop/Dockerfile.admin", "FROM alpine:3.19\nEXPOSE 8070\nCMD [\"./admin\"]\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", file.name, err)
		}
		if _, err := tw.Write([]byte(file.body)); err != nil {
			t.Fatalf("tar write %s: %v", file.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// projectAppsByWorkload reads back the apps an apply created, by service name.
func projectAppsByWorkload(t *testing.T, f *normalPathFixture, applied api.ApplyResponse) map[string]state.App {
	t.Helper()
	apps := map[string]state.App{}
	for _, member := range applied.Apps {
		app, err := f.store.AppByID(f.ctx, member.ID)
		if err != nil {
			t.Fatalf("read applied app %q: %v", member.Slug, err)
		}
		apps[app.WorkloadName] = app
	}
	for _, name := range []string{"web", "billing_api", "ledger", "admin"} {
		if _, ok := apps[name]; !ok {
			t.Fatalf("apply created no %q service; apps = %+v", name, applied.Apps)
		}
	}
	return apps
}

// parkMultiServiceApp gives app a live, cold-bootable deployment and no
// instance: parked, as scale-to-zero leaves it.
func parkMultiServiceApp(t *testing.T, f *normalPathFixture, app state.App) {
	t.Helper()
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("4", 64)})
	if err != nil {
		t.Fatalf("create %s deployment: %v", app.Slug, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark %s deployment live: %v", app.Slug, err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	if running, err := f.store.RunningInstanceForApp(f.ctx, app.ID); err == nil {
		t.Fatalf("%s should start parked, found running instance %q", app.Slug, running.ID)
	}
}

// wokenInstance is the running instance a service call woke, and the request
// the fake guest received must have reached it.
func wokenInstance(t *testing.T, f *normalPathFixture, app state.App, wantURI string) {
	t.Helper()
	running, err := f.store.RunningInstanceForApp(f.ctx, app.ID)
	if err != nil {
		t.Fatalf("no running %s instance after the service call: %v", app.Slug, err)
	}
	request := f.vmmd.LastRequest()
	if request == nil {
		t.Fatal("fake vmmd received no ForwardHTTPStream request")
	}
	if request.Instance != running.ID || request.RequestUri != wantURI {
		t.Fatalf("guest request reached instance %q at %q, want %s's %q at %q", request.Instance, request.RequestUri, app.Slug, running.ID, wantURI)
	}
}

// pollPublicRequest sends the owner's request to app's public host until it
// gets want or the budget runs out, and returns the last status.
func pollPublicRequest(t *testing.T, f *normalPathFixture, app state.App, want int, budget time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(budget)
	status := 0
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.h.EdgeURL()+"/", nil)
		if err != nil {
			t.Fatalf("new public request: %v", err)
		}
		req.Host = app.Slug + ".apps.test.example"
		req.Header.Set("Authorization", "Bearer "+f.key)
		resp, err := f.h.HTTPClient().Do(req)
		if err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
			if status == want {
				return status
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return status
}

// waitRunning reports whether app has a running instance within budget.
func waitRunning(f *normalPathFixture, app state.App, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if _, err := f.store.RunningInstanceForApp(f.ctx, app.ID); err == nil {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// ownerNodeOf waits for schedd to stamp app's owner node. Apply inserts
// project apps unplaced and schedd claims them from apid's created notify, so
// a row read right after apply may not carry a node yet.
func ownerNodeOf(t *testing.T, f *normalPathFixture, app state.App) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		current, err := f.store.AppByID(f.ctx, app.ID)
		if err != nil {
			t.Fatalf("read %s: %v", app.Slug, err)
		}
		if current.NodeID != "" {
			return current.NodeID
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has no owner node 10s after apply", app.Slug)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// createPreviewThroughAPI creates app's PR preview with the developer preview
// API (`gregale preview create`) and reads the row back.
func createPreviewThroughAPI(t *testing.T, f *normalPathFixture, app state.App, pr int) state.App {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+app.Slug+"/previews", api.CreatePreviewRequest{PRNumber: pr})
	if status != http.StatusCreated {
		t.Fatalf("create PR %d preview of %s: status=%d body=%s", pr, app.Slug, status, body)
	}
	var created api.AppResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode preview: %v body=%s", err, body)
	}
	preview, err := f.store.AppByID(f.ctx, created.ID)
	if err != nil {
		t.Fatalf("read preview %s: %v", created.Slug, err)
	}
	return preview
}

// previewOf creates the PR-number preview of a project service, carrying its
// manifest and owner node, as the preview path does. The preview row is
// inserted directly, with no created notify, so schedd never claims it: it
// must carry the owner node from the start.
func previewOf(t *testing.T, f *normalPathFixture, app state.App, pr int) state.App {
	t.Helper()
	preview, err := f.store.CreateApp(f.ctx, state.App{AccountID: app.AccountID, ProjectID: app.ProjectID, Slug: fmt.Sprintf("pr-%d-%s", pr, app.Slug),
		WorkloadName: app.WorkloadName, PreviewOfSlug: app.Slug, PreviewPrNumber: pr, Type: app.Type, Status: state.AppActive, NodeID: ownerNodeOf(t, f, app),
		RAMMB: app.RAMMB, MaxConcurrency: app.MaxConcurrency, IdleTimeoutS: app.IdleTimeoutS, Manifest: app.Manifest})
	if err != nil {
		t.Fatalf("create preview of %s: %v", app.Slug, err)
	}
	return preview
}

func TestE2E_MultiServiceProject_ComposeServicesCallEachOtherByName(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "msp-anchor", api.PlanPro)
	if f == nil {
		return
	}
	applied := applyProjectMultipart(t, f.h, f.key, "shop", "", multiServiceProjectTarball(t))
	apps := projectAppsByWorkload(t, f, applied)
	web, billing, ledger, admin := apps["web"], apps["billing_api"], apps["ledger"], apps["admin"]

	// web declared billing_api, so it gets billing_api's address under the
	// name it is called by.
	for key, want := range map[string]string{
		"GREGALE_SERVICE_BILLING_API_URL":       "http://billing-api.svc.gregale:10081",
		"GREGALE_SERVICE_BILLING_API_HTTPS_URL": "https://billing-api.internal",
		"GREGALE_SERVICE_BILLING_API_HOST":      "billing-api.svc.gregale",
	} {
		if got := web.Manifest.Env[key]; got != want {
			t.Errorf("web %s = %q, want %q", key, got, want)
		}
	}
	if web.Manifest.EffectiveServiceBindingPolicy() != api.ServiceBindingPolicyDeclared {
		t.Errorf("web service policy = %q, want declared", web.Manifest.EffectiveServiceBindingPolicy())
	}

	// The Compose value that called billing_api by its Docker name imports as
	// its Gregale address, path kept; a plain value imports as written.
	rows, err := f.store.ListAppEnv(f.ctx, web.AccountID, web.ID)
	if err != nil {
		t.Fatalf("list web env: %v", err)
	}
	imported := map[string]string{}
	for _, row := range rows {
		imported[row.Key] = row.Value
	}
	if imported["BILLING_URL"] != "http://billing-api.svc.gregale:10081/v1" || imported["LOG_LEVEL"] != "info" {
		t.Errorf("imported web env = %v, want BILLING_URL as billing-api's address and LOG_LEVEL=info", imported)
	}

	// The project lists what each service calls.
	body, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/projects/shop", nil)
	if status != http.StatusOK {
		t.Fatalf("get project: status=%d body=%s", status, body)
	}
	var project api.ProjectResponse
	if err := json.Unmarshal(body, &project); err != nil {
		t.Fatalf("decode project: %v body=%s", err, body)
	}
	calls := map[string][]string{}
	for _, workload := range project.Workloads {
		calls[workload.WorkloadName] = workload.DependsOn
	}
	if !slices.Equal(calls["web"], []string{"billing_api"}) || !slices.Equal(calls["admin"], []string{"ledger"}) ||
		calls["billing_api"] != nil || calls["ledger"] != nil {
		t.Errorf("project service calls = %v, want web calling billing_api and admin calling ledger", calls)
	}

	// A call to billing-api wakes the parked billing_api and reaches it with
	// the path kept. The slug is project-prefixed, so only the service-name
	// lookup can find it.
	f.vmmd.SetDefaultVersion("v1")
	parkMultiServiceApp(t, f, billing)
	parkMultiServiceApp(t, f, ledger)
	if status, _, body := pollServiceCall(t, f.h, web.ID, "billing-api", "/v1/invoices", http.StatusOK, 30*time.Second); status != http.StatusOK {
		t.Fatalf("web -> billing-api status = %d, want 200 (the parked service must wake); body=%s", status, body)
	}
	wokenInstance(t, f, billing, "/v1/invoices")

	// web declared only billing_api, so ledger refuses it before waking.
	if status, _, body := serviceCall(t, f.h, web.ID, "ledger", "/"); status != http.StatusForbidden {
		t.Errorf("web -> ledger status = %d, want 403 (undeclared under the declared policy); body=%s", status, body)
	}
	if running, err := f.store.RunningInstanceForApp(f.ctx, ledger.ID); err == nil {
		t.Errorf("a refused call woke ledger: instance %q", running.ID)
	}

	// A public request wakes the parked admin, and wake-ahead wakes ledger,
	// which admin declared, before admin calls it.
	if enabled, err := f.store.ServiceWakeAheadEnabled(f.ctx, admin.ID); err != nil || !enabled {
		t.Fatalf("wake-ahead for admin = %t, %v; want on by default under the project policy (stored policy %q)",
			enabled, err, admin.Manifest.ServiceBindingPolicy)
	}
	if target, err := state.AppByServiceName(f.ctx, f.store, admin, "ledger"); err != nil || target.ID != ledger.ID {
		t.Fatalf("admin resolves ledger to %+v, %v", target, err)
	}
	if _, err := servicebinding.NewAuthorizer(f.store)(f.ctx, admin.ID, ledger.ID); err != nil {
		t.Fatalf("admin may not call ledger: %v", err)
	}
	if err := state.CheckFinancialBudgetAdmission(f.ctx, f.store, ledger, "", time.Now().UTC()); err != nil {
		t.Fatalf("ledger budget admission: %v", err)
	}
	parkMultiServiceApp(t, f, admin)
	if status := pollPublicRequest(t, f, admin, http.StatusOK, 30*time.Second); status != http.StatusOK {
		t.Fatalf("public GET on admin status = %d, want 200 (the parked app must wake)", status)
	}
	if !waitRunning(f, ledger, 30*time.Second) {
		resident, ceiling, err := f.store.ServiceWakeAheadFleetResidency(f.ctx)
		t.Errorf("waking admin did not wake ledger ahead of admin's first call; residency %d/%d MB (%v); gateway: %v",
			resident, ceiling, err, metricLines(f.h.GatewayControlURL+"/metrics", "gateway_service_wake_ahead"))
	}

	// PR 7's web gets web's pr-preview values (ADR-20261028090000) and calls
	// PR 7's billing_api, never production's.
	if body, status := doReq(t, f.h, f.key, http.MethodPut, "/v1/apps/"+web.Slug+"/env/PAYMENTS_MODE?scope="+api.PreviewEnvScope,
		api.PutAppEnvRequest{Value: "sandbox"}); status != http.StatusOK {
		t.Fatalf("set web's pr-preview value: status=%d body=%s", status, body)
	}
	previewWeb := createPreviewThroughAPI(t, f, web, 7)
	previewBilling := previewOf(t, f, billing, 7)
	if rows, err := f.store.ListAppEnvInScope(f.ctx, previewWeb.AccountID, previewWeb.ID, api.DefaultEnvScope); err != nil ||
		!slices.ContainsFunc(rows, func(row state.AppEnv) bool { return row.Key == "PAYMENTS_MODE" && row.Value == "sandbox" }) {
		t.Fatalf("PR 7's web values = %+v, %v; want web's pr-preview PAYMENTS_MODE=sandbox", rows, err)
	}
	parkMultiServiceApp(t, f, previewBilling)
	if status, _, body := pollServiceCall(t, f.h, previewWeb.ID, "billing-api", "/v1/invoices?pr=7", http.StatusOK, 30*time.Second); status != http.StatusOK {
		t.Fatalf("preview web -> billing-api status = %d, want 200; body=%s", status, body)
	}
	wokenInstance(t, f, previewBilling, "/v1/invoices?pr=7")
}
