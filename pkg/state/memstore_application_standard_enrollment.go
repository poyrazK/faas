package state

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type applicationStandardAssignmentRecord struct {
	appstandards.Assignment
	Revision             int64
	Active               bool
	CreatedBy            string
	CreatedAt, UpdatedAt time.Time
}

var _ ApplicationStandardEnrollmentStore = (*MemStore)(nil)

func (m *MemStore) GetApplicationStandardEnrollment(_ context.Context, orgID, appID string) (ApplicationStandardEnrollment, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, appID) && sameStandardUUID(row.OrgID, orgID) {
			return cloneApplicationStandardEnrollment(row), nil
		}
	}
	return ApplicationStandardEnrollment{}, ErrNotFound
}

func (m *MemStore) ListApplicationStandardAssignments(_ context.Context, orgID string) ([]appstandards.Assignment, error) {
	if !validStandardResourceRead(orgID, orgID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applicationStandardAssignmentsLocked(orgID), nil
}

func (m *MemStore) applicationStandardAssignmentsLocked(orgID string) []appstandards.Assignment {
	rows := []appstandards.Assignment{}
	for _, row := range m.applicationStandardAssignments {
		if row.Active && sameStandardUUID(row.OrgID, orgID) {
			rows = append(rows, row.Assignment)
		}
	}
	slices.SortFunc(rows, func(a, b appstandards.Assignment) int {
		return strings.Compare(a.Scope+"/"+a.ScopeID+"/"+a.StandardID, b.Scope+"/"+b.ScopeID+"/"+b.StandardID)
	})
	return rows
}

func sameStandardUUID(a, b string) bool {
	first, err := uuid.Parse(a)
	if err != nil || first == uuid.Nil {
		return false
	}
	second, err := uuid.Parse(b)
	return err == nil && first == second
}

// The mutex protects both scope selection and the app insert. Older MemStore
// fixtures without an org retain their legacy behavior; API-created apps and
// all org-owned fixtures participate in the same enrollment boundary as PG.
func (m *MemStore) initializeApplicationStandardEnrollmentLocked(app App) error {
	pins, err := m.applicationStandardAdmissionPinsLocked(app)
	if err != nil {
		return err
	}
	if app.OrgID == "" {
		return nil
	}
	if m.applicationStandardEnrollments == nil {
		m.applicationStandardEnrollments = map[string]ApplicationStandardEnrollment{}
	}
	before, exists := m.applicationStandardEnrollments[app.ID]
	value := ApplicationStandardEnrollment{AppID: app.ID, OrgID: app.OrgID, ProjectID: app.ProjectID, BaseSettings: applicationStandardBaseSettings(app), LocalSettings: appstandards.Settings{}, AdditionalLogDestinations: []string{}, MaterializedFields: []appstandards.Field{}, Adoptions: pins, DesiredRevision: 1, State: "unmanaged", UpdatedAt: time.Now().UTC()}
	if len(pins) > 0 {
		value.State = "pending"
	}
	if exists {
		value.BaseSettings, value.LocalSettings, value.AdditionalLogDestinations = before.BaseSettings, before.LocalSettings, before.AdditionalLogDestinations
		value.PersistedRevision, value.ObservedRevision = before.PersistedRevision, before.ObservedRevision
		value.Effective, value.EffectiveHash, value.ExceptionExpiresAt = before.Effective, before.EffectiveHash, before.ExceptionExpiresAt
		value.MaterializedFields = append([]appstandards.Field{}, before.MaterializedFields...)
		if before.PersistedRevision > 0 || len(value.MaterializedFields) > 0 {
			value.State = "pending"
		}
		value.DesiredRevision = before.DesiredRevision + 1
		m.revokeStandardEnrollmentClaimLocked(app.ID)
	}
	m.applicationStandardEnrollments[app.ID] = cloneApplicationStandardEnrollment(value)
	return nil
}

func (m *MemStore) applicationStandardAdmissionPinsLocked(app App) ([]appstandards.Adoption, error) {
	if before, exists := m.apps[app.ID]; exists && before.OrgID != "" && app.OrgID == "" {
		return nil, fmt.Errorf("application organization owner cannot be removed: %w", ErrInvalidArgument)
	}
	pins := []appstandards.Adoption{}
	for _, record := range m.applicationStandardAssignments {
		if !record.Active {
			continue
		}
		assignment := record.Assignment
		matches := assignment.Scope == "organization" && sameStandardUUID(assignment.ScopeID, app.OrgID) || assignment.Scope == "project" && sameStandardUUID(assignment.ScopeID, app.ProjectID) || assignment.Scope == "application" && sameStandardUUID(assignment.ScopeID, app.ID)
		if !matches {
			continue
		}
		if !sameStandardUUID(assignment.OrgID, app.OrgID) {
			return nil, fmt.Errorf("assigned app scope belongs to another organization: %w", ErrInvalidArgument)
		}
		pins = append(pins, appstandards.Adoption{AssignmentID: assignment.ID, Version: assignment.AdmissionVersion})
	}
	slices.SortFunc(pins, func(a, b appstandards.Adoption) int { return strings.Compare(a.AssignmentID, b.AssignmentID) })
	return pins, nil
}

func (m *MemStore) cloneApplicationStandardEnrollmentsLocked() map[string]ApplicationStandardEnrollment {
	copy := map[string]ApplicationStandardEnrollment{}
	for id, row := range m.applicationStandardEnrollments {
		copy[id] = cloneApplicationStandardEnrollment(row)
	}
	return copy
}
