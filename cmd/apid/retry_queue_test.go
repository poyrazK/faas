package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryDeployment_QueuesSourceBuild(t *testing.T) {
	e := setup(t, api.PlanScale)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "retry-queue", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(source, []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: 15, Handler: "index.handler"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	if r.Code != http.StatusConflict {
		t.Fatalf("pending deployment retry: status=%d body=%s", r.Code, r.Body.String())
	}
	latest, err := e.store.LatestDeployment(t.Context(), app.ID)
	if err != nil || latest.ID != dep.ID {
		t.Fatalf("pending retry created a deployment: latest=%s err=%v", latest.ID, err)
	}
	if err := e.store.FailSourceDeployment(t.Context(), dep.ID, "test failure"); err != nil {
		t.Fatal(err)
	}
	for _, stage := range state.AllStageNames {
		t.Run(string(stage), func(t *testing.T) {
			r := e.do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: string(stage)}, nil)
			if r.Code != http.StatusAccepted {
				t.Fatalf("status=%d: %s", r.Code, r.Body.String())
			}
			var got api.DeploymentResponse
			if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			build, err := e.store.BuildByDeployment(t.Context(), got.ID)
			if err != nil {
				t.Fatalf("accepted retry has no durable build: %v", err)
			}
			if build.Status != state.BuildQueued {
				t.Fatalf("build status=%s", build.Status)
			}
			if got.Status != string(state.DeployBuilding) {
				t.Fatalf("deployment status=%s", got.Status)
			}
		})
	}
	latest, err = e.store.LatestDeployment(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	if r.Code != http.StatusConflict {
		t.Fatalf("missing source: status=%d body=%s", r.Code, r.Body.String())
	}
	unchanged, err := e.store.LatestDeployment(t.Context(), app.ID)
	if err != nil || unchanged.ID != latest.ID {
		t.Fatalf("missing source created a deployment: %s %v", unchanged.ID, err)
	}
}
