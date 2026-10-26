package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_ManualTrafficBetweenCompletedReleases: on production-us rc.294,
// `gregale traffic set --deployment v4 --percent 50` and `traffic promote
// --deployment v4` both answered 500 for an app whose v5 served 100% and
// whose retained v4 was live at 0%, both completed stable releases in one
// scope: the stamp tripped deployments_app_scope_live_uniq (Hunt 14
// H14-71). normalizeTrafficFixture marks every row explicit, which hid it;
// these rows are as production writes them.
func TestPg_ManualTrafficBetweenCompletedReleases(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, v5 := seedLiveDeploy(t, s, ctx, "manual-traffic")
	v4, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("4", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments
	    set scope = 'default', status = 'live', rollout_state = 'complete', canary_total_steps = 0,
	        traffic_percent_explicit = false,
	        traffic_percent = case when id = $2::uuid then 100 else 0 end
	  where app_id = $1`, appID, v5); err != nil {
		t.Fatal(err)
	}
	check := func(step string, want map[string][2]int) {
		t.Helper()
		for id, w := range want {
			d, err := s.DeploymentByID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			explicit := 0
			if d.TrafficPercentExplicit {
				explicit = 1
			}
			if d.TrafficPercent != w[0] || explicit != w[1] {
				t.Errorf("%s: %s = %d%% explicit=%v, want %d%% explicit=%v", step, id, d.TrafficPercent, d.TrafficPercentExplicit, w[0], w[1] == 1)
			}
		}
	}

	if _, err := s.UpdateDeploymentTraffic(ctx, v4.ID, 50); err != nil {
		t.Fatalf("split v4=50: %v", err)
	}
	check("split", map[string][2]int{v4.ID: {50, 1}, v5: {50, 1}})

	if _, err := s.UpdateDeploymentTraffic(ctx, v5, 100); err != nil {
		t.Fatalf("promote v5 back: %v", err)
	}
	check("promote v5", map[string][2]int{v5: {100, 0}, v4.ID: {0, 0}})

	if _, err := s.UpdateDeploymentTraffic(ctx, v4.ID, 100); err != nil {
		t.Fatalf("promote v4 over a stable v5: %v", err)
	}
	check("promote v4", map[string][2]int{v4.ID: {100, 0}, v5: {0, 0}})

	// After another split, a normal release retires both members; a retained
	// member must not read as a manual dark deployment (explicit at 0%).
	if _, err := s.UpdateDeploymentTraffic(ctx, v5, 30); err != nil {
		t.Fatalf("split v5=30: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into deployment_revision_pins (deployment_id, app_id, expires_at) values ($1, $2, now() + interval '1 hour')`, v5, appID); err != nil {
		t.Fatal(err)
	}
	v6, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("6", 64), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, v6.ID); err != nil {
		t.Fatalf("release v6 over the split: %v", err)
	}
	check("release v6", map[string][2]int{v6.ID: {100, 0}, v5: {0, 0}})
}
