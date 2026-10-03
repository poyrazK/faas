package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppManifestSecretReloadClientPreservesReadiness(t *testing.T) {
	manifest := AppManifest{Entrypoint: []string{"node", "server.js"}, SecretReloadSignal: "SIGHUP", SecretReloadReadiness: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/worker" {
			t.Errorf("request = %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AppResponse{Slug: "worker", Manifest: manifest})
	}))
	defer server.Close()
	app, err := NewClient(server.URL, "fixture-token").GetApp(context.Background(), "worker")
	if err != nil || !app.Manifest.SecretReloadReadiness || app.Manifest.SecretReloadSignal != "SIGHUP" {
		t.Fatalf("readiness lost in app response: %+v %v", app.Manifest, err)
	}
}

func TestAppManifestSecretReloadValidation(t *testing.T) {
	for _, manifest := range []AppManifest{
		{Entrypoint: []string{"app"}, SecretReloadReadiness: true},
		{Entrypoint: []string{"app"}, SecretReloadSignal: "SIGKILL"},
	} {
		if err := manifest.Validate(); err == nil {
			t.Fatalf("accepted invalid reload configuration: %+v", manifest)
		}
	}
}
