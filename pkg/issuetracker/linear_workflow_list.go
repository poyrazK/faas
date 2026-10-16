package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type LinearWorkflowState struct {
	ID, TeamID, Name, Type, State string
}

type LinearWorkflowLister interface {
	ListLinearWorkflowStates(context.Context, string, string) ([]LinearWorkflowState, error)
}

type linearWorkflowPage struct {
	Data struct {
		Team *struct {
			ID         string          `json:"id"`
			ArchivedAt json.RawMessage `json:"archivedAt"`
			States     *struct {
				Nodes []struct {
					workflowState
					Name string `json:"name"`
				} `json:"nodes"`
				PageInfo *struct {
					HasNextPage *bool  `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"states"`
		} `json:"team"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// NormalizeLinearWorkflowStates validates a credential-free catalog from a scoped
// provider, copies it, and sorts by lifecycle/name/ID for stable picker choices.
func NormalizeLinearWorkflowStates(states []LinearWorkflowState, teamID string) ([]LinearWorkflowState, error) {
	team, err := uuid.Parse(teamID)
	if err != nil || team == uuid.Nil || team.String() != teamID || len(states) > api.IssueTrackerLinearWorkflowMax {
		return nil, ErrUnsupportedStatus
	}
	seen := map[string]bool{}
	out := make([]LinearWorkflowState, 0, len(states))
	for _, state := range states {
		id, e := uuid.Parse(state.ID)
		if e != nil || id == uuid.Nil || id.String() != state.ID || state.TeamID != teamID || seen[state.ID] || mappedLinearState(state.Type) == "" || mappedLinearState(state.Type) != state.State || !utf8.ValidString(state.Name) || len(state.Name) > api.IssueTrackerLinearWorkflowNameMaxBytes || strings.TrimSpace(state.Name) == "" || strings.ContainsFunc(state.Name, unicode.IsControl) {
			return nil, ErrUnsupportedStatus
		}
		seen[state.ID] = true
		state.Name = strings.TrimSpace(state.Name)
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.State != b.State {
			return a.State < b.State
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	return out, nil
}

// ListLinearWorkflowStates reads bounded pages of the connected team's states.
// Archived/canceled/unfamiliar states are omitted; incomplete scope evidence or
// pagination returns no partial catalog. This method never sends a mutation.
func (c *Client) ListLinearWorkflowStates(ctx context.Context, token, teamID string) ([]LinearWorkflowState, error) {
	team, err := uuid.Parse(teamID)
	if err != nil || team == uuid.Nil {
		return nil, ErrUnsupportedStatus
	}
	teamID = team.String()
	states := []LinearWorkflowState{}
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	scanned := 0
	for page := 0; page < api.IssueTrackerLinearWorkflowMax/api.IssueTrackerLinearWorkflowPageSize; page++ {
		var response linearWorkflowPage
		variables := map[string]any{"id": teamID, "first": api.IssueTrackerLinearWorkflowPageSize, "after": nil}
		if cursor != "" {
			variables["after"] = cursor
		}
		err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{"query": "query GregaleTeamWorkflows($id: String!, $first: Int!, $after: String) { team(id: $id) { id archivedAt states(first: $first, after: $after) { nodes { id name type archivedAt team { id } } pageInfo { hasNextPage endCursor } } } }", "variables": variables}, &response, false)
		if err != nil {
			return nil, err
		}
		row := response.Data.Team
		if len(response.Errors) > 0 || row == nil || row.ID != teamID || strings.TrimSpace(string(row.ArchivedAt)) != "null" || row.States == nil || row.States.Nodes == nil || row.States.PageInfo == nil || row.States.PageInfo.HasNextPage == nil || len(row.States.Nodes) > api.IssueTrackerLinearWorkflowPageSize {
			return nil, errors.New("linear workflow catalog scope could not be confirmed")
		}
		scanned += len(row.States.Nodes)
		if scanned > api.IssueTrackerLinearWorkflowMax {
			return nil, errors.New("linear workflow catalog exceeds the supported bound")
		}
		for _, node := range row.States.Nodes {
			id, e := uuid.Parse(node.ID)
			if e != nil || id == uuid.Nil || id.String() != node.ID || node.Team.ID != teamID || seenIDs[node.ID] || len(node.ArchivedAt) == 0 {
				return nil, errors.New("linear workflow catalog returned inconsistent identities")
			}
			seenIDs[node.ID] = true
			if strings.TrimSpace(string(node.ArchivedAt)) != "null" || mappedLinearState(node.Type) == "" {
				continue
			}
			states = append(states, LinearWorkflowState{ID: node.ID, TeamID: teamID, Name: node.Name, Type: node.Type, State: mappedLinearState(node.Type)})
		}
		if !*row.States.PageInfo.HasNextPage {
			return NormalizeLinearWorkflowStates(states, teamID)
		}
		cursor = row.States.PageInfo.EndCursor
		if len(row.States.Nodes) == 0 || cursor == "" || len(cursor) > api.IssueTrackerLinearWorkflowCursorMaxBytes || strings.ContainsFunc(cursor, unicode.IsControl) || seenCursors[cursor] {
			return nil, errors.New("linear workflow catalog pagination could not be confirmed")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("linear workflow catalog exceeds the supported page bound")
}
