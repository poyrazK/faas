package main

// adr: 773
import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestRuntimeConsentRequiresAdminAndOriginalRegionalScope(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	app := seedCellDeploymentApp(t, e.store, e.acct, "consent-scope", uuid.NewString(), "eu-fsn")
	e.s.regionalQuotaTrust, e.s.regionalQuotaAPIBase = trust, "https://fsn.example.com"
	e.h = e.s.handler()
	c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: d.s.globalQuotaAuthority.Signer.IssuerID(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(),
		Binding: runtimequota.Binding{CellID: e.s.controlPlaneCell.ID, Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: e.s.regionalQuotaAPIBase}, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	path := "/v1/cells/" + c.Binding.CellID + "/apps/" + app.ID + "/runtime-consents/" + c.ID
	request := httptest.NewRequest(http.MethodPut, path, nil)
	recorder := httptest.NewRecorder()
	e.h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing admin authentication: %d", recorder.Code)
	}
	for _, change := range []func(*runtimequota.Consent){
		func(c *runtimequota.Consent) { c.IssuerID = uuid.NewString() },
		func(c *runtimequota.Consent) { c.Binding.RegionalOrgID = uuid.NewString() },
		func(c *runtimequota.Consent) { c.Binding.RegionalAccountID = uuid.NewString() },
		func(c *runtimequota.Consent) { c.Binding.Region = "eu-hel" },
		func(c *runtimequota.Consent) { c.Binding.APIBase = "https://replacement.example.com" },
	} {
		wrong := c
		change(&wrong)
		if r := e.do(t, http.MethodPut, path, wrong, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("wrong regional consent accepted: %d %s", r.Code, r.Body)
		}
	}
	if _, err := e.store.RegionalRuntimeConsent(t.Context(), e.acct.ID, c.Binding.CellID, app.ID, c.ID); err == nil {
		t.Fatal("rejected consent changed state")
	}
}

func TestRuntimeConsentDeliveryWithoutRegionalCredential(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	app, err := d.s.globalDirectory.CreateApp(t.Context(), d.acct.ID, "consent-world", 5)
	if err != nil {
		t.Fatal(err)
	}
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	local := seedCellDeploymentApp(t, e.store, e.acct, "consent-local", uuid.NewString(), "eu-fsn")
	e.s.regionalQuotaTrust = trust
	e.h = e.s.handler()
	lost := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/delegated") {
			if r.Header.Get("Authorization") != "" {
				t.Error("delegated delivery sent a customer credential")
			}
			if !lost {
				lost = true
				recorder := httptest.NewRecorder()
				e.h.ServeHTTP(recorder, r)
				if recorder.Code != http.StatusOK {
					t.Errorf("regional consent delivery: %d %s", recorder.Code, recorder.Body)
				}
				http.Error(w, "lost response", http.StatusServiceUnavailable)
				return
			}
		}
		e.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.s.regionalQuotaAPIBase = srv.URL
	d.s.globalVerifier.Client = srv.Client()
	cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: "eu-fsn", APIBase: srv.URL, Enabled: true})
	b := globaldirectory.Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalAppID: local.ID, RegionalAppSlug: local.Slug}
	if _, err := d.s.globalDirectory.AttachTarget(t.Context(), d.acct.ID, app.Slug, b); err != nil {
		t.Fatal(err)
	}
	c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: d.s.globalQuotaAuthority.Signer.IssuerID(), GlobalAppID: app.ID, OwnerID: quotaLocalUUID(app.OwnerID),
		Binding: runtimequota.Binding{CellID: b.CellID, Region: b.Region, CellGeneration: b.CellGeneration, RegionalAccountID: b.RegionalAccountID, RegionalAppID: b.RegionalAppID, RegionalAppSlug: b.RegionalAppSlug, APIBase: srv.URL}, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	consentPath := "/v1/cells/" + cell.ID + "/apps/" + local.ID + "/runtime-consents/" + c.ID
	d.h = d.s.handler()
	directoryConsentPath := "/v1/global-apps/" + app.Slug + "/runtime-consents/" + c.ID
	for range 2 {
		if r := d.do(t, http.MethodPut, directoryConsentPath, api.PutGlobalRuntimeConsentRequest{Region: b.Region, ExpiresAt: time.UnixMicro(c.ExpiresAtMicros), RegionalAPIKey: e.key}, nil); r.Code != http.StatusOK {
			t.Fatalf("retained consent: %d %s", r.Code, r.Body)
		}
	}
	id := uuid.NewString()
	path := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + id
	if r := d.do(t, http.MethodPut, path, api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}}}, nil); r.Code != http.StatusOK {
		t.Fatalf("reserve: %d %s", r.Code, r.Body)
	}
	body := map[string]string{"runtime_consent_id": c.ID}
	if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", body, nil); r.Code != http.StatusBadGateway {
		t.Fatalf("lost response: %d %s", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", body, nil); r.Code != http.StatusOK {
		t.Fatalf("credential-free retry: %d %s", r.Code, r.Body)
	}
	receipt, err := e.store.RegionalRuntimeQuota(t.Context(), local.ID, id)
	if err != nil || receipt.ConsentID != c.ID {
		t.Fatal("missing original consent provenance", err)
	}
	if r := e.do(t, http.MethodDelete, consentPath, nil, nil); r.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", body, nil); r.Code != http.StatusBadGateway {
		t.Fatalf("revoked delegated retry accepted: %d %s", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodDelete, path, nil, nil); r.Code != http.StatusOK {
		t.Fatalf("retire: %d %s", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodPost, path+"/deliver/eu-fsn", body, nil); r.Code != http.StatusOK {
		t.Fatalf("revocation blocked retirement: %d %s", r.Code, r.Body)
	}
	head, err := e.store.RegionalRuntimeQuotaHead(t.Context(), local.ID)
	if err != nil || head.AdmissionEnabled {
		t.Fatal("delivery consent activated capacity", err)
	}
}
