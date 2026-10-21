package state

import (
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionServiceGraphMemory(t *testing.T) {
	testPromotionServiceGraph(t, func(*testing.T) Store { return NewMemStore() })
}

func serviceGraphHealthPolicy(f *promotionSnapshotFixture) *api.ProjectPromotionHealthPolicy {
	policy := &api.ProjectPromotionHealthPolicy{}
	for _, app := range f.apps {
		if app.WorkloadClass == WorkloadClassWorker {
			continue
		}
		policy.Probes = append(policy.Probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/health"})
	}
	sort.Slice(policy.Probes, func(i, j int) bool { return policy.Probes[i].AppID < policy.Probes[j].AppID })
	return policy
}

func testPromotionServiceGraph(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"publish_rollback", "expired", "failed", "incomplete", "operator_edit", "no_health"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f := newPromotionSnapshotFixture(t, factory(t), false, true)
			if f.view.DestinationConfigHash == "" || f.view.DestinationConfigHash == f.promotion.SourceConfigHash {
				t.Fatal("resolved destination configuration has no distinct identity")
			}
			if scenario == "no_health" {
				_, _, err := f.store.CreateProjectEnvironmentPromotion(ctx, f.promotion, f.workloads)
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatal("service graph admitted without health", err)
				}
				return
			}
			f.promotion.HealthPolicy = serviceGraphHealthPolicy(f)
			f.create(t)
			f.prepare(t)
			candidate, err := f.store.DeploymentByID(ctx, f.candidates[1].DeploymentID)
			requirePromotionSnapshot(t, err)
			var overrides map[string]string
			requirePromotionSnapshot(t, json.Unmarshal(candidate.OverrideEnv, &overrides))
			if overrides[api.ServiceBindingEnvKey("api")] != "https://api.internal" {
				t.Fatal("candidate persisted source override URL")
			}

			bindings := f.store.(ProjectServiceGraphStore)
			_, noBindingsManaged, noBindingsFound, noBindingsErr := bindings.ResolveProjectServiceBinding(ctx, f.apps[0].ID, f.candidates[0].DeploymentID, "web")
			if noBindingsErr != nil || !noBindingsManaged || noBindingsFound {
				t.Fatal("undeclared candidate call fell through to ordinary routing", noBindingsErr)
			}

			assertRoute := func(deployment, target, graph string) {
				t.Helper()
				route, managed, found, err := bindings.ResolveProjectServiceBinding(ctx, f.apps[1].ID, deployment, "api")
				requirePromotionSnapshot(t, err)
				if !managed || !found || route.TargetDeploymentID != target || route.ReleaseSetID != graph || !route.RequireHTTPS {
					t.Fatalf("wrong graph route: %+v managed=%v found=%v", route, managed, found)
				}
			}
			assertRoute(f.candidates[1].DeploymentID, f.candidates[0].DeploymentID, f.promotion.ID)
			assertRoute(f.previous[1].ID, f.previous[0].ID, f.targetGraph.ID)
			_, managedHint, foundHint, errHint := bindings.ResolveProjectServiceBinding(ctx, f.apps[1].ID, f.candidates[1].DeploymentID, "api", f.targetGraph.ID)
			if !managedHint || foundHint || !errors.Is(errHint, ErrConflict) {
				t.Fatal("candidate accepted baseline release hint", errHint)
			}
			routeHint, _, foundHint, errHint := bindings.ResolveProjectServiceBinding(ctx, f.apps[1].ID, f.candidates[1].DeploymentID, "api", f.promotion.ID)
			if errHint != nil || !foundHint || routeHint.TargetDeploymentID != f.candidates[0].DeploymentID {
				t.Fatal("candidate rejected its own private graph hint", errHint)
			}

			_, managed, found, err := bindings.ResolveProjectServiceBinding(ctx, f.apps[1].ID, f.candidates[1].DeploymentID, "other-service")
			if err != nil || !managed || found {
				t.Fatal("undeclared binding escaped frozen graph", err)
			}
			_, _, _, err = bindings.ResolveProjectServiceBinding(ctx, f.apps[0].ID, f.candidates[1].DeploymentID, "api")
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			h, err := f.store.(ProjectPromotionHealthStore).ProjectEnvironmentPromotionHealth(ctx, f.account.ID, f.promotion.ID)
			requirePromotionSnapshot(t, err)
			if scenario == "expired" || scenario == "failed" || scenario == "incomplete" {
				switch scenario {
				case "expired":
					h.Deadline = time.Now().Add(-time.Second)
				case "failed":
					h.Phase = "failed"
				case "incomplete":
					requirePromotionSnapshot(t, f.store.UpdateDeploymentStatus(ctx, f.candidates[0].DeploymentID, DeployFailed, "test failure"))
				}
				if scenario != "incomplete" {
					setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
				}
				_, managed, found, err = bindings.ResolveProjectServiceBinding(ctx, f.apps[1].ID, f.candidates[1].DeploymentID, "api")
				if !managed || found || !errors.Is(err, ErrConflict) {
					t.Fatal("private candidate graph survived revocation", err)
				}
				assertRoute(f.previous[1].ID, f.previous[0].ID, f.targetGraph.ID)
				return
			}
			checked := time.Now().UTC()
			h.Phase, h.Verdict, h.HealthyRounds, h.CheckedAt = "cutover", "healthy", api.ProjectReleaseVerificationRounds, &checked
			h.Evidence = promotionHealthEvidence(h.Policy, 0, 0)
			setPromotionHealthTestCheckpoint(t, f.store, f.promotion.ID, h)
			publisher := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			if scenario == "operator_edit" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[1].ID, "production", "MODE", "operator-change"))
				if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); !errors.Is(err, ErrConflict) {
					t.Fatal("overwrote operator intent", err)
				}
				return
			}
			release, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			assertRoute(f.candidates[1].DeploymentID, f.candidates[0].DeploymentID, release.ID)
			assertRoute(f.previous[1].ID, f.previous[0].ID, f.targetGraph.ID)
			config, err := f.store.ProjectEnvironmentConfigLatest(ctx, f.account.ID, f.project.ID, "production")
			requirePromotionSnapshot(t, err)
			if config.ConfigHash != f.view.DestinationConfigHash {
				t.Fatal("publication copied source configuration document")
			}
			var document struct {
				Env map[string]map[string]string `json:"env"`
			}
			requirePromotionSnapshot(t, json.Unmarshal(config.Values, &document))
			if document.Env["web"][api.ServiceBindingEnvKey("api")] != "https://api.internal" {
				t.Fatal("document retained source HTTP URL")
			}
			runtime, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, f.apps[1].ID, f.candidates[1].DeploymentID)
			requirePromotionSnapshot(t, err)
			for _, row := range runtime.Values {
				if document.Env["web"][row.Key] != row.Value {
					t.Fatal("runtime pins differ from destination document", row.Key)
				}
			}
			if _, err = publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); err != nil {
				t.Fatal("publication replay rejected resolved destination", err)
			}
			restored, err := publisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			requirePromotionSnapshot(t, err)
			assertRoute(f.previous[1].ID, f.previous[0].ID, restored.ID)
			config, err = f.store.ProjectEnvironmentConfigLatest(ctx, f.account.ID, f.project.ID, "production")
			requirePromotionSnapshot(t, err)
			if config.ConfigHash != f.promotion.PreviousTargetConfigHash {
				t.Fatal("rollback lost previous configuration")
			}
		})
	}
}

func TestPromotionGeneratedBindingValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "tampered_url", "unknown_key", "external_target", "removed_dependency", "secret_collision", "quota", "override"} {
		t.Run(scenario, func(t *testing.T) {
			bindings := api.ServiceBindingsForTargets([]string{"api"})
			w := promotionReleaseWorkload{AppID: "web", Name: "web", SourceSettings: ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{ServiceBindings: bindings}},
				TargetSettings: ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{ServiceBindings: bindings, ServiceBindingTransport: api.ServiceBindingTransportHTTPS}}}
			w.SourceVariables = []projectReleaseVariable{{Key: api.ServiceBindingEnvKey("api"), Value: "http://api.svc.gregale:10081"}}
			c := promotionReleaseConfiguration{AccountID: "account", From: "staging", To: "production", Workloads: []promotionReleaseWorkload{{AppID: "api", Name: "api"}, w}}
			limit := api.MustLimitsFor(api.PlanPro).EnvVarsMax
			switch scenario {
			case "tampered_url":
				w.SourceVariables[0].Value = "https://staging.example.test"
			case "unknown_key":
				w.SourceVariables[0].Key = "GREGALE_SERVICE_UNKNOWN_URL"
			case "external_target":
				w.TargetSettings.Manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"external"})
			case "removed_dependency":
				w.TargetSettings.Manifest.ServiceBindings = nil
			case "secret_collision":
				w.ReservedKeys = []string{api.ServiceBindingEnvKey("api")}
			case "quota":
				limit = 1
			case "override":
				w.Source.OverrideEnv = json.RawMessage(`{"GREGALE_SERVICE_API_URL":"http://api.svc.gregale:10081","USER_VALUE":"keep"}`)
			}
			err := c.prepareServiceBindings(&w, limit)
			if scenario != "valid" && scenario != "override" {
				if err == nil {
					t.Fatal("unsupported generated binding accepted")
				}
				return
			}
			requirePromotionSnapshot(t, err)
			if w.PromotedVariables == nil || len(*w.PromotedVariables) != 3 {
				t.Fatal("destination binding companions not pinned")
			}
			if scenario == "override" {
				var values map[string]string
				requirePromotionSnapshot(t, json.Unmarshal(*w.PromotedOverrideEnv, &values))
				if values[api.ServiceBindingEnvKey("api")] != "https://api.internal" || values["USER_VALUE"] != "keep" {
					t.Fatal("override rewrite lost destination URL or user setting")
				}
			}
		})
	}
}

func TestPromotionFrozenServiceGrants(t *testing.T) {
	allowed := []string{"web"}
	scopes := api.ServiceCallerScopes{"web": {Methods: []string{"GET"}, PathPrefixes: []string{"/health"}}}
	sourceAllowed := []string{"source-only"}
	source := ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{AllowedServiceCallers: &sourceAllowed}}
	target := ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{AllowedServiceCallers: &allowed, AllowedServiceCallScopes: &scopes}}
	promoted, err := SnapshotWorkloadSettingsForPromotion(source, target, true)
	requirePromotionSnapshot(t, err)
	if (*promoted.Manifest.AllowedServiceCallers)[0] != "web" {
		t.Fatal("promotion imported source service grants")
	}
	legacy, err := SnapshotWorkloadSettingsForPromotion(source, target)
	requirePromotionSnapshot(t, err)
	if (*legacy.Manifest.AllowedServiceCallers)[0] != "source-only" {
		t.Fatal("existing literal-only capture changed behavior")
	}
	caller := projectServiceMember{AppID: "web-id", AccountID: "account", ProjectID: "project", Name: "web", Slug: "project-web", DeploymentID: "web-dep", Scope: "production", Live: true,
		Settings: ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"api"})}}}
	peer := projectServiceMember{AppID: "api-id", AccountID: "account", ProjectID: "project", Name: "api", Slug: "project-api", DeploymentID: "api-dep", Scope: "production", Live: true, Settings: promoted}
	caller.Hash, err = WorkloadSettingsHash(caller.Settings)
	requirePromotionSnapshot(t, err)
	peer.Hash, err = WorkloadSettingsHash(peer.Settings)
	requirePromotionSnapshot(t, err)
	input := projectServiceGraphInput{Caller: caller, Graphs: []projectServiceGraph{{ID: "graph", Members: []projectServiceMember{caller, peer}}}}
	route, managed, found, err := resolveProjectServiceGraph(input, "api")
	requirePromotionSnapshot(t, err)
	if !managed || !found || route.CallScope == nil || !route.CallScope.Allows("GET", "/health/database") || route.CallScope.Allows("POST", "/health") {
		t.Fatal("destination method/path grant was widened")
	}
	input.Graphs[0].Members[1].Scope = "staging"
	if _, _, _, err = resolveProjectServiceGraph(input, "api"); !errors.Is(err, ErrConflict) {
		t.Fatal("binding crossed environments", err)
	}
}
