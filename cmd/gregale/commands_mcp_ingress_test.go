package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPDefaultRequestTimeout(t *testing.T) {
	for _, tc := range []struct {
		name        string
		app         api.AppResponse
		wantTimeout int
	}{
		{"scale plan unset raises to 300", api.AppResponse{EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 30_000, RequestBudgetMaxMS: 900_000}}, 300},
		{"hobby ceiling caps at 60", api.AppResponse{EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 30_000, RequestBudgetMaxMS: 60_000}}, 60},
		{"free plan already at ceiling", api.AppResponse{EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 30_000, RequestBudgetMaxMS: 30_000}}, 0},
		{"explicit owner timeout kept", api.AppResponse{RequestTimeoutS: 45, EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 45_000, RequestBudgetMaxMS: 900_000}}, 0},
		{"unknown ceiling uses preferred", api.AppResponse{}, 300},
	} {
		if got := mcpDefaultRequestTimeout(tc.app); got != tc.wantTimeout {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.wantTimeout)
		}
	}
}

func TestMCPServingIngressReadyNamesCommands(t *testing.T) {
	open := api.AppResponse{StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}}
	if err := mcpServingIngressReady("tools", open); err != nil {
		t.Fatalf("open streaming app refused: %v", err)
	}
	protected := api.AppResponse{RequireAuthn: true, PublicAuth: api.PublicAuthStatus{Mode: "bearer"}}
	err := mcpServingIngressReady("tools", protected)
	if err == nil {
		t.Fatal("protected app accepted")
	}
	for _, want := range []string{"gregale app tools --no-require-authn", "gregale app tools --public-auth open", "gregale app tools --streaming-enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
