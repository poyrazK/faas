// adr: 461 — unavailable verification replays while genuine app verdicts finalize.
package imaged

import (
	"context"
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

type hostingRecoveryTransport func(*http.Request) (*http.Response, error)

func (fn hostingRecoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestHostingUnavailableVerificationRecoversAfterRestart(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"gateway", apihostingreceipt.SmokeErrorGatewayUnavailable},
		{"transport", apihostingreceipt.SmokeErrorTransportUnavailable},
		{"missing proof", apihostingreceipt.SmokeErrorResponseUnproven},
		{"stale deployment", apihostingreceipt.SmokeErrorDeploymentMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := hostingReplayFixture(t)
			var recovered atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id := f.candidate.ID
				status := http.StatusOK
				prove := recovered.Load()
				if !prove && tc.name == "gateway" {
					status = http.StatusServiceUnavailable
				}
				if !prove && tc.name == "stale deployment" {
					id, prove = f.previous.ID, true
				}
				w.Header().Set(apihostingreceipt.ServedDeploymentHeader, id)
				if prove {
					w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(id, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			client := &http.Client{Transport: hostingRecoveryTransport(func(r *http.Request) (*http.Response, error) {
				if !recovered.Load() && tc.name == "transport" {
					return nil, fmt.Errorf("connection reset challenge=%s", r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader))
				}
				return http.DefaultTransport.RoundTrip(r)
			})}
			verifier := apihostingreceipt.Verifier{BaseURL: srv.URL, Client: client, Authorize: func(context.Context, string, string, time.Time) error { return nil }}
			smoke := func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
				return VerifyHostingDeployment(ctx, verifier, app, dep)
			}
			now := time.Now().UTC()
			f.handler.hostingVerificationNow = func() time.Time { return now }
			f.handler.WithHostingSmoke(smoke)
			err := f.handler.HandleNotification(context.Background(), f.notification)
			if apihostingreceipt.VerificationRecoveryCode(err) != tc.code || strings.Contains(err.Error(), "challenge=") {
				t.Fatalf("outage not returned safely: %v", err)
			}
			f.assertReplayable(t)
			first := hostingProgress(t, f)
			if first.LastErrorCode != tc.code || first.Attempts != 1 || first.RetryNotBefore == nil {
				t.Fatalf("wrong durable reason: %+v", first)
			}
			if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, state.ErrHostingVerificationDeferred) {
				t.Fatalf("early retry not deferred: %v", err)
			}
			now = *first.RetryNotBefore
			recovered.Store(true)
			f.handler = New(f.store, f.notif, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmokeRequired(true).WithHostingSmoke(smoke)
			f.handler.hostingVerificationNow = func() time.Time { return now }
			if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil {
				t.Fatal(err)
			}
			live, err := f.store.LiveDeploymentForScope(context.Background(), f.candidate.AppID, state.DefaultEnvScope)
			last := hostingProgress(t, f)
			if err != nil || live.ID != f.candidate.ID || f.notif.failed != 0 || last.Attempts != 2 || !last.DeadlineAt.Equal(first.DeadlineAt) || last.LastErrorCode != "" || last.CompletedAt == nil {
				t.Fatalf("same candidate did not recover: live=%s progress=%+v events=%d err=%v", live.ID, last, f.notif.failed, err)
			}
		})
	}
}

func TestHostingUnavailableDeadlineKeepsReasonAndAtomicVerdict(t *testing.T) {
	for _, code := range []string{apihostingreceipt.SmokeErrorGatewayUnavailable, apihostingreceipt.SmokeErrorTransportUnavailable, apihostingreceipt.SmokeErrorResponseUnproven} {
		t.Run(code, func(t *testing.T) {
			f := hostingReplayFixture(t)
			now := time.Now().UTC()
			f.handler.hostingVerificationNow = func() time.Time { return now }
			probes := 0
			f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
				probes++
				return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped, ErrorCode: code}, &apihostingreceipt.VerificationUnavailableError{Code: code}
			})
			if err := f.handler.HandleNotification(context.Background(), f.notification); apihostingreceipt.VerificationRecoveryCode(err) != code {
				t.Fatalf("outage not replayable: %v", err)
			}
			f.assertReplayable(t)
			first := hostingProgress(t, f)
			now = first.DeadlineAt
			outage := errors.New("verdict commit unavailable")
			f.store.failureErr = outage
			if err := f.handler.HandleNotification(context.Background(), f.notification); !errors.Is(err, outage) {
				t.Fatalf("terminal commit outage not replayable: %v", err)
			}
			f.assertReplayable(t)
			if err := f.handler.HandleNotification(context.Background(), f.notification); err == nil {
				t.Fatal("exhausted verification did not finalize")
			}
			dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
			receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
			last := hostingProgress(t, f)
			if err != nil || dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeDeploymentVerificationUnavailable || receipt.Smoke.ErrorCode != apihostingreceipt.SmokeErrorVerificationUnavailable || last.LastErrorCode != code || !last.DeadlineAt.Equal(first.DeadlineAt) || probes != 1 || f.notif.failed != 1 || f.notif.commitErr != nil {
				t.Fatalf("wrong terminal outcome: dep=%s code=%s progress=%+v probes=%d events=%d err=%v", dep.Status, dep.ErrorCode, last, probes, f.notif.failed, err)
			}
			if err := f.handler.HandleNotification(context.Background(), f.notification); err != nil || f.notif.failed != 1 {
				t.Fatalf("terminal redelivery repeated verdict: events=%d err=%v", f.notif.failed, err)
			}
		})
	}
}

func TestHostingUnavailableCannotOverwriteCancellation(t *testing.T) {
	for _, consumer := range []bool{false, true} {
		t.Run(fmt.Sprintf("consumer=%v", consumer), func(t *testing.T) {
			f := hostingReplayFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.handler.WithHostingSmoke(func(ctx context.Context, _ state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
				if consumer {
					cancel()
				} else if err := f.store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, "customer cancelled"); err != nil {
					t.Fatal(err)
				}
				return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped}, &apihostingreceipt.VerificationUnavailableError{Code: apihostingreceipt.SmokeErrorTransportUnavailable, Cause: context.Canceled}
			})
			err := f.handler.HandleNotification(ctx, f.notification)
			if (consumer && !errors.Is(err, context.Canceled)) || (!consumer && err != nil) {
				t.Fatalf("wrong cancellation disposition: %v", err)
			}
			dep, _ := f.store.DeploymentByID(context.Background(), f.candidate.ID)
			want := state.DeployCancelled
			if consumer {
				want = state.DeploySnapshotting
				f.assertReplayable(t)
			}
			if dep.Status != want || len(dep.APIHostingReceipt) != 0 || f.notif.failed != 0 || hostingProgress(t, f).LastErrorCode != "" {
				t.Fatalf("cancellation replaced by outage: dep=%s receipt=%s events=%d", dep.Status, dep.APIHostingReceipt, f.notif.failed)
			}
		})
	}
}
