package apidserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSafeProjectApplyDarkCandidatesAndFailurePreserveProduction(t *testing.T) {
	s, store, account, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != 200 {
		t.Fatalf("initial=%d %s", rec.Code, rec.Body)
	}
	var original api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	var members []state.ProjectReleaseMember
	for _, b := range original.Builds {
		app, err := store.MemStore.AppByID(t.Context(), b.AppID)
		if err != nil {
			t.Fatal(err)
		}
		app.Manifest.RevisionPinTTLSeconds = 3600
		if _, err := store.MemStore.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
			t.Fatal(err)
		}
		old, err := store.MemStore.CreateDeployment(t.Context(), state.Deployment{AppID: b.AppID, Scope: "production", Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MemStore.MarkDeploymentLive(t.Context(), old.ID); err != nil {
			t.Fatal(err)
		}
		members = append(members, state.ProjectReleaseMember{AppID: b.AppID, DeploymentID: old.ID})
	}
	baseline, err := store.MemStore.PublishProjectReleaseSet(t.Context(), account.ID, original.ProjectID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MemStore.UpdateProjectEnvironmentProtection(t.Context(), account.ID, original.ProjectID, "production", false); err != nil {
		t.Fatal(err)
	}
	admitted := 0
	store.onBuild = func(d state.Deployment) {
		admitted++
		if !d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.Scope != "production" {
			t.Fatalf("candidate is not dark: %+v", d)
		}
		active, err := store.MemStore.ActiveProjectReleaseSet(t.Context(), account.ID, original.ProjectID, "production")
		if err != nil || active.ID != baseline.ID {
			t.Fatalf("early cutover=%+v %v", active, err)
		}
	}
	rec = projectSecretsRequest(t, s, key, "/v1/projects/safe-deploy", "", map[string]string{"attempt_id": strings.Repeat("b", 32), "no_triggers": "true"})
	if rec.Code != 200 {
		t.Fatalf("safe apply=%d %s", rec.Code, rec.Body)
	}
	var apply api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &apply); err != nil {
		t.Fatal(err)
	}
	if admitted != 2 || apply.SafeRelease == nil || apply.SafeRelease.PreviousReleaseID != baseline.ID {
		t.Fatalf("apply=%+v admitted=%d", apply, admitted)
	}
	if err := store.MemStore.UpdateDeploymentStatus(t.Context(), apply.Builds[1].DeploymentID, state.DeployFailed, ""); err != nil {
		t.Fatal(err)
	}
	progress, err := store.MemStore.ReconcileSafeProjectSubmission(t.Context(), account.ID, strings.Repeat("b", 32))
	if err != nil || progress.Status != "failed" {
		t.Fatalf("progress=%+v %v", progress, err)
	}
	active, err := store.MemStore.ActiveProjectReleaseSet(t.Context(), account.ID, original.ProjectID, "production")
	if err != nil || active.ID != baseline.ID {
		t.Fatalf("failed candidate changed production: %+v %v", active, err)
	}
	for _, m := range members {
		_, target, err := store.MemStore.ResolveProjectRelease(t.Context(), m.AppID, "production", "")
		if err != nil || target != m.DeploymentID {
			t.Fatalf("mixed production graph target=%s err=%v", target, err)
		}
	}
}
