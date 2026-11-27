package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage's stored workflows are its own: production's
// lists and runnable definitions never see them, names stay unique per app
// until the contract release, a stage's workflow stays disabled, and the
// stage's deletion removes them.
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
			t.Fatalf("stage save = %+v, %v; want the stage's, disabled", saved, err)
		}
		published, err := mutate("stage-job", "publish", stage.ID, saved.Version, nil, false)
		if err != nil || published.Enabled || published.PublishedVersion == 0 {
			t.Fatalf("stage publish = %+v, %v; want published and disabled", published, err)
		}
		if _, err := mutate("stage-job", "enable", stage.ID, published.Version, nil, true); !errors.Is(err, state.ErrStoredWorkflowStageDisabled) {
			t.Fatalf("enable stage workflow: %v, want ErrStoredWorkflowStageDisabled", err)
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
		if err != nil || len(staged) != 1 || staged[0].Name != "stage-job" || staged[0].EnvironmentID != stage.ID {
			t.Fatalf("stage list = %+v, %v; want stage-job only", staged, err)
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
