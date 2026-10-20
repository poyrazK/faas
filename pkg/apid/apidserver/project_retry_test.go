package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestProjectRetryUnchangedSourceAndStaleFence(t *testing.T) {
	s, store, _, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != 200 {
		t.Fatalf("initial: %d %s", rec.Code, rec.Body)
	}
	var initial api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	failed := initial.Builds[0]
	if err := store.MemStore.UpdateDeploymentStatus(t.Context(), failed.DeploymentID, state.DeployFailed, "fixture"); err != nil {
		t.Fatal(err)
	}
	app, err := store.MemStore.AppByID(t.Context(), failed.AppID)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := json.Marshal(map[string]string{failed.AppID: failed.DeploymentID})
	if err != nil {
		t.Fatal(err)
	}
	builds := 0
	store.onBuild = func(state.Deployment) { builds++ }
	fields := map[string]string{"attempt_id": strings.Repeat("b", 32), "only": app.WorkloadName, "no_triggers": "true", "retry_targets": string(targets)}
	rec = projectSecretsRequest(t, s, key, "/v1/projects", "", fields)
	if rec.Code != 200 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	var apply api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &apply); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || len(apply.Builds) != 1 || apply.Builds[0].AppID != failed.AppID || apply.Builds[0].DeploymentID == failed.DeploymentID || apply.Builds[0].DeploymentID == "" {
		t.Fatalf("retry builds=%d apply=%+v", builds, apply)
	}
	fields["attempt_id"] = strings.Repeat("c", 32)
	rec = projectSecretsRequest(t, s, key, "/v1/projects", "", fields)
	if rec.Code != 409 || builds != 1 {
		t.Fatalf("stale retry: %d %s builds=%d", rec.Code, rec.Body, builds)
	}
}
