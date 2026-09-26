package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectReleaseSetReader exposes account-scoped inventory, including expired
// graphs. Reading a graph does not imply that its deployments remain routable.
type ProjectReleaseSetReader interface {
	ActiveProjectReleaseSet(context.Context, string, string, string) (ProjectReleaseSet, error)
	ProjectReleaseSetByID(context.Context, string, string, string, string) (ProjectReleaseSet, error)
	ListProjectReleaseSetsBefore(context.Context, string, string, string, time.Time, string, int) ([]ProjectReleaseSet, error)
}

var _ ProjectReleaseSetReader = (*PgStore)(nil)
var _ ProjectReleaseSetReader = (*MemStore)(nil)

func (s *PgStore) ActiveProjectReleaseSet(ctx context.Context, accountID, projectID, environment string) (ProjectReleaseSet, error) {
	return s.readProjectReleaseSet(ctx, accountID, projectID, environment, pgtype.UUID{})
}

func (s *PgStore) ProjectReleaseSetByID(ctx context.Context, accountID, projectID, environment, id string) (ProjectReleaseSet, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	return s.readProjectReleaseSet(ctx, accountID, projectID, environment, pgtype.UUID{Bytes: parsed, Valid: true})
}

func (s *PgStore) readProjectReleaseSet(ctx context.Context, accountID, projectID, environment string, id pgtype.UUID) (ProjectReleaseSet, error) {
	data, err := sqlc.New().ReadProjectReleaseSet(ctx, s.pool, sqlc.ReadProjectReleaseSetParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment, ReleaseID: id,
	})
	if err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	return decodeProjectReleaseSet(data)
}

func decodeProjectReleaseSet(data []byte) (ProjectReleaseSet, error) {
	var release ProjectReleaseSet
	if err := json.Unmarshal(data, &release); err != nil {
		return release, fmt.Errorf("state: decode release set: %w", err)
	}
	return release, nil
}

func validateProjectReleasePage(before time.Time, beforeID string, limit int) error {
	// One extra row lets the API determine whether a next page exists.
	if limit < 1 || limit > api.ProjectReleaseSetPageMax+1 || before.IsZero() != (beforeID == "") {
		return ErrInvalidArgument
	}
	if beforeID != "" {
		if _, err := uuid.Parse(beforeID); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

func (s *PgStore) ListProjectReleaseSetsBefore(ctx context.Context, accountID, projectID, environment string, before time.Time, beforeID string, limit int) ([]ProjectReleaseSet, error) {
	if err := validateProjectReleasePage(before, beforeID, limit); err != nil {
		return nil, err
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, accountID, projectID, environment); err != nil {
		return nil, err
	}
	params := sqlc.ListProjectReleaseSetsBeforeParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment, PageLimit: int32(limit),
	}
	if !before.IsZero() {
		params.BeforeAt = pgtype.Timestamptz{Time: before, Valid: true}
		params.BeforeID = mustPgUUID(beforeID)
	}
	rows, err := sqlc.New().ListProjectReleaseSetsBefore(ctx, s.pool, params)
	if err != nil {
		return nil, mapErr(err)
	}
	releases := make([]ProjectReleaseSet, 0, len(rows))
	for _, data := range rows {
		release, err := decodeProjectReleaseSet(data)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, nil
}

// Return detached data: callers must not mutate membership or expiry in memory.
func cloneProjectReleaseSet(release ProjectReleaseSet) ProjectReleaseSet {
	release.Members = append([]ProjectReleaseMember{}, release.Members...)
	sort.Slice(release.Members, func(i, j int) bool { return release.Members[i].AppID < release.Members[j].AppID })
	if release.ExpiresAt != nil {
		expires := *release.ExpiresAt
		release.ExpiresAt = &expires
	}
	return release
}

func (m *MemStore) ownsReleaseEnvironmentLocked(accountID, projectID, environment string) bool {
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return false
	}
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.AccountID == accountID && env.Slug == environment {
			return true
		}
	}
	return false
}

func (m *MemStore) ActiveProjectReleaseSet(_ context.Context, accountID, projectID, environment string) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return ProjectReleaseSet{}, ErrNotFound
	}
	release, ok := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(projectID, environment)]]
	if !ok || !release.Active {
		return ProjectReleaseSet{}, ErrNotFound
	}
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) ProjectReleaseSetByID(_ context.Context, accountID, projectID, environment, id string) (ProjectReleaseSet, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	release, ok := m.projectReleaseSets[parsed.String()]
	if !ok || !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) ||
		release.AccountID != accountID || release.ProjectID != projectID || release.EnvironmentSlug != environment {
		return ProjectReleaseSet{}, ErrNotFound
	}
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) ListProjectReleaseSetsBefore(_ context.Context, accountID, projectID, environment string, before time.Time, beforeID string, limit int) ([]ProjectReleaseSet, error) {
	if err := validateProjectReleasePage(before, beforeID, limit); err != nil {
		return nil, err
	}
	if beforeID != "" {
		beforeID = uuid.MustParse(beforeID).String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return nil, ErrNotFound
	}
	releases := make([]ProjectReleaseSet, 0)
	for _, release := range m.projectReleaseSets {
		if release.AccountID != accountID || release.ProjectID != projectID || release.EnvironmentSlug != environment {
			continue
		}
		if !before.IsZero() && (release.CreatedAt.After(before) || release.CreatedAt.Equal(before) && release.ID >= beforeID) {
			continue
		}
		releases = append(releases, cloneProjectReleaseSet(release))
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].CreatedAt.Equal(releases[j].CreatedAt) {
			return releases[i].ID > releases[j].ID
		}
		return releases[i].CreatedAt.After(releases[j].CreatedAt)
	})
	if len(releases) > limit {
		releases = releases[:limit]
	}
	return releases, nil
}
