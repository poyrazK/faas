// adr: 1352
package outbound

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state"
)

func commandSimulation(t *testing.T, backend, mode string) api.ScenarioActionSnapshot {
	t.Helper()
	f := newSimulationFixture(t, backend)
	primary := f.recorderIdentity
	primaryRecorder := f.recorderID
	if err := f.store.SetDeploymentRootfs(t.Context(), primary.DeploymentID, "/test/rootfs", "test/command", 4096); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateDeploymentStatus(t.Context(), primary.DeploymentID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	spec := rehearsalcommand.Spec{Version: 1, RunID: primary.RunID, DeploymentID: primary.DeploymentID, Command: []string{"node", "import.js"}}
	argv, err := rehearsalcommand.Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	tasks := f.store.(state.AppTaskStore)
	params := state.CreateAppTaskParams{AccountID: primary.AccountID, AppID: primary.AppID, DeploymentID: primary.DeploymentID, Kind: state.AppTaskKindManual, Command: argv, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes, CreatedAt: time.Now().UTC()}
	ordinary := params
	ordinary.Command = []string{"true"}
	if _, err := tasks.CreateAppTask(t.Context(), ordinary); err == nil {
		t.Fatal("ordinary command admitted into contained app")
	}
	row, err := tasks.CreateAppTask(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if s := rawRecorderSeal(t, f); s.Status != "pending" || s.PendingManagedWork == 0 {
		t.Fatalf("queued command was omitted: %+v", s)
	}
	claimed, err := tasks.ClaimNextAppTask(t.Context(), "test-command", time.Now().UTC(), time.Minute)
	if err != nil || claimed.ID != row.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}

	identity := primary
	identity.InstanceID = row.ID
	owner, err := rehearsalrecorder.Start(t.Context(), t.Context(), f.store.(rehearsalrecorder.Store), identity)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.store.(interface {
		RecordScenarioCommandNetwork(context.Context, rehearsalrecorder.Identity, string, string, string) error
	})
	node := "memory-node"
	if f.pool != nil {
		n, err := f.store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		node = n.ID
	}
	if err := publisher.RecordScenarioCommandNetwork(t.Context(), identity, node, *claimed.LeaseToken, "10.100.1.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.MarkAppTaskRunning(t.Context(), claimed.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if pg, ok := f.store.(*state.PgStore); ok {
		callers, err := pg.LiveServiceCallersByHostIP(t.Context(), state.DefaultLocalNodeName, "10.100.1.2")
		if err != nil || len(callers) != 1 || callers[0].AppID != identity.AppID || callers[0].DeploymentID != identity.DeploymentID {
			t.Fatalf("command private caller identity missing: %+v %v", callers, err)
		}
	}
	owner.Activate()
	defer owner.Stop()
	f.recorderIdentity = identity
	// The helper's request writer uses the command owner's durable session.
	// Obtain its ID through the session's normal Begin path below, rather than
	// reconstructing or adopting a persisted recorder owner.
	token, err := f.signer.MintRehearsalRelay(time.Now(), identity.AccountID, identity.AppID, identity.InstanceID, identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	f.token = token.BearerValue()
	requests := 2
	if mode == "duplicate" {
		requests = 3
	}
	for i := 0; i < requests; i++ {
		requestID, err := owner.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		response, err := f.send(t, f.token, "/i/email/emails", "import-1", simulatedEmailBody, requestID)
		if i == 0 {
			if err == nil {
				t.Fatal("fixture timeout was lost")
			}
		} else if err != nil || response.StatusCode != 200 {
			t.Fatalf("command relay response: %+v %v", response, err)
		}
	}
	if mode == "recorder-gap" {
		if _, err := owner.Begin(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := (rehearsalcommand.Receipt{Version: 1, TaskID: row.ID, SpecSHA256: spec.SHA256(), Behavior: []byte(`{"version":1,"observations":[]}`)}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	complete := state.CompleteAppTaskParams{ID: row.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: string(raw), ExitCode: &exit, FinishedAt: time.Now().UTC()}
	if _, err := tasks.CompleteAppTask(t.Context(), complete); err == nil {
		t.Fatal("exit zero succeeded without owner retirement")
	}
	if f.pool != nil {
		if _, err := f.pool.Exec(t.Context(), `UPDATE app_tasks SET command=ARRAY['true'] WHERE id=$1`, row.ID); err == nil {
			t.Fatal("SQL command intent could be rewritten")
		}
	}
	if f.pool != nil {
		if _, err := f.pool.Exec(t.Context(), `UPDATE scenario_recorder_sessions SET status='closed',command_result=$2 WHERE instance_id=$1`, row.ID, string(raw)); err == nil {
			t.Fatal("SQL retired an active network identity")
		}
	}
	if mode == "owner-lost" {
		fencer := f.store.(state.ScenarioCommandStore)
		if err := fencer.RevokeScenarioCommandNetworksForNode(t.Context(), node); err != nil {
			t.Fatal(err)
		}
		owner.Stop()
		select {
		case <-owner.Done():
		case <-time.After(time.Second):
			t.Fatal("lost owner did not stop")
		}
		err = errors.New("owner lost")
	} else {
		if err := owner.PrepareRetirement(t.Context()); err != nil {
			t.Fatal(err)
		}
		err = owner.FinishRetirement(t.Context(), string(raw))
	}
	if pg, ok := f.store.(*state.PgStore); ok {
		callers, lookupErr := pg.LiveServiceCallersByHostIP(t.Context(), state.DefaultLocalNodeName, "10.100.1.2")
		if lookupErr != nil || len(callers) != 0 {
			t.Fatalf("retired command still authenticated: %+v %v", callers, lookupErr)
		}
		if _, writeErr := f.pool.Exec(t.Context(), `UPDATE scenario_command_networks SET active=true WHERE task_id=$1`, row.ID); writeErr == nil {
			t.Fatal("retired network authority could be reactivated")
		}
	}

	if mode == "recorder-gap" || mode == "owner-lost" {
		if err == nil {
			t.Fatal("unadmitted request retired as complete")
		}
		owner.Stop()
		select {
		case <-owner.Done():
		case <-time.After(time.Second):
			t.Fatal("failed owner did not stop")
		}
		code, message := "recorder_incomplete", "command recorder did not settle"
		complete.Status = state.AppTaskFailed
		complete.ExitCode = nil
		complete.FailureCode = &code
		complete.FailureMessage = &message
	} else if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.CompleteAppTask(t.Context(), complete); err != nil {
		t.Fatal(err)
	}
	addForeignQueuedRehearsalCommand(t, f)
	// Restore the still-running serving VM owner for the global closing barrier.
	f.recorderIdentity, f.recorderID = primary, primaryRecorder
	s := f.seal(t)
	if f.providerCalls.Load() != 0 {
		t.Fatal("contained command contacted live provider")
	}
	if mode != "recorder-gap" {
		if _, err := tasks.CreateAppTask(t.Context(), params); err == nil {
			t.Fatal("sealed run admitted another command")
		}
	}
	return s
}

func addForeignQueuedRehearsalCommand(t *testing.T, f *simulationFixture) {
	t.Helper()
	account, err := f.store.CreateAccount(t.Context(), uuid.NewString()+"@foreign-command.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "foreign-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := api.NormalizeScenarioSimulationPlan(f.plan, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RegisterScenarioTestMembers(t.Context(), account.ID, f.req.RunID, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: "capture", NetworkMode: "contained", SimulationJSON: string(body)}}); err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:foreign", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentRootfs(t.Context(), dep.ID, "/test/foreign", "test/foreign", 4096); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	argv, err := rehearsalcommand.Encode(rehearsalcommand.Spec{Version: 1, RunID: f.req.RunID, DeploymentID: dep.ID, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.(state.AppTaskStore).CreateAppTask(t.Context(), state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: argv, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}); err != nil {
		t.Fatal(err)
	}
}

func TestContainedCommandComparisonDetectsDuplicateAndRecorderGap(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			before, after := commandSimulation(t, backend, "baseline"), commandSimulation(t, backend, "duplicate")
			comparison := actioncompare.Compare(simulationEvidence(before), simulationEvidence(after))
			if comparison.Status != "failed" || comparison.BeforeCount == nil || *comparison.BeforeCount != 2 || *comparison.AfterCount != 3 {
				t.Fatalf("duplicate command action lost: %+v", comparison)
			}
			for _, mode := range []string{"recorder-gap", "owner-lost"} {
				gap := commandSimulation(t, backend, mode)
				if result := actioncompare.Compare(simulationEvidence(before), simulationEvidence(gap)); result.Status != "inconclusive" {
					t.Fatalf("recorder gap passed: %+v", result)
				}
			}
		})
	}
}
