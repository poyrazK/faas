package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentResponseCheckCapabilitiesNeverAdmitOrForward(t *testing.T) {
	for _, missing := range []string{"none", "store", "picker", "admission", "forward"} {
		t.Run(missing, func(t *testing.T) {
			checker := deploymentResponseChecker{
				store: state.NewMemStore(), picker: &responseCheckPicker{},
				ensure: func(context.Context, gateway.App, string) error {
					t.Error("capability read admitted capacity")
					return nil
				},
				forward: func(gateway.Target) http.Handler {
					t.Error("capability read contacted a guest")
					return http.NotFoundHandler()
				},
			}
			switch missing {
			case "store":
				checker.store = nil
			case "picker":
				checker.picker = nil
			case "admission":
				checker.ensure = nil
			case "forward":
				checker.forward = nil
			}
			response := httptest.NewRecorder()
			checker.Capabilities(response, httptest.NewRequest(http.MethodGet, api.DeploymentResponseCheckCapabilitiesControlPath, nil))
			var result api.DeploymentResponseCheckCapabilities
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.SupportsCurrentProtocol() != (missing == "none") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("incorrect control capability: %+v code=%d", result, response.Code)
			}
		})
	}
}
