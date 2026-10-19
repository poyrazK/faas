package apidserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestFeatureFlagCapabilityOnlyRequiredForEnabledSubjectRules(t *testing.T) {
	config := flags.Config{Flags: []flags.Flag{{Key: "export", Rules: []flags.Rule{{ID: "pilot-user", Subjects: []string{"user-17"}}}}}}
	if apidserver.FeatureFlagConfigRequiresSubjectTargeting(config) {
		t.Fatal("disabled subject flag required a new SDK capability")
	}
	config.Flags[0].Enabled = true
	if !apidserver.FeatureFlagConfigRequiresSubjectTargeting(config) {
		t.Fatal("enabled subject flag did not require the SDK capability")
	}
}

// Hunt #9 H9-43: with flags not qualified on the installation, the 403 had
// no hint, so the CLI fell back to "check the selected profile, account
// membership, and credential permissions" and sent users after the wrong
// cause.
func TestFeatureFlagsDisabledNamesTheOperatorGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	// Project access resolves the addressed project first (#4882), so the gate
	// is observed on a project the caller owns.
	if _, err := (*e.Store()).CreateProject(t.Context(), state.Project{AccountID: (*e.Acct()).ID, Slug: "flags"}); err != nil {
		t.Fatal(err)
	}
	res := e.Do(t, http.MethodGet, "/v1/projects/flags/environments/production/flags", nil, nil)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(res.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem.Hint, "not enabled on this Gregale installation") {
		t.Fatalf("hint = %q, want the operator gate named", problem.Hint)
	}
}

func TestFeatureFlagsCustomerReleaseLifecycle(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*(*e.S()).FeatureFlagsEnabled()) = true
	ctx := context.Background()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	_ = project
	var ids []string
	for _, ref := range []string{"one", "two", "three", "four"} {
		customer, _, err := (*e.Store()).CreatePlatformTenant(ctx, (*e.Acct()).ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, customer.ID)
	}
	cfg := flags.Config{Groups: map[string][]string{"internal": ids[:1]}, Flags: []flags.Flag{
		{Key: "export", Enabled: true, Rules: []flags.Rule{{ID: "selected", Customers: ids[:3], Value: true}}},
		{Key: "checkout", Type: "variant", Enabled: true, Default: "legacy", Variants: []flags.FlagVariant{{Key: "legacy", Weight: 5000}, {Key: "new", Weight: 5000}}, Rules: []flags.Rule{{ID: "selected-customer", Customers: ids[:1], Value: "new"}}},
		{Key: "actor-release", Enabled: true, Default: false, Rules: []flags.Rule{{ID: "pilot-user", Customers: ids[:1], Subjects: []string{"user-17"}, Value: true}}},
	}}
	path := "/v1/projects/flags/environments/production/flags"
	zero := int64(0)
	res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &zero, Config: cfg}, nil)
	if res.Code != 200 {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	var saved state.FeatureFlagVersion
	_ = json.Unmarshal(res.Body.Bytes(), &saved)
	for i, id := range ids {
		res = e.Do(t, http.MethodPost, path+"/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: id}, nil)
		if res.Code != 200 {
			t.Fatalf("inspect: %d %s", res.Code, res.Body.String())
		}
		var d flags.Decision
		_ = json.Unmarshal(res.Body.Bytes(), &d)
		if d.Value != (i < 3) || d.ConfigVersion != 1 {
			t.Fatal(d)
		}
	}
	res = e.Do(t, http.MethodPost, path+"/checkout/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0]}, nil)
	var variantDecision flags.Decision
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &variantDecision) != nil || variantDecision.Type != "variant" || variantDecision.Value != "new" || variantDecision.RuleID != "selected-customer" {
		t.Fatalf("variant inspect: %d %s %+v", res.Code, res.Body.String(), variantDecision)
	}
	res = e.Do(t, http.MethodPost, path+"/actor-release/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0], SubjectID: "user-17"}, nil)
	var subjectDecision flags.Decision
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &subjectDecision) != nil || subjectDecision.Value != true || subjectDecision.RuleID != "pilot-user" {
		t.Fatalf("subject inspect: %d %s %+v", res.Code, res.Body.String(), subjectDecision)
	}
	res = e.Do(t, http.MethodPost, path+"/actor-release/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0]}, nil)
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &subjectDecision) != nil || subjectDecision.Reason != "subject_missing" || subjectDecision.Value != false {
		t.Fatalf("missing-subject inspect: %d %s %+v", res.Code, res.Body.String(), subjectDecision)
	}
	res = e.Do(t, http.MethodPost, path+"/actor-release/inspect", apidserver.InspectFeatureFlagRequest{SubjectID: "user-17"}, nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("subject inspect without customer=%d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, http.MethodPost, path+"/actor-release/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0], SubjectID: "user with space"}, nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid subject inspect=%d %s", res.Code, res.Body.String())
	}
	saved.Flags[0].Enabled = false
	one := int64(1)
	res = e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &one, Config: saved.Config}, nil)
	if res.Code != 200 {
		t.Fatalf("disable: %d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, http.MethodPost, path+"/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0]}, nil)
	var d flags.Decision
	_ = json.Unmarshal(res.Body.Bytes(), &d)
	if d.Value != false || d.Reason != "disabled" {
		t.Fatal(d)
	}
	res = e.Do(t, http.MethodPost, path+"/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: ids[0], Version: 1}, nil)
	_ = json.Unmarshal(res.Body.Bytes(), &d)
	if d.Value != true || d.RuleID != "selected" {
		t.Fatal(d)
	}
	if res = e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &one, Config: saved.Config}, nil); res.Code != 409 {
		t.Fatalf("stale update=%d", res.Code)
	}
	two := int64(2)
	res = e.Do(t, http.MethodPost, path+"/rollback", apidserver.RollbackFeatureFlagsRequest{ExpectedVersion: &two, Version: 1}, nil)
	if res.Code != 200 {
		t.Fatalf("rollback=%d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, http.MethodGet, path+"/versions", nil, nil)
	var history []state.FeatureFlagVersion
	_ = json.Unmarshal(res.Body.Bytes(), &history)
	if len(history) != 3 || history[0].Version != 3 || history[0].RestoredFrom != 1 {
		t.Fatal(history)
	}
	other, err := (*e.Store()).CreateAccount(ctx, "outsider@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, _, err := (*e.Store()).CreatePlatformTenant(ctx, other.ID, "foreign", "foreign", 100)
	if err != nil {
		t.Fatal(err)
	}
	res = e.Do(t, http.MethodPost, path+"/export/inspect", apidserver.InspectFeatureFlagRequest{CustomerID: outsider.ID}, nil)
	if res.Code != 404 {
		t.Fatalf("foreign inspect=%d", res.Code)
	}
}
func TestFeatureFlagsRuntimeWorkloadScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*(*e.S()).FeatureFlagsEnabled()) = true
	ctx := context.Background()
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: (*e.Acct()).ID, Slug: "flags-runtime", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "flags-runtime-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := (*e.Store()).CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "default-local", "")
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
	(*(*e.S()).FlagsWorkloadVerifier()), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(time.Now(), (*e.Acct()).ID, app.ID, instance.ID, workloadidentity.FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	customer, _, err := (*e.Store()).CreatePlatformTenant(ctx, (*e.Acct()).ID, "runtime-subject", "Runtime Subject", 100)
	if err != nil {
		t.Fatal(err)
	}
	env, err := (*e.Store()).ProjectEnvironmentBySlug(ctx, (*e.Acct()).ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{
		Scope:           state.FeatureFlagScope{AccountID: (*e.Acct()).ID, ProjectID: project.ID, EnvironmentID: env.ID},
		ExpectedVersion: 0, Actor: (*e.Acct()).ID,
		Config: flags.Config{Groups: map[string][]string{}, Flags: []flags.Flag{{Key: "export", Enabled: true, Default: false, Rules: []flags.Rule{{
			ID: "pilot-user", Customers: []string{customer.ID}, Subjects: []string{"user-18"}, Value: true,
		}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := e.Do(t, http.MethodGet, "/v1/runtime/flags?environment=foreign", nil, map[string]string{"Authorization": "Bearer " + token.AccessToken})
	if res.Code != http.StatusUpgradeRequired {
		t.Fatalf("legacy SDK received subject-targeted bundle: %d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, http.MethodGet, "/v1/runtime/flags?environment=foreign", nil, map[string]string{
		"Authorization":               "Bearer " + token.AccessToken,
		api.FlagSDKCapabilitiesHeader: api.FlagSDKSubjectTargetingCapability,
	})
	if res.Code != 200 {
		t.Fatalf("runtime: %d %s", res.Code, res.Body.String())
	}
	var bundle flags.Bundle
	_ = json.Unmarshal(res.Body.Bytes(), &bundle)
	if bundle.EnvironmentID != env.ID {
		t.Fatal(bundle)
	}
	if len(bundle.Flags) != 1 || len(bundle.Flags[0].Rules) != 1 || len(bundle.Flags[0].Rules[0].Subjects) != 1 || bundle.Flags[0].Rules[0].Subjects[0] != "user-18" {
		t.Fatalf("runtime bundle lost subject targeting: %+v", bundle.Flags)
	}
	foreign, _ := signer.Mint(time.Now(), (*e.Acct()).ID, app.ID, instance.ID, "sts.amazonaws.com")
	res = e.Do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + foreign.AccessToken})
	if res.Code != 401 {
		t.Fatalf("wrong audience=%d", res.Code)
	}
	res = e.Do(t, http.MethodGet, "/v1/runtime/flags", nil, nil)
	if res.Code != 401 {
		t.Fatalf("account credential on runtime=%d", res.Code)
	}
	wrongAccount, err := signer.Mint(time.Now(), "another-account", app.ID, instance.ID, workloadidentity.FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	res = e.Do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + wrongAccount.AccessToken})
	if res.Code != 401 {
		t.Fatalf("mismatched workload account=%d", res.Code)
	}
	if err = (*e.Store()).UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	res = e.Do(t, http.MethodGet, "/v1/runtime/flags", nil, map[string]string{"Authorization": "Bearer " + token.AccessToken})
	if res.Code != 401 {
		t.Fatalf("stopped workload=%d", res.Code)
	}
}
