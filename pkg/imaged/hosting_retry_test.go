// adr: 460 — publication recovery replays the same candidate across restarts.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingProgress(t *testing.T, f *hostingReplay) state.HostingVerificationProgress {
	t.Helper()
	dep, err := f.store.DeploymentByID(context.Background(), f.candidate.ID)
	var stages state.StageState
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(dep.StageState, &stages); err != nil || stages.HostingVerification == nil {
		t.Fatalf("missing durable recovery progress: stage=%s err=%v", dep.StageState, err)
	}
	return *stages.HostingVerification
}

func TestHostingPublicationRecoverySurvivesHandlerRestart(t *testing.T) {
	f := hostingReplayFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	f.handler.hostingVerificationNow = clock
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(apihostingreceipt.PlatformSmokeDeploymentHeader) != f.candidate.ID {
			t.Error("recovery probed another candidate")
		}
		w.Header().Set(apihostingreceipt.ServedDeploymentHeader, f.candidate.ID)
		w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(f.candidate.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	publications := 0
	var secret string
	verifier := apihostingreceipt.Verifier{BaseURL: srv.URL, Timeout: time.Second,
		Authorize: func(_ context.Context, _, token string, _ time.Time) error {
			publications++
			secret = token
			if publications == 1 {
				return fmt.Errorf("publisher unavailable token=%s", token)
			}
			return nil
		},
	}
	smoke := func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
		return VerifyHostingDeployment(ctx, verifier, app, dep)
	}
	f.handler.WithHostingSmoke(smoke)
	err := f.handler.HandleNotification(ctx, f.notification)
	var publication *apihostingreceipt.ChallengePublicationError
	if !errors.As(err, &publication) || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe/non-replayable publication failure: %v", err)
	}
	f.assertReplayable(t)
	first := hostingProgress(t, f)
	if first.Attempts != 1 || first.LastErrorCode != apihostingreceipt.SmokeErrorAuthorizationUnavailable || first.RetryNotBefore == nil || requests.Load() != 0 {
		t.Fatalf("first progress=%+v requests=%d", first, requests.Load())
	}
	if err := f.handler.HandleNotification(ctx, f.notification); !errors.Is(err, state.ErrHostingVerificationDeferred) || publications != 1 {
		t.Fatalf("early replay ignored eligibility: publications=%d err=%v", publications, err)
	}
	// A new process reconstructs its handler; all retry state comes from storage.
	now = *first.RetryNotBefore
	f.handler = New(f.store, f.notif, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmokeRequired(true).WithHostingSmoke(smoke)
	f.handler.hostingVerificationNow = clock
	if err := f.handler.HandleNotification(ctx, f.notification); err != nil {
		t.Fatal(err)
	}
	live, err := f.store.LiveDeploymentForScope(ctx, f.candidate.AppID, state.DefaultEnvScope)
	last := hostingProgress(t, f)
	if err != nil || live.ID != f.candidate.ID || publications != 2 || requests.Load() != 1 || f.notif.failed != 0 || last.Attempts != 2 || !last.DeadlineAt.Equal(first.DeadlineAt) || last.LastErrorCode != "" || last.RetryNotBefore != nil || last.CompletedAt == nil {
		t.Fatalf("recovery did not promote same candidate: live=%s progress=%+v publications=%d requests=%d events=%d err=%v", live.ID, last, publications, requests.Load(), f.notif.failed, err)
	}
	receipt, err := apihostingreceipt.Decode(live.APIHostingReceipt)
	if err != nil || receipt.Smoke.Status != apihostingreceipt.SmokeVerified || receipt.Smoke.StatusCode != http.StatusNotFound || receipt.Smoke.Authentication != apihostingreceipt.AuthenticationPlatformChallenge {
		t.Fatalf("recovery did not require candidate evidence: %+v err=%v", receipt.Smoke, err)
	}
}

func unavailablePublication(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
	err := &apihostingreceipt.ChallengePublicationError{Cause: errors.New("publisher unavailable")}
	return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped, ErrorCode: apihostingreceipt.SmokeErrorAuthorizationUnavailable, Error: err.Error()}, err
}

func TestHostingPublicationDeadlineFinalizesPlatformFailureAtomically(t *testing.T) {
	f := hostingReplayFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.handler.hostingVerificationNow = func() time.Time { return now }
	probes := 0
	f.handler.WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
		probes++
		return unavailablePublication(ctx, app, dep)
	})
	if err := f.handler.HandleNotification(ctx, f.notification); err == nil {
		t.Fatal("publication outage acknowledged")
	}
	f.assertReplayable(t)
	first := hostingProgress(t, f)
	now = first.DeadlineAt
	// Even the terminal platform verdict remains replayable on a commit outage.
	outage := errors.New("failure commit unavailable")
	f.store.failureErr = outage
	if err := f.handler.HandleNotification(ctx, f.notification); !errors.Is(err, outage) {
		t.Fatalf("commit outage not replayable: %v", err)
	}
	f.assertReplayable(t)
	if err := f.handler.HandleNotification(ctx, f.notification); err == nil {
		t.Fatal("expired recovery did not fail candidate")
	}
	dep, _ := f.store.DeploymentByID(ctx, f.candidate.ID)
	receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
	live, liveErr := f.store.LiveDeploymentForScope(ctx, dep.AppID, state.DefaultEnvScope)
	if err != nil || liveErr != nil || dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeDeploymentVerificationUnavailable || receipt.Smoke.ErrorCode != apihostingreceipt.SmokeErrorVerificationUnavailable || live.ID != f.previous.ID || probes != 1 || f.notif.failed != 1 || f.notif.commitErr != nil || hostingProgress(t, f).Attempts != first.Attempts {
		t.Fatalf("incorrect expiry verdict: dep=%+v smoke=%+v live=%s probes=%d events=%d commit=%v err=%v liveErr=%v", dep, receipt.Smoke, live.ID, probes, f.notif.failed, f.notif.commitErr, err, liveErr)
	}
	if err := f.handler.HandleNotification(ctx, f.notification); err != nil || f.notif.failed != 1 {
		t.Fatalf("terminal replay repeated failure: err=%v events=%d", err, f.notif.failed)
	}
}

func TestHostingPublicationRecoveryPreservesAppHealthFailure(t *testing.T) {
	f := hostingReplayFixture(t)
	now := time.Now().UTC()
	f.handler.hostingVerificationNow = func() time.Time { return now }
	probes := 0
	f.handler.WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
		probes++
		if probes == 1 {
			return unavailablePublication(ctx, app, dep)
		}
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, StatusCode: http.StatusServiceUnavailable, ErrorCode: "smoke_status_failed", Error: "application returned 503"}, nil
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); err == nil {
		t.Fatal("outage acknowledged")
	}
	now = *hostingProgress(t, f).RetryNotBefore
	if err := f.handler.HandleNotification(context.Background(), f.notification); err == nil {
		t.Fatal("application health failure retried")
	}
	dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
	if dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeDeploymentSmokeFailed || probes != 2 || f.notif.failed != 1 || hostingProgress(t, f).LastErrorCode != "" {
		t.Fatalf("app failure misclassified: status=%s code=%s probes=%d events=%d", dep.Status, dep.ErrorCode, probes, f.notif.failed)
	}
}

func TestHostingPublicationFailureCannotOverwriteCancellation(t *testing.T) {
	f := hostingReplayFixture(t)
	f.handler.WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
		if err := f.store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, "customer cancelled"); err != nil {
			t.Fatal(err)
		}
		return unavailablePublication(ctx, app, dep)
	})
	if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
		t.Fatal(err)
	}
	dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
	if dep.Status != state.DeployCancelled || len(dep.APIHostingReceipt) != 0 || f.notif.failed != 0 || hostingProgress(t, f).LastErrorCode != "" {
		t.Fatalf("publication failure replaced cancellation: status=%s receipt=%s events=%d", dep.Status, dep.APIHostingReceipt, f.notif.failed)
	}
}

type hostingProgressFaultStore struct {
	*hostingFailureFaultStore
	action state.HostingVerificationAction
	err    error
}

func (s *hostingProgressFaultStore) UpdateDeploymentHostingVerification(ctx context.Context, id string, u state.HostingVerificationUpdate) (state.HostingVerificationProgress, error) {
	if u.Action == s.action && s.err != nil {
		err := s.err
		s.err = nil
		return state.HostingVerificationProgress{}, err
	}
	return s.MemStore.UpdateDeploymentHostingVerification(ctx, id, u)
}

func TestHostingProgressPersistenceOutageRemainsReplayable(t *testing.T) {
	for _, action := range []state.HostingVerificationAction{state.HostingVerificationBegin, state.HostingVerificationRetry, state.HostingVerificationComplete} {
		t.Run(string(action), func(t *testing.T) {
			f := hostingReplayFixture(t)
			outage := errors.New("progress commit unavailable")
			f.handler.store = &hostingProgressFaultStore{hostingFailureFaultStore: f.store, action: action, err: outage}
			probes := 0
			f.handler.WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
				probes++
				if action == state.HostingVerificationRetry && probes == 1 {
					return unavailablePublication(ctx, app, dep)
				}
				return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: http.StatusOK}, nil
			})
			if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, outage) {
				t.Fatalf("progress outage not returned to consumer: %v", err)
			}
			f.assertReplayable(t)
			if action == state.HostingVerificationBegin && probes != 0 {
				t.Fatal("probe sent without durable attempt")
			}
			if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
				t.Fatal(err)
			}
			live, err := f.store.LiveDeploymentForScope(context.Background(), f.candidate.AppID, state.DefaultEnvScope)
			if err != nil || live.ID != f.candidate.ID || f.notif.failed != 0 {
				t.Fatalf("progress outage stranded candidate: live=%s err=%v", live.ID, err)
			}
		})
	}
}
