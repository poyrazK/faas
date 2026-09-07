package apihostingreceipt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

func TestReceiptRoundTrip(t *testing.T) {
	want := Receipt{SchemaVersion: SchemaVersion, DeploymentID: "dep-1", AppID: "app-1", Profile: frameworkprofile.Profile{Version: frameworkprofile.Version, HealthPath: "/healthz"}, Smoke: SmokeResult{Status: SmokeVerified, Path: "/healthz"}}
	raw, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeploymentID != want.DeploymentID || got.Smoke.Status != SmokeVerified {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestVerifierSuccessAndHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" || r.Host != "demo.apps.example" || r.Header.Get("X-Gregale-Platform-Smoke") != "1" {
			t.Fatalf("request shape: host=%q path=%q header=%q", r.Host, r.URL.Path, r.Header.Get("X-Gregale-Platform-Smoke"))
		}
		w.Header().Set("X-Request-ID", "req-1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL, AppsDomain: "apps.example"}).Verify(context.Background(), "demo", "/ready")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeVerified || got.StatusCode != http.StatusNoContent || got.RequestID != "req-1" {
		t.Fatalf("unexpected smoke result: %#v", got)
	}
}

func TestVerifierFailureDoesNotPersistBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret response body", http.StatusBadGateway)
	}))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL}).Verify(context.Background(), "demo", "health")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeFailed || got.ErrorCode != "smoke_http_status" || got.Error != "health probe returned HTTP 502" {
		t.Fatalf("unexpected smoke result: %#v", got)
	}
}
