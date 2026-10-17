package issuetracker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type LinearOAuthTeam struct{ ID, Name string }
type LinearOAuthWorkspace struct {
	WorkspaceID, ViewerID string
	Teams                 []LinearOAuthTeam
}

func (c *Client) ListLinearOAuthTeams(ctx context.Context, access string) (LinearOAuthWorkspace, error) {
	result := LinearOAuthWorkspace{}
	cursor := ""
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for page := 0; page < api.IssueTrackerLinearOAuthTeamsMax/api.IssueTrackerLinearOAuthTeamsPage; page++ {
		var response struct {
			Data struct {
				Organization *struct {
					ID string `json:"id"`
				} `json:"organization"`
				Viewer *struct {
					ID string `json:"id"`
				} `json:"viewer"`
				Teams *struct {
					Nodes []struct {
						ID, Name   string
						ArchivedAt json.RawMessage `json:"archivedAt"`
					} `json:"nodes"`
					PageInfo *struct {
						HasNext *bool  `json:"hasNextPage"`
						Cursor  string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"teams"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		vars := map[string]any{"first": api.IssueTrackerLinearOAuthTeamsPage, "after": nil}
		if cursor != "" {
			vars["after"] = cursor
		}
		err := c.request(ctx, "linear", "POST", "/graphql", OAuthAuthorization(access), map[string]any{"query": "query GregaleOAuthTeams($first: Int!, $after: String) { organization { id } viewer { id } teams(first: $first, after: $after) { nodes { id name archivedAt } pageInfo { hasNextPage endCursor } } }", "variables": vars}, &response, false)
		d := response.Data
		if err != nil {
			return result, err
		}
		if len(response.Errors) > 0 || d.Organization == nil || d.Viewer == nil || d.Teams == nil || d.Teams.Nodes == nil || d.Teams.PageInfo == nil || d.Teams.PageInfo.HasNext == nil || len(d.Teams.Nodes) > api.IssueTrackerLinearOAuthTeamsPage {
			return result, errors.New("linear workspace and teams could not be confirmed")
		}
		for _, id := range []string{d.Organization.ID, d.Viewer.ID} {
			parsed, e := uuid.Parse(id)
			if e != nil || parsed == uuid.Nil || parsed.String() != id {
				return result, errors.New("linear OAuth identity is invalid")
			}
		}
		if page > 0 && (result.WorkspaceID != d.Organization.ID || result.ViewerID != d.Viewer.ID) {
			return result, errors.New("linear OAuth identity changed during pagination")
		}
		result.WorkspaceID, result.ViewerID = d.Organization.ID, d.Viewer.ID
		for _, n := range d.Teams.Nodes {
			id, e := uuid.Parse(n.ID)
			if e != nil || id == uuid.Nil || id.String() != n.ID || seen[n.ID] || len(n.ArchivedAt) == 0 || !utf8.ValidString(n.Name) || strings.TrimSpace(n.Name) == "" || len(n.Name) > api.IssueTrackerLinearWorkflowNameMaxBytes || strings.ContainsFunc(n.Name, unicode.IsControl) {
				return result, errors.New("linear team catalog is invalid")
			}
			seen[n.ID] = true
			if string(n.ArchivedAt) == "null" {
				result.Teams = append(result.Teams, LinearOAuthTeam{n.ID, n.Name})
			}
		}
		if !*d.Teams.PageInfo.HasNext {
			sort.Slice(result.Teams, func(i, j int) bool {
				if result.Teams[i].Name == result.Teams[j].Name {
					return result.Teams[i].ID < result.Teams[j].ID
				}
				return result.Teams[i].Name < result.Teams[j].Name
			})
			return result, nil
		}
		cursor = d.Teams.PageInfo.Cursor
		if cursor == "" || len(cursor) > api.IssueTrackerLinearWorkflowCursorMaxBytes || cursors[cursor] {
			return result, errors.New("linear team pagination is invalid")
		}
		cursors[cursor] = true
	}
	return result, errors.New("linear team catalog exceeds the supported bound")
}
