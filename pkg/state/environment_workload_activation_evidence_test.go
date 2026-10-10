package state

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGraphActivationEvidenceCountsRestoreWithoutCallingItSmoke(t *testing.T) {
	graphID, sourceID, environmentID, revisionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, requestID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	frame := EnvironmentQualificationExecution{InstanceID: uuid.NewString(), RequestID: requestID, GraphID: graphID, AppID: appID,
		DeploymentID: deploymentID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID, Resource: "workload/api",
		Scope: "production", PlanHash: "plan", Generation: 3, IntentVersion: 5, Attempt: 1}
	captureID := uuid.NewString()
	snapshot := Snapshot{StorageKey: SnapshotCaptureMemKey(deploymentID, SnapshotTierWarm, captureID)}
	proof := EnvironmentQualificationSnapshot{CaptureID: captureID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		FCVersion:  "1.7.0",
		StorageKey: snapshot.StorageKey, VMStateStorageKey: SnapshotVMStateKey(snapshot), DriveStorageKey: SnapshotDriveKey(snapshot),
		BackingStorageKey: SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048}
	inputs := RuntimeConfigInputs{Scope: "production", Boundary: time.Unix(0, 0).UTC(), Variables: map[string]string{},
		SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, SidecarSecretVersions: map[string]int64{}, AllSecrets: true}
	capture := EnvironmentQualificationSnapshotReceipt{Execution: frame, Snapshot: proof, Inputs: inputs}
	graph := EnvironmentWorkloadGraph{ID: graphID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID,
		Generation: 3, IntentVersion: 5, PlanHash: "plan", Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
			Resource: frame.Resource, AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: "request",
		}}}
	withoutRestore := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, nil, nil, nil, nil)
	if withoutRestore.CapturesRecorded != 1 || withoutRestore.RestoresRecorded != 0 ||
		!slices.Contains(withoutRestore.BlockingReasons, "environment_restore_evidence_missing") {
		t.Fatalf("capture incorrectly satisfied restore evidence: %+v", withoutRestore)
	}
	captureConfig := EnvironmentQualificationConfigReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID,
		InstanceID: frame.InstanceID, APIEnvSHA256: strings.Repeat("a", 64)}
	restore := EnvironmentQualificationRestoreReceipt{RequestID: requestID, Attempt: 1, CaptureInstanceID: frame.InstanceID,
		InstanceID: uuid.NewString(), Inputs: inputs}
	// Each target captures its own input boundary; matching values are enough
	// when both source and restore boundaries are independently fresh.
	restore.Inputs.Boundary = time.Now().UTC()
	withRestore := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: restore,
	}, nil, map[string]EnvironmentQualificationConfigReceipt{frame.InstanceID: captureConfig}, nil)
	if withRestore.CapturesRecorded != 1 || withRestore.RestoresRecorded != 1 ||
		withRestore.GuestConfigAcknowledgementsRecorded != 0 ||
		slices.Contains(withRestore.BlockingReasons, "environment_restore_evidence_missing") ||
		!slices.Contains(withRestore.BlockingReasons, "environment_guest_config_acknowledgement_missing") ||
		!slices.Contains(withRestore.BlockingReasons, "environment_framework_ready_evidence_missing") ||
		!slices.Contains(withRestore.BlockingReasons, "environment_isolated_smoke_evidence_missing") || withRestore.Qualified || withRestore.Activated || withRestore.Serving {
		t.Fatalf("restore receipt was promoted to smoke or activation: %+v", withRestore)
	}
	restoreConfig := EnvironmentQualificationConfigReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID,
		InstanceID: restore.InstanceID, CaptureInstanceID: frame.InstanceID, APIEnvSHA256: strings.Repeat("b", 64)}
	configs := map[string]EnvironmentQualificationConfigReceipt{frame.InstanceID: captureConfig, restore.InstanceID: restoreConfig}
	smoke := EnvironmentQualificationSmokeReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID,
		CaptureInstanceID: frame.InstanceID, InstanceID: restore.InstanceID, Resource: frame.Resource, PolicyID: "http-healthz-v1",
		PolicySHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("b", 64), RecordedAt: time.Now().UTC()}
	withSmoke := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: restore,
	}, map[string]EnvironmentQualificationSmokeReceipt{frame.InstanceID: smoke}, configs, nil)
	if withSmoke.GuestConfigAcknowledgementsRecorded != 1 || withSmoke.SmokesRecorded != 1 ||
		slices.Contains(withSmoke.BlockingReasons, "environment_guest_config_acknowledgement_missing") ||
		slices.Contains(withSmoke.BlockingReasons, "environment_isolated_smoke_evidence_missing") ||
		withSmoke.Qualified || withSmoke.Activated || withSmoke.Serving {
		t.Fatalf("successful isolated smoke did not clear only its own evidence blocker: %+v", withSmoke)
	}
	ready := EnvironmentQualificationFrameworkReadyReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID,
		CaptureInstanceID: frame.InstanceID, InstanceID: restore.InstanceID, Runtime: "node22", WarmupMS: 42, RecordedAt: time.Now().UTC()}
	withReady := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: restore,
	}, map[string]EnvironmentQualificationSmokeReceipt{frame.InstanceID: smoke}, configs,
		map[string]EnvironmentQualificationFrameworkReadyReceipt{frame.InstanceID: ready})
	if !withReady.Qualified || withReady.Activated || withReady.Serving || withReady.FrameworkReadyAcknowledgementsRecorded != 1 ||
		!slices.Contains(withReady.BlockingReasons, "environment_activation_evidence_missing") ||
		!slices.Contains(withReady.BlockingReasons, "environment_serving_evidence_missing") {
		t.Fatalf("complete qualification evidence should qualify while activation and serving stay gated: %+v", withReady)
	}
	wrongReady := ready
	wrongReady.InstanceID = frame.InstanceID
	wrongReadyEvidence := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: restore,
	}, map[string]EnvironmentQualificationSmokeReceipt{frame.InstanceID: smoke}, configs,
		map[string]EnvironmentQualificationFrameworkReadyReceipt{frame.InstanceID: wrongReady})
	if wrongReadyEvidence.Qualified || wrongReadyEvidence.FrameworkReadyAcknowledgementsRecorded != 0 ||
		!slices.Contains(wrongReadyEvidence.BlockingReasons, "environment_framework_ready_evidence_missing") {
		t.Fatalf("framework-ready event from the capture instance satisfied restored readiness: %+v", wrongReadyEvidence)
	}
	wrongSmoke := smoke
	wrongSmoke.InstanceID = frame.InstanceID
	wrongSmokeEvidence := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: restore,
	}, map[string]EnvironmentQualificationSmokeReceipt{frame.InstanceID: wrongSmoke}, configs, map[string]EnvironmentQualificationFrameworkReadyReceipt{frame.InstanceID: ready})
	if wrongSmokeEvidence.SmokesRecorded != 0 || !slices.Contains(wrongSmokeEvidence.BlockingReasons, "environment_isolated_smoke_evidence_missing") {
		t.Fatalf("smoke from the capture instance satisfied restored smoke: %+v", wrongSmokeEvidence)
	}
	wrongRestore := restore
	wrongRestore.Attempt++
	if qualificationSmokeReceiptMatchesRequest(smoke, EnvironmentWorkloadQualificationRequest{ID: requestID, GraphID: graphID,
		ReservedInstanceID: frame.InstanceID, Attempt: 1}, wrongRestore) {
		t.Fatal("smoke receipt accepted a restore receipt from another attempt")
	}
	stale := restore
	stale.Inputs = cloneRuntimeConfigInputs(inputs)
	stale.Inputs.Variables = map[string]string{"MODE": "stale"}
	staleEvidence := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{
		frame.InstanceID: stale,
	}, nil, configs, map[string]EnvironmentQualificationFrameworkReadyReceipt{frame.InstanceID: ready})
	if staleEvidence.RestoresRecorded != 0 || !slices.Contains(staleEvidence.BlockingReasons, "environment_restore_evidence_missing") {
		t.Fatalf("stale restore inputs satisfied the graph: %+v", staleEvidence)
	}
}

func TestGraphActivationEvidenceNamesUnsupportedExecutionModeSmokeBlocker(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wantBlocker string
		jobSmokeConfigured      bool
	}{
		{name: "worker", mode: "worker", wantBlocker: "environment_worker_queue_smoke_contract_missing"},
		{name: "job without reviewed contract", mode: "job", wantBlocker: "environment_job_execution_smoke_contract_missing"},
		{name: "job with reviewed contract", mode: "job", jobSmokeConfigured: true, wantBlocker: "environment_job_execution_smoke_evidence_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
				Resource: "workload/processor", AppID: uuid.NewString(), CandidateDeploymentID: uuid.NewString(), ExecutionMode: tc.mode,
				JobSmokeConfigured: tc.jobSmokeConfigured,
			}}}
			evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
			if !slices.Contains(evidence.BlockingReasons, tc.wantBlocker) || evidence.Qualified {
				t.Fatalf("%s candidate did not expose its isolated execution blocker: %+v", tc.name, evidence)
			}
		})
	}
}

func TestGraphActivationEvidenceRequiresFunctionQueueSmokeReceipt(t *testing.T) {
	enabled := true
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/transform", AppID: uuid.NewString(), CandidateDeploymentID: uuid.NewString(),
		ExecutionMode: "request", Function: true, QueueModes: map[string]string{"events": "push"},
		QueueBindings: map[string]EnvironmentScopedQueueBinding{"events": {
			BindingID: uuid.NewString(), TriggerID: uuid.NewString(), Contract: api.EnvironmentQueueBinding{
				QueueName: "events", Mode: "push", WorkloadClass: "http", Enabled: &enabled,
			},
		}},
	}}}
	evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
	if evidence.Qualified || slices.Contains(evidence.BlockingReasons, "environment_queue_binding_execution_unsupported") ||
		!slices.Contains(evidence.BlockingReasons, "environment_function_queue_smoke_evidence_missing") ||
		slices.Contains(evidence.BlockingReasons, "environment_function_queue_consumer_adapter_missing") ||
		slices.Contains(evidence.BlockingReasons, "environment_workload_production_execution_adapter_missing") {
		t.Fatalf("function queue receipt gate is incorrect: %+v", evidence)
	}
}

func TestGraphActivationEvidenceRejectsUnsupportedPersistedModes(t *testing.T) {
	for _, tc := range []struct {
		name, executionMode, queueMode, wantBlocker string
	}{
		{name: "execution", executionMode: "future-mode", wantBlocker: "environment_workload_execution_mode_unsupported"},
		{name: "queue", executionMode: "worker", queueMode: "future-mode", wantBlocker: "environment_queue_binding_mode_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			member := EnvironmentWorkloadGraphMember{Resource: "workload/api", AppID: uuid.NewString(),
				CandidateDeploymentID: uuid.NewString(), ExecutionMode: tc.executionMode}
			if tc.queueMode != "" {
				member.QueueModes = map[string]string{"events": tc.queueMode}
			}
			graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{member}}
			evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
			if evidence.Qualified || !slices.Contains(evidence.BlockingReasons, tc.wantBlocker) {
				t.Fatalf("unsupported persisted mode %q qualified: %+v", tc.name, evidence)
			}
		})
	}
}

func TestGraphActivationEvidenceRejectsEmptyPreparedGraph(t *testing.T) {
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared"}
	evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
	if evidence.Qualified || evidence.Candidates != 0 ||
		!slices.Contains(evidence.BlockingReasons, "environment_workload_graph_empty") {
		t.Fatalf("empty prepared graph was treated as a qualified workload cohort: %+v", evidence)
	}
}

func TestGraphActivationEvidenceRequiresExactActiveRetainedTarget(t *testing.T) {
	projectID, environmentID, sourceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &MemStore{
		activeProjectReleaseSets: map[string]string{releaseKey(projectID, "production"): releaseID},
		projectReleaseSets: map[string]ProjectReleaseSet{releaseID: {
			ID: releaseID, ProjectID: projectID, EnvironmentSlug: "production", Active: true,
			Members: []ProjectReleaseMember{{AppID: appID, DeploymentID: deploymentID}},
		}},
		deployments: map[string]Deployment{deploymentID: {
			ID: deploymentID, AppID: appID, Scope: "production", Status: DeployLive,
		}},
	}
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), SourceID: sourceID, EnvironmentID: environmentID,
		Phase: "prepared", ResourceIDs: map[string]string{"workload/api": appID}, Members: []EnvironmentWorkloadGraphMember{{
			Resource: "workload/api", AppID: appID, ExecutionMode: "worker", QueueModes: map[string]string{"orders": "pull"},
			QueueBindingsConfigured: true, RetainedDeployments: []string{deploymentID},
		}}}
	source := EnvironmentGitSource{ProjectID: projectID, EnvironmentSlug: "production"}
	activeTargets := store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	evidence := graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets)
	if !evidence.Qualified || evidence.RetainedWorkloadsRecorded != 1 || evidence.Candidates != 0 ||
		!evidence.Activated || evidence.Serving ||
		slices.Contains(evidence.BlockingReasons, "environment_activation_before_qualification") ||
		slices.Contains(evidence.BlockingReasons, "environment_retained_workload_evidence_missing") {
		t.Fatalf("active retained member was not recorded as qualified: %+v", evidence)
	}

	graph.Members[0].ExecutionMode = "job"
	graph.Members[0].QueueModes = nil
	graph.Members[0].QueueBindingsConfigured = false
	graph.Members[0].JobSmokeConfigured = true
	activeTargets = store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	evidence = graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets)
	if evidence.Qualified || !slices.Contains(evidence.BlockingReasons, "environment_job_execution_smoke_evidence_missing") {
		t.Fatalf("active retained membership waived a job execution receipt: %+v", evidence)
	}

	graph.Members[0].ExecutionMode = "worker"
	graph.Members[0].JobSmokeConfigured = false
	graph.ResourceIDs["workload/api"] = uuid.NewString()
	activeTargets = store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	if evidence := graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets); evidence.RetainedWorkloadsRecorded != 0 ||
		!slices.Contains(evidence.BlockingReasons, "environment_retained_workload_evidence_missing") {
		t.Fatalf("active target for a different mapped app counted as retained proof: %+v", evidence)
	}
	graph.ResourceIDs["workload/api"] = appID
	graph.Members[0].RetainedDeployments = nil
	activeTargets = store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	evidence = graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets)
	if evidence.Qualified || evidence.RetainedWorkloadsRecorded != 0 ||
		!slices.Contains(evidence.BlockingReasons, "environment_retained_workload_evidence_missing") {
		t.Fatalf("active release target absent from the frozen retained set was accepted: %+v", evidence)
	}

	store.deployments[deploymentID] = Deployment{ID: deploymentID, AppID: appID, Scope: "production", Status: DeployLive,
		EnvironmentWorkloadRuntime: `{"held":true}`}
	graph.Members[0].RetainedDeployments = []string{deploymentID}
	activeTargets = store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	if len(activeTargets) != 0 {
		t.Fatalf("held deployment in an active release set counted as active proof: %v", activeTargets)
	}
}

func TestGraphActivationEvidenceRecognizesExactActiveCandidateWithoutCallingItServing(t *testing.T) {
	projectID, environmentID, sourceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &MemStore{
		activeProjectReleaseSets: map[string]string{releaseKey(projectID, "production"): releaseID},
		projectReleaseSets: map[string]ProjectReleaseSet{releaseID: {
			ID: releaseID, ProjectID: projectID, EnvironmentSlug: "production", Active: true,
			Members: []ProjectReleaseMember{{AppID: appID, DeploymentID: deploymentID}},
		}},
		deployments: map[string]Deployment{deploymentID: {
			ID: deploymentID, AppID: appID, Scope: "production", Status: DeployLive,
		}},
	}
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), SourceID: sourceID, EnvironmentID: environmentID,
		Phase: "prepared", ResourceIDs: map[string]string{"workload/api": appID}, Members: []EnvironmentWorkloadGraphMember{{
			Resource: "workload/api", AppID: appID, CandidateDeploymentID: deploymentID,
			ExecutionMode: "request", RetainedDeployments: []string{},
		}}}
	source := EnvironmentGitSource{ProjectID: projectID, EnvironmentSlug: "production"}
	activeTargets := store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	evidence := graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets)
	if !evidence.Activated || evidence.Serving || evidence.Qualified ||
		!slices.Contains(evidence.BlockingReasons, "environment_capture_evidence_missing") ||
		!slices.Contains(evidence.BlockingReasons, "environment_activation_before_qualification") ||
		slices.Contains(evidence.BlockingReasons, "environment_activation_evidence_missing") ||
		!slices.Contains(evidence.BlockingReasons, "environment_serving_evidence_missing") {
		t.Fatalf("release-set activation was conflated with qualification or serving: %+v", evidence)
	}

	store.projectReleaseSets[releaseID] = ProjectReleaseSet{ID: releaseID, ProjectID: projectID, EnvironmentSlug: "production", Active: true,
		Members: []ProjectReleaseMember{{AppID: appID, DeploymentID: uuid.NewString()}},
	}
	activeTargets = store.environmentGraphActiveReleaseTargetsLocked(source, graph)
	evidence = graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, nil, nil, nil, activeTargets)
	if evidence.Activated || !slices.Contains(evidence.BlockingReasons, "environment_activation_evidence_missing") {
		t.Fatalf("different active candidate was accepted as graph activation: %+v", evidence)
	}
}

func TestGraphActivationEvidenceBlocksJobWithDisabledQueueBinding(t *testing.T) {
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/report", AppID: uuid.NewString(), CandidateDeploymentID: uuid.NewString(),
		ExecutionMode: "job", JobSmokeConfigured: true, QueueBindingsConfigured: true,
	}}}
	evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
	if evidence.Qualified || !slices.Contains(evidence.BlockingReasons, "environment_job_queue_binding_execution_unsupported") {
		t.Fatalf("job with a disabled queue binding did not fail closed with a precise blocker: %+v", evidence)
	}
}

func TestGraphActivationEvidenceCountsRetiredJobSmokeWithoutRestore(t *testing.T) {
	request := qualificationJobSmokeRequest()
	request.ID, request.GraphID, request.DeploymentID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	request.Attempt, request.ReservedInstanceID = 2, uuid.NewString()
	request.FrozenInputs.AppID = request.AppID
	evidence, err := NewEnvironmentQualificationJobSmokeEvidence(request, request.ReservedInstanceID, 0, "succeeded", 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt := EnvironmentQualificationJobSmokeReceipt{RequestID: request.ID, Attempt: request.Attempt, GraphID: request.GraphID,
		InstanceID: evidence.InstanceID, Resource: evidence.Resource, PolicyID: evidence.PolicyID,
		PolicySHA256: evidence.PolicySHA256, ResultSHA256: evidence.ResultSHA256, RecordedAt: time.Now().UTC()}
	config := EnvironmentQualificationConfigReceipt{RequestID: request.ID, Attempt: request.Attempt, GraphID: request.GraphID,
		InstanceID: request.ReservedInstanceID, APIEnvSHA256: strings.Repeat("a", 64), RecordedAt: time.Now().UTC()}
	graph := EnvironmentWorkloadGraph{ID: request.GraphID, Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: request.Resource, AppID: request.AppID, CandidateDeploymentID: request.DeploymentID,
		ExecutionMode: "job", JobSmokeConfigured: true,
	}}}
	withoutReceipt := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
	if withoutReceipt.Qualified || !slices.Contains(withoutReceipt.BlockingReasons, "environment_job_execution_smoke_evidence_missing") {
		t.Fatalf("job candidate qualified without a committed exit receipt: %+v", withoutReceipt)
	}
	qualified := graphActivationEvidence(graph, nil, nil, nil, map[string]EnvironmentQualificationConfigReceipt{request.ReservedInstanceID: config}, nil,
		map[string]EnvironmentQualificationJobSmokeReceipt{request.DeploymentID: receipt})
	if !qualified.Qualified || qualified.JobSmokesRecorded != 1 || qualified.SmokesRecorded != 1 ||
		qualified.GuestConfigAcknowledgementsRecorded != 1 || qualified.CapturesRecorded != 0 || qualified.RestoresRecorded != 0 || qualified.Activated || qualified.Serving ||
		slices.Contains(qualified.BlockingReasons, "environment_job_execution_smoke_evidence_missing") ||
		!slices.Contains(qualified.BlockingReasons, "environment_job_production_adapter_missing") ||
		slices.Contains(qualified.BlockingReasons, "environment_scheduled_job_dispatch_adapter_missing") {
		t.Fatalf("retired job receipt did not satisfy only job smoke evidence: %+v", qualified)
	}
}

func TestGraphActivationEvidenceNamesScheduledJobAdapterBlocker(t *testing.T) {
	graph := EnvironmentWorkloadGraph{Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/report", AppID: uuid.NewString(), ExecutionMode: api.ExecutionModeJob, ScheduleConfigured: true,
	}}}
	blockers := environmentGraphProductionAdapterBlockers(graph)
	if !slices.Contains(blockers, "environment_scheduled_job_dispatch_adapter_missing") ||
		slices.Contains(blockers, "environment_job_production_adapter_missing") {
		t.Fatalf("scheduled job got an inaccurate production blocker: %v", blockers)
	}
}

func TestGraphActivationEvidencePullOnlyWorkerRequiresSmokeButHasServingAdapter(t *testing.T) {
	enabled := true
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
		Resource: "workload/processor", AppID: uuid.NewString(), CandidateDeploymentID: uuid.NewString(), ExecutionMode: "worker",
		QueueModes: map[string]string{"orders": "pull"}, QueueBindings: map[string]EnvironmentScopedQueueBinding{
			"orders": {BindingID: uuid.NewString(), Contract: api.EnvironmentQueueBinding{
				QueueName: "orders", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled,
			}},
		},
	}}}
	evidence := graphActivationEvidence(graph, nil, nil, nil, nil, nil)
	if !slices.Contains(evidence.BlockingReasons, "environment_worker_queue_smoke_contract_missing") ||
		slices.Contains(evidence.BlockingReasons, "environment_workload_production_execution_adapter_missing") ||
		slices.Contains(evidence.BlockingReasons, "environment_pull_queue_consumer_adapter_missing") {
		t.Fatalf("pull-only worker reported the wrong qualification/production blockers: %+v", evidence.BlockingReasons)
	}
}

func TestGraphActivationEvidenceCountsPushWorkerQueueSmokeReceipt(t *testing.T) {
	graphID, sourceID, environmentID, revisionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, requestID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	instanceID, restoredID := uuid.NewString(), uuid.NewString()
	frame := EnvironmentQualificationExecution{InstanceID: instanceID, RequestID: requestID, GraphID: graphID, AppID: appID,
		DeploymentID: deploymentID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID, Resource: "workload/worker",
		Scope: "production", PlanHash: strings.Repeat("a", 64), Generation: 3, IntentVersion: 5, Attempt: 1}
	captureID := uuid.NewString()
	snapshot := Snapshot{StorageKey: SnapshotCaptureMemKey(deploymentID, SnapshotTierWarm, captureID)}
	proof := EnvironmentQualificationSnapshot{CaptureID: captureID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		FCVersion: "1.7.0", StorageKey: snapshot.StorageKey, VMStateStorageKey: SnapshotVMStateKey(snapshot), DriveStorageKey: SnapshotDriveKey(snapshot),
		BackingStorageKey: SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048}
	inputs := RuntimeConfigInputs{Scope: "production", Boundary: time.Unix(0, 0).UTC(), Variables: map[string]string{},
		SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, SidecarSecretVersions: map[string]int64{}, AllSecrets: true}
	capture := EnvironmentQualificationSnapshotReceipt{Execution: frame, Snapshot: proof, Inputs: inputs}
	restore := EnvironmentQualificationRestoreReceipt{RequestID: requestID, Attempt: 1, CaptureInstanceID: instanceID, InstanceID: restoredID, Inputs: inputs}
	smoke := EnvironmentQualificationSmokeReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID, CaptureInstanceID: instanceID,
		InstanceID: restoredID, Resource: frame.Resource, PolicyID: environmentQualificationWorkerQueuePolicyID,
		PolicySHA256: strings.Repeat("b", 64), ResultSHA256: strings.Repeat("c", 64), RecordedAt: time.Now().UTC()}
	graph := EnvironmentWorkloadGraph{ID: graphID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID,
		Generation: 3, IntentVersion: 5, PlanHash: frame.PlanHash, Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{{
			Resource: frame.Resource, AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: "worker",
			QueueModes: map[string]string{"orders": "push", "audit": "pull"},
		}}}
	evidence := graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture},
		map[string]EnvironmentQualificationRestoreReceipt{instanceID: restore}, map[string]EnvironmentQualificationSmokeReceipt{instanceID: smoke}, nil, nil)
	if evidence.SmokesRecorded != 1 || slices.Contains(evidence.BlockingReasons, "environment_worker_queue_smoke_contract_missing") ||
		!slices.Contains(evidence.BlockingReasons, "environment_workload_production_execution_adapter_missing") {
		t.Fatalf("isolated mixed-mode worker smoke and production adapter blockers were not separated: %+v", evidence)
	}
}
