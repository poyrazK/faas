// ADR-202610180816: recovery commands preserve explicit event and request identity.
package main

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIAutomationRecoveryRequestsRequireExactIdentity(t *testing.T) {
	binding, event := uuid.NewString(), uuid.NewString()
	base := "/v1/internal/ci/github/automation/" + binding + "/events/" + event
	method, path, body, headers, err := ciAutomationRecoveryRequest("retry", binding, event, "stable-request", 0)
	if err != nil || method != http.MethodPost || path != base+"/retry" || headers.Get("Idempotency-Key") != "stable-request" || body.(ci.AutomationRetryRequest).ExpectedRetryGeneration != 0 {
		t.Fatal("automation retry lost source, generation or request identity")
	}
	method, path, body, headers, err = ciAutomationRecoveryRequest("status", binding, event, "", -1)
	if err != nil || method != http.MethodGet || path != base || body != nil || len(headers) != 0 {
		t.Fatal("status mutated recovery state")
	}
	for _, tc := range []struct {
		command, binding, event, key string
		generation                   int64
	}{
		{"retry", binding, event, "", 0},
		{"retry", "../foreign", event, "key", 0},
		{"retry", binding, "../foreign", "key", 0},
		{"retry", binding, event, "key", -1},
		{"retry", binding, event, "key", api.CIAutomationRetryMaxGenerations + 1},
		{"retry", binding, event, "key\ninvalid", 0},
		{"status", binding, event, "key", -1},
		{"status", binding, event, "", 0},
		{"unknown", binding, event, "", -1},
	} {
		if _, _, _, _, err := ciAutomationRecoveryRequest(tc.command, tc.binding, tc.event, tc.key, tc.generation); err == nil {
			t.Fatalf("unsafe recovery request accepted: %+v", tc)
		}
	}
}
