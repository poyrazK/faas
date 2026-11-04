package pgintegration_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsReportRuntimeObservationPreservesPendingWork(t *testing.T) {
	for _, scenario := range []string{"snapshot", "new-boundary"} {
		t.Run(scenario, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, old, _, app, deployment, _ := runtimeFixture(t, basic, scenario == "new-boundary")
				var snapshot state.Snapshot
				var err error
				if scenario == "snapshot" {
					snapshot, err = basic.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: deployment.ID,
						FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.UpdateEnvironmentGitSource(t.Context(), old.Source.AccountID, old.Source.ID,
					state.EnvironmentGitSourceUpdate{ExpectedGeneration: old.Source.Generation, Mode: "report"}); err != nil {
					t.Fatal(err)
				}
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "report-observation", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
				before, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
				if err != nil || len(before) != 1 {
					t.Fatal("missing runtime effect", err)
				}
				if scenario == "new-boundary" {
					if err := basic.MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), old, before[0].ID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("superseded lease observed runtime work", err)
				}
				for _, callerMode := range []string{"report", "enforce"} {
					// Caller metadata cannot grant execution after the persisted
					// source switched to report mode.
					caller := lease
					caller.Source.Spec.Mode = callerMode
					progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), caller, before[0].ID)
					if err != nil || progress.Ready || progress.Request != nil {
						t.Fatalf("report observation hid pending work: %+v %v", progress, err)
					}
				}
				after, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("report changed runtime effect identity or progress: %+v %+v %v", before, after, err)
				}
				if scenario == "snapshot" {
					latest, err := basic.LatestSnapshot(t.Context(), deployment.ID)
					if err != nil || latest.ID != snapshot.ID || latest.Stale {
						t.Fatalf("report invalidated a snapshot: %+v %v", latest, err)
					}
				}
			})
		})
	}
}
