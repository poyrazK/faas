package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectComputeRegionScanApplyLifecycle(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	client := api.NewClient(srv.URL, e.key)
	source := applyProjectOneWorkloadTarGz(t)
	scan := func(region string) api.PlanResponse {
		t.Helper()
		plan, err := client.ScanProjectWithBindingOptions(t.Context(), bytes.NewReader(source), "project.tar.gz", "regional-project", "", "", 0, nil, nil, false, false, "", api.ProjectDeploymentOptions{ComputeRegion: region})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	apply := func(plan api.PlanResponse, region string) error {
		_, err := client.ApplyProjectPlanWithBindingOptions(t.Context(), plan.PlanToken, bytes.NewReader(source), "project.tar.gz", "regional-project", "", "", 0, nil, nil, false, false, "", "", api.ProjectDeploymentOptions{ComputeRegion: region})
		return err
	}
	plan := scan("local")
	if !plan.CanApply || plan.ComputeRegion != "local" || len(plan.Workloads) != 1 || plan.Workloads[0].ComputeRegion != "local" {
		t.Fatalf("plan=%+v", plan)
	}
	var apiErr *api.APIError
	if err := apply(plan, ""); !errors.As(err, &apiErr) || apiErr.Problem.Code != "plan_token_stale" {
		t.Fatalf("changed region apply=%v", err)
	}
	apps, _ := e.store.ListApps(t.Context(), e.acct.ID)
	if len(apps) != 0 {
		t.Fatalf("stale plan created apps: %+v", apps)
	}
	if err := apply(plan, "local"); err != nil {
		t.Fatal(err)
	}
	project, err := e.store.ProjectBySlug(t.Context(), e.acct.ID, "regional-project")
	if err != nil || project.ComputeRegion != "local" {
		t.Fatalf("project=%+v err=%v", project, err)
	}
	app, err := e.store.AppBySlug(t.Context(), plan.Workloads[0].Name)
	if err != nil || app.ComputeRegion != "local" {
		t.Fatalf("app=%+v err=%v", app, err)
	}
	// Pausing new apps and disabling the gate must not block existing redeploys.
	if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "local", DisplayName: "Local", AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	e.s.regionalComputeEnabled = false
	plan = scan("")
	if !plan.CanApply || plan.ComputeRegion != "local" || plan.Workloads[0].ComputeRegion != "local" {
		t.Fatalf("paused redeploy plan=%+v", plan)
	}
	if err := apply(plan, ""); err != nil {
		t.Fatal(err)
	}
	// Environment clone reuses the apps, so it cannot change placement.
	_, _, err = e.store.CloneProjectEnvironment(context.Background(), state.ProjectEnvironmentClone{AccountID: e.acct.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging"}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	app, err = e.store.AppBySlug(t.Context(), app.Slug)
	if err != nil || app.ComputeRegion != "local" {
		t.Fatalf("clone changed placement: %+v %v", app, err)
	}
}

type projectRegionRaceStore struct {
	state.Store
	arrived atomic.Int32
	ready   chan struct{}
}

func (s *projectRegionRaceStore) CreateProject(ctx context.Context, project state.Project) (state.Project, error) {
	if s.arrived.Add(1) == 2 {
		close(s.ready)
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		return state.Project{}, ctx.Err()
	}
	return s.Store.CreateProject(ctx, project)
}

func TestProjectComputeRegionConcurrentApplyDifferentDefaults(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	e.s.regionalComputeEnabled = true
	region := "eu-hel"
	if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: region, DisplayName: "Helsinki", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "hel-worker", TargetURL: "unix:///hel-worker", Region: &region, Active: true, AdmissionCeilingMB: 1024, VCPUBudget: 16}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	client := api.NewClient(srv.URL, e.key)
	source := applyProjectOneWorkloadTarGz(t)
	var plans []api.PlanResponse
	for _, region := range []string{"local", "eu-hel"} {
		plan, err := client.ScanProjectWithBindingOptions(t.Context(), bytes.NewReader(source), "project.tar.gz", "racing-project", "", "", 0, nil, nil, false, false, "", api.ProjectDeploymentOptions{ComputeRegion: region})
		if err != nil || !plan.CanApply {
			t.Fatalf("scan=%+v err=%v", plan, err)
		}
		plans = append(plans, plan)
	}
	e.s.store = &projectRegionRaceStore{Store: e.store, ready: make(chan struct{})}
	results := make(chan error, 2)
	for _, plan := range plans {
		go func(plan api.PlanResponse) {
			_, err := client.ApplyProjectPlanWithBindingOptions(ctx, plan.PlanToken, bytes.NewReader(source), "project.tar.gz", "racing-project", "", "", 0, nil, nil, false, false, "", "", api.ProjectDeploymentOptions{ComputeRegion: plan.ComputeRegion})
			results <- err
		}(plan)
	}
	success, rejected := 0, 0
	for range plans {
		err := <-results
		if err == nil {
			success++
			continue
		}
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Problem.Code != "plan_token_stale" {
			t.Fatalf("apply=%v", err)
		}
		rejected++
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("success=%d rejected=%d", success, rejected)
	}
	project, err := e.store.ProjectBySlug(t.Context(), e.acct.ID, "racing-project")
	if err != nil {
		t.Fatal(err)
	}
	apps, err := e.store.AppsForProject(t.Context(), e.acct.ID, project.ID)
	if err != nil || len(apps) != 1 || apps[0].ComputeRegion != project.ComputeRegion {
		t.Fatalf("project=%+v apps=%+v err=%v", project, apps, err)
	}
}

func TestProjectComputeRegionAdmissionGateAndOutage(t *testing.T) {
	for _, tc := range []struct {
		name, region    string
		enabled, paused bool
		code            string
	}{
		{name: "disabled", region: "local", code: api.CodeCapacity},
		{name: "unknown", region: "unknown", enabled: true, code: api.CodeComputeRegionUnknown},
		{name: "paused", region: "local", enabled: true, paused: true, code: api.CodeComputeRegionAdmissionPaused},
		{name: "unavailable", region: "eu-empty", enabled: true, code: api.CodeComputeRegionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
			e.s.regionalComputeEnabled = tc.enabled
			if tc.paused || tc.region == "eu-empty" {
				if _, err := e.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: tc.region, DisplayName: tc.region, AcceptingNewApps: !tc.paused}); err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewServer(e.h)
			defer srv.Close()
			client := api.NewClient(srv.URL, e.key)
			source := applyProjectOneWorkloadTarGz(t)
			opts := api.ProjectDeploymentOptions{ComputeRegion: tc.region}
			plan, err := client.ScanProjectWithBindingOptions(t.Context(), bytes.NewReader(source), "project.tar.gz", "blocked-project", "", "", 0, nil, nil, false, false, "", opts)
			if err != nil || plan.CanApply || len(plan.CanApplyReasons) == 0 {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			_, err = client.ApplyProjectPlanWithBindingOptions(t.Context(), plan.PlanToken, bytes.NewReader(source), "project.tar.gz", "blocked-project", "", "", 0, nil, nil, false, false, "", "", opts)
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Problem.Code != tc.code || apiErr.Problem.ComputeRegion != tc.region {
				t.Fatalf("apply=%v", err)
			}
			projects, err := e.store.ListProjectsForAccount(t.Context(), e.acct.ID)
			if err != nil || len(projects) != 0 {
				t.Fatalf("blocked apply wrote projects: %+v %v", projects, err)
			}
		})
	}
}
