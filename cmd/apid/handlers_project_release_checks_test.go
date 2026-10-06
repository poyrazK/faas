package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedProjectReleaseExactServiceEvidenceAndAtomicActivation(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	configureSourceRefManagedPostgres(t, sourceRefTestEnv{acctID: e.acct.ID, srv: e.s})
	ctx := context.Background()
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string, bindings []api.AppServiceBinding) (state.App, state.Deployment) {
		t.Helper()
		app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, ServiceBindings: bindings, ServiceBindingTransport: api.ServiceBindingTransportHTTPS}})
		if err != nil {
			t.Fatal(err)
		}
		serving, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100, ImageDigest: appTaskTestDigest})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.SetDeploymentRootfs(ctx, serving.ID, "/serving/image", "serving/"+serving.ID, 4096); err != nil {
			t.Fatal(err)
		}
		return app, seedBindingCandidate(t, e, app, "production")
	}
	caller, callerDep := create("shop-api", api.ServiceBindingsForTargets([]string{"shop-billing"}))
	_, target := create("shop-billing", nil)
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, caller.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	empty := ""
	request := api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: map[string]string{"shop-api": callerDep.ID, "shop-billing": target.ID}, ExpectedActiveReleaseID: &empty}
	path := "/v1/projects/shop/environments/production/release-sets"
	check := func() api.ProjectReleaseCheckResponse {
		t.Helper()
		response := e.do(t, http.MethodPost, path+"/check", request, nil)
		if response.Code != 200 {
			t.Fatalf("check: %d %s", response.Code, response.Body.String())
		}
		var report api.ProjectReleaseCheckResponse
		if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	if report := check(); report.Passed {
		t.Fatal("missing service evidence passed")
	}
	probe := func(exact string) {
		t.Helper()
		command := []string{api.AppTaskServiceBindingProbeCommand, "shop-billing"}
		if exact != "" {
			exact = uuid.MustParse(exact).String()
			command = append(command, exact)
		}
		task := createAppTaskForTest(t, e, caller.Slug, api.CreateAppTaskRequest{VerificationDeploymentID: callerDep.ID, Command: command})
		running, err := e.store.ClaimNextAppTask(ctx, "graph-probe", time.Now().UTC(), time.Minute)
		if err != nil || running.ID != task.ID {
			t.Fatalf("claim: %+v %v", running, err)
		}
		running, err = e.store.MarkAppTaskRunning(ctx, running.ID, *running.LeaseToken, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		passed := api.ServiceBindingProbeCheck{Status: "passed"}
		output, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "shop-billing", TargetDeploymentID: exact, DNS: passed, TLS: passed, Authorization: passed, Routing: passed})
		exit := 0
		if _, err := e.store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: string(output), ExitCode: &exit, FinishedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	probe("")
	if report := check(); report.Passed {
		t.Fatal("ordinary service route evidence passed for candidate graph")
	}
	probe(target.ID)
	report := check()
	if !report.Passed || len(report.Checks) != 2 {
		t.Fatalf("exact graph check: %+v", report)
	}
	if _, err := e.store.ActiveProjectReleaseSet(ctx, e.acct.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("check activated graph: %v", err)
	}
	// Configuration can also change after a successful observation but before
	// the activation transaction. The checked store must leave the graph absent.
	e.s.store = &changingProjectReleaseStore{MemStore: e.store, before: func() {
		if err := e.store.UpsertAppEnv(ctx, e.acct.ID, caller.ID, "RACE", "changed"); err != nil {
			t.Fatal(err)
		}
	}}
	raced := e.do(t, http.MethodPost, path, request, nil)
	e.s.store = e.store
	if raced.Code != 409 {
		t.Fatalf("configuration race activated: %d %s", raced.Code, raced.Body.String())
	}
	if _, err := e.store.ActiveProjectReleaseSet(ctx, e.acct.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("race changed graph: %v", err)
	}
	probe(target.ID)
	report = check()
	if !report.Passed {
		t.Fatalf("fresh graph evidence failed: %+v", report)
	}
	unchecked := request
	unchecked.ExpectedActiveReleaseID = nil
	response := e.do(t, http.MethodPost, path, unchecked, nil)
	if response.Code != 409 {
		t.Fatalf("unchecked activation: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPost, path, request, nil)
	if response.Code != 201 {
		t.Fatalf("checked activation: %d %s", response.Code, response.Body.String())
	}
	var activated api.ProjectReleaseSetResponse
	if err := json.Unmarshal(response.Body.Bytes(), &activated); err != nil {
		t.Fatal(err)
	}
	if activated.BindingsCheck == nil || !activated.BindingsCheck.Passed || activated.BindingsCheck.GraphDigest != report.GraphDigest || !activated.Active {
		t.Fatalf("receipt: %+v", activated)
	}
	response = e.do(t, http.MethodPost, path, request, nil)
	if response.Code != 409 {
		t.Fatalf("stale active graph selector passed: %d %s", response.Code, response.Body.String())
	}
	request.ExpectedActiveReleaseID = &activated.ID
	// A configuration mutation invalidates the exact service probe.
	if err := e.store.UpsertAppEnv(ctx, e.acct.ID, caller.ID, "FEATURE", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkAppRuntimeConfigChanged(ctx, caller.ID); err != nil {
		t.Fatal(err)
	}
	response = e.do(t, http.MethodPost, path, request, nil)
	if response.Code != 409 {
		t.Fatalf("stale graph evidence: %d %s", response.Code, response.Body.String())
	}
	current, err := e.store.ActiveProjectReleaseSet(ctx, e.acct.ID, project.ID, "production")
	if err != nil || current.ID != activated.ID {
		t.Fatalf("failed check changed graph: %+v %v", current, err)
	}
}

type changingProjectReleaseStore struct {
	*state.MemStore
	before func()
}

func (s *changingProjectReleaseStore) PublishProjectReleaseSetWithBindings(ctx context.Context, account, project, environment, expected string, ttl int, members []state.ProjectReleaseMember) (state.ProjectReleaseSet, error) {
	s.before()
	return s.MemStore.PublishProjectReleaseSetWithBindings(ctx, account, project, environment, expected, ttl, members)
}
