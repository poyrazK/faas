package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrUnsupportedStatus = errors.New("tracker status cannot be mapped to open or resolved")
var ErrStatusChanged = errors.New("tracker status changed during synchronization; preview again")

type Status struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	ID       string `json:"id"`
	State    string `json:"state"`
	TeamID   string `json:"team_id,omitempty"`
	StateID  string `json:"state_id,omitempty"`
}
type StatusProvider interface {
	ReadStatus(context.Context, string, string) (Status, error)
	SetStatus(context.Context, Status, string, string, string) (Status, error)
}

type githubStatus struct {
	githubIssue
	State  string `json:"state"`
	Reason string `json:"state_reason"`
}

func githubStatusRow(expected string, row githubStatus) (Status, error) {
	normalized, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: expected})
	if err != nil {
		return Status{}, err
	}
	target := strings.SplitN(normalized.ExternalKey, "#", 2)[0]
	ticket, err := githubTicket(Request{Target: target}, row.githubIssue)
	if err != nil || ticket.URL != normalized.URL {
		return Status{}, errors.New("GitHub returned a different ticket")
	}
	state := ""
	switch row.State {
	case "open":
		state = "open"
	case "closed":
		if row.Reason == "completed" || row.Reason == "" {
			state = "resolved"
		}
	}
	if state == "" {
		return Status{}, ErrUnsupportedStatus
	}
	return Status{Provider: "github", URL: ticket.URL, ID: ticket.ProviderID, State: state}, nil
}

type linearStatus struct {
	linearIssue
	ArchivedAt json.RawMessage `json:"archivedAt"`
	State      struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"state"`
}
type workflowState struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	ArchivedAt json.RawMessage `json:"archivedAt"`
	Team       struct {
		ID string `json:"id"`
	} `json:"team"`
}
type statusResponse struct {
	Data struct {
		Issue    *linearStatus  `json:"issue"`
		Workflow *workflowState `json:"workflowState"`
		Update   struct {
			Success bool          `json:"success"`
			Issue   *linearStatus `json:"issue"`
		} `json:"issueUpdate"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

func mappedLinearState(kind string) string {
	switch kind {
	case "backlog", "unstarted", "started":
		return "open"
	case "completed":
		return "resolved"
	}
	return ""
}
func linearStatusRow(expected string, row *linearStatus) (Status, error) {
	if row == nil {
		return Status{}, errors.New("linear did not return the linked issue")
	}
	if len(row.ArchivedAt) == 0 {
		return Status{}, errors.New("linear archive status was not confirmed")
	}
	if strings.TrimSpace(string(row.ArchivedAt)) != "null" {
		return Status{}, ErrUnsupportedStatus
	}
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: row.URL})
	wanted, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: expected})
	id, e1 := uuid.Parse(row.ID)
	team, e2 := uuid.Parse(row.Team.ID)
	state, e3 := uuid.Parse(row.State.ID)
	if err != nil || e != nil || n.Provider != "linear" || n.URL != wanted.URL || !strings.HasSuffix(n.URL, "/issue/"+row.Identifier) || e1 != nil || e2 != nil || e3 != nil || id == uuid.Nil || team == uuid.Nil || state == uuid.Nil {
		return Status{}, errors.New("linear returned an inconsistent linked issue")
	}
	mapped := mappedLinearState(row.State.Type)
	if mapped == "" {
		return Status{}, ErrUnsupportedStatus
	}
	return Status{Provider: "linear", URL: n.URL, ID: id.String(), TeamID: team.String(), StateID: state.String(), State: mapped}, nil
}

const linearStatusFields = "id url identifier archivedAt team { id } state { id type }"

func (c *Client) ReadStatus(ctx context.Context, url, token string) (Status, error) {
	normalized, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: url})
	if err != nil {
		return Status{}, errors.New("invalid linked tracker URL")
	}
	if normalized.Provider == "github" {
		parts := strings.SplitN(normalized.ExternalKey, "#", 2)
		var row githubStatus
		if err = c.request(ctx, "github", "GET", "/repos/"+parts[0]+"/issues/"+parts[1], token, nil, &row, false); err != nil {
			return Status{}, err
		}
		return githubStatusRow(normalized.URL, row)
	}
	parts := strings.SplitN(normalized.ExternalKey, "/", 2)
	var out statusResponse
	err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "query GregaleLinkedIssue($id: String!) { issue(id: $id) { " + linearStatusFields + " } }", "variables": map[string]string{"id": parts[1]}}, &out, false)
	if err != nil {
		return Status{}, err
	}
	if len(out.Errors) > 0 {
		return Status{}, errors.New("linear status could not be confirmed")
	}
	return linearStatusRow(normalized.URL, out.Data.Issue)
}

// SetStatus rereads the exact linked ticket and refuses a changed snapshot.
// Provider APIs do not offer a shared atomic compare-and-swap with Gregale;
// another writer can still race after this check. Callers must select authority.
func (c *Client) SetStatus(ctx context.Context, observed Status, token, wanted, linearStateID string) (Status, error) {
	if wanted != "open" && wanted != "resolved" {
		return Status{}, ErrUnsupportedStatus
	}
	fresh, err := c.ReadStatus(ctx, observed.URL, token)
	if err != nil {
		return Status{}, err
	}
	if fresh != observed {
		return Status{}, ErrStatusChanged
	}
	if fresh.State == wanted {
		return fresh, nil
	}
	if fresh.Provider == "github" {
		if linearStateID != "" {
			return Status{}, errors.New("linear state selection does not apply to GitHub")
		}
		state, reason := "open", any(nil)
		if wanted == "resolved" {
			state, reason = "closed", "completed"
		}
		n, _ := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: fresh.URL})
		parts := strings.SplitN(n.ExternalKey, "#", 2)
		var row githubStatus
		err = c.request(ctx, "github", "PATCH", "/repos/"+parts[0]+"/issues/"+parts[1], token, map[string]any{"state": state, "state_reason": reason}, &row, false)
		if err != nil {
			return Status{}, err
		}
		result, e := githubStatusRow(fresh.URL, row)
		if e != nil || result.State != wanted {
			return Status{}, errors.New("GitHub status update was not confirmed")
		}
		return result, nil
	}
	stateID, e := uuid.Parse(linearStateID)
	if e != nil || stateID == uuid.Nil {
		return Status{}, errors.New("linear push requires the selected workflow state UUID")
	}
	if err := c.VerifyLinearWorkflowState(ctx, token, fresh.TeamID, stateID.String(), wanted); err != nil {
		return Status{}, err
	}
	var out statusResponse
	err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "mutation GregaleIssueStatus($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success issue { " + linearStatusFields + " } } }", "variables": map[string]any{"id": fresh.ID, "input": map[string]string{"stateId": stateID.String()}}}, &out, false)
	if err != nil {
		return Status{}, err
	}
	if len(out.Errors) > 0 || !out.Data.Update.Success {
		return Status{}, errors.New("linear status update was not confirmed")
	}
	result, e := linearStatusRow(fresh.URL, out.Data.Update.Issue)
	if e != nil || result.ID != fresh.ID || result.TeamID != fresh.TeamID || result.State != wanted || result.StateID != stateID.String() {
		return Status{}, errors.New("linear status update returned inconsistent evidence")
	}
	return result, nil
}
