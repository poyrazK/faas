package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectPlatformTenantPolicyScanApply(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	client := api.NewClient(srv.URL, e.key)
	source := applyProjectOneWorkloadTarGz(t)
	ctx := context.Background()
	policy := true
	scan := func(override *bool) api.PlanResponse {
		t.Helper()
		plan, err := client.ScanProjectWithBindingEnvironment(ctx, bytes.NewReader(source), "project.tar.gz", "customer-platform", "", "", 0, nil, nil, false, false, "", override)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Workloads) != 1 {
			t.Fatalf("workloads=%+v", plan.Workloads)
		}
		return plan
	}
	apply := func(plan api.PlanResponse, override *bool) error {
		_, err := client.ApplyProjectPlanWithBindingEnvironmentApproval(ctx, plan.PlanToken, bytes.NewReader(source), "project.tar.gz", "customer-platform", "", "", 0, nil, nil, false, false, "", "", override)
		return err
	}
	plan := scan(&policy)
	if plan.Workloads[0].PlatformTenantRequired == nil || !*plan.Workloads[0].PlatformTenantRequired {
		t.Fatal("scan lost policy")
	}
	for _, mismatch := range []*bool{nil, projectTenantBool(false)} {
		var problem *api.APIError
		if err := apply(plan, mismatch); !errors.As(err, &problem) || problem.Problem.Code != "plan_token_stale" {
			t.Fatalf("mismatched apply=%v", err)
		}
		apps, err := e.store.ListApps(ctx, e.acct.ID)
		if err != nil || len(apps) != 0 {
			t.Fatalf("mismatched apply wrote apps: %+v, %v", apps, err)
		}
	}
	if err := apply(plan, &policy); err != nil {
		t.Fatal(err)
	}
	assertPolicy := func(want bool) {
		t.Helper()
		app, err := e.store.AppBySlug(ctx, plan.Workloads[0].Name)
		if err != nil || app.PlatformTenantRequired != want {
			t.Fatalf("app policy=%v want=%v err=%v", app.PlatformTenantRequired, want, err)
		}
	}
	assertPolicy(true)
	plan = scan(nil)
	if err := apply(plan, nil); err != nil {
		t.Fatal(err)
	}
	assertPolicy(true)
	policy = false
	plan = scan(&policy)
	if err := apply(plan, &policy); err != nil {
		t.Fatal(err)
	}
	assertPolicy(false)
}

func TestProjectPlatformTenantPolicyFreeScanRejected(t *testing.T) {
	e := setup(t, api.PlanFree)
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	policy := true
	_, err := api.NewClient(srv.URL, e.key).ScanProjectWithBindingEnvironment(context.Background(), bytes.NewReader(applyProjectOneWorkloadTarGz(t)), "project.tar.gz", "customer-platform", "", "", 0, nil, nil, false, false, "", &policy)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusPaymentRequired {
		t.Fatalf("Free scan=%v", err)
	}
}

func projectTenantBool(v bool) *bool { return &v }
