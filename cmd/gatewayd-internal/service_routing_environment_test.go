// adr: 590
package main

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// A service call stays in its caller's environment: production reaches only
// the target's production lane, a stage only its own deployment, and a stage
// whose target has no deployment there gets no weights (the proxy refuses it).
func TestServiceRoutingSnapshotPostgresStaysInCallerEnvironment(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "service-routing-environments@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "service-environments"})
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, WorkloadName: slug,
			Type: state.AppTypeApp, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := createApp("environments-client"), createApp("environments-target")
	live := func(app state.App, scope, digest string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			Scope: scope, ImageDigest: digest, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	callerProduction, callerStaging, callerReview := live(caller, "production", "sha256:cp"), live(caller, "staging", "sha256:cs"), live(caller, "review", "sha256:cq")
	live(target, state.DefaultEnvScope, "sha256:legacy")
	targetProduction, targetStaging := live(target, "production", "sha256:tp"), live(target, "staging", "sha256:ts")

	pin := func(callerDeployment, override string) *gateway.ServiceRoutingSnapshot {
		t.Helper()
		inputs := gateway.ServicePolicyRoutingInputs{CallerDeploymentID: callerDeployment, ReleaseValid: true, Method: "GET", TargetPath: "/"}
		if override != "" {
			inputs.OverridePresent, inputs.OverrideValid, inputs.OverrideDeploymentID = true, true, override
		}
		policy, err := newServicePolicyPinner(store)(gateway.WithServicePolicyRoutingInputs(t.Context(), inputs), caller.ID, target.Slug, false)
		if err != nil || policy.Routing == nil {
			t.Fatalf("pin from %q: %+v %v", callerDeployment, policy.Routing, err)
		}
		return policy.Routing
	}
	weightIDs := func(routing *gateway.ServiceRoutingSnapshot) []string {
		var ids []string
		for _, weight := range routing.Weights {
			ids = append(ids, weight.ID)
		}
		return ids
	}
	for _, tc := range []struct {
		name, caller, environment string
		want                      []string
	}{
		// Named production carries traffic, so the legacy default lane drops out.
		{"production", callerProduction.ID, "", []string{targetProduction.ID}},
		{"unknown caller deployment is production", "", "", []string{targetProduction.ID}},
		{"staging", callerStaging.ID, "staging", []string{targetStaging.ID}},
		{"stage without a target deployment", callerReview.ID, "review", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routing := pin(tc.caller, "")
			if routing.CallerEnvironment != tc.environment || !slices.Equal(weightIDs(routing), tc.want) {
				t.Fatalf("environment=%q weights=%v, want %q %v", routing.CallerEnvironment, weightIDs(routing), tc.environment, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, caller, override string
		want                   bool
	}{
		{"production pins production", callerProduction.ID, targetProduction.ID, true},
		{"production cannot pin a stage", callerProduction.ID, targetStaging.ID, false},
		{"stage pins its own deployment", callerStaging.ID, targetStaging.ID, true},
		{"stage cannot pin production", callerStaging.ID, targetProduction.ID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routing := pin(tc.caller, tc.override)
			if !routing.OverrideChecked || routing.OverrideAllowed != tc.want {
				t.Fatalf("override = %+v, want allowed=%v", routing, tc.want)
			}
		})
	}
}
