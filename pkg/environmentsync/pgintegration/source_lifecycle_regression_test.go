package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInvestigationFailedOldPreparationDoesNotBlockDetach(t *testing.T) {
	for _, reconcileBeforeSwitch := range []bool{false, true} {
		name := "superseded-before-failure-observed"
		if reconcileBeforeSwitch {
			name = "control-observed-before-superseding"
		}
		t.Run(name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, plan, _ := workloadGraphFixture(t, basic)
				preparer := basic.(state.EnvironmentGitOpsPreparationStore)
				graphs := basic.(state.EnvironmentGitOpsGraphPreparationStore)
				candidates, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
				if err != nil || len(candidates) != 2 {
					t.Fatalf("prepare: %+v %v", candidates, err)
				}
				graph, err := graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
				if err != nil || graph.Phase != "preparing" {
					t.Fatalf("preparing: %+v %v", graph, err)
				}
				for _, candidate := range candidates {
					if err := basic.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeployFailed, "imager failed"); err != nil {
						t.Fatal(err)
					}
				}
				if reconcileBeforeSwitch {
					graph, err = graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
					if err != nil || graph.Phase != "failed" {
						t.Fatalf("failed reconciliation: %+v %v", graph, err)
					}
				}
				mode := "report"
				source, err := basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Mode: mode})
				if err != nil {
					t.Fatalf("switch to report: %v", err)
				}
				if !reconcileBeforeSwitch {
					_, err = graphs.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
					if !errors.Is(err, state.ErrConflict) {
						t.Fatalf("old preparation should be fenced: %v", err)
					}
				}
				err = basic.(state.EnvironmentGitOpsLifecycleStore).DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation)
				t.Logf("graph=%s, candidate deployments=failed, switched generation %d -> %d, detach=%v", graph.Phase, lease.Source.Generation, source.Generation, err)
				if err != nil {
					t.Errorf("completed failed candidates leave an unreachable preparation graph blocking detach: %v", err)
				}
			})
		})
	}
}

func TestInvestigationApprovedNumericDefinitionSurvivesStorage(t *testing.T) {
	for _, number := range []string{"1e-7", "1000"} {
		t.Run(number, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store := basic.(intentTestStore)
				source, desired, _ := intentFixture(t, store, "report")
				definition := desired.Definition
				definition.Configuration = map[string]json.RawMessage{"RATE": json.RawMessage(number)}
				manifest, err := json.Marshal(definition)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := gregalemanifest.ParseEnvironment(manifest)
				if err != nil {
					t.Fatal(err)
				}
				desired, err = environmentsync.Compile(parsed)
				if err != nil {
					t.Fatal(err)
				}
				source, revision, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
				if err != nil {
					t.Fatal(err)
				}
				var storedDefinition api.EnvironmentDefinition
				if err := json.Unmarshal(revision.Definition, &storedDefinition); err != nil {
					t.Fatal(err)
				}
				persisted, err := environmentsync.Compile(storedDefinition)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("approved RATE=%s, stored RATE=%s; digest survives persistence=%t", desired.Definition.Configuration["RATE"], persisted.Definition.Configuration["RATE"], persisted.Digest == revision.Digest)
				if persisted.Digest != revision.Digest {
					t.Error("approval accepted a valid parsed manifest but persisted a definition with a different digest")
				}
				worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, Mode: "report", LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
				if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
					t.Fatalf("report: %t %v", worked, err)
				}
				runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
				if err != nil || len(runs) != 1 {
					t.Fatalf("runs: %+v %v", runs, err)
				}
				t.Logf("first report after valid approval: status=%s error=%s", runs[0].Status, runs[0].ErrorCode)
				if runs[0].ErrorCode == "environment_definition_invalid" {
					t.Error("valid approved numeric configuration becomes unreadable by the report worker")
				}
			})
		})
	}
}

func TestObsoletePreparationBlocksRebindUntilPhysicalWorkFinishes(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, _ := workloadGraphFixture(t, basic)
		candidates, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(candidates) != 2 {
			t.Fatalf("prepare: %+v %v", candidates, err)
		}
		source, err := basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Mode: "report"})
		if err != nil {
			t.Fatal(err)
		}
		lifecycle := basic.(state.EnvironmentGitOpsLifecycleStore)
		spec := source.Spec
		spec.ManifestPath = "rebound.yaml"
		assertPending := func() {
			t.Helper()
			if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("pending image work allowed detach: %v", err)
			}
			if _, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("pending image work allowed rebind: %v", err)
			}
		}
		assertPending()
		if err := basic.UpdateDeploymentStatus(t.Context(), candidates[0].DeploymentID, state.DeployFailed, "failed image"); err != nil {
			t.Fatal(err)
		}
		assertPending()
		if err := basic.UpdateDeploymentStatus(t.Context(), candidates[1].DeploymentID, state.DeployFailed, "failed image"); err != nil {
			t.Fatal(err)
		}
		if next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec); err != nil || next.ID == source.ID {
			t.Fatalf("finished obsolete preparation blocked rebind: %+v %v", next, err)
		}
	})
}
