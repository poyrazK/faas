package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceTrafficWriterRechecksWithdrawnReadiness(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := store.CreateAccount(ctx, "readiness-writer@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "readiness-writer", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5,
				Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			create := func(in state.Deployment) state.Deployment {
				t.Helper()
				in.AppID = app.ID
				in.Kind = state.DeploymentKindImage
				in.ImageDigest = "sha256:readiness-writer"
				d, err := store.CreateDeployment(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				return d
			}
			previous := create(state.Deployment{})
			candidate := create(state.Deployment{RolloutState: "rolling_out", OverrideReadinessProbe: json.RawMessage(`{"path":"/ready"}`),
				Sidecars: json.RawMessage(`[{"name":"proxy","type":"sidecar","image":"example/proxy:latest","port":8080,"primary_ingress":true,"readiness_probe":{"http_get":{"path":"/ready"}}}]`)})
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 128, node.ID, uuid.NewString(), "service")
			if err != nil {
				t.Fatal(err)
			}
			observe := func(kind, status, wake string) {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": instance.ID, "node_id": node.ID, "wake_id": wake, "sidecar_name": "proxy", "status": status})
				err := store.(interface {
					AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
				}).AppendEventAt(ctx, "vmmd", kind, nil, body, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
			}
			observe("wake.app_readiness", "ready", instance.WakeID)
			if _, err := store.BeginServiceRolloutCutover(ctx, candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("missing ingress evidence: %v", err)
			}
			observe("wake.sidecar_health", "ready", instance.WakeID)
			if _, err := store.BeginServiceRolloutCutover(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
			observe("wake.app_readiness", "unready", instance.WakeID)
			observe("wake.app_readiness", "ready", uuid.NewString())
			if _, err := store.FinalizeServiceRollout(ctx, candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
				t.Fatalf("withdrawn readiness: %v", err)
			}
			old, err := store.DeploymentByID(ctx, previous.ID)
			if err != nil {
				t.Fatal(err)
			}
			if old.Status != state.DeployLive {
				t.Fatalf("predecessor retired after withdrawal: %s", old.Status)
			}
			observe("wake.app_readiness", "ready", instance.WakeID)
			if _, err := store.FinalizeServiceRollout(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceReadinessClockCoversStatusWriters(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, err := store.CreateAccount(ctx, "readiness-clock@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "readiness-clock", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5,
				Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			admitted := time.Now().UTC().Add(-time.Hour)
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:readiness-clock", RolloutState: "rolling_out", RolloutStartedAt: &admitted})
			if err != nil {
				t.Fatal(err)
			}
			activated := time.Now().UTC()
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			first, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if first.RolloutStartedAt == nil || first.RolloutStartedAt.Before(activated) {
				t.Fatalf("readiness clock includes queue time: %v", first.RolloutStartedAt)
			}
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			second, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if second.RolloutStartedAt == nil || !second.RolloutStartedAt.Equal(*first.RolloutStartedAt) {
				t.Fatal("duplicate status write extended readiness budget")
			}
		})
	}
}

func TestPinnedServiceRuntimeAdmissionCapture(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := t.Context()
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			owner, err := store.(interface {
				CreateAccountWithPersonalOrg(context.Context, state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
			}).CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "pinned-runtime@example.test", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "pinned-runtime"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID, Slug: "pinned-runtime", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := state.WorkloadSettingsFromApp(app)
			if err != nil {
				t.Fatal(err)
			}
			settings.RAMMB = 128
			settings.Manifest = state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}}
			if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, owner.Account.ID, project.ID, "production", app.ID, 0, settings); err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:pinned-runtime", Scope: "production"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			dep, err = store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			for _, globalRAM := range []int{256, 512} {
				if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &globalRAM}); err != nil {
					t.Fatal(err)
				}
				effective, err := state.AppForDeployment(ctx, store, dep)
				if err != nil {
					t.Fatal(err)
				}
				instance, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "cold_booting", 128, node.ID, uuid.NewString(), "service")
				if err != nil {
					t.Fatal(err)
				}
				reader := store.(state.InstanceApplicationStandardAdmissionStore)
				if err := state.CheckInstanceApplicationStandardAdmission(ctx, reader, instance.ID, effective, owner.Account, dep); err != nil {
					t.Fatalf("global RAM %d, pinned RAM 128: %v", globalRAM, err)
				}
				wrong := effective
				wrong.RAMMB++
				if err := state.CheckInstanceApplicationStandardAdmission(ctx, reader, instance.ID, wrong, owner.Account, dep); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
					t.Fatalf("wrong runtime shape accepted: %v", err)
				}
			}
		})
	}
}
