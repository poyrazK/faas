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

// A deployment of a deleted app is a final answer, not a 503: the CLI's
// deploy wait retries 503s and hung for 17 minutes on one (prod 2026-11-15).
func TestGetDeploymentOfDeletedAppIsGone(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "gone-app"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app = %d: %s", rec.Code, rec.Body.String())
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), "gone-app")
	if err != nil {
		t.Fatal(err)
	}
	d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat("a", 64),
		Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/deployments/"+d.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("live app deployment = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/gone-app", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete app = %d: %s", rec.Code, rec.Body.String())
	}
	rec := e.Do(t, http.MethodGet, "/v1/deployments/"+d.ID, nil, nil)
	var problem api.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	if rec.Code != http.StatusGone || problem.Code != api.CodeAppDeleted {
		t.Fatalf("deleted app deployment = %d %s, want 410 %s", rec.Code, rec.Body.String(), api.CodeAppDeleted)
	}
}
