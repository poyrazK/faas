package main

// adr: 647
import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
)

func TestGlobalQuotaDeliveryAPITwoRegionsRecoverOriginal(t *testing.T) {
	d := globalDirectoryHarness(t)
	cfg, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	cfg.DeliveryWorker = true
	if err := d.s.configureRuntimeQuotaTransport(t.Context(), cfg, nil); err != nil || !d.s.globalQuotaDeliveryWorker {
		t.Fatal("worker config", err)
	}
	app, err := d.s.globalDirectory.CreateApp(t.Context(), d.acct.ID, "quota-outbox-world", 5)
	if err != nil {
		t.Fatal(err)
	}
	type regional struct {
		env                      testEnv
		appID, consent, delivery string
		bodies                   [][]byte
		mu                       sync.Mutex
	}
	var regions []*regional
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		e := setup(t, api.PlanPro)
		configureCellDeploymentTest(t, e.store, e.s, region)
		local := seedCellDeploymentApp(t, e.store, e.acct, "outbox-local", uuid.NewString(), region)
		e.s.regionalQuotaTrust = trust
		e.h = e.s.handler()
		c := &regional{env: e, appID: local.ID, consent: uuid.NewString(), delivery: uuid.NewString()}
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/delegated") {
				if r.Header.Get("Authorization") != "" {
					t.Error("worker sent admin credential")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				c.mu.Lock()
				c.bodies = append(c.bodies, body)
				first := len(c.bodies) == 1
				c.mu.Unlock()
				if first {
					rec := httptest.NewRecorder()
					e.h.ServeHTTP(rec, r)
					if rec.Code != http.StatusOK {
						t.Errorf("regional acceptance: %d %s", rec.Code, rec.Body)
					}
					http.Error(w, "lost after commit", http.StatusServiceUnavailable)
					return
				}
			}
			e.h.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		e.s.regionalQuotaAPIBase = srv.URL
		d.s.globalVerifier.Client = srv.Client()
		cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: region, APIBase: srv.URL, Enabled: true})
		if _, err = d.s.globalDirectory.AttachTarget(t.Context(), d.acct.ID, app.Slug, globaldirectory.Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalAppID: local.ID, RegionalAppSlug: local.Slug}); err != nil {
			t.Fatal(err)
		}
		d.h = d.s.handler()
		consentPath := "/v1/global-apps/" + app.Slug + "/runtime-consents/" + c.consent
		if r := d.do(t, http.MethodPut, consentPath, api.PutGlobalRuntimeConsentRequest{Region: region, ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond), RegionalAPIKey: e.key}, nil); r.Code != http.StatusOK {
			t.Fatal("consent", r.Code, r.Body)
		}
		regions = append(regions, c)
	}
	allocation := uuid.NewString()
	path := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + allocation
	if r := d.do(t, http.MethodPut, path, api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}, {Region: "eu-hel", Concurrency: 2}}}, nil); r.Code != http.StatusOK {
		t.Fatal("reserve", r.Code, r.Body)
	}
	for i, c := range regions {
		p := path + "/deliveries/" + c.delivery
		req := api.QueueGlobalQuotaDeliveryRequest{Region: []string{"eu-fsn", "eu-hel"}[i], Purpose: "grant", ConsentID: c.consent}
		for range 2 {
			if r := d.do(t, http.MethodPut, p, req, nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"pending"`) || strings.Contains(r.Body.String(), c.env.key) {
				t.Fatal("queue", r.Code, r.Body)
			}
		}
		if r := d.do(t, http.MethodGet, strings.Replace(p, allocation, uuid.NewString(), 1), nil, nil); r.Code != http.StatusNotFound {
			t.Fatal("wrong allocation inspected", r.Code)
		}
	}
	j := d.s.globalDirectory.(globaldirectory.QuotaDeliveryStore)
	w := globaldirectory.QuotaDeliveryWorker{Store: j, Authority: *d.s.globalQuotaAuthority, Verifier: d.s.globalVerifier}
	for range 2 {
		if err = w.Once(t.Context()); !errors.Is(err, globaldirectory.ErrVerification) {
			t.Fatal("lost response", err)
		}
	}
	for _, c := range regions {
		if r := d.do(t, http.MethodPost, path+"/deliveries/"+c.delivery+"/retry", nil, nil); r.Code != http.StatusOK {
			t.Fatal("retry", r.Code, r.Body)
		}
	}
	// A fresh worker has no request-only regional key and retains every original envelope.
	w = globaldirectory.QuotaDeliveryWorker{Store: j, Authority: *d.s.globalQuotaAuthority, Verifier: d.s.globalVerifier}
	for range 2 {
		if err = w.Once(t.Context()); err != nil {
			t.Fatal("recovery", err)
		}
	}
	for _, c := range regions {
		c.mu.Lock()
		same := len(c.bodies) == 2 && string(c.bodies[0]) == string(c.bodies[1])
		c.mu.Unlock()
		if !same {
			t.Fatal("worker changed original body")
		}
		r := d.do(t, http.MethodGet, path+"/deliveries/"+c.delivery, nil, nil)
		var out api.GlobalQuotaDeliveryResponse
		if json.Unmarshal(r.Body.Bytes(), &out) != nil || r.Code != http.StatusOK || out.Status != "accepted" || out.AcceptedAt == nil || out.Attempts != 2 {
			t.Fatal("inspection", r.Code, r.Body)
		}
		head, err := c.env.store.RegionalRuntimeQuotaHead(t.Context(), c.appID)
		if err != nil || head.AdmissionEnabled || head.Grant.Concurrency != 2 {
			t.Fatal("receipt activated runtime", err)
		}
	}
}

func TestGlobalQuotaDeliveryAPIRejectsInvalidIdentityAndCredentials(t *testing.T) {
	d := globalDirectoryHarness(t)
	quotaDirectoryKeys(t, d, uuid.NewString())
	d.h = d.s.handler()
	p := "/v1/global-apps/quota-outbox/runtime-quotas/" + uuid.NewString() + "/deliveries/" + uuid.NewString()
	for _, req := range []any{api.QueueGlobalQuotaDeliveryRequest{Region: "eu-fsn", Purpose: "grant"}, api.QueueGlobalQuotaDeliveryRequest{Region: "eu-fsn", Purpose: "retirement", ConsentID: uuid.NewString()}, map[string]string{"region": "eu-fsn", "purpose": "grant", "runtime_consent_id": uuid.NewString(), "regional_api_key": "never-persist"}} {
		if r := d.do(t, http.MethodPut, p, req, nil); r.Code != http.StatusBadRequest {
			t.Fatal("invalid intent", r.Code, r.Body)
		}
	}
	if r := d.do(t, http.MethodGet, strings.Replace(p, "/deliveries/", "/deliveries/invalid", 1), nil, nil); r.Code != http.StatusBadRequest {
		t.Fatal("invalid identity", r.Code)
	}
	if err := (&server{}).configureRuntimeQuotaTransport(t.Context(), RuntimeQuotaTransportConfig{DeliveryWorker: true}, nil); err == nil {
		t.Fatal("regional worker configured")
	}
}
