package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPromotionReleaseConfigurationMemory(t *testing.T) {
	testPromotionReleaseConfiguration(t, func(*testing.T) Store { return NewMemStore() })
}

type promotionSnapshotFixture struct {
	store                    Store
	account                  Account
	project                  Project
	apps                     []App
	sources, previous        []Deployment
	sourceGraph, targetGraph ProjectReleaseSet
	promotion                ProjectEnvironmentPromotion
	view                     *ProjectEnvironmentPromotionReleaseConfiguration
	workloads                []ProjectEnvironmentPromotionWorkload
	inputs                   []ProjectEnvironmentPromotionWorkloadSpecInput
	candidates               []ProjectReleaseMember
	identity                 *age.X25519Identity
}

func newPromotionSnapshotFixture(t *testing.T, store Store, empty bool, serviceGraph ...bool) *promotionSnapshotFixture {
	return newPromotionSnapshotFixtureNamed(t, store, empty, "snapshot-promotion", serviceGraph...)
}
func newPromotionSnapshotFixtureNamed(t *testing.T, store Store, empty bool, prefix string, serviceGraph ...bool) *promotionSnapshotFixture {
	t.Helper()
	ctx := t.Context()
	f := &promotionSnapshotFixture{store: store}
	var err error
	plan := api.PlanPro
	if len(serviceGraph) > 4 && serviceGraph[4] {
		plan = api.PlanScale
	}
	f.account, err = store.CreateAccount(ctx, prefix+"@example.test", plan)
	requirePromotionSnapshot(t, err)
	f.project, err = store.CreateProject(ctx, Project{AccountID: f.account.ID, Slug: prefix})
	requirePromotionSnapshot(t, err)
	_, err = store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "staging"})
	requirePromotionSnapshot(t, err)
	identity, err := age.GenerateX25519Identity()
	requirePromotionSnapshot(t, err)
	f.identity = identity
	names := []string{"api", "web"}
	if len(serviceGraph) > 4 && serviceGraph[4] {
		names = append(names, "mail", "billing")
	}
	if len(serviceGraph) > 3 && serviceGraph[3] {
		names = append(names, "frontend")
	}
	for _, name := range names {
		manifest := AppManifest{RevisionPinTTLSeconds: 3600}
		class := WorkloadClassHTTP
		if len(serviceGraph) > 2 && serviceGraph[2] && (name == "web" || name == "mail" || name == "billing") {
			class = WorkloadClassWorker
			manifest.ExecutionMode = api.ExecutionModeWorker
			manifest.StopGracePeriodS = 30
		}
		if len(serviceGraph) > 0 && serviceGraph[0] {
			if name == "web" || name == "frontend" {
				manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"api"})
				manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
				manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
				manifest.Env = api.ServiceBindingEnvForTransport(nil, manifest.ServiceBindings, manifest.ServiceBindingTransport)
			} else {
				allowed := []string{"web"}
				if len(serviceGraph) > 3 && serviceGraph[3] {
					allowed = append(allowed, "frontend")
				}
				manifest.AllowedServiceCallers = &allowed
			}
		}
		app, err := store.CreateApp(ctx, App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: strings.TrimSuffix(prefix, "-promotion") + "-" + name, WorkloadName: name, WorkloadClass: class, Type: AppTypeApp,
			PublicAuthMode: "basic", PublicAuthBasicSealed: []byte("destination-auth"), Manifest: manifest})
		requirePromotionSnapshot(t, err)
		app, err = store.UpdateApp(ctx, app.ID, UpdateAppParams{SetPublicAuth: true, PublicAuth: &AppPublicAuthUpdate{Mode: "basic", Sealed: []byte("destination-auth")}})
		requirePromotionSnapshot(t, err)
		if class == WorkloadClassWorker {
			_, err = store.(QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, QueueBinding{AccountID: f.account.ID, AppID: app.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: WorkloadClassWorker, MaxConcurrency: 2})
			requirePromotionSnapshot(t, err)
		}
		requirePromotionSnapshot(t, store.UpsertAppEnvInScope(ctx, f.account.ID, app.ID, "production", "MODE", "destination-value"))
		requirePromotionSnapshot(t, store.UpsertAppEnvInScope(ctx, f.account.ID, app.ID, "production", "OLD_ONLY", "previous-only"))
		requirePromotionSnapshot(t, store.UpsertAppEnvInScope(ctx, f.account.ID, app.ID, "staging", "MODE", "tested-value"))
		for _, scope := range []string{"staging", "production"} {
			cipher, err := secretbox.SealOne(identity.Recipient(), "TOKEN", scope+"-private", 4096)
			requirePromotionSnapshot(t, err)
			requirePromotionSnapshot(t, store.UpsertAppSecretWithClassInScope(ctx, f.account.ID, app.ID, scope, "TOKEN", identity.Recipient().String(), "0123456789abcdef", api.SecretClassPersistent, cipher))
		}
		target, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, SourceSHA256: strings.Repeat("a", 64)})
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, store.SetDeploymentRootfs(ctx, target.ID, "/artifacts/previous-"+name, "/artifacts/previous-"+name, 4096))
		requirePromotionSnapshot(t, store.MarkDeploymentLive(ctx, target.ID))
		settings, err := WorkloadSettingsFromApp(app)
		requirePromotionSnapshot(t, err)
		settings.RAMMB, settings.StartCommand, settings.PublicAuthBasicSealed = 512, "serve tested", []byte("source-auth")
		if len(serviceGraph) > 0 && serviceGraph[0] && (name == "web" || name == "frontend") {
			settings.Manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTP
			settings.Manifest.Env = api.ServiceBindingEnvForTransport(nil, settings.Manifest.ServiceBindings, settings.Manifest.ServiceBindingTransport)
			for key, value := range settings.Manifest.Env {
				requirePromotionSnapshot(t, store.UpsertAppEnvInScope(ctx, f.account.ID, app.ID, "staging", key, value))
			}
		}
		stageSpec, err := store.(ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "staging", app.ID, 0, settings)
		requirePromotionSnapshot(t, err)
		sourceInput := Deployment{AppID: app.ID, Scope: "staging", Kind: DeploymentKindTarball, SourceSHA256: strings.Repeat("b", 64)}
		if len(serviceGraph) > 0 && serviceGraph[0] && (name == "web" || name == "frontend") {
			sourceInput.OverrideEnv = json.RawMessage(`{"GREGALE_SERVICE_API_URL":"http://api.svc.gregale:10081"}`)
		}
		if len(serviceGraph) > 1 && serviceGraph[1] && name == "api" {
			sourceInput.ReleaseCommand = []string{"database expand --compatible"}
			sourceInput.ReleaseCommandShell = true
			sourceInput.ImageDigest = "sha256:" + strings.Repeat("d", 64)
		}
		source, err := store.CreateDeployment(ctx, sourceInput)
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, store.SetDeploymentRootfs(ctx, source.ID, "/artifacts/source-"+name, "/artifacts/source-"+name, 8192))
		requirePromotionSnapshot(t, store.MarkDeploymentLive(ctx, source.ID))
		vars, err := store.ListAppEnvInScope(ctx, f.account.ID, app.ID, "staging")
		requirePromotionSnapshot(t, err)
		if empty && name == "api" {
			vars = []AppEnv{}
		}
		pinPromotionSnapshot(t, store, source.ID, vars)
		source, err = store.DeploymentByID(ctx, source.ID)
		requirePromotionSnapshot(t, err)
		target, err = store.DeploymentByID(ctx, target.ID)
		requirePromotionSnapshot(t, err)
		targetSettings := appSettingsForPromotionTest(t, app)
		if class == WorkloadClassWorker {
			targetSettings, err = MaterializeEnvironmentWorkloadSettings(ctx, store, app, "production")
			requirePromotionSnapshot(t, err)
		}
		targetHash, err := WorkloadSettingsHash(targetSettings)
		requirePromotionSnapshot(t, err)
		f.apps = append(f.apps, app)
		f.sources = append(f.sources, source)
		f.previous = append(f.previous, target)
		f.inputs = append(f.inputs, ProjectEnvironmentPromotionWorkloadSpecInput{SourceDeploymentID: source.ID, SourceHash: stageSpec.Hash, PreviousTargetHash: targetHash})
		f.workloads = append(f.workloads, ProjectEnvironmentPromotionWorkload{WorkloadSlug: app.Slug, WorkloadName: name, SourceDeploymentID: source.ID, PreviousTargetDeploymentID: target.ID, Status: "pending"})
	}
	publisher := store.(ProjectReleaseSetStore)
	for _, scope := range []string{"staging", "production"} {
		members := []ProjectReleaseMember{}
		for i, app := range f.apps {
			d := f.sources[i]
			if scope == "production" {
				d = f.previous[i]
			}
			members = append(members, ProjectReleaseMember{app.ID, d.ID})
		}
		r, err := publisher.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, scope, 1800, members)
		requirePromotionSnapshot(t, err)
		if scope == "staging" {
			f.sourceGraph = r
		} else {
			f.targetGraph = r
		}
	}
	f.promotion = ProjectEnvironmentPromotion{AccountID: f.account.ID, ProjectID: f.project.ID, ProjectSlug: f.project.Slug, FromEnvironment: "staging", ToEnvironment: "production", SyncConfig: true, ReleaseGraphMode: true,
		SourceReleaseSetID: f.sourceGraph.ID, PreviousTargetReleaseSetID: f.targetGraph.ID, Status: "running", IdempotencyKey: "snapshot", PromotionHash: strings.Repeat("c", 64), ReleaseTTLSeconds: 1800,
		SourceConfigHash: api.EmptyProjectEnvironmentConfigHash(), PreviousTargetConfigHash: api.EmptyProjectEnvironmentConfigHash(), SourceConfigSnapshot: json.RawMessage(`{}`), PreviousTargetConfigSnapshot: json.RawMessage(`{}`)}
	f.promotion.ResolveServiceBindings = len(serviceGraph) > 0 && serviceGraph[0]
	f.promotion.Migrate = len(serviceGraph) > 1 && serviceGraph[1]
	f.promotion.Workers = len(serviceGraph) > 2 && serviceGraph[2]
	f.view, err = store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx, f.promotion)
	requirePromotionSnapshot(t, err)
	if f.view == nil || f.view.Hash == "" {
		t.Fatal("pinned source has no promotion proof")
	}
	f.promotion.ReleaseConfigurationHash = f.view.Hash
	return f
}

func appSettingsForPromotionTest(t *testing.T, app App) ProjectEnvironmentWorkloadSettings {
	t.Helper()
	settings, err := WorkloadSettingsFromApp(app)
	requirePromotionSnapshot(t, err)
	return settings
}

func pinPromotionSnapshot(t *testing.T, store Store, deploymentID string, values []AppEnv) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		if s.releaseEnvironmentPins == nil {
			s.releaseEnvironmentPins = map[string][]AppEnv{}
		}
		s.releaseEnvironmentPins[deploymentID] = append([]AppEnv{}, values...)
		s.mu.Unlock()
	case *PgStore:
		raw, err := json.Marshal(releaseVariables(values))
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, sqlc.New().PinDeploymentReleaseEnvironment(t.Context(), s.pool, sqlc.PinDeploymentReleaseEnvironmentParams{DeploymentID: deploymentID, ValuesJson: raw}))
	default:
		t.Fatal("unsupported fixture store")
	}
}

func requirePromotionSnapshot(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *promotionSnapshotFixture) create(t *testing.T) {
	t.Helper()
	var err error
	f.promotion, _, err = f.store.CreateProjectEnvironmentPromotion(t.Context(), f.promotion, f.workloads)
	requirePromotionSnapshot(t, err)
	view, err := f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).ProjectEnvironmentPromotionReleaseConfiguration(t.Context(), f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if view == nil || view.Hash != f.view.Hash {
		t.Fatal("saved proof changed")
	}
	public, _ := json.Marshal(view)
	if strings.Contains(string(public), "tested-value") || strings.Contains(string(public), "private") || strings.Contains(string(public), "source-auth") {
		t.Fatal("public promotion proof exposed values")
	}
}

func (f *promotionSnapshotFixture) prepare(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	for i, app := range f.apps {
		input := f.inputs[i]
		input.PromotionID = f.promotion.ID
		candidate := f.sources[i]
		candidate.ID, candidate.BuildID, candidate.Scope, candidate.Status = "", "", "production", DeployPending
		candidate.TrafficPercent, candidate.TrafficPercentExplicit, candidate.CreatedAt = 0, true, candidate.CreatedAt.UTC()
		candidate.Revision = 0
		created, err := f.store.(ProjectEnvironmentPromotionWorkloadSpecStore).CreateDeploymentForEnvironmentPromotion(ctx, candidate, input)
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, f.store.SetDeploymentRootfs(ctx, created.ID, f.sources[i].RootfsPath, f.sources[i].RootfsKey, f.sources[i].RootfsBytes))
		retried, err := f.store.(ProjectEnvironmentPromotionWorkloadSpecStore).CreateDeploymentForEnvironmentPromotion(ctx, candidate, input)
		requirePromotionSnapshot(t, err)
		if retried.ID != created.ID {
			t.Fatal("preparation retry changed candidate")
		}
		requirePromotionSnapshot(t, f.store.(ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, created.ID))
		f.candidates = append(f.candidates, ProjectReleaseMember{app.ID, created.ID})
		snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, created.ID)
		requirePromotionSnapshot(t, err)
		baseline, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, f.previous[i].ID)
		requirePromotionSnapshot(t, err)
		if f.view.Secrets == nil && !reflect.DeepEqual(snapshot.Secrets, baseline.Secrets) {
			t.Fatal("candidate transferred source secrets")
		}
		pinned, err := AppForDeployment(ctx, f.store, created)
		requirePromotionSnapshot(t, err)
		if !reflect.DeepEqual(pinned.PublicAuthBasicSealed, app.PublicAuthBasicSealed) {
			t.Fatal("candidate transferred source authentication credential")
		}
	}
}

func testPromotionReleaseConfiguration(t *testing.T, factory func(*testing.T) Store) {
	for _, scenario := range []string{"success", "empty", "pinned_secrets", "rekey", "preview_target_env", "preview_source_env", "preview_config_aba", "cutover_env", "cutover_source_env", "cutover_secret", "cutover_config_aba", "cutover_source_graph", "candidate_artifact", "wrong_members", "rollback_env", "rollback_secret", "rollback_config", "rollback_source_changed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f := newPromotionSnapshotFixture(t, factory(t), scenario == "empty")
			if scenario == "pinned_secrets" || scenario == "rekey" {
				pinPromotionSecretSnapshots(t, f)
				view, err := f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).PreviewProjectEnvironmentPromotionReleaseConfiguration(ctx, f.promotion)
				requirePromotionSnapshot(t, err)
				f.view, f.promotion.ReleaseConfigurationHash = view, view.Hash
			}
			if strings.HasPrefix(scenario, "preview_") {
				scope := "production"
				if scenario == "preview_source_env" {
					scope = "staging"
				}
				if scenario == "preview_config_aba" {
					_, err := f.store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account.ID, ProjectID: f.project.ID, EnvironmentSlug: scope, Values: json.RawMessage(`{}`), ConfigHash: api.EmptyProjectEnvironmentConfigHash()})
					requirePromotionSnapshot(t, err)
				} else {
					requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, scope, "MODE", "operator-change"))
				}
				if _, _, err := f.store.CreateProjectEnvironmentPromotion(ctx, f.promotion, f.workloads); !errors.Is(err, ErrConflict) {
					t.Fatalf("stale preview admitted: %v", err)
				}
				return
			}
			f.create(t)
			f.prepare(t)
			if scenario == "rekey" {
				rekeyPromotionSecrets(t, f)
			}
			publish := f.store.(ProjectEnvironmentPromotionReleaseSetStore)
			if strings.HasPrefix(scenario, "cutover_") || scenario == "candidate_artifact" || scenario == "wrong_members" {
				switch scenario {
				case "cutover_env":
					requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "production", "MODE", "operator-change"))
				case "cutover_source_env":
					requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "staging", "MODE", "source-edit"))
				case "cutover_secret":
					changePromotionSecret(t, f)
				case "cutover_config_aba":
					_, err := f.store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account.ID, ProjectID: f.project.ID, EnvironmentSlug: "production", Values: json.RawMessage(`{}`), ConfigHash: api.EmptyProjectEnvironmentConfigHash()})
					requirePromotionSnapshot(t, err)
				case "cutover_source_graph":
					_, err := f.store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "staging", 1800, f.sourceGraph.Members)
					requirePromotionSnapshot(t, err)
				case "candidate_artifact":
					requirePromotionSnapshot(t, f.store.SetDeploymentRootfs(ctx, f.candidates[0].DeploymentID, "/changed", "/changed", 1))
				case "wrong_members":
					f.candidates[0].DeploymentID = f.previous[0].ID
				}
				if _, err := publish.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates); !errors.Is(err, ErrConflict) {
					t.Fatalf("stale cutover admitted: %v", err)
				}
				active, err := f.store.(ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, f.account.ID, f.project.ID, "production")
				requirePromotionSnapshot(t, err)
				if active.ID != f.targetGraph.ID {
					t.Fatal("failed cutover changed graph")
				}
				return
			}
			active, err := publish.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			replayed, err := publish.PublishProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.candidates)
			requirePromotionSnapshot(t, err)
			if replayed.ID != active.ID {
				t.Fatal("publication retry changed graph")
			}
			for i, app := range f.apps {
				snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, f.candidates[i].DeploymentID)
				requirePromotionSnapshot(t, err)
				if scenario == "empty" && i == 0 {
					if len(snapshot.Values) != 0 {
						t.Fatal("explicit empty source inherited destination variables")
					}
				} else if len(snapshot.Values) != 1 || snapshot.Values[0].Key != "MODE" || snapshot.Values[0].Value != "tested-value" || snapshot.Values[0].Scope != "production" {
					t.Fatal("candidate did not retain tested variables")
				}
			}
			if scenario == "rollback_env" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "production", "MODE", "operator-change"))
			}
			if scenario == "rollback_secret" {
				changePromotionSecret(t, f)
			}
			if scenario == "rollback_config" {
				_, err := f.store.CreateProjectEnvironmentConfigVersion(ctx, ProjectEnvironmentConfig{AccountID: f.account.ID, ProjectID: f.project.ID, EnvironmentSlug: "production", Values: json.RawMessage(`{}`), ConfigHash: api.EmptyProjectEnvironmentConfigHash()})
				requirePromotionSnapshot(t, err)
			}
			if scenario == "rollback_source_changed" {
				requirePromotionSnapshot(t, f.store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "staging", "MODE", "later-source"))
			}
			restored, err := publish.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			if scenario == "rollback_env" || scenario == "rollback_config" || scenario == "rollback_secret" {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("rollback overwrote later edit: %v", err)
				}
				return
			}
			requirePromotionSnapshot(t, err)
			replayed, err = publish.RollbackProjectEnvironmentPromotionReleaseSet(ctx, f.account.ID, f.promotion.ID, 1800, f.targetGraph.Members)
			requirePromotionSnapshot(t, err)
			if restored.ID != replayed.ID {
				t.Fatal("rollback retry changed graph")
			}
			for i, app := range f.apps {
				snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, f.previous[i].ID)
				requirePromotionSnapshot(t, err)
				if len(snapshot.Values) != 2 || snapshot.Values[0].Value != "destination-value" {
					t.Fatal("rollback failed to restore previous variables")
				}
			}
		})
	}
}

func changePromotionSecret(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	cipher, err := secretbox.SealOne(f.identity.Recipient(), "TOKEN", "operator-private", 4096)
	requirePromotionSnapshot(t, err)
	requirePromotionSnapshot(t, f.store.UpsertAppSecretWithClassInScope(t.Context(), f.account.ID, f.apps[0].ID, "production", "TOKEN", f.identity.Recipient().String(), "fedcba9876543210", api.SecretClassPersistent, cipher))
}

func pinPromotionSecretSnapshots(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	for i, app := range f.apps {
		for _, d := range []Deployment{f.sources[i], f.previous[i]} {
			snapshot, err := f.store.(RuntimeAppValuesStore).RuntimeAppValuesForDeployment(t.Context(), f.account.ID, app.ID, d.ID)
			requirePromotionSnapshot(t, err)
			rows := []releaseSecretVersion{}
			for _, secret := range snapshot.Secrets {
				rows = append(rows, releaseSecretFromRow(app.ID, releaseSecretRow(secret)))
			}
			switch store := f.store.(type) {
			case *MemStore:
				store.mu.Lock()
				if store.releaseSecretPins == nil {
					store.releaseSecretPins = map[string][]releaseSecretVersion{}
				}
				store.releaseSecretPins[d.ID] = rows
				store.mu.Unlock()
			case *PgStore:
				raw, err := json.Marshal(rows)
				requirePromotionSnapshot(t, err)
				requirePromotionSnapshot(t, sqlc.New().PinDeploymentReleaseSecrets(t.Context(), store.pool, sqlc.PinDeploymentReleaseSecretsParams{DeploymentID: mustPgUUID(d.ID), ValuesJson: raw}))
			}
		}
	}
}

func rekeyPromotionSecrets(t *testing.T, f *promotionSnapshotFixture) {
	t.Helper()
	current, err := age.GenerateX25519Identity()
	requirePromotionSnapshot(t, err)
	maintenance := f.store.(SecretRekeyStore)
	docs, err := maintenance.ListSecretRekeyDocuments(t.Context(), current.Recipient().String(), "", 100)
	requirePromotionSnapshot(t, err)
	for _, doc := range docs {
		next, err := TransformSecretRekeyDocument(doc, func(envelope SecretRekeyEnvelope) (SecretRekeyEnvelope, error) {
			values, err := secretbox.OpenMulti([]*age.X25519Identity{f.identity}, envelope.Ciphertext)
			if err != nil {
				return envelope, err
			}
			envelope.Ciphertext, err = secretbox.Seal(current.Recipient(), values)
			envelope.Kid = current.Recipient().String()
			return envelope, err
		})
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, maintenance.CompareAndSwapSecretRekeyDocument(t.Context(), doc, next))
	}
	view, err := f.store.(ProjectEnvironmentPromotionReleaseConfigurationStore).ProjectEnvironmentPromotionReleaseConfiguration(t.Context(), f.account.ID, f.promotion.ID)
	requirePromotionSnapshot(t, err)
	if view.Hash != f.view.Hash {
		t.Fatal("envelope maintenance invalidated approved promotion")
	}
}
