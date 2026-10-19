package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func managedCLIInventory() api.AppBindingInventoryItem {
	return api.AppBindingInventoryItem{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING_URL", Scope: "production", State: "enforced", Transport: "https", VerificationStatus: "unsupported", ServiceTargetDeploymentID: uuid.NewString(), ServiceContext: &api.ServiceBindingInventoryContext{Source: "environment_release", CallerDeploymentID: pinnedBindingDeployment, TargetApp: "git-billing", TargetAppID: uuid.NewString(), ReleaseID: uuid.NewString(), Revision: strings.Repeat("a", 64), RoutingStatus: "resolved", RoutingReason: "active_reviewed_release", VerificationReason: api.ManagedServiceVerificationUnsupported}}
}

func TestBindingsInventoryDeploymentSelection(t *testing.T) {
	resetJSONOut(t)
	f := newAllProbePreviewFixture(t)
	f.inventory.Bindings = []api.AppBindingInventoryItem{managedCLIInventory()}
	f.inventory.ServiceBindingSource = "environment_release"
	t.Setenv("FAAS_API", f.server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	args := []string{"bindings", "api", "--deployment", pinnedBindingDeployment, "--json"}
	if code := run(args); code != 0 {
		t.Fatalf("code=%d output=%s", code, &output)
	}
	var got api.AppBindingInventory
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ServiceBindingSource != "environment_release" || got.Bindings[0].ServiceContext.TargetApp != "git-billing" {
		t.Fatalf("inventory=%+v", got)
	}
	f.inventory.ServiceBindingSource = ""
	output.Reset()
	if code := run(args); code == 0 {
		t.Fatal("old server did not confirm the service namespace contract")
	}
	f.inventory.ServiceBindingSource = "environment_release"
	f.inventory.RequestedDeploymentID = ""
	output.Reset()
	if code := run(args); code == 0 {
		t.Fatal("server ignored deployment selector")
	}
}

func TestManagedServiceProbesBlockedBeforeAdmission(t *testing.T) {
	for _, args := range [][]string{
		{"verify", "api", "--all", "--deployment", pinnedBindingDeployment, "--require-complete", "--dry-run", "--json"},
		{"verify", "api", "billing", "--deployment", pinnedBindingDeployment, "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetJSONOut(t)
			stderr, restoreStderr := captureStderr(t)
			defer restoreStderr()
			f := newAllProbePreviewFixture(t)
			f.inventory.Bindings = []api.AppBindingInventoryItem{managedCLIInventory()}
			t.Setenv("FAAS_API", f.server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			if code := run(append([]string{"bindings"}, args...)); code == 0 {
				t.Fatalf("managed service probe passed: %s", &output)
			}
			if !strings.Contains(output.String()+stderr.String(), api.ManagedServiceVerificationUnsupported) {
				t.Fatalf("missing reason: stdout=%s stderr=%s", &output, stderr.String())
			}
			for _, call := range f.calls {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatalf("unsupported managed context admitted work: %s", call)
				}
			}
		})
	}
}
