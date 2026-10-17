package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRehearsalNetworkRegistrationRequiresExactAcknowledgement(t *testing.T) {
	for _, mode := range []string{"", "contained", "other"} {
		t.Run("ack="+mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NetworkMode != "contained" {
					t.Error("containment missing from request")
				}
				_ = json.NewEncoder(w).Encode(RegisterScenarioTestResponse{WebhookMode: "capture", PolicyVersion: 1, NetworkMode: mode})
			}))
			defer server.Close()
			err := NewClient(server.URL, "key").RegisterScenarioTest(t.Context(), "abababababababababababababababab", RegisterScenarioTestRequest{WebhookMode: "capture", NetworkMode: "contained"})
			if (err == nil) != (mode == "contained") {
				t.Fatalf("ack %q: %v", mode, err)
			}
		})
	}
}
