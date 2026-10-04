package apihostingreceipt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// adr: 433 — candidate connectivity is distinct from HTTP health.
func TestRouteVerificationContract(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		status                  int
		deployment, proof, want string
	}{
		{"ok", 200, "candidate", "candidate", SmokeVerified},
		{"no content", 204, "candidate", "candidate", SmokeVerified},
		{"redirect", 302, "candidate", "candidate", SmokeVerified},
		{"app auth", 401, "candidate", "candidate", SmokeVerified},
		{"app forbidden", 403, "candidate", "candidate", SmokeVerified},
		{"missing root", 404, "candidate", "candidate", SmokeVerified},
		{"old revision", 200, "old", "old", SmokeSkipped},
		{"old revision missing root", 404, "old", "old", SmokeSkipped},
		{"gateway not found", 404, "", "", SmokeSkipped},
		{"selected but no app response", 404, "candidate", "", SmokeSkipped},
		{"mismatched proof", 200, "candidate", "old", SmokeSkipped},
		{"gateway refusal", 429, "candidate", "", SmokeSkipped},
		{"guest unavailable", 503, "candidate", "candidate", SmokeFailed},
		{"gateway unavailable", 503, "candidate", "", SmokeSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" || r.Header.Get(PlatformSmokeDeploymentHeader) != "candidate" || r.Header.Get(PlatformSmokeTokenHeader) == "" {
					t.Errorf("unexpected probe: %s %v", r.URL.Path, r.Header)
				}
				w.Header().Set(ServedDeploymentHeader, tc.deployment)
				if tc.proof != "" {
					w.Header().Set(ServedResponseHeader, CandidateResponseProof(tc.proof, r.Header.Get(PlatformSmokeTokenHeader)))
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			got, err := (Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
			unavailable := VerificationRecoveryCode(err) != ""
			if unavailable != (tc.want == SmokeSkipped) || (err != nil && !unavailable) || got.Status != tc.want || got.StatusCode != tc.status || got.Verification != VerificationRouteConnectivity || got.Authentication != AuthenticationPlatformChallenge {
				t.Fatalf("result=%+v err=%v, want %s", got, err, tc.want)
			}
		})
	}
}

func allowTestSmoke(context.Context, string, string, time.Time) error { return nil }

func TestRouteVerificationRejectsReplayedProof(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", "previous challenge"))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if VerificationRecoveryCode(err) != SmokeErrorResponseUnproven || got.Status != SmokeSkipped || got.ErrorCode != SmokeErrorResponseUnproven {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestHTTPHealthVerificationRemainsStrict(t *testing.T) {
	for _, status := range []int{200, 204, 401, 403, 404, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(ServedDeploymentHeader, "candidate")
			w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
			w.WriteHeader(status)
		}))
		got, err := (Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeployment(context.Background(), "demo", "/health", "candidate")
		srv.Close()
		want := SmokeFailed
		if status >= 200 && status < 300 {
			want = SmokeVerified
		}
		if err != nil || got.Status != want || got.Verification != VerificationHTTPHealth {
			t.Fatalf("HTTP %d result=%+v err=%v, want %s", status, got, err, want)
		}
	}
}

func TestRouteVerificationDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer destination.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer srv.Close()
	client := &http.Client{}
	got, err := (Verifier{Client: client, BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if err != nil || got.Status != SmokeVerified || redirected || client.CheckRedirect != nil {
		t.Fatalf("result=%+v err=%v redirected=%v", got, err, redirected)
	}
}

func TestRouteVerificationFailsClosedWithoutCandidateOrAuthorizer(t *testing.T) {
	for _, id := range []string{"", "candidate"} {
		got, err := (Verifier{BaseURL: "http://unused.invalid"}).VerifyDeploymentRoute(context.Background(), "demo", id)
		if err != nil || got.Status != SmokeFailed {
			t.Fatalf("result=%+v err=%v", got, err)
		}
	}
}

func TestRouteVerificationTimesOutWithoutResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL, Timeout: 30 * time.Millisecond, Authorize: allowTestSmoke}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if VerificationRecoveryCode(err) != SmokeErrorTransportUnavailable || got.Status != SmokeSkipped || got.ErrorCode != SmokeErrorTransportUnavailable {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestRouteVerificationRejectsTruncatedCandidateResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if VerificationRecoveryCode(err) != SmokeErrorTransportUnavailable || got.Status != SmokeSkipped || got.ErrorCode != SmokeErrorTransportUnavailable {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestReceiptRetainsVerificationContract(t *testing.T) {
	receipt := Receipt{SchemaVersion: SchemaVersion, DeploymentID: "candidate", AppID: "app",
		Profile: frameworkprofile.Profile{Version: frameworkprofile.Version},
		Smoke:   SmokeResult{Status: SmokeVerified, Verification: VerificationRouteConnectivity, Authentication: AuthenticationPlatformChallenge, Path: "/", StatusCode: 404},
	}
	raw, err := Encode(receipt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw)
	if err != nil || got.Smoke != receipt.Smoke {
		t.Fatalf("receipt=%+v err=%v", got, err)
	}
	receipt.Smoke.Verification = "unknown"
	if _, err := Encode(receipt); err == nil {
		t.Fatal("unknown verification accepted")
	}
	receipt.Smoke.Verification = VerificationRouteConnectivity
	receipt.Smoke.Authentication = "unknown"
	if _, err := Encode(receipt); err == nil {
		t.Fatal("unknown authentication accepted")
	}
}
