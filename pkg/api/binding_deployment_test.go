// adr: 428 — only reserved probes accept an explicit source deployment.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreateAppTaskRequestBindingDeploymentSelection(t *testing.T) {
	id := uuid.NewString()
	for _, command := range []string{AppTaskServiceBindingProbeCommand, AppTaskPostgresBindingProbeCommand, AppTaskObjectStorageBindingProbeCommand} {
		request := CreateAppTaskRequest{VerificationDeploymentID: strings.ReplaceAll(id, "-", ""), Command: []string{command, "BINDING"}}
		resolved, problem := request.Resolve()
		if problem != nil || resolved.VerificationDeploymentID != id {
			t.Fatalf("resolved=%+v problem=%+v", resolved, problem)
		}
	}
	for _, request := range []CreateAppTaskRequest{
		{VerificationDeploymentID: id, Command: []string{"echo", "BINDING"}},
		{VerificationDeploymentID: id, Command: []string{AppTaskServiceBindingSmokeCommand, "billing"}},
		{VerificationDeploymentID: id, Command: []string{AppTaskServiceBindingProbeCommand}},
		{VerificationDeploymentID: id, Command: []string{AppTaskServiceBindingProbeCommand, "billing", "extra"}},
		{VerificationDeploymentID: id, Command: []string{AppTaskServiceBindingProbeCommand + " billing"}, CommandShell: true},
		{VerificationDeploymentID: "v12", Command: []string{AppTaskServiceBindingProbeCommand, "billing"}},
	} {
		if _, problem := request.Resolve(); problem == nil {
			t.Fatalf("accepted unsafe selector: %+v", request)
		}
	}
}

func TestGetAppBindingInventoryForDeploymentSendsBothSelectors(t *testing.T) {
	id := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" || r.URL.Query().Get("deployment_id") != id || r.URL.Query().Get("scope") != "staging" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AppBindingInventory{App: "api", RequestedDeploymentID: id, VerificationDeploymentID: id})
	}))
	defer srv.Close()
	got, err := NewClient(srv.URL, "test").GetAppBindingInventoryForDeployment(context.Background(), "api", "staging", id)
	if err != nil || got.RequestedDeploymentID != id || got.VerificationDeploymentID != id {
		t.Fatalf("inventory=%+v err=%v", got, err)
	}
}
