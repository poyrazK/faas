// adr: 1155 — candidate assessment and customer status feedback contracts.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsCommitPreviewBeforeApprovalHasNoAuthority(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		source, desired := seed(t, store)
		previewer := store.(state.EnvironmentGitOpsPreviewStore)
		preview, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), source, strings.Repeat("a", 40), desired)
		if err != nil || !preview.ReadOnly || !preview.ValuesRedacted || preview.Plan.Generation != 0 || len(preview.Plan.Hash) != 64 {
			t.Fatalf("unapproved preview: %+v %v", preview, err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || current.Generation != source.Generation || current.IntentVersion != source.IntentVersion || current.ApprovedRevisionID != "" || current.AppliedRevisionID != "" {
			t.Fatalf("preview wrote source authority: %+v %v", current, err)
		}
		if _, err := store.AppBySlug(t.Context(), "shop-api"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("preview reserved a workload: %v", err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 0 {
			t.Fatalf("preview created controller work: %v %v", runs, err)
		}
		foreign := source
		foreign.AccountID = uuid.NewString()
		if _, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), foreign, strings.Repeat("a", 40), desired); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign account preview: %v", err)
		}
		for _, axis := range []string{"project", "environment"} {
			definition := desired.Definition
			if axis == "project" {
				definition.Project = "another-project"
			} else {
				definition.Environment = "another-environment"
			}
			candidate, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), source, strings.Repeat("a", 40), candidate); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("wrong %s definition accepted: %v", axis, err)
			}
		}
		suspended := true
		if _, err := store.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		if _, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), source, strings.Repeat("a", 40), desired); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed binding preview accepted: %v", err)
		}
	})
}

func TestGitOpsCommitPreviewRedactsValuesAndPinsLiveIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		source, desired, app := intentFixture(t, store, "report")
		adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash) != nil {
			t.Fatal("could not adopt fixture")
		}
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", "PRIVATE_CURRENT_VALUE"); err != nil {
			t.Fatal(err)
		}
		desired.Definition.Workloads["api"].Variables["MODE"] = "PRIVATE_CANDIDATE_VALUE"
		desired, err = environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		previewer := basic.(state.EnvironmentGitOpsPreviewStore)
		preview, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), source, strings.Repeat("b", 40), desired)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(preview)
		if strings.Contains(string(body), "PRIVATE_") || !strings.Contains(string(body), "variables/MODE") {
			t.Fatalf("preview disclosed values or lost affected field: %s", body)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Plan.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("candidate hash authorized adoption: %v", err)
		}
		assertVariable(t, store, source, app, source.EnvironmentSlug, "MODE", "PRIVATE_CURRENT_VALUE")
		if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, source.EnvironmentSlug, "MODE", "ANOTHER_VALUE"); err != nil {
			t.Fatal(err)
		}
		later, err := previewer.PreviewEnvironmentGitOpsCommit(t.Context(), source, strings.Repeat("b", 40), desired)
		if err != nil || later.Plan.ObservedVersion <= preview.Plan.ObservedVersion || later.Plan.Hash == preview.Plan.Hash {
			t.Fatalf("preview did not bind the new observation: %+v %v", later, err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
		if err != nil || current.ApprovedRevisionID != source.ApprovedRevisionID {
			t.Fatalf("candidate became approved: %+v %v", current, err)
		}
	})
}
