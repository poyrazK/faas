//go:build !no_pg

// adr: 117 — retries retain admission intent and rebuild derived source artifacts.
package state_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRetryRetainsReleaseIntent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, _ = pgStore(t)
			}
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "retry-audit-"+backend+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			createApp := func(slug string, service bool) state.App {
				t.Helper()
				manifest := state.AppManifest{}
				if service {
					manifest.ExecutionMode = api.ExecutionModeService
					manifest.ServiceReplicas = &state.ServiceReplicas{Desired: 2}
				}
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, Manifest: manifest})
				if err != nil {
					t.Fatal(err)
				}
				return app
			}
			create := func(input state.Deployment) state.Deployment {
				t.Helper()
				dep, err := store.CreateDeployment(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			fail := func(dep state.Deployment) state.Deployment {
				t.Helper()
				dep, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeDeployFailed, "temporary failure")
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			retry := func(dep state.Deployment) state.Deployment {
				t.Helper()
				dep, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageReadiness)
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			t.Run("source output digest must be new", func(t *testing.T) {
				app := createApp("source-retry-digest", false)
				original := create(state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployPending, SourcePath: "/review/source.tgz", SourceBytes: 512})
				if err := store.UpdateDeploymentStatus(ctx, original.ID, state.DeployImaging, ""); err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, original.ID, "/builder/first.tar", "builder/first", 512); err != nil {
					t.Fatal(err)
				}
				pins := store.(state.DeploymentBuildDigestStore)
				first, second := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
				if err := pins.PinDeploymentBuildDigest(ctx, original.ID, "/builder/first.tar", first); err != nil {
					t.Fatal(err)
				}
				original = fail(original)
				attempt := retry(original)
				if err := store.UpdateDeploymentStatus(ctx, attempt.ID, state.DeployImaging, ""); err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, attempt.ID, "/builder/second.tar", "builder/second", 512); err != nil {
					t.Fatal(err)
				}
				err := pins.PinDeploymentBuildDigest(ctx, attempt.ID, "/builder/second.tar", second)
				t.Logf("original digest=%s retry digest=%s new build digest=%s pin result=%v", original.ImageDigest, attempt.ImageDigest, second, err)
				if err != nil {
					t.Errorf("source retry cannot pin its rebuilt output: %v", err)
				}
			})
			for _, retryAttempt := range []bool{false, true} {
				name := "fresh service control"
				if retryAttempt {
					name = "failed service retry must keep readiness gate"
				}
				t.Run(name, func(t *testing.T) {
					slug := "service-fresh"
					if retryAttempt {
						slug = "service-retry"
					}
					app := createApp(slug, true)
					old := create(state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("a", 64), Status: state.DeployPending})
					if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
						t.Fatal(err)
					}
					now := time.Now().UTC()
					candidate := create(state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("b", 64), Status: state.DeployPending, RolloutState: "rolling_out", RolloutStartedAt: &now})
					if retryAttempt {
						candidate = retry(fail(candidate))
					}
					t.Logf("before activation: status=%s rollout=%s traffic=%d IsServiceRollout=%v", candidate.Status, candidate.RolloutState, candidate.TrafficPercent, state.IsServiceRollout(candidate))
					if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
						t.Fatal(err)
					}
					got, _ := store.DeploymentByID(ctx, candidate.ID)
					previous, _ := store.DeploymentByID(ctx, old.ID)
					t.Logf("after activation: candidate status=%s rollout=%s traffic=%d predecessor=%s/%d", got.Status, got.RolloutState, got.TrafficPercent, previous.Status, previous.TrafficPercent)
					if got.TrafficPercent != 0 || previous.Status != state.DeployLive || previous.TrafficPercent != 100 {
						t.Error("service candidate cut over before its readiness/route handoff")
					}
				})
			}
			for _, percent := range []int{0, 10, 100} {
				t.Run(fmt.Sprintf("explicit traffic retry %d", percent), func(t *testing.T) {
					slug := "explicit-zero"
					if percent == 10 {
						slug = "explicit-ten"
					}
					if percent == 100 {
						slug = "explicit-hundred"
					}
					app := createApp(slug, false)
					old := create(state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("a", 64), Status: state.DeployPending})
					if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
						t.Fatal(err)
					}
					candidate := create(state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example/api@sha256:" + strings.Repeat("b", 64), Status: state.DeployPending, TrafficPercent: percent, TrafficPercentExplicit: true})
					attempt := retry(fail(candidate))
					attempt = retry(fail(attempt))
					if err := store.MarkDeploymentLive(ctx, attempt.ID); err != nil {
						t.Fatal(err)
					}
					got, _ := store.DeploymentByID(ctx, attempt.ID)
					t.Logf("requested traffic=%d retry traffic=%d explicit=%v status=%s", percent, got.TrafficPercent, got.TrafficPercentExplicit, got.Status)
					if got.TrafficPercent != percent {
						t.Errorf("retry lost explicit traffic intent: got %d want %d", got.TrafficPercent, percent)
					}
				})
			}
		})
	}
}
