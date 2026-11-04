package state

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func TestEnvironmentGitOpsCandidateInputsIgnoreActivatedDeployments(t *testing.T) {
	src := &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/app@sha256:" + strings.Repeat("a", 64)}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: "api", Source: src}}})
	if err != nil {
		t.Fatal(err)
	}
	source := EnvironmentGitSource{ID: uuid.NewString(), EnvironmentID: uuid.NewString(), EnvironmentSlug: "production", Generation: 1, Spec: EnvironmentGitSourceSpec{Mode: "enforce"}}
	revision := EnvironmentDesiredRevision{ID: uuid.NewString(), CommitSHA: strings.Repeat("b", 40), Digest: desired.Digest}
	snapshot := gitOpsIntentSnapshot{SourceID: source.ID, EnvironmentID: source.EnvironmentID, Environment: "production", Project: "shop", Version: 1, Plan: api.PlanPro,
		Resources: []gitOpsIntentResource{{Resource: "workload/api", AppID: "app"}},
		Owners:    []environmentsync.Ownership{{Field: environmentsync.Field{Resource: "workload/api", Path: "source"}, Manager: source.ID}},
		Apps: []gitOpsIntentApp{{ID: "app", Slug: "api", Type: AppTypeApp, WorkloadClass: WorkloadClassHTTP, WorkloadIntent: &EnvironmentWorkloadIntent{Source: src},
			Sources: []gitOpsSourceBaseline{{ID: "previous-release", Kind: DeploymentKindImage, Image: src.Image, Managed: true, PlanHash: strings.Repeat("d", 64), Inputs: EnvironmentWorkloadDeploymentInputs{MinInstances: 1}}}}}}
	plan := environmentsync.Plan{Hash: strings.Repeat("c", 64)}
	before, err := workloadCandidateInputs(source, revision, desired, snapshot, plan, nil)
	if err != nil || len(before) != 1 {
		t.Fatalf("before: %v %v", before, err)
	}
	frozen := candidateFrozenInputs(before[0])
	if frozen.DeploymentInputs == nil || frozen.DeploymentInputs.MinInstances != 1 || !slices.Equal(frozen.SourceDeployments, []string{"previous-release"}) {
		t.Fatalf("previous release's inherited inputs were lost: %+v", frozen)
	}
	// The observation includes both the previous release and its replacement
	// after activation, but the replacement cannot inherit from itself.
	snapshot.Apps[0].Sources = append(snapshot.Apps[0].Sources, gitOpsSourceBaseline{ID: "activated-candidate", Kind: DeploymentKindImage, Image: src.Image, Managed: true, PlanHash: plan.Hash})
	after, err := workloadCandidateInputs(source, revision, desired, snapshot, plan, nil)
	if err != nil || len(after) != 1 {
		t.Fatalf("after: %v %v", after, err)
	}
	if !frozenCandidateInputsMatch([]byte(before[0].EnvironmentWorkloadRuntime), candidateFrozenInputs(after[0])) {
		t.Error("newly activated managed deployment invalidates its own frozen candidate inputs")
	}
}

func TestEnvironmentGitOpsPreparationIncludesActivatedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []EnvironmentWorkloadCandidate
		phase      string
	}{
		{"prepared", []EnvironmentWorkloadCandidate{{Status: DeploySnapshotting, HasRootfs: true}}, "prepared"},
		{"activated", []EnvironmentWorkloadCandidate{{Status: DeployLive, HasRootfs: true}}, "prepared"},
		{"mixed activation", []EnvironmentWorkloadCandidate{{Status: DeployLive, HasRootfs: true}, {Status: DeploySnapshotting, HasRootfs: true}}, "prepared"},
		{"missing artifact", []EnvironmentWorkloadCandidate{{Status: DeployLive}}, "preparing"},
		{"pending build", []EnvironmentWorkloadCandidate{{Status: DeployBuilding}}, "preparing"},
		{"failed member", []EnvironmentWorkloadCandidate{{Status: DeployLive, HasRootfs: true}, {Status: DeployFailed}}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phase, code := preparationGraphPhase(tc.candidates)
			if phase != tc.phase || (code != "") != (tc.phase == "failed") {
				t.Fatalf("preparation = %q %q, want %q", phase, code, tc.phase)
			}
		})
	}
}

func TestGraphActivationEvidenceMixedJobAndHTTPConfigReceipts(t *testing.T) {
	graphID, sourceID, environmentID, revisionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	appID, deploymentID, requestID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	frame := EnvironmentQualificationExecution{InstanceID: uuid.NewString(), RequestID: requestID, GraphID: graphID, AppID: appID,
		DeploymentID: deploymentID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID, Resource: "workload/api",
		Scope: "production", PlanHash: "plan", Generation: 3, IntentVersion: 5, Attempt: 1}
	captureID := uuid.NewString()
	snapshot := Snapshot{StorageKey: SnapshotCaptureMemKey(deploymentID, SnapshotTierWarm, captureID)}
	proof := EnvironmentQualificationSnapshot{CaptureID: captureID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), FCVersion: "1.7.0",
		StorageKey: snapshot.StorageKey, VMStateStorageKey: SnapshotVMStateKey(snapshot), DriveStorageKey: SnapshotDriveKey(snapshot), BackingStorageKey: SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 128, StoredBytes: 2048}
	inputs := RuntimeConfigInputs{Scope: "production", Boundary: time.Unix(0, 0).UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, SidecarSecretVersions: map[string]int64{}, AllSecrets: true}
	capture := EnvironmentQualificationSnapshotReceipt{Execution: frame, Snapshot: proof, Inputs: inputs}
	restore := EnvironmentQualificationRestoreReceipt{RequestID: requestID, Attempt: 1, CaptureInstanceID: frame.InstanceID, InstanceID: uuid.NewString(), Inputs: inputs}
	smoke := EnvironmentQualificationSmokeReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID, CaptureInstanceID: frame.InstanceID, InstanceID: restore.InstanceID, Resource: frame.Resource, PolicyID: "http-healthz-v1", PolicySHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("b", 64)}
	ready := EnvironmentQualificationFrameworkReadyReceipt{RequestID: requestID, Attempt: 1, GraphID: graphID, CaptureInstanceID: frame.InstanceID, InstanceID: restore.InstanceID, Runtime: "node22", RecordedAt: time.Now().UTC()}
	jobID, jobDepID, jobInstance, jobRequest := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	job := EnvironmentQualificationJobSmokeReceipt{RequestID: jobRequest, Attempt: 1, GraphID: graphID, InstanceID: jobInstance, Resource: "workload/report", PolicyID: environmentQualificationJobSmokePolicyID, PolicySHA256: strings.Repeat("a", 64), ResultSHA256: qualificationJobSmokeResultSHA256(0, "succeeded", 0)}
	graph := EnvironmentWorkloadGraph{ID: graphID, SourceID: sourceID, EnvironmentID: environmentID, RevisionID: revisionID, Generation: 3, IntentVersion: 5, PlanHash: "plan", Phase: "prepared", Members: []EnvironmentWorkloadGraphMember{
		{Resource: frame.Resource, AppID: appID, CandidateDeploymentID: deploymentID, ExecutionMode: "request"},
		{Resource: job.Resource, AppID: jobID, CandidateDeploymentID: jobDepID, ExecutionMode: "job", JobSmokeConfigured: true, ScheduleConfigured: true, JobID: uuid.NewString()},
	}}
	configs := map[string]EnvironmentQualificationConfigReceipt{
		frame.InstanceID:   {RequestID: requestID, Attempt: 1, GraphID: graphID, InstanceID: frame.InstanceID, APIEnvSHA256: strings.Repeat("a", 64)},
		restore.InstanceID: {RequestID: requestID, Attempt: 1, GraphID: graphID, InstanceID: restore.InstanceID, CaptureInstanceID: frame.InstanceID, APIEnvSHA256: strings.Repeat("b", 64)},
		jobInstance:        {RequestID: jobRequest, Attempt: 1, GraphID: graphID, InstanceID: jobInstance, APIEnvSHA256: strings.Repeat("c", 64)},
	}
	assess := func() EnvironmentWorkloadActivationEvidence {
		return graphActivationEvidence(graph, map[string]EnvironmentQualificationSnapshotReceipt{deploymentID: capture}, map[string]EnvironmentQualificationRestoreReceipt{frame.InstanceID: restore}, map[string]EnvironmentQualificationSmokeReceipt{frame.InstanceID: smoke}, configs, map[string]EnvironmentQualificationFrameworkReadyReceipt{frame.InstanceID: ready}, map[string]EnvironmentQualificationJobSmokeReceipt{jobDepID: job})
	}
	complete := assess()
	if !complete.Qualified {
		encoded, _ := json.Marshal(complete)
		t.Errorf("complete API + Job graph incorrectly blocked: %s", encoded)
	}
	for _, instanceID := range []string{frame.InstanceID, restore.InstanceID, jobInstance} {
		t.Run("missing config/"+instanceID, func(t *testing.T) {
			config := configs[instanceID]
			delete(configs, instanceID)
			missing := assess()
			configs[instanceID] = config
			if missing.Qualified || !slices.Contains(missing.BlockingReasons, "environment_guest_config_acknowledgement_missing") {
				t.Errorf("missing config receipt accepted: qualified=%v configs=%d blockers=%v", missing.Qualified, missing.GuestConfigAcknowledgementsRecorded, missing.BlockingReasons)
			}
		})
	}
}

func TestMemStoreEnvironmentGitOpsServiceBindingSurvivesReconciliationPause(t *testing.T) {
	for _, mode := range []string{"suspended", "report"} {
		t.Run(mode, func(t *testing.T) {
			store, caller, dep, _, _, _ := environmentGitOpsServiceBindingFixture(t, true)
			frozen := candidateFrozenInputs(dep)
			if _, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(t.Context(), caller.ID, dep.ID, "database"); err != nil || !managed || !found {
				t.Fatalf("initial route: managed=%v found=%v err=%v", managed, found, err)
			}
			update := EnvironmentGitSourceUpdate{}
			if mode == "suspended" {
				value := true
				update.Suspended = &value
			} else {
				update.Mode = "report"
			}
			if _, err := store.UpdateEnvironmentGitSource(t.Context(), caller.AccountID, frozen.SourceID, update); err != nil {
				t.Fatal(err)
			}
			if _, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(t.Context(), caller.ID, dep.ID, "database"); err != nil || !managed || !found {
				t.Errorf("reconciliation control cuts off unchanged live service route: managed=%v found=%v err=%v", managed, found, err)
			}
			// Pausing the controller cannot make a withdrawn release routable.
			releaseID := store.activeProjectReleaseSets[releaseKey(caller.ProjectID, dep.Scope)]
			release := store.projectReleaseSets[releaseID]
			release.Active = false
			store.projectReleaseSets[releaseID] = release
			if _, managed, found, err := store.ResolveEnvironmentGitOpsServiceBinding(t.Context(), caller.ID, dep.ID, "database"); err == nil || !managed || found {
				t.Fatalf("withdrawn release resolved after pause: managed=%v found=%v err=%v", managed, found, err)
			}
		})
	}
}
