package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type serviceHandoffNotifier struct{}

func (serviceHandoffNotifier) Notify(context.Context, string, string) error { return nil }

type serviceHandoffBootVM struct {
	sched.RoutedVMM
	boots int
}

func (v *serviceHandoffBootVM) CreateColdBoot(context.Context, string, string, sched.AppSpec) (*sched.WakeOutcome, error) {
	v.boots++
	return nil, errors.New("test VM boundary: boot attempted")
}
func (v *serviceHandoffBootVM) Destroy(context.Context, string, string) error { return nil }

func TestServiceHandoffReadinessAndPinnedSettings(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "service-handoff-"+backend+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			service := state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 1}}
			createApp := func(t *testing.T, slug string, project string, manifest state.AppManifest) state.App {
				t.Helper()
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, Manifest: manifest})
				if err != nil {
					t.Fatal(err)
				}
				return app
			}
			create := func(t *testing.T, in state.Deployment) state.Deployment {
				t.Helper()
				d, err := store.CreateDeployment(ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			live := func(t *testing.T, d state.Deployment) state.Deployment {
				t.Helper()
				if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				d, err := store.DeploymentByID(ctx, d.ID)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			engine := func(t *testing.T) (*sched.Engine, *serviceHandoffBootVM) {
				t.Helper()
				vm := &serviceHandoffBootVM{}
				e, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), vm, serviceHandoffNotifier{}, "1.10.0", logger)
				if err != nil {
					t.Fatal(err)
				}
				// This fixture drives each reconciliation explicitly, including restarts.
				// Transition notifications must not race a later engine's boot assertion.
				*e.ServiceReconcileSubmit() = func(context.Context, string) {}
				return e, vm
			}

			for _, probeCase := range []string{"ready", "unready", "missing", "previous-wake", "ingress-unready"} {
				ready := probeCase == "ready"
				t.Run("traffic readiness "+probeCase, func(t *testing.T) {
					app := createApp(t, "service-readiness-"+probeCase, "", service)
					old := live(t, create(t, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("a", 64), CreatedAt: time.Now().Add(-time.Hour)}))
					started := time.Now().UTC()
					var sidecars json.RawMessage
					if probeCase == "ingress-unready" {
						sidecars = json.RawMessage(`[{"name":"proxy","type":"sidecar","image":"example/proxy:latest","port":8080,"primary_ingress":true,"readiness_probe":{"http_get":{"path":"/ready"}}}]`)
					}
					candidate := live(t, create(t, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("b", 64), RolloutState: "rolling_out", RolloutStartedAt: &started, Sidecars: sidecars, OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`)}))
					ins, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, string(state.StateRunning), 256, node.ID, uuid.NewString(), string(state.InstanceModeService))
					if err != nil {
						t.Fatal(err)
					}
					status := "ready"
					if probeCase == "unready" {
						status = "unready"
					}
					wakeID := ins.WakeID
					if probeCase == "previous-wake" {
						wakeID = uuid.NewString()
					}
					body, _ := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": ins.ID, "deployment_id": candidate.ID, "wake_id": wakeID, "node_id": node.ID, "status": status})
					if probeCase != "missing" {
						if err := store.(interface {
							AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
						}).AppendEventAt(ctx, "vmmd", "wake.app_readiness", nil, body, time.Now()); err != nil {
							t.Fatal(err)
						}
					}
					if !ready {
						for _, cutover := range []func(context.Context, string) (state.Deployment, error){store.BeginServiceRolloutCutover, store.FinalizeServiceRollout} {
							if _, err := cutover(ctx, candidate.ID); !errors.Is(err, state.ErrServiceRolloutNotReady) {
								t.Fatalf("unready traffic writer = %v", err)
							}
						}
					}
					if !ready {
						if _, err := store.CreateInstanceWithMode(ctx, app.ID, old.ID, "running", 256, node.ID, uuid.NewString(), "service"); err != nil {
							t.Fatal(err)
						}
					}
					e, vm := engine(t)
					e.ReconcileServiceDeployment(ctx, candidate.ID)
					got, _ := store.DeploymentByID(ctx, candidate.ID)
					previous, _ := store.DeploymentByID(ctx, old.ID)
					t.Logf("probe=%s candidate=%s/%s/%d predecessor=%s/%d", probeCase, got.Status, got.RolloutState, got.TrafficPercent, previous.Status, previous.TrafficPercent)
					if !ready && vm.boots != 0 {
						t.Fatalf("unready running replica caused %d duplicate boot attempts", vm.boots)
					}
					if !ready && (got.TrafficPercent != 0 || previous.TrafficPercent != 100 || got.RolloutState == "complete") {
						t.Error("service cut over to a readiness-withdrawn generation")
					}
					if ready && (got.TrafficPercent != 100 || got.RolloutState != "complete") {
						t.Error("ready control did not complete")
					}
				})
			}

			for _, delay := range []time.Duration{0, 11 * time.Minute} {
				t.Run(fmt.Sprintf("time before readiness %s", delay), func(t *testing.T) {
					app := createApp(t, fmt.Sprintf("service-build-delay-%d", int(delay.Minutes())), "", service)
					admission := time.Now().UTC().Add(-delay)
					_ = live(t, create(t, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("a", 64), CreatedAt: admission.Add(-time.Minute)}))
					candidate := create(t, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("b", 64), CreatedAt: admission, RolloutState: "rolling_out", RolloutStartedAt: &admission})
					for _, s := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
						if err := store.UpdateDeploymentStatus(ctx, candidate.ID, s, ""); err != nil {
							t.Fatal(err)
						}
					}
					beforeLive := time.Now().UTC()
					candidate = live(t, candidate)
					if candidate.RolloutStartedAt == nil || candidate.RolloutStartedAt.Before(beforeLive) {
						t.Fatalf("readiness start predates activation: %v", candidate.RolloutStartedAt)
					}
					again := live(t, candidate)
					if !again.RolloutStartedAt.Equal(*candidate.RolloutStartedAt) {
						t.Fatal("duplicate live notification extended readiness budget")
					}

					e, vm := engine(t)
					e.ReconcileServiceDeployment(ctx, candidate.ID)
					got, _ := store.DeploymentByID(ctx, candidate.ID)
					t.Logf("build/approval elapsed=%s first readiness pass: boots=%d status=%s rollout=%s reason=%q", delay, vm.boots, got.Status, got.RolloutState, got.RolloutAbortedReason)
					if vm.boots == 0 || got.RolloutState == "aborted" {
						t.Error("build/approval time consumed the service replica readiness budget")
					}
				})
			}

			for _, scoped := range []bool{false, true} {
				t.Run(fmt.Sprintf("scoped service mode %v", scoped), func(t *testing.T) {
					project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: fmt.Sprintf("svc-%v", scoped)})
					if err != nil {
						t.Fatal(err)
					}
					manifest := service
					if scoped {
						manifest = state.AppManifest{ExecutionMode: api.ExecutionModeRequest}
					}
					app := createApp(t, fmt.Sprintf("service-scoped-%v", scoped), project.ID, manifest)
					if scoped {
						settings, err := state.WorkloadSettingsFromApp(app)
						if err != nil {
							t.Fatal(err)
						}
						settings.Manifest = service
						if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "production", app.ID, 0, settings); err != nil {
							t.Fatal(err)
						}
					}
					started := time.Now().UTC()
					candidate := live(t, create(t, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/app@sha256:" + strings.Repeat("b", 64), Scope: "production", RolloutState: "rolling_out", RolloutStartedAt: &started}))
					effective, err := state.AppForDeployment(ctx, store, candidate)
					if err != nil {
						t.Fatal(err)
					}
					if effective.Manifest.ExecutionMode != api.ExecutionModeService {
						t.Fatalf("pinned service fixture=%s", effective.Manifest.ExecutionMode)
					}
					due, err := store.ListServiceRecoveryApps(ctx, "", time.Now().UTC(), 100)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, id := range due {
						found = found || id == app.ID
					}
					if !found {
						t.Fatal("pinned service missing from periodic recovery")
					}
					e, vm := engine(t)
					e.ReconcileServiceDeployment(ctx, candidate.ID)
					got, _ := store.DeploymentByID(ctx, candidate.ID)
					t.Logf("global mode=%s deployment mode=%s boots=%d candidate=%s/%s/%d", app.Manifest.ExecutionMode, effective.Manifest.ExecutionMode, vm.boots, got.Status, got.RolloutState, got.TrafficPercent)
					if vm.boots == 0 {
						t.Error("deployment-pinned service rollout never admits a service replica")
					}
					if scoped {
						// A restart and an unrelated shared-app edit cannot bypass the
						// deployed generation's durable startup cooldown.
						largerRAM := 512
						if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &largerRAM}); err != nil {
							t.Fatal(err)
						}
						restarted, retryVM := engine(t)
						restarted.ReconcileServiceDeployment(ctx, candidate.ID)
						if retryVM.boots != 0 {
							t.Fatal("shared app edit reset pinned deployment cooldown")
						}
						// Exercise steady-state allocation after a successful rollout loses
						// its last resident, while the global app still uses request mode.
						resident, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 256, node.ID, uuid.NewString(), "service")
						if err != nil {
							t.Fatal(err)
						}
						if _, err := store.FinalizeServiceRollout(ctx, candidate.ID); err != nil {
							t.Fatal(err)
						}
						if err := store.UpdateInstanceState(ctx, resident.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
						steady, steadyVM := engine(t)
						steady.ReconcileServiceDeployment(ctx, candidate.ID)
						if steadyVM.boots == 0 {
							t.Fatal("pinned service steady-state recovery did not admit a replacement")
						}
					}

				})
			}
		})
	}
}

func TestServiceHandoffUsesPinnedReplicaTarget(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "service-target-"+backend+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			for _, globalDesired := range []int{2, 1} {
				t.Run(fmt.Sprint(globalDesired), func(t *testing.T) {
					project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: fmt.Sprintf("replica-%d", globalDesired)})
					if err != nil {
						t.Fatal(err)
					}
					app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: fmt.Sprintf("pinned-replicas-%d", globalDesired), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5,
						Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: globalDesired}}})
					if err != nil {
						t.Fatal(err)
					}
					settings, err := state.WorkloadSettingsFromApp(app)
					if err != nil {
						t.Fatal(err)
					}
					settings.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}
					if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "production", app.ID, 0, settings); err != nil {
						t.Fatal(err)
					}
					old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous", Scope: "production", CreatedAt: time.Now().Add(-time.Minute)})
					if err != nil {
						t.Fatal(err)
					}
					if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
						t.Fatal(err)
					}
					candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:next", Scope: "production", RolloutState: "rolling_out"})
					if err != nil {
						t.Fatal(err)
					}
					if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := store.CreateInstanceWithMode(ctx, app.ID, candidate.ID, "running", 256, node.ID, uuid.NewString(), "service"); err != nil {
						t.Fatal(err)
					}
					effective, err := state.AppForDeployment(ctx, store, candidate)
					if err != nil {
						t.Fatal(err)
					}
					vm := &serviceHandoffBootVM{}
					logger := slog.New(slog.NewTextHandler(io.Discard, nil))
					e, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), vm, serviceHandoffNotifier{}, "1.10.0", logger)
					if err != nil {
						t.Fatal(err)
					}
					*e.ServiceReconcileSubmit() = func(context.Context, string) {}
					e.ReconcileServiceDeployment(ctx, candidate.ID)
					got, _ := store.DeploymentByID(ctx, candidate.ID)
					prev, _ := store.DeploymentByID(ctx, old.ID)
					t.Logf("global desired=%d pinned desired=%d running candidate replicas=1 replacement attempts=%d candidate rollout=%s/%d previous=%s/%d", globalDesired, effective.Manifest.ServiceReplicas.Desired, vm.boots, got.RolloutState, got.TrafficPercent, prev.Status, prev.TrafficPercent)
					if got.TrafficPercent != 0 || got.RolloutState == "complete" {
						t.Error("service rollout promoted below its pinned replica count")
					}
				})
			}
		})
	}
}
