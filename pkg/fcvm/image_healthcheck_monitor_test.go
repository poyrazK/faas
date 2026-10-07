// adr:684
package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

func TestImageHealthcheckMonitorConsecutiveFailuresAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []string
		want     string
		attempts int
	}{
		{"threshold", []string{"unhealthy", "unhealthy", "unhealthy"}, LivenessReasonImageHealthcheck, 3},
		{"transient failure resets", []string{"unhealthy", "", "unhealthy", "unhealthy", "unhealthy"}, LivenessReasonImageHealthcheck, 5},
		{"grace resets", []string{"unhealthy", "starting", "unhealthy", "unhealthy", "unhealthy"}, LivenessReasonImageHealthcheck, 5},
		{"transport breaks command streak", []string{"unhealthy", "transport", "unhealthy", "unhealthy", "unhealthy"}, LivenessReasonImageHealthcheck, 5},
		{"missing proof", []string{"transport", "transport", "transport"}, LivenessReasonInfrastructure, 3},
		{"runtime change clears failures", []string{"unhealthy", "runtime_changed", "unhealthy", "unhealthy", "unhealthy"}, LivenessReasonImageHealthcheck, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts, configs := 0, 0
			interval, timeout := 1250*time.Microsecond, 2750*time.Microsecond
			monitor := imageHealthcheckMonitor{
				config: func(context.Context) (healthcheckproto.Config, error) {
					configs++
					return healthcheckproto.Config{RuntimeID: "main", IntervalNS: int64(interval), TimeoutNS: int64(timeout), Retries: 3}, nil
				},
				probe: func(ctx context.Context, runtime string) (healthcheckproto.Response, error) {
					if runtime != "main" {
						t.Fatal("wrong runtime")
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > timeout+api.ImageHealthcheckTransportAllowance {
						t.Fatal("command timeout was not preserved")
					}
					if attempts >= len(tc.outcomes) {
						t.Fatal("threshold never reached")
					}
					outcome := tc.outcomes[attempts]
					attempts++
					if outcome == "transport" {
						return healthcheckproto.Response{}, errors.New("missing proof")
					}
					return healthcheckproto.Response{Healthy: outcome == "", Error: outcome, NextIntervalNS: int64(interval)}, nil
				},
				wait: func(_ context.Context, delay time.Duration) bool {
					if delay != 0 && delay != interval && delay != api.ImageHealthcheckTransportRetryInterval {
						t.Fatalf("lost exact interval: %s", delay)
					}
					return true
				},
			}
			if reason := monitor.run(t.Context()); reason != tc.want || attempts != tc.attempts {
				t.Fatalf("reason=%s attempts=%d", reason, attempts)
			}
			if tc.name == "runtime change clears failures" && configs != 2 {
				t.Fatal("runtime configuration was reused")
			}
		})
	}
}

func TestImageHealthcheckMonitorFreshWireProof(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt string
		want    string
	}{
		{"fresh failures", "", LivenessReasonImageHealthcheck},
		{"stale nonce", "nonce", LivenessReasonInfrastructure},
		{"wrong runtime", "runtime", LivenessReasonInfrastructure},
		{"contradictory result", "healthy", LivenessReasonInfrastructure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			nonces := map[string]bool{}
			v, lease := imageHealthcheckTestSocket(t, func(kind uint32, req healthcheckproto.Request) (uint32, any) {
				mu.Lock()
				defer mu.Unlock()
				if nonces[req.Nonce] {
					t.Error("reused host challenge")
				}
				nonces[req.Nonce] = true
				if kind == healthcheckproto.ConfigProbe {
					return healthcheckproto.ConfigAck, healthcheckproto.Config{Nonce: req.Nonce, RuntimeID: "main", IntervalNS: int64(time.Millisecond), TimeoutNS: int64(time.Second), Retries: 3}
				}
				response := healthcheckproto.Response{Nonce: req.Nonce, RuntimeID: req.RuntimeID, Error: "unhealthy", NextIntervalNS: int64(time.Millisecond)}
				switch tc.corrupt {
				case "nonce":
					response.Nonce = "old-pass"
				case "runtime":
					response.RuntimeID = "predecessor"
				case "healthy":
					response.Healthy = true
				}
				return healthcheckproto.CheckAck, response
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if got := RunImageHealthcheckMonitor(ctx, v.VsockUDSSocketPath(lease.Instance), nil); got != tc.want {
				t.Fatalf("reason=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestImageHealthcheckMonitorCancellationAndLegacyGuest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := RunImageHealthcheckMonitor(ctx, "unused", nil); got != "" {
		t.Fatal("shutdown requested recovery")
	}
	v, lease := imageHealthcheckTestSocket(t, func(_ uint32, req healthcheckproto.Request) (uint32, any) {
		return healthcheckproto.Ack, healthcheckproto.Response{Nonce: req.Nonce, Healthy: true}
	})
	if err := v.WaitImageHealthcheck(t.Context(), lease, 0); err == nil {
		t.Fatal("legacy guest without runtime monitoring passed readiness")
	}
	if got := imageHealthcheckProbeBudget(time.Duration(1<<63 - 1)); got <= 0 {
		t.Fatal("probe budget overflow")
	}
}
