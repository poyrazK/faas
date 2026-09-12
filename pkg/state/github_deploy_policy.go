package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

const (
	// GitHubDeployPolicyDefaultPreviewTTLHours keeps PR previews bounded while
	// matching the existing seven-day githubd lease.
	GitHubDeployPolicyDefaultPreviewTTLHours = 7 * 24
	GitHubDeployPolicyMaxIgnoredPaths        = 64
	GitHubDeployPolicyMaxPathLength          = 255
	GitHubDeployPolicyMaxRootDirLength       = 255
	GitHubDeployPolicyMinPreviewTTLHours     = 1
	GitHubDeployPolicyMaxPreviewTTLHours     = 30 * 24
)

// GitHubDeployPolicy is the customer-owned deployment policy for a project
// connected to GitHub. A missing row means the defaults below, so existing
// bindings remain wire- and behaviour-compatible.
type GitHubDeployPolicy struct {
	ProjectID       string
	AccountID       string
	RootDir         string
	IgnoredPaths    []string
	PreviewEnabled  bool
	PreviewTTLHours int
	UpdatedAt       time.Time
}

// DefaultGitHubDeployPolicy returns the backwards-compatible policy for a
// project that has never been customized.
func DefaultGitHubDeployPolicy(projectID, accountID string) GitHubDeployPolicy {
	return GitHubDeployPolicy{
		ProjectID:       projectID,
		AccountID:       accountID,
		PreviewEnabled:  true,
		PreviewTTLHours: GitHubDeployPolicyDefaultPreviewTTLHours,
	}
}

// GitHubDeployPolicyStore is deliberately optional on Store. Older embedders
// can keep using the legacy GitHub binding surface and githubd will use the
// default policy until the store implements this extension.
type GitHubDeployPolicyStore interface {
	GetGitHubDeployPolicy(ctx context.Context, projectID, accountID string) (GitHubDeployPolicy, error)
	UpsertGitHubDeployPolicy(ctx context.Context, policy GitHubDeployPolicy) (GitHubDeployPolicy, error)
}

// Validate checks the untrusted customer-facing policy before it reaches
// either Postgres or githubd's source tree walker.
func (p GitHubDeployPolicy) Validate() error {
	if p.ProjectID == "" || p.AccountID == "" {
		return errors.New("state: GitHub deployment policy requires project_id and account_id")
	}
	if p.RootDir != "" {
		if len(p.RootDir) > GitHubDeployPolicyMaxRootDirLength {
			return fmt.Errorf("state: root_dir exceeds %d characters", GitHubDeployPolicyMaxRootDirLength)
		}
		if p.RootDir != path.Clean(p.RootDir) || strings.HasPrefix(p.RootDir, "/") || p.RootDir == "." || strings.HasPrefix(p.RootDir, "../") || p.RootDir == ".." {
			return fmt.Errorf("state: root_dir must be a repository-relative path")
		}
		for _, r := range p.RootDir {
			if unicode.IsControl(r) {
				return errors.New("state: root_dir contains a control character")
			}
		}
	}
	if len(p.IgnoredPaths) > GitHubDeployPolicyMaxIgnoredPaths {
		return fmt.Errorf("state: at most %d ignored_paths are supported", GitHubDeployPolicyMaxIgnoredPaths)
	}
	for _, pattern := range p.IgnoredPaths {
		if pattern == "" || len(pattern) > GitHubDeployPolicyMaxPathLength || strings.TrimSpace(pattern) != pattern {
			return fmt.Errorf("state: invalid ignored path %q", pattern)
		}
		for _, r := range pattern {
			if unicode.IsControl(r) {
				return fmt.Errorf("state: ignored path %q contains a control character", pattern)
			}
		}
		// Validate the pattern with path.Match after allowing the common
		// "dir/**" form used by monorepos. This keeps the wire shape
		// predictable without pretending to implement a full gitignore DSL.
		matchPattern := strings.TrimSuffix(pattern, "/**")
		if _, err := path.Match(matchPattern, matchPattern); err != nil {
			return fmt.Errorf("state: invalid ignored path %q: %w", pattern, err)
		}
	}
	if p.PreviewTTLHours < GitHubDeployPolicyMinPreviewTTLHours || p.PreviewTTLHours > GitHubDeployPolicyMaxPreviewTTLHours {
		return fmt.Errorf("state: preview_ttl_hours must be between %d and %d", GitHubDeployPolicyMinPreviewTTLHours, GitHubDeployPolicyMaxPreviewTTLHours)
	}
	return nil
}

// IgnorePath reports whether a changed repository path is covered by one of
// the policy patterns. Patterns are intentionally small: exact paths, shell
// globs for one path segment, and a trailing /** directory rule.
func (p GitHubDeployPolicy) IgnorePath(changed string) bool {
	changed = strings.TrimPrefix(path.Clean(changed), "./")
	for _, pattern := range p.IgnoredPaths {
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if changed == prefix || strings.HasPrefix(changed, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pattern, changed); ok {
			return true
		}
	}
	return false
}

// MarshalIgnoredPaths keeps the database column JSON-shaped and gives both
// PgStore and MemStore one canonical representation.
func MarshalIgnoredPaths(paths []string) ([]byte, error) {
	if paths == nil {
		paths = []string{}
	}
	return json.Marshal(paths)
}

func (m *MemStore) GetGitHubDeployPolicy(_ context.Context, projectID, accountID string) (GitHubDeployPolicy, error) {
	if projectID == "" || accountID == "" {
		return GitHubDeployPolicy{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return GitHubDeployPolicy{}, ErrNotFound
	}
	policy, ok := m.githubDeployPolicies[projectID]
	if !ok {
		return DefaultGitHubDeployPolicy(projectID, accountID), nil
	}
	policy.IgnoredPaths = append([]string(nil), policy.IgnoredPaths...)
	return policy, nil
}

func (m *MemStore) UpsertGitHubDeployPolicy(_ context.Context, policy GitHubDeployPolicy) (GitHubDeployPolicy, error) {
	if err := policy.Validate(); err != nil {
		return GitHubDeployPolicy{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[policy.ProjectID]
	if !ok || project.AccountID != policy.AccountID {
		return GitHubDeployPolicy{}, ErrNotFound
	}
	policy.IgnoredPaths = append([]string(nil), policy.IgnoredPaths...)
	policy.UpdatedAt = time.Now().UTC()
	m.githubDeployPolicies[policy.ProjectID] = policy
	return policy, nil
}

func (s *PgStore) GetGitHubDeployPolicy(ctx context.Context, projectID, accountID string) (GitHubDeployPolicy, error) {
	if projectID == "" || accountID == "" {
		return GitHubDeployPolicy{}, ErrNotFound
	}
	var policy GitHubDeployPolicy
	var raw []byte
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		select project_id, account_id, root_dir, ignored_paths, preview_enabled,
		       preview_ttl_hours, updated_at
		  from github_deploy_policies
		 where project_id = $1 and account_id = $2`, projectID, accountID).Scan(
		&policy.ProjectID, &policy.AccountID, &policy.RootDir, &raw,
		&policy.PreviewEnabled, &policy.PreviewTTLHours, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultGitHubDeployPolicy(projectID, accountID), nil
		}
		return GitHubDeployPolicy{}, err
	}
	if err := json.Unmarshal(raw, &policy.IgnoredPaths); err != nil {
		return GitHubDeployPolicy{}, fmt.Errorf("state: decode GitHub ignored_paths: %w", err)
	}
	policy.UpdatedAt = updatedAt
	if err := policy.Validate(); err != nil {
		return GitHubDeployPolicy{}, err
	}
	return policy, nil
}

func (s *PgStore) UpsertGitHubDeployPolicy(ctx context.Context, policy GitHubDeployPolicy) (GitHubDeployPolicy, error) {
	if err := policy.Validate(); err != nil {
		return GitHubDeployPolicy{}, err
	}
	raw, err := MarshalIgnoredPaths(policy.IgnoredPaths)
	if err != nil {
		return GitHubDeployPolicy{}, err
	}
	var stored GitHubDeployPolicy
	var storedRaw []byte
	err = s.pool.QueryRow(ctx, `
		insert into github_deploy_policies
		    (project_id, account_id, root_dir, ignored_paths, preview_enabled, preview_ttl_hours, updated_at)
		values ($1, $2, $3, $4::jsonb, $5, $6, now())
		on conflict (project_id) do update set
		    account_id = excluded.account_id,
		    root_dir = excluded.root_dir,
		    ignored_paths = excluded.ignored_paths,
		    preview_enabled = excluded.preview_enabled,
		    preview_ttl_hours = excluded.preview_ttl_hours,
		    updated_at = now()
		returning project_id, account_id, root_dir, ignored_paths, preview_enabled,
		          preview_ttl_hours, updated_at`,
		policy.ProjectID, policy.AccountID, policy.RootDir, raw,
		policy.PreviewEnabled, policy.PreviewTTLHours).Scan(
		&stored.ProjectID, &stored.AccountID, &stored.RootDir, &storedRaw,
		&stored.PreviewEnabled, &stored.PreviewTTLHours, &stored.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubDeployPolicy{}, ErrNotFound
		}
		return GitHubDeployPolicy{}, err
	}
	if err := json.Unmarshal(storedRaw, &stored.IgnoredPaths); err != nil {
		return GitHubDeployPolicy{}, fmt.Errorf("state: decode stored GitHub ignored_paths: %w", err)
	}
	return stored, nil
}
