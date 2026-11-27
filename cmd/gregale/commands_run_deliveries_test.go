package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRunDeliveriesUseExactArtifactAndSeparateStorageIdentity(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/executions/run-one":
			_ = json.NewEncoder(w).Encode(api.ExecutionResponse{ID: "run-one", Status: api.ExecutionStatusSucceeded, Artifacts: []api.ExecutionArtifact{{Name: "report.html", SizeBytes: 20, SHA256: "sha256:expected"}}})
		case "/v1/apps/reports/run-artifact-deliveries/reports":
			posts++
			var req api.DeliverRunArtifactRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IdempotencyKey != "report-one" || req.ExecutionID != "run-one" || req.ArtifactName != "report.html" || req.SizeBytes != 20 || req.SHA256 != "sha256:expected" {
				t.Error("delivery identity or source metadata changed", req, err)
			}
			_ = json.NewEncoder(w).Encode(api.RunArtifactDelivery{ID: "delivery-one", Status: "completed"})
		case "/v1/apps/reports/run-artifact-deliveries/reports/delivery-one":
			_ = json.NewEncoder(w).Encode(api.RunArtifactDelivery{ID: "delivery-one", Status: "failed", ErrorCode: "provider_write_rejected"})
		default:
			t.Error("unexpected delivery route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	for _, prefix := range [][]string{{"run"}, {"sandbox", "run"}} {
		for range 2 {
			args := append(append([]string(nil), prefix...), "deliveries", "deliver", "reports", "reports", "--run", "run-one", "--artifact", "report.html", "--content-type", "text/html", "--idempotency-key", "report-one", "--json")
			code, _, stderr := runSourceRunCLI(t, args...)
			if code != 0 {
				t.Fatal(code, stderr)
			}
		}
	}
	if posts != 4 {
		t.Fatal(posts)
	}
	code, _, stderr := runSourceRunCLI(t, "run", "deliveries", "status", "reports", "reports", "delivery-one", "--wait", "--json")
	if code != 1 {
		t.Fatal("failed storage receipt accepted", code, stderr)
	}
}
