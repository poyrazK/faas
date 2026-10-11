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
		assertEvidence := func(captures, restores, smokes int) {
			t.Helper()
			qualified := captures == 2 && restores == 2 && smokes == 2
			evidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
			if err != nil || !evidence.ArtifactsPrepared || evidence.Candidates != 2 || evidence.CapturesRecorded != captures ||
				evidence.GuestConfigAcknowledgementsRecorded != restores || evidence.RestoresRecorded != restores ||
				evidence.FrameworkReadyAcknowledgementsRecorded != restores || evidence.SmokesRecorded != smokes ||
				evidence.Qualified != qualified || evidence.Activated || evidence.Serving || evidence.PlanHash != plan.Hash {
				t.Fatalf("preparation/capture became activation: %+v %v", evidence, err)
			}
			if slices.Contains(evidence.BlockingReasons, "environment_capture_evidence_missing") != (captures < 2) ||
				slices.Contains(evidence.BlockingReasons, "environment_restore_evidence_missing") != (restores < 2) ||
				slices.Contains(evidence.BlockingReasons, "environment_guest_config_acknowledgement_missing") != (restores < 2) ||
				slices.Contains(evidence.BlockingReasons, "environment_framework_ready_evidence_missing") != (restores < 2) ||
				slices.Contains(evidence.BlockingReasons, "environment_isolated_smoke_evidence_missing") != (smokes < 2) ||
				!slices.Contains(evidence.BlockingReasons, "environment_activation_evidence_missing") ||
				!slices.Contains(evidence.BlockingReasons, "environment_serving_evidence_missing") {
				t.Fatalf("missing proof was hidden: %+v", evidence)
			}
		}
		assertEvidence(0, 0, 0)
		activationStore := basic.(state.EnvironmentGitOpsGraphActivationStore)
		if _, activated, err := activationStore.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || activated {
			t.Fatalf("unqualified graph was activated: activated=%t err=%v", activated, err)
		}
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
			runtime := state.EnvironmentWorkloadQualificationRuntime{
				NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: "qualification-netns", HostIP: "10.0.0.1", GuestUID: 20001,
				Inputs: state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}}
			if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil {
				t.Fatal(err)
			}
			if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
				t.Context(), claimed, frame, strings.Repeat("a", 64)); err != nil {
				t.Fatalf("capture guest config acknowledgement: %v", err)
			}
			proof := captureProof(frame)
			if _, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), claimed, frame, proof); err != nil {
				t.Fatal(err)
			}
			if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), frame, state.EnvironmentQualificationRetirement{
				Kind: state.QualificationNativeRetired, ReceiptID: proof.CaptureID, NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}); err != nil {
				t.Fatal(err)
			}
			assertEvidence(i+1, i, i)

			restoreStore := basic.(state.EnvironmentQualificationRestoreStore)
			restorePlacement := placement
			restorePlacement.WakeID = uuid.NewString()
			restored, err := restoreStore.CreateEnvironmentQualificationRestore(t.Context(), claimed, restorePlacement)
			if err != nil || !restored.Created || restored.Execution.CaptureInstanceID != frame.InstanceID {
				t.Fatalf("distinct restore admission: %+v %v", restored, err)
			}
			if err := restoreStore.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, restored.Execution); err != nil {
				t.Fatal(err)
			}
			runtime.NodeID, runtime.WakeID = restorePlacement.NodeID, restorePlacement.WakeID
			runtime.Inputs.Boundary = time.Now().UTC()
			if _, err := basic.(state.EnvironmentQualificationRestoreRuntimeStore).PublishEnvironmentQualificationRestoreRuntime(t.Context(), claimed, restored.Execution, runtime); err != nil {
				t.Fatal(err)
			}
			if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
				t.Context(), claimed, restored.Execution, strings.Repeat("b", 64)); err != nil {
				t.Fatalf("restore guest config acknowledgement: %v", err)
			}
			readyStore, ok := basic.(state.EnvironmentQualificationFrameworkReadyReceiptStore)
			if !ok {
				t.Fatal("store is missing restored framework-ready receipt support")
			}
			runtimeLabel := claimed.FrozenInputs.RuntimeBase
			if runtimeLabel == "" {
				runtimeLabel = "node22" // custom images may not declare a Gregale runner base
			} else {
				wrongRuntime := "node22"
				if runtimeLabel == wrongRuntime {
					wrongRuntime = "python312"
				}
				if _, err := readyStore.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, wrongRuntime, int64(42+i)); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("framework-ready event disagreed with the frozen runner: %v", err)
				}
			}
			ready, err := readyStore.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, runtimeLabel, int64(42+i))
			if err != nil || ready.RequestID != request.ID || ready.Attempt != claimed.Attempt || ready.GraphID != claimed.GraphID ||
				ready.CaptureInstanceID != frame.InstanceID || ready.InstanceID != restored.Execution.InstanceID || ready.Runtime != runtimeLabel ||
				ready.WarmupMS != int64(42+i) || ready.RecordedAt.IsZero() {
				t.Fatalf("restored framework-ready receipt: %+v %v", ready, err)
			}
			if _, err := readyStore.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, runtimeLabel, int64(42+i)); err != nil {
				t.Fatalf("idempotent framework-ready receipt: %v", err)
			}
			if _, err := readyStore.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, runtimeLabel, int64(99+i)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("framework-ready retry replaced original warmup: %v", err)
			}
			restoreReceipts := basic.(state.EnvironmentQualificationRestoreReceiptStore)
			if _, err := restoreReceipts.RecordEnvironmentQualificationRestoreReceipt(t.Context(), claimed, restored.Execution, runtime.Inputs); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("live restore target counted as retired evidence: %v", err)
			}
			smokeReceipts := basic.(state.EnvironmentQualificationSmokeReceiptStore)
			policy, smokePolicySHA256, policyErr := state.EnvironmentQualificationSmokePolicyFor(claimed)
			if policyErr != nil {
				t.Fatalf("derive reviewed smoke policy: resource=%s mode=%s runtime=%s queues=%d smokes=%d: %v",
					claimed.Resource, claimed.ExecutionMode, claimed.FrozenInputs.Runtime, len(claimed.FrozenInputs.QueueBindings),
					len(claimed.FrozenInputs.QueueSmoke), policyErr)
			}
			smokeEvidence := state.EnvironmentQualificationSmokeEvidence{Resource: claimed.Resource, InstanceID: restored.Execution.InstanceID,
				PolicyID: policy.ID, PolicySHA256: smokePolicySHA256, ResultSHA256: strings.Repeat("b", 64), Passed: true}
			if _, err := smokeReceipts.RecordEnvironmentQualificationSmokeReceipt(t.Context(), claimed, smokeEvidence); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("live restore target counted as isolated smoke evidence: %v", err)
			}
			targetGeneration := uuid.NewString()
			if targetGeneration == proof.NativeGeneration {
				targetGeneration = uuid.NewString()
			}
			retirement := state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(),
				NativeGeneration: targetGeneration, KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}
			if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), restored.Execution, retirement); err != nil {
				t.Fatal(err)
			}
			receipt, err := restoreReceipts.RecordEnvironmentQualificationRestoreReceipt(t.Context(), claimed, restored.Execution, runtime.Inputs)
			if err != nil || receipt.RequestID != request.ID || receipt.Attempt != claimed.Attempt || receipt.CaptureInstanceID != frame.InstanceID ||
				receipt.InstanceID != restored.Execution.InstanceID || !receipt.Inputs.Boundary.Equal(runtime.Inputs.Boundary.UTC().Truncate(time.Microsecond)) {
				t.Fatalf("restore receipt: %+v %v", receipt, err)
			}
			if _, err := restoreReceipts.RecordEnvironmentQualificationRestoreReceipt(t.Context(), claimed, restored.Execution, state.RuntimeConfigInputs{}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("restore receipt replacement accepted: %v", err)
			}
			assertEvidence(i+1, i+1, i)
			smoke, err := smokeReceipts.RecordEnvironmentQualificationSmokeReceipt(t.Context(), claimed, smokeEvidence)
			if err != nil || smoke.RequestID != request.ID || smoke.Attempt != claimed.Attempt || smoke.GraphID != claimed.GraphID ||
				smoke.CaptureInstanceID != frame.InstanceID || smoke.InstanceID != restored.Execution.InstanceID || smoke.Resource != request.Resource ||
				smoke.PolicyID != smokeEvidence.PolicyID || smoke.PolicySHA256 != smokeEvidence.PolicySHA256 || smoke.ResultSHA256 != smokeEvidence.ResultSHA256 || smoke.RecordedAt.IsZero() {
				t.Fatalf("isolated smoke receipt: %+v %v", smoke, err)
			}
			if _, err := smokeReceipts.RecordEnvironmentQualificationSmokeReceipt(t.Context(), claimed, smokeEvidence); err != nil {
				t.Fatalf("idempotent isolated smoke receipt: %v", err)
			}
			changedEvidence := smokeEvidence
			changedEvidence.ResultSHA256 = strings.Repeat("c", 64)
			if _, err := smokeReceipts.RecordEnvironmentQualificationSmokeReceipt(t.Context(), claimed, changedEvidence); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("smoke receipt accepted a replacement result: %v", err)
			}
			assertEvidence(i+1, i+1, i+1)
		}
		for _, request := range requests {
			if err := basic.MarkDeploymentLive(t.Context(), request.DeploymentID); err == nil {
				t.Fatal("complete capture cohort released a held deployment")
			}
		}
		beforeActivation, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		release, activated, err := activationStore.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !activated || release.ID == "" || len(release.Members) != len(requests) {
			t.Fatalf("qualified cohort was not atomically activated: release=%+v activated=%t err=%v", release, activated, err)
		}
		activeSource, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil || activeSource.IntentVersion != beforeActivation.IntentVersion {
			t.Fatalf("GitOps activation changed its own reviewed intent version: before=%d after=%d err=%v",
				beforeActivation.IntentVersion, activeSource.IntentVersion, err)
		}
		assertPlanStillCurrent := func(operation string) {
			t.Helper()
			if _, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan); err != nil {
				t.Fatalf("rejected %s changed the reviewed plan: %v", operation, err)
			}
		}
		assertPlanStillCurrent("graph activation")
		for _, request := range requests {
			deployment, err := basic.(state.Store).DeploymentByID(t.Context(), request.DeploymentID)
			if err != nil || deployment.Status != state.DeployLive || deployment.EnvironmentWorkloadHeld() ||
				deployment.TrafficPercent != 0 || !deployment.TrafficPercentExplicit {
				t.Fatalf("activated candidate is not live, unheld and dark: deployment=%+v err=%v", deployment, err)
			}
			if err := basic.MarkDeploymentLive(t.Context(), request.DeploymentID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("ordinary promotion reclaimed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("ordinary promotion")
			if err := basic.(state.Store).UpdateDeploymentStatus(t.Context(), request.DeploymentID, state.DeploySuperseded, ""); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("status update superseded activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("status update")
			if _, err := basic.(state.Store).UpdateDeploymentMinInstances(t.Context(), request.DeploymentID, deployment.MinInstances+1); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("minimum-instance update changed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("minimum-instance update")
			if err := basic.(state.Store).SetDeploymentSourceURL(t.Context(), request.DeploymentID,
				"unapproved-source-"+request.DeploymentID, "unapproved-"+request.DeploymentID[:8]); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("source update changed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("source update")
			if _, err := basic.(state.Store).UpdateDeploymentTraffic(t.Context(), request.DeploymentID, 100); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("traffic update changed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("traffic update")
			if err := basic.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), request.DeploymentID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("dark promotion reclaimed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("dark promotion")
			if _, err := basic.(state.Store).PrepareDeploymentRollback(t.Context(), deployment.AppID, request.DeploymentID); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("rollback preparation reclaimed activated GitOps candidate: %v", err)
			}
			assertPlanStillCurrent("rollback preparation")
		}
		// Check current-plan evidence before creating an ordinary alternate,
		// which intentionally changes the deployment inventory.
		activeEvidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
		if err != nil || !activeEvidence.Qualified || !activeEvidence.Activated || activeEvidence.Serving ||
			slices.Contains(activeEvidence.BlockingReasons, "environment_activation_evidence_missing") ||
			!slices.Contains(activeEvidence.BlockingReasons, "environment_serving_evidence_missing") {
			t.Fatalf("activation and serving evidence were conflated: %+v %v", activeEvidence, err)
		}
		retry, activated, err := activationStore.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
		if err != nil || !activated || retry.ID != release.ID {
			t.Fatalf("activation retry changed release identity: first=%s retry=%s activated=%t err=%v", release.ID, retry.ID, activated, err)
		}
		if err := basic.SetDeploymentRootfs(t.Context(), requests[0].DeploymentID, "/changed.ext4", "changed", 4096); !errors.Is(err, state.ErrInvalidStateTransition) {
			t.Fatalf("activated candidate accepted a different image artifact: %v", err)
		}
		evidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
		if err != nil || evidence.CapturesRecorded != 2 || !evidence.Qualified || !evidence.Activated {
			t.Fatalf("rejected artifact mutation changed committed qualification: %+v %v", evidence, err)
		}
		alternate, err := basic.(state.Store).CreateDeployment(t.Context(), state.Deployment{AppID: requests[0].AppID, Scope: "production",
			Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("d", 64), TrafficPercent: 0, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := basic.MarkDeploymentLive(t.Context(), alternate.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ordinary promotion bypassed active GitOps ownership: %v", err)
		}
		if err := basic.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(t.Context(), alternate.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("dark promotion bypassed active GitOps ownership: %v", err)
		}
		if err := basic.(state.Store).UpdateDeploymentStatus(t.Context(), alternate.ID, state.DeployLive, ""); !errors.Is(err, state.ErrInvalidStateTransition) {
			t.Fatalf("direct status update bypassed active GitOps ownership: %v", err)
		}
		var fallback []state.ProjectReleaseMember
		for _, request := range requests {
			live, err := basic.(state.Store).LiveDeploymentForScope(t.Context(), request.AppID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if live.ID != "" && live.TrafficPercent > 0 {
				fallback = append(fallback, state.ProjectReleaseMember{AppID: request.AppID, DeploymentID: live.ID})
			}
		}
		replacement := append([]state.ProjectReleaseMember(nil), release.Members...)
		replaced := false
		for i := range replacement {
			if replacement[i].AppID == requests[0].AppID {
				for _, fallbackMember := range fallback {
					if fallbackMember.AppID == requests[0].AppID {
						replacement[i].DeploymentID = fallbackMember.DeploymentID
						replaced = true
					}
				}
			}
		}
		if !replaced {
			t.Fatal("activated release did not contain the prepared workload")
		}
		releaseMutations := basic.(state.ProjectReleaseSetPromotionStore)
		if _, err := releaseMutations.PublishProjectReleaseSetIfActive(t.Context(), lease.Source.AccountID, lease.Source.ProjectID,
			"production", release.ID, nil, release.TTLSeconds, replacement); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ordinary release publication replaced an activated GitOps member: %v", err)
		}
		if len(fallback) == 0 {
			t.Fatal("activation fixture did not retain a direct fallback route")
		}
		if _, err := basic.(state.Store).UpdateDeploymentTraffic(t.Context(), fallback[0].DeploymentID, 50); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ordinary traffic change modified a GitOps-managed app route: %v", err)
		}
		if err := releaseMutations.DeactivateProjectReleaseSetIfActive(t.Context(), lease.Source.AccountID, lease.Source.ProjectID,
			"production", release.ID, fallback); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("ordinary release deactivation removed the activated GitOps graph: %v", err)
		}
	})
}

func TestEnvironmentQualificationRestoreReceiptRequiresGuestConfigAcknowledgement(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		request, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(
			t.Context(), requests[0].ID, "scheduler", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		placement := qualificationPlacement(t, basic, 4096)
		admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(
			t.Context(), request, placement)
		if err != nil {
			t.Fatal(err)
		}
		frame := admission.Execution
		if err := basic.(state.EnvironmentQualificationExecutionStore).MarkEnvironmentQualificationDispatched(t.Context(), request, frame); err != nil {
			t.Fatal(err)
		}
		inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{},
			SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
		if _, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(
			t.Context(), request, state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID,
				Netns: "source", HostIP: "10.0.0.1", GuestUID: 20001, Inputs: inputs}); err != nil {
			t.Fatal(err)
		}
		proof := captureProof(frame)
		if _, err := basic.(state.EnvironmentQualificationSnapshotStore).RecordEnvironmentQualificationSnapshot(t.Context(), request, frame, proof); err != nil {
			t.Fatal(err)
		}
		sourceRetirement := state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: proof.CaptureID,
			NativeGeneration: proof.NativeGeneration, KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}
		if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), frame, sourceRetirement); err != nil {
			t.Fatal(err)
		}

		restorePlacement := placement
		restorePlacement.WakeID = uuid.NewString()
		restored, err := basic.(state.EnvironmentQualificationRestoreStore).CreateEnvironmentQualificationRestore(t.Context(), request, restorePlacement)
		if err != nil {
			t.Fatal(err)
		}
		if err := basic.(state.EnvironmentQualificationRestoreStore).MarkEnvironmentQualificationRestoreDispatched(t.Context(), request, restored.Execution); err != nil {
			t.Fatal(err)
		}
		inputs.Boundary = time.Now().UTC()
		if _, err := basic.(state.EnvironmentQualificationRestoreRuntimeStore).PublishEnvironmentQualificationRestoreRuntime(t.Context(), request,
			restored.Execution, state.EnvironmentWorkloadQualificationRuntime{NodeID: restorePlacement.NodeID, WakeID: restorePlacement.WakeID,
				Netns: "restore", HostIP: "10.0.0.2", GuestUID: 20002, Inputs: inputs}); err != nil {
			t.Fatal(err)
		}
		if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
			t.Context(), request, restored.Execution, strings.Repeat("b", 64)); err != nil {
			t.Fatal("target guest config acknowledgement", err)
		}
		targetRetirement := qualificationNativeProof()
		for targetRetirement.NativeGeneration == proof.NativeGeneration {
			targetRetirement.NativeGeneration = uuid.NewString()
		}
		targetRetirement.KernelBootID = proof.KernelBootID
		if targetRetirement.ReceiptID == proof.CaptureID {
			targetRetirement.ReceiptID = uuid.NewString()
		}
		if err := basic.(state.EnvironmentQualificationExecutionStore).RetireEnvironmentQualificationExecution(t.Context(), restored.Execution, targetRetirement); err != nil {
			t.Fatal(err)
		}
		if _, err := basic.(state.EnvironmentQualificationRestoreReceiptStore).RecordEnvironmentQualificationRestoreReceipt(
			t.Context(), request, restored.Execution, inputs); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("restore evidence accepted without source guest acknowledgement: %v", err)
		}
	})
}
