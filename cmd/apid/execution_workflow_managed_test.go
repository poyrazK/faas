package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedExecutionWorkflowContinuesFromDurableRunReceipts(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-disconnect-test", Version: "v1",
		Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "first", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {ok:true}", Input: json.RawMessage("null")}},
			{Label: "second", InputFromPreviousResult: true, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-workflow-create"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create managed workflow = %d: %s", created.Code, created.Body.String())
	}
	var response api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != api.ManagedExecutionWorkflowQueued || response.StepCount != 2 || response.NextStep != 0 {
		t.Fatalf("initial workflow response = %+v", response)
	}
	if strings.Contains(created.Body.String(), "return {ok:true}") {
		t.Fatal("workflow response exposed source")
	}
	replayed := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-workflow-retry"})
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("identical workflow retry = %d: %s", replayed.Code, replayed.Body.String())
	}
	var replayedResponse api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayedResponse); err != nil || replayedResponse.PlanID != response.PlanID {
		t.Fatalf("identical workflow retry response = %+v, %v", replayedResponse, err)
	}
	changed := request
	changed.Steps = append([]api.CreateManagedExecutionWorkflowStep(nil), request.Steps...)
	changed.Steps[0].Request.Source = "return {ok:false}"
	conflict := e.do(t, http.MethodPost, "/v1/execution-workflows", changed, map[string]string{"Idempotency-Key": "managed-workflow-changed"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed workflow retry = %d: %s", conflict.Code, conflict.Body.String())
	}

	workflowURL := "/v1/execution-workflows/" + request.WorkflowID
	status := e.do(t, http.MethodGet, workflowURL, nil, nil)
	if status.Code != http.StatusOK {
		t.Fatalf("empty workflow summary = %d: %s", status.Code, status.Body.String())
	}
	var summary api.ExecutionWorkflowResponse
	if err := json.Unmarshal(status.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Managed) != 1 || summary.Managed[0].Status != api.ManagedExecutionWorkflowQueued || summary.RunCount != 0 {
		t.Fatalf("managed-only summary = %+v", summary)
	}

	jobStore := state.ExecutionWorkflowJobStore(e.store)
	jobClaim, err := jobStore.ClaimExecutionWorkflowJob(context.Background(), "test-worker", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), jobClaim); err != nil {
		t.Fatalf("admit first step: %v", err)
	}
	workflowRuns := state.ExecutionWorkflowStore(e.store)
	firstRows, err := workflowRuns.ListExecutionsByWorkflow(context.Background(), e.acct.ID, request.WorkflowID, nil, "", 10, 0)
	if err != nil || len(firstRows) != 1 || firstRows[0].StepLabel != managedExecutionWorkflowStepLabel(response.PlanID, "first") {
		t.Fatalf("first Run receipts = %+v, %v", firstRows, err)
	}
	completeManagedWorkflowRun(t, e, identity, firstRows[0].ID, `{"ok":true}`)

	jobClaim, err = jobStore.ClaimExecutionWorkflowJob(context.Background(), "test-worker", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), jobClaim); err != nil {
		t.Fatalf("admit second step: %v", err)
	}
	allRows, err := workflowRuns.ListExecutionsByWorkflow(context.Background(), e.acct.ID, request.WorkflowID, nil, "", 10, 0)
	if err != nil || len(allRows) != 2 {
		t.Fatalf("second Run receipts = %+v, %v", allRows, err)
	}
	var second state.Execution
	for _, row := range allRows {
		if row.StepLabel == managedExecutionWorkflowStepLabel(response.PlanID, "second") {
			second = row
		}
	}
	if second.ID == "" {
		t.Fatalf("second Run receipt missing: %+v", allRows)
	}
	completeManagedWorkflowRun(t, e, identity, second.ID, `"done"`)

	jobClaim, err = jobStore.ClaimExecutionWorkflowJob(context.Background(), "test-worker", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), jobClaim); err != nil {
		t.Fatalf("complete workflow: %v", err)
	}
	status = e.do(t, http.MethodGet, workflowURL, nil, nil)
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &summary) != nil {
		t.Fatalf("terminal workflow summary = %d: %s", status.Code, status.Body.String())
	}
	if len(summary.Managed) != 1 || summary.Managed[0].Status != api.ManagedExecutionWorkflowSucceeded || summary.Managed[0].NextStep != 2 || summary.RunCount != 2 {
		t.Fatalf("terminal managed workflow = %+v", summary)
	}
}

func TestManagedExecutionWorkflowFansOutAndCollectsDependencyResults(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-dag-test", Version: "v1", MaxParallelSteps: 2,
		FailurePolicy: api.ExecutionWorkflowManagedFailureContinueIndependent,
		Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "inspect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {finding:'issue'}"}},
			{Label: "test", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {passed:true}"}},
			{Label: "summarize", DependsOn: []string{"inspect", "test"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-dag-create"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create managed DAG = %d: %s", created.Code, created.Body.String())
	}
	var response api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobs := state.ExecutionWorkflowJobStore(e.store)
	claim, err := jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-dag-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("drive managed DAG fan-out: %v", err)
	}
	workflowRuns := state.ExecutionWorkflowStore(e.store)
	rows, err := workflowRuns.ListExecutionsByWorkflow(context.Background(), e.acct.ID, request.WorkflowID, nil, "", 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("independent root receipts = %+v, %v; want two parallel roots", rows, err)
	}
	rootLabels := map[string]bool{}
	for _, row := range rows {
		rootLabels[row.StepLabel] = true
	}
	if !rootLabels[managedExecutionWorkflowStepLabel(response.PlanID, "inspect")] || !rootLabels[managedExecutionWorkflowStepLabel(response.PlanID, "test")] {
		t.Fatalf("fan-out receipts = %+v", rows)
	}
	for i := 0; i < 2; i++ {
		runClaim, claimErr := e.store.ClaimExecution(context.Background(), "managed-dag-test", time.Now().UTC().Add(time.Second), time.Minute)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		result := `{"finding":"issue"}`
		if strings.HasSuffix(runClaim.StepLabel, ":test") {
			result = `{"passed":true}`
		}
		completeManagedWorkflowClaimedRun(t, e, identity, runClaim, result, "")
	}

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-dag-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("drive managed DAG fan-in: %v", err)
	}
	jobAfterFanIn, err := jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	joinLabel := managedExecutionWorkflowStepLabel(response.PlanID, "summarize")
	join, err := workflowRuns.ExecutionWorkflowStepByLabel(context.Background(), e.acct.ID, request.WorkflowID, jobAfterFanIn.RunsPrincipalID, joinLabel)
	if err != nil {
		t.Fatalf("fan-in Run receipt = %+v, %v; workflow job = %+v", join, err, jobAfterFanIn)
	}
	runClaim, err := e.store.ClaimExecution(context.Background(), "managed-dag-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || runClaim.ID != join.ID {
		t.Fatalf("claim fan-in Run = %q, %v; want %q", runClaim.ID, err, join.ID)
	}
	decoded, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, runClaim.SealedPayload, runClaim.PayloadKID)
	if err != nil {
		t.Fatal(err)
	}
	var inputs map[string]managedWorkflowDependencyInput
	if err := json.Unmarshal(decoded.Input, &inputs); err != nil {
		t.Fatalf("decode dependency results %s: %v", decoded.Input, err)
	}
	if inputs["inspect"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["inspect"].Result) != `{"finding":"issue"}` ||
		inputs["test"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["test"].Result) != `{"passed":true}` {
		t.Fatalf("fan-in dependency input = %+v", inputs)
	}
	completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"summary":"ready"}`, "")

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-dag-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("finish managed DAG: %v", err)
	}
	job, err := jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil || job.Status != api.ManagedExecutionWorkflowSucceeded || job.NextStep != 3 {
		t.Fatalf("terminal managed DAG = %+v, %v", job, err)
	}
}

func TestManagedExecutionWorkflowEnforcesResultSchemaBeforeFanIn(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-contract-dag", Version: "v1", MaxParallelSteps: 2,
		FailurePolicy: api.ExecutionWorkflowManagedFailureContinueIndependent,
		Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "collect", ResultSchema: json.RawMessage(`{"type":"object","required":["items"],"properties":{"items":{"type":"array"}}}`),
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {items: 'wrong'}"}},
			{Label: "test", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {ok: true}"}},
			{Label: "summarize", DependsOn: []string{"collect", "test"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	invalidSchema := request
	invalidSchema.Steps = append([]api.CreateManagedExecutionWorkflowStep(nil), request.Steps...)
	invalidSchema.Steps[0].ResultSchema = json.RawMessage(`{"$ref":"https://example.com/schema.json"}`)
	invalid := e.do(t, http.MethodPost, "/v1/execution-workflows", invalidSchema, map[string]string{"Idempotency-Key": "managed-contract-invalid-schema"})
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("managed workflow with external result schema = %d: %s", invalid.Code, invalid.Body.String())
	}
	created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-contract-create"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create result-contract workflow = %d: %s", created.Code, created.Body.String())
	}
	var response api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}

	jobs := state.ExecutionWorkflowJobStore(e.store)
	claim, err := jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-contract-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("admit independent steps: %v", err)
	}
	for i := 0; i < 2; i++ {
		runClaim, claimErr := e.store.ClaimExecution(context.Background(), "managed-contract-test", time.Now().UTC().Add(time.Second), time.Minute)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if strings.HasSuffix(runClaim.StepLabel, ":collect") {
			completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"items":"wrong"}`, "")
		} else {
			completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"ok":true}`, "")
		}
	}

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-contract-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("admit result fan-in: %v", err)
	}
	job, err := jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	join, err := state.ExecutionWorkflowStore(e.store).ExecutionWorkflowStepByLabel(context.Background(), e.acct.ID, request.WorkflowID, job.RunsPrincipalID,
		managedExecutionWorkflowStepLabel(response.PlanID, "summarize"))
	if err != nil {
		t.Fatalf("result-contract fan-in receipt = %+v, %v", join, err)
	}
	runClaim, err := e.store.ClaimExecution(context.Background(), "managed-contract-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || runClaim.ID != join.ID {
		t.Fatalf("claim result-contract fan-in Run = %q, %v; want %q", runClaim.ID, err, join.ID)
	}
	decoded, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, runClaim.SealedPayload, runClaim.PayloadKID)
	if err != nil {
		t.Fatal(err)
	}
	var inputs map[string]managedWorkflowDependencyInput
	if err := json.Unmarshal(decoded.Input, &inputs); err != nil {
		t.Fatalf("decode result-contract dependency inputs %s: %v", decoded.Input, err)
	}
	if inputs["collect"].Status != "contract_failed" || len(inputs["collect"].Result) != 0 ||
		inputs["test"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["test"].Result) != `{"ok":true}` {
		t.Fatalf("result-contract dependency input = %+v", inputs)
	}
	completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"summary":"partial"}`, "")

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-contract-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("complete result-contract workflow: %v", err)
	}
	job, err = jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil || job.Status != api.ManagedExecutionWorkflowFailed || job.NextStep != 3 ||
		!strings.Contains(job.LastError, `step "collect" result contract failed`) {
		t.Fatalf("terminal result-contract workflow = %+v, %v", job, err)
	}
	rows, err := state.ExecutionWorkflowStore(e.store).ListExecutionsByWorkflow(context.Background(), e.acct.ID, request.WorkflowID, nil, "", 10, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("Run receipts after result-contract failure = %+v, %v", rows, err)
	}
	for _, row := range rows {
		if strings.HasSuffix(row.StepLabel, ":collect") && row.Status != api.ExecutionStatusSucceeded {
			t.Fatalf("contract-failed Run receipt status changed to %s", row.Status)
		}
	}
}

func TestManagedExecutionWorkflowStagesArtifactFromEarlierStep(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-artifact-test", Version: "v1",
		Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "collect", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return {ok:true}", OutputFiles: []string{"patch.diff"}}},
			{Label: "review", ArtifactInputs: []api.ManagedExecutionWorkflowArtifactInput{{FromStep: "collect", Name: "patch.diff", Path: "input/patch.diff"}},
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Entrypoint: "main.py", Files: []api.ExecutionFile{{Path: "main.py", Content: []byte("print('review')")}}}},
		},
	}
	invalid := request
	invalid.Steps = append([]api.CreateManagedExecutionWorkflowStep(nil), request.Steps...)
	invalid.Steps[1].Request.Source = "print('review')"
	invalid.Steps[1].Request.Entrypoint = ""
	invalid.Steps[1].Request.Files = nil
	invalidResponse := e.do(t, http.MethodPost, "/v1/execution-workflows", invalid, map[string]string{"Idempotency-Key": "managed-artifact-invalid"})
	if invalidResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("managed artifact without an entrypoint bundle = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-artifact-create"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create artifact workflow = %d: %s", created.Code, created.Body.String())
	}
	var response api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobs := state.ExecutionWorkflowJobStore(e.store)
	claim, err := jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-artifact-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("admit artifact producer: %v", err)
	}
	runClaim, err := e.store.ClaimExecution(context.Background(), "managed-artifact-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || !strings.HasSuffix(runClaim.StepLabel, ":collect") {
		t.Fatalf("claim artifact producer = %q, %v", runClaim.StepLabel, err)
	}
	content := []byte("diff --git a/file b/file\\n+change\\n")
	digest := sha256.Sum256(content)
	completeManagedWorkflowClaimedRunWithArtifacts(t, e, identity, runClaim, `{"ok":true}`, []api.ExecutionArtifact{{
		Name: "patch.diff", SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:]), Content: content,
	}})

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-artifact-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("admit artifact consumer: %v", err)
	}
	runClaim, err = e.store.ClaimExecution(context.Background(), "managed-artifact-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || !strings.HasSuffix(runClaim.StepLabel, ":review") {
		t.Fatalf("claim artifact consumer = %q, %v", runClaim.StepLabel, err)
	}
	decoded, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, runClaim.SealedPayload, runClaim.PayloadKID)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Entrypoint != "main.py" || len(decoded.Files) != 2 || string(decoded.Files[0].Content) != "print('review')" ||
		decoded.Files[1].Path != "input/patch.diff" || string(decoded.Files[1].Content) != string(content) {
		t.Fatalf("artifact consumer bundle = %+v", decoded)
	}
	completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"reviewed":true}`, "")

	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-artifact-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("complete artifact workflow: %v", err)
	}
	job, err := jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil || job.Status != api.ManagedExecutionWorkflowSucceeded || job.NextStep != 2 {
		t.Fatalf("terminal artifact workflow = %+v, %v", job, err)
	}
}

func TestManagedWorkflowCannotConsumeFailedArtifactDependency(t *testing.T) {
	plan := api.CreateManagedExecutionWorkflowRequest{Steps: []api.CreateManagedExecutionWorkflowStep{
		{Label: "collect", Request: api.CreateExecutionRequest{OutputFiles: []string{"patch.diff"}}},
		{Label: "independent"},
		{Label: "summarize", IncludeDependencyResults: true,
			ArtifactInputs: []api.ManagedExecutionWorkflowArtifactInput{{FromStep: "collect", Name: "patch.diff", Path: "input/patch.diff"}}},
	}}
	step := plan.Steps[2]
	dependencies := []string{"collect", "independent"}
	observed := []managedWorkflowObservedStep{
		{Run: state.Execution{Status: api.ExecutionStatusFailed}, HasRun: true},
		{Run: state.Execution{Status: api.ExecutionStatusFailed}, HasRun: true},
		{},
	}
	if managedWorkflowCanConsumeFailedDependencies(step, dependencies, plan, observed) {
		t.Fatal("fan-in was allowed without its artifact-producing step")
	}
	observed[0].Run.Status = api.ExecutionStatusSucceeded
	if !managedWorkflowCanConsumeFailedDependencies(step, dependencies, plan, observed) {
		t.Fatal("fan-in was blocked after its artifact producer succeeded")
	}
	observed[0].ContractFailure = "result does not match its declared schema"
	if managedWorkflowCanConsumeFailedDependencies(step, dependencies, plan, observed) {
		t.Fatal("fan-in was allowed to consume an artifact from a contract-failed producer")
	}
}

func TestManagedExecutionWorkflowContinuesIndependentAfterFailure(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-partial-dag", Version: "v1", MaxParallelSteps: 2,
		FailurePolicy: api.ExecutionWorkflowManagedFailureContinueIndependent,
		Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "research", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return null"}},
			{Label: "test", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return true"}},
			{Label: "summarize", DependsOn: []string{"research", "test"}, IncludeDependencyResults: true,
				Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input"}},
		},
	}
	created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-partial-dag-create"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create managed partial DAG = %d: %s", created.Code, created.Body.String())
	}
	var response api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobs := state.ExecutionWorkflowJobStore(e.store)
	claim, err := jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-partial-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("drive partial DAG fan-out: %v", err)
	}
	for i := 0; i < 2; i++ {
		runClaim, claimErr := e.store.ClaimExecution(context.Background(), "managed-partial-test", time.Now().UTC().Add(time.Second), time.Minute)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if strings.HasSuffix(runClaim.StepLabel, ":research") {
			completeManagedWorkflowClaimedRunWithStatus(t, e, identity, runClaim, "", "", api.ExecutionStatusFailed)
		} else {
			completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"passed":true}`, "")
		}
	}
	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-partial-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("drive partial DAG fan-in: %v", err)
	}
	job, err := jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	join, err := state.ExecutionWorkflowStore(e.store).ExecutionWorkflowStepByLabel(context.Background(), e.acct.ID, request.WorkflowID, job.RunsPrincipalID,
		managedExecutionWorkflowStepLabel(response.PlanID, "summarize"))
	if err != nil {
		t.Fatalf("partial-result fan-in receipt = %+v, %v", join, err)
	}
	runClaim, err := e.store.ClaimExecution(context.Background(), "managed-partial-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || runClaim.ID != join.ID {
		t.Fatalf("claim partial-result fan-in Run = %q, %v; want %q", runClaim.ID, err, join.ID)
	}
	decoded, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, runClaim.SealedPayload, runClaim.PayloadKID)
	if err != nil {
		t.Fatal(err)
	}
	var inputs map[string]managedWorkflowDependencyInput
	if err := json.Unmarshal(decoded.Input, &inputs); err != nil {
		t.Fatalf("decode partial dependency results %s: %v", decoded.Input, err)
	}
	if inputs["research"].Status != string(api.ExecutionStatusFailed) || len(inputs["research"].Result) != 0 ||
		inputs["test"].Status != string(api.ExecutionStatusSucceeded) || string(inputs["test"].Result) != `{"passed":true}` {
		t.Fatalf("partial dependency input = %+v", inputs)
	}
	completeManagedWorkflowClaimedRun(t, e, identity, runClaim, `{"summary":"partial"}`, "")
	claim, err = jobs.ClaimExecutionWorkflowJob(context.Background(), "managed-partial-test", time.Now().UTC().Add(3*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.driveManagedExecutionWorkflow(context.Background(), claim); err != nil {
		t.Fatalf("finish partial DAG: %v", err)
	}
	job, err = jobs.ExecutionWorkflowJobByKey(context.Background(), e.acct.ID, request.WorkflowID, nil)
	if err != nil || job.Status != api.ManagedExecutionWorkflowFailed || job.NextStep != 3 {
		t.Fatalf("partial workflow terminal state = %+v, %v", job, err)
	}
}

func TestManagedExecutionWorkflowIsKeyFamilyScoped(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	owner := createRunsOnlyKey(t, e, "managed-workflow-owner")
	other := createRunsOnlyKey(t, e, "managed-workflow-other")
	request := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "key-family-private", Version: "v1",
		Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "step", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}}},
	}
	created := e.doAs(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-owner-submit"}, owner)
	if created.Code != http.StatusAccepted {
		t.Fatalf("owner submit = %d: %s", created.Code, created.Body.String())
	}
	for _, check := range []struct {
		key  string
		code int
	}{{owner, http.StatusOK}, {other, http.StatusNotFound}} {
		rec := e.doAs(t, http.MethodGet, "/v1/execution-workflows/"+request.WorkflowID, nil, nil, check.key)
		if rec.Code != check.code {
			t.Fatalf("workflow summary for key = %d, want %d: %s", rec.Code, check.code, rec.Body.String())
		}
	}
	broadPlan := request
	broadPlan.Version = "v2"
	broadCreate := e.do(t, http.MethodPost, "/v1/execution-workflows", broadPlan, map[string]string{"Idempotency-Key": "managed-broad-submit"})
	if broadCreate.Code != http.StatusAccepted {
		t.Fatalf("broad submit sharing workflow id = %d: %s", broadCreate.Code, broadCreate.Body.String())
	}
	var broadResponse api.ManagedExecutionWorkflowResponse
	if err := json.Unmarshal(broadCreate.Body.Bytes(), &broadResponse); err != nil {
		t.Fatal(err)
	}
	otherPlan := request
	otherPlan.Version = "v3"
	otherCreate := e.doAs(t, http.MethodPost, "/v1/execution-workflows", otherPlan, map[string]string{"Idempotency-Key": "managed-other-submit"}, other)
	if otherCreate.Code != http.StatusAccepted {
		t.Fatalf("other key-family submit sharing workflow id = %d: %s", otherCreate.Code, otherCreate.Body.String())
	}
	broadReplay := e.do(t, http.MethodPost, "/v1/execution-workflows", broadPlan, map[string]string{"Idempotency-Key": "managed-broad-replay"})
	var broadReplayResponse api.ManagedExecutionWorkflowResponse
	if broadReplay.Code != http.StatusAccepted || json.Unmarshal(broadReplay.Body.Bytes(), &broadReplayResponse) != nil || broadReplayResponse.PlanID != broadResponse.PlanID {
		t.Fatalf("broad retry replay = %d: %s", broadReplay.Code, broadReplay.Body.String())
	}
	broadSummary := e.do(t, http.MethodGet, "/v1/execution-workflows/"+request.WorkflowID, nil, nil)
	var broadSummaryResponse api.ExecutionWorkflowResponse
	if broadSummary.Code != http.StatusOK || json.Unmarshal(broadSummary.Body.Bytes(), &broadSummaryResponse) != nil || len(broadSummaryResponse.Managed) != 3 {
		t.Fatalf("broad workflow summary = %d: %s", broadSummary.Code, broadSummary.Body.String())
	}
}

func TestManagedExecutionWorkflowQueueIsBoundedPerAccount(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	previousIdentities := mfaIdentities
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { mfaIdentities = previousIdentities })
	requests := make([]api.CreateManagedExecutionWorkflowRequest, 0, state.ExecutionWorkflowManagedMaxActivePerAccount)
	for i := 0; i < state.ExecutionWorkflowManagedMaxActivePerAccount; i++ {
		request := api.CreateManagedExecutionWorkflowRequest{
			WorkflowID: "managed-limit-" + strconv.Itoa(i), Version: "v1",
			Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "step", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}}},
		}
		created := e.do(t, http.MethodPost, "/v1/execution-workflows", request, map[string]string{"Idempotency-Key": "managed-limit-" + strconv.Itoa(i)})
		if created.Code != http.StatusAccepted {
			t.Fatalf("create active workflow %d = %d: %s", i, created.Code, created.Body.String())
		}
		requests = append(requests, request)
	}
	replay := e.do(t, http.MethodPost, "/v1/execution-workflows", requests[0], map[string]string{"Idempotency-Key": "managed-limit-replay"})
	if replay.Code != http.StatusAccepted {
		t.Fatalf("duplicate active workflow replay = %d: %s", replay.Code, replay.Body.String())
	}
	overflow := api.CreateManagedExecutionWorkflowRequest{
		WorkflowID: "managed-limit-overflow", Version: "v1",
		Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "step", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return 1"}}},
	}
	full := e.do(t, http.MethodPost, "/v1/execution-workflows", overflow, map[string]string{"Idempotency-Key": "managed-limit-overflow"})
	if full.Code != http.StatusServiceUnavailable || !strings.Contains(full.Body.String(), "queue is full") {
		t.Fatalf("overflow workflow response = %d: %s", full.Code, full.Body.String())
	}
}

func completeManagedWorkflowRun(t *testing.T, e testEnv, identity *age.X25519Identity, executionID string, result string) {
	t.Helper()
	ctx := context.Background()
	claim, err := e.store.ClaimExecution(ctx, "managed-workflow-test", time.Now().UTC().Add(time.Second), time.Minute)
	if err != nil || claim.ID != executionID {
		t.Fatalf("claim managed Run = %q, %v; want %q", claim.ID, err, executionID)
	}
	completeManagedWorkflowClaimedRun(t, e, identity, claim, result, `{"ok":true}`)
}

func completeManagedWorkflowClaimedRun(t *testing.T, e testEnv, identity *age.X25519Identity, claim state.ExecutionClaim, result, expectedInput string) {
	completeManagedWorkflowClaimedRunWithStatus(t, e, identity, claim, result, expectedInput, api.ExecutionStatusSucceeded)
}

func completeManagedWorkflowClaimedRunWithStatus(t *testing.T, e testEnv, identity *age.X25519Identity, claim state.ExecutionClaim, result, expectedInput string, status api.ExecutionStatus) {
	completeManagedWorkflowClaimedRunWithStatusAndArtifacts(t, e, identity, claim, result, expectedInput, status, nil)
}

func completeManagedWorkflowClaimedRunWithArtifacts(t *testing.T, e testEnv, identity *age.X25519Identity, claim state.ExecutionClaim, result string, artifacts []api.ExecutionArtifact) {
	completeManagedWorkflowClaimedRunWithStatusAndArtifacts(t, e, identity, claim, result, "", api.ExecutionStatusSucceeded, artifacts)
}

func completeManagedWorkflowClaimedRunWithStatusAndArtifacts(t *testing.T, e testEnv, identity *age.X25519Identity, claim state.ExecutionClaim, result, expectedInput string, status api.ExecutionStatus, artifacts []api.ExecutionArtifact) {
	t.Helper()
	ctx := context.Background()
	decoded, err := executionpayload.DecodeRequest(ctx, []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil {
		t.Fatal(err)
	}
	if expectedInput != "" && strings.Contains(decoded.Source, "return input") && string(decoded.Input) != expectedInput {
		t.Fatalf("dependent Run input = %s, want %s", decoded.Input, expectedInput)
	}
	startedAt := time.Now().UTC().Add(time.Second)
	if _, err := e.store.MarkExecutionRunning(ctx, claim.ID, *claim.LeaseToken, startedAt); err != nil {
		t.Fatal(err)
	}
	params := state.CompleteExecutionParams{
		ID: claim.ID, LeaseToken: *claim.LeaseToken, Status: status,
		FinishedAt: startedAt.Add(time.Millisecond), Artifacts: artifacts,
	}
	if status == api.ExecutionStatusSucceeded {
		params.Result = json.RawMessage(result)
	} else {
		failureCode, failureMessage := "agent_test_failure", "synthetic workflow test failure"
		params.FailureCode = &failureCode
		params.FailureMessage = &failureMessage
	}
	_, err = e.store.CompleteExecution(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
}
