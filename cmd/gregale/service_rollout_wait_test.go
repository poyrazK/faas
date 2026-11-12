package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func readyRollout(state string, traffic, steps int) api.DeploymentResponse {
	return api.DeploymentResponse{ID: "service-candidate", Status: "live", TrafficPercent: traffic, RolloutState: state, CanaryTotalSteps: steps,
		StageState: json.RawMessage(`{"current":"readiness","history":[{"name":"readiness","status":"completed"}]}`)}
}

func TestServiceRolloutWaitIncludesRoutingAndDrain(t *testing.T) {
	for _, traffic := range []int{0, 100} {
		t.Run(strconv.Itoa(traffic), func(t *testing.T) {
			dep := readyRollout("rolling_out", traffic, 0)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(dep)
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			got, terminal := main.WaitForDeploymentRollout(ctx, api.NewClient(srv.URL, "test").SetCompletionCache(nil), dep)
			if terminal || got.RolloutState != "rolling_out" {
				t.Fatalf("rollout wait returned before handoff closed: terminal=%v release=%+v", terminal, got)
			}
		})
	}
}

func TestRolloutReceiptRequiresCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, state          string
		traffic, steps, exit int
		dark                 bool
	}{
		{"service readiness", "rolling_out", 0, 0, 3, false},
		{"service routing and drain", "rolling_out", 100, 0, 3, false},
		{"service complete", "complete", 100, 0, 0, false},
		{"service aborted", "aborted", 0, 0, 1, false},
		{"canary progress", "rolling_out", 10, 3, 3, false},
		{"canary complete", "complete", 100, 3, 0, false},
		{"canary aborted", "aborted", 0, 3, 1, false},
		{"legacy immediate", "pending", 100, 0, 0, false},
		{"dark release", "pending", 0, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := readyRollout(tc.state, tc.traffic, tc.steps)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(dep)
			}))
			defer srv.Close()
			out, _, restore := main.SwapIO(t)
			defer restore()
			code := main.WriteWaitedDeploymentReceiptUntilWithOptions(t.Context(), api.NewClient(srv.URL, "test").SetCompletionCache(nil), dep, nil, "https://app.example.test", "", "app", 100*time.Millisecond, true, tc.dark)
			if code != tc.exit {
				t.Fatalf("exit=%d want=%d receipt=%s", code, tc.exit, out.String())
			}
			var receipt struct {
				WaitStage string `json:"wait_stage"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
				t.Fatalf("invalid receipt: %v; output=%s", err, out.String())
			}
			if tc.exit == 3 && receipt.WaitStage != "rollout" {
				t.Fatalf("receipt omitted rollout recovery: %s", out.String())
			}
		})
	}
}

func TestServiceRolloutSuccessOutputShowsPendingCutover(t *testing.T) {
	dep := readyRollout("rolling_out", 0, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(dep)
	}))
	defer srv.Close()
	out, _, restore := main.SwapIO(t)
	defer restore()
	code := main.RenderSuccessfulDeployment(context.Background(), api.NewClient(srv.URL, "test").SetCompletionCache(nil), dep, "app")
	if code != 0 || !strings.Contains(out.String(), "cutover in progress") || strings.Contains(out.String(), "Deployed.") {
		t.Fatalf("exit=%d output=%s", code, out.String())
	}
}
