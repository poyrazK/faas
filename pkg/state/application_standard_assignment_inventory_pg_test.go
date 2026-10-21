//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgApplicationStandardAssignmentInventoryLifecycle(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAssignmentInventoryLifecycle(t, s)
}

func TestPgApplicationStandardAssignmentInventoryPaging(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardAssignmentInventoryPaging(t, s, func(a api.ApplicationStandardAssignment) {
		_, err := pool.Exec(t.Context(), `INSERT INTO application_standard_assignments (id,org_id,scope,scope_id,standard_id,admission_version,revision,active,created_by,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, a.Revision, a.Active, a.CreatedBy, a.CreatedAt, a.UpdatedAt)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardAssignmentInventoryValidation(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAssignmentInventoryValidation(t, s)
}
