//go:build !no_pg

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRuntimeQuotaTransportPostgres(t *testing.T) {
	d := setupPGHandler(t, api.PlanPro)
	if err := d.store.MarkAccountEmailVerified(t.Context(), d.acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.s.configureGlobalDirectory(t.Context(), true, d.pool); err != nil {
		t.Fatal(err)
	}
	cfg, _ := quotaDirectoryKeys(t, testEnv{s: d.s}, uuid.NewString())
	app, err := d.s.globalDirectory.CreateApp(t.Context(), d.acct.ID, "quota-pg-world", 5)
	if err != nil {
		t.Fatal(err)
	}
	e := cellDeploymentPGHarness(t)
	local := seedCellDeploymentApp(t, e.store, e.acct, "quota-pg-local", uuid.NewString(), "eu-fsn")
	if local.OrgID == "" {
		t.Fatal("fixture must exercise a real personal organization")
	}
	srv := httptest.NewTLSServer(e.h)
	t.Cleanup(srv.Close)
	regionalConfig := RuntimeQuotaTransportConfig{TrustBundlePath: cfg.TrustBundlePath, RegionalAPIBase: srv.URL}
	if err := e.s.configureRuntimeQuotaTransport(t.Context(), regionalConfig, e.pool); err != nil {
		t.Fatal(err)
	}
	// Configured environment strings cannot replace the durable database cell.
	wrong := &server{controlPlaneCell: e.s.controlPlaneCell}
	wrong.controlPlaneCell.ID = uuid.NewString()
	if err := wrong.configureRuntimeQuotaTransport(t.Context(), regionalConfig, e.pool); err == nil {
		t.Fatal("wrong database cell configured")
	}
	d.s.globalVerifier.Client = srv.Client()
	registerDirectoryCell(t, testEnv{s: d.s}, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: "eu-fsn", APIBase: srv.URL, Enabled: true})
	d.h = d.s.handler()
	if r := d.do(t, http.MethodPut, "/v1/global-apps/"+app.Slug+"/targets/eu-fsn", api.AttachGlobalAppTargetRequest{RegionalAppSlug: local.Slug, RegionalAPIKey: e.key}, nil); r.Code != http.StatusOK {
		t.Fatalf("attach via regional proof: %d %s", r.Code, r.Body)
	}
	id := uuid.NewString()
	path := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + id
	if r := d.do(t, http.MethodPut, path, api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}}}, nil); r.Code != http.StatusOK {
		t.Fatalf("reserve: %d %s", r.Code, r.Body)
	}
	for range 2 {
		if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", map[string]string{"regional_api_key": e.key}, nil); r.Code != http.StatusOK {
			t.Fatalf("deliver: %d %s", r.Code, r.Body)
		}
		if err := d.s.configureRuntimeQuotaTransport(t.Context(), cfg, d.pool); err != nil {
			t.Fatal(err)
		}
	}
	journal, ok := e.store.(state.RegionalRuntimeQuotaStore)
	if !ok {
		t.Fatal("regional quota journal missing")
	}
	stored, err := journal.RegionalRuntimeQuota(t.Context(), local.ID, id)
	if err != nil || stored.GrantEnvelope == nil || stored.Grant.Binding.RegionalOrgID != "" {
		t.Fatal("missing durable receipt", err)
	}
	head, err := e.store.RegionalRuntimeQuotaHead(t.Context(), local.ID)
	if err != nil || head.AdmissionEnabled {
		t.Fatal("receipt activated head", err)
	}
	if r := d.do(t, http.MethodDelete, path, nil, nil); r.Code != http.StatusOK {
		t.Fatalf("retire: %d %s", r.Code, r.Body)
	}
	if err := e.store.DeleteApp(t.Context(), local.ID); err != nil {
		t.Fatal(err)
	}
	if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", map[string]string{}, nil); r.Code != http.StatusOK {
		t.Fatalf("retirement: %d %s", r.Code, r.Body)
	}
	retired, err := journal.RegionalRuntimeQuota(t.Context(), local.ID, id)
	if err != nil || retired.RetirementEnvelope == nil || *retired.GrantEnvelope != *stored.GrantEnvelope {
		t.Fatal("retirement changed original authority", err)
	}
	a, err := d.s.globalDirectory.(globaldirectory.RuntimeQuotaStore).RuntimeQuota(t.Context(), d.acct.ID, app.Slug, id)
	if err != nil || a.Targets[0].ReleasedAt != nil {
		t.Fatal("receipt released charge", err)
	}
}
