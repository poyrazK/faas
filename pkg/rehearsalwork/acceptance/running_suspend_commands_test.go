// adr: 1359
package acceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunningSuspendWaitsForExistingTriggerCommandRetirement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newFeatureFixture(t, backend, false, fixtureFeatures{running: true})
			ctx := t.Context()
			primary := f.identities["primary"]
			if err := f.store.SetDeploymentRootfs(ctx, primary.DeploymentID, "/fixture/layer.ext4", "fixture/layer.ext4", 4096); err != nil {
				t.Fatal(err)
			}
			argv, err := rehearsalcommand.Encode(rehearsalcommand.Spec{Version: 1, RunID: f.run, DeploymentID: primary.DeploymentID, Command: []string{"trigger-import"}})
			if err != nil {
				t.Fatal(err)
			}
			tasks := f.store.(state.AppTaskStore)
			params := state.CreateAppTaskParams{AccountID: f.account.ID, AppID: primary.AppID, DeploymentID: primary.DeploymentID, Kind: state.AppTaskKindManual, Command: argv, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes, TimeoutSeconds: 60}
			task, err := tasks.CreateAppTask(ctx, params)
			if err != nil {
				t.Fatal(err)
			}
			job := f.claim(t, f.enqueue(t).ID)
			decision := f.checkpoint(t)
			control := f.store.(rehearsalresume.Store)
			receipt, err := control.ScenarioSuspendResume(ctx, decision.SuspendResumeID)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.State != "waiting" {
				t.Fatal("retiring command did not retain an ineligible checkpoint")
			}
			if _, err = f.store.(rehearsalrecorder.SuspendStore).AdvanceScenarioSuspendResume(ctx, f.identities["worker"], receipt.RecorderSessionID, receipt.ID, "waiting", "armed", 0, 0); !errors.Is(err, rehearsalresume.ErrCheckpointBusy) {
				t.Fatalf("running command did not fence pause: %v", err)
			}
			var conn *rehearsalrecorder.Connection
			conn, err = f.workerSession.Attach(func() { conn.Detach(false) })
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Detach(false)
			holdCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			held := make(chan error, 1)
			go func() { held <- f.workerSession.HoldSuspendCheckpoint(holdCtx, receipt.ID, conn) }()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for !f.workerSession.HeldSuspendCheckpoint(receipt.ID) {
				select {
				case <-deadline.C:
					t.Fatal("original relay did not retain checkpoint")
				case <-tick.C:
				}
			}
			if pending, err := control.PendingScenarioSuspendResumes(ctx); err != nil || len(pending) != 0 {
				t.Fatal("scheduler could claim a pause before trigger retirement")
			}
			if _, err = tasks.CreateAppTask(ctx, params); err == nil {
				t.Fatal("another command entered the held checkpoint")
			}
			if _, err = tasks.RequestAppTaskCancellation(ctx, f.account.ID, primary.AppID, task.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			for {
				r, readErr := control.ScenarioSuspendResume(ctx, receipt.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if r.State == "armed" {
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("settled command did not release checkpoint eligibility")
				case <-tick.C:
				}
			}
			f.resumeRunningCheckpoint(t, receipt, held, 100)
			f.complete(t, job)
		})
	}
}
