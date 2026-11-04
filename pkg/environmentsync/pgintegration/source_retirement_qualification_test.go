package pgintegration_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitOpsSourceRetirementPreservesCompletedQualification(t *testing.T) {
	for _, operation := range []string{"detach", "rebind"} {
		t.Run(operation, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, requests := preparedRequestModeQualificationFixture(t, basic)
				finishColdGitOpsRuntime(t, basic, lease)
				qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
				executor := basic.(state.EnvironmentQualificationExecutionStore)
				snapshots := basic.(state.EnvironmentQualificationSnapshotStore)
				var claims []state.EnvironmentWorkloadQualificationRequest
				for _, request := range requests {
					claim, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "retirement", time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					completeClaimedQualificationReceipts(t, basic.(requestQualificationReceiptStore), claim, qualificationPlacement(t, basic, 4096))
					claims = append(claims, claim)
				}
				before, err := executor.EnvironmentQualificationExecution(t.Context(), claims[0].ReservedInstanceID)
				if err != nil || before.RetiredAt == nil {
					t.Fatalf("qualification did not retire: %+v %v", before, err)
				}
				capture, err := snapshots.EnvironmentQualificationSnapshotReceipt(t.Context(), claims[0].ReservedInstanceID)
				if err != nil {
					t.Fatal(err)
				}
				source := reportSourceForRetirement(t, basic, lease)
				lifecycle := basic.(state.EnvironmentGitOpsLifecycleStore)
				if operation == "detach" {
					if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
						t.Fatal("completed history blocked detach", err)
					}
				} else {
					spec := source.Spec
					spec.ManifestPath = "environments/rebound.yaml"
					next, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec)
					if err != nil || next.ID == source.ID || next.Generation <= source.Generation || next.ApprovedRevisionID != "" || next.AppliedRevisionID != "" {
						t.Fatalf("rebind inherited old authority: %+v %v", next, err)
					}
				}
				after, err := executor.EnvironmentQualificationExecution(t.Context(), claims[0].ReservedInstanceID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("source retirement changed immutable cleanup history", err)
				}
				retained, err := snapshots.EnvironmentQualificationSnapshotReceipt(t.Context(), claims[0].ReservedInstanceID)
				if err != nil || !reflect.DeepEqual(capture, retained) {
					t.Fatal("source retirement changed snapshot evidence", err)
				}
				if err := qualifier.ValidateEnvironmentWorkloadQualification(t.Context(), claims[0]); !errors.Is(err, state.ErrConflict) {
					t.Fatal("retired source retained dispatch authority", err)
				}
				for _, claim := range claims {
					deployment, err := basic.DeploymentByID(t.Context(), claim.DeploymentID)
					if err != nil || deployment.RootfsKey != claim.Artifact.RootfsKey || deployment.Status != state.DeploySnapshotting || !deployment.EnvironmentWorkloadHeld() {
						t.Fatalf("source retirement changed the retained artifact: %+v %v", deployment, err)
					}
				}
			})
		})
	}
}

func TestGitOpsSourceRetirementWaitsForQualificationCleanup(t *testing.T) {
	for _, stage := range []string{"capture", "restore"} {
		t.Run(stage, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, requests := preparedRequestModeQualificationFixture(t, basic)
				finishColdGitOpsRuntime(t, basic, lease)
				qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
				executor := basic.(state.EnvironmentQualificationExecutionStore)
				claim, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "retirement", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				placement := qualificationPlacement(t, basic, 4096)
				admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claim, placement)
				if err != nil {
					t.Fatal(err)
				}
				frame := admission.Execution
				if stage == "restore" {
					if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), claim, frame); err != nil {
						t.Fatal(err)
					}
					runtime := state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID,
						Netns: "retirement-capture", HostIP: "10.100.0.5", GuestUID: 20001,
						Inputs: state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}}
					if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claim, runtime); err != nil {
						t.Fatal(err)
					}
					if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(t.Context(), claim, frame, strings.Repeat("a", 64)); err != nil {
						t.Fatal(err)
					}
					capture, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), claim, frame, captureProof(frame))
					if err != nil {
						t.Fatal(err)
					}
					retireCapturedQualification(t, basic, capture)
					restored, err := basic.(state.EnvironmentQualificationRestoreStore).CreateEnvironmentQualificationRestore(t.Context(), claim, qualificationPlacement(t, basic, 4096))
					if err != nil {
						t.Fatal(err)
					}
					frame = restored.Execution
				}
				source := reportSourceForRetirement(t, basic, lease)
				lifecycle := basic.(state.EnvironmentGitOpsLifecycleStore)
				if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); !errors.Is(err, state.ErrConflict) {
					t.Fatal("source detached before native cleanup was proven", err)
				}
				spec := source.Spec
				if _, err := lifecycle.RebindEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation, spec); !errors.Is(err, state.ErrConflict) {
					t.Fatal("source rebound before native cleanup was proven", err)
				}
				// A never-dispatched owner is retired through its original
				// cleanup capability, even after report mode revokes execution.
				if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}); err != nil {
					t.Fatal(err)
				}
				if err := lifecycle.DetachEnvironmentGitSource(t.Context(), source.AccountID, source.ID, source.Generation); err != nil {
					t.Fatal("completed cleanup still blocked detach", err)
				}
			})
		})
	}
}

func reportSourceForRetirement(t *testing.T, basic gitOpsTestStore, lease state.EnvironmentGitOpsLease) state.EnvironmentGitSource {
	t.Helper()
	source, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	source, err = basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID,
		state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
	if err != nil {
		t.Fatal(err)
	}
	return source
}
