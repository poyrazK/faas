package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateApp_LifecycleRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := api.CreateAppRequest{
		Slug: "service-app", ExecutionMode: api.ExecutionModeService,
		RestartPolicy: api.RestartPolicyAlways, StartupDeadlineS: 60,
		MaxRetries: 10, RequestTimeoutS: 12, ServiceReplicas: &api.ServiceReplicas{Min: 1, Max: 5, Desired: 2},
	}
	rec := e.do(t, "POST", "/v1/apps", req, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Manifest.ExecutionMode != api.ExecutionModeService || out.Manifest.RestartPolicy != api.RestartPolicyAlways ||
		out.Manifest.StartupDeadlineS != 60 || out.Manifest.MaxRetries != 10 || out.Manifest.RequestTimeoutS != 12 || out.RequestTimeoutS != 12 ||
		out.Manifest.ServiceReplicas == nil || out.Manifest.ServiceReplicas.Desired != 2 {
		t.Fatalf("lifecycle response = %+v", out.Manifest)
	}
	if out.MaxConcurrency != 2 {
		t.Fatalf("service max_concurrency = %d, want desired replica default 2", out.MaxConcurrency)
	}
	stored, err := e.store.AppBySlug(t.Context(), "service-app")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Manifest.ServiceReplicas == nil || stored.Manifest.ServiceReplicas.Desired != 2 {
		t.Fatalf("stored lifecycle = %+v", stored.Manifest)
	}
	if stored.Manifest.RequestTimeoutS != 12 {
		t.Fatalf("stored request timeout = %d, want 12", stored.Manifest.RequestTimeoutS)
	}
}

func TestCreateApp_RequestTimeoutIsBounded(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "timeout-too-large", RequestTimeoutS: 31}, nil)
	if rec.Code != 422 {
		t.Fatalf("request timeout: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 422, api.CodeValidation)
}

func TestUpdateApp_LifecycleIsPartialAndValidated(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "lifecycle-app"}, nil); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	mode := api.ExecutionModeService
	maxConcurrency := 3
	replicas := &api.ServiceReplicas{Min: 1, Max: 5, Desired: 3}
	rec := e.do(t, "PATCH", "/v1/apps/lifecycle-app", api.UpdateAppRequest{
		ExecutionMode: &mode, MaxConcurrency: &maxConcurrency, ServiceReplicas: replicas,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Manifest.ExecutionMode != mode || out.Manifest.ServiceReplicas == nil || out.Manifest.ServiceReplicas.Desired != 3 {
		t.Fatalf("patched lifecycle = %+v", out.Manifest)
	}
	tooMany := &api.ServiceReplicas{Min: 1, Max: 5, Desired: 4}
	rec = e.do(t, "PATCH", "/v1/apps/lifecycle-app", api.UpdateAppRequest{ServiceReplicas: tooMany}, nil)
	if rec.Code != 400 {
		t.Fatalf("replica target above app cap: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 400, api.CodeValidation)

	badPolicy := api.RestartPolicyAlways
	job := api.ExecutionModeJob
	rec = e.do(t, "PATCH", "/v1/apps/lifecycle-app", api.UpdateAppRequest{
		ExecutionMode: &job, RestartPolicy: &badPolicy,
	}, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid job policy: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 400, api.CodeValidation)
	stored, err := e.store.AppBySlug(t.Context(), "lifecycle-app")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Manifest.ExecutionMode != api.ExecutionModeService || stored.Manifest.ServiceReplicas == nil {
		t.Fatalf("invalid patch changed state: %+v", stored.Manifest)
	}
}

func TestCreateApp_LifecyclePlanGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{
		Slug: "free-service", ExecutionMode: api.ExecutionModeService,
	}, nil)
	if rec.Code != 400 {
		t.Fatalf("free service: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 400, api.CodeValidation)
}

func TestStateManifestForUpdate_PreservesProjectMetadataAndUpdatesPorts(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{
		ProjectSourceSHA256: "source-digest",
		BuildDockerfile:     "deploy/Dockerfile.production",
		WorkingDir:          "/workspace/service",
		Env:                 map[string]string{"DATABASE_URL": "secret"},
		Ports:               []api.WorkloadPort{{Name: "old", Port: 8080}},
	}}
	ports := []api.WorkloadPort{{Name: "http", Port: 3000}, {Name: "admin", Port: 9090}}

	updated, changed := stateManifestForUpdate(app, &api.UpdateAppRequest{Ports: &ports})
	if !changed || updated == nil {
		t.Fatal("ports update was not detected")
	}
	if len(updated.Ports) != 2 || updated.Ports[0].Name != "http" || updated.Ports[1].Port != 9090 {
		t.Fatalf("updated ports = %+v", updated.Ports)
	}
	if updated.ProjectSourceSHA256 != "source-digest" || updated.BuildDockerfile != "deploy/Dockerfile.production" ||
		updated.WorkingDir != "/workspace/service" || updated.Env["DATABASE_URL"] != "secret" {
		t.Fatalf("project metadata was not preserved: %+v", *updated)
	}
}
