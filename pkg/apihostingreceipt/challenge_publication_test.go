// adr: 460 — publication outages are replayable platform failures.
package apihostingreceipt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChallengePublicationFailureDoesNotProbeOrExposeSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("candidate requested without published authorization")
	}))
	defer srv.Close()
	cause := errors.New("publisher unavailable")
	var token string
	v := Verifier{BaseURL: srv.URL, Authorize: func(_ context.Context, _, secret string, _ time.Time) error {
		token = secret
		return fmt.Errorf("publication failed for token=%s: %w", secret, cause)
	}}
	got, err := v.VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	var publication *ChallengePublicationError
	if !errors.As(err, &publication) || !errors.Is(err, cause) || got.Status != SmokeSkipped || got.ErrorCode != SmokeErrorAuthorizationUnavailable || got.Verification != VerificationRouteConnectivity || got.Authentication != AuthenticationPlatformChallenge {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	raw, marshalErr := json.Marshal(got)
	if marshalErr != nil || token == "" || strings.Contains(string(raw), token) || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("unsafe publication diagnostics: result=%s err=%v marshal=%v", raw, err, marshalErr)
	}
}

func TestChallengePublicationRespectsTimeoutsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		total, request   time.Duration
		deadline, cancel bool
	}{
		{name: "total budget", total: 20 * time.Millisecond},
		{name: "publication request budget", total: time.Second, request: 20 * time.Millisecond},
		{name: "durable deadline", total: time.Second, deadline: true},
		{name: "consumer cancellation", total: time.Second, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.deadline {
				ctx = WithChallengePublicationDeadline(ctx, time.Now().Add(20*time.Millisecond))
			}
			v := Verifier{BaseURL: "http://unused.invalid", Timeout: tc.total, RequestTimeout: tc.request,
				Authorize: func(ctx context.Context, _, _ string, _ time.Time) error {
					if tc.cancel {
						cancel()
					}
					<-ctx.Done()
					return ctx.Err()
				},
			}
			got, err := v.VerifyDeploymentRoute(ctx, "demo", "candidate")
			want := context.DeadlineExceeded
			if tc.cancel {
				want = context.Canceled
			}
			var publication *ChallengePublicationError
			if !errors.As(err, &publication) || !errors.Is(err, want) || got.Status != SmokeSkipped {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestRecoveredPublicationDoesNotShortenProbeBudget(t *testing.T) {
	// The publication deadline expires before the response, but publication
	// itself succeeds in time. The request retains its own verifier budget.
	deadlineExpired := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-deadlineExpired
		w.Header().Set(ServedDeploymentHeader, "candidate")
		w.Header().Set(ServedResponseHeader, CandidateResponseProof("candidate", r.Header.Get(PlatformSmokeTokenHeader)))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	deadline, _ := deadlineCtx.Deadline()
	go func() { <-deadlineCtx.Done(); close(deadlineExpired) }()
	v := Verifier{BaseURL: srv.URL, Timeout: time.Second, Authorize: allowTestSmoke}
	got, err := v.VerifyDeploymentRoute(WithChallengePublicationDeadline(context.Background(), deadline), "demo", "candidate")
	if err != nil || got.Status != SmokeVerified || got.StatusCode != http.StatusNotFound {
		t.Fatalf("recovered publication constrained candidate probe: result=%+v err=%v", got, err)
	}
}

func TestMissingChallengePublisherRemainsConfigurationFailure(t *testing.T) {
	got, err := (Verifier{BaseURL: "http://unused.invalid"}).VerifyDeploymentRoute(context.Background(), "demo", "candidate")
	if err != nil || got.Status != SmokeFailed || got.ErrorCode != SmokeErrorAuthorizationFailed {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}
