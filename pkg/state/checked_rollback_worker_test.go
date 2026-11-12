package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerCheckedRollbackRequiresCurrentRuntimeAtCommit(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := scopedRollbackStore(t, backend)
			for _, change := range []string{"ready", "stopped", "env", "missing-proof", "old-running", "replacement"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					app, target := deploymentAttemptFixture(t, s)
					if err := s.MarkDeploymentLive(ctx, target.ID); err != nil {
						t.Fatal(err)
					}
					retained := publishAttemptWorker(t, s, app, target, true)
					current, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)})
					if err != nil {
						t.Fatal(err)
					}
					if err = s.SetDeploymentRootfs(ctx, current.ID, "/test/"+current.ID, "test/"+current.ID, 4096); err != nil {
						t.Fatal(err)
					}
					if err = s.MarkDeploymentLive(ctx, current.ID); err != nil {
						t.Fatal(err)
					}
					if change != "old-running" {
						if err = s.UpdateInstanceState(ctx, retained.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
					}
					rollbacks := s.(state.CheckedRollbackStore)
					op, err := rollbacks.CreateCheckedRollback(ctx, app.AccountID, app.ID, target.ID, current.ID, "worker recovery")
					if err != nil {
						t.Fatal(err)
					}
					worker := publishAttemptWorker(t, s, app, target, change != "missing-proof")
					if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
						t.Fatal(err)
					}
					// imaged closes these stages after readiness, before apid commits.
					if _, err = s.AppendDeploymentStage(ctx, target.ID, state.StageSnapshotPrepare, state.StageReadiness, time.Now().UTC(), ""); err != nil {
						t.Fatal(err)
					}
					if _, err = s.CloseDeploymentStage(ctx, target.ID, state.StageReadiness, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					op, err = rollbacks.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
					if err != nil || op.Status != "ready" {
						t.Fatalf("readiness=%+v %v", op, err)
					}
					switch change {
					case "stopped", "old-running", "replacement":
						err = s.UpdateInstanceState(ctx, worker.ID, "stopped")
					case "env":
						err = s.UpsertAppEnv(ctx, app.AccountID, app.ID, "VERSION", "new")
					}
					if err != nil {
						t.Fatal(err)
					}
					if change == "replacement" {
						publishAttemptWorker(t, s, app, target, true)
					}
					committed, err := rollbacks.CommitCheckedRollback(ctx, op)
					if change == "ready" || change == "replacement" {
						if err != nil || committed.Status != "complete" {
							t.Fatalf("valid cutover=%+v %v", committed, err)
						}
						return
					}
					if !errors.Is(err, state.ErrWorkerRollbackNotReady) {
						t.Fatalf("unready cutover=%+v %v", committed, err)
					}
					d, err := s.DeploymentByID(ctx, target.ID)
					if err != nil {
						t.Fatal(err)
					}
					old, err := s.DeploymentByID(ctx, current.ID)
					if err != nil || d.TrafficPercent != 0 || old.Status != state.DeployLive || old.TrafficPercent != 100 {
						t.Fatalf("rejected cutover changed traffic: target=%+v current=%+v %v", d, old, err)
					}
					if change == "env" {
						if err = rollbacks.UpdateCheckedRollback(ctx, op, "blocked", "worker_rollback_not_ready", nil); err != nil {
							t.Fatal(err)
						}
						if err = s.UpdateInstanceState(ctx, worker.ID, "stopped"); err != nil {
							t.Fatal(err)
						}
						publishAttemptWorker(t, s, app, target, true)
						if committed, err = rollbacks.CommitCheckedRollback(ctx, op); err != nil || committed.Status != "complete" {
							t.Fatalf("blocked rollback did not recover with current worker: %+v %v", committed, err)
						}
					}
				})
			}
		})
	}
}
