package issuetracker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// LinearIdentity is immutable provider identity, not a user-supplied URL slug.
type LinearIdentity struct {
	WorkspaceID string `json:"workspace_id"`
	TeamID      string `json:"team_id"`
	ViewerID    string `json:"viewer_id"`
}

// VerifyLinearConnection verifies the credential and explicit team selection.
// OAuth callers supply OAuthAuthorization after scoped refresh resolution.
func (c *Client) VerifyLinearConnection(ctx context.Context, token, teamID string) (LinearIdentity, error) {
	id, err := uuid.Parse(teamID)
	if err != nil || id == uuid.Nil {
		return LinearIdentity{}, errors.New("linear team must be a nonzero UUID")
	}
	var out struct {
		Data struct {
			Organization *struct {
				ID string `json:"id"`
			} `json:"organization"`
			Viewer *struct {
				ID string `json:"id"`
			} `json:"viewer"`
			Team *struct {
				ID         string          `json:"id"`
				ArchivedAt json.RawMessage `json:"archivedAt"`
			} `json:"team"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	err = c.request(ctx, "linear", "POST", "/graphql", token, map[string]any{
		"query":     "query GregaleLinearConnection($id: String!) { organization { id } viewer { id } team(id: $id) { id archivedAt } }",
		"variables": map[string]string{"id": id.String()},
	}, &out, false)
	if err != nil {
		return LinearIdentity{}, err
	}
	if len(out.Errors) != 0 || out.Data.Organization == nil || out.Data.Viewer == nil || out.Data.Team == nil || string(out.Data.Team.ArchivedAt) != "null" {
		return LinearIdentity{}, errors.New("linear connection identity could not be confirmed")
	}
	workspace, e1 := uuid.Parse(out.Data.Organization.ID)
	viewer, e2 := uuid.Parse(out.Data.Viewer.ID)
	team, e3 := uuid.Parse(out.Data.Team.ID)
	if e1 != nil || e2 != nil || e3 != nil || workspace == uuid.Nil || viewer == uuid.Nil || team != id {
		return LinearIdentity{}, errors.New("linear returned inconsistent connection identity")
	}
	return LinearIdentity{WorkspaceID: workspace.String(), TeamID: team.String(), ViewerID: viewer.String()}, nil
}
