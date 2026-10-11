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

// adr: 597
func TestCreateAppTaskRequestSmokeDeploymentSelection(t *testing.T) {
	id := uuid.NewString()
	command := []string{AppTaskServiceBindingSmokeCommand, "billing", uuid.NewString(), "/ready?key=private", "200"}
	request := CreateAppTaskRequest{SmokeDeploymentID: strings.ReplaceAll(id, "-", ""), Command: command}
	resolved, problem := request.Resolve()
	if problem != nil || resolved.SmokeDeploymentID != id || resolved.VerificationDeploymentID != "" {
		t.Fatalf("resolved=%+v problem=%+v", resolved, problem)
	}
	for _, bad := range []CreateAppTaskRequest{
		{SmokeDeploymentID: "v12", Command: command},
		{SmokeDeploymentID: uuid.Nil.String(), Command: command},
		{SmokeDeploymentID: id, VerificationDeploymentID: id, Command: command},
		{SmokeDeploymentID: id, Command: []string{"echo", "billing"}},
		{SmokeDeploymentID: id, Command: []string{AppTaskServiceBindingProbeCommand, "billing"}},
		{SmokeDeploymentID: id, Command: command, CommandShell: true},
	} {
		if _, problem := bad.Resolve(); problem == nil {
			t.Fatalf("accepted unsafe smoke selector: %+v", bad)
		}
	}
	for _, change := range []func([]string) []string{
		func(c []string) []string { return c[:4] },
		func(c []string) []string { c[1] = "BILLING"; return c },
		func(c []string) []string { c[2] = uuid.Nil.String(); return c },
		func(c []string) []string { c[3] = "https://outside.example/ready"; return c },
		func(c []string) []string { c[4] = "199"; return c },
		func(c []string) []string { c[4] = "600"; return c },
		func(c []string) []string { c[4] = "2xx"; return c },
	} {
		bad := change(append([]string(nil), command...))
		for _, selector := range []string{"", id} {
			if _, problem := (CreateAppTaskRequest{SmokeDeploymentID: selector, Command: bad}).Resolve(); problem == nil {
				t.Fatalf("accepted malformed reserved command: %v", bad)
			}
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
