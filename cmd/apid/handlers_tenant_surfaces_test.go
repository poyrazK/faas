// handlers_tenant_surfaces_test.go — issue #879 / ADR-100 PR-C
// handler tests for the tenant-surfaces customer-facing surface.
//
// The cluster ships dark under FAAS_TENANT_SURFACES_ENABLED — every
// handler must return a 503 feature-unavailable problem unless the flag is
// set. With the flag set, the
// surface mirrors the closest precedent: cmd/apid/handlers_ext.go:1573
// (custom_domains). The 12 cases below pin the contract so a future
// refactor that drops the feature flag, the plan-tier gate, or the
// cross-account predicate fires a red gate here, not a 3am pager.
//
// Coverage split:
//   - shape:    happy path create→list→get→addHostname→removeHostname→delete
//   - flag:     feature flag off → 503 on every handler
//   - plan:     Free → 402 (TenantSurfacesAllowed=false)
//   - quota:    per-account surface cap, per-surface hostname cap
//   - IDOR:     cross-account surface → 404 (no existence oracle)
//   - UQ:       hostname already claimed by another surface → 409
//   - cascade:  deleteSurface removes hostnames (re-add of same hostname works)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// withTenantSurfacesEnabled flips the dark-launch flag for the
// duration of one test. Mirrors the pattern in
// cmd/gatewayd-internal/tenant_surfaces_routing_test.go that uses
// t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true").
func withTenantSurfacesEnabled(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
}

// withDomainDoctorEnabled (ADR-120 Tier A3) flips the
// FAAS_DOMAIN_DOCTOR_ENABLED env var to "true" for the duration
// of one test. Mirror of withTenantSurfacesEnabled — same test
// pack pattern (default-on in production but tests flip
// explicitly to a recognised on-token so the test reads as
// opt-in). Post-Tier-A3 the env var's default is already on
// (pkg/api/flags.go::DomainDoctorEnabled returns true when the
// var is unset), but the explicit flip is kept here so a test
// reads as deterministic against an explicit decision rather
// than an implicit default.
func withDomainDoctorEnabled(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "true")
}

// withDomainDoctorDisabled flips the FAAS_DOMAIN_DOCTOR_ENABLED
// env var to an explicit-off token ("false") for the duration
// of one test. Used to verify the doctor route returns 503
// doctor_disabled when the operator opts out (the same
// CodeDoctorDisabled path the JSON endpoint serves). Mirror
// pattern to withDomainDoctorEnabled.
func withDomainDoctorDisabled(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "false")
}

// seedTenantSurface creates a tenant surface (no hostnames) on the
// test account's seeded app and returns the surface ID. Mirrors
// mustSeedApp's shape but for the tenant surface primitive.
func seedTenantSurface(t *testing.T, e testEnv, appID, name string) string {
	t.Helper()
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatalf("LimitsFor(%q) returned false", e.acct.Plan)
	}
	surf, err := e.store.CreateTenantSurfaceIfUnderQuota(context.Background(), state.CreateTenantSurfaceParams{
		AccountID: e.acct.ID,
		AppID:     appID,
		Name:      name,
		CertKind:  state.CertKindPerHostSAN,
	}, limits)
	if err != nil {
		t.Fatalf("seedTenantSurface(%s): %v", name, err)
	}
	return surf.ID
}

// makeTenantSurfaceReq mirrors CreateTenantSurfaceRequest — kept
// local so tests stay self-contained against the API DTO.
func makeTenantSurfaceReq(appID, name string, hostnames ...string) api.CreateTenantSurfaceRequest {
	return api.CreateTenantSurfaceRequest{
		AppID:     appID,
		Name:      name,
		CertKind:  string(state.CertKindPerHostSAN),
		Hostnames: hostnames,
	}
}

// TestCreateTenantSurface_HappyPath is the vertical-slice opening
// case: feature flag on, plan=Pro, one surface with two hostnames.
// Asserts 202 (mirrors createDomain's "cert engine has to mint"
// choice), response shape, and that the hostnames persist under
// the surface.
func TestCreateTenantSurface_HappyPath(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "happy-app")

	req := makeTenantSurfaceReq(appID, "customer-zones", "api.cust-a.com", "api.cust-b.com")
	rec := e.do(t, "POST", "/v1/apps/happy-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("createSurface status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.TenantSurfaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Name != "customer-zones" {
		t.Errorf("name = %q, want customer-zones", resp.Name)
	}
	if resp.AppID != appID {
		t.Errorf("app_id = %q, want %q", resp.AppID, appID)
	}
	if resp.AccountID != e.acct.ID {
		t.Errorf("account_id = %q, want %q", resp.AccountID, e.acct.ID)
	}
	if len(resp.Hostnames) != 2 {
		t.Fatalf("hostnames = %d, want 2", len(resp.Hostnames))
	}
	for _, h := range resp.Hostnames {
		if !strings.HasPrefix(h.TXTRecord, "_faas-verify.") {
			t.Errorf("hostname %q TXT record %q missing _faas-verify prefix", h.Hostname, h.TXTRecord)
		}
		if h.ChallengeToken == "" {
			t.Errorf("hostname %q challenge_token empty; want a token", h.Hostname)
		}
		if h.Verified {
			t.Errorf("hostname %q verified=true on creation; want false until DNS-01 lands", h.Hostname)
		}
	}

	// Notify: the handler does NOT call s.notif.Notify here. The
	// surface row INSERT fires the trigger at migrations/00243:127-145
	// which emits pg_notify('tenant_surface_changed', bare_surface_uuid).
	// A second explicit emit was redundant — it produced a JSON payload
	// the gatewayd consumer parsed as a UUID and silently dropped
	// (review finding #1 on PR #937).
	notif.mu.Lock()
	defer notif.mu.Unlock()
	for _, n := range notif.emitted {
		if n.Channel == "tenant_surface_changed" {
			t.Errorf("unexpected explicit tenant_surface_changed emit: payload=%s (the trigger handles this)", n.Payload)
		}
	}
}

// TestCreateTenantSurface_FlagOffBlocks is the dark-launch guard:
// without FAAS_TENANT_SURFACES_ENABLED, the handler must return 503
// with the feature-unavailable code. The check runs BEFORE the loadApp call
// so even a 404-eligible request (unknown slug) gets 503 — the
// surface is invisible until the operator flips the flag.
func TestCreateTenantSurface_FlagOffBlocks(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "")
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "flag-off-app")

	req := makeTenantSurfaceReq(appID, "any-name", "api.example.com")
	rec := e.do(t, "POST", "/v1/apps/flag-off-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("flag-off status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_surfaces_not_enabled") {
		t.Errorf("body missing tenant_surfaces_not_enabled code: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upgrade to Hobby") {
		t.Errorf("feature-disabled response must not suggest a plan downgrade: %s", rec.Body.String())
	}
}

// TestTenantSurfaces_RuntimeDisabledOnScale verifies the live failure mode
// from issue #1989 for both advertised Scale operations. A disabled runtime
// switch is a deployment problem, not a billing decision, so list and add
// must return the feature code without Scale→Hobby guidance.
func TestTenantSurfaces_RuntimeDisabledOnScale(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "")
	e := setup(t, api.PlanScale)
	appID := mustSeedApp(t, e, "scale-runtime-off-app")
	surfaceID := seedTenantSurface(t, e, appID, "scale-surface")

	list := e.do(t, "GET", "/v1/apps/scale-runtime-off-app/tenant-surfaces", nil, nil)
	add := e.do(t, "POST", "/v1/apps/scale-runtime-off-app/tenant-surfaces/"+surfaceID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "tenant.example.com"}, nil)
	for name, rec := range map[string]*httptest.ResponseRecorder{"list": list, "add": add} {
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503; body=%s", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "tenant_surfaces_not_enabled") {
			t.Errorf("%s body missing tenant_surfaces_not_enabled code: %s", name, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "upgrade to Hobby") {
			t.Errorf("%s feature-disabled response must not suggest a plan downgrade: %s", name, rec.Body.String())
		}
	}
}

// TestTenantSurfaces_ScaleEnabledListAndAdd pins the advertised Scale tier
// against the deployed feature configuration: once the runtime switch is
// enabled, the Scale customer can create, list, and add a hostname.
func TestTenantSurfaces_ScaleEnabledListAndAdd(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanScale)
	appID := mustSeedApp(t, e, "scale-enabled-app")

	created := e.do(t, "POST", "/v1/apps/scale-enabled-app/tenant-surfaces",
		makeTenantSurfaceReq(appID, "scale-surface"), nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202; body=%s", created.Code, created.Body.String())
	}
	var surface api.TenantSurfaceResponse
	if err := json.Unmarshal(created.Body.Bytes(), &surface); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	list := e.do(t, "GET", "/v1/apps/scale-enabled-app/tenant-surfaces", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", list.Code, list.Body.String())
	}
	add := e.do(t, "POST", "/v1/apps/scale-enabled-app/tenant-surfaces/"+surface.ID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "tenant.example.com"}, nil)
	if add.Code != http.StatusAccepted {
		t.Fatalf("add status = %d, want 202; body=%s", add.Code, add.Body.String())
	}
}

// TestCreateTenantSurface_FreePlanBlocks pins the plan-tier gate:
// Hobby/Pro/Scale unlock TenantSurfacesAllowed; Free does not. The
// handler reads LimitsFor(acct.Plan) and 402s before any DB row
// is touched.
func TestCreateTenantSurface_FreePlanBlocks(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanFree)
	appID := mustSeedApp(t, e, "free-app")

	req := makeTenantSurfaceReq(appID, "freezone", "api.example.com")
	rec := e.do(t, "POST", "/v1/apps/free-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("Free plan status = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_surfaces_not_allowed") {
		t.Errorf("body missing tenant_surfaces_not_allowed code: %s", rec.Body.String())
	}
}

// TestCreateTenantSurface_AppIDMismatch locks the "app_id in body
// must equal slug's app" predicate. Customers can't attach a
// surface to a different app by smuggling the id in the body —
// the handler refuses with 404 (not 400, to avoid confirming the
// slug exists).
func TestCreateTenantSurface_AppIDMismatch(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	mustSeedApp(t, e, "real-app")
	otherAppID := mustSeedApp(t, e, "other-app")

	req := makeTenantSurfaceReq(otherAppID, "smuggled", "api.example.com")
	rec := e.do(t, "POST", "/v1/apps/real-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("app_id mismatch status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateTenantSurface_QuotaExceeded pins the per-account
// surface quota: Hobby allows 1; we seed 1 then attempt a 2nd.
// The 2nd must 4xx with the dedicated quota code so the
// dashboard renders the upgrade copy.
func TestCreateTenantSurface_QuotaExceeded(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "hobby-quota-app")
	seedTenantSurface(t, e, appID, "first")

	req := makeTenantSurfaceReq(appID, "second", "api.example.com")
	rec := e.do(t, "POST", "/v1/apps/hobby-quota-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("quota status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_surface_quota") {
		t.Errorf("body missing tenant_surface_quota code: %s", rec.Body.String())
	}
}

// TestCreateTenantSurface_InvalidCertKind pins fix #3 from the
// PR #937 review: the handler must validate cert_kind against the
// state.CertKind whitelist BEFORE the store call so a bogus value
// returns 400 with the dedicated code instead of falling through to
// the SQL CHECK constraint and surfacing as a 500 capacity error.
func TestCreateTenantSurface_InvalidCertKind(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "cert-kind-app")

	req := makeTenantSurfaceReq(appID, "bogus-cert", "api.example.com")
	req.CertKind = "not_a_real_kind"
	rec := e.do(t, "POST", "/v1/apps/cert-kind-app/tenant-surfaces", req, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid cert_kind status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_surface_cert_kind_invalid") {
		t.Errorf("body missing tenant_surface_cert_kind_invalid code: %s", rec.Body.String())
	}
}

// TestAddTenantHostname_AlreadyClaimed pins the UQ trip: a hostname
// already attached to ANOTHER surface must 409 with the dedicated
// code. The PR-A-reserved CodeTenantHostnameAlreadyClaimed is what
// the dashboard renders as "this hostname is already in use".
func TestAddTenantHostname_AlreadyClaimed(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "uq-app")

	// First surface takes the hostname (via API, so the surface
	// exists for the routing predicate).
	first := makeTenantSurfaceReq(appID, "first", "api.shared.com")
	rec := e.do(t, "POST", "/v1/apps/uq-app/tenant-surfaces", first, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first surface status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// Second surface is created via the store directly (skipping
	// the API) so we can use the POST hostnames endpoint to
	// attempt to add the conflicting hostname.
	secondID := seedTenantSurface(t, e, appID, "second")
	rec = e.do(t, "POST", "/v1/apps/uq-app/tenant-surfaces/"+secondID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.shared.com"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("uq status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_hostname_already_claimed") {
		t.Errorf("body missing tenant_hostname_already_claimed code: %s", rec.Body.String())
	}
}

// TestAddTenantHostname_PerSurfaceQuotaExceeded pins the per-surface
// hostname quota. Hobby plan allows 10 VERIFIED hostnames per surface
// (per pkg/state/memstore_tenant_surface.go:230 — pending hostnames
// don't count). We seed 10 verified hostnames, then attempt the
// 11th — must 403 with the per-surface quota code.
func TestAddTenantHostname_PerSurfaceQuotaExceeded(t *testing.T) {
	withTenantSurfacesEnabled(t)
	// Hobby plan: 10 hostnames per surface.
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "host-quota-app")
	surfID := seedTenantSurface(t, e, appID, "imposed")

	// Fill 10 hostnames, mark each verified so the per-surface cap
	// counts them.
	for i := 0; i < 10; i++ {
		hn := fmt.Sprintf("api%d.example.com", i)
		rec := e.do(t, "POST", "/v1/apps/host-quota-app/tenant-surfaces/"+surfID+"/hostnames",
			api.AddTenantHostnameRequest{Hostname: hn}, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("host %d add status = %d, want 202; body=%s", i, rec.Code, rec.Body.String())
		}
		if err := e.store.MarkTenantHostnameVerified(context.Background(), hn); err != nil {
			t.Fatalf("MarkTenantHostnameVerified(%s): %v", hn, err)
		}
	}

	// 11th hostname must 403.
	rec := e.do(t, "POST", "/v1/apps/host-quota-app/tenant-surfaces/"+surfID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api10.example.com"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("11th hostname status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_hostname_quota") {
		t.Errorf("body missing tenant_hostname_quota code: %s", rec.Body.String())
	}
}

// TestListTenantSurfaces_OnlyOwnApp guards the list scope: the
// only path to listing surfaces is /v1/apps/{slug}/tenant-surfaces,
// so the handler runs loadApp first; an unknown slug 404s.
func TestListTenantSurfaces_UnknownApp(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/apps/no-such-slug/tenant-surfaces", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestGetTenantSurface_CrossAccountIs404 is the IDOR guard: the
// handler gets the surface by id, then checks surface.AccountID
// != acct.ID → 404. The response body must be byte-identical to
// a missing-id request so the endpoint never serves as an
// existence oracle.
func TestGetTenantSurface_CrossAccountIs404(t *testing.T) {
	withTenantSurfacesEnabled(t)
	store := state.NewMemStore()
	acctA, _ := store.CreateAccount(context.Background(), "a-cross@example.com", api.PlanPro)
	acctB, _ := store.CreateAccount(context.Background(), "b-cross@example.com", api.PlanPro)
	appA, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acctA.ID, Slug: "cross-app", Type: state.AppTypeApp, Status: state.AppActive,
	})
	_, _ = store.CreateApp(context.Background(), state.App{
		AccountID: acctB.ID, Slug: "app-b", Type: state.AppTypeApp, Status: state.AppActive,
	})
	limits, _ := api.LimitsFor(api.PlanPro)
	surfOnA, err := store.CreateTenantSurfaceIfUnderQuota(context.Background(), state.CreateTenantSurfaceParams{
		AccountID: acctA.ID, AppID: appA.ID, Name: "on-a", CertKind: state.CertKindPerHostSAN,
	}, limits)
	if err != nil {
		t.Fatalf("CreateTenantSurfaceIfUnderQuota: %v", err)
	}

	// Account B fires the GET — must 404 with byte-identical body
	// to a missing-id request.
	eB, _ := newTestServerWithCapturingNotifierWithStore(t, store, acctB)
	rec := eB.do(t, "GET", "/v1/apps/app-b/tenant-surfaces/"+surfOnA.ID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	missingRec := eB.do(t, "GET", "/v1/apps/app-b/tenant-surfaces/00000000-0000-0000-0000-000000000000", nil, nil)
	if rec.Body.String() != missingRec.Body.String() {
		t.Errorf("body differs:\n  cross-account: %s\n  missing:      %s",
			rec.Body.String(), missingRec.Body.String())
	}
}

// TestDeleteTenantSurface_CascadesHostnames pins the cascade
// contract: deleting a surface removes its hostnames so a future
// re-add of the same hostname to a new surface doesn't trip the
// UQ. PR-A's DeleteTenantSurface is soft-delete; the hostname
// rows are hard-deleted by the handler.
func TestDeleteTenantSurface_CascadesHostnames(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "cascade-app")

	// Create surface with one hostname.
	req := makeTenantSurfaceReq(appID, "todelete", "api.legacy.com")
	rec := e.do(t, "POST", "/v1/apps/cascade-app/tenant-surfaces", req, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.TenantSurfaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	surfID := resp.ID

	// Delete the surface.
	rec = e.do(t, "DELETE", "/v1/apps/cascade-app/tenant-surfaces/"+surfID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// GET on the deleted surface must 404 (it's soft-deleted; the
	// store filter excludes status='deleted').
	rec = e.do(t, "GET", "/v1/apps/cascade-app/tenant-surfaces/"+surfID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("post-delete GET status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// Re-add the same hostname to a new surface — must succeed.
	second := makeTenantSurfaceReq(appID, "replacement", "api.legacy.com")
	rec = e.do(t, "POST", "/v1/apps/cascade-app/tenant-surfaces", second, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("re-add hostname status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRemoveTenantHostname_NotFound pins the cross-surface hostname
// remove guard: a hostname attached to surface A, accessed via
// surface B's path, must 404. The handler joins through the
// hostname → surface_id link so the path check is enforced at
// the data layer, not by trusting the URL.
func TestRemoveTenantHostname_NotFound(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "rm-host-app")
	surfA := seedTenantSurface(t, e, appID, "surfaceA")
	surfB := seedTenantSurface(t, e, appID, "surfaceB")

	// Attach hostname to surfaceA.
	rec := e.do(t, "POST", "/v1/apps/rm-host-app/tenant-surfaces/"+surfA+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.move.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("add status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	// Try to remove it via surfaceB's path — must 404.
	rec = e.do(t, "DELETE", "/v1/apps/rm-host-app/tenant-surfaces/"+surfB+"/hostnames/api.move.com", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-surface remove status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRemoveTenantHostname_HappyPath completes the vertical slice:
// add hostname → remove hostname → re-add must succeed. The
// challenge token rotates only on re-add; the add→remove path
// is atomic (no partial state).
func TestRemoveTenantHostname_HappyPath(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "rm-happy-app")
	surfID := seedTenantSurface(t, e, appID, "happy")

	rec := e.do(t, "POST", "/v1/apps/rm-happy-app/tenant-surfaces/"+surfID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.byebye.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("add status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "DELETE", "/v1/apps/rm-happy-app/tenant-surfaces/"+surfID+"/hostnames/api.byebye.com", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// Re-add via a fresh POST must succeed (no UQ conflict).
	rec = e.do(t, "POST", "/v1/apps/rm-happy-app/tenant-surfaces/"+surfID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.byebye.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("re-add status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestListTenantSurfaces_HappyPath pins the list shape: a surface
// with N hostnames renders as a TenantSurfaceResponse whose
// Hostnames field is non-nil and len == N. The list is bounded
// by TenantSurfacesPerAccount so we render the whole set with no
// cursor (mirrors listCrons).
func TestListTenantSurfaces_HappyPath(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	appID := mustSeedApp(t, e, "list-app")

	// Two surfaces, one with two hostnames.
	alphaID := seedTenantSurface(t, e, appID, "alpha")
	seedTenantSurface(t, e, appID, "beta")
	rec := e.do(t, "POST", "/v1/apps/list-app/tenant-surfaces/"+alphaID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "a1.example.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("alpha h1: %d", rec.Code)
	}
	rec = e.do(t, "POST", "/v1/apps/list-app/tenant-surfaces/"+alphaID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "a2.example.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("alpha h2: %d", rec.Code)
	}

	rec = e.do(t, "GET", "/v1/apps/list-app/tenant-surfaces", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ListTenantSurfacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Surfaces) != 2 {
		t.Fatalf("surfaces = %d, want 2", len(resp.Surfaces))
	}
	var alpha, beta *api.TenantSurfaceResponse
	for i := range resp.Surfaces {
		switch resp.Surfaces[i].Name {
		case "alpha":
			alpha = &resp.Surfaces[i]
		case "beta":
			beta = &resp.Surfaces[i]
		}
	}
	if alpha == nil || beta == nil {
		t.Fatalf("missing alpha/beta in list response: %+v", resp.Surfaces)
	}
	if len(alpha.Hostnames) != 2 {
		t.Errorf("alpha hostnames = %d, want 2", len(alpha.Hostnames))
	}
	if len(beta.Hostnames) != 0 {
		t.Errorf("beta hostnames = %d, want 0", len(beta.Hostnames))
	}
}

// --- audit-event coverage (issue #291 / PR-C audit parity) ----------------
//
// The handlers in handlers_tenant_surfaces.go emit
// tenant_surface.added / tenant_surface.removed /
// tenant_hostname.added / tenant_hostname.removed. The dns_poller
// emits tenant_hostname.verified. These tests pin the audit row
// shape so a future refactor that drops the emit (or breaks the
// payload) fires a red gate, not silent audit-trail loss.
//
// Mirrors TestAuditEvents_DomainAddedEmitsEvent at
// handlers_audit_test.go:1261.

// TestAuditEvents_TenantSurfaceAddedEmitsEvent is the surface-add
// counterpart. Drives POST /v1/apps/{slug}/tenant-surfaces and
// asserts the auditor records tenant_surface.added with surface_id,
// app_id, name, cert_kind, hostnames[].
func TestAuditEvents_TenantSurfaceAddedEmitsEvent(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "audit-surf-add")

	req := makeTenantSurfaceReq(appID, "audit-zone", "api.audit-a.com", "api.audit-b.com")
	rec := e.do(t, "POST", "/v1/apps/audit-surf-add/tenant-surfaces", req, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.TenantSurfaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "tenant_surface.added")
	found = mustAuditEvent(t, found, fmt.Sprintf("no tenant_surface.added event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["surface_id"] != resp.ID {
		t.Errorf("Data.surface_id = %v, want %s", data["surface_id"], resp.ID)
	}
	if data["app_id"] != appID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], appID)
	}
	if data["name"] != "audit-zone" {
		t.Errorf("Data.name = %v, want audit-zone", data["name"])
	}
	if data["cert_kind"] != string(state.CertKindPerHostSAN) {
		t.Errorf("Data.cert_kind = %v, want per_host_san", data["cert_kind"])
	}
	hostnames, ok := data["hostnames"].([]any)
	if !ok || len(hostnames) != 2 {
		t.Errorf("Data.hostnames = %v, want 2 entries", data["hostnames"])
	}
}

// TestAuditEvents_TenantSurfaceRemovedEmitsEvent is the surface-delete
// counterpart. Drives DELETE /v1/apps/{slug}/tenant-surfaces/{id}
// and asserts tenant_surface.removed with surface_id, app_id, name.
func TestAuditEvents_TenantSurfaceRemovedEmitsEvent(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "audit-surf-rm")
	surfID := seedTenantSurface(t, e, appID, "audit-rm-target")

	rec := e.do(t, "DELETE", "/v1/apps/audit-surf-rm/tenant-surfaces/"+surfID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "tenant_surface.removed")
	found = mustAuditEvent(t, found, fmt.Sprintf("no tenant_surface.removed event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["surface_id"] != surfID {
		t.Errorf("Data.surface_id = %v, want %s", data["surface_id"], surfID)
	}
	if data["app_id"] != appID {
		t.Errorf("Data.app_id = %v, want %s", data["app_id"], appID)
	}
	if data["name"] != "audit-rm-target" {
		t.Errorf("Data.name = %v, want audit-rm-target", data["name"])
	}
}

// TestAuditEvents_TenantHostnameAddedEmitsEvent pins the add-hostname
// audit row. Symmetric to the add/delete surface tests; focuses on
// the per-hostname emit rather than the per-surface one.
func TestAuditEvents_TenantHostnameAddedEmitsEvent(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "audit-host-add")
	surfID := seedTenantSurface(t, e, appID, "audit-hostname")

	rec := e.do(t, "POST", "/v1/apps/audit-host-add/tenant-surfaces/"+surfID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.audit-host.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "tenant_hostname.added")
	found = mustAuditEvent(t, found, fmt.Sprintf("no tenant_hostname.added event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["surface_id"] != surfID {
		t.Errorf("Data.surface_id = %v, want %s", data["surface_id"], surfID)
	}
	if data["hostname"] != "api.audit-host.com" {
		t.Errorf("Data.hostname = %v, want api.audit-host.com", data["hostname"])
	}
}

// TestAuditEvents_TenantHostnameRemovedEmitsEvent pins the remove
// emit. Same data shape as added (surface_id + hostname); the
// dashboard timeline reads the pair to render "added at T0, removed
// at T1".
func TestAuditEvents_TenantHostnameRemovedEmitsEvent(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "audit-host-rm")
	surfID := seedTenantSurface(t, e, appID, "rm-host-audit")
	// Add first so the remove has something to remove.
	rec := e.do(t, "POST", "/v1/apps/audit-host-rm/tenant-surfaces/"+surfID+"/hostnames",
		api.AddTenantHostnameRequest{Hostname: "api.audit-rm.com"}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("add status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "DELETE", "/v1/apps/audit-host-rm/tenant-surfaces/"+surfID+"/hostnames/api.audit-rm.com", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "tenant_hostname.removed")
	found = mustAuditEvent(t, found, fmt.Sprintf("no tenant_hostname.removed event row; rows=%+v", rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["surface_id"] != surfID {
		t.Errorf("Data.surface_id = %v, want %s", data["surface_id"], surfID)
	}
	if data["hostname"] != "api.audit-rm.com" {
		t.Errorf("Data.hostname = %v, want api.audit-rm.com", data["hostname"])
	}
}

// --- test helpers scoped to this file ------------------------------------

// newTestServerWithCapturingNotifierWithStore builds an env that
// shares a pre-constructed store (so the IDOR test can plant a
// surface under account A and probe with account B's env). The
// account's API key is generated freshly so the env's auth path
// functions against the same store. Mirrors
// newTestServerWithCapturingNotifier's body verbatim but takes
// the store as input rather than constructing one.
func newTestServerWithCapturingNotifierWithStore(t *testing.T, store *state.MemStore, acct state.Account) (testEnv, *capturingNotifier) {
	t.Helper()
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	notif := &capturingNotifier{}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notif)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: nil}, notif
}

// silence unused-helper lints for symbols not used in this file's
// tests but referenced from the package-level test harness.
var (
	_ = httptest.NewRecorder
	_ = strings.ToLower
)
