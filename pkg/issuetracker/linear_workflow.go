package issuetracker

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// LinearWorkflowVerifier verifies a selected workflow without changing an issue.
// Automatic reconciliation must verify both mappings before accepting delegation.
type LinearWorkflowVerifier interface {
	VerifyLinearWorkflowState(context.Context, string, string, string, string) error
}

// VerifyLinearWorkflowState requires an active state in the connected team with
// the selected Gregale lifecycle. It never sends a provider mutation.
func (c *Client) VerifyLinearWorkflowState(ctx context.Context, token, teamID, stateID, wanted string) error {
	team, teamErr := uuid.Parse(teamID)
	selected, stateErr := uuid.Parse(stateID)
	if teamErr != nil || stateErr != nil || team == uuid.Nil || selected == uuid.Nil || (wanted != "open" && wanted != "resolved") {
		return ErrUnsupportedStatus
	}
	var workflow statusResponse
	err := c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "query GregaleWorkflowState($id: String!) { workflowState(id: $id) { id type archivedAt team { id } } }", "variables": map[string]string{"id": selected.String()}}, &workflow, false)
	if err != nil {
		return err
	}
	w := workflow.Data.Workflow
	if len(workflow.Errors) > 0 || w == nil || w.ID != selected.String() || w.Team.ID != team.String() || len(w.ArchivedAt) == 0 || strings.TrimSpace(string(w.ArchivedAt)) != "null" || mappedLinearState(w.Type) != wanted {
		return errors.New("linear workflow state must be active, belong to the linked issue's team and match the desired lifecycle")
	}
	return nil
}
