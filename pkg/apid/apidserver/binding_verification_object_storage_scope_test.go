package apidserver_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http"
	"testing"
)

func TestObjectStorageBindingVerificationScopeAndPermission(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		t.Run(scope, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			apidserver.EnableAppTaskAPIForTest(&e)
			app, _ := apidserver.SeedAppTaskDeployment(t, e, "storage-scope")
			apidserver.ReadyObjectStorageVerificationBinding(t, e, app, scope)
			task := apidserver.CreateAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}})
			stored, err := (*e.Store()).AppTaskByID(context.Background(), (*e.Acct()).ID, app.ID, task.ID)
			if err != nil || (stored.BindingVerification != nil) != (scope == "default") {
				t.Fatalf("scope=%s pin=%+v err=%v", scope, stored.BindingVerification, err)
			}
		})
	}
	e := apidserver.SetupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	apidserver.EnableAppTaskAPIForTest(&e)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "storage-permission")
	apidserver.ReadyObjectStorageVerificationBinding(t, e, app, "default")
	task := apidserver.CreateAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}})
	stored, err := (*e.Store()).AppTaskByID(context.Background(), (*e.Acct()).ID, app.ID, task.ID)
	if err != nil || stored.BindingVerification != nil {
		t.Fatalf("restricted task acquired evidence: %+v %v", stored, err)
	}
	inventory := apidserver.DecodeBindingInventory(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeObjectStorage {
			t.Fatal("forbidden storage metadata exposed")
		}
	}
}
