package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
	"net/http"
	"testing"
	"time"
)

func TestFeatureFlagsCustomerReleaseLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.featureFlagsEnabled = true
	ctx := context.Background()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	_ = project
	var ids []string
	for _, ref := range []string{"one", "two", "three", "four"} {
		customer, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, customer.ID)
	}
	cfg := flags.Config{Groups: map[string][]string{"internal": ids[:1]}, Flags: []flags.Flag{
		{Key: "export", Enabled: true, Rules: []flags.Rule{{ID: "selected", Customers: ids[:3], Value: true}}},
		{Key: "checkout", Type: "variant", Enabled: true, Default: "legacy", Variants: []flags.FlagVariant{{Key: "legacy", Weight: 5000}, {Key: "new", Weight: 5000}}, Rules: []flags.Rule{{ID: "selected-customer", Customers: ids[:1], Value: "new"}}},
	}}
	path := "/v1/projects/flags/environments/production/flags"
	zero := int64(0)
	res := e.do(t, http.MethodPut, path, updateFeatureFlagsRequest{ExpectedVersion: &zero, Config: cfg}, nil)
	if res.Code != 200 {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	var saved state.FeatureFlagVersion
	_ = json.Unmarshal(res.Body.Bytes(), &saved)
	for i, id := range ids {
		res = e.do(t, http.MethodPost, path+"/export/inspect", inspectFeatureFlagRequest{CustomerID: id}, nil)
		if res.Code != 200 {
			t.Fatalf("inspect: %d %s", res.Code, res.Body.String())
		}
		var d flags.Decision
		_ = json.Unmarshal(res.Body.Bytes(), &d)
		if d.Value != (i < 3) || d.ConfigVersion != 1 {
			t.Fatal(d)
		}
	}
	res = e.do(t, http.MethodPost, path+"/checkout/inspect", inspectFeatureFlagRequest{CustomerID: ids[0]}, nil)
	var variantDecision flags.Decision
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &variantDecision) != nil || variantDecision.Type != "variant" || variantDecision.Value != "new" || variantDecision.RuleID != "selected-customer" {
		t.Fatalf("variant inspect: %d %s %+v", res.Code, res.Body.String(), variantDecision)
	}
	saved.Flags[0].Enabled = false
	one := int64(1)
	res = e.do(t, http.MethodPut, path, updateFeatureFlagsRequest{ExpectedVersion: &one, Config: saved.Config}, nil)
	if res.Code != 200 {
		t.Fatalf("disable: %d %s", res.Code, res.Body.String())
	}
	res = e.do(t, http.MethodPost, path+"/export/inspect", inspectFeatureFlagRequest{CustomerID: ids[0]}, nil)
	var d flags.Decision
	_ = json.Unmarshal(res.Body.Bytes(), &d)
	if d.Value != false || d.Reason != "disabled" {
		t.Fatal(d)
	}
	res = e.do(t, http.MethodPost, path+"/export/inspect", inspectFeatureFlagRequest{CustomerID: ids[0], Version: 1}, nil)
	_ = json.Unmarshal(res.Body.Bytes(), &d)
	if d.Value != true || d.RuleID != "selected" {
		t.Fatal(d)
	}
	if res = e.do(t, http.MethodPut, path, updateFeatureFlagsRequest{ExpectedVersion: &one, Config: saved.Config}, nil); res.Code != 409 {
		t.Fatalf("stale update=%d", res.Code)
	}
	two := int64(2)
	res = e.do(t, http.MethodPost, path+"/rollback", rollbackFeatureFlagsRequest{ExpectedVersion: &two, Version: 1}, nil)
	if res.Code != 200 {
		t.Fatalf("rollback=%d %s", res.Code, res.Body.String())
	}
	res = e.do(t, http.MethodGet, path+"/versions", nil, nil)
	var history []state.FeatureFlagVersion
	_ = json.Unmarshal(res.Body.Bytes(), &history)
	if len(history) != 3 || history[0].Version != 3 || history[0].RestoredFrom != 1 {
		t.Fatal(history)
	}
	other, err := e.store.CreateAccount(ctx, "outsider@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, _, err := e.store.CreatePlatformTenant(ctx, other.ID, "foreign", "foreign", 100)
	if err != nil {
		t.Fatal(err)
	}
	res = e.do(t, http.MethodPost, path+"/export/inspect", inspectFeatureFlagRequest{CustomerID: outsider.ID}, nil)
	if res.Code != 404 {
		t.Fatalf("foreign inspect=%d", res.Code)
	}
}
func TestFeatureFlagsRuntimeWorkloadScope(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.featureFlagsEnabled = true
	ctx := context.Background()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "flags-runtime", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "flags-runtime-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := e.store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "default-local", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "flags-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e.s.flagsWorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(time.Now(), e.acct.ID, app.ID, instance.ID, workloadidentity.FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	res := e.do(t, http.MethodGet, "/v1/runtime/flags?environment=foreign", nil, map[string]string{"Authorization": "Bearer " + token.AccessToken})
	if res.Code != 200 {
		t.Fatalf("runtime: %d %s", res.Code, res.Body.String())
	}
	var bundle flags.Bundle
	_ = json.Unmarshal(res.Body.Bytes(), &bundle)
	env, err := e.store.ProjectEnvironmentBySlug(ctx, e.acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.EnvironmentID != env.ID {
		t.Fatal(bundle)
	}
	foreign, _ := signer.Mint(time.Now(), e.acct.ID, app.ID, instance.ID, "sts.amazonaws.com")
	res = e.do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + foreign.AccessToken})
	if res.Code != 401 {
		t.Fatalf("wrong audience=%d", res.Code)
	}
	res = e.do(t, http.MethodGet, "/v1/runtime/flags", nil, nil)
	if res.Code != 401 {
		t.Fatalf("account credential on runtime=%d", res.Code)
	}
	wrongAccount, err := signer.Mint(time.Now(), "another-account", app.ID, instance.ID, workloadidentity.FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	res = e.do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + wrongAccount.AccessToken})
	if res.Code != 401 {
		t.Fatalf("mismatched workload account=%d", res.Code)
	}
	if err = e.store.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	res = e.do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + token.AccessToken})
	if res.Code != 401 {
		t.Fatalf("stopped workload=%d", res.Code)
	}
}
