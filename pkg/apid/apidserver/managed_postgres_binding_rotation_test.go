package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRotateManagedPostgresBindingQueuesRuntimeRefresh(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "postgres-rotate", 9001)
	bindings, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	binding, err := (*(*env.Srv()).ManagedPostgresBindings()).Create(context.Background(), managedpostgres.CreateBindingRequest{
		AccountID: (*env.AcctID()), DatabaseID: databaseID, AppID: (*env.AppID()),
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite,
	})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if _, err := (*env.Store()).CreateDeployment(context.Background(), state.Deployment{
		AppID: (*env.AppID()), Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:binding-rotation", Status: state.DeployLive,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create live deployment: %v", err)
	}
	notifier := &apidserver.RuntimeConfigRestartNotifier{}
	(*(*env.Srv()).Notif()) = notifier

	req := httptest.NewRequest(http.MethodPost, "/v1/postgres/bindings/"+binding.ID+"/rotate", nil)
	req.SetPathValue("id", binding.ID)
	acct, err := (*env.Store()).AccountByID(context.Background(), (*env.AcctID()))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(*env.Srv()).RotateManagedPostgresBinding(response, req, acct)
	if response.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", response.Code, response.Body.String())
	}
	var rotated api.ManagedPostgresBinding
	if err := json.Unmarshal(response.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode rotated binding: %v", err)
	}
	if rotated.CredentialGeneration != 2 || !rotated.RotationPending || rotated.State != "ready" {
		t.Fatalf("rotation response = %+v", rotated)
	}
	if (*notifier.Channel()) != db.NotifyRuntimeConfigRestart {
		t.Fatalf("notification channel=%q, want %q", (*notifier.Channel()), db.NotifyRuntimeConfigRestart)
	}
	var payload struct {
		AppID  string `json:"app_id"`
		WakeID string `json:"wake_id"`
	}
	if err := json.Unmarshal([]byte((*notifier.Payload())), &payload); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if payload.AppID != (*env.AppID()) || payload.WakeID == "" {
		t.Fatalf("rotation notification = %+v", payload)
	}
	current, err := bindings.GetBinding(context.Background(), (*env.AcctID()), binding.ID)
	if err != nil || current.RotationWakeID != payload.WakeID || current.RotationPreviousGeneration != 1 {
		t.Fatalf("stored rotation = %+v err=%v", current, err)
	}
}

func TestRotateMigrationBindingDoesNotRestartServingInstances(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "migration-rotate", 9002)
	_, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	binding, err := (*(*env.Srv()).ManagedPostgresBindings()).Create(context.Background(), managedpostgres.CreateBindingRequest{AccountID: (*env.AcctID()), DatabaseID: databaseID, AppID: (*env.AppID()), Scope: "default", EnvironmentKey: "SCHEMA_DSN", Access: managedpostgres.CredentialMigration})
	if err != nil {
		t.Fatal(err)
	}
	notifier := &apidserver.RuntimeConfigRestartNotifier{}
	(*(*env.Srv()).Notif()) = notifier
	req := httptest.NewRequest(http.MethodPost, "/v1/postgres/bindings/"+binding.ID+"/rotate", nil)
	req.SetPathValue("id", binding.ID)
	acct, err := (*env.Store()).AccountByID(context.Background(), (*env.AcctID()))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(*env.Srv()).RotateManagedPostgresBinding(response, req, acct)
	if response.Code != http.StatusOK {
		t.Fatalf("rotate = %d: %s", response.Code, response.Body.String())
	}
	if (*notifier.Channel()) != "" {
		t.Fatal("migration rotation restarted the serving application")
	}
}
