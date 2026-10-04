package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type environmentExternalFieldOwner struct {
	EnvironmentID string `json:"environment_id"`
	Resource      string `json:"resource"`
	Path          string `json:"path"`
	Manager       string `json:"manager"`
}

func validateEnvironmentFieldOwnership(in api.EnvironmentFieldOwnershipRequest) error {
	if in.Manager != "terraform" || (in.App == "") == (in.Project == "") || in.Environment == "" || len(in.Paths) == 0 || len(in.Paths) > api.EnvironmentFieldOwnershipMaxPaths {
		return ErrInvalidArgument
	}
	prefix := "configuration/"
	if in.App != "" {
		prefix = "variables/"
	}
	for _, path := range in.Paths {
		if in.App != "" && path == "source" {
			continue
		}
		key := strings.TrimPrefix(path, prefix)
		if key == path || in.App != "" && api.ValidateEnvKey(key) != nil {
			return ErrInvalidArgument
		}
		if in.Project != "" {
			raw, _ := json.Marshal(map[string]any{key: true})
			if _, _, err := api.NormalizeProjectEnvironmentConfig(raw); err != nil {
				return ErrInvalidArgument
			}
		}
	}
	return nil
}
func (m *MemStore) SetEnvironmentFieldOwnership(_ context.Context, accountID string, in api.EnvironmentFieldOwnershipRequest, release bool) (bool, error) {
	if err := validateEnvironmentFieldOwnership(in); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	projectID, resource := "", "environment"
	appFound := false
	if in.App != "" {
		for _, app := range m.apps {
			if app.AccountID == accountID && app.Slug == in.App && app.Status != AppDeleted {
				appFound = true
				projectID = app.ProjectID
				resource = "app/" + app.ID
				break
			}
		}
	} else {
		for _, project := range m.projects {
			if project.AccountID == accountID && project.Slug == in.Project {
				projectID = project.ID
				break
			}
		}
	}
	if in.App != "" && !appFound {
		return false, ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(projectID, in.Environment)
	if err != nil {
		if in.App != "" && (in.Environment == "default" || projectID == "") {
			return false, nil
		}
		return false, ErrNotFound
	}
	var memory *environmentGitOpsMemory
	for _, candidate := range m.environmentGitOps {
		if candidate.source.EnvironmentID == env.ID && !candidate.source.Detached {
			memory = candidate
			break
		}
	}
	if !release && memory != nil {
		for _, path := range in.Paths {
			for _, owner := range memory.owners {
				matches := resource == "environment" && owner.Resource == "environment" || resource == "app/"+memory.resources[owner.Resource]
				if matches && (owner.Path == path || strings.HasPrefix(path, "variables/") && owner.Path == "secret_refs/"+strings.TrimPrefix(path, "variables/")) {
					return false, ErrConflict
				}
			}
		}
	}
	if m.environmentExternalOwners == nil {
		m.environmentExternalOwners = map[string]environmentExternalFieldOwner{}
	}
	changed := false
	for _, path := range in.Paths {
		key := env.ID + "#" + resource + "#" + path
		if release {
			if _, ok := m.environmentExternalOwners[key]; ok {
				changed = true
			}
			delete(m.environmentExternalOwners, key)
		} else {
			if _, ok := m.environmentExternalOwners[key]; !ok {
				changed = true
			}
			m.environmentExternalOwners[key] = environmentExternalFieldOwner{EnvironmentID: env.ID, Resource: resource, Path: path, Manager: in.Manager}
		}
	}
	if changed {
		touchGitOpsMemoryIntent(memory)
	}
	return true, nil
}
func (s *PgStore) SetEnvironmentFieldOwnership(ctx context.Context, accountID string, in api.EnvironmentFieldOwnershipRequest, release bool) (bool, error) {
	if err := validateEnvironmentFieldOwnership(in); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	scope, err := q.ResolveEnvironmentFieldOwnershipScope(ctx, tx, sqlc.ResolveEnvironmentFieldOwnershipScopeParams{AccountID: mustPgUUID(accountID), App: in.App, Project: in.Project, Environment: in.Environment})
	if errors.Is(err, pgx.ErrNoRows) && in.App != "" {
		legacy, checkErr := q.EnvironmentFieldOwnershipLegacyApp(ctx, tx, sqlc.EnvironmentFieldOwnershipLegacyAppParams{AccountID: mustPgUUID(accountID), App: in.App, Environment: in.Environment})
		if checkErr != nil {
			return false, checkErr
		}
		if legacy {
			return false, nil
		}
	}
	if err != nil {
		return false, mapErr(err)
	}
	if err = q.LockEnvironmentFieldOwnershipScope(ctx, tx, pgUUIDString(scope.EnvironmentID)); err != nil {
		return false, err
	}
	sources, err := q.LockEnvironmentGitSourceForScope(ctx, tx, sqlc.LockEnvironmentGitSourceForScopeParams{AccountID: mustPgUUID(accountID), ProjectID: scope.ProjectID, Environment: in.Environment})
	if err != nil {
		return false, err
	}
	// A retirement may commit a replacement while the first source lock waits.
	// Read again at a fresh statement snapshot before testing or reserving fields.
	if len(sources) == 0 {
		sources, err = q.LockEnvironmentGitSourceForScope(ctx, tx, sqlc.LockEnvironmentGitSourceForScopeParams{AccountID: mustPgUUID(accountID), ProjectID: scope.ProjectID, Environment: in.Environment})
		if err != nil {
			return false, err
		}
	}
	changed := false
	var affected int64
	for _, path := range in.Paths {
		if !release {
			owned, err := q.EnvironmentFieldGitOwned(ctx, tx, sqlc.EnvironmentFieldGitOwnedParams{EnvironmentID: scope.EnvironmentID, Resource: scope.Resource, FieldPath: path})
			if err != nil {
				return false, err
			}
			if owned {
				return false, ErrConflict
			}
		}
		if release {
			affected, err = q.DeleteEnvironmentExternalFieldOwner(ctx, tx, sqlc.DeleteEnvironmentExternalFieldOwnerParams{EnvironmentID: scope.EnvironmentID, Resource: scope.Resource, FieldPath: path})
		} else {
			affected, err = q.PutEnvironmentExternalFieldOwner(ctx, tx, sqlc.PutEnvironmentExternalFieldOwnerParams{EnvironmentID: scope.EnvironmentID, Resource: scope.Resource, FieldPath: path})
		}
		if err != nil {
			return false, mapErr(err)
		}
		changed = changed || affected > 0
	}
	for _, id := range sources {
		if !changed {
			break
		}
		if err = q.TouchEnvironmentGitOpsIntent(ctx, tx, id); err != nil {
			return false, err
		}
	}
	return true, mapErr(tx.Commit(ctx))
}
