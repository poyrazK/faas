package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

func TestNewDeployReceiptIncludesSimpleAppPlan(t *testing.T) {
	plan := &simpleapp.Plan{
		ResourceProfile: "small",
		MemoryMB:        512,
		CPUMillicores:   500,
		Port:            8080,
		HealthPath:      "/ready",
		ExecutionMode:   api.ExecutionModeRequest,
		ScaleToZero:     true,
		LocalStorage:    simpleapp.LocalStorageEphemeral,
		DurableState:    simpleapp.DurableStateExternal,
	}

	receipt := newDeployReceipt(api.DeploymentResponse{ID: "dep-1"}, nil, "https://demo.apps.gregale.dev", "", plan)
	if receipt.SimpleAppPlan == nil {
		t.Fatal("simple_app_plan is nil")
	}
	if got := receipt.SimpleAppPlan.ResourceProfile; got != "small" {
		t.Fatalf("resource profile = %q, want small", got)
	}
	if got := receipt.SimpleAppPlan.HealthPath; got != "/ready" {
		t.Fatalf("health path = %q, want /ready", got)
	}
	if !receipt.SimpleAppPlan.ScaleToZero {
		t.Fatal("scale_to_zero = false, want true")
	}

	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	if strings.Contains(string(body), "secret") {
		t.Fatalf("receipt contains secret material: %s", body)
	}
}

func TestNewDeployReceiptOmitsSimpleAppPlanByDefault(t *testing.T) {
	body, err := json.Marshal(newDeployReceipt(api.DeploymentResponse{ID: "dep-1"}, nil, "", ""))
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	if strings.Contains(string(body), "simple_app_plan") {
		t.Fatalf("non-simple receipt contains simple_app_plan: %s", body)
	}
}
