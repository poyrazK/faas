package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod hunt 2026-11-21: a redeploy summarized "Changes since v2" against a
// canary it had aborted, while v1 still served. The summary compares with the
// nearest older release that served: failed builds and aborted canaries are
// skipped.
func TestDeploymentSummarySkipsReleasesThatNeverServed(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "summary-served")
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	create := func(build string, at time.Duration, status state.DeploymentStatus, rollout string) state.Deployment {
		t.Helper()
		d, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{
			AppID: appID, BuildID: build, ImageDigest: "sha256:" + build, Kind: state.DeploymentKindImage,
			Status: status, RolloutState: rollout, CreatedAt: base.Add(at), Scope: "production",
		})
		if err != nil {
			t.Fatalf("create %s: %v", build, err)
		}
		return d
	}
	served := create("v1", 0, state.DeployLive, "")
	create("v2-aborted", time.Second, state.DeploySuperseded, "aborted")
	create("v3-failed", 2*time.Second, state.DeployFailed, "")
	current := create("v4", 3*time.Second, state.DeployLive, "rolling_out")

	rec := e.Do(t, http.MethodGet, "/v1/apps/summary-served/deployments/"+current.ID+"/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Previous == nil || got.Previous.ID != served.ID {
		t.Fatalf("previous = %+v, want the served v1 %s", got.Previous, served.ID)
	}
}

// prod hunt r49: after a rollback to v1, a canary of v3 was summarized as
// "Changes since v2" while v1 served 99%. The release still live is the one
// the canary replaces.
func TestDeploymentSummaryPrefersStillLiveRelease(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "summary-live")
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Minute)
	create := func(build string, at time.Duration, status state.DeploymentStatus, rollout string) state.Deployment {
		t.Helper()
		d, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{
			AppID: appID, BuildID: build, ImageDigest: "sha256:" + build, Kind: state.DeploymentKindImage,
			Status: status, RolloutState: rollout, CreatedAt: base.Add(at), Scope: "production",
		})
		if err != nil {
			t.Fatalf("create %s: %v", build, err)
		}
		return d
	}
	rolledBackTo := create("v1", 0, state.DeployLive, "")
	create("v2", time.Second, state.DeploySuperseded, "")
	current := create("v3", 2*time.Second, state.DeployLive, "rolling_out")

	rec := e.Do(t, http.MethodGet, "/v1/apps/summary-live/deployments/"+current.ID+"/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Previous == nil || got.Previous.ID != rolledBackTo.ID {
		t.Fatalf("previous = %+v, want the still-live v1 %s", got.Previous, rolledBackTo.ID)
	}
}
