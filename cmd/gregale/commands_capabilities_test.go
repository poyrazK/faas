package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestCmdCapabilitiesHumanOutput(t *testing.T) {
	main.ResetJSONOutput()
	capabilities, err := api.CapabilitiesForPlan(api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	f := main.AuthedFakeAPI(t, string(body), 200)
	var out bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previous })
	if code := main.CmdCapabilities(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawPath()) != "/v1/capabilities" {
		t.Fatalf("path = %q, want /v1/capabilities", (*f.SawPath()))
	}
	if !strings.Contains(out.String(), "custom-domains") || !strings.Contains(out.String(), "available") {
		t.Fatalf("human output missing capability rows:\n%s", out.String())
	}
}

func TestCmdCapabilitiesJSONOutput(t *testing.T) {
	main.ResetJSONOutput()
	capabilities, err := api.CapabilitiesForPlan(api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	main.AuthedFakeAPI(t, string(body), 200)
	var out bytes.Buffer
	previousOut := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previousOut })
	previousJSON := (*main.JsonOutput)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = previousJSON })
	if code := main.CmdCapabilities(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got api.CapabilitiesResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan != string(api.PlanFree) || got.RegistryVersion != catalog.Version {
		t.Fatalf("unexpected response: %+v", got)
	}
	for _, capability := range got.Capabilities {
		if capability.Key == "object-storage" && (capability.Enabled || capability.UnavailableReason != api.CapabilityUnavailablePlan || capability.UnavailableDetail == "") {
			t.Fatalf("JSON output lost the plan explanation: %+v", capability)
		}
	}
}

func TestCmdCapabilitiesExplainsUnavailableFeaturesAndSupportsOlderServers(t *testing.T) {
	main.ResetJSONOutput()
	response := api.CapabilitiesResponse{
		RegistryVersion: 1,
		Plan:            "free",
		Capabilities: []api.CapabilityStatus{
			{Key: "object-storage", UnavailableReason: api.CapabilityUnavailablePlan, UnavailableDetail: "Not included in your current plan."},
			{Key: "github-deploys", UnavailableReason: api.CapabilityUnavailableRuntime, UnavailableDetail: "Contact support for availability."},
			{Key: "legacy-feature"},
		},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	main.AuthedFakeAPI(t, string(body), 200)
	var out bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previous })
	if code := main.CmdCapabilities(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, text := range []string{"Not included in your current plan.", "Contact support for availability.", "legacy-feature"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("human output missing %q:\n%s", text, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "legacy-feature") && !strings.HasSuffix(line, "unavailable") {
			t.Errorf("legacy row lost its fallback: %s", line)
		}
	}
}

func TestCmdCapabilitiesRejectsPositionals(t *testing.T) {
	main.ResetJSONOutput()
	if code := main.CmdCapabilities([]string{"extra"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
