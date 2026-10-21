package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedServiceGraphProtocol(t *testing.T) {
	for _, scenario := range []string{"matched", "mixed_artifact", "mixed_config", "mixed_release", "missing_release", "untrusted_ca"} {
		t.Run(scenario, func(t *testing.T) {
			backend := newWorkload("api", "stable", "stable-config")
			if scenario == "mixed_artifact" {
				backend.report.Generation = "progressive"
			}
			if scenario == "mixed_config" {
				backend.report.Mode = "progressive-config"
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "mixed_release" {
					r.Header.Set("X-Gregale-Release", "release-b")
				}
				backend.ServeHTTP(w, r)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "service-ca.pem")
			data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			if scenario == "untrusted_ca" {
				data = []byte("untrusted")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GREGALE_SERVICE_CA_BUNDLE", path)
			t.Setenv("GREGALE_SERVICE_BACKEND_HTTPS_URL", server.URL)
			frontend := newWorkload("api", "stable", "stable-config")
			req := httptest.NewRequest(http.MethodGet, "http://frontend/", nil)
			if scenario != "missing_release" {
				req.Header.Set("X-Gregale-Release", "release-a")
			}
			report := frontend.snapshot()
			err := frontend.downstream(req, &report)
			if scenario == "matched" {
				if err != nil || report.BackendGeneration != "stable" || report.BackendMode != "stable-config" || report.BackendReleaseID != "release-a" || report.ReleaseID != "release-a" {
					t.Fatalf("matched graph: %+v %v", report, err)
				}
			} else if err == nil {
				t.Fatal("incompatible service graph was accepted")
			}
		})
	}
}
