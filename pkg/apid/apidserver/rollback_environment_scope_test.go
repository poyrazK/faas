package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackEnvApp seeds an app whose environments each served two releases,
// in the given order, and returns the superseded first release of each.
func rollbackEnvApp(t *testing.T, e apidserver.TestEnv, slug string, scopes ...string) map[string]state.Deployment {
	t.Helper()
	ctx := t.Context()
	store := *e.Store()
	seed := apidserver.MustSeedDeployment(t, e, slug)
	app, err := store.AppBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, seed.ID, state.DeployFailed, "unused seed"); err != nil {
		t.Fatal(err)
	}
	first := map[string]state.Deployment{}
	for _, scope := range scopes {
		for i := range 2 {
			d, err := store.CreateDeployment(ctx, state.Deployment{
				AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, Status: state.DeployBuilding,
				ImageDigest: "sha256:" + strings.Repeat("e", 64), CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, d.ID, "/srv/fc/apps/"+slug+"/"+d.ID+".ext4", "apps/"+slug+"/"+d.ID+".ext4", 1); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first[scope] = d
			}
		}
	}
	return first
}

// A plain rollback chose the newest superseded row of any environment: with
// staging redeployed after production, `gregale rollback api` rolled staging
// back and reported success while the bad production release kept serving.
func TestRollbackApp_RestoresProductionNotNewerStaging(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	first := rollbackEnvApp(t, e, "rb-env", state.DefaultEnvScope, "staging")

	rec := e.Do(t, http.MethodPost, "/v1/apps/rb-env/rollback", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rollback = %d %s, want 202", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != first[state.DefaultEnvScope].ID {
		t.Fatalf("rollback target = %s, want the previous production release %s (not staging's %s)", out.ID, first[state.DefaultEnvScope].ID, first["staging"].ID)
	}
	staging, err := (*e.Store()).DeploymentByID(t.Context(), first["staging"].ID)
	if err != nil || staging.Status != state.DeploySuperseded {
		t.Fatalf("staging's previous release = %+v err=%v, want untouched", staging.Status, err)
	}
}

// An app serving in only one non-production environment rolls that one back.
func TestRollbackApp_RestoresTheOnlyServingEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	first := rollbackEnvApp(t, e, "rb-staging-only", "staging")

	rec := e.Do(t, http.MethodPost, "/v1/apps/rb-staging-only/rollback", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rollback = %d %s, want 202", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != first["staging"].ID {
		t.Fatalf("rollback target = %s, want staging's previous release %s", out.ID, first["staging"].ID)
	}
}

// Several non-production environments and no production: the request names
// none, so the rollback is refused rather than guessing.
func TestRollbackApp_AmbiguousEnvironmentsNeedTarget(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rollbackEnvApp(t, e, "rb-ambiguous", "staging", "testing")

	rec := e.Do(t, http.MethodPost, "/v1/apps/rb-ambiguous/rollback", nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rollback = %d %s, want 409", rec.Code, rec.Body)
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != api.CodeNoRollbackTarget || !strings.Contains(problem.Detail, "gregale rollback rb-ambiguous --to") {
		t.Fatalf("problem = %+v, want no_rollback_target naming --to", problem)
	}
}
