package main

// adr: 786
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalIngressStatusAcceptedStreamRemainsFrozen(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	app := seedCellDeploymentApp(t, e.store, e.acct, "status-local", uuid.NewString(), "eu-fsn")
	e.s.regionalQuotaTrust, e.s.regionalQuotaAPIBase, e.s.domain = trust, "https://fsn.example.com", "fsn.apps.example.com"
	e.h = e.s.handler()
	policy, ok := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	if !ok {
		t.Fatal("missing plan")
	}
	g := runtimequota.Grant{IssuerID: d.s.globalQuotaAuthority.Signer.IssuerID(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Policy: policy, Concurrency: 1, RolloutConcurrency: api.RolloutConcurrencyGrant, IssuedAtMicros: time.Now().UnixMicro(), Binding: runtimequota.Binding{CellID: e.s.controlPlaneCell.ID, Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: e.s.regionalQuotaAPIBase}}
	c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: g.IssuerID, GlobalAppID: g.GlobalAppID, OwnerID: g.OwnerID, Binding: g.Binding, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	if _, err := e.store.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	envelope, err := d.s.globalQuotaAuthority.Signer.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	authority := state.RegionalRuntimeQuotaAuthority{Store: e.store, Directories: trust, Binding: g.Binding}
	if _, err = authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p := runtimequota.ServingProbe{Nonce: uuid.NewString(), ConsentID: c.ID, Grant: g, Origin: api.RegionalAppOrigin(app.Slug, e.s.domain), IssuedAtMicros: now.UnixMicro(), ExpiresAtMicros: now.Add(api.GlobalIngressLeaseDuration).UnixMicro()}
	request := func(probe runtimequota.ServingProbe) *httptest.ResponseRecorder {
		signed, err := d.s.globalQuotaAuthority.Signer.SignServingProbe(probe)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(signed)
		r := httptest.NewRequest(http.MethodPost, "/internal/v1/ingress-status", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}
	w := request(p)
	if w.Code != http.StatusOK {
		t.Fatalf("observation: %d %s", w.Code, w.Body)
	}
	status, err := runtimequota.DecodeServingStatus(w.Body.Bytes(), p)
	if err != nil || status.Eligible || status.Healthy || status.Code != "admission_frozen" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("receipt became serving authority", status, err)
	}
	for _, kind := range []string{"origin", "expiry", "stream", "consent"} {
		t.Run(kind, func(t *testing.T) {
			wrong := p
			switch kind {
			case "origin":
				wrong.Origin = "https://status-local.replacement.example.com"
			case "expiry":
				wrong.IssuedAtMicros = time.Now().Add(-time.Hour).UnixMicro()
				wrong.ExpiresAtMicros = wrong.IssuedAtMicros + api.GlobalIngressLeaseDuration.Microseconds()
			case "stream":
				wrong.Grant.AllocationID = uuid.NewString()
			case "consent":
				wrong.ConsentID = uuid.NewString()
			}
			if w := request(wrong); w.Code == http.StatusOK {
				t.Fatal("substituted status accepted", w.Body)
			}
		})
	}
	if _, err = e.store.RevokeRegionalRuntimeConsent(t.Context(), e.acct.ID, g.Binding.CellID, app.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if w := request(p); w.Code == http.StatusOK {
		t.Fatal("revoked consent observed as current")
	}
	receipt, err := e.store.RegionalRuntimeQuota(t.Context(), app.ID, g.AllocationID)
	if err != nil || receipt.Retirement != nil {
		t.Fatal("observation altered quota evidence", err)
	}
}

func TestGlobalIngressPublisherConfigurationScope(t *testing.T) {
	e := globalDirectoryHarness(t)
	path := filepath.Join(t.TempDir(), "global-routes.json")
	cfg := GlobalIngressPublisherConfig{ManifestPath: path}
	if err := e.s.configureGlobalIngressPublisher(cfg); err == nil {
		t.Fatal("publisher without signing identity")
	}
	quotaDirectoryKeys(t, e, uuid.NewString())
	if err := e.s.configureGlobalIngressPublisher(cfg); err != nil {
		t.Fatal(err)
	}
	if e.s.globalIngressManifest != path {
		t.Fatal("configuration discarded")
	}
	if err := e.s.configureGlobalIngressPublisher(GlobalIngressPublisherConfig{ManifestPath: "relative.json"}); err == nil {
		t.Fatal("relative handoff accepted")
	}
	e.s.controlPlaneCell.ID = uuid.NewString()
	if err := e.s.configureGlobalIngressPublisher(cfg); err == nil {
		t.Fatal("regional API became publisher")
	}
}
