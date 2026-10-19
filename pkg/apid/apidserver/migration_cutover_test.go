package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationCutoverHTTPPreparationRecoveryAndReadScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, dep := apidserver.SeedAppTaskDeployment(t, e, "cutover-http")
	stamp, _, err := state.RuntimeConfigChangedAtForScope(t.Context(), *e.Store(), app.ID, dep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	v := api.MigrationVerification{ID: uuid.NewString(), App: app.Slug, DeploymentID: dep.ID, Scope: "default", ConfigurationRevision: state.MigrationConfigurationRevision(dep, stamp), Status: "passed", Phase: "complete", Request: api.CreateMigrationVerificationRequest{SmokePath: "/ready"}, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Checks: []api.MigrationVerificationCheck{}}
	if _, err := (*e.Store()).CreateMigrationVerification(t.Context(), state.MigrationVerificationRecord{AccountID: (*e.Acct()).ID, AppID: app.ID, Report: v}); err != nil {
		t.Fatal(err)
	}
	in := api.CreateMigrationCutoverRequest{ID: uuid.NewString(), VerificationID: v.ID, Hostname: "api.example.test", PreviousRecords: []api.MigrationDNSRecord{{Type: "CNAME", Value: "old.example.test", TTL: 300}}, Max5xxRateBPS: 100, MaxP95MS: 1000}
	base := "/v1/apps/" + app.Slug + "/migration-cutovers"
	created := e.Do(t, http.MethodPost, base, in, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var plan api.MigrationCutover
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != "prepared" || plan.DomainConnected {
		t.Fatalf("preparation changed domain: %+v", plan)
	}
	if _, err := (*e.Store()).DomainByName(t.Context(), in.Hostname); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("preparation enrolled a domain: %v", err)
	}
	if rec := e.Do(t, http.MethodPost, base+"/"+in.ID+"/actions", api.MigrationCutoverActionRequest{Action: "start_checks"}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("started without domain: %d %s", rec.Code, rec.Body)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "read only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if rec := e.DoAs(t, http.MethodGet, base+"/"+in.ID, nil, nil, key); rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	if rec := e.DoAs(t, http.MethodPost, base+"/"+in.ID+"/actions", api.MigrationCutoverActionRequest{Action: "confirm_restored"}, nil, key); rec.Code != http.StatusForbidden {
		t.Fatalf("read key mutated recovery: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/another-app/migration-cutovers/"+in.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("app isolation: %d", rec.Code)
	}
	if err := (*e.Store()).MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	loaded := e.Do(t, http.MethodGet, base+"/"+in.ID, nil, nil)
	if loaded.Code != http.StatusOK {
		t.Fatalf("read after drift: %d %s", loaded.Code, loaded.Body)
	}
	if err := json.Unmarshal(loaded.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Status != "blocked" || plan.Request.PreviousRecords[0].Value != "old.example.test" {
		t.Fatalf("stale proof/recovery: %+v", plan)
	}
	if rec := e.Do(t, http.MethodPost, base+"/"+in.ID+"/actions", api.MigrationCutoverActionRequest{Action: "confirm_restored"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("recovery after drift: %d %s", rec.Code, rec.Body)
	}
}
