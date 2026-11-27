package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemPipelineProgressAndRerun(t *testing.T) {
	exercisePipelineRecovery(t, state.NewMemStore(), nil)
}
func exercisePipelineRecovery(t *testing.T, s contractTestStore, oldClaim func(string)) {
	ctx := t.Context()
	account, e := s.CreateAccount(ctx, uuid.NewString()+"@progress.test", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	principal := account.ID
	request := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "original", Version: "v1", Pipeline: &api.ExecutionPipelineOptions{}, MaxParallelSteps: 2, FailurePolicy: api.ExecutionWorkflowManagedFailureContinueIndependent, Steps: []api.CreateManagedExecutionWorkflowStep{
		{Label: "good", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x=>x", OutputFiles: []string{"data.txt"}}},
		{Label: "bad", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x=>x"}},
		{Label: "join", DependsOn: []string{"good", "bad"}, IncludeDependencyResults: true, ArtifactInputs: []api.ManagedExecutionWorkflowArtifactInput{{FromStep: "good", Name: "data.txt", Path: "data.txt"}}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default x=>x"}}}}
	pub, meta, err := runtask.Normalize(api.PublishExecutionTaskRequest{Name: "repair", Version: 1, Definition: request.Steps[1].Request}, account.Plan)
	if err != nil {
		t.Fatal(err)
	}
	meta.ID = uuid.NewString()
	version, _, err := s.PublishExecutionTaskVersion(ctx, state.ExecutionTaskVersion{ID: meta.ID, AccountID: account.ID, Name: meta.Name, Version: meta.Version, Digest: meta.Digest, Runtime: meta.Runtime, Metadata: meta, SealedDefinition: []byte("sealed"), PayloadKID: "key"})
	if err != nil {
		t.Fatal(err)
	}
	request.Steps[1].Request = api.CreateExecutionRequest{}
	request.Steps[1].Task = &api.InvokeExecutionTaskRequest{Name: "repair", Version: 1}
	submit := runpipeline.Submitter{Jobs: s, Receipts: s, Enabled: true, Compiler: runpipeline.Compiler{Lookup: func(ctx context.Context, name string, v int) (state.ExecutionTaskVersion, error) {
		return s.ExecutionTaskVersionByName(ctx, account.ID, name, v)
	}, Open: func(context.Context, state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error) {
		return pub, nil
	}}, Seal: func(raw []byte) ([]byte, string, error) { return raw, "key", nil }}
	parent, _, e := submit.Submit(ctx, account.ID, &principal, false, false, request)
	if e != nil {
		t.Fatal(e)
	}
	if oldClaim != nil {
		oldClaim(parent.ID)
	}
	var handedOff bool
	worker := runpipeline.Worker{Jobs: s, Runs: s, WorkflowRuns: s, Open: func(_ context.Context, j state.ExecutionWorkflowJob) (runpipeline.Plan, error) {
		var p runpipeline.Plan
		e := json.Unmarshal(j.SealedPlan, &p)
		return p, e
	}, Admit: func(ctx context.Context, c state.ExecutionWorkflowJobClaim, p state.ExecutionPipeline, i, a int, previous string, r api.CreateExecutionRequest, _ []runfiles.AgentFilePin) error {
		if c.WorkflowID != "original" && i == 2 {
			handedOff = len(r.Files) == 2 && string(r.Files[1].Content) == "PRIVATE_ARTIFACT"
			if !strings.Contains(string(r.Input), "PRIVATE_RESULT") {
				t.Fatal("reused result handoff missing", string(r.Input))
			}
		}
		params := executionTestParams(t, account.ID, time.Now().UTC(), 0, "sealed")
		params.Request, _ = r.Resolve(account.Plan)
		params.WorkflowID = c.WorkflowID
		params.StepLabel = runpipeline.Label(c.PlanID, i, a)
		params.RunsPrincipalID = &principal
		params.SourceBytes = params.Request.SourceBytes()
		params.InputBytes = len(params.Request.Input)
		params.FilesAccountWide = false
		params.ControlAccountWide = false
		if p.Policy.Steps[i].Task != nil {
			params.Task = &state.ExecutionTaskAdmission{VersionID: version.ID, RequestDigest: sha256.Sum256([]byte(c.PlanID + params.StepLabel))}
		}
		params.Pipeline = &state.ExecutionPipelineAdmission{JobID: c.ID, LeaseToken: c.ClaimToken, Step: i, Attempt: a, PreviousRunID: previous}
		if params.DeadlineAt.After(p.DeadlineAt) {
			params.DeadlineAt = p.DeadlineAt
		}
		_, e := s.CreateExecution(ctx, params)
		return e
	}}
	drive := func() {
		t.Helper()
		c, e := s.ClaimExecutionPipeline(ctx, "restarted-worker", time.Now().Add(3*time.Second), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		if e = worker.Drive(ctx, c); e != nil {
			t.Fatal(e)
		}
	}
	finish := func(failed bool, artifact bool) {
		t.Helper()
		c, e := s.ClaimExecutionForAccount(ctx, account.ID, "schedd", time.Now(), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.MarkExecutionRunning(ctx, c.ID, *c.LeaseToken, time.Now()); e != nil {
			t.Fatal(e)
		}
		p := state.CompleteExecutionParams{ID: c.ID, LeaseToken: *c.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"value":"PRIVATE_RESULT"}`), Usage: api.ExecutionUsage{WallTimeMS: 7}, FinishedAt: time.Now()}
		if failed {
			code, message := "guest_error", "failed"
			p.Status = api.ExecutionStatusFailed
			p.FailureCode = &code
			p.FailureMessage = &message
			p.Result = nil
		}
		if artifact {
			p.Artifacts = []api.ExecutionArtifact{{Name: "data.txt", SizeBytes: 16, SHA256: "sha256:c3155d0f3df49b23b717a394f2a0eaec97735c1141c516d2cb0516bfaebf68e8", Content: []byte("PRIVATE_ARTIFACT")}}
			p.Artifacts[0].SizeBytes = len(p.Artifacts[0].Content)
			p.Artifacts[0].SHA256 = artifactDigest(p.Artifacts[0].Content)
		}
		if _, e = s.CompleteExecution(ctx, p); e != nil {
			t.Fatal(e)
		}
	}
	drive()
	finish(false, true)
	finish(true, false)
	drive()
	finish(false, false)
	drive()
	parent, e = s.ExecutionWorkflowJobByKey(ctx, account.ID, "original", &principal)
	if e != nil || parent.Status != api.ManagedExecutionWorkflowFailed || len(parent.SealedPlan) != 0 {
		t.Fatal("parent terminal", parent, e)
	}
	page, e := s.ListExecutionPipelineEvents(ctx, account.ID, parent.ID, &principal, 0, api.ExecutionPipelineProgressRetained)
	if e != nil || !page.Terminal || page.NextSequence < 4 {
		t.Fatal("progress", page, e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("progress leaked data")
	}
	page.Progress.Steps[0].State = "corrupted"
	again, _ := s.ListExecutionPipelineEvents(ctx, account.ID, parent.ID, &principal, 0, 256)
	if again.Progress.Steps[0].State == "corrupted" {
		t.Fatal("mutable snapshot")
	}
	foreign := uuid.NewString()
	if _, e = s.ListExecutionPipelineEvents(ctx, account.ID, parent.ID, &foreign, 0, 256); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("cross family", e)
	}
	if _, e = s.ListExecutionPipelineEvents(ctx, account.ID, parent.ID, &principal, page.NextSequence+1, 256); e == nil {
		t.Fatal("future cursor accepted")
	}
	rerun := api.RerunExecutionPipelineRequest{WorkflowID: "rerun", Original: request, RepeatSteps: []string{"bad"}}
	if _, _, e = submit.RerunPipeline(ctx, parent, rerun, &principal, false, false, s); e == nil {
		t.Fatal("successful affected fan-in repeated without acknowledgement")
	}
	rerun.RepeatSteps = []string{"join", "bad"}
	child, replayed, e := submit.RerunPipeline(ctx, parent, rerun, &principal, false, false, s)
	if e != nil || replayed {
		t.Fatal("rerun", e)
	}
	claim, err := s.ClaimExecutionPipeline(ctx, "rerun-worker", time.Now().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forged := executionTestParams(t, account.ID, time.Now(), 0, "sealed")
	forged.WorkflowID = child.WorkflowID
	forged.StepLabel = runpipeline.Label(child.PlanID, 0, 1)
	forged.RunsPrincipalID = &principal
	forged.FilesAccountWide = false
	forged.ControlAccountWide = false
	forged.Pipeline = &state.ExecutionPipelineAdmission{JobID: child.ID, LeaseToken: claim.ClaimToken, Step: 0, Attempt: 1}
	if _, err = s.CreateExecution(ctx, forged); !errors.Is(err, state.ErrExecutionInvalid) {
		t.Fatal("reused step admitted again", err)
	}
	if err = worker.Drive(ctx, claim); err != nil {
		t.Fatal(err)
	}
	finish(false, false)
	drive()
	finish(false, false)
	drive()
	if !handedOff {
		t.Fatal("reused artifact not handed off")
	}
	childMeta, e := s.ExecutionPipelineByID(ctx, child.ID)
	if e != nil || childMeta.Completion == nil || childMeta.Completion.Outcome != "succeeded" || childMeta.Completion.Usage.WallTimeMS != 14 || childMeta.Completion.Steps[0].ReusedRunID == "" || len(childMeta.Completion.Steps[0].Attempts) != 0 {
		t.Fatal("child receipt", childMeta.Completion, e)
	}
	usageStore, ok := s.(state.PipelineRunUsageStore)
	if !ok {
		t.Fatal("pipeline usage unavailable")
	}
	childUsage, err := runpipeline.ReadUsage(ctx, usageStore, account.ID, &api.ExecutionPipelineResponse{Completion: childMeta.Completion})
	if err != nil || childUsage.NewRuns != 2 || childUsage.ReusedSteps != 1 || childUsage.SettledRuns != 2 || childUsage.WallTimeMS != 14 {
		t.Fatalf("child usage = %+v, %v", childUsage, err)
	}
	foreignUsage, err := usageStore.PipelineRunUsage(ctx, foreign, []string{childUsage.Attempts[0].RunID})
	if err != nil || len(foreignUsage) != 0 {
		t.Fatalf("foreign pipeline usage = %+v, %v", foreignUsage, err)
	}
	runs, e := s.ListExecutionsByWorkflow(ctx, account.ID, "rerun", &principal, "", 100, 0)
	if e != nil || len(runs) != 2 {
		t.Fatal("reused step charged again", len(runs), e)
	}
	submit.Now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	expired := rerun
	expired.WorkflowID = "expired-reuse"
	if _, _, err := submit.RerunPipeline(ctx, parent, expired, &principal, false, false, s); err == nil {
		t.Fatal("expired reuse accepted")
	}
	submit.Now = nil
	if err = s.RetireExecutionTaskVersion(ctx, account.ID, "repair", 1); err != nil {
		t.Fatal(err)
	}
	submit.Enabled = false
	copy, replayed, e := submit.RerunPipeline(ctx, parent, rerun, &principal, false, false, s)
	if e != nil || !replayed || copy.ID != child.ID {
		t.Fatal("lost ack replay with gates off", e)
	}
	rerun.RepeatSteps = []string{"bad"}
	if _, _, e = submit.RerunPipeline(ctx, parent, rerun, &principal, false, false, s); e == nil {
		t.Fatal("changed rerun intent replayed")
	}
	original, _ := s.ExecutionPipelineByID(ctx, parent.ID)
	if original.Completion.Outcome != "failed" || original.Completion.Rerun != nil {
		t.Fatal("parent modified")
	}
	history, ok := s.(state.ExecutionPipelineHistoryStore)
	if !ok {
		t.Fatal("pipeline history unavailable")
	}
	entries, err := history.ListExecutionPipelineHistory(ctx, account.ID, cursor.Key{}, 1)
	if err != nil || len(entries) != 1 || entries[0].ID != child.ID || entries[0].ParentID != parent.ID || entries[0].Outcome != "succeeded" {
		t.Fatalf("newest history page = %+v, %v", entries, err)
	}
	before := cursor.Key{ID: entries[0].ID, CreatedAt: entries[0].CreatedAt}
	entries, err = history.ListExecutionPipelineHistory(ctx, account.ID, before, 1)
	if err != nil || len(entries) != 1 || entries[0].ID != parent.ID || entries[0].Outcome != "failed" {
		t.Fatalf("older history page = %+v, %v", entries, err)
	}
	entries, err = history.ListExecutionPipelineHistory(ctx, foreign, cursor.Key{}, 1)
	if err != nil || len(entries) != 0 {
		t.Fatalf("foreign history page = %+v, %v", entries, err)
	}
	exerciseProgressTail(t, s, account.ID, &principal)
}
func artifactDigest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
func exerciseProgressTail(t *testing.T, s contractTestStore, account string, principal *string) {
	t.Helper()
	ctx := t.Context()
	policy := state.ExecutionPipelinePolicy{ProgressVersion: "v1", TotalTimeoutMS: 60000}
	progress := &api.ExecutionPipelineProgress{Status: "queued"}
	for i := 0; i < 16; i++ {
		label := fmt.Sprint("step-", i)
		policy.Steps = append(policy.Steps, state.ExecutionPipelineStep{Label: label, Retry: api.ExecutionRetryPolicy{MaxAttempts: 1, BackoffMS: 1000}})
		progress.Steps = append(progress.Steps, api.ExecutionPipelineStepProgress{Label: label, State: "queued", Reason: "pending"})
	}
	j, e := s.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account, RunsPrincipalID: principal, WorkflowID: "tail", PlanID: strings.Repeat("b", 24), StepCount: 16, SealedPlan: []byte("sealed"), PayloadKID: "key", CreatedAt: time.Now()}, policy)
	if e != nil {
		t.Fatal(e)
	}
	var previous int64
	for n := 0; n < 25; n++ {
		c, e := s.ClaimExecutionPipeline(ctx, "recreated-worker", time.Now().Add(time.Duration(n+1)*time.Second), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		for i := range progress.Steps {
			progress.Steps[i].Reason = "capacity"
			if n%2 == 0 {
				progress.Steps[i].Reason = "budget_concurrency"
			}
		}
		u := state.ExecutionWorkflowJobUpdate{Status: api.ManagedExecutionWorkflowQueued, UpdatedAt: time.Now(), ScheduledFor: time.Now(), PipelineProgress: progress}
		if e = s.UpdateExecutionPipeline(ctx, c.ID, c.ClaimToken, u); e != nil {
			t.Fatal(e)
		}
		if e = s.UpdateExecutionPipeline(ctx, c.ID, c.ClaimToken, u); !errors.Is(e, state.ErrExecutionWorkflowLeaseLost) {
			t.Fatal("stale journal write", e)
		}
		page, e := s.ListExecutionPipelineEvents(ctx, account, j.ID, principal, 0, 256)
		if e != nil || page.NextSequence <= previous {
			t.Fatal(page, e)
		}
		previous = page.NextSequence
	}
	page, e := s.ListExecutionPipelineEvents(ctx, account, j.ID, principal, 0, 256)
	if e != nil || !page.Gap || len(page.Events) != 256 {
		t.Fatal("retention gap", page, e)
	}
	c, e := s.ClaimExecutionPipeline(ctx, "unchanged-poll", time.Now().Add(time.Minute), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateExecutionPipeline(ctx, c.ID, c.ClaimToken, state.ExecutionWorkflowJobUpdate{Status: api.ManagedExecutionWorkflowQueued, UpdatedAt: time.Now(), ScheduledFor: time.Now(), PipelineProgress: progress}); e != nil {
		t.Fatal(e)
	}
	page, e = s.ListExecutionPipelineEvents(ctx, account, j.ID, principal, previous, 256)
	if e != nil || len(page.Events) != 0 || page.NextSequence != previous {
		t.Fatal("poll duplicated events", page, e)
	}
}
