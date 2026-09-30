package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var _ ApplicationStandardStore = (*MemStore)(nil)

func (m *MemStore) PublishApplicationStandardVersion(_ context.Context, p ApplicationStandardPublish) (ApplicationStandardVersion, error) {
	version, err := prepareApplicationStandard(p)
	if err != nil {
		return ApplicationStandardVersion{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	org, exists := m.orgs[p.OrgID]
	if !exists || org.DeletedPending {
		return ApplicationStandardVersion{}, ErrNotFound
	}
	if _, exists := m.accounts[p.ActorID]; !exists {
		return ApplicationStandardVersion{}, ErrNotFound
	}
	if m.applicationStandardVersions == nil {
		m.applicationStandardVersions = map[string][]ApplicationStandardVersion{}
	}
	key := p.OrgID + "/" + p.Slug
	versions := m.applicationStandardVersions[key]
	if int64(len(versions)) != p.ExpectedVersion {
		return ApplicationStandardVersion{}, ErrConflict
	}
	version.StandardID = uuid.NewString()
	if len(versions) > 0 {
		version.StandardID = versions[0].StandardID
	}
	version.CreatedAt = time.Now().UTC()
	m.applicationStandardVersions[key] = append(versions, cloneApplicationStandard(version))
	return cloneApplicationStandard(version), nil
}

func (m *MemStore) GetApplicationStandardVersion(_ context.Context, orgID, slug string, number int64) (ApplicationStandardVersion, error) {
	if !validApplicationStandardRead(orgID, slug, number) {
		return ApplicationStandardVersion{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	versions := m.applicationStandardVersions[orgID+"/"+slug]
	if number == 0 {
		number = int64(len(versions))
	}
	if number < 1 || number > int64(len(versions)) {
		return ApplicationStandardVersion{}, ErrNotFound
	}
	return cloneApplicationStandard(versions[number-1]), nil
}

func (m *MemStore) ListApplicationStandards(_ context.Context, orgID, after string, limit int) ([]ApplicationStandardVersion, error) {
	if !validApplicationStandardPage(orgID, after, limit) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []ApplicationStandardVersion{}
	for key, versions := range m.applicationStandardVersions {
		if !strings.HasPrefix(key, orgID+"/") || len(versions) == 0 {
			continue
		}
		version := versions[len(versions)-1]
		if version.Slug > after {
			result = append(result, cloneApplicationStandard(version))
		}
	}
	slices.SortFunc(result, func(a, b ApplicationStandardVersion) int { return strings.Compare(a.Slug, b.Slug) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
