//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func buildWaitFixture(t *testing.T, backend string, split bool, stores ...state.Store) (state.Store, state.App, state.Deployment, state.Deployment, state.Build) {
	t.Helper()
	var s state.Store = state.NewMemStore()
	if len(stores) > 0 {
		s = stores[0]
	} else if backend == "postgres" {
		s, _, _ = pgStoreWithPool(t)
	}
	a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "build-wait"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) state.App {
		t.Helper()
		app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: project.ID, Slug: "wait-" + name, WorkloadName: name, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	target, web := create("api"), create("web")
	if split {
		prior, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(t.Context(), prior.ID); err != nil {
			t.Fatal(err)
		}
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Kind: state.DeploymentKindTarball, TrafficPercentExplicit: split})
	if err != nil {
		t.Fatal(err)
	}
	pins := []state.DeploymentDependencyPin{{Service: "api", AppID: target.ID, DeploymentID: dep.ID}}
	candidate, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: web.ID, Kind: state.DeploymentKindTarball, BuildDependencies: pins})
	if err != nil {
		t.Fatal(err)
	}
	pins[0].DeploymentID = uuid.NewString()
	build, err := s.CreateBuild(t.Context(), candidate.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, web, dep, candidate, build
}

func TestMigrationBuildWaitQueueAndCancellation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, web, target, candidate, build := buildWaitFixture(t, backend, false)
			claim, err := s.ClaimQueuedBuild(t.Context(), build.ID)
			if err != nil {
				t.Fatal(err)
			}
			gate, err := s.(state.BuildDependencyGateStore).CheckBuildDependencies(t.Context(), claim, time.Now())
			if err != nil || gate.Status != "waiting" || gate.Dependencies[0].DeploymentID != target.ID {
				t.Fatalf("wait: %+v %v", gate, err)
			}
			queued, _ := s.BuildByID(t.Context(), build.ID)
			if queued.Status != state.BuildQueued || !queued.StartedAt.IsZero() || !queued.EnqueuedAt.Equal(build.EnqueuedAt) {
				t.Fatalf("pending claim lost queue identity: %+v", queued)
			}
			claims := []func() (state.Build, error){
				func() (state.Build, error) { return s.ClaimQueuedBuild(t.Context(), build.ID) },
				func() (state.Build, error) {
					return s.(state.BuildAffinityClaimStore).ClaimQueuedBuildWithNodeAffinity(t.Context(), build.ID, "node", time.Minute)
				},
				func() (state.Build, error) { return s.ClaimNextQueuedBuild(t.Context()) },
				func() (state.Build, error) { return s.ClaimNextQueuedBuildWithFairness(t.Context(), time.Minute) },
				func() (state.Build, error) {
					return s.(state.BuildAffinityClaimStore).ClaimNextQueuedBuildWithNodeAffinity(t.Context(), "node", time.Minute, time.Minute)
				},
			}
			for i, claimNext := range claims {
				if _, err := claimNext(); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("claim path %d bypassed wait: %v", i, err)
				}
			}
			other, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: web.ID, Scope: "staging", Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			sibling, err := s.CreateBuild(t.Context(), other.ID, state.DeploymentKindTarball, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			next, err := s.ClaimNextQueuedBuild(t.Context())
			if err != nil || next.ID != sibling.ID {
				t.Fatal("waiting frontend starved another build", err)
			}
			if _, _, err := s.CancelDeploymentTx(t.Context(), candidate.ID, "test-user", state.CancelReasonUser); err != nil {
				t.Fatal(err)
			}
			if _, err := s.(state.BuildDependencyGateStore).CheckBuildDependencies(t.Context(), claim, time.Now()); err == nil {
				t.Fatal("stale worker revived cancellation")
			}
			cancelled, _ := s.BuildByID(t.Context(), build.ID)
			if cancelled.Status != state.BuildCancelled {
				t.Fatalf("cancellation lost: %+v", cancelled)
			}
		})
	}
}

func TestMigrationBuildWaitReadinessRetryAndTimeout(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, scenario := range []string{"ready", "timeout", "changed", "failed", "split"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				s, _, target, candidate, build := buildWaitFixture(t, backend, scenario == "split")
				claim, err := s.ClaimQueuedBuild(t.Context(), build.ID)
				if err != nil {
					t.Fatal(err)
				}
				gateStore := s.(state.BuildDependencyGateStore)
				gate, err := gateStore.CheckBuildDependencies(t.Context(), claim, time.Now().Add(-api.ProjectMigrationBuildWaitPoll-time.Second))
				if err != nil || gate.Status != "waiting" {
					t.Fatalf("initial gate: %+v %v", gate, err)
				}
				beforeDeadline := gate.DeadlineAt
				code := ""
				switch scenario {
				case "ready":
					if err := s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
						t.Fatal(err)
					}
				case "timeout":
					code = "build_dependency_timeout"
				case "changed":
					if _, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: target.AppID, Kind: state.DeploymentKindTarball}); err != nil {
						t.Fatal(err)
					}
					code = "build_dependency_changed"
				case "failed":
					if _, err := s.SetDeploymentFailed(t.Context(), target.ID, "test_failure", "test"); err != nil {
						t.Fatal(err)
					}
					code = "build_dependency_failed"
				case "split":
					if err := s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := s.UpdateDeploymentTraffic(t.Context(), target.ID, 50); err != nil {
						t.Fatal(err)
					}
				}
				claim, err = s.ClaimQueuedBuild(t.Context(), build.ID)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				if scenario == "timeout" {
					now = beforeDeadline.Add(time.Second)
				}
				gate, err = gateStore.CheckBuildDependencies(t.Context(), claim, now)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "ready" && gate.Status != "ready" || scenario == "split" && gate.Status != "waiting" || code != "" && (gate.Status != "failed" || gate.Code != code) {
					t.Fatalf("gate: %+v", gate)
				}
				if !gate.DeadlineAt.Equal(beforeDeadline) {
					t.Fatal("poll reset deadline")
				}
				if scenario == "split" {
					return
				}
				if _, err := s.SetDeploymentFailed(t.Context(), candidate.ID, "fixture_failure", "test"); err != nil {
					t.Fatal(err)
				}
				retry, err := s.RetryDeploymentFromStage(t.Context(), candidate.ID, state.StageSourceDownload)
				if err != nil {
					t.Fatal(err)
				}
				var stages state.StageState
				if err := json.Unmarshal(retry.StageState, &stages); err != nil {
					t.Fatal(err)
				}
				// Pg's return value predates the atomic projection; reload the durable row.
				retry, err = s.DeploymentByID(t.Context(), retry.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(retry.StageState, &stages); err != nil {
					t.Fatal(err)
				}
				if stages.BuildDependencyGate == nil || stages.BuildDependencyGate.Dependencies[0].DeploymentID != target.ID || stages.BuildDependencyGate.Status != "waiting" {
					t.Fatalf("retry changed pins: %+v", stages.BuildDependencyGate)
				}
			})
		}
	}
}

func TestMigrationBuildWaitRejectsMissingForeignAndOtherScopeTargets(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, scenario := range []string{"missing", "foreign", "other-scope"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				s, web, target, _, _ := buildWaitFixture(t, backend, false)
				if err := s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
					t.Fatal(err)
				}
				pin := state.DeploymentDependencyPin{Service: "api", AppID: target.AppID, DeploymentID: uuid.NewString()}
				if scenario == "foreign" {
					other, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					app, err := s.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign-api", WorkloadName: "api", Type: state.AppTypeApp})
					if err != nil {
						t.Fatal(err)
					}
					dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
					if err != nil {
						t.Fatal(err)
					}
					pin.AppID, pin.DeploymentID = app.ID, dep.ID
				} else if scenario == "other-scope" {
					dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: target.AppID, Kind: state.DeploymentKindTarball, Scope: "staging"})
					if err != nil {
						t.Fatal(err)
					}
					pin.DeploymentID = dep.ID
				}
				dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: web.ID, Kind: state.DeploymentKindTarball, BuildDependencies: []state.DeploymentDependencyPin{pin}})
				if err != nil {
					t.Fatal(err)
				}
				build, err := s.CreateBuild(t.Context(), dep.ID, state.DeploymentKindTarball, 0, "")
				if err != nil {
					t.Fatal(err)
				}
				claim, err := s.ClaimQueuedBuild(t.Context(), build.ID)
				if err != nil {
					t.Fatal(err)
				}
				gate, err := s.(state.BuildDependencyGateStore).CheckBuildDependencies(t.Context(), claim, time.Now())
				if err != nil || gate.Status != "failed" || gate.Code != "build_dependency_failed" {
					t.Fatalf("unreviewed target admitted or serving predecessor adopted: %+v %v", gate, err)
				}
			})
		}
	}
}
