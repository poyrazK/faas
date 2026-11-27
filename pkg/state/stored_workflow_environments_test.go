package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage's stored workflows are its own: production's
// lists and runnable definitions never see them, names stay unique per app
// until the contract release, a stage runs only manual and schedule
// workflows, and the stage's deletion removes them.
func TestStoredWorkflowEnvironments(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		account, err := store.CreateAccount(ctx, "swenv-"+suffix+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "swenv-" + suffix})
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "swenv-" + suffix,
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
		if err != nil {
			t.Fatal(err)
		}
		author := store.(state.StoredWorkflowStore)
		mutate := func(name, action, env string, version int64, raw json.RawMessage, enabled bool) (state.StoredWorkflow, error) {
			return author.MutateStoredWorkflow(ctx, app.ID, name, state.StoredWorkflowMutation{
				Action: action, ExpectedVersion: version, Draft: raw, Enabled: enabled, EnvironmentID: env})
		}
		schedule := `{"type":"schedule","schedule":"* * * * *"}`

		prod, err := mutate("prod-job", "save", "", 0, state.StoredWorkflowDraft("prod-job", "/prod", schedule), false)
		if err != nil {
			t.Fatal(err)
		}
		if prod, err = mutate("prod-job", "publish", "", prod.Version, nil, false); err != nil || !prod.Enabled {
			t.Fatalf("production publish = %+v, %v; want enabled", prod, err)
		}
		saved, err := mutate("stage-job", "save", stage.ID, 0, state.StoredWorkflowDraft("stage-job", "/stage", schedule), false)
		if err != nil || saved.EnvironmentID != stage.ID || saved.Enabled {
			t.Fatalf("stage save = %+v, %v; want the stage's, not yet enabled", saved, err)
		}
		published, err := mutate("stage-job", "publish", stage.ID, saved.Version, nil, false)
		if err != nil || !published.Enabled || published.PublishedVersion == 0 {
			t.Fatalf("stage schedule publish = %+v, %v; want published and enabled", published, err)
		}
		event := `{"type":"event","source":"billing","event_type":"paid"}`
		onEvent, err := mutate("stage-event", "save", stage.ID, 0, state.StoredWorkflowDraft("stage-event", "/event", event), false)
		if err != nil {
			t.Fatal(err)
		}
		if onEvent, err = mutate("stage-event", "publish", stage.ID, onEvent.Version, nil, false); err != nil || onEvent.Enabled {
			t.Fatalf("stage event publish = %+v, %v; want disabled", onEvent, err)
		}
		if _, err := mutate("stage-event", "enable", stage.ID, onEvent.Version, nil, true); !errors.Is(err, state.ErrStoredWorkflowStageDisabled) {
			t.Fatalf("enable stage event workflow: %v, want ErrStoredWorkflowStageDisabled", err)
		}
		if _, err := mutate("stage-job", "save", "", published.Version, state.StoredWorkflowDraft("stage-job", "/x", ""), false); !errors.Is(err, state.ErrStoredWorkflowNameInOtherEnvironment) {
			t.Fatalf("production write to a stage's name: %v", err)
		}
		if _, err := mutate("prod-job", "save", stage.ID, 0, state.StoredWorkflowDraft("prod-job", "/x", ""), false); !errors.Is(err, state.ErrStoredWorkflowNameInOtherEnvironment) {
			t.Fatalf("stage write to production's name: %v", err)
		}

		production, err := author.ListStoredWorkflows(ctx, app.ID)
		if err != nil || len(production) != 1 || production[0].Name != "prod-job" {
			t.Fatalf("production list = %+v, %v; want prod-job only", production, err)
		}
		staged, err := author.ListStageStoredWorkflows(ctx, app.ID, stage.ID)
		if err != nil || len(staged) != 2 || staged[0].Name != "stage-event" || staged[1].Name != "stage-job" || staged[1].EnvironmentID != stage.ID {
			t.Fatalf("stage list = %+v, %v; want stage-event and stage-job", staged, err)
		}
		if held, err := author.StoredWorkflowEnvironment(ctx, app.ID, "stage-job"); err != nil || held != stage.ID {
			t.Fatalf("StoredWorkflowEnvironment = %q, %v", held, err)
		}
		if held, err := author.StoredWorkflowEnvironment(ctx, app.ID, "prod-job"); err != nil || held != "" {
			t.Fatalf("StoredWorkflowEnvironment(production) = %q, %v", held, err)
		}
		effective, err := author.EffectiveWorkflowDefinitions(ctx, app.ID, json.RawMessage(`[]`))
		if err != nil || !strings.Contains(string(effective), "prod-job") || strings.Contains(string(effective), "stage-job") {
			t.Fatalf("production's runnable definitions = %s, %v; want prod-job without stage-job", effective, err)
		}
		revisions, total, err := author.ListStoredWorkflowRevisions(ctx, app.ID, "stage-job", state.StoredWorkflowRevisionListOptions{Limit: 10})
		if err != nil || total != 1 || len(revisions) != 1 {
			t.Fatalf("stage revisions = %+v (%d), %v", revisions, total, err)
		}

		if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
			t.Fatal(err)
		}
		if _, err := author.StoredWorkflowEnvironment(ctx, app.ID, "stage-job"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stage workflow after the stage's deletion: %v", err)
		}
		if _, total, err := author.ListStoredWorkflowRevisions(ctx, app.ID, "stage-job", state.StoredWorkflowRevisionListOptions{Limit: 10}); err != nil || total != 0 {
			t.Fatalf("stage revisions after the stage's deletion: %d, %v", total, err)
		}
	})
}

// adr: 20261104120000 — a stage's enabled schedule workflow runs on the
// stage's live deployment; production's schedules never see it, and its
// cursor survives production's admissions.
func TestStageWorkflowSchedules(t *testing.T) {
	state.WorkflowScheduleStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		suffix := uuid.NewString()[:8]
		account, err := store.CreateAccount(ctx, "swsched-"+suffix+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "swsched-" + suffix})
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "swsched-" + suffix,
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
		if err != nil {
			t.Fatal(err)
		}
		live := func(scope string) state.Deployment {
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending, Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			return dep
		}
		production, staging := live("default"), live("staging")
		author := store.(state.StoredWorkflowStore)
		saved, err := author.MutateStoredWorkflow(ctx, app.ID, "stage-cron", state.StoredWorkflowMutation{Action: "save",
			Draft: state.StoredWorkflowDraft("stage-cron", "/tick", `{"type":"schedule","schedule":"* * * * *"}`), EnvironmentID: stage.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateStoredWorkflow(ctx, app.ID, "stage-cron", state.StoredWorkflowMutation{Action: "publish", ExpectedVersion: saved.Version, EnvironmentID: stage.ID}); err != nil {
			t.Fatal(err)
		}

		schedules := store.(state.WorkflowScheduleStore)
		candidates, err := schedules.ListWorkflowScheduleCandidates(ctx, "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			if candidate.AppID == app.ID {
				t.Fatalf("production schedule candidates include the app with only a stage schedule: %+v", candidate)
			}
		}
		stages := store.(state.StageWorkflowScheduleStore)
		found, err := stages.ListStageWorkflowScheduleCandidates(ctx, "", "", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		var candidate state.StageWorkflowScheduleCandidate
		for _, c := range found {
			if c.AppID == app.ID {
				candidate = c
			}
		}
		if candidate.EnvironmentID != stage.ID || candidate.DeploymentID != staging.ID {
			t.Fatalf("stage candidate = %+v, want stage %s on %s", candidate, stage.ID, staging.ID)
		}
		if _, _, err := stages.AdmitStageScheduledWorkflow(ctx, app.ID, stage.ID, production.ID, "stage-cron", time.Now()); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Minute)
		var cursor state.WorkflowScheduleCursor
		for minute := 0; minute < 3 && cursor.LastRunID == ""; minute++ {
			if cursor, _, err = stages.AdmitStageScheduledWorkflow(ctx, app.ID, stage.ID, staging.ID, "stage-cron", now.Add(time.Duration(minute)*time.Minute)); err != nil {
				t.Fatal(err)
			}
			// Production's admission prunes production's cursors only.
			if _, _, err := schedules.AdmitScheduledWorkflow(ctx, app.ID, production.ID, "anything", now); err != nil {
				t.Fatal(err)
			}
		}
		if cursor.LastRunID == "" {
			t.Fatalf("stage schedule admitted no run: %+v", cursor)
		}
		run, err := store.GetWorkflowRun(ctx, cursor.LastRunID)
		if err != nil || run.DeploymentID != staging.ID || run.WorkflowName != "stage-cron" {
			t.Fatalf("stage run = %+v, %v; want stage-cron on %s", run, err, staging.ID)
		}
	})
}
