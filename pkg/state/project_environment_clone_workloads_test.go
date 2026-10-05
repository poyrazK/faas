package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneWorkloadTestStore interface {
	cloneReservationStore
	cloneFlagFixtureStore
	state.ProjectEnvironmentCloneWorkloadStore
	state.ProjectEnvironmentWorkloadSpecStore
	state.ProjectEnvironmentClonePublicationStore
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneValuesStore
	state.ProjectReleaseSetReader
	state.ProjectPromotionDeploymentStore
	ProjectEnvironmentWorkloadSpecForDeployment(context.Context, string, string, string) (state.ProjectEnvironmentWorkloadSpec, error)
	DeploymentSidecarSecretReloadSignal(context.Context, string, string) (string, error)
	GetProjectEnvironmentEdgePolicy(context.Context, string, string, string) (state.ProjectEnvironmentEdgePolicy, error)
	PutProjectEnvironmentEdgePolicy(context.Context, state.ProjectEnvironmentEdgePolicy) (state.ProjectEnvironmentEdgePolicy, error)
	GetProjectEnvironmentRoutePolicy(context.Context, string, string, string) (state.ProjectEnvironmentRoutePolicy, error)
}

// ADR-590: a clone reuses the selected immutable artifacts and actual deployed
// settings. It must survive a source head edit and a later production rollout.
func TestMemProjectEnvironmentCloneCapturesAndPreparesWorkloads(t *testing.T) {
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, state.NewMemStore())
}

// ADR-590: captured state must appear in the completeness receipt, even when
// the target contains matching values and all workloads are already live.
func TestMemProjectEnvironmentCloneRequiresProjectConfigurationReceipt(t *testing.T) {
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, state.NewMemStore(), true)
}

func projectEnvironmentCloneCapturesAndPreparesWorkloads(t *testing.T, s cloneWorkloadTestStore, omitConfigReceipt ...bool) {
	t.Helper()
	projectEnvironmentClonePublicationContract(t, s, len(omitConfigReceipt) > 0 && omitConfigReceipt[0], "")
}

func projectEnvironmentClonePublicationContract(t *testing.T, s cloneWorkloadTestStore, missingConfigReceipt bool, fault string, policyMutation ...func(context.Context, string) error) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, "workload-clone@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "captured"})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	var sources []state.Deployment
	for i, scope := range []string{"production", "default"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: fmt.Sprintf("service-%d", i), WorkloadName: fmt.Sprintf("service-%d", i), Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{RevisionPinTTLSeconds: 1800}})
		if err != nil {
			t.Fatal(err)
		}
		settings, err := state.WorkloadSettingsFromApp(app)
		if err != nil {
			t.Fatal(err)
		}
		settings.RAMMB, settings.StartCommand = 256, "serve captured"
		settings.RetryPolicyJSON = json.RawMessage(`{"max_attempts":2,"backoff_multiplier":1e0}`)
		settings.PublicAuthBasicSealed = []byte("sealed-basic")
		if i == 0 {
			settings.OnlyAllowDeclaredRoutes, settings.DeclaredRoutes = true, []state.DeclaredRoute{{Path: "/captured", Methods: []string{"GET"}}}
			if _, err := s.PutProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "production", app.ID, 0, settings); err != nil {
				t.Fatal(err)
			}
		}
		source, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured",
			OverrideEnv: json.RawMessage(`{"Z":"last","A":"first","EXACT":9007199254740993}`), OverridePort: 8080,
			Sidecars: json.RawMessage(`[{"name":"metrics","type":"sidecar","image":"metrics@sha256:captured"}]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, source.ID, "/immutable/source.ext4", "layers/source.ext4", 4096); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: source.ID, SidecarName: "metrics", StorageKey: "layers/metrics.ext4", Bytes: 2048, ContentDigest: "sha256:metrics"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentSecretReloadSignal(ctx, source.ID, "SIGUSR1"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentSidecarSecretReloadSignal(ctx, source.ID, "metrics", "SIGHUP"); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, source.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, scope, "CAPTURED", "source-variable-at-capture"); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppSecretWithClassInScope(ctx, a.ID, app.ID, scope, "TOKEN", "age1-captured", "1111111111111111", state.SecretClassEphemeral, []byte("sealed-before-capture")); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppSecretWithClassInScope(ctx, a.ID, app.ID, scope, "TOKEN", "age1-captured", "2222222222222222", state.SecretClassEphemeral, []byte("sealed-at-capture")); err != nil {
			t.Fatal(err)
		}
		apps, sources = append(apps, app), append(sources, source)
	}
	if _, err := s.PutProjectEnvironmentEdgePolicy(ctx, cloneWorkloadEdgePolicy(a.ID, p.ID, apps[0].ID, "production", "captured-policy")); err != nil {
		t.Fatal(err)
	}
	configValues, configHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"feature":"captured","large":9007199254740993123,"factor":1e0}`))
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "production", ConfigHash: configHash, Values: configValues,
	})
	if err != nil {
		t.Fatal(err)
	}
	valuesSnapshot, err := s.CaptureProjectEnvironmentCloneValues(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	sourceEnvironment, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	flagScope := state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: sourceEnvironment.ID}
	sourceFlags, _ := cloneFlagFixture(t, s, flagScope)
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "capture", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), 10*time.Minute)
	if err != nil || lease.Operation.ID != op.ID {
		t.Fatalf("claim workload operation: %v", err)
	}
	op = lease.Operation
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 2 {
		t.Fatalf("capture = %+v, %v", views, err)
	}
	byApp := map[string]state.ProjectEnvironmentCloneWorkload{}
	for _, view := range views {
		byApp[view.AppID] = view
		if view.SourceValuesHash == "" {
			t.Fatal("capture omitted scoped variable and sealed-secret identity")
		}
		if view.SourceProjectConfigHash != configHash {
			t.Fatal("capture omitted the selected project configuration identity")
		}
	}
	for i, app := range apps {
		if byApp[app.ID].SourceDeploymentID != sources[i].ID {
			t.Fatalf("wrong artifact: %+v", byApp[app.ID])
		}
	}
	public, _ := json.Marshal(views)
	if strings.Contains(string(public), "source-variable-at-capture") || strings.Contains(string(public), "sealed-at-capture") || strings.Contains(string(public), "age1-captured") {
		t.Fatal("public clone capture metadata exposed source values")
	}
	// Edit the desired source without rebuilding; the deployed pin is truth.
	newFlags, err := s.GetFeatureFlags(ctx, flagScope, 0)
	if err != nil {
		t.Fatal(err)
	}
	newFlags.Flags[0].Description = "source-after-capture"
	if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope, ExpectedVersion: newFlags.Version, Config: newFlags.Config, Actor: "developer"}); err != nil {
		t.Fatal(err)
	}
	current, err := s.ProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "production", apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Settings.RAMMB, current.Settings.StartCommand = 512, "serve newer"
	if _, err := s.PutProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "production", apps[0].ID, current.Revision, current.Settings); err != nil {
		t.Fatal(err)
	}
	newer, err := s.CreateDeployment(ctx, state.Deployment{AppID: apps[0].ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, newer.ID, "/newer.ext4", "layers/newer.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	for i, app := range apps {
		if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, sources[i].Scope, "CAPTURED", "source-variable-after-capture"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteAppSecretInScope(ctx, a.ID, app.ID, sources[i].Scope, "TOKEN"); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppSecretWithClassInScope(ctx, a.ID, app.ID, sources[i].Scope, "NEW_TOKEN", "age1-newer", "3333333333333333", state.SecretClassPersistent, []byte("sealed-after-capture")); err != nil {
			t.Fatal(err)
		}
	}
	// ADR-590: a named-production rollout cannot replace the captured legacy
	// default value scope used by the second workload.
	newNamed, err := s.CreateDeployment(ctx, state.Deployment{AppID: apps[1].ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer-named"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, newNamed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, a.ID, apps[1].ID, "production", "DECOY", "new-serving-scope"); err != nil {
		t.Fatal(err)
	}
	newConfigValues, newConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"feature":"newer"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "production", ConfigHash: newConfigHash, Values: newConfigValues,
	}); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); err != nil || len(replay) != 2 || replay[0].SourceHash != views[0].SourceHash || replay[1].SourceHash != views[1].SourceHash {
		t.Fatalf("recaptured changed source: %+v, %v", replay, err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision-1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale capture = %v", err)
	}
	for _, app := range apps {
		if _, err := s.PutProjectEnvironmentEdgePolicy(ctx, cloneWorkloadEdgePolicy(a.ID, p.ID, app.ID, "production", "changed-source-policy")); err != nil {
			t.Fatal(err)
		}
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"}}
	for _, view := range views {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "workload", Name: view.WorkloadSlug, SourceID: view.SourceDeploymentID, SourceVersion: view.SourceHash, Status: "captured"})
		for _, kind := range []string{"variables", "secrets"} {
			if fault == "omit_"+kind {
				continue
			}
			version := view.SourceValuesHash
			if fault == "wrong_value_hash" && kind == "variables" {
				version = strings.Repeat("0", 64)
			}
			resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: view.WorkloadSlug, SourceID: view.AppID, TargetID: view.AppID, SourceVersion: version, Status: "ready"})
		}
	}
	if !missingConfigReceipt {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "project_config", Name: "production", SourceVersion: configHash, Status: "ready"})
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	clone := state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID, SourceSlug: "production", TargetSlug: "stage", CloneOperationID: op.ID, CloneOperationRevision: op.Revision,
		ExpectedSourceValueScopes: valuesSnapshot.ValueScopes, ExpectedSourceValuesHash: valuesSnapshot.Hash}
	limits := api.MustLimitsFor(a.Plan)
	limits.EnvVarsMax = 1
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, limits); !errors.Is(err, state.ErrProjectEnvironmentCloneQuota) {
		t.Fatalf("captured values exceeded quota without rejection: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed frozen-value preflight materialized the target: %v", err)
	}
	_, result, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(a.Plan))
	if err != nil {
		t.Fatal(err)
	}
	targetEnvironment, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	flagScope.EnvironmentID = targetEnvironment.ID
	copiedFlags, err := s.GetFeatureFlags(ctx, flagScope, 0)
	if err != nil || copiedFlags.Version != 1 || !reflect.DeepEqual(copiedFlags.Config, sourceFlags.Config) {
		t.Fatalf("target flags reread mutable source instead of frozen capture: %v", err)
	}
	mutateFlags := func(sameConfig bool) error {
		config := copiedFlags.Config
		if !sameConfig {
			config.Flags[0].Description = "target-after-materialization"
		}
		_, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope, ExpectedVersion: copiedFlags.Version, Config: config, Actor: "developer"})
		return err
	}
	if result.VariablesCopied != 2 || result.SecretsCopied != 2 {
		t.Fatalf("frozen scoped value counts: %+v", result)
	}
	for _, app := range apps {
		variables, err := s.ListAppEnvInScope(ctx, a.ID, app.ID, "stage")
		if err != nil || len(variables) != 1 || variables[0].Key != "CAPTURED" || variables[0].Value != "source-variable-at-capture" {
			t.Fatalf("target variables did not use the capture: count=%d, err=%v", len(variables), err)
		}
		secrets, err := s.ListAppSecretsInScope(ctx, a.ID, app.ID, "stage")
		if err != nil || len(secrets) != 1 || secrets[0].Key != "TOKEN" || string(secrets[0].Ciphertext) != "sealed-at-capture" ||
			secrets[0].Kid != "age1-captured" || secrets[0].ValueHash != "2222222222222222" || secrets[0].SecretVersion != 2 || secrets[0].SecretClass != state.SecretClassEphemeral ||
			secrets[0].DeliveryVersion != 1 || secrets[0].DeliveredVersion != 0 || secrets[0].DeliveryStatus != state.SecretDeliveryPending {
			t.Fatalf("target sealed secret did not retain its captured revision with independent delivery: count=%d, err=%v", len(secrets), err)
		}
	}
	targetConfig, err := s.ProjectEnvironmentConfigLatest(ctx, a.ID, p.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	_, expectedValuesHash, _ := api.NormalizeProjectEnvironmentConfig(config.Values)
	_, actualValuesHash, _ := api.NormalizeProjectEnvironmentConfig(targetConfig.Values)
	if targetConfig.ConfigHash != configHash || expectedValuesHash != actualValuesHash {
		t.Fatal("clone copied the newer project configuration instead of its immutable capture")
	}
	targets := map[string]state.Deployment{}
	for _, app := range apps {
		spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Settings.RAMMB != 256 || (app.ID == apps[0].ID && spec.Settings.StartCommand != "serve captured") {
			t.Fatalf("copied desired source instead of capture: %+v", spec.Settings)
		}
		route, err := s.GetProjectEnvironmentRoutePolicy(ctx, a.ID, app.ID, "stage")
		if err != nil || route.OnlyAllowDeclaredRoutes != spec.Settings.OnlyAllowDeclaredRoutes || (app.ID == apps[0].ID && (len(route.DeclaredRoutes) != 1 || route.DeclaredRoutes[0].Path != "/captured")) {
			t.Fatalf("target routes ignored deployed capture: %v", err)
		}
		edge, err := s.GetProjectEnvironmentEdgePolicy(ctx, a.ID, app.ID, "stage")
		if app.ID == apps[0].ID {
			if err != nil || len(edge.Rules) != 1 || len(edge.Rules[0].Action.Headers.ResponseHeaders) != 1 || edge.Rules[0].Action.Headers.ResponseHeaders[0].Value != "captured-policy" {
				t.Fatalf("target edge policy read changed source configuration: %v", err)
			}
		} else if !errors.Is(err, state.ErrNotFound) {
			t.Fatal("source policy added after capture leaked into stage")
		}
		if _, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision-1, app.ID, spec.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale preparation = %v", err)
		}
		if _, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision, app.ID, strings.Repeat("f", 64)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("wrong target settings = %v", err)
		}
		target, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision, app.ID, spec.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if target.ID == byApp[app.ID].SourceDeploymentID || target.ImageDigest != "sha256:captured" || target.RootfsKey != "layers/source.ext4" || target.Status != state.DeployPending ||
			target.TrafficPercent != 0 || !target.TrafficPercentExplicit || target.SourcePath != "" || target.BuildID != "" || target.GitHubSourceRef != "" || target.CanaryTotalSteps != 0 {
			t.Fatalf("target not isolated/captured/dark: %+v", target)
		}
		if !strings.Contains(string(target.OverrideEnv), "9007199254740993") {
			t.Fatalf("numeric config changed: %s", target.OverrideEnv)
		}
		pin, err := s.ProjectEnvironmentWorkloadSpecForDeployment(ctx, a.ID, p.ID, target.ID)
		if err != nil || pin.Hash != spec.Hash {
			t.Fatalf("target pin = %+v, %v", pin, err)
		}
		layers, err := s.ListDeploymentSidecarLayers(ctx, target.ID)
		if err != nil || len(layers) != 1 || layers[0].StorageKey != "layers/metrics.ext4" {
			t.Fatalf("layers = %+v, %v", layers, err)
		}
		if signal, err := s.DeploymentSidecarSecretReloadSignal(ctx, target.ID, "metrics"); err != nil || signal != "SIGHUP" {
			t.Fatalf("sidecar signal = %q, %v", signal, err)
		}
		if target.SecretReloadSignal != "SIGUSR1" || !target.SecretReloadSignalKnown {
			t.Fatalf("main signal missing: %+v", target)
		}
		replay, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision, app.ID, spec.Hash)
		if err != nil || replay.ID != target.ID {
			t.Fatalf("duplicate preparation = %+v, %v", replay, err)
		}
		targets[app.ID] = target
	}
	for i := range resources {
		for _, app := range apps {
			if resources[i].Kind == "workload" && resources[i].Name == app.Slug {
				resources[i].TargetID, resources[i].Status = targets[app.ID].ID, "ready"
			}
		}
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pending workloads published: %v", err)
	}
	for _, d := range targets {
		if err := s.MarkDeploymentLiveDark(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	changed := targets[apps[0].ID]
	if err := s.SetDeploymentRootfs(ctx, changed.ID, "/other.ext4", "layers/other.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("altered artifact published: %v", err)
	}
	if err := s.SetDeploymentRootfs(ctx, changed.ID, "/immutable/source.ext4", "layers/source.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if missingConfigReceipt {
		if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("project configuration omitted from completeness receipt: %v", err)
		}
		if _, err := s.ActiveProjectReleaseSet(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("incomplete clone acquired a serving graph: %v", err)
		}
		return
	}
	if fault == "before_variable" {
		if err := s.UpsertAppEnvInScope(ctx, a.ID, apps[0].ID, "stage", "CAPTURED", "changed-private-target-value"); err != nil {
			t.Fatal(err)
		}
	}
	if fault == "before_flags" {
		if err := mutateFlags(false); err != nil {
			t.Fatal(err)
		}
	}
	if fault == "before_edge_policy" {
		if _, err := s.PutProjectEnvironmentEdgePolicy(ctx, cloneWorkloadEdgePolicy(a.ID, p.ID, apps[0].ID, "stage", "changed-private-target-policy")); err != nil {
			t.Fatal(err)
		}
	}
	if fault == "before_route_policy" {
		if len(policyMutation) != 1 {
			t.Fatal("missing route policy fault injector")
		}
		if err := policyMutation[0](ctx, apps[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(fault, "before_") || strings.HasPrefix(fault, "omit_") || fault == "wrong_value_hash" {
		_, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, "")
		assertCloneValuePublicationRejected(t, s, a.ID, p.ID, op, err)
		return
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationReady, op.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ready without atomic graph publication: %v", err)
	}
	if _, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision-1, 1800); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale publication = %v", err)
	}
	// The final transaction rechecks head changes after the earlier proof.
	head, err := s.ProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	original := head.Settings
	head.Settings.RAMMB = 512
	edited, err := s.PutProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", apps[0].ID, head.Revision, head.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision, 1800); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("edited head published: %v", err)
	}
	if _, err := s.PutProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", apps[0].ID, edited.Revision, original); err != nil {
		t.Fatal(err)
	}
	// The source version hash alone is insufficient: a corrupted writer could
	// persist different values under the same hash. Publication authenticates
	// both the captured identity and the stored target payload.
	if _, err := s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "stage", ConfigHash: configHash, Values: newConfigValues,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision, 1800); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed project values published under captured hash: %v", err)
	}
	if _, err := s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "stage", ConfigHash: configHash, Values: config.Values,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(fault, "after_") {
		var mutationErr error
		switch fault {
		case "after_flags", "after_flags_same_config":
			mutationErr = mutateFlags(fault == "after_flags_same_config")
		case "after_variable":
			mutationErr = s.UpsertAppEnvInScope(ctx, a.ID, apps[0].ID, "stage", "CAPTURED", "changed-private-target-value")
		case "after_extra_variable":
			mutationErr = s.UpsertAppEnvInScope(ctx, a.ID, apps[0].ID, "stage", "EXTRA", "changed-private-target-value")
		case "after_secret_ciphertext":
			// Keep the public digest unchanged: the actual sealed envelope
			// must still be authenticated by the final publication proof.
			mutationErr = s.ResealAppSecretWithKidAndValueHashInScope(ctx, a.ID, apps[0].ID, "stage", "TOKEN", "age1-captured", "2222222222222222", []byte("changed-private-target-envelope"))
		case "after_secret_deleted":
			mutationErr = s.DeleteAppSecretInScope(ctx, a.ID, apps[0].ID, "stage", "TOKEN")
		case "after_edge_policy":
			_, mutationErr = s.PutProjectEnvironmentEdgePolicy(ctx, cloneWorkloadEdgePolicy(a.ID, p.ID, apps[0].ID, "stage", "changed-private-target-policy"))
		case "after_route_policy":
			if len(policyMutation) != 1 {
				t.Fatal("missing route policy fault injector")
			}
			mutationErr = policyMutation[0](ctx, apps[0].ID)
		case "after_publication_lease_wait":
			if len(policyMutation) != 1 {
				t.Fatal("missing publication lease fault injector")
			}
			mutationErr = policyMutation[0](ctx, apps[0].ID)
		default:
			t.Fatalf("unknown publication fault: %s", fault)
		}
		if mutationErr != nil {
			t.Fatal(mutationErr)
		}
		_, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision, 1800)
		assertCloneValuePublicationRejected(t, s, a.ID, p.ID, op, err)
		return
	}
	release, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision, 1800)
	if err != nil || len(release.Members) != len(apps) || !release.Active {
		t.Fatalf("publish = %+v, %v", release, err)
	}
	finished, err := s.ProjectEnvironmentCloneOperationByID(ctx, a.ID, p.ID, op.ID)
	if err != nil || finished.Status != state.CloneOperationReady || finished.TargetReleaseSetID != release.ID || finished.Revision != op.Revision+1 {
		t.Fatalf("non-atomic completion = %+v, %v", finished, err)
	}
	if replay, err := s.PublishProjectEnvironmentCloneReleaseSet(ctx, a.ID, p.ID, op.ID, op.Revision, 1800); err != nil || replay.ID != release.ID {
		t.Fatalf("publication replay = %+v, %v", replay, err)
	}
	production, err := state.ResolveProductionDeployment(ctx, s, apps[0].ID)
	if err != nil || production.ID != newer.ID {
		t.Fatalf("production changed = %+v, %v", production, err)
	}
	// Other accounts cannot read even non-secret clone metadata.
	other, err := s.CreateAccount(ctx, "other-clone@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneWorkloads(ctx, other.ID, p.ID, op.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross account capture read = %v", err)
	}
}

// ADR-590: a complete receipt and frozen values are checked at both publication gates.
func TestMemProjectEnvironmentCloneValuePublication(t *testing.T) {
	for _, fault := range cloneValuePublicationFaults {
		t.Run(fault, func(t *testing.T) { projectEnvironmentClonePublicationContract(t, state.NewMemStore(), false, fault) })
	}
}

var cloneValuePublicationFaults = []string{"before_variable", "before_edge_policy", "omit_variables", "omit_secrets", "wrong_value_hash", "after_variable", "after_extra_variable", "after_secret_ciphertext", "after_secret_deleted", "after_edge_policy"}

func cloneWorkloadEdgePolicy(accountID, projectID, appID, environment, value string) state.ProjectEnvironmentEdgePolicy {
	return state.ProjectEnvironmentEdgePolicy{AccountID: accountID, ProjectID: projectID, AppID: appID, EnvironmentSlug: environment, Rules: []state.ProjectEnvironmentEdgeRule{{
		Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Priority: 100, Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Stage-Policy", Value: value, Action: "set"}}}},
	}}}
}

func assertCloneValuePublicationRejected(t *testing.T, s cloneWorkloadTestStore, accountID, projectID string, op state.ProjectEnvironmentCloneOperation, err error) {
	t.Helper()
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed or omitted values published: %v", err)
	}
	if strings.Contains(err.Error(), "changed-private-target") {
		t.Fatal("publication error exposed private values")
	}
	current, readErr := s.ProjectEnvironmentCloneOperationByID(context.Background(), accountID, projectID, op.ID)
	if readErr != nil || current.Status != op.Status || current.Revision != op.Revision || current.TargetReleaseSetID != "" {
		t.Fatalf("rejected publication changed durable state: %+v, %v", current, readErr)
	}
	if _, err := s.ActiveProjectReleaseSet(context.Background(), accountID, projectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected publication acquired a serving graph: %v", err)
	}
}
