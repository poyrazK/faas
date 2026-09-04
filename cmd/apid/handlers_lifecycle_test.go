package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCreateApp_LifecycleRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := api.CreateAppRequest{
		Slug: "service-app", ExecutionMode: api.ExecutionModeService,
		RestartPolicy: api.RestartPolicyAlways, StartupDeadlineS: 60,
		MaxRetries: 10, ServiceReplicas: &api.ServiceReplicas{Min: 1, Max: 5, Desired: 2},
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
		out.Manifest.StartupDeadlineS != 60 || out.Manifest.MaxRetries != 10 ||
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
