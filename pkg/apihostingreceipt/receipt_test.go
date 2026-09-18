package apihostingreceipt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestReceiptAllowsEmptyHealthPathForTCPReadiness(t *testing.T) {
	receipt := Receipt{
		SchemaVersion: SchemaVersion,
		DeploymentID:  "dep-tcp",
		AppID:         "app-tcp",
		Profile:       frameworkprofile.Profile{Version: frameworkprofile.Version},
		Smoke:         SmokeResult{Status: SmokeSkipped},
	}
	if _, err := Encode(receipt); err != nil {
		t.Fatalf("Encode TCP-readiness receipt: %v", err)
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

func TestVerifierRetriesGatewayRoutePropagation(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 3 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Request-ID", "candidate-request")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	got, err := (Verifier{
		BaseURL: srv.URL, Timeout: time.Second, RetryInterval: time.Millisecond,
	}).Verify(context.Background(), "demo", "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeVerified || got.RequestID != "candidate-request" || requests != 3 {
		t.Fatalf("result=%+v requests=%d", got, requests)
	}
}

func TestVerifierRequiredWithoutBaseURLFailsClosed(t *testing.T) {
	got, err := (Verifier{Required: true}).Verify(context.Background(), "demo", "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.ErrorCode != SmokeErrorVerifierNotConfigured {
		t.Fatalf("error_code = %q, want %q", got.ErrorCode, SmokeErrorVerifierNotConfigured)
	}
	if got.Error == "" || got.Path != "/healthz" || !got.VerifiedAt.IsZero() {
		t.Fatalf("unexpected required-missing result: %+v", got)
	}
}

func TestVerifierOptionalWithoutBaseURLKeepsCompatibilitySkip(t *testing.T) {
	got, err := (Verifier{}).Verify(context.Background(), "demo", "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeSkipped || got.ErrorCode != SmokeErrorNotConfigured {
		t.Fatalf("unexpected optional-missing result: %+v", got)
	}
}

func TestVerifierDeploymentRequiresAuthorizedMatchingCandidate(t *testing.T) {
	requests := 0
	var authorizedToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get(PlatformSmokeTokenHeader) != authorizedToken || r.Header.Get(PlatformSmokeDeploymentHeader) != "dep-new" {
			t.Fatalf("unauthorized request headers: %#v", r.Header)
		}
		if requests == 1 {
			w.Header().Set(ServedDeploymentHeader, "dep-old")
		} else {
			w.Header().Set(ServedDeploymentHeader, "dep-new")
			w.Header().Set("X-Faas-Request-ID", "faas-request")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	verifier := Verifier{
		BaseURL: srv.URL, Timeout: time.Second, RetryInterval: time.Millisecond,
		Authorize: func(_ context.Context, deploymentID, token string, expiresAt time.Time) error {
			if deploymentID != "dep-new" || !expiresAt.After(time.Now()) {
				t.Fatalf("authorization = deployment %q expires %s", deploymentID, expiresAt)
			}
			authorizedToken = token
			return nil
		},
	}
	got, err := verifier.VerifyDeployment(context.Background(), "demo", "/healthz", "dep-new")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeVerified || got.DeploymentID != "dep-new" || got.RequestID != "faas-request" || requests != 2 {
		t.Fatalf("result=%+v requests=%d", got, requests)
	}
}
