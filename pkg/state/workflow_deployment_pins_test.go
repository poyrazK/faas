// adr: 640
package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func replaceWorkflowDeployment(t *testing.T, store Store, app App) Deployment {
	t.Helper()
	dep, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:new", Status: DeployPending, Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestWorkflowDeploymentPinRoutingRetentionAndIdempotency(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		app, dep := seedWorkflowSchedule(t, store, "allow")
		key := "private-workflow-" + uuid.NewString()
		fingerprint := sha256.Sum256([]byte("input"))
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/rootfs/old", key, 1024); err != nil {
			t.Fatal(err)
		}
		run := &WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","run":"report"}]}`)}
		if _, replay, err := store.CreateWorkflowRunAdmittedWithIdempotencyKey(ctx, run, 10, "pin", fingerprint[:]); err != nil || replay {
			t.Fatalf("replay=%v err=%v", replay, err)
		}
		replacement := replaceWorkflowDeployment(t, store, app)
		if _, err := store.(RevisionPinStore).ExpireRevisionPins(ctx); err != nil {
			t.Fatal(err)
		}
		retained, err := store.DeploymentByID(ctx, dep.ID)
		if err != nil || retained.Status != DeployLive || retained.TrafficPercent != 0 {
			t.Fatalf("retained=%+v err=%v", retained, err)
		}
		replay := &WorkflowRun{AppID: app.ID, DeploymentID: replacement.ID, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{}`)}
		if _, yes, err := store.CreateWorkflowRunAdmittedWithIdempotencyKey(ctx, replay, 10, "pin", fingerprint[:]); err != nil || !yes || replay.DeploymentID != dep.ID {
			t.Fatalf("replay=%+v yes=%v err=%v", replay, yes, err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusRunning, nil, nil); err != nil {
			t.Fatal(err)
		}
		inv := Invocation{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID}
		for range 2 {
			_, selected, err := ResolveInvocationVersion(ctx, store, inv)
			if err != nil || selected.DeploymentID != dep.ID {
				t.Fatalf("selected=%+v err=%v", selected, err)
			}
		}
		if _, err := store.(RevisionPinStore).ResolveRevisionPin(ctx, app.ID, "default", dep.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("private pin extended public access: %v", err)
		}
		forgedHeaders, _ := json.Marshal(map[string]string{api.RevisionHeader: dep.ID, "X-Faas-Workflow-Run-Id": run.ID})
		if _, _, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Source: "http", Headers: forgedHeaders}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("forged private routing: %v", err)
		}
		mismatchedHeaders, _ := json.Marshal(map[string]string{api.RevisionHeader: replacement.ID})
		pinnedHeaders, _ := json.Marshal(map[string]string{api.RevisionHeader: dep.ID})
		if _, selected, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID, Headers: pinnedHeaders}); err != nil || selected.DeploymentID != dep.ID {
			t.Fatalf("transport pin=%+v err=%v", selected, err)
		}
		for _, bad := range []Invocation{
			{AppID: app.ID, Source: "http", WorkflowRunID: run.ID},
			{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID, PlatformTenantID: uuid.NewString()},
			{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID, DeploymentScope: "preview"},
			{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID, Headers: mismatchedHeaders},
		} {
			if _, _, err := ResolveInvocationVersion(ctx, store, bad); err == nil {
				t.Fatalf("invalid envelope routed: %+v", bad)
			}
		}
		if _, claimed, err := store.(LayerArtifactRetentionStore).ClaimLayerArtifactDeletion(ctx, key); err != nil || claimed {
			t.Fatalf("retained artifact deleted: claimed=%v err=%v", claimed, err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveInvocationVersion(ctx, store, inv); err == nil {
			t.Fatal("terminal run routed")
		}
		if _, err := store.(RevisionPinStore).ExpireRevisionPins(ctx); err != nil {
			t.Fatal(err)
		}
		retained, _ = store.DeploymentByID(ctx, dep.ID)
		if retained.Status != DeployLive {
			t.Fatal("terminal history lost code pin")
		}
		if n, err := store.SweepExpiredWorkflowRuns(ctx, 0); err != nil || n != 1 {
			t.Fatalf("pruned=%d err=%v", n, err)
		}
		if _, err := store.(RevisionPinStore).ExpireRevisionPins(ctx); err != nil {
			t.Fatal(err)
		}
		retained, _ = store.DeploymentByID(ctx, dep.ID)
		if retained.Status != DeploySuperseded {
			t.Fatalf("unreferenced code leaked: %+v", retained)
		}
		if _, claimed, err := store.(LayerArtifactRetentionStore).ClaimLayerArtifactDeletion(ctx, key); err != nil || !claimed {
			t.Fatalf("artifact not released: claimed=%v err=%v", claimed, err)
		}
	})
}

func TestWorkflowDeploymentPinEventsAndWebhooksBeforeAdmission(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		app, dep := seedEventWorkflow(t, store)
		work := publishWorkflowEvent(t, store, app, uuid.NewString())
		replaceWorkflowDeployment(t, store, app)
		if _, err := store.(RevisionPinStore).ExpireRevisionPins(ctx); err != nil {
			t.Fatal(err)
		}
		retained, _ := store.DeploymentByID(ctx, dep.ID)
		if retained.Status != DeployLive {
			t.Fatal("queued event lost captured code")
		}
		runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil || run.DeploymentID != dep.ID {
			t.Fatalf("event run=%+v err=%v", run, err)
		}

		webhookApp, endpoint, _ := seedWebhookAutomation(t, store)
		webhookDep, err := store.LiveDeploymentForScope(ctx, webhookApp.ID, "default")
		if err != nil {
			t.Fatal(err)
		}
		receipt, handled, err := store.(WebhookAutomationStore).AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_pinned"), true)
		if err != nil || !handled {
			t.Fatalf("receipt=%+v handled=%v err=%v", receipt, handled, err)
		}
		// Finish the previous claim before claiming this webhook's captured work.
		if err := store.(PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		replaceWorkflowDeployment(t, store, webhookApp)
		if _, err := store.(RevisionPinStore).ExpireRevisionPins(ctx); err != nil {
			t.Fatal(err)
		}
		webhookWork, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		captured := webhookWork.RecipientSnapshot[0]
		if captured.DeploymentID != webhookDep.ID {
			t.Fatalf("webhook capture=%+v", captured)
		}
		webhookRunID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, webhookWork.ID, webhookWork.ClaimToken, captured.ID)
		if err != nil {
			t.Fatal(err)
		}
		webhookRun, err := store.GetWorkflowRun(ctx, webhookRunID)
		if err != nil || webhookRun.DeploymentID != webhookDep.ID {
			t.Fatalf("webhook run=%+v err=%v", webhookRun, err)
		}
	})
}

func TestWorkflowDeploymentPinLegacyAndUnavailable(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		app, dep := seedWorkflowSchedule(t, store, "allow")
		run := &WorkflowRun{AppID: app.ID, WorkflowName: "old", DefinitionSnapshot: json.RawMessage(`{}`)}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusRunning, nil, nil); err != nil {
			t.Fatal(err)
		}
		replaceWorkflowDeployment(t, store, app)
		_, selected, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID})
		if err != nil || selected.DeploymentID != "" {
			t.Fatalf("legacy routing=%+v err=%v", selected, err)
		}
		for _, id := range []string{dep.ID, uuid.NewString()} {
			failed := &WorkflowRun{AppID: app.ID, DeploymentID: id, WorkflowName: "new", DefinitionSnapshot: json.RawMessage(`{}`)}
			if err := store.CreateWorkflowRun(ctx, failed); !errors.Is(err, ErrWorkflowDeploymentUnavailable) {
				t.Fatalf("unavailable code admitted: %v", err)
			}
		}
	})
}

func TestWorkflowDeploymentPinAdmissionSerializesWithRetirement(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		for range 5 {
			app, dep := seedWorkflowSchedule(t, store, "allow")
			replacement, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:new", Status: DeployPending})
			if err != nil {
				t.Fatal(err)
			}
			run := &WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{}`)}
			var wg sync.WaitGroup
			var admitted, promoted error
			wg.Add(2)
			go func() { defer wg.Done(); admitted = store.CreateWorkflowRun(t.Context(), run) }()
			go func() { defer wg.Done(); promoted = store.MarkDeploymentLive(t.Context(), replacement.ID) }()
			wg.Wait()
			if promoted != nil {
				t.Fatal(promoted)
			}
			old, err := store.DeploymentByID(t.Context(), dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if admitted == nil && old.Status != DeployLive {
				t.Fatal("committed run lost its code during cutover")
			}
			if admitted != nil && !errors.Is(admitted, ErrWorkflowDeploymentUnavailable) {
				t.Fatal(admitted)
			}
		}
	})
}

func TestWorkflowDeploymentPinDatabaseImmutability(t *testing.T) {
	ctx := context.Background()
	store := NewPgStore(pgtest.OpenMigrated(t))
	app, dep := seedWorkflowSchedule(t, store, "allow")
	run := &WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	replacement := replaceWorkflowDeployment(t, store, app)
	if _, err := store.pool.Exec(ctx, "UPDATE workflow_runs SET deployment_id=$2 WHERE id=$1", run.ID, replacement.ID); err == nil {
		t.Fatal("run code identity mutated")
	}
	if _, err := store.pool.Exec(ctx, "UPDATE workflow_runs SET deployment_id=NULL WHERE id=$1", run.ID); err == nil {
		t.Fatal("run code identity removed")
	}
	otherApp, otherDep := seedWorkflowSchedule(t, store, "allow")
	foreign := &WorkflowRun{AppID: app.ID, DeploymentID: otherDep.ID, WorkflowName: "foreign", DefinitionSnapshot: json.RawMessage(`{}`)}
	if err := store.CreateWorkflowRun(ctx, foreign); !errors.Is(err, ErrWorkflowDeploymentUnavailable) {
		t.Fatalf("foreign pin accepted (%s): %v", otherApp.ID, err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID}); err == nil {
		t.Fatal("retired code fell back to newest")
	}
	// The app purge removes deployments before its app-owned run ledger.
	// The deferred owner FK must allow that order, without letting an
	// independent deployment deletion discard a retained run.
	if _, err := store.pool.Exec(ctx, "DELETE FROM deployments WHERE id=$1", dep.ID); err == nil {
		t.Fatal("deployment deleted while a retained run still referenced it")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "DELETE FROM deployments WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM apps WHERE id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal("app purge blocked by private code FK", err)
	}
}

func TestWorkflowDeploymentPinSurvivesManualResume(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		app, dep := seedWorkflowSchedule(t, store, "allow")
		spec := simpleResumeSpec()
		raw, _ := json.Marshal(spec)
		run := &WorkflowRun{AppID: app.ID, DeploymentID: dep.ID, WorkflowName: spec.Name, DefinitionSnapshot: raw}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		failResumeRun(t, store, run, "send")
		replaceWorkflowDeployment(t, store, app)
		resumed, _, _, err := store.(WorkflowResumeStore).ResumeWorkflowRun(ctx, resumeOptions(run, app, 0))
		if err != nil || resumed.DeploymentID != dep.ID || resumed.ResumeCount != 1 {
			t.Fatalf("resumed=%+v err=%v", resumed, err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		_, version, err := ResolveInvocationVersion(ctx, store, Invocation{AppID: app.ID, Source: "workflow", WorkflowRunID: run.ID})
		if err != nil || version.DeploymentID != dep.ID {
			t.Fatalf("version=%+v err=%v", version, err)
		}
	})
}
