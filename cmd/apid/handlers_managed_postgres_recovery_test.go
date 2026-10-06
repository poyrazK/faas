package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type recoveryAPIProvider struct {
	resizeAPIProvider
	reads int
	err   error
}

func (p *recoveryAPIProvider) ObserveRestoreSource(_ context.Context, d managedpostgres.RestoreSourceDefinition) (managedpostgres.RestoreSourceObservation, error) {
	p.reads++
	return managedpostgres.RestoreSourceObservation{ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Status: managedpostgres.ProviderStatusReady, RetentionSeconds: 300, HistoryNotBefore: time.Now().UTC().Add(-24 * time.Hour)}, p.err
}

func TestManagedPostgresRecoveryRouteScopesAndLiveUncertainty(t *testing.T) {
	e := setup(t, api.PlanPro)
	p := &recoveryAPIProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "private-backend"}, Backends: []managedpostgres.BackendConfig{{ID: "private-backend", Driver: "fixture", Region: "us-east-1", Namespace: "private"}}}, func(string) string { return "PRIVATE_SECRET" }, map[string]managedpostgres.Factory{"fixture": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return p, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return enabled }})
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.Create(t.Context(), managedpostgres.CreateRequest{AccountID: e.acct.ID, Name: "orders", Spec: managedpostgres.Spec{Region: "us-east-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	e.s.managedPostgres = svc
	enabled = false
	path := "/v1/postgres/databases/" + d.ID + "/recovery"
	if rec := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": ""}); rec.Code != 401 || p.reads != 0 {
		t.Fatal(rec.Code, p.reads)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	rec := e.do(t, http.MethodGet, path, nil, headers)
	var out api.ManagedPostgresRecoveryStatus
	if err = json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || out.Status != "limits_known" || !out.Fresh || out.HistoryBoundsKnown || out.RetentionSeconds != 300 || p.reads != 1 {
		t.Fatal(out, rec.Code, err, p.reads)
	}
	if strings.Contains(rec.Body.String(), "PRIVATE_") || strings.Contains(rec.Body.String(), "private-backend") {
		t.Fatal("private recovery evidence leaked")
	}
	other, err := e.store.CreateAccount(t.Context(), "other-"+d.ID+"@recovery.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, foreignHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), other.ID, foreignHash, "foreign reader", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	if foreign := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + foreignToken}); foreign.Code != http.StatusNotFound || p.reads != 1 {
		t.Fatal("foreign source read", foreign.Code, p.reads)
	}
	wrongToken, wrongHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, wrongHash, "usage only", []string{"usage:read"}); err != nil {
		t.Fatal(err)
	}
	if denied := e.do(t, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + wrongToken}); denied.Code != http.StatusForbidden || p.reads != 1 {
		t.Fatal("scope bypass", denied.Code, p.reads)
	}
	p.err = errors.New("PRIVATE_PROVIDER_PASSWORD")
	rec = e.do(t, http.MethodGet, path, nil, headers)
	out = api.ManagedPostgresRecoveryStatus{}
	if err = json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 || out.Status != "unknown" || out.Fresh || out.EarliestPossibleTime != "" || out.LatestPossibleTime != "" || out.LastErrorCode != "provider_unavailable" || strings.Contains(rec.Body.String(), "PRIVATE_") {
		t.Fatal(out, rec.Body.String(), err)
	}
	for _, requestPath := range []string{"/v1/postgres/databases/unknown/recovery", path + "?unexpected=true"} {
		before := p.reads
		rec = e.do(t, http.MethodGet, requestPath, nil, headers)
		if (rec.Code != 404 && rec.Code != 400) || p.reads != before {
			t.Fatal(rec.Code, p.reads)
		}
	}
}
