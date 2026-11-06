package imaged

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestImagePreparationUsesPinnedEnvironment(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "image-env-audit@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "image-audit"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "image-audit-app", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5, StartCommand: "./production", Manifest: state.AppManifest{RequestTimeoutS: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.StartCommand, settings.Manifest.RequestTimeoutS = "./staging", 55
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "example.test/app@sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := state.AppForDeployment(ctx, store, dep)
	if err != nil || resolved.StartCommand != "./staging" {
		t.Fatalf("fixture lacks environment pin: %q %v", resolved.StartCommand, err)
	}
	builder, scans := &fakeBuilder{bytesOut: 9}, 0
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans)
	err = h.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"})
	if len(builder.calls) != 1 {
		t.Fatalf("did not reach rootfs build: calls=%d err=%v", len(builder.calls), err)
	}
	manifest := builder.calls[0].Manifest
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.Entrypoint, []string{"/bin/sh", "-c", "./staging"}) || manifest.RequestTimeoutS != 55 {
		t.Fatal("image preparation used global app settings instead of pinned environment")
	}
}

func TestReleaseApprovalRecoveryReachesLaterBatches(t *testing.T) {
	for _, crowded := range []bool{false, true} {
		name := "control"
		if crowded {
			name = "64-older-held-releases"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			fx := seedReleaseGate(t, true)
			fx.requireApprovals(t)
			fx.decide(t, state.ReleaseApprovalApprove)
			fx.decide(t, state.ReleaseApprovalApprove)
			var held state.Deployment
			if crowded {
				for i := 0; i < 64; i++ {
					var err error
					held, err = fx.store.CreateDeployment(ctx, state.Deployment{AppID: fx.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:older-held", Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}, CreatedAt: fx.candidate.CreatedAt.Add(-30 * time.Minute)})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			loop := NewLoop(LoopConfig{Handler: fx.handler, Store: fx.store, Log: silentLogger()})
			loop.resumeReleaseApprovalHolds(ctx)
			if crowded {
				if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("recovery exceeded its first batch: %v", err)
				}
			}
			for i := 0; i < 2; i++ {
				loop.resumeReleaseApprovalHolds(ctx)
			}
			task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
			if err == nil && task.Status != state.AppTaskQueued {
				t.Fatalf("release task status=%q", task.Status)
			}
			if errors.Is(err, state.ErrNotFound) {
				t.Fatal("approved release never reached: older pending approvals monopolize every sweep")
			}
			if err != nil {
				t.Fatal(err)
			}
			if crowded {
				previous := fx
				previous.candidate = held
				if err := fx.store.SetDeploymentRootfs(ctx, held.ID, "/tmp/"+held.ID+".ext4", "apps/release-gate/"+held.ID+".ext4", 4096); err != nil {
					t.Fatal(err)
				}
				previous.decide(t, state.ReleaseApprovalApprove)
				previous.decide(t, state.ReleaseApprovalApprove)
				for i := 0; i < 2; i++ {
					loop.resumeReleaseApprovalHolds(ctx)
				}
				if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, held.ID); err != nil {
					t.Fatalf("recovery did not wrap to an older newly approved release: %v", err)
				}
			}
		})
	}
}
