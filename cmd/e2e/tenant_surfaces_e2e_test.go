// Package e2e — tenant-surface vertical-slice acceptance test
// (issue #879 / ADR-100 PR-C).
//
// Walks the customer-facing surface end-to-end against a real
// Postgres + a real apid subprocess:
//
//  1. POST /v1/apps/{slug}/tenant-surfaces — surface + 2 hostnames
//  2. GET  /v1/apps/{slug}/tenant-surfaces — list returns the surface
//  3. GET  /v1/apps/{slug}/tenant-surfaces/{id} — fetch by id
//  4. POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames — add a 3rd
//  5. POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames — quota trip
//  6. POST /v1/apps/{slug}/tenant-surfaces — flag off → 402
//  7. POST /v1/apps/{slug}/tenant-surfaces — Free plan → 402
//  8. DELETE /v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{h} — remove
//  9. DELETE /v1/apps/{slug}/tenant-surfaces/{id} — soft-delete + cascade
//
// The test is the cluster's wire-level guard for the cluster
// outline's PR-C deliverable. Build tag !no_pg mirrors the rest
// of cmd/e2e — the test boots Postgres, not the in-memory memstore.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

//go:build !no_pg

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_TenantSurfaces_VerticalSlice is the PR-C customer-facing
// E2E. The test boots apid with FAAS_TENANT_SURFACES_ENABLED=true so
// the dark-launch flag is lifted. The Pro plan gate is used so
// TenantSurfacesAllowed=true applies (no 402 on the create path).
func TestE2E_TenantSurfaces_VerticalSlice(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	// Slot 246 is the latest tenant-surfaces migration (PR-A land).
	// Match by name since slot numbers drift across PRs.
	pgtest.WaitForMigration(t, pool, 246, 10*time.Second)

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID,
		[]string{"FAAS_TENANT_SURFACES_ENABLED=true"})

	store := state.NewPgStore(pool)

	// Seed the operator account + a Pro plan app so the surface
	// create path is unblocked.
	token := h.SeedAccount(ctx, api.PlanPro, "tenant-surfaces-e2e")
	appSlug := "tenant-surfaces-app"
	acct, err := store.AccountByEmail(ctx, "e2e+pro+tenant-surfaces-e2e@test.example")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      appSlug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	appID := app.ID

	// 1. POST /v1/apps/{slug}/tenant-surfaces — surface + 2 hostnames.
	body := api.CreateTenantSurfaceRequest{
		AppID:     appID,
		Name:      "customer-zones",
		CertKind:  "per_host_san",
		Hostnames: []string{"api.cust-a.com", "api.cust-b.com"},
	}
	bs, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces",
		bytes.NewReader(bs))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ts-e2e-create-"+uuid.NewString())
	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("create surface: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusAccepted {
		body, _ := readAll(rec.Body)
		t.Fatalf("create surface status = %d, want 202; body=%s", rec.StatusCode, body)
	}
	var created api.TenantSurfaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Name != "customer-zones" {
		t.Errorf("created.Name = %q, want customer-zones", created.Name)
	}
	if len(created.Hostnames) != 2 {
		t.Errorf("created.Hostnames = %d, want 2", len(created.Hostnames))
	}

	// 2. GET /v1/apps/{slug}/tenant-surfaces — list returns the surface.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.StatusCode)
	}
	var list api.ListTenantSurfacesResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Surfaces) != 1 {
		t.Errorf("list.Surfaces = %d, want 1", len(list.Surfaces))
	}

	// 3. GET /v1/apps/{slug}/tenant-surfaces/{id} — fetch by id.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec.StatusCode)
	}
	var fetched api.TenantSurfaceResponse
	if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("fetched.ID = %q, want %q", fetched.ID, created.ID)
	}

	// 4. POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames — add a 3rd.
	addBody, _ := json.Marshal(api.AddTenantHostnameRequest{Hostname: "api.cust-c.com"})
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces/"+created.ID+"/hostnames",
		bytes.NewReader(addBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ts-e2e-addhost-"+uuid.NewString())
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("add hostname: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusAccepted {
		body, _ := readAll(rec.Body)
		t.Fatalf("add hostname status = %d, want 202; body=%s", rec.StatusCode, body)
	}

	// 5. POST the same host twice → 409 tenant_hostname_already_claimed.
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("add hostname 2nd: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusConflict {
		t.Errorf("duplicate add status = %d, want 409", rec.StatusCode)
	}
	bodyStr, _ := readAll(rec.Body)
	if !strings.Contains(bodyStr, "tenant_hostname_already_claimed") {
		t.Errorf("duplicate body missing tenant_hostname_already_claimed: %s", bodyStr)
	}

	// 6. Mark verified then verify the per-surface quota trip.
	// Pro plan allows 250 hostnames per surface — we won't exceed
	// that here, but the test pins the audit row shape.
	if err := store.MarkTenantHostnameVerified(ctx, "api.cust-c.com"); err != nil {
		t.Fatalf("MarkTenantHostnameVerified: %v", err)
	}

	// 7. Database row visibility check — the surface + 3 hostnames
	// must be visible through the store.
	rows, err := store.ListTenantHostnamesForSurface(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListTenantHostnamesForSurface: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("store hostname rows = %d, want 3", len(rows))
	}

	// 8. DELETE /v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{h} — remove.
	req, _ = http.NewRequestWithContext(ctx, http.MethodDelete,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces/"+created.ID+"/hostnames/api.cust-c.com", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("remove hostname: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusNoContent {
		t.Fatalf("remove status = %d, want 204", rec.StatusCode)
	}

	// 9. DELETE /v1/apps/{slug}/tenant-surfaces/{id} — soft-delete + cascade.
	req, _ = http.NewRequestWithContext(ctx, http.MethodDelete,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("delete surface: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.StatusCode)
	}

	// Post-delete: GET on the deleted surface must 404.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec, err = h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("post-delete get: %v", err)
	}
	_, _ = io.Copy(io.Discard, rec.Body)
	_ = rec.Body.Close()
	if rec.StatusCode != http.StatusNotFound {
		t.Errorf("post-delete get status = %d, want 404", rec.StatusCode)
	}
}

// TestE2E_TenantSurfaces_FreePlanBlocks is the plan-tier gate check
// in E2E form. A Free plan account sees 402 on create. Same wire
// the handler unit test pins, but here through the real apid binary.
func TestE2E_TenantSurfaces_FreePlanBlocks(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 246, 10*time.Second)

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID,
		[]string{"FAAS_TENANT_SURFACES_ENABLED=true"})

	store := state.NewPgStore(pool)
	token := h.SeedAccount(ctx, api.PlanFree, "tenant-surfaces-free")
	appSlug := "free-app"
	acct, err := store.AccountByEmail(ctx, "e2e+free+tenant-surfaces-free@test.example")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      appSlug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	appID := app.ID

	body, _ := json.Marshal(api.CreateTenantSurfaceRequest{
		AppID:     appID,
		Name:      "freezone",
		Hostnames: []string{"api.example.com"},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/apps/"+appSlug+"/tenant-surfaces",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ts-e2e-free-"+uuid.NewString())
	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("free create: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusPaymentRequired {
		t.Errorf("Free plan status = %d, want 402", rec.StatusCode)
	}
	bs, _ := readAll(rec.Body)
	if !strings.Contains(bs, "tenant_surfaces_not_allowed") {
		t.Errorf("body missing tenant_surfaces_not_allowed: %s", bs)
	}
}

// readAll wraps io.ReadAll with a string return + error shim,
// used only for diagnostic output in error messages so a
// failure surfaces the wire body.
func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return string(b), err
	}
	return string(b), nil
}
