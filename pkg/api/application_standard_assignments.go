package api

import "time"

// Admission intent is separate from each application's adopted version and
// desired/persisted/observed progress. Inactive assignments retain their identity.
type ApplicationStandardAssignment struct {
	ID               string    `json:"id"`
	OrgID            string    `json:"org_id"`
	Scope            string    `json:"scope"`
	ScopeID          string    `json:"scope_id"`
	StandardID       string    `json:"standard_id"`
	AdmissionVersion int64     `json:"admission_version"`
	Revision         int64     `json:"revision"`
	Active           bool      `json:"active"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ApplicationStandardAssignmentList struct {
	Assignments   []ApplicationStandardAssignment `json:"assignments"`
	NextPageAfter string                          `json:"next_page_after,omitempty"`
}
