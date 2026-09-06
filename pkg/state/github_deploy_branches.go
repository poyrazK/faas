package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var deployScopePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

// ProjectDeployBranchesStore is an optional extension implemented by stores
// that persist GitHub branch-to-deployment-scope routing. Keeping this out of
// Store preserves compatibility with small embedders and older test doubles.
type ProjectDeployBranchesStore interface {
	ListProjectDeployBranches(ctx context.Context, projectID string) (map[string]string, error)
	ReplaceProjectDeployBranches(ctx context.Context, accountID, projectID string, branches map[string]string) error
}

func validateDeployBranchMapping(branches map[string]string) error {
	if len(branches) > 32 {
		return errors.New("state: at most 32 GitHub deploy branches are supported")
	}
	for branch, scope := range branches {
		if branch == "" || len(branch) > 255 {
			return fmt.Errorf("state: invalid deploy branch %q", branch)
		}
		for _, r := range branch {
			if unicode.IsControl(r) {
				return fmt.Errorf("state: invalid deploy branch %q", branch)
			}
		}
		if strings.TrimSpace(scope) != scope || !deployScopePattern.MatchString(scope) {
			return fmt.Errorf("state: invalid deployment scope for branch %q", branch)
		}
	}
	return nil
}

func (m *MemStore) ListProjectDeployBranches(_ context.Context, projectID string) (map[string]string, error) {
	if projectID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	out := make(map[string]string)
	for branch, scope := range m.githubDeployBranches[projectID] {
		out[branch] = scope
	}
	return out, nil
}

func (m *MemStore) ReplaceProjectDeployBranches(_ context.Context, accountID, projectID string, branches map[string]string) error {
	if projectID == "" || accountID == "" {
		return ErrNotFound
	}
	if err := validateDeployBranchMapping(branches); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ErrNotFound
	}
	copyBranches := make(map[string]string, len(branches))
	for branch, scope := range branches {
		copyBranches[branch] = scope
	}
	m.githubDeployBranches[projectID] = copyBranches
	return nil
}

func (s *PgStore) ListProjectDeployBranches(ctx context.Context, projectID string) (map[string]string, error) {
	if projectID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		select branch, scope
		  from github_deploy_branches
		 where project_id = $1
		 order by branch`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var branch, scope string
		if err := rows.Scan(&branch, &scope); err != nil {
			return nil, err
		}
		out[branch] = scope
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PgStore) ReplaceProjectDeployBranches(ctx context.Context, accountID, projectID string, branches map[string]string) error {
	if projectID == "" || accountID == "" {
		return ErrNotFound
	}
	if err := validateDeployBranchMapping(branches); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from projects where id = $1 and account_id = $2)`, projectID, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `delete from github_deploy_branches where project_id = $1`, projectID); err != nil {
		return err
	}
	for branch, scope := range branches {
		if _, err := tx.Exec(ctx, `
			insert into github_deploy_branches (project_id, branch, scope)
			values ($1, $2, $3)`, projectID, branch, scope); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// LatestDeploymentForScope is the scope-aware counterpart to the legacy
// app-wide lookup. It is optional so older Store implementations remain
// source-compatible while deployment routing rolls out.
func (m *MemStore) LatestDeploymentForScope(_ context.Context, appID, scope string) (Deployment, error) {
	scope = normalizedDeploymentScope(scope)
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID != appID || normalizedDeploymentScope(d.Scope) != scope {
			continue
		}
		if !found || d.CreatedAt.After(latest.CreatedAt) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (s *PgStore) LatestDeploymentForScope(ctx context.Context, appID, scope string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments
		  where app_id = $1 and scope = $2
		  order by created_at desc, id desc limit 1`, appID, normalizedDeploymentScope(scope))
	return scanDeployment(row)
}
