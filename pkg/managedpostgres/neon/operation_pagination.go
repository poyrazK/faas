package neon

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Operation history includes automatic suspend/wake work, so its budget is
// larger than project discovery's. Request deadlines still bound elapsed time.
const maximumOperationPages = 200

// A cursor identifies the last returned operation, even on the terminal page.
// Establish completeness before using operations to authorize readiness or DDL.
func (p *Provider) listProjectOperations(ctx context.Context, projectID string) ([]operation, error) {
	ops := []operation{}
	seenCursors, seenIDs := map[string]bool{}, map[string]bool{}
	cursor := ""
	for range maximumOperationPages {
		query := url.Values{"limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response operationsResponse
		if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/operations", query, nil, &response, http.StatusOK); err != nil {
			return nil, err
		}
		if response.Operations == nil {
			return nil, managedpostgres.ErrUnavailable
		}
		for _, op := range response.Operations {
			if op.ID == "" || seenIDs[op.ID] || op.ProjectID != "" && op.ProjectID != projectID {
				return nil, managedpostgres.ErrConflict
			}
			seenIDs[op.ID] = true
			ops = append(ops, op)
		}
		if len(response.Operations) == 0 || response.Pagination.Cursor == "" {
			return ops, nil
		}
		cursor = response.Pagination.Cursor
		if seenCursors[cursor] {
			return nil, managedpostgres.ErrUnavailable
		}
		seenCursors[cursor] = true
	}
	return nil, managedpostgres.ErrUnavailable
}
