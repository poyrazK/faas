package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationJobBootStub struct {
	spec         JobVmmSpec
	result       JobVmmResult
	err          error
	releaseCalls int
	releaseCheck func(context.Context, JobStartSpec) error
}

func (s *qualificationJobBootStub) JobColdBoot(_ context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	s.spec = spec
	if s.result.InstanceID == "" {
		s.result = JobVmmResult{InstanceID: spec.InstanceID, NodeID: spec.NodeID, Netns: "ns-job", HostIP: "10.0.0.2", GuestUID: 1000, StartHeld: true}
	}
	return s.result, s.err
}

func (s *qualificationJobBootStub) ReleaseJobStart(ctx context.Context, spec JobStartSpec) error {
	s.releaseCalls++
	if s.releaseCheck != nil {
		return s.releaseCheck(ctx, spec)
	}
	return nil
}

type qualificationJobExitStub struct {
	spec   JobExitSpec
	result JobExitResult
	err    error
}

type qualificationJobReceiptReader struct {
	receipt state.EnvironmentQualificationJobSmokeReceipt
}

func (r qualificationJobReceiptReader) RecordEnvironmentQualificationJobSmokeReceipt(context.Context,
	state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationJobSmokeEvidence) (state.EnvironmentQualificationJobSmokeReceipt, error) {
	return state.EnvironmentQualificationJobSmokeReceipt{}, errors.New("not implemented")
}

func (r qualificationJobReceiptReader) EnvironmentQualificationJobSmokeReceipt(_ context.Context, requestID string,
	attempt int64) (state.EnvironmentQualificationJobSmokeReceipt, error) {
	if requestID != r.receipt.RequestID || attempt != r.receipt.Attempt {
		return state.EnvironmentQualificationJobSmokeReceipt{}, state.ErrNotFound
	}
	return r.receipt, nil
}

func (s *qualificationJobExitStub) WaitJobExit(_ context.Context, spec JobExitSpec) (JobExitResult, error) {
	s.spec = spec
	if s.result.LeaseToken == "" {
		s.result.LeaseToken = spec.LeaseToken
	}
	return s.result, s.err
}

func qualificationJobRuntimeFixture() (state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationExecution) {
	request := state.EnvironmentWorkloadQualificationRequest{ID: uuid.NewString(), GraphID: uuid.NewString(), DeploymentID: uuid.NewString(),
		AppID: uuid.NewString(), Resource: "workload/function", Artifact: state.EnvironmentWorkloadArtifact{RootfsKey: "images/function.ext4",
			RootfsBytes: 1024, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage}, ExecutionMode: api.ExecutionModeJob,
		Attempt: 2, ReservedInstanceID: uuid.NewString(), FrozenInputs: state.EnvironmentWorkloadRuntime{AppID: "", Resource: "workload/function",
			JobSmoke: &api.EnvironmentJobSmoke{Command: []string{"node", "smoke.js"}, TimeoutSeconds: 15}}}
	request.FrozenInputs.AppID = request.AppID
	frame := state.EnvironmentQualificationExecution{InstanceID: request.ReservedInstanceID, RequestID: request.ID, GraphID: request.GraphID,
		AppID: request.AppID, DeploymentID: request.DeploymentID, NodeID: uuid.NewString(), WakeID: uuid.NewString(),
		SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Resource: request.Resource,
		Scope: "production", PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 2,
		IntentVersion: 1, Attempt: request.Attempt, RAMMB: 256, Artifact: request.Artifact, CleanupToken: uuid.NewString()}
	return request, frame
}

func TestRunEnvironmentQualificationJobUsesPrivateBoundedBootAndExit(t *testing.T) {
	request, frame := qualificationJobRuntimeFixture()
	request.FrozenInputs.SecretRefs = map[string]string{"API_TOKEN": "secret:TOKEN_SOURCE"}
	sealed := []fcvm.SealedEnvEntry{{Key: "API_TOKEN", SourceKey: "TOKEN_SOURCE", Ciphertext: []byte("sealed")}}
	boot := &qualificationJobBootStub{}
	exit := &qualificationJobExitStub{result: JobExitResult{ExitCode: 0, ErrorClass: "succeeded", Signal: 0,
		OutputManifest: []byte(`{"version":1,"artifacts":[{"key":"secret-output"}]}`)}}
	engine := &Engine{fcVer: "1.10.0", jobVmmClient: boot, jobExitWaiter: exit}
	evidence, err := engine.runEnvironmentQualificationJob(context.Background(), request, frame, uuid.NewString(), api.PlanHobby,
		map[string]string{"API_URL": "http://10.0.0.1:1027/private"}, sealed, func(context.Context, state.Instance) error { return nil })
	if err != nil {
		t.Fatalf("runEnvironmentQualificationJob: %v", err)
	}
	if boot.spec.QualificationExecution == nil || *boot.spec.QualificationExecution != frame || boot.spec.Command[1] != "smoke.js" ||
		boot.spec.TaskTimeoutSec != 15 || boot.spec.VcpuCount != 1 || boot.spec.ImageRef != request.Artifact.RootfsKey || !boot.spec.StartHeld ||
		boot.spec.Env["API_URL"] != "http://10.0.0.1:1027/private" ||
		len(boot.spec.SealedEnvEntries) != 1 || boot.spec.SealedEnvEntries[0].Key != "API_TOKEN" ||
		boot.spec.SealedEnvEntries[0].SourceKey != "TOKEN_SOURCE" || string(boot.spec.SealedEnvEntries[0].Ciphertext) != "sealed" ||
		exit.spec.InstanceID != frame.InstanceID || exit.spec.Deadline <= 0 || evidence.ValidateFor(request, frame.InstanceID) != nil {
		t.Fatalf("job qualification boot/wait/evidence = %+v / %+v / %+v", boot.spec, exit.spec, evidence)
	}
}

func TestQualificationJobRuntimeConfigInputsResolveFrozenScopedSecrets(t *testing.T) {
	store, account, app, _ := scopedSecretRuntimeFixture(t)
	engine := &Engine{store: store, log: testLog()}
	request, _ := qualificationJobRuntimeFixture()
	request.AppID = app.ID
	request.FrozenInputs.AppID = app.ID
	request.FrozenInputs.Scope = "production"
	request.FrozenInputs.Variables = map[string]string{}
	request.FrozenInputs.SecretRefs = map[string]string{"DATABASE_URL": "secret:DATABASE_B"}
	inputs, delivery, err := engine.qualificationJobRuntimeConfigInputs(t.Context(), account.ID, store, request)
	if err != nil {
		t.Fatalf("resolve qualification job config: %v", err)
	}
	if len(delivery.Entries) != 1 || delivery.Entries[0].Key != "DATABASE_URL" || delivery.Entries[0].SourceKey != "DATABASE_B" ||
		string(delivery.Entries[0].Ciphertext) != "production-sealed-DATABASE_B" || inputs.SecretRefs["DATABASE_URL"] != "secret:DATABASE_B" ||
		inputs.SecretVersions["production/DATABASE_B"] < 1 || len(inputs.SecretVersions) != 1 {
		t.Fatalf("qualification job did not freeze the selected secret input: inputs=%+v delivery=%+v", inputs, delivery)
	}
	if err := store.UpsertAppSecretInScope(t.Context(), account.ID, app.ID, "production", "DATABASE_B", []byte("rotated-ciphertext")); err != nil {
		t.Fatal(err)
	}
	if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs); err != nil || fresh {
		t.Fatalf("old qualification secret version remained fresh after rotation: fresh=%t err=%v", fresh, err)
	}
	if err := store.PutAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.qualificationJobRuntimeConfigInputs(t.Context(), account.ID, store, request); err == nil {
		t.Fatal("qualification accepted a secret alias changed after candidate preparation")
	}
}

func TestRunEnvironmentQualificationJobRejectsUnmatchedExit(t *testing.T) {
	request, frame := qualificationJobRuntimeFixture()
	boot := &qualificationJobBootStub{}
	exit := &qualificationJobExitStub{result: JobExitResult{ExitCode: 0, ErrorClass: "succeeded", Signal: 0, LeaseToken: "other-token"}}
	engine := &Engine{fcVer: "1.10.0", jobVmmClient: boot, jobExitWaiter: exit}
	if _, err := engine.runEnvironmentQualificationJob(context.Background(), request, frame, uuid.NewString(), api.PlanHobby,
		map[string]string{}, nil, func(context.Context, state.Instance) error { return nil }); err == nil {
		t.Fatal("accepted a job-exit frame for a different lease")
	}
}

func TestQualificationGraphRuntimeAllowsOnlyReceiptBackedJobOmissions(t *testing.T) {
	job, _ := qualificationJobRuntimeFixture()
	service := job
	service.ID, service.Resource, service.ExecutionMode = uuid.NewString(), "workload/api", api.ExecutionModeService
	service.ReservedInstanceID = uuid.NewString()
	service.FrozenInputs = state.EnvironmentWorkloadRuntime{AppID: service.AppID, Resource: service.Resource}
	evidence, err := state.NewEnvironmentQualificationJobSmokeEvidence(job, job.ReservedInstanceID, 0, "succeeded", 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt := state.EnvironmentQualificationJobSmokeReceipt{RequestID: job.ID, Attempt: job.Attempt, GraphID: job.GraphID,
		InstanceID: evidence.InstanceID, Resource: evidence.Resource, PolicyID: evidence.PolicyID,
		PolicySHA256: evidence.PolicySHA256, ResultSHA256: evidence.ResultSHA256}
	reader := qualificationJobReceiptReader{receipt: receipt}
	if err := validateQualificationGraphClaimSubset(t.Context(), []state.EnvironmentWorkloadQualificationRequest{job, service},
		[]state.EnvironmentWorkloadQualificationRequest{service}, reader); err != nil {
		t.Fatalf("accepted graph subset with a retired passing job receipt: %v", err)
	}
	if err := validateQualificationGraphClaimSubset(t.Context(), []state.EnvironmentWorkloadQualificationRequest{job, service},
		[]state.EnvironmentWorkloadQualificationRequest{job}, reader); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted an omitted service member: %v", err)
	}
	wrong := receipt
	wrong.PolicySHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := validateQualificationGraphClaimSubset(t.Context(), []state.EnvironmentWorkloadQualificationRequest{job, service},
		[]state.EnvironmentWorkloadQualificationRequest{service}, qualificationJobReceiptReader{receipt: wrong}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted a job receipt for a different reviewed policy: %v", err)
	}
}
