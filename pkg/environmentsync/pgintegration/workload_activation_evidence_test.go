package pgintegration_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsActivationEvidenceRequiresWholeGraphAndSeparateProofs(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests := preparedQualificationFixture(t, basic)
		store := basic.(state.EnvironmentGitOpsActivationEvidenceStore)
		if _, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, environmentsync.Plan{Hash: strings.Repeat("0", 64)}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unreviewed graph assessed: %v", err)
		}
		assertEvidence := func(captures int) {
			t.Helper()
			evidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
			if err != nil || !evidence.ArtifactsPrepared || evidence.Candidates != 2 || evidence.CapturesRecorded != captures ||
				evidence.Qualified || evidence.Activated || evidence.Serving || evidence.PlanHash != plan.Hash {
				t.Fatalf("preparation/capture became activation: %+v %v", evidence, err)
			}
			if slices.Contains(evidence.BlockingReasons, "environment_capture_evidence_missing") != (captures < 2) ||
				!slices.Contains(evidence.BlockingReasons, "environment_restore_evidence_missing") ||
				!slices.Contains(evidence.BlockingReasons, "environment_isolated_smoke_evidence_missing") ||
				!slices.Contains(evidence.BlockingReasons, "environment_activation_evidence_missing") ||
				!slices.Contains(evidence.BlockingReasons, "environment_serving_evidence_missing") {
				t.Fatalf("missing proof was hidden: %+v", evidence)
			}
		}
		assertEvidence(0)
		for i, request := range requests {
			placement := qualificationPlacement(t, basic, 4096)
			claimed, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "scheduler", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
			if err != nil {
				t.Fatal(err)
			}
			frame := admitted.Execution
			if err := basic.(state.EnvironmentQualificationExecutionStore).MarkEnvironmentQualificationDispatched(t.Context(), claimed, frame); err != nil {
				t.Fatal(err)
			}
			if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, state.EnvironmentWorkloadQualificationRuntime{
				NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: "qualification-netns", HostIP: "10.0.0.1", GuestUID: 20001,
				Inputs: state.RuntimeConfigInputs{Scope: "production", Boundary: time.Unix(0, 0), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}}); err != nil {
				t.Fatal(err)
			}
			proof := captureProof(frame)
			if _, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof); err != nil {
				t.Fatal(err)
			}
			if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), frame, state.EnvironmentQualificationRetirement{
				Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}); err != nil {
				t.Fatal(err)
			}
			assertEvidence(i + 1)
		}
		for _, request := range requests {
			if err := basic.MarkDeploymentLive(t.Context(), request.DeploymentID); err == nil {
				t.Fatal("complete capture cohort released a held deployment")
			}
		}
		if err := basic.SetDeploymentRootfs(t.Context(), requests[0].DeploymentID, "/changed.ext4", "changed", 4096); err != nil {
			t.Fatal(err)
		}
		evidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
		if err == nil && evidence.CapturesRecorded == 2 {
			t.Fatal("changed artifact borrowed old capture evidence")
		}
	})
}
