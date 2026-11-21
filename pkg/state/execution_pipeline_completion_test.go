package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemPipelineCompletionAndAgentObligation(t *testing.T) {
	exercisePipelineCompletion(t, state.NewMemStore())
}
func exercisePipelineCompletion(t *testing.T, s contractTestStore) {
	ctx := t.Context()
	now := time.Now().UTC()
	account, err := s.CreateAccount(ctx, uuid.NewString()+"@pipeline.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pipeline-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, CallbackURL: "https://example.test/hooks", ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect", CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), Enabled: true}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	pub, meta, err := runtask.Normalize(api.PublishExecutionTaskRequest{Name: "report", Version: 1, AgentEndpointIDs: []string{ep.ID}, Definition: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x => x", OutputFiles: []string{"report.csv"}}}, account.Plan)
	if err != nil {
		t.Fatal(err)
	}
	meta.ID = uuid.NewString()
	version, _, err := s.PublishExecutionTaskVersion(ctx, state.ExecutionTaskVersion{ID: meta.ID, AccountID: account.ID, Name: meta.Name, Version: meta.Version, Digest: meta.Digest, Runtime: meta.Runtime, Metadata: meta, SealedDefinition: []byte("sealed"), PayloadKID: "key"})
	if err != nil {
		t.Fatal(err)
	}
	maximum := int64(2)
	if _, err = s.PutRunBudget(ctx, account.ID, "customer", "customer-42", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &maximum}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutRunBinding(ctx, account.ID, "agent_endpoint", ep.ID, api.SetRunAttributionBindingRequest{CustomerRef: "customer-42"}); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(account.Plan)
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: account.ID, TargetURL: "https://example.test/completed", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{api.ExecutionPipelineFinishedWebhookEvent}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	callback := &state.ExecutionTaskCallback{EndpointID: ep.ID, ThreadID: "thread", ToolCallID: "call", ProviderCallID: "provider", IssuedAt: now}
	submit := runpipeline.Submitter{Jobs: s, Receipts: s, Enabled: true, Callback: callback, Compiler: runpipeline.Compiler{Lookup: func(context.Context, string, int) (state.ExecutionTaskVersion, error) { return version, nil }, Open: func(context.Context, state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error) {
		return pub, nil
	}}, Seal: func(raw []byte) ([]byte, string, error) { return raw, "key", nil }}
	request := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "agent-pipeline", Version: "v1", Batch: &api.ExecutionTaskBatch{Name: "report", Version: 1, Retry: &api.ExecutionRetryPolicy{MaxAttempts: 2, SafeToRepeat: true}, Items: []api.ExecutionTaskBatchItem{{Label: "one", Input: json.RawMessage(`{"z":9007199254740993,"private":"PRIVATE_INPUT","a":1}`)}}}}
	principal := account.ID
	job, replayed, err := submit.Submit(ctx, account.ID, &principal, true, true, request)
	if err != nil || replayed {
		t.Fatal("submit", err)
	}
	submit.Enabled = false
	submit.Callback = &state.ExecutionTaskCallback{EndpointID: ep.ID, ThreadID: "thread", ToolCallID: "call", ProviderCallID: "provider", IssuedAt: now.Add(time.Hour)}
	replayRequest := request
	replayBatch := *request.Batch
	replayBatch.Items = append([]api.ExecutionTaskBatchItem(nil), request.Batch.Items...)
	replayBatch.Items[0].Input = json.RawMessage(`{"a":1,"private":"PRIVATE_INPUT","z":9007199254740993}`)
	replayRequest.Batch = &replayBatch
	again, replayed, err := submit.Submit(ctx, account.ID, &principal, true, true, replayRequest)
	if err != nil || !replayed || again.ID != job.ID {
		t.Fatal("lost ack replay", err)
	}
	submit.Callback.ProviderCallID = "changed"
	if _, _, err = submit.Submit(ctx, account.ID, &principal, true, true, request); err == nil {
		t.Fatal("changed provider accepted")
	}
	submit.Callback.ProviderCallID = "provider"
	changedInput := replayRequest
	changedBatch := *replayRequest.Batch
	changedBatch.Items = append([]api.ExecutionTaskBatchItem(nil), replayBatch.Items...)
	changedBatch.Items[0].Input = json.RawMessage(`{"a":1,"private":"PRIVATE_INPUT","z":9007199254740992}`)
	changedInput.Batch = &changedBatch
	if _, _, err = submit.Submit(ctx, account.ID, &principal, true, true, changedInput); err == nil {
		t.Fatal("distinct large integer input replayed")
	}
	worker := runpipeline.Worker{Jobs: s, Runs: s, WorkflowRuns: s, Open: func(_ context.Context, j state.ExecutionWorkflowJob) (runpipeline.Plan, error) {
		var p runpipeline.Plan
		err := json.Unmarshal(j.SealedPlan, &p)
		return p, err
	}, Admit: func(ctx context.Context, c state.ExecutionWorkflowJobClaim, p state.ExecutionPipeline, i, a int, previous string, req api.CreateExecutionRequest, _ []runfiles.AgentFilePin) error {
		params := executionTestParams(t, account.ID, time.Now().UTC(), 0, "sealed")
		params.Request, _ = req.Resolve(account.Plan)
		params.SourceBytes = len(req.Source)
		params.InputBytes = len(req.Input)
		params.WorkflowID = c.WorkflowID
		params.StepLabel = runpipeline.Label(c.PlanID, i, a)
		params.RunsPrincipalID = c.RunsPrincipalID
		params.ControlAccountWide = p.Policy.ControlWide
		params.FilesAccountWide = p.Policy.Wide
		params.Pipeline = &state.ExecutionPipelineAdmission{JobID: c.ID, LeaseToken: c.ClaimToken, Step: i, Attempt: a, PreviousRunID: previous}
		params.Task = &state.ExecutionTaskAdmission{VersionID: version.ID, PipelineEndpointID: ep.ID, RequestDigest: sha256.Sum256([]byte(c.PlanID + params.StepLabel))}
		if params.DeadlineAt.After(p.DeadlineAt) {
			params.DeadlineAt = p.DeadlineAt
		}
		_, err := s.CreateExecution(ctx, params)
		return err
	}}
	claim, err := s.ClaimExecutionPipeline(ctx, "first-process", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	legacy := state.ExecutionWorkflowJobUpdate{Status: api.ManagedExecutionWorkflowFailed, UpdatedAt: time.Now().UTC(), ScheduledFor: time.Now().UTC()}
	if err = s.UpdateExecutionWorkflowJob(ctx, job.ID, claim.ClaimToken, legacy); err == nil {
		t.Fatal("old writer erased callback obligation")
	}
	if err = worker.Drive(ctx, claim); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingExecutionPipelineCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	if err != nil || len(pending) != 0 {
		t.Fatal("premature completion", err)
	}
	run, err := s.ClaimExecutionForAccount(ctx, account.ID, "schedd", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	receipt, readErr := s.ExecutionByID(ctx, account.ID, run.ID)
	if readErr != nil || receipt.Attribution == nil || receipt.Attribution.CustomerRef != "customer-42" || receipt.Attribution.AgentRef != "agent_endpoint:"+ep.ID {
		t.Fatal("endpoint attribution lost on receipt", receipt.Attribution, readErr)
	}
	if _, err = s.MarkExecutionRunning(ctx, run.ID, *run.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	code, message := "execution_transport_failed", "transport lost"
	if _, err = s.CompleteExecution(ctx, state.CompleteExecutionParams{ID: run.ID, LeaseToken: *run.LeaseToken, Status: api.ExecutionStatusFailed, FailureCode: &code, FailureMessage: &message, Usage: api.ExecutionUsage{WallTimeMS: 1}, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// This must read durable retry classification/finish time, including on PgStore.
	time.Sleep(1100 * time.Millisecond)
	retryClaim, claimErr := s.ClaimExecutionPipeline(ctx, "retry-process", time.Now().UTC().Add(3*time.Second), time.Minute)
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	if err = worker.Drive(ctx, retryClaim); err != nil {
		t.Fatal(err)
	}
	firstRunID := run.ID
	run, err = s.ClaimExecutionForAccount(ctx, account.ID, "schedd-retry", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal("retry admission", err)
	}
	if run.ID == firstRunID {
		t.Fatal("retry reused physical attempt")
	}
	if _, err = s.MarkExecutionRunning(ctx, run.ID, *run.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	artifactContent := []byte("PRIVATE_ARTIFACT")
	artifactHash := sha256.Sum256(artifactContent)
	artifact := api.ExecutionArtifact{Name: "report.csv", Content: artifactContent, SizeBytes: len(artifactContent), SHA256: "sha256:" + hex.EncodeToString(artifactHash[:])}
	if _, err = s.CompleteExecution(ctx, state.CompleteExecutionParams{ID: run.ID, LeaseToken: *run.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"private":"PRIVATE_RESULT"}`), Artifacts: []api.ExecutionArtifact{artifact}, ResultValidation: &api.ExecutionResultValidation{Status: "valid"}, Usage: api.ExecutionUsage{WallTimeMS: 2, CPUTimeMS: 1, PeakMemoryMB: 8}, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimExecutionPipeline(ctx, "restarted-process", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Drive(ctx, claim); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingExecutionPipelineCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	if err != nil || len(pending) != 1 || pending[0].JobID != job.ID || pending[0].Completion.Outcome != "succeeded" || pending[0].Completion.Usage.WallTimeMS != 3 {
		raw, _ := json.Marshal(pending)
		t.Fatal("durable completion", string(raw), err)
	}
	completedStep := pending[0].Completion.Steps[0]
	if len(completedStep.Attempts) != 2 || completedStep.ResultValidation != "valid" || len(completedStep.Attempts[1].Artifacts) != 1 || completedStep.Attempts[1].Artifacts[0].SHA256 != artifact.SHA256 {
		t.Fatal("receipt metadata missing from manifest", completedStep)
	}
	linked, err := s.ExecutionPipelineDeliveryForRun(ctx, account.ID, run.ID)
	if err != nil || linked.JobID != job.ID {
		t.Fatal("run scope", err)
	}
	if _, err = s.ExecutionPipelineDeliveryByID(ctx, uuid.NewString(), job.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross account receipt", err)
	}
	deliveries, _, err := s.ListAccountReleaseWebhookDeliveries(ctx, account.ID, hook.ID, 100, "")
	if err != nil || len(deliveries) != 1 {
		t.Fatal("atomic outbox", len(deliveries), err)
	}
	raw, _ := json.Marshal(pending[0].Completion)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(deliveries[0].Payload), "PRIVATE_") {
		t.Fatal("private data in completion")
	}
	final, _ := s.ExecutionWorkflowJobByKey(ctx, account.ID, job.WorkflowID, &principal)
	if len(final.SealedPlan) != 0 {
		t.Fatal("terminal plan retained")
	}
	if err = s.UpdateExecutionPipeline(ctx, job.ID, claim.ClaimToken, legacy); !errors.Is(err, state.ErrExecutionWorkflowLeaseLost) {
		t.Fatal("terminal snapshot mutable", err)
	}
	if err = s.MarkExecutionPipelineCallbackDelivered(ctx, account.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkExecutionPipelineCallbackDelivered(ctx, account.ID, job.ID); err != nil {
		t.Fatal("mark replay", err)
	}
	pending, err = s.PendingExecutionPipelineCallbacks(ctx, "", api.ExecutionTaskCallbackBatch)
	if err != nil || len(pending) != 0 {
		t.Fatal("delivery obligation not settled", err)
	}
	// Replay must never reserve another invocation, even with publication off.
	submit.Callback = callback
	if _, replayed, err = submit.Submit(ctx, account.ID, &principal, true, true, request); err != nil || !replayed {
		t.Fatal("terminal replay", err)
	}
	rows, _ := s.ListExecutions(ctx, account.ID, 10, 0)
	if len(rows) != 2 {
		t.Fatal("duplicate admission", len(rows))
	}
}
