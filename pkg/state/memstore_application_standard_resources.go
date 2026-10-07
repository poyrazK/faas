package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var _ ApplicationStandardResourceStore = (*MemStore)(nil)

func (m *MemStore) standardResourceOwnerExistsLocked(orgID, actorID string) bool {
	org, exists := m.orgs[orgID]
	_, actorExists := m.accounts[actorID]
	return exists && !org.DeletedPending && actorExists
}

func (m *MemStore) CreateApplicationStandardLogDestination(_ context.Context, in ApplicationStandardLogDestinationCreate) (ApplicationStandardLogDestination, error) {
	row, err := prepareStandardLogDestination(in)
	if err != nil {
		return ApplicationStandardLogDestination{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.standardResourceOwnerExistsLocked(in.OrgID, in.ActorID) {
		return ApplicationStandardLogDestination{}, ErrNotFound
	}
	if m.applicationStandardLogDestinations == nil {
		m.applicationStandardLogDestinations = map[string]ApplicationStandardLogDestination{}
	}
	row.ID, row.CreatedAt = uuid.NewString(), time.Now().UTC()
	m.applicationStandardLogDestinations[row.ID] = cloneStandardLogDestination(row)
	return cloneStandardLogDestination(row), nil
}

func (m *MemStore) GetApplicationStandardLogDestination(_ context.Context, orgID, id string) (ApplicationStandardLogDestination, error) {
	if !validStandardResourceRead(orgID, id) {
		return ApplicationStandardLogDestination{}, ErrInvalidArgument
	}
	parsedID, _ := uuid.Parse(id)
	id = parsedID.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, exists := m.applicationStandardLogDestinations[id]
	if !exists || row.OrgID != orgID {
		return ApplicationStandardLogDestination{}, ErrNotFound
	}
	return cloneStandardLogDestination(row), nil
}

func (m *MemStore) ListApplicationStandardLogDestinations(_ context.Context, orgID, after string, limit int) ([]ApplicationStandardLogDestination, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	if after != "" {
		parsedID, _ := uuid.Parse(after)
		after = parsedID.String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []ApplicationStandardLogDestination{}
	for _, row := range m.applicationStandardLogDestinations {
		if row.OrgID == orgID && row.ID > after {
			rows = append(rows, cloneStandardLogDestination(row))
		}
	}
	slices.SortFunc(rows, func(a, b ApplicationStandardLogDestination) int { return strings.Compare(a.ID, b.ID) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemStore) CreateApplicationStandardPublisher(_ context.Context, in ApplicationStandardPublisherCreate) (api.ApplicationStandardPublisher, error) {
	row, err := prepareStandardPublisher(in)
	if err != nil {
		return api.ApplicationStandardPublisher{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.standardResourceOwnerExistsLocked(in.OrgID, in.ActorID) {
		return api.ApplicationStandardPublisher{}, ErrNotFound
	}
	if m.applicationStandardPublishers == nil {
		m.applicationStandardPublishers = map[string]api.ApplicationStandardPublisher{}
	}
	row.ID, row.CreatedAt = uuid.NewString(), time.Now().UTC()
	m.applicationStandardPublishers[row.ID] = row
	return row, nil
}

func (m *MemStore) GetApplicationStandardPublisher(_ context.Context, orgID, id string) (api.ApplicationStandardPublisher, error) {
	if !validStandardResourceRead(orgID, id) {
		return api.ApplicationStandardPublisher{}, ErrInvalidArgument
	}
	parsedID, _ := uuid.Parse(id)
	id = parsedID.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, exists := m.applicationStandardPublishers[id]
	if !exists || row.OrgID != orgID {
		return api.ApplicationStandardPublisher{}, ErrNotFound
	}
	return row, nil
}

func (m *MemStore) ListApplicationStandardPublishers(_ context.Context, orgID, after string, limit int) ([]api.ApplicationStandardPublisher, error) {
	if !validStandardResourcePage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	if after != "" {
		parsedID, _ := uuid.Parse(after)
		after = parsedID.String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.ApplicationStandardPublisher{}
	for _, row := range m.applicationStandardPublishers {
		if row.OrgID == orgID && row.ID > after {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(a, b api.ApplicationStandardPublisher) int { return strings.Compare(a.ID, b.ID) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemStore) validateApplicationStandardRefsLocked(orgID string, definition appstandards.Definition) error {
	for _, id := range standardResourceRefs(definition, appstandards.LogDestinations) {
		if row, ok := m.applicationStandardLogDestinations[id]; !ok || row.OrgID != orgID {
			return ErrInvalidArgument
		}
	}
	for _, id := range standardResourceRefs(definition, appstandards.TrustedPublishers) {
		if row, ok := m.applicationStandardPublishers[id]; !ok || row.OrgID != orgID {
			return ErrInvalidArgument
		}
	}
	return nil
}
