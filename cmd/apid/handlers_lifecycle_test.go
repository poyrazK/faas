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

func TestAppRevisionPinTTL(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "skew-app", RevisionPinTTLSeconds: 3600}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.RevisionPinTTLSeconds != 3600 || out.Manifest.RevisionPinTTLSeconds != 3600 {
		t.Fatalf("create ttl = %d / %d", out.RevisionPinTTLSeconds, out.Manifest.RevisionPinTTLSeconds)
	}
	zero := 0
	rec = e.do(t, "PATCH", "/v1/apps/skew-app", api.UpdateAppRequest{RevisionPinTTLSeconds: &zero}, nil)
	if rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	stored, err := e.store.AppBySlug(t.Context(), "skew-app")
	if err != nil || stored.Manifest.RevisionPinTTLSeconds != 0 {
		t.Fatalf("stored ttl = %d, %v", stored.Manifest.RevisionPinTTLSeconds, err)
	}
	invalid := api.RevisionPinMaxTTLSeconds + 1
	rec = e.do(t, "PATCH", "/v1/apps/skew-app", api.UpdateAppRequest{RevisionPinTTLSeconds: &invalid}, nil)
	if rec.Code != 422 {
		t.Fatalf("invalid ttl: %d %s", rec.Code, rec.Body)
	}
}

func TestAppVersionAffinityCookieRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "cookie-app", VersionAffinityCookie: "visitor_id"}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.VersionAffinityCookie != "visitor_id" || out.Manifest.VersionAffinityCookie != "visitor_id" {
		t.Fatalf("create response cookie = %q / %q", out.VersionAffinityCookie, out.Manifest.VersionAffinityCookie)
	}
	name := "session_id"
	rec = e.do(t, "PATCH", "/v1/apps/cookie-app", api.UpdateAppRequest{VersionAffinityCookie: &name}, nil)
	if rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	stored, err := e.store.AppBySlug(t.Context(), "cookie-app")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Manifest.VersionAffinityCookie != name {
		t.Fatalf("stored cookie = %q, want %q", stored.Manifest.VersionAffinityCookie, name)
	}
	bad := "bad cookie"
	rec = e.do(t, "PATCH", "/v1/apps/cookie-app", api.UpdateAppRequest{VersionAffinityCookie: &bad}, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid cookie name: %d %s", rec.Code, rec.Body)
	}
	empty := ""
	rec = e.do(t, "PATCH", "/v1/apps/cookie-app", api.UpdateAppRequest{VersionAffinityCookie: &empty}, nil)
	if rec.Code != 200 {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	stored, err = e.store.AppBySlug(t.Context(), "cookie-app")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Manifest.VersionAffinityCookie != "" {
		t.Fatalf("stored cookie after clear = %q", stored.Manifest.VersionAffinityCookie)
	}
}

func TestAppManagedVersionAffinityCookieRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "managed-cookie-app", VersionAffinityManagedCookie: true}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.VersionAffinityManagedCookie || !out.Manifest.VersionAffinityManagedCookie {
		t.Fatalf("managed cookie omitted from response: %+v", out)
	}
	stored, err := e.store.AppBySlug(t.Context(), "managed-cookie-app")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Manifest.VersionAffinityManagedCookie {
		t.Fatal("managed cookie not persisted")
	}
	name := "visitor_id"
	rec = e.do(t, "PATCH", "/v1/apps/managed-cookie-app", api.UpdateAppRequest{VersionAffinityCookie: &name}, nil)
	if rec.Code != 400 {
		t.Fatalf("conflicting cookie source: %d %s", rec.Code, rec.Body)
	}
	disabled := false
	rec = e.do(t, "PATCH", "/v1/apps/managed-cookie-app", api.UpdateAppRequest{VersionAffinityManagedCookie: &disabled, VersionAffinityCookie: &name}, nil)
	if rec.Code != 200 {
		t.Fatalf("switch to app cookie: %d %s", rec.Code, rec.Body)
	}
	stored, err = e.store.AppBySlug(t.Context(), "managed-cookie-app")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Manifest.VersionAffinityManagedCookie || stored.Manifest.VersionAffinityCookie != name {
		t.Fatalf("stored source after switch: %+v", stored.Manifest)
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
