package main

// adr: 20261020214632

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// The control plane's API check reaches apid through the control-plane proxy
// and passes on apid's answer to a request without credentials; a compute
// edge checks readiness only.
func TestHandoffChecksReachAPIDThroughTheProxy(t *testing.T) {
	checks := handoffChecks("127.0.0.1:8080", "127.0.0.1:9092", false)
	if len(checks) != 2 || checks[0].Path != "/readyz" || checks[0].Address != "127.0.0.1:9092" {
		t.Fatalf("checks = %+v, want readyz on the control listener first", checks)
	}
	apiCheck := checks[1]
	if apiCheck.Address != "127.0.0.1:8080" {
		t.Fatalf("api check goes to %s, want the public listener", apiCheck.Address)
	}
	var reached string
	apid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(apid.Close)
	handler, err := newControlPlaneProxy(apid.URL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the check request went to the compute data plane: %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
	}), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+apiCheck.Host+apiCheck.Path, nil)
	req.RemoteAddr = "127.0.0.1:0" // what a check connection reports
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if reached != "/v1/apps" || !slices.Contains(apiCheck.Want, rec.Code) {
		t.Fatalf("check request reached %q and answered %d, want apid's /v1/apps and one of %v", reached, rec.Code, apiCheck.Want)
	}

	edge := handoffChecks("127.0.0.1:8080", "127.0.0.1:9092", true)
	if len(edge) != 1 || edge[0].Name != "readyz" {
		t.Fatalf("compute edge checks = %+v, want readiness only", edge)
	}
}
