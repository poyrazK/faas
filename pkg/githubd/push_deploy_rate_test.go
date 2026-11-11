// adr: 20261105095300
package githubd

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// A push whose services do not all fit the hourly deploy budget deploys none
// of them, so the project never lands on mixed releases.
func TestPushOverDeployRateDeploysNoService(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "api", RootDir: "services/auth/api", DetectedBy: reposcan.Detection{Detector: "compose"}},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "services/billing", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig, svc, rec, checks := pushRig(t, &scan, nil)
	ctx := context.Background()
	limit := api.PlanHobby.DeploysPerHour()
	for range limit - 1 {
		if _, err := rig.mem.ConsumeAccountDeployRate(ctx, rig.acct, limit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	push(t, svc, "c0ffee01")
	if len(rec.calls) != 0 {
		t.Fatalf("push deployed %d of 2 services with 1 deploy left: %+v", len(rec.calls), rec.calls)
	}
	apps, err := rig.mem.AppsForProject(ctx, rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps = %d, %v", len(apps), err)
	}
	for _, app := range apps {
		if !failedCheck(*checks, app.Slug, "needs 2 deploys") {
			t.Fatalf("no deploy-rate check for %s: %+v", app.Slug, *checks)
		}
	}
}

// A push that fits the budget deploys every service.
func TestPushWithinDeployRateDeploysEveryService(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "api", RootDir: "services/auth/api", DetectedBy: reposcan.Detection{Detector: "compose"}},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "services/billing", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig, svc, rec, _ := pushRig(t, &scan, nil)
	limit := api.PlanHobby.DeploysPerHour()
	for range limit - 2 {
		if _, err := rig.mem.ConsumeAccountDeployRate(context.Background(), rig.acct, limit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	push(t, svc, "c0ffee01")
	if len(rec.calls) != 2 {
		t.Fatalf("push deployed %d of 2 services with 2 deploys left: %+v", len(rec.calls), rec.calls)
	}
}
