package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// WorkflowRunEnd is a finished agent step run whose end schedd has not yet
// delivered to its workflow run (ADR-20261027090010 §9).
type WorkflowRunEnd struct {
	Link   WorkflowLink
	Output api.WorkflowAgentStepOutput
}

// UndeliveredWorkflowRunEnds lists up to limit finished agent step runs whose
// active workflow run has no event for them yet. The event schedd records is
// the receipt, so a delivery interrupted before it is retried and one that
// committed is not repeated.
func (s *Store) UndeliveredWorkflowRunEnds(ctx context.Context, limit int) ([]WorkflowRunEnd, error) {
	rows, err := s.q.ListUndeliveredWorkflowAgentRuns(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list undelivered workflow agent runs: %w", err)
	}
	out := make([]WorkflowRunEnd, 0, len(rows))
	for _, row := range rows {
		end := WorkflowRunEnd{Link: WorkflowLink{RunID: row.WorkflowRunID, Step: row.WorkflowStep, Event: row.WorkflowEvent},
			Output: api.WorkflowAgentStepOutput{RunID: row.ID, State: row.State}}
		if row.OutcomeCode.Valid {
			end.Output.Outcome = &api.AgentRunOutcome{Code: row.OutcomeCode.String, Message: row.OutcomeMessage.String}
		}
		if row.State == StateSucceeded && len(row.Result) > 0 {
			if len(row.Result) <= api.WorkflowAgentStepResultMaxBytes {
				end.Output.Result = json.RawMessage(row.Result)
			} else {
				end.Output.ResultOmitted = true
			}
		}
		out = append(out, end)
	}
	return out, nil
}
