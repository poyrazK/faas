package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPromotionGraphUsesDestinationBindingWithoutForwardingClaims(t *testing.T) {
	for _, scenario := range []string{"http", "https", "mixed_artifact", "mixed_config", "missing_graph", "wrong_secret", "regressed"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("PROMOTION_SERVICE_GRAPH", "1")
			t.Setenv("GRAPH_ROLE", "frontend")
			t.Setenv("TOKEN", "promotion-production-secret")
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, header := range []string{"X-Gregale-Release", "Gregale-Target-Deployment", "X-Gregale-Revision", "X-Faas-Route-Probe-Token"} {
					if r.Header.Get(header) != "" {
						t.Errorf("forwarded caller claim: %s", header)
					}
				}
				if r.URL.Path != "/promotion-protocol" || r.Method != "GET" {
					t.Error("wrong managed protocol")
				}
				if scenario == "regressed" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				report := observation{Generation: "tested", Mode: "tested-config", DeploymentID: "candidate-api", InstanceID: "api-instance", ReleaseID: "verified-private-graph", DestinationSecretMatches: true}
				switch scenario {
				case "mixed_artifact":
					report.Generation = "baseline"
				case "mixed_config":
					report.Mode = "staging-config"
				case "missing_graph":
					report.ReleaseID = ""
				case "wrong_secret":
					report.DestinationSecretMatches = false
				}
				writeObservation(w, report)
			})
			server := httptest.NewUnstartedServer(handler)
			if scenario == "https" {
				server.StartTLS()
				path := filepath.Join(t.TempDir(), "ca.pem")
				if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GREGALE_SERVICE_CA_BUNDLE", path)
			} else {
				server.Start()
			}
			defer server.Close()
			t.Setenv("GREGALE_SERVICE_BACKEND_URL", server.URL)
			frontend := newWorkload("api", "tested", "tested-config")
			r := httptest.NewRequest(http.MethodGet, "http://frontend/", nil)
			for _, header := range []string{"X-Gregale-Release", "Gregale-Target-Deployment", "X-Gregale-Revision", "X-Faas-Route-Probe-Token"} {
				r.Header.Set(header, "forged")
			}
			report := frontend.snapshot()
			err := frontend.downstream(r, &report)
			if scenario == "http" || scenario == "https" {
				if err != nil || report.BackendDeploymentID != "candidate-api" || report.ReleaseID != "verified-private-graph" || report.BindingTransport != scenario {
					t.Fatalf("destination graph: %+v %v", report, err)
				}
			} else if err == nil {
				t.Fatal("accepted mixed or failed API")
			}
		})
	}
}

func TestPromotionProtocolRejectsMissingSQLProof(t *testing.T) {
	t.Setenv("PROMOTION_SERVICE_GRAPH", "1")
	t.Setenv("POSTGRES_MIGRATION_RUN_ID", "invalid-migration-identity")
	guest := newWorkload("api", "tested", "tested-config")
	response := httptest.NewRecorder()
	guest.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://backend/promotion-protocol", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("dependency protocol bypassed SQL health", response.Code)
	}
}
