package apihostingreceipt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVerifyDeploymentAPIRoutesRequiresCandidateProofAndAcceptsReadOnlyResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusNoContent, SmokeVerified},
		{http.StatusFound, SmokeVerified},
		{http.StatusUnauthorized, SmokeVerified},
		{http.StatusForbidden, SmokeVerified},
		{http.StatusNotFound, SmokeFailed},
		{http.StatusInternalServerError, SmokeFailed},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/status" {
					t.Errorf("request = %s %s, want GET /v1/status", r.Method, r.URL.Path)
				}
				w.Header().Set(ServedDeploymentHeader, "candidate")
				w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			verifier := Verifier{BaseURL: server.URL, Authorize: allowTestSmoke}
			checks, err := verifier.VerifyDeploymentAPIRoutes(context.Background(), "demo", "candidate", []APIRouteProbe{{Method: "GET", Path: "/v1/status"}})
			if err != nil || len(checks) != 1 || checks[0].Status != tc.want || checks[0].StatusCode != tc.status {
				t.Fatalf("checks=%+v err=%v, want status %s", checks, err, tc.want)
			}
		})
	}
}

func TestVerifyDeploymentAPIRoutesStopsAfterRequiredRouteFails(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		if r.URL.Path == "/a-missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checks, err := (Verifier{BaseURL: server.URL, Authorize: allowTestSmoke}).VerifyDeploymentAPIRoutes(context.Background(), "demo", "candidate", []APIRouteProbe{
		{Method: "GET", Path: "/z-later"},
		{Method: "GET", Path: "/a-missing"},
		{Method: "GET", Path: "/zz-never"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/a-missing"}) {
		t.Fatalf("probed paths=%v, want only failed route", paths)
	}
	if len(checks) != 3 || checks[0].Path != "/a-missing" || checks[0].Status != SmokeFailed || checks[1].Path != "/z-later" || checks[1].Status != SmokeSkipped || checks[2].Path != "/zz-never" || checks[2].Status != SmokeSkipped {
		t.Fatalf("checks=%+v, want failed route and explicit unrun route", checks)
	}
}

func TestValidateAPIRouteProbeRejectsUnsafeOrStateChangingRoutes(t *testing.T) {
	for _, probe := range []APIRouteProbe{
		{Method: "POST", Path: "/v1/charge"},
		{Method: "GET", Path: "https://attacker.test/path"},
		{Method: "GET", Path: "//attacker.test/path"},
		{Method: "GET", Path: "/v1/items/{id}"},
		{Method: "GET", Path: "/v1/items?include=all"},
		{Method: "GET", Path: "/v1/items#details"},
		{Method: "GET", Path: "/v1/../admin"},
		{Method: "GET", Path: "/v1/%2e%2e/admin"},
		{Method: "GET", Path: "/v1%2fadmin"},
	} {
		if err := ValidateAPIRouteProbe(probe); err == nil {
			t.Errorf("unsafe route accepted: %+v", probe)
		}
	}
	if err := ValidateAPIRouteProbe(APIRouteProbe{Method: "GET", Path: "/v1/status"}); err != nil {
		t.Fatalf("valid route rejected: %v", err)
	}
}

func TestSafeSmokeRequestIDBoundsUntrustedHeaders(t *testing.T) {
	if got := safeSmokeRequestID(strings.Repeat("a", 256)); len(got) != 128 {
		t.Fatalf("request ID length=%d, want 128", len(got))
	}
	if got := safeSmokeRequestID("valid\nforged"); got != "" {
		t.Fatalf("control characters survived in request ID: %q", got)
	}
}

func TestVerifyDeploymentAPIRoutesClassifiesUnprovenGatewayFailureAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	verifier := Verifier{BaseURL: server.URL, Timeout: 50 * time.Millisecond, RequestTimeout: time.Second, Authorize: allowTestSmoke}
	checks, err := verifier.VerifyDeploymentAPIRoutes(context.Background(), "demo", "candidate", []APIRouteProbe{{Method: "GET", Path: "/v1/status"}})
	if VerificationRecoveryCode(err) != SmokeErrorGatewayUnavailable || len(checks) != 1 || checks[0].Status != SmokeSkipped {
		t.Fatalf("checks=%+v err=%v, want retryable gateway evidence", checks, err)
	}
}
