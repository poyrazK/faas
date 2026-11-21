//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostedRegionalStatus struct {
	RequestID string
	Status    string
	Outcome   string
	Recovery  *struct {
		RunSteps       []string
		RepeatSteps    []string
		ReuseSteps     []string
		ChildRequestID string
	}
	Summary struct {
		Orders int
		Units  int
		Cents  int `json:"revenue_cents"`
	}
}

type hostedRegionalProgress struct {
	Report   hostedRegionalStatus
	Pipeline *struct {
		NextSequence int64
		Terminal     bool
		Gap          bool
		Events       []api.ExecutionPipelineEvent
		Snapshot     *api.ExecutionPipelineProgress
	}
}

func exerciseHostedRegionalRecovery(t *testing.T, f *runFixture, db *pgx.Conn, object *reportObjectFixture,
	store *state.PgStore, downloadClient *http.Client, b hostedReportBackend, setup reportSetupReceipt) {
	t.Helper()
	ctx := t.Context()
	merge := setup.TaskPrefix + "-merge"
	zero, one := int64(0), int64(1)
	hold, err := f.client.SetRunBudget(ctx, "task", merge, api.SetRunBudgetRequest{
		Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &zero}})
	requireReport(t, err)
	orders, err := os.ReadFile("../../../examples/gregale-run/sales-report/rows.json")
	requireReport(t, err)
	var input struct{ Rows json.RawMessage }
	requireReport(t, json.Unmarshal(orders, &input))
	owner, parentID, childID := "acceptance-customer", uuid.NewString(), uuid.NewString()
	var submitted hostedRegionalStatus
	b.call(t, "submit-regional", parentID, owner, input.Rows, &submitted)
	if submitted.RequestID != parentID || (submitted.Status != "queued" && submitted.Status != "running") {
		t.Fatal("regional submission did not return an active report")
	}
	first := readHostedRegionalProgress(t, b, owner, parentID, 0)
	if first.Pipeline.Terminal || first.Pipeline.NextSequence == 0 || len(first.Pipeline.Events) == 0 {
		t.Fatal("expected a nonterminal customer progress page with a resumable cursor")
	}
	initialCursor := first.Pipeline.NextSequence
	t.Logf("regional recovery first live cursor %d", initialCursor)
	failed := waitHostedRegionalReport(t, b, owner, parentID, initialCursor, "failed")
	recovery := failed.Report.Recovery
	if failed.Report.Outcome != "failed" || failed.Pipeline.NextSequence <= initialCursor || recovery == nil ||
		!slices.Equal(recovery.RunSteps, []string{"merge"}) || !slices.Equal(recovery.ReuseSteps, []string{"north", "south"}) ||
		len(recovery.RepeatSteps) != 0 || recovery.ChildRequestID != "" {
		if failed.Pipeline.Snapshot != nil {
			for _, step := range failed.Pipeline.Snapshot.Steps {
				t.Logf("regional recovery snapshot: %s state=%s reason=%q", step.Label, step.State, step.Reason)
			}
		}
		t.Fatal("resumed progress did not show failed merge and both reuse candidates")
	}
	requireRegionalBranch(t, failed, "north", "succeeded", "")
	requireRegionalBranch(t, failed, "south", "succeeded", "")
	requireRegionalBranch(t, failed, "merge", "blocked", "admission_refused")
	parent, linked := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, parentID)
	if !linked || parent.Pipeline.Completion == nil || parent.Pipeline.Completion.Outcome != "failed" {
		t.Fatal("parent did not retain its failed pipeline receipt")
	}
	north := hostedRegionalStep(t, parent, "north")
	south := hostedRegionalStep(t, parent, "south")
	for _, step := range []api.ExecutionPipelineCompletedStep{north, south} {
		if step.Outcome != "succeeded" || len(step.Attempts) != 1 || step.OutputSHA256 == "" {
			t.Fatal("successful parent branch did not retain one attempt and its output fingerprint")
		}
	}
	if len(hostedRegionalStep(t, parent, "merge").Attempts) != 0 {
		t.Fatal("merge was physically executed despite the admission budget")
	}
	expectHostedRegionalRuns(t, f, store, north.Attempts[0].RunID, south.Attempts[0].RunID)
	var foreign *hostedRegionalProgress
	b.call(t, "progress", parentID, "other-customer", []byte("0"), &foreign)
	if foreign != nil {
		t.Fatal("another customer observed regional progress")
	}
	t.Logf("regional recovery resumed cursor %d through merge admission refusal", failed.Pipeline.NextSequence)

	_, err = f.client.SetRunBudget(ctx, "task", merge, api.SetRunBudgetRequest{ExpectedRevision: hold.Revision,
		Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &one}})
	requireReport(t, err)
	intent, err := json.Marshal(map[string]any{"requestID": childID, "repeatSteps": recovery.RepeatSteps})
	requireReport(t, err)
	var lost struct{ DroppedAcceptedResponse bool }
	b.call(t, "rerun-drop-ack", parentID, owner, intent, &lost)
	admitted, linked := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, childID)
	if !lost.DroppedAcceptedResponse || linked || admitted.Pipeline.Rerun == nil || admitted.Pipeline.Rerun.ParentID != parent.ID {
		t.Fatal("the child was not durably admitted before the response was dropped")
	}
	var retried hostedRegionalStatus
	b.call(t, "rerun", parentID, owner, intent, &retried)
	next, linked := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, childID)
	if retried.RequestID != childID || !linked || next.ID != admitted.ID {
		t.Fatal("exact UUID retry did not recover the admitted child")
	}
	done := waitHostedRegionalReport(t, b, owner, childID, 0, "succeeded")
	requireRegionalBranch(t, done, "north", "reused", "")
	requireRegionalBranch(t, done, "south", "reused", "")
	requireRegionalBranch(t, done, "merge", "succeeded", "")
	child, _ := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, childID)
	for _, step := range []api.ExecutionPipelineCompletedStep{north, south} {
		reused := hostedRegionalStep(t, child, step.Label)
		if reused.Outcome != "succeeded" || len(reused.Attempts) != 0 || reused.ReusedRunID != step.Attempts[0].RunID ||
			reused.OutputSHA256 != step.OutputSHA256 {
			t.Fatalf("child did not reuse the exact %s output", step.Label)
		}
	}
	merged := hostedRegionalStep(t, child, "merge")
	if merged.Outcome != "succeeded" || len(merged.Attempts) != 1 ||
		done.Report.Summary.Orders != 4 || done.Report.Summary.Units != 8 || done.Report.Summary.Cents != 6996 {
		t.Fatal("child did not execute a single successful merge")
	}
	want := []string{north.Attempts[0].RunID, south.Attempts[0].RunID, merged.Attempts[0].RunID}
	expectHostedRegionalRuns(t, f, store, want...)
	download := func() []byte {
		var signed api.ObjectSignedRequest
		b.call(t, "download", childID, owner, nil, &signed)
		if signed.Method != http.MethodGet || signed.URL == "" {
			t.Fatal("expected a private regional report download")
		}
		return fetchHostedReport(t, downloadClient, signed)
	}
	firstDownload := download()
	b.call(t, "rerun", parentID, owner, intent, &retried)
	replayed, _ := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, childID)
	parentAfter, _ := hostedRegionalWorkflow(t, f, db, b.endpoint, owner, parentID)
	if retried.RequestID != childID || retried.Status != "succeeded" || replayed.ID != child.ID ||
		!reflect.DeepEqual(replayed.Pipeline.Completion, child.Pipeline.Completion) ||
		!reflect.DeepEqual(parentAfter.Pipeline.Completion, parent.Pipeline.Completion) ||
		!bytes.Equal(firstDownload, download()) || !bytes.Contains(firstDownload, []byte("4 orders · 8 units · 6996 USD cents")) {
		t.Fatal("same-UUID replay changed the report, child, or original completion")
	}
	expectHostedRegionalRuns(t, f, store, want...)
	var workflows int
	requireReport(t, f.h.Pool.QueryRow(ctx, "SELECT count(*) FROM agent_execution_workflows WHERE account_id=$1", f.accountID).Scan(&workflows))
	if workflows != 2 {
		t.Fatalf("expected exactly one parent and one child workflow, got %d", workflows)
	}
	object.mu.Lock()
	writes, reads := object.writes, object.reads
	object.mu.Unlock()
	if writes != 1 || reads != 2 {
		t.Fatalf("expected one private delivery and two reads, got writes=%d reads=%d", writes, reads)
	}
	result := map[string]any{"schema": "gregale.report-agent-recovery/v1", "deployment_id": setup.DeploymentID,
		"parent_pipeline_id": parent.ID, "child_pipeline_id": child.ID, "child_request_id": childID,
		"initial_cursor": initialCursor, "resumed_terminal_cursor": failed.Pipeline.NextSequence, "live_progress": true,
		"merge_failure": "admission_refused", "parent_physical_runs": 2, "rerun_physical_runs": 1,
		"physical_runs_after_replay": 3, "workflow_count": workflows, "reused_steps": recovery.ReuseSteps, "same_uuid_reused_child": true,
		"lost_ack_recovered": true, "original_receipt_unchanged": true, "provider_writes": writes, "provider_reads": reads,
		"report_bytes": len(firstDownload), "report_sha256": fmt.Sprintf("%x", sha256.Sum256(firstDownload)), "revenue_cents": 6996}
	raw, err := json.Marshal(result)
	requireReport(t, err)
	requireReport(t, os.WriteFile(requiredHostedEnv(t, "FAAS_TEST_REPORT_MEASUREMENTS"), append(raw, '\n'), 0600))
	t.Log("regional recovery reused both branches; one merge, zero replayed Runs")
}

func readHostedRegionalProgress(t *testing.T, b hostedReportBackend, owner, id string, after int64) hostedRegionalProgress {
	t.Helper()
	var view hostedRegionalProgress
	b.call(t, "progress", id, owner, []byte(strconv.FormatInt(after, 10)), &view)
	if view.Report.RequestID != id || view.Pipeline == nil || view.Pipeline.Gap || view.Pipeline.NextSequence < after {
		t.Fatal("invalid customer progress page")
	}
	last := after
	for _, event := range view.Pipeline.Events {
		if event.Sequence != last+1 || event.Sequence > view.Pipeline.NextSequence ||
			(event.Step != nil && (event.Step.RunID != "" || event.Step.EventsURL != "")) {
			t.Fatal("resumed progress skipped or duplicated an event, or exposed a private Run reference")
		}
		last = event.Sequence
	}
	if last != view.Pipeline.NextSequence {
		t.Fatal("progress cursor skipped a report event")
	}
	return view
}

func waitHostedRegionalReport(t *testing.T, b hostedReportBackend, owner, id string, after int64, want string) hostedRegionalProgress {
	t.Helper()
	deadline := time.Now().Add(155 * time.Second)
	for time.Now().Before(deadline) {
		view := readHostedRegionalProgress(t, b, owner, id, after)
		after = view.Pipeline.NextSequence
		if view.Pipeline.Terminal && view.Report.Status == want {
			again := readHostedRegionalProgress(t, b, owner, id, after)
			if !again.Pipeline.Terminal || len(again.Pipeline.Events) != 0 {
				t.Fatal("terminal cursor replay returned events again")
			}
			return view
		}
		if view.Report.Status == "failed" && want != "failed" {
			t.Fatal("the recovery pipeline failed")
		}
		time.Sleep(750 * time.Millisecond)
	}
	t.Fatalf("report did not reach %s before the acceptance deadline", want)
	return hostedRegionalProgress{}
}

func requireRegionalBranch(t *testing.T, view hostedRegionalProgress, label, status, reason string) {
	t.Helper()
	if view.Pipeline.Snapshot != nil {
		for _, step := range view.Pipeline.Snapshot.Steps {
			if step.Label == label && step.State == status && (reason == "" || step.Reason == reason) &&
				step.RunID == "" && step.EventsURL == "" {
				return
			}
		}
	}
	t.Fatalf("expected customer snapshot branch %s in state %s with reason %s", label, status, reason)
}

func hostedRegionalWorkflow(t *testing.T, f *runFixture, db *pgx.Conn, endpoint, owner, requestID string) (api.ManagedExecutionWorkflowResponse, bool) {
	t.Helper()
	var workflow, local string
	requireReport(t, db.QueryRow(t.Context(), `
		SELECT p.workflow_id, COALESCE(p.pipeline_id::text, '') FROM report_requests r
		JOIN report_regional_pipelines p ON p.thread_id=r.thread_id
		WHERE r.endpoint_id=$1 AND r.customer_id=$2 AND r.request_id=$3`, endpoint, owner, requestID).
		Scan(&workflow, &local))
	out, err := f.client.GetExecutionWorkflow(t.Context(), workflow)
	requireReport(t, err)
	if len(out.Managed) != 1 || out.Managed[0].Pipeline == nil || (local != "" && local != out.Managed[0].ID) {
		t.Fatal("expected one managed pipeline for this regional request")
	}
	return out.Managed[0], local != ""
}

func hostedRegionalStep(t *testing.T, row api.ManagedExecutionWorkflowResponse, label string) api.ExecutionPipelineCompletedStep {
	t.Helper()
	if row.Pipeline.Completion != nil {
		for _, step := range row.Pipeline.Completion.Steps {
			if step.Label == label {
				return step
			}
		}
	}
	t.Fatalf("missing completed branch %s", label)
	return api.ExecutionPipelineCompletedStep{}
}

func expectHostedRegionalRuns(t *testing.T, f *runFixture, store *state.PgStore, expected ...string) {
	t.Helper()
	rows, err := store.ListExecutions(t.Context(), f.accountID, 10, 0)
	requireReport(t, err)
	if len(rows) != len(expected) {
		t.Fatalf("physical Run count is %d; want %d", len(rows), len(expected))
	}
	for _, row := range rows {
		if !slices.Contains(expected, row.ID) || row.Status != api.ExecutionStatusSucceeded {
			t.Fatal("unexpected physical Run identity or outcome")
		}
		f.runs[row.ID] = true
	}
}
