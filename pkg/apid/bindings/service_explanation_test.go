package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A read-only catalog makes accidental task creation or lifecycle writes
// impossible in these orchestration tests. The gateway retains its full
// authorizer tests; state tests cover the actual managed route resolver.
type explanationCatalog struct {
	apps        map[string]state.App
	deployment  state.Deployment
	frozen      state.EnvironmentWorkloadRuntime
	active      state.ProjectReleaseSet
	route       state.EnvironmentGitOpsServiceBindingRoute
	activeErr   error
	routeErr    error
	releaseErr  error
	globalReads int
	managed     bool
	found       bool
	releaseID   string
	targetID    string
}

func (c *explanationCatalog) AppByID(_ context.Context, id string) (state.App, error) {
	app, ok := c.apps[id]
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}
func (c *explanationCatalog) AppBySlug(_ context.Context, slug string) (state.App, error) {
	c.globalReads++
	for _, app := range c.apps {
		if app.Slug == slug {
			return app, nil
		}
	}
	return state.App{}, state.ErrNotFound
}
func (c *explanationCatalog) ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error) {
	return state.ScenarioTestMember{}, state.ErrNotFound
}
func (c *explanationCatalog) ScenarioRehearsalNetworkRun(context.Context, string) (string, error) {
	return "", nil // These ordinary deployment fixtures have no retained rehearsal enrollment.
}
func (c *explanationCatalog) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	if id != c.deployment.ID {
		return state.Deployment{}, state.ErrNotFound
	}
	return c.deployment, nil
}
func (c *explanationCatalog) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return c.active, c.activeErr
}
func (c *explanationCatalog) ResolveEnvironmentGitOpsServiceBinding(context.Context, string, string, string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	return c.route, c.managed, c.found, c.routeErr
}
func (c *explanationCatalog) ResolveServiceRelease(context.Context, string, string, string, string) (string, string, error) {
	return c.releaseID, c.targetID, c.releaseErr
}

func explanationFixture(t *testing.T, managed bool) (*explanationCatalog, state.App, string) {
	t.Helper()
	account := uuid.NewString()
	caller := state.App{ID: uuid.NewString(), AccountID: account, ProjectID: uuid.NewString(), Slug: "frontend", Status: state.AppActive}
	target := state.App{ID: uuid.NewString(), AccountID: account, ProjectID: caller.ProjectID, Slug: "billing", Status: state.AppActive}
	if managed {
		target.Slug = "git-billing"
	}
	dep := state.Deployment{ID: uuid.NewString(), AppID: caller.ID, Scope: "production", Status: state.DeployLive, Kind: state.DeploymentKindImage, ImageDigest: "ghcr.io/example/workload@sha256:" + strings.Repeat("a", 64)}
	c := &explanationCatalog{apps: map[string]state.App{caller.ID: caller, target.ID: target}, deployment: dep, managed: managed, found: true}
	c.route = state.EnvironmentGitOpsServiceBindingRoute{CallerAppID: caller.ID, CallerDeploymentID: dep.ID, TargetAppID: target.ID, TargetDeploymentID: uuid.NewString(), ReleaseSetID: uuid.NewString(), AccountID: account, RequireHTTPS: true, CallScope: &api.ServiceCallScope{Methods: []string{"GET"}, PathPrefixes: []string{"/invoices"}}}
	c.active = state.ProjectReleaseSet{ID: c.route.ReleaseSetID, Active: true, AccountID: account, ProjectID: caller.ProjectID, EnvironmentSlug: "production", Members: []state.ProjectReleaseMember{{AppID: caller.ID, DeploymentID: dep.ID}, {AppID: target.ID, DeploymentID: c.route.TargetDeploymentID}}}
	c.frozen = state.EnvironmentWorkloadRuntime{AppID: caller.ID, AppType: state.AppTypeApp, RuntimeBase: "node22", Scope: "production", SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Generation: 1, IntentVersion: 1, Resource: "workload/frontend", WorkloadClass: state.WorkloadClassHTTP, PlanHash: strings.Repeat("b", 64), DefinitionDigest: strings.Repeat("c", 64), Runtime: map[string]json.RawMessage{}, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: dep.ImageDigest}, ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "BILLING_URL", TargetAppID: target.ID}}}
	if managed {
		c.freeze(t)
	}
	return c, caller, target.ID
}

func (c *explanationCatalog) freeze(t *testing.T) {
	t.Helper()
	raw, err := json.Marshal(c.frozen)
	if err != nil {
		t.Fatal(err)
	}
	c.deployment.EnvironmentWorkloadRuntime = string(raw)
	held := false
	c.deployment.EnvironmentWorkloadHeldValue = &held
}

func TestDeploymentServiceExplanationManagedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, result, reason string
		change               func(*explanationCatalog, *api.ExplainServiceBindingOptions)
	}{
		{"frozen method granted", "allowed", "active_reviewed_release", nil},
		{"frozen method denied despite app permission", "denied", "method_path_not_granted", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { in.Method = "POST" }},
		{"scope assertion", "denied", "deployment_scope_mismatch", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { in.Scope = "staging" }},
		{"missing binding", "denied", "service_not_bound", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { c.frozen.ServiceBindings = nil }},
		{"no active release", "denied", "active_release_missing", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { c.activeErr = state.ErrNotFound }},
		{"stale caller", "denied", "caller_not_in_active_release", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) {
			c.active.Members[0].DeploymentID = uuid.NewString()
		}},
		{"resolver rejects stale target or graph", "denied", "managed_route_denied", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { c.routeErr = state.ErrConflict }},
		{"managed miss", "denied", "service_not_bound", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { c.found = false }},
		{"context changed", "indeterminate", "managed_context_changed", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) { c.managed = false }},
		{"storage failure", "indeterminate", "managed_route_unavailable", func(c *explanationCatalog, in *api.ExplainServiceBindingOptions) {
			c.routeErr = errors.New("PRIVATE_PASSWORD")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, caller, _ := explanationFixture(t, true)
			in := api.ExplainServiceBindingOptions{DeploymentID: c.deployment.ID, Method: "GET", Path: "/invoices/123?token=PRIVATE_QUERY"}
			if tc.change != nil {
				tc.change(c, &in)
			}
			c.freeze(t)
			got, problem := ExplainServiceBinding(t.Context(), c, caller, "billing", in)
			if problem != nil {
				t.Fatal(problem)
			}
			assertExplanationReason(t, got, tc.result, tc.reason)
			if c.globalReads != 0 {
				t.Fatal("managed route fell back to global slug")
			}
			if got.Result == "allowed" && (got.TargetDeploymentID != c.route.TargetDeploymentID || got.ReleaseID != c.route.ReleaseSetID || got.Transport != api.ServiceBindingTransportHTTPS || got.PolicySource != "environment_release") {
				t.Fatalf("wrong effective route: %+v", got)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "PRIVATE_") {
				t.Fatalf("private data leaked: %s", raw)
			}
		})
	}
}

func assertExplanationReason(t *testing.T, got api.ServiceBindingExplanation, result, reason string) {
	t.Helper()
	for _, check := range got.Checks {
		if check.Reason == reason && got.Result == result {
			return
		}
	}
	t.Fatalf("want %s/%s, got %+v", result, reason, got)
}

func TestDeploymentServiceExplanationAppPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, result, reason string
		change               func(*explanationCatalog, state.App, string)
	}{
		{"ordinary route", "allowed", "unpinned_service_route", nil},
		{"project release pin", "allowed", "project_release_route", func(c *explanationCatalog, _ state.App, _ string) {
			c.releaseID = uuid.NewString()
			c.targetID = uuid.NewString()
		}},
		{"stale project release", "denied", "release_route_denied", func(c *explanationCatalog, _ state.App, _ string) { c.releaseErr = state.ErrConflict }},
		{"read failed", "indeterminate", "release_route_unavailable", func(c *explanationCatalog, _ state.App, _ string) { c.releaseErr = errors.New("PRIVATE_ERROR") }},
		{"outbound denied", "denied", "outbound_binding_denied", func(c *explanationCatalog, a state.App, _ string) {
			a.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
			c.apps[a.ID] = a
		}},
		{"inbound denied", "denied", "inbound_caller_denied", func(c *explanationCatalog, _ state.App, id string) {
			a := c.apps[id]
			empty := []string{}
			a.Manifest.AllowedServiceCallers = &empty
			c.apps[id] = a
		}},
		{"https needs bound alias", "denied", "canonical_alias_not_declared", func(c *explanationCatalog, a state.App, _ string) {
			a.Manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
			c.apps[a.ID] = a
		}},
		{"foreign target hidden", "indeterminate", "target_unreadable", func(c *explanationCatalog, _ state.App, id string) {
			a := c.apps[id]
			a.AccountID = uuid.NewString()
			c.apps[id] = a
		}},
		{"preview unsupported", "indeterminate", "preview_context_unsupported", func(c *explanationCatalog, a state.App, _ string) { a.PreviewOfSlug = "parent"; c.apps[a.ID] = a }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, caller, target := explanationFixture(t, false)
			if tc.change != nil {
				tc.change(c, caller, target)
			}
			got, p := ExplainServiceBinding(t.Context(), c, c.apps[caller.ID], "billing", api.ExplainServiceBindingOptions{DeploymentID: c.deployment.ID})
			if p != nil {
				t.Fatal(p)
			}
			assertExplanationReason(t, got, tc.result, tc.reason)
			if tc.name == "foreign target hidden" && (got.TargetAppID != "" || got.TargetFingerprint != "") {
				t.Fatal("foreign identity leaked")
			}
		})
	}
}

func TestDeploymentServiceExplanationRealProjectRelease(t *testing.T) {
	m := state.NewMemStore()
	acct, err := m.CreateAccount(t.Context(), "explain-release@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "explain-release"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) (state.App, state.Deployment) {
		t.Helper()
		app, err := m.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: slug, Status: state.AppActive, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := m.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + slug})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
			t.Fatal(err)
		}
		return app, dep
	}
	caller, dep := create("explain-release-caller")
	target, targetDep := create("explain-release-target")
	release, err := m.PublishProjectReleaseSet(t.Context(), acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: caller.ID, DeploymentID: dep.ID}, {AppID: target.ID, DeploymentID: targetDep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got, problem := ExplainServiceBinding(t.Context(), m, caller, target.Slug, api.ExplainServiceBindingOptions{DeploymentID: dep.ID})
	if problem != nil {
		t.Fatal(problem)
	}
	if got.Result != "allowed" || got.ReleaseID != release.ID || got.TargetDeploymentID != targetDep.ID {
		t.Fatalf("actual release route=%+v", got)
	}
	candidate, err := m.CreateDeployment(t.Context(), state.Deployment{AppID: caller.ID, Scope: "production", Status: state.DeployLive, ImageDigest: dep.ImageDigest, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	got, problem = ExplainServiceBinding(t.Context(), m, caller, target.Slug, api.ExplainServiceBindingOptions{DeploymentID: candidate.ID})
	if problem != nil {
		t.Fatal(problem)
	}
	assertExplanationReason(t, got, "denied", "release_route_denied")
	if got.TargetDeploymentID != "" {
		t.Fatalf("candidate fell back to another deployment: %+v", got)
	}
}
