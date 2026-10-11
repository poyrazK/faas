package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestRolloutHeldNotice — production-us `deployment wait --rollout` stayed
// silent for ten minutes while meterd held a canary at step 1. The wait now
// prints one notice per step once the step has held for five minutes, and
// another if the next step also holds.
func TestRolloutHeldNotice(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	started := now.Add(-6 * time.Minute)
	d := api.DeploymentResponse{ID: "dep-1", Status: statusLive, RolloutState: "progressing", CanaryStep: 0, CanaryTotalSteps: 3, TrafficPercent: 10, CanaryStepStartedAt: &started}
	var out bytes.Buffer
	var n rolloutHeldNotice

	young := now.Add(-time.Minute)
	n.maybeWarn(&out, api.DeploymentResponse{ID: "dep-1", Status: statusLive, CanaryTotalSteps: 3, CanaryStepStartedAt: &young}, now)
	if out.Len() != 0 {
		t.Fatalf("notice before the hold threshold: %s", out.String())
	}
	n.maybeWarn(&out, d, now)
	n.maybeWarn(&out, d, now.Add(time.Minute))
	if got := out.String(); strings.Count(got, "Rollout held") != 1 || !strings.Contains(got, "10% traffic (step 1/3) for 6m0s") {
		t.Fatalf("held notice:\n%s", got)
	}
	next := now.Add(-7 * time.Minute)
	d.CanaryStep, d.TrafficPercent, d.CanaryStepStartedAt = 1, 50, &next
	n.maybeWarn(&out, d, now)
	if got := out.String(); strings.Count(got, "Rollout held") != 2 || !strings.Contains(got, "50% traffic (step 2/3)") {
		t.Fatalf("second step notice:\n%s", got)
	}
	d.RolloutState = rolloutStateComplete
	before := out.Len()
	n.maybeWarn(&out, d, now.Add(time.Hour))
	if out.Len() != before {
		t.Fatal("notice for a completed rollout")
	}
}

// production-us hunt #6 (H5-60): `gregale deploy --safe` printed nothing for
// 30 minutes while meterd held a scale-to-zero app's canary at its first step
// for lack of request samples, then reported the automatic abort. The deploy
// wait now prints the same held notice as `deployment wait --rollout`.
func TestWaitForDeploymentRolloutReportsAHeldStep(t *testing.T) {
	started := time.Now().Add(-6 * time.Minute)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		d := api.DeploymentResponse{ID: "dep-1", Status: statusLive, RolloutState: "progressing",
			CanaryTotalSteps: 4, TrafficPercent: 5, CanaryStepStartedAt: &started}
		if calls.Add(1) > 1 {
			d.RolloutState, d.RolloutAbortedReason = rolloutStateAborted, "automatic abort: rollout stuck"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	}))
	defer srv.Close()
	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	pending := api.DeploymentResponse{ID: "dep-1", Status: statusLive, RolloutState: "progressing", CanaryTotalSteps: 4, CanaryStepStartedAt: &started}
	got, ok := waitForDeploymentRollout(t.Context(), NewClient(srv.URL, "fp_test"), pending)
	if !ok || got.RolloutState != rolloutStateAborted {
		t.Fatalf("wait = %+v, %v; want the aborted rollout", got, ok)
	}
	if out := stderr.String(); strings.Count(out, "Rollout held at 5% traffic (step 1/4)") != 1 || !strings.Contains(out, "Send traffic to the app") {
		t.Fatalf("deploy wait printed no held notice:\n%s", out)
	}
}
