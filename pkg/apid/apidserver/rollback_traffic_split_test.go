package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// H10-10: v1 → v2 → rollback to v1 → v3 at 50%. A plain rollback picked the
// latest superseded row (v2, which nobody served) instead of returning the
// traffic to v1, and the deploy summary advertised `rollback --to v2`.
func TestRollbackApp_TrafficSplitPointsAtEarlierRelease(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	v1 := apidserver.MustSeedDeployment(t, e, "rb-split")
	if err := store.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.AppBySlug(ctx, "rb-split")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("b", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	// v2 served, then the customer rolled back to v1.
	if err := store.MarkDeploymentLive(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, v1.ID); err != nil {
		t.Fatal(err)
	}
	if d, err := store.DeploymentByID(ctx, v2.ID); err != nil || d.Status != state.DeploySuperseded {
		t.Fatalf("setup: v2 after rollback = %+v, err=%v; want superseded", d, err)
	}
	v3, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("c", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true,
		CreatedAt: time.Now().UTC().Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, v3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDeploymentTraffic(ctx, v3.ID, 50); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{v1.ID, v3.ID} {
		d, err := store.DeploymentByID(ctx, id)
		if err != nil || d.Status != state.DeployLive || d.TrafficPercent != 50 {
			t.Fatalf("setup: %s = %+v, err=%v; want live at 50%%", id, d, err)
		}
	}

	rec := e.Do(t, http.MethodPost, "/v1/apps/rb-split/rollback", nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rollback during split = %d %s, want 409", rec.Code, rec.Body)
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	earlier := v1.ID
	if v1.Revision > 0 {
		earlier = "v" + strconv.Itoa(v1.Revision)
	}
	if problem.Code != api.CodeNoRollbackTarget || !strings.Contains(problem.Detail, "gregale traffic promote --app rb-split --deployment "+earlier) {
		t.Fatalf("problem = %+v, want the traffic promote command for %s", problem, earlier)
	}
	if d, _ := store.DeploymentByID(ctx, v2.ID); d.Status != state.DeploySuperseded {
		t.Fatalf("v2 = %s, want still superseded", d.Status)
	}

	rec = e.Do(t, http.MethodGet, "/v1/apps/rb-split/deployments/"+v3.ID+"/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", rec.Code, rec.Body)
	}
	var summary api.DeploymentSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.RollbackTargetID != "" {
		t.Fatalf("summary rollback target = %s during a split, want none", summary.RollbackTargetID)
	}
}
