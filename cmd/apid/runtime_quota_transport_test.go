package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func quotaDirectoryKeys(t *testing.T, e testEnv, issuer string) (RuntimeQuotaTransportConfig, runtimequota.Trust) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "directory")
	if _, err := runtimequota.ProvisionAuthorityKeys(dir, runtimequota.Directory, issuer); err != nil {
		t.Fatal(err)
	}
	cfg := RuntimeQuotaTransportConfig{DirectoryIssuerID: issuer, PrivateKeyPath: filepath.Join(dir, runtimequota.PrivateKeyFilename), TrustBundlePath: filepath.Join(dir, runtimequota.TrustFilename)}
	if err := e.s.configureRuntimeQuotaTransport(t.Context(), cfg, nil); err != nil {
		t.Fatal(err)
	}
	keys, err := runtimequota.LoadAuthorityKeys(runtimequota.KeyConfig{PrivateKeyPath: cfg.PrivateKeyPath, TrustBundlePath: cfg.TrustBundlePath}, runtimequota.Directory, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, keys.Trust
}

func TestRuntimeQuotaTransportTwoCellsLostResponseAndRetirement(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	app, err := d.s.globalDirectory.CreateApp(t.Context(), d.acct.ID, "quota-world", 5)
	if err != nil {
		t.Fatal(err)
	}
	type cellFixture struct {
		env     testEnv
		app     state.App
		binding runtimequota.Binding
		lost    bool
	}
	cells := make([]*cellFixture, 0, 2)
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		e := setup(t, api.PlanPro)
		configureCellDeploymentTest(t, e.store, e.s, region)
		local := seedCellDeploymentApp(t, e.store, e.acct, "quota-local", uuid.NewString(), region)
		c := &cellFixture{env: e, app: local}
		e.s.regionalQuotaTrust = trust
		e.h = e.s.handler()
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/retirement") && r.Header.Get("Authorization") != "" {
				t.Error("retirement sent a regional credential")
			}
			if r.Method == http.MethodPut && !strings.HasSuffix(r.URL.Path, "/retirement") && !c.lost {
				c.lost = true
				recorder := httptest.NewRecorder()
				e.h.ServeHTTP(recorder, r)
				if recorder.Code != http.StatusOK {
					t.Errorf("regional acceptance: %d %s", recorder.Code, recorder.Body)
				}
				http.Error(w, e.key, http.StatusServiceUnavailable) // Remote bodies must never escape diagnostics.
				return
			}
			e.h.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		e.s.regionalQuotaAPIBase = srv.URL
		d.s.globalVerifier.Client = srv.Client()
		cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: e.s.controlPlaneCell.ID, Region: region, APIBase: srv.URL, Enabled: true})
		b := globaldirectory.Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalOrgID: quotaLocalUUID(local.OrgID), RegionalAppID: local.ID, RegionalAppSlug: local.Slug}
		if _, err := d.s.globalDirectory.AttachTarget(t.Context(), d.acct.ID, app.Slug, b); err != nil {
			t.Fatal(err)
		}
		c.binding = runtimequota.Binding{CellID: b.CellID, Region: b.Region, CellGeneration: b.CellGeneration, RegionalAccountID: b.RegionalAccountID, RegionalOrgID: b.RegionalOrgID, RegionalAppID: b.RegionalAppID, RegionalAppSlug: b.RegionalAppSlug, APIBase: srv.URL}
		c.env = e
		cells = append(cells, c)
	}
	d.h = d.s.handler()
	id := uuid.NewString()
	path := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + id
	if r := d.do(t, http.MethodPut, path, api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}, {Region: "eu-hel", Concurrency: 2}}}, nil); r.Code != http.StatusOK {
		t.Fatalf("reserve: %d %s", r.Code, r.Body)
	}
	for _, c := range cells {
		deliver := path + "/deliver/" + c.binding.Region
		first := d.do(t, http.MethodPost, deliver, map[string]string{"regional_api_key": c.env.key}, nil)
		if first.Code != http.StatusBadGateway || strings.Contains(first.Body.String(), c.env.key) {
			t.Fatalf("uncertain delivery: %d %s", first.Code, first.Body)
		}
		stored, err := c.env.store.RegionalRuntimeQuota(t.Context(), c.app.ID, id)
		if err != nil || stored.GrantEnvelope == nil {
			t.Fatalf("commit lost: %+v %v", stored, err)
		}
		second := d.do(t, http.MethodPost, deliver, map[string]string{"regional_api_key": c.env.key}, nil)
		if second.Code != http.StatusOK {
			t.Fatalf("retry: %d %s", second.Code, second.Body)
		}
		proof, err := runtimequota.DecodeDeliveryReceipt(second.Body.Bytes())
		if err != nil || proof.Grant == nil || *proof.Grant != *stored.GrantEnvelope {
			t.Fatal("retry replaced original evidence", err)
		}
		head, err := c.env.store.RegionalRuntimeQuotaHead(t.Context(), c.app.ID)
		if err != nil || head.AdmissionEnabled || head.Grant.Concurrency != 2 {
			t.Fatalf("activated or multiplied quota: %+v %v", head, err)
		}
	}
	// Restart with an overlapping new signing key. Both cells retain their
	// original old-key grant; the directory verifies that original response.
	oldSigner := d.s.globalQuotaAuthority.Signer
	oldAnchor, err := oldSigner.PublicAnchor(runtimequota.Directory)
	if err != nil {
		t.Fatal(err)
	}
	keyDir := filepath.Join(t.TempDir(), "rotation")
	newAnchor, err := runtimequota.ProvisionAuthorityKeys(keyDir, runtimequota.Directory, oldSigner.IssuerID())
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "overlap.json")
	if err := runtimequota.WriteTrustBundle(trustPath, []runtimequota.Anchor{oldAnchor, newAnchor}); err != nil {
		t.Fatal(err)
	}
	if err := d.s.configureRuntimeQuotaTransport(t.Context(), RuntimeQuotaTransportConfig{DirectoryIssuerID: oldSigner.IssuerID(), PrivateKeyPath: filepath.Join(keyDir, runtimequota.PrivateKeyFilename), TrustBundlePath: trustPath}, nil); err != nil {
		t.Fatal(err)
	}
	overlap, err := runtimequota.NewTrust([]runtimequota.Anchor{oldAnchor, newAnchor})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cells {
		c.env.s.regionalQuotaTrust = overlap
		if r := d.do(t, http.MethodPost, path+"/deliver/"+c.binding.Region, map[string]string{"regional_api_key": c.env.key}, nil); r.Code != http.StatusOK {
			t.Fatalf("rotated retry: %d %s", r.Code, r.Body)
		}
		stored, err := c.env.store.RegionalRuntimeQuota(t.Context(), c.app.ID, id)
		if err != nil || stored.GrantEnvelope.KeyID != oldSigner.KeyID() {
			t.Fatal("rotation replaced accepted evidence", err)
		}
	}
	if r := d.do(t, http.MethodDelete, path, nil, nil); r.Code != http.StatusOK {
		t.Fatalf("retire: %d %s", r.Code, r.Body)
	}
	for _, c := range cells {
		if err := c.env.store.DeleteApp(t.Context(), c.app.ID); err != nil {
			t.Fatal(err)
		}
		if r := d.do(t, http.MethodPost, path+"/deliver/"+c.binding.Region, map[string]string{}, nil); r.Code != http.StatusOK {
			t.Fatalf("retirement after deletion: %d %s", r.Code, r.Body)
		}
		stored, err := c.env.store.RegionalRuntimeQuota(t.Context(), c.app.ID, id)
		if err != nil || stored.RetirementEnvelope == nil {
			t.Fatal("missing retirement", err)
		}
	}
	a, err := d.s.globalDirectory.(globaldirectory.RuntimeQuotaStore).RuntimeQuota(t.Context(), d.acct.ID, app.Slug, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range a.Targets {
		if target.IssuedAt == nil || target.ReleasedAt != nil {
			t.Fatal("delivery released issued capacity")
		}
	}
}

func TestRuntimeQuotaReceiverRefusesBindingAndPathBeforeMutation(t *testing.T) {
	d := globalDirectoryHarness(t)
	_, trust := quotaDirectoryKeys(t, d, uuid.NewString())
	e, app := cellDeploymentHarness(t)
	e.s.regionalQuotaTrust, e.s.regionalQuotaAPIBase = trust, "https://regional.example.com"
	e.h = e.s.handler()
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	b := runtimequota.Binding{CellID: e.s.controlPlaneCell.ID, Region: e.s.controlPlaneCell.Region, CellGeneration: 1, RegionalAccountID: quotaLocalUUID(e.acct.ID), RegionalOrgID: quotaLocalUUID(app.OrgID), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: e.s.regionalQuotaAPIBase}
	g := runtimequota.Grant{IssuerID: d.s.globalQuotaAuthority.Signer.IssuerID(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Policy: policy, Binding: b, Concurrency: 2, IssuedAtMicros: 1}
	envelope, err := d.s.globalQuotaAuthority.Signer.SignGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/cells/" + b.CellID + "/apps/" + app.ID + "/runtime-quotas/"
	for _, kind := range []string{"path", "origin", "owner", "signature", "duplicate", "no-consent", "read-only", "region", "cell", "slug", "org"} {
		t.Run(kind, func(t *testing.T) {
			body := runtimequota.Delivery{Binding: b, Authority: envelope}
			id := g.AllocationID
			key := e.key
			switch kind {
			case "path":
				id = uuid.NewString()
			case "origin":
				body.Binding.APIBase = "https://other.example.com"
			case "owner":
				body.Binding.RegionalAccountID = uuid.NewString()
			case "signature":
				body.Authority.Signature = "invalid"
			case "no-consent":
				key = ""
			case "read-only":
				var hash []byte
				key, hash, err = api.GenerateAPIKey()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
					t.Fatal(err)
				}
			case "region":
				body.Binding.Region = "eu-hel"
			case "cell":
				body.Binding.CellID = uuid.NewString()
			case "slug":
				body.Binding.RegionalAppSlug = "replacement-app"
			case "org":
				body.Binding.RegionalOrgID = uuid.NewString()
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "duplicate" {
				raw = bytes.Replace(raw, []byte(`"binding":`), []byte(`"binding":{},"binding":`), 1)
			}
			req := httptest.NewRequest(http.MethodPut, path+id, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+key)
			recorder := httptest.NewRecorder()
			e.h.ServeHTTP(recorder, req)
			if recorder.Code == http.StatusOK {
				t.Fatal("invalid authority accepted")
			}
			if _, err := e.store.RegionalRuntimeQuota(t.Context(), app.ID, g.AllocationID); err == nil {
				t.Fatal("invalid request mutated journal")
			}
		})
	}
	retirement := runtimequota.Retirement{Grant: g, RetirementID: uuid.NewString(), RetiredAtMicros: 2}
	retired, err := d.s.globalQuotaAuthority.Signer.SignRetirement(retirement)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, http.MethodPut, path+g.AllocationID+"/retirement", runtimequota.Delivery{Binding: b, Authority: retired}, nil); r.Code != http.StatusOK {
		t.Fatalf("retire before grant: %d %s", r.Code, r.Body)
	}
	if r := e.do(t, http.MethodPut, path+g.AllocationID, runtimequota.Delivery{Binding: b, Authority: envelope}, nil); r.Code != http.StatusOK {
		t.Fatalf("delayed grant: %d %s", r.Code, r.Body)
	}
	stored, err := e.store.RegionalRuntimeQuota(t.Context(), app.ID, g.AllocationID)
	if err != nil || stored.GrantEnvelope != nil || stored.RetirementEnvelope == nil {
		t.Fatal("delayed grant revived tombstone", err)
	}
}

func TestRuntimeQuotaDirectoryConfiguration(t *testing.T) {
	e := globalDirectoryHarness(t)
	issuer := uuid.NewString()
	cfg, _ := quotaDirectoryKeys(t, e, issuer)
	if err := e.s.configureRuntimeQuotaTransport(t.Context(), cfg, nil); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*RuntimeQuotaTransportConfig){
		func(c *RuntimeQuotaTransportConfig) { c.PrivateKeyPath = "" },
		func(c *RuntimeQuotaTransportConfig) { c.DirectoryIssuerID = uuid.NewString() },
		func(c *RuntimeQuotaTransportConfig) { c.RegionalAPIBase = "https://regional.example.com" },
	} {
		bad := cfg
		mutate(&bad)
		if err := e.s.configureRuntimeQuotaTransport(t.Context(), bad, nil); err == nil {
			t.Fatal("partial/conflicting signer configured")
		}
	}
	if err := (&server{}).configureRuntimeQuotaTransport(t.Context(), RuntimeQuotaTransportConfig{TrustBundlePath: cfg.TrustBundlePath, RegionalAPIBase: "https://regional.example.com"}, nil); err == nil {
		t.Fatal("receiver without database configured")
	}
}
