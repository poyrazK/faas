// adr: 461 — only authenticated candidate responses establish app verdicts.
package apihostingreceipt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCandidateProofControlsFailureAttribution(t *testing.T) {
	for _, contract := range []string{VerificationHTTPHealth, VerificationRouteConnectivity} {
		for _, status := range []int{200, 204, 302, 401, 403, 404, 429, 500, 502, 503, 504} {
			for _, proven := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/proven=%v", contract, status, proven), func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set(ServedDeploymentHeader, "candidate")
						if proven {
							w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
						}
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"code":"capacity","detail":"private app response"}`))
					}))
					defer srv.Close()
					v := Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}
					got, err := v.verifyWithContract(context.Background(), "demo", "/", "candidate", contract)
					if !proven {
						wantCode := SmokeErrorResponseUnproven
						if status >= 400 {
							wantCode = SmokeErrorGatewayUnavailable
						}
						if VerificationRecoveryCode(err) != wantCode || got.Status != SmokeSkipped || got.ErrorCode != wantCode || got.StatusCode != status {
							t.Fatalf("unproven response became an app verdict: result=%+v err=%v", got, err)
						}
					} else {
						want := SmokeFailed
						if status < 300 || (contract == VerificationRouteConnectivity && (status < 400 || status == 401 || status == 403 || status == 404)) {
							want = SmokeVerified
						}
						if err != nil || got.Status != want || got.StatusCode != status {
							t.Fatalf("authenticated app verdict changed: result=%+v err=%v want=%s", got, err, want)
						}
					}
					if strings.Contains(got.Error, "private app response") {
						t.Fatal("response body escaped into diagnostics")
					}
				})
			}
		}
	}
}

type verificationRoundTripper func(*http.Request) (*http.Response, error)

func (fn verificationRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestVerificationTransportFailureIsUnattributedAndSecretSafe(t *testing.T) {
	for _, contract := range []string{VerificationHTTPHealth, VerificationRouteConnectivity} {
		t.Run(contract, func(t *testing.T) {
			var cause error
			client := &http.Client{Transport: verificationRoundTripper(func(r *http.Request) (*http.Response, error) {
				cause = fmt.Errorf("connection reset token=%s", r.Header.Get(PlatformSmokeTokenHeader))
				return nil, cause
			})}
			v := Verifier{BaseURL: "http://example.test", Client: client, Authorize: allowTestSmoke}
			got, err := v.verifyWithContract(context.Background(), "demo", "/health", "candidate", contract)
			if VerificationRecoveryCode(err) != SmokeErrorTransportUnavailable || !errors.Is(err, cause) || got.Status != SmokeSkipped || strings.Contains(err.Error(), "token=") || strings.Contains(got.Error, "token=") {
				t.Fatalf("unsafe/attributed transport verdict: result=%+v err=%v", got, err)
			}
		})
	}
}

func TestHTTPHealthCannotVerifyTruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	got, err := (Verifier{BaseURL: srv.URL, Authorize: allowTestSmoke}).VerifyDeployment(context.Background(), "demo", "/health", "candidate")
	if got.Status != SmokeSkipped || VerificationRecoveryCode(err) != SmokeErrorTransportUnavailable {
		t.Fatalf("incomplete health response verified: result=%+v err=%v", got, err)
	}
}

func TestHTTPHealthDoesNotFollowCandidateRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("candidate health redirect was followed")
	}))
	defer destination.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer srv.Close()
	client := &http.Client{}
	got, err := (Verifier{BaseURL: srv.URL, Client: client, Authorize: allowTestSmoke}).VerifyDeployment(context.Background(), "demo", "/health", "candidate")
	if err != nil || got.Status != SmokeFailed || got.StatusCode != http.StatusFound || client.CheckRedirect != nil {
		t.Fatalf("health redirect changed contract: result=%+v err=%v", got, err)
	}
}

func TestVerificationRecoveryDeadlineBoundsMissingEvidence(t *testing.T) {
	client := &http.Client{Transport: verificationRoundTripper(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	deadline := time.Now().Add(50 * time.Millisecond)
	v := Verifier{BaseURL: "http://example.test", Client: client, Timeout: time.Second, Authorize: allowTestSmoke}
	got, err := v.VerifyDeployment(WithVerificationRecoveryDeadline(context.Background(), deadline), "demo", "/health", "candidate")
	if VerificationRecoveryCode(err) != SmokeErrorTransportUnavailable || !errors.Is(err, context.DeadlineExceeded) || got.Status != SmokeSkipped {
		t.Fatalf("recovery deadline not returned as unavailable: result=%+v err=%v", got, err)
	}
}

func TestProvenAppVerdictRetainsOrdinaryProbeBudgetDuringRecovery(t *testing.T) {
	var firstDeadline time.Time
	calls := 0
	client := &http.Client{Transport: verificationRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("probe has no deadline")
		}
		if calls == 1 {
			firstDeadline = deadline
		} else if !deadline.After(firstDeadline) {
			t.Error("proven app-health retry retained the shorter unavailable-verification deadline")
		}
		headers := make(http.Header)
		headers.Set(ServedDeploymentHeader, "candidate")
		headers.Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		status := http.StatusServiceUnavailable
		if calls > 1 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	v := Verifier{BaseURL: "http://example.test", Client: client, Timeout: 2 * time.Second, RetryInterval: time.Millisecond, Authorize: allowTestSmoke}
	got, err := v.VerifyDeployment(WithVerificationRecoveryDeadline(context.Background(), time.Now().Add(time.Second)), "demo", "/health", "candidate")
	if err != nil || got.Status != SmokeVerified || calls != 2 {
		t.Fatalf("proven app did not get ordinary health retries: result=%+v calls=%d err=%v", got, calls, err)
	}
}

func TestRecoveryDeadlineDuringBackoffRetainsGatewayReason(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: verificationRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("gateway unavailable"))}, nil
	})}
	v := Verifier{BaseURL: "http://example.test", Client: client, Timeout: time.Second, RetryInterval: time.Second, Authorize: allowTestSmoke}
	got, err := v.VerifyDeployment(WithVerificationRecoveryDeadline(context.Background(), time.Now().Add(50*time.Millisecond)), "demo", "/health", "candidate")
	if VerificationRecoveryCode(err) != SmokeErrorGatewayUnavailable || got.StatusCode != http.StatusServiceUnavailable || calls != 1 {
		t.Fatalf("deadline replaced actual gateway reason: result=%+v calls=%d err=%v", got, calls, err)
	}
}

func TestOrdinaryProbeBudgetRetainsLastCandidateVerdict(t *testing.T) {
	for _, proven := range []bool{false, true} {
		t.Run(fmt.Sprintf("proven=%v", proven), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: verificationRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Context().Err() != nil {
					t.Error("probe started after its budget expired")
				}
				headers := make(http.Header)
				if proven {
					headers.Set(ServedDeploymentHeader, "candidate")
					headers.Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
				}
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: headers, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			})}
			v := Verifier{BaseURL: "http://example.test", Client: client, Timeout: 50 * time.Millisecond, RetryInterval: 50 * time.Millisecond, Authorize: allowTestSmoke}
			got, err := v.VerifyDeployment(context.Background(), "demo", "/health", "candidate")
			if got.StatusCode != http.StatusServiceUnavailable || calls != 1 || (proven && (err != nil || got.Status != SmokeFailed)) || (!proven && VerificationRecoveryCode(err) != SmokeErrorGatewayUnavailable) {
				t.Fatalf("expired budget replaced last verdict: result=%+v calls=%d err=%v", got, calls, err)
			}
		})
	}
}

func TestInvalidSmokeOriginIsTerminalConfigurationFailure(t *testing.T) {
	for _, origin := range []string{"example.test", "ftp://example.test", "http://%invalid"} {
		v := Verifier{BaseURL: origin, Authorize: func(context.Context, string, string, time.Time) error {
			t.Error("invalid origin published a challenge")
			return nil
		}}
		got, err := v.VerifyDeployment(context.Background(), "demo", "/health", "candidate")
		if err != nil || got.Status != SmokeFailed || got.ErrorCode != SmokeErrorVerifierNotConfigured {
			t.Fatalf("configuration failure became retryable: result=%+v err=%v", got, err)
		}
	}
}
