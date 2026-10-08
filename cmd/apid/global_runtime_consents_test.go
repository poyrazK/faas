package main

// adr: 774
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func TestGlobalRuntimeConsentAPISetupLostResponseAndOriginalRevoke(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	app, err := d.s.globalDirectory.CreateApp(t.Context(), d.acct.ID, "runtime-consent-world", 5)
	if err != nil {
		t.Fatal(err)
	}
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	local := seedCellDeploymentApp(t, e.store, e.acct, "runtime-consent-local", uuid.NewString(), "eu-fsn")
	e.s.regionalQuotaTrust = trust
	e.h = e.s.handler()
	id := uuid.NewString()
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	j := d.s.globalDirectory.(globaldirectory.RuntimeConsentStore)
	puts, deletes := 0, 0
	var deliveries atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/delegated") {
			deliveries.Add(1)
		}
		if strings.Contains(r.URL.Path, "/runtime-consents/") {
			stored, err := j.RuntimeConsent(r.Context(), d.acct.ID, app.Slug, id)
			if err != nil {
				t.Error("intent not committed", err)
			}
			if r.Header.Get("Authorization") != "Bearer "+e.key {
				t.Error("wrong regional admin principal")
			}
			if r.Method == http.MethodPut {
				puts++
				if stored.Status != globaldirectory.RuntimeConsentPending && stored.Status != globaldirectory.RuntimeConsentActive {
					t.Error("wrong setup intent")
				}
			}
			if r.Method == http.MethodDelete {
				deletes++
				if stored.Status != globaldirectory.RuntimeConsentRevoking {
					t.Error("revocation intent not persisted")
				}
			}
			if (r.Method == http.MethodPut && puts == 1) || (r.Method == http.MethodDelete && deletes == 1) {
				rec := httptest.NewRecorder()
				e.h.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK {
					t.Errorf("regional operation rejected: %d %s", rec.Code, rec.Body)
				}
				http.Error(w, "lost response", http.StatusServiceUnavailable)
				return
			}
		}
		e.h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	e.s.regionalQuotaAPIBase = srv.URL
	d.s.globalVerifier.Client = srv.Client()
	cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: "eu-fsn", APIBase: srv.URL, Enabled: true})
	b := globaldirectory.Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalAppID: local.ID, RegionalAppSlug: local.Slug}
	if _, err := d.s.globalDirectory.AttachTarget(t.Context(), d.acct.ID, app.Slug, b); err != nil {
		t.Fatal(err)
	}
	d.h = d.s.handler()
	path := "/v1/global-apps/" + app.Slug + "/runtime-consents/" + id
	allocation := uuid.NewString()
	quotaPath := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + allocation
	if r := d.do(t, http.MethodPut, quotaPath, api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}}}, nil); r.Code != http.StatusOK {
		t.Fatal("reserve", r.Code, r.Body)
	}
	delivery := map[string]string{"runtime_consent_id": id}
	req := api.PutGlobalRuntimeConsentRequest{Region: "eu-fsn", ExpiresAt: expiry, RegionalAPIKey: e.key}
	if r := d.do(t, http.MethodPut, path, req, nil); r.Code != http.StatusBadGateway {
		t.Fatal("lost setup response", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodGet, path, nil, nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"pending"`) || strings.Contains(r.Body.String(), e.key) {
		t.Fatal("pending inspection", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodPost, quotaPath+"/deliver/eu-fsn", delivery, nil); r.Code != http.StatusBadGateway || deliveries.Load() != 0 {
		t.Fatal("pending consent dispatched", r.Code, r.Body)
	}
	reserved, err := d.s.globalQuotaAuthority.Store.RuntimeQuota(t.Context(), d.acct.ID, app.Slug, allocation)
	if err != nil || len(reserved.Targets) != 1 || reserved.Targets[0].IssuedAt != nil {
		t.Fatal("pending consent issued grant authority", err)
	}
	if r := d.do(t, http.MethodPut, path, req, nil); r.Code != http.StatusOK {
		t.Fatal("stable setup retry", r.Code, r.Body)
	}
	retained, err := j.CheckRuntimeConsent(t.Context(), d.acct.ID, app.Slug, id)
	if err != nil || retained.Consent.ID != id || retained.Consent.ExpiresAtMicros != expiry.UnixMicro() {
		t.Fatal("retained active consent", err)
	}
	if r := d.do(t, http.MethodPost, quotaPath+"/deliver/eu-fsn", delivery, nil); r.Code != http.StatusOK || deliveries.Load() != 1 {
		t.Fatal("setup did not authorize credential-free delivery", r.Code, r.Body)
	}
	if _, err := j.BeginRevokeRuntimeConsent(t.Context(), d.acct.ID, app.Slug, id); err != nil {
		t.Fatal(err)
	}
	if r := d.do(t, http.MethodPost, quotaPath+"/deliver/eu-fsn", delivery, nil); r.Code != http.StatusBadGateway || deliveries.Load() != 1 {
		t.Fatal("revoking consent dispatched before regional tombstone", r.Code, r.Body)
	}
	raw, _ := json.Marshal(req)
	ambiguous := strings.Replace(string(raw), `"region":`, `"region":"eu-hel","region":`, 1)
	if r := d.do(t, http.MethodPut, path, json.RawMessage(ambiguous), nil); r.Code != http.StatusBadRequest || puts != 2 {
		t.Fatal("duplicate input dispatched", r.Code)
	}
	if err := d.s.globalDirectory.DetachTarget(t.Context(), d.acct.ID, app.Slug, "eu-fsn"); err != nil {
		t.Fatal(err)
	}
	revoke := api.RevokeGlobalRuntimeConsentRequest{RegionalAPIKey: e.key}
	if r := d.do(t, http.MethodDelete, path, revoke, nil); r.Code != http.StatusBadGateway {
		t.Fatal("lost revoke response", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodDelete, path, revoke, nil); r.Code != http.StatusOK {
		t.Fatal("original endpoint revoke retry", r.Code, r.Body)
	}
	if r := d.do(t, http.MethodGet, path, nil, nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"revoked"`) {
		t.Fatal("retained revoke inspection", r.Code, r.Body)
	}
	head, err := e.store.RegionalRuntimeQuotaHead(t.Context(), local.ID)
	if err != nil || head.AdmissionEnabled {
		t.Fatal("consent setup activated runtime", err)
	}
}
