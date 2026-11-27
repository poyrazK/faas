package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type upgradeRehearsalAPIProvider struct {
	resizeAPIProvider
	t         *testing.T
	request   string
	rehearsal string
}

func (p upgradeRehearsalAPIProvider) UpgradeRehearsal(_ context.Context, resource, key string, major int, start bool) (api.ManagedPostgresUpgradeRehearsal, error) {
	if resource != "PRIVATE_DATA" || (start && (key != p.request || major != 18)) || (!start && (key != p.rehearsal || major != 0)) {
		p.t.Errorf("unexpected provider call: %q %q %d start=%v", resource, key, major, start)
	}
	started := time.Now().UTC().Add(-time.Minute)
	out := api.ManagedPostgresUpgradeRehearsal{
		RehearsalID: p.rehearsal, RequestID: p.request, SourceMajor: 17, TargetMajor: 18, Outcome: "queued", StartedAt: started,
	}
	if !start {
		point := started.Add(-time.Minute)
		out.Outcome, out.RecoveryPoint, out.FinishedAt = "succeeded", &point, &started
	}
	return out, nil
}

func TestManagedPostgresUpgradeRehearsalRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	p := upgradeRehearsalAPIProvider{t: t, request: uuid.NewString(), rehearsal: uuid.NewString()}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "us-east-1", MaxDatabasesPerAccount: 3, Defaults: map[string]string{"us-east-1": "native"},
		Backends: []managedpostgres.BackendConfig{{ID: "native", Driver: "firecracker", Region: "us-east-1", Namespace: "private"}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"firecracker": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	acct := (*e.Acct()).ID
	d, err := service.Create(t.Context(), managedpostgres.CreateRequest{AccountID: acct, Name: "orders", Spec: managedpostgres.Spec{
		Region: "us-east-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600,
	}})
	if err != nil {
		t.Fatal(err)
	}
	*(*e.S()).ManagedPostgres() = service

	base := "/v1/postgres/databases/" + d.ID + "/upgrade-rehearsals"
	body := api.RequestManagedPostgresUpgradeRehearsal{RequestID: p.request, PostgresMajor: 18}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Store()).CreateAPIKey(t.Context(), acct, hash, "read only", []string{"postgres:read"}); err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]string{"Authorization": "Bearer " + token}
	if rec := e.Do(t, http.MethodPost, base, body, readOnly); rec.Code != http.StatusForbidden {
		t.Fatalf("read scope queued a rehearsal: %d %s", rec.Code, rec.Body)
	}

	rec := e.Do(t, http.MethodPost, base, body, nil)
	var status api.ManagedPostgresUpgradeRehearsal
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || rec.Code != http.StatusAccepted ||
		status.Outcome != "queued" || status.DatabaseID != d.ID || status.RehearsalID != p.rehearsal || strings.Contains(rec.Body.String(), "PRIVATE_") {
		t.Fatalf("start = %d %s, %v", rec.Code, rec.Body, err)
	}
	rec = e.Do(t, http.MethodGet, base+"/"+p.rehearsal, nil, readOnly)
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || rec.Code != http.StatusOK ||
		rec.Header().Get("Cache-Control") != "no-store" || status.Outcome != "succeeded" || status.RecoveryPoint == nil || status.TargetMajor != 18 {
		t.Fatalf("read = %d %s, %v", rec.Code, rec.Body, err)
	}
}
