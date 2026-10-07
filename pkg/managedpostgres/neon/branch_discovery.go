// adr: 590 — complete, bounded branch identity discovery.
package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const maximumBranchSearchPages = 10

// Branch pagination uses an explicit next cursor, unlike project/operation
// lists. Read every page before establishing uniqueness or absence. Sorting
// on creation time prevents asynchronous state updates from reordering pages.
func (p *Provider) findOwnedBranch(ctx context.Context, projectID, id, name string) (branch, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/branches"
	if id != "" {
		var response struct {
			Branch branch `json:"branch"`
		}
		if err := p.doJSON(ctx, http.MethodGet, path+"/"+url.PathEscape(id), nil, nil, &response, http.StatusOK); err != nil {
			return branch{}, err
		}
		if response.Branch.ID != id || response.Branch.Name != "" && response.Branch.Name != name {
			return branch{}, managedpostgres.ErrConflict
		}
		return response.Branch, nil
	}
	branches, err := p.listProjectBranches(ctx, projectID, name)
	if err != nil {
		return branch{}, err
	}
	var found branch
	for _, candidate := range branches {
		if candidate.Name != name {
			continue
		}
		if found.ID != "" {
			return branch{}, managedpostgres.ErrConflict
		}
		found = candidate
	}
	if found.ID == "" {
		return branch{}, managedpostgres.ErrNotFound
	}
	return found, nil
}

func (p *Provider) listProjectBranches(ctx context.Context, projectID, name string) ([]branch, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/branches"
	seen, seenIDs := map[string]bool{}, map[string]bool{}
	cursor := ""
	branches := []branch{}
	for range maximumBranchSearchPages {
		var response struct {
			Branches   []branch `json:"branches"`
			Pagination struct {
				Next      string `json:"next"`
				SortBy    string `json:"sort_by"`
				SortOrder string `json:"sort_order"`
			} `json:"pagination"`
		}
		query := url.Values{"limit": {"10000"}, "sort_by": {"created_at"}, "sort_order": {"asc"}}
		if name != "" {
			query.Set("search", name)
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := p.doJSON(ctx, http.MethodGet, path, query, nil, &response, http.StatusOK); err != nil {
			return nil, err
		}
		if response.Branches == nil || response.Pagination.SortBy != "" && response.Pagination.SortBy != "created_at" ||
			response.Pagination.SortOrder != "" && response.Pagination.SortOrder != "asc" {
			return nil, managedpostgres.ErrUnavailable
		}
		for _, candidate := range response.Branches {
			if !validProviderID.MatchString(candidate.ID) || seenIDs[candidate.ID] || candidate.ProjectID != "" && candidate.ProjectID != projectID {
				return nil, managedpostgres.ErrConflict
			}
			seenIDs[candidate.ID] = true
			branches = append(branches, candidate)
		}
		cursor = response.Pagination.Next
		if cursor == "" {
			return branches, nil
		}
		if seen[cursor] {
			return nil, managedpostgres.ErrUnavailable
		}
		seen[cursor] = true
	}
	return nil, managedpostgres.ErrUnavailable
}
