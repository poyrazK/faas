package runpipeline_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

type usageReader func(context.Context, string, []string) (map[string]state.RunUsageRecord, error)

func (r usageReader) PipelineRunUsage(ctx context.Context, account string, runs []string) (map[string]state.RunUsageRecord, error) {
	return r(ctx, account, runs)
}

func TestReadUsageReportAgentRecovery(t *testing.T) {
	admitted := time.Date(2026, time.November, 21, 10, 0, 0, 0, time.UTC)
	started := admitted.Add(250 * time.Millisecond)
	finished := started.Add(500 * time.Millisecond)
	ledger := map[string]state.RunUsageRecord{}
	for _, id := range []string{"north", "south", "merge-first", "merge-rerun"} {
		ledger[id] = state.RunUsageRecord{CreatedAt: admitted, StartedAt: &started, FinishedAt: finished, WallTimeMS: 500}
	}
	step := func(label, id string, status api.ExecutionStatus, bytes int) api.ExecutionPipelineCompletedStep {
		out := api.ExecutionPipelineCompletedStep{Label: label, Outcome: string(status), Attempts: []api.ExecutionPipelineCompletedAttempt{
			{RunID: id, Attempt: 1, Status: status},
		}}
		if bytes > 0 {
			out.Attempts[0].Artifacts = []api.ExecutionPipelineArtifactRef{{Name: "report.html", SizeBytes: bytes}}
		}
		return out
	}
	read := func(receipt *api.ExecutionPipelineResponse, wantIDs []string) runpipeline.UsageBreakdown {
		t.Helper()
		r := usageReader(func(_ context.Context, account string, ids []string) (map[string]state.RunUsageRecord, error) {
			if account != "report-account" || !slices.Equal(ids, wantIDs) {
				t.Fatalf("usage lookup for %q with %v, want %v", account, ids, wantIDs)
			}
			records := make(map[string]state.RunUsageRecord, len(ids))
			for _, id := range ids {
				records[id] = ledger[id]
			}
			return records, nil
		})
		usage, err := runpipeline.ReadUsage(t.Context(), r, "report-account", receipt)
		if err != nil {
			t.Fatal(err)
		}
		return usage
	}
	parent := &api.ExecutionPipelineResponse{Completion: &api.ExecutionPipelineCompletion{Steps: []api.ExecutionPipelineCompletedStep{
		step("north", "north", api.ExecutionStatusSucceeded, 100), step("south", "south", api.ExecutionStatusSucceeded, 120),
		step("merge", "merge-first", api.ExecutionStatusCancelled, 0),
	}}}
	first := read(parent, []string{"north", "south", "merge-first"})
	if first.NewRuns != 3 || first.SettledRuns != 3 || first.ReusedSteps != 0 || first.WallTimeMS != 1500 || first.OutputBytes != 220 {
		t.Fatalf("original report usage = %+v", first)
	}
	merge := step("merge", "merge-rerun", api.ExecutionStatusSucceeded, 400)
	merge.Attempts = append(merge.Attempts, merge.Attempts[0])
	child := &api.ExecutionPipelineResponse{Completion: &api.ExecutionPipelineCompletion{Steps: []api.ExecutionPipelineCompletedStep{
		{Label: "north", Outcome: "succeeded", ReusedRunID: "north"},
		{Label: "south", Outcome: "succeeded", ReusedRunID: "south"},
		merge,
	}}}
	recovered := read(child, []string{"merge-rerun"})
	if recovered.NewRuns != 1 || recovered.SettledRuns != 1 || recovered.ReusedSteps != 2 ||
		recovered.RetryRuns != 0 || recovered.WallTimeMS != 500 || recovered.OutputBytes != 400 || recovered.OutputCount != 1 {
		t.Fatalf("recovered report usage = %+v", recovered)
	}
	attempt := recovered.Attempts[0]
	if attempt.QueueTime == nil || *attempt.QueueTime != 250*time.Millisecond ||
		attempt.OutputAt == nil || !attempt.OutputAt.Equal(finished) || attempt.Execution != 500*time.Millisecond {
		t.Fatalf("recovered report timings = %+v", attempt)
	}
	if replay := read(child, []string{"merge-rerun"}); !reflect.DeepEqual(replay, recovered) {
		t.Fatalf("replayed report usage = %+v", replay)
	}
}

func TestReadUsageIncompleteLedger(t *testing.T) {
	view := &api.ExecutionPipelineResponse{Steps: []api.ExecutionPipelineStepResponse{
		{Label: "render", Attempts: []api.ExecutionPipelineAttemptResponse{
			{RunID: "pruned", Attempt: 1, Status: api.ExecutionStatusFailed},
			{RunID: "active", Attempt: 2, Status: api.ExecutionStatusRunning},
		}},
	}}
	usage, err := runpipeline.ReadUsage(t.Context(), nil, "account", view)
	if err != nil || usage.NewRuns != 2 || usage.RetryRuns != 1 || usage.SettledRuns != 0 ||
		usage.UnavailableRuns != 1 || usage.PendingRuns != 1 || usage.Attempts[0].LedgerState != "Unavailable" ||
		usage.Attempts[1].LedgerState != "Pending" || usage.Attempts[1].QueueTime != nil {
		t.Fatalf("incomplete usage = %+v, %v", usage, err)
	}
}
