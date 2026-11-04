package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us 2026-10-22: h12-mcp's first deploy stopped before promotion,
// leaving v1 live at 0% traffic. Every request answered "the app has no live
// deployment to wake" while gregale deployments listed v1 as live (Hunt 12
// H12-59). The wake error now names the dark deployment and its promotion.
func TestWakeNamesADarkLiveDeployment(t *testing.T) {
	tests := []struct {
		name         string
		dark         bool
		wantDetail   string
		wantHint     string
		unwantedHint string
	}{
		{"only a 0% live deployment", true, "deployment %s is live at 0% traffic", "gregale traffic promote --app app-pro --deployment %s", ""},
		{"no live deployment at all", false, "the app has no live deployment to wake", "", "traffic promote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, app, serving := sched.SeedApp(t, store, api.PlanPro, 256, 1)
			if err := store.UpdateDeploymentStatus(ctx, serving.ID, state.DeploySuperseded, ""); err != nil {
				t.Fatal(err)
			}
			darkID := ""
			if tt.dark {
				dark, err := store.CreateDeployment(ctx, state.Deployment{
					AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployLive,
					TrafficPercent: 0, TrafficPercentExplicit: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				darkID = dark.ID
			}
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")

			_, err := e.Wake(ctx, app.ID, "", "", "")
			var problem *api.Problem
			if !errors.Is(err, sched.ErrPermanentWake) || !errors.As(err, &problem) {
				t.Fatalf("Wake error = %v, want a permanent problem", err)
			}
			if want := strings.ReplaceAll(tt.wantDetail, "%s", darkID); !strings.Contains(problem.Detail, want) {
				t.Errorf("detail = %q, want %q", problem.Detail, want)
			}
			// The next step rides in Detail, which crosses the gRPC hop to
			// the gateway; Hint does not.
			if want := strings.ReplaceAll(tt.wantHint, "%s", darkID); want != "" && !strings.Contains(problem.Detail, want) {
				t.Errorf("detail = %q, want %q", problem.Detail, want)
			}
			if tt.unwantedHint != "" && strings.Contains(problem.Detail, tt.unwantedHint) {
				t.Errorf("detail = %q names %q with nothing to promote", problem.Detail, tt.unwantedHint)
			}
		})
	}
}
