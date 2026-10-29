package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The support view resolves a slug as well as a UUID and says whether the
// next wake restores (a usable snapshot per tier) or cold boots.
func TestObsAppDetailBySlugReportsSnapshotsAndWakes(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "support-view", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.7.0", Tier: state.SnapshotTierInit, MemBytes: 128 << 20,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "test"),
	}); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{app.Slug, app.ID} {
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/apps/"+ref+"?range=1h", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body=%s", ref, rec.Code, rec.Body.String())
		}
		var out api.ObsAppDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.App.ID != app.ID || out.Wakes == nil || out.Wakes.Rows == nil || len(out.Snapshots) != 2 {
			t.Fatalf("%s: app=%s wakes=%v snapshots=%+v", ref, out.App.ID, out.Wakes, out.Snapshots)
		}
		warm, init := out.Snapshots[0], out.Snapshots[1]
		if warm.Tier != state.SnapshotTierWarm || warm.Present || init.Tier != state.SnapshotTierInit || !init.Present ||
			init.DeploymentID != dep.ID || init.FCVersion != "1.7.0" || init.MemBytes != 128<<20 || init.CreatedAt == nil {
			t.Fatalf("%s: snapshots = %+v", ref, out.Snapshots)
		}
	}
	for path, want := range map[string]int{
		"/v1/admin/obs/apps/no-such-app":               http.StatusNotFound,
		"/v1/admin/obs/apps/" + uuid.NewString():       http.StatusNotFound,
		"/v1/admin/obs/apps/" + app.Slug + "?range=2y": http.StatusBadRequest,
	} {
		if rec := e.DoOperator(t, http.MethodGet, path, nil, nil); rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}
