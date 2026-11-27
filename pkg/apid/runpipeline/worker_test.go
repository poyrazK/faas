package runpipeline_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type receipts struct{ rows map[string]state.Execution }

func (r *receipts) ExecutionWorkflowStepByLabel(_ context.Context, account, workflow string, principal *string, label string) (state.Execution, error) {
	row, ok := r.rows[label]
	if !ok {
		return row, state.ErrNotFound
	}
	return row, nil
}
func (*receipts) ListExecutionsByWorkflow(context.Context, string, string, *string, api.ExecutionStatus, int, int) ([]state.Execution, error) {
	return nil, nil
}
func (*receipts) ExecutionWorkflowSummary(context.Context, string, string, *string) (state.ExecutionWorkflowResponse, error) {
	return state.ExecutionWorkflowResponse{}, nil
}
func TestPipelineParallelBranchesAndArtifactHandoffAfterRestart(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "pipeline-dag@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	rawRequest := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default (x)=>x", OutputFiles: []string{"report.csv"}}
	request := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "dag", Version: "v1", MaxParallelSteps: 2, Pipeline: &api.ExecutionPipelineOptions{}, Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "inline", Request: rawRequest}, {Label: "stored", Request: rawRequest}, {Label: "consume", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default (x)=>x"}, DependsOn: []string{"inline", "stored"}, IncludeDependencyResults: true, ArtifactInputs: []api.ManagedExecutionWorkflowArtifactInput{{FromStep: "inline", Name: "report.csv", Path: "inline.csv"}, {FromStep: "stored", Name: "report.csv", Path: "stored.csv"}}}}}
	plan, err := (runpipeline.Compiler{}).Compile(ctx, request, true, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	hash := sha256.Sum256(encoded)
	now := time.Now().UTC()
	principal := account.ID
	job, err := store.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account.ID, RunsPrincipalID: &principal, WorkflowID: "dag", PlanID: hex.EncodeToString(hash[:12]), StepCount: 3, SealedPlan: []byte("sealed"), PayloadKID: "key", CreatedAt: now}, plan.Policy)
	if err != nil {
		t.Fatal(err)
	}
	reader := &receipts{rows: map[string]state.Execution{}}
	admitted := []int{}
	content := []byte("a,b\n1,2\n")
	digest := sha256.Sum256(content)
	fileID := uuid.NewString()
	worker := runpipeline.Worker{Jobs: store, Runs: store, WorkflowRuns: reader, Open: func(context.Context, state.ExecutionWorkflowJob) (runpipeline.Plan, error) { return plan, nil }, Admit: func(_ context.Context, c state.ExecutionWorkflowJobClaim, _ state.ExecutionPipeline, i, attempt int, previous string, r api.CreateExecutionRequest, _ []runfiles.AgentFilePin) error {
		admitted = append(admitted, i)
		if i == 2 {
			if len(r.Files) != 2 || len(r.InputFiles) != 1 || r.InputFiles[0].FileID != fileID || r.InputFiles[0].Path != "stored.csv" || r.Files[1].Path != "inline.csv" {
				t.Fatal("artifact handoff", r)
			}
			var fanin map[string]any
			if json.Unmarshal(r.Input, &fanin) != nil || len(fanin) != 2 {
				t.Fatal("fan-in", string(r.Input))
			}
		}
		artifact := api.ExecutionArtifact{Name: "report.csv", SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:]), Content: content}
		if i == 1 {
			artifact.Content = nil
			artifact.FileID = fileID
		}
		reader.rows[runpipeline.Label(c.PlanID, i, attempt)] = state.Execution{ID: fmt.Sprint(i), Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Artifacts: []api.ExecutionArtifact{artifact}}
		return nil
	}}
	for n := 0; n < 3; n++ {
		claim, err := store.ClaimExecutionPipeline(ctx, "restarted-worker", now.Add(time.Duration(n+1)*5*time.Second), time.Minute)
		if err != nil {
			row, _ := store.ExecutionWorkflowJobByKey(ctx, account.ID, "dag", &principal)
			t.Fatal("claim", n, row.Status, row.LastError, err)
		}
		if err = worker.Drive(ctx, claim); err != nil {
			t.Fatal(err)
		}
		if n == 0 && len(admitted) != 2 {
			t.Fatal("parallel branches", admitted)
		}
	}
	final, err := store.ExecutionWorkflowJobByKey(ctx, account.ID, "dag", &principal)
	if err != nil || final.Status != api.ManagedExecutionWorkflowSucceeded || len(admitted) != 3 {
		t.Fatal("final DAG", final.Status, admitted, err)
	}
	meta, e := store.ExecutionPipelineByID(ctx, job.ID)
	if e != nil || meta.Completion == nil || meta.Completion.Outcome != "succeeded" || len(meta.Completion.Steps) != 3 || len(meta.Completion.Steps[0].Attempts[0].Artifacts) != 1 {
		t.Fatal("completion manifest", meta.Completion, e)
	}
	rawManifest, _ := json.Marshal(meta.Completion)
	if bytes.Contains(rawManifest, content) || bytes.Contains(rawManifest, []byte("content")) {
		t.Fatal("artifact bytes leaked into manifest")
	}
	meta.Completion.Steps[0].Outcome = "mutated"
	again, _ := store.ExecutionPipelineByID(ctx, job.ID)
	if again.Completion.Steps[0].Outcome != "succeeded" {
		t.Fatal("mutable completion snapshot")
	}
	response, err := runpipeline.Response(ctx, job, store, reader)
	if err != nil || len(response.Steps) != 3 || len(response.Steps[2].Attempts) != 1 {
		t.Fatal("metadata", response, err)
	}
}

func TestPipelineCancellationAndExpiryDoNotNeedPlanIdentity(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			store := state.NewMemStore()
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "stop@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			created := now
			if !cancel {
				created = now.Add(-2 * time.Minute)
			}
			policy := state.ExecutionPipelinePolicy{TotalTimeoutMS: 60000, Steps: []state.ExecutionPipelineStep{{Label: "work", Retry: api.ExecutionRetryPolicy{MaxAttempts: 1, BackoffMS: 1000}}}}
			principal := account.ID
			job, err := store.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account.ID, RunsPrincipalID: &principal, WorkflowID: "stop", PlanID: "aaaaaaaaaaaaaaaaaaaaaaaa", StepCount: 1, SealedPlan: []byte("private"), PayloadKID: "missing-key", CreatedAt: created}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				if err = store.CancelExecutionPipeline(ctx, account.ID, "stop", &principal, now); err != nil {
					t.Fatal(err)
				}
			}
			claim, err := store.ClaimExecutionPipeline(ctx, "worker", now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			worker := runpipeline.Worker{Jobs: store, Runs: store, WorkflowRuns: &receipts{rows: map[string]state.Execution{}}, Enabled: func() bool { return false }, Open: func(context.Context, state.ExecutionWorkflowJob) (runpipeline.Plan, error) {
				t.Fatal("stopping opened a plan")
				return runpipeline.Plan{}, nil
			}}
			if err = worker.Drive(ctx, claim); err != nil {
				t.Fatal(err)
			}
			final, err := store.ExecutionWorkflowJobByKey(ctx, account.ID, "stop", &principal)
			if err != nil || final.ID != job.ID || final.Status != api.ManagedExecutionWorkflowFailed || len(final.SealedPlan) != 0 {
				t.Fatal("stop outcome", final, err)
			}
			meta, err := store.ExecutionPipelineByID(ctx, job.ID)
			outcome := "deadline_exceeded"
			if cancel {
				outcome = "cancelled"
			}
			if err != nil || meta.Completion == nil || meta.Completion.Outcome != outcome || meta.Completion.Steps[0].Outcome != "not_started" {
				t.Fatal("stop manifest", meta.Completion, err)
			}
		})
	}
}

func TestPipelineCompletionContractFailureKeepsSuccessfulBranch(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "pipeline-contract@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	req := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "contract", Version: "v1", MaxParallelSteps: 2, FailurePolicy: api.ExecutionWorkflowManagedFailureContinueIndependent, Steps: []api.CreateManagedExecutionWorkflowStep{
		{Label: "invalid", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default ()=>1"}, ResultSchema: json.RawMessage(`{"type":"integer"}`)},
		{Label: "valid", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default ()=>1"}},
	}}
	plan, err := (runpipeline.Compiler{}).Compile(ctx, req, true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	hash := sha256.Sum256(raw)
	now := time.Now().UTC()
	principal := account.ID
	job, err := store.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account.ID, RunsPrincipalID: &principal, WorkflowID: "contract", PlanID: hex.EncodeToString(hash[:12]), StepCount: 2, SealedPlan: raw, PayloadKID: "key", CreatedAt: now}, plan.Policy)
	if err != nil {
		t.Fatal(err)
	}
	reader := &receipts{rows: map[string]state.Execution{}}
	for i, wall := range []int64{3, 5} {
		reader.rows[runpipeline.Label(job.PlanID, i, 1)] = state.Execution{ID: fmt.Sprint(i), Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{}`), Usage: api.ExecutionUsage{WallTimeMS: wall}}
	}
	worker := runpipeline.Worker{Jobs: store, Runs: store, WorkflowRuns: reader, Open: func(context.Context, state.ExecutionWorkflowJob) (runpipeline.Plan, error) { return plan, nil }, Admit: func(context.Context, state.ExecutionWorkflowJobClaim, state.ExecutionPipeline, int, int, string, api.CreateExecutionRequest, []runfiles.AgentFilePin) error {
		t.Fatal("settled steps admitted again")
		return nil
	}}
	claim, err := store.ClaimExecutionPipeline(ctx, "worker", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Drive(ctx, claim); err != nil {
		t.Fatal(err)
	}
	meta, err := store.ExecutionPipelineByID(ctx, job.ID)
	c := meta.Completion
	if err != nil || c == nil || c.Outcome != "failed" || c.Steps[0].Outcome != "contract_failed" || c.Steps[0].ResultValidation != "invalid" || c.Steps[1].Outcome != "succeeded" || c.Usage.WallTimeMS != 8 {
		t.Fatal("partial completion", c, err)
	}
}
