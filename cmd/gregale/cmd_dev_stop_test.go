package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCmdDevStopIgnoresManifestSyncFiles(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
	}{
		{name: "env file", manifest: "dev:\n  env_file: .env.dev\n"},
		{name: "service override file", manifest: "dev:\n  service_override_file: .env.services.local\n"},
		{name: "both", manifest: "dev:\n  env_file: .env.dev\n  service_override_file: .env.services.local\n  postgres: true\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(tt.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			var destroyed atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/dev/sessions/stop-demo") {
					destroyed.Store(true)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", testAPIKey('s'))
			t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("ab", 16))
			t.Chdir(dir)

			if code := cmdDev([]string{"--name", "stop-demo", "--stop"}); code != 0 {
				t.Fatalf("cmdDev --stop = %d, want 0 with manifest sync-file defaults", code)
			}
			if !destroyed.Load() {
				t.Fatal("cmdDev --stop did not destroy the developer session")
			}
		})
	}
}
