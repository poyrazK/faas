package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// production-us hunt #8: githubd answered /readyz, so github-deploys was
// advertised as available, but no build-enqueue bridge socket was configured
// and githubd's stub refused every push. Availability now needs the bridge.
func TestGitHubDeploysAvailabilityNeedsTheEnqueueBridge(t *testing.T) {
	githubd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer githubd.Close()
	getenv := func(key string) string {
		if key == "FAAS_GITHUBD_LOOPBACK" {
			return githubd.URL
		}
		return ""
	}
	if githubDeploysAvailabilityProbe(getenv, "")(context.Background()) {
		t.Fatal("github-deploys advertised without a build-enqueue bridge")
	}
	if !githubDeploysAvailabilityProbe(getenv, "/run/faas/apid-githubd.sock")(context.Background()) {
		t.Fatal("github-deploys unavailable with a bridge and a ready githubd")
	}
}
