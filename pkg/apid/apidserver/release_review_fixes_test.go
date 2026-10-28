package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019071733 amendment 3 — a release parked without traffic before a
// freeze began could ship during it through traffic, promote or a rollback
// to it, with no override and no audit (release review 2026-10-28). It now
// waits for the freeze like a new deploy; a release deployed during the
// freeze passed the freeze check when it was created.
func TestFreezeRefusesTrafficForAReleaseParkedBeforeIt(t *testing.T) {
	f := newPostReleaseFixture(t, "frz-dark", true, time.Hour)
	ctx := context.Background()
	store := *f.e.Store()
	park := func(created time.Time, digit byte) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.release.AppID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + apidserver.Repeat(string(digit), 64), Status: state.DeployLive, CreatedAt: created,
			TrafficPercent: 0, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	before := park(time.Now().UTC().Add(-time.Hour), 'b')
	var freeze api.ReleaseFreeze
	if rec := guardDo(t, f.e, http.MethodPost, "/v1/freezes", api.CreateReleaseFreezeRequest{App: "frz-dark", Kind: "window", Reason: "launch week"}, &freeze); rec.Code != http.StatusCreated {
		t.Fatalf("create freeze = %d %s", rec.Code, rec.Body)
	}
	during := park(time.Now().UTC().Add(time.Second), 'c')
	frozen := func(method, path string, body any) bool {
		t.Helper()
		rec := f.e.Do(t, method, path, body, nil)
		return rec.Code == http.StatusConflict && decodeProblem(t, rec.Body.Bytes()).Code == api.CodeReleaseFrozen
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/v1/deployments/" + before.ID + "/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 50}},
		{http.MethodPost, "/v1/deployments/" + before.ID + "/promote", map[string]any{}},
		{http.MethodPost, "/v1/apps/frz-dark/rollback", api.RollbackRequest{TargetDeploymentID: &before.ID}},
	} {
		if !frozen(c.method, c.path, c.body) {
			t.Errorf("%s %s during the freeze was not refused with release_frozen", c.method, c.path)
		}
	}
	if frozen(http.MethodPatch, "/v1/deployments/"+during.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 50}) {
		t.Error("a release deployed during the freeze was refused traffic")
	}
	if rec := guardDo(t, f.e, http.MethodDelete, "/v1/freezes/"+freeze.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("lift = %d %s", rec.Code, rec.Body)
	}
	if frozen(http.MethodPatch, "/v1/deployments/"+before.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 50}) {
		t.Error("traffic was refused after the freeze lifted")
	}
}

// adr: 20261019071734 amendment 2 — Postgres cascaded an SLO's deletion to
// the release gates that judge it, with no audit, so a release lost a guard
// nobody removed. The SLO cannot be deleted while gates use it.
func TestSLOUsedByReleaseGatesCannotBeDeleted(t *testing.T) {
	f := newPostReleaseFixture(t, "slo-gated", true, time.Hour)
	var slo api.SLOResponse
	if rec := guardDo(t, f.e, http.MethodPost, "/v1/apps/slo-gated/slos", api.CreateSLORequest{Name: "available", SLI: "availability", ObjectivePct: 99.9, WindowDays: 7}, &slo); rec.Code != http.StatusCreated {
		t.Fatalf("create SLO = %d %s", rec.Code, rec.Body)
	}
	if rec := guardDo(t, f.e, http.MethodPost, "/v1/apps/slo-gated/release-gates", api.CreateReleaseGateRequest{Name: "burn", Kind: "slo", SLO: "available", MaxBurnRate: 10}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create gate = %d %s", rec.Code, rec.Body)
	}
	apidserver.AssertProblem(t, f.e.Do(t, http.MethodDelete, "/v1/apps/slo-gated/slos/"+slo.ID, nil, nil), http.StatusConflict, api.CodeConflict)
	if rec := f.e.Do(t, http.MethodDelete, "/v1/apps/slo-gated/release-gates/burn", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete gate = %d %s", rec.Code, rec.Body)
	}
	if rec := f.e.Do(t, http.MethodDelete, "/v1/apps/slo-gated/slos/"+slo.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete SLO after its gate = %d %s", rec.Code, rec.Body)
	}
}
