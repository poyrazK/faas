package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcap"
)

func TestCmdCapabilitiesHumanOutput(t *testing.T) {
	resetJSONOutput()
	capabilities, err := api.CapabilitiesForPlan(api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	f := authedFakeAPI(t, string(body), 200)
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })
	if code := cmdCapabilities(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawPath != "/v1/capabilities" {
		t.Fatalf("path = %q, want /v1/capabilities", f.sawPath)
	}
	if !strings.Contains(out.String(), "custom-domains") || !strings.Contains(out.String(), "available") {
		t.Fatalf("human output missing capability rows:\n%s", out.String())
	}
}

func TestCmdCapabilitiesJSONOutput(t *testing.T) {
	resetJSONOutput()
	capabilities, err := api.CapabilitiesForPlan(api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	authedFakeAPI(t, string(body), 200)
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	if code := cmdCapabilities(nil); code != 0 {
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
}

func TestCmdCapabilitiesRejectsPositionals(t *testing.T) {
	resetJSONOutput()
	if code := cmdCapabilities([]string{"extra"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
