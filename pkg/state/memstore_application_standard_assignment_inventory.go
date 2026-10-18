package state

import (
	"context"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardAssignmentInventoryStore = (*MemStore)(nil)

func (m *MemStore) GetApplicationStandardAssignmentRecord(ctx context.Context, orgID, id string) (api.ApplicationStandardAssignment, error) {
	if !validStandardResourceRead(orgID, id) {
		return api.ApplicationStandardAssignment{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return api.ApplicationStandardAssignment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.applicationStandardAssignments {
		if sameStandardUUID(row.OrgID, orgID) && sameStandardUUID(row.ID, id) {
			return standardAssignmentInventoryRecord(row), nil
		}
	}
	return api.ApplicationStandardAssignment{}, ErrNotFound
}

func (m *MemStore) ListApplicationStandardAssignmentRecords(ctx context.Context, orgID, after string, limit int) ([]api.ApplicationStandardAssignment, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if after != "" {
		after = canonicalStandardUUID(after)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.ApplicationStandardAssignment{}
	for _, row := range m.applicationStandardAssignments {
		if sameStandardUUID(row.OrgID, orgID) && canonicalStandardUUID(row.ID) > after {
			rows = append(rows, standardAssignmentInventoryRecord(row))
		}
	}
	slices.SortFunc(rows, func(a, b api.ApplicationStandardAssignment) int { return strings.Compare(a.ID, b.ID) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
