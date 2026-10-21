package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Inventory includes retained inactive assignments. Admission selection keeps
// using ApplicationStandardEnrollmentStore's active-only assignment reader.
type ApplicationStandardAssignmentInventoryStore interface {
	GetApplicationStandardAssignmentRecord(context.Context, string, string) (api.ApplicationStandardAssignment, error)
	ListApplicationStandardAssignmentRecords(context.Context, string, string, int) ([]api.ApplicationStandardAssignment, error)
}

func standardAssignmentInventoryRecord(row applicationStandardAssignmentRecord) api.ApplicationStandardAssignment {
	return api.ApplicationStandardAssignment{ID: canonicalStandardUUID(row.ID), OrgID: canonicalStandardUUID(row.OrgID), Scope: row.Scope, ScopeID: canonicalStandardUUID(row.ScopeID), StandardID: canonicalStandardUUID(row.StandardID), AdmissionVersion: row.AdmissionVersion, Revision: row.Revision, Active: row.Active, CreatedBy: canonicalStandardUUID(row.CreatedBy), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
