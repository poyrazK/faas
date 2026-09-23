package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/previewset"
)

// PRPreviewSet is the current, exact workload set for one GitHub pull request.
// Replacing it on every head update makes old deployment notifications inert.
type PRPreviewSet struct {
	InstallationID int64
	RepoFullName   string
	PRNumber       int
	CommitSHA      string
	RootAppID      string
	MemberAppIDs   []string
	Closed         bool
}

// PRPreviewSetStore is separate from Store so preview-only state does not
// expand every Store embedding used by unrelated services.
type PRPreviewSetStore interface {
	PutPRPreviewSet(context.Context, PRPreviewSet) error
	ClosePRPreviewSet(context.Context, int64, string, int) error
	GetPRPreviewSet(context.Context, int64, string, int) (PRPreviewSet, error)
}

// PRPreviewEnvironment is a current-head snapshot, including the latest
// preview deployment for that commit for each expected workload.
type PRPreviewEnvironment struct {
	Set     PRPreviewSet
	Members []previewset.Member
}

// PRPreviewEnvironmentReader is separate from Store so non-preview callers
// do not need to implement a customer-facing projection.
type PRPreviewEnvironmentReader interface {
	PRPreviewEnvironmentByRoot(context.Context, string) (PRPreviewEnvironment, error)
}

var (
	_             PRPreviewSetStore          = (*PgStore)(nil)
	_             PRPreviewSetStore          = (*MemStore)(nil)
	_             PRPreviewEnvironmentReader = (*PgStore)(nil)
	_             PRPreviewEnvironmentReader = (*MemStore)(nil)
	previewSetSHA                            = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

func validatePRPreviewSet(set PRPreviewSet) error {
	if set.InstallationID <= 0 || set.RepoFullName == "" || set.PRNumber <= 0 ||
		!previewSetSHA.MatchString(set.CommitSHA) || set.RootAppID == "" || len(set.MemberAppIDs) == 0 || len(set.MemberAppIDs) > 100 {
		return fmt.Errorf("state: invalid PR preview set: %w", ErrConflict)
	}
	rootFound := false
	seen := make(map[string]struct{}, len(set.MemberAppIDs))
	for _, id := range set.MemberAppIDs {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("state: invalid PR preview member %q: %w", id, ErrConflict)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("state: duplicate PR preview member %q: %w", id, ErrConflict)
		}
		seen[id] = struct{}{}
		rootFound = rootFound || id == set.RootAppID
	}
	if !rootFound {
		return fmt.Errorf("state: PR preview root absent from members: %w", ErrConflict)
	}
	return nil
}

func previewSetKey(installationID int64, repo string, prNumber int) string {
	return fmt.Sprintf("%d\x00%s\x00%d", installationID, repo, prNumber)
}

// PutPRPreviewSet replaces the current head and expected members atomically.
// Removed members no longer referenced by any set become stale in the same
// transaction. The preview janitor owns resource cleanup and tombstoning.
func (s *PgStore) PutPRPreviewSet(ctx context.Context, set PRPreviewSet) error {
	if err := validatePRPreviewSet(set); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin PR preview set replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var accountID string
	err = tx.QueryRow(ctx, `select account_id::text from apps where id = $1`, set.RootAppID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: resolve PR preview root account: %w", err)
	}
	// Preview reservation and all set replacements for this account take the
	// same lock. A sibling cannot be retired while another root adds it.
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, accountID).Scan(&locked); err != nil {
		return fmt.Errorf("state: lock PR preview account: %w", err)
	}
	var previous []string
	err = tx.QueryRow(ctx, `select member_app_ids from pr_preview_sets
		where installation_id = $1 and repo_full_name = $2 and pr_number = $3`,
		set.InstallationID, set.RepoFullName, set.PRNumber).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("state: load previous PR preview members: %w", err)
	}
	// Lock app rows before the set row. The janitor locks an app and may
	// remove its set through the root-deletion trigger, so this order avoids
	// deadlocking a concurrent teardown against this replacement.
	lockIDs := append(append([]string(nil), previous...), set.MemberAppIDs...)
	rows, err := tx.Query(ctx, `select id::text from apps where id::text = any($1::text[]) order by id for update`, lockIDs)
	if err != nil {
		return fmt.Errorf("state: lock PR preview members: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan locked PR preview member: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("state: iterate locked PR preview members: %w", err)
	}
	rows.Close()
	var valid int
	err = tx.QueryRow(ctx, `
		select count(*)
		from unnest($2::text[]) as expected(app_id)
		join apps member on member.id = expected.app_id::uuid
		join apps root on root.id = $1::uuid
		where member.account_id = root.account_id
		  and member.project_id is not distinct from root.project_id
		  and member.preview_pr_number = $3
		  and member.preview_of_slug is not null
		  and member.status <> 'deleted'`, set.RootAppID, set.MemberAppIDs, set.PRNumber).Scan(&valid)
	if err != nil {
		return fmt.Errorf("state: validate PR preview members: %w", err)
	}
	if valid != len(set.MemberAppIDs) {
		return fmt.Errorf("state: PR preview members do not share the root scope: %w", ErrConflict)
	}
	_, err = tx.Exec(ctx, `
		insert into pr_preview_sets
		  (installation_id, repo_full_name, pr_number, commit_sha, root_app_id, member_app_ids)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (installation_id, repo_full_name, pr_number)
		do update set commit_sha = excluded.commit_sha,
		              root_app_id = excluded.root_app_id,
		              member_app_ids = excluded.member_app_ids,
		              closed_at = null,
		              updated_at = now()`, set.InstallationID, set.RepoFullName,
		set.PRNumber, set.CommitSHA, set.RootAppID, set.MemberAppIDs)
	if err != nil {
		return fmt.Errorf("state: put PR preview set: %w", err)
	}
	if len(previous) > 0 {
		_, err = tx.Exec(ctx, `update apps as member
			set preview_pr_state = $2, preview_expires_at = now()
			where member.id::text = any($1::text[])
			  and member.preview_of_slug is not null
			  and member.status <> 'deleted'
			  and not exists (
			    select 1 from pr_preview_sets as active_set
			    where active_set.member_app_ids @> array[member.id::text]
			  )`, previous, PreviewPrStateStale)
		if err != nil {
			return fmt.Errorf("state: retire removed PR preview members: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit PR preview set replacement: %w", err)
	}
	return nil
}

func (s *PgStore) ClosePRPreviewSet(ctx context.Context, installationID int64, repo string, prNumber int) error {
	if installationID <= 0 || repo == "" || prNumber <= 0 {
		return fmt.Errorf("state: invalid PR preview set identity: %w", ErrConflict)
	}
	_, err := s.pool.Exec(ctx, `update pr_preview_sets set closed_at = now(), updated_at = now()
		where installation_id = $1 and repo_full_name = $2 and pr_number = $3`, installationID, repo, prNumber)
	if err != nil {
		return fmt.Errorf("state: close PR preview set: %w", err)
	}
	return nil
}

func (s *PgStore) GetPRPreviewSet(ctx context.Context, installationID int64, repo string, prNumber int) (PRPreviewSet, error) {
	var set PRPreviewSet
	var closed bool
	err := s.pool.QueryRow(ctx, `select commit_sha, root_app_id::text, member_app_ids, closed_at is not null
		from pr_preview_sets where installation_id = $1 and repo_full_name = $2 and pr_number = $3`,
		installationID, repo, prNumber).Scan(&set.CommitSHA, &set.RootAppID, &set.MemberAppIDs, &closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return PRPreviewSet{}, ErrNotFound
	}
	if err != nil {
		return PRPreviewSet{}, fmt.Errorf("state: get PR preview set: %w", err)
	}
	set.InstallationID, set.RepoFullName, set.PRNumber, set.Closed = installationID, repo, prNumber, closed
	return set, nil
}

func (s *PgStore) PRPreviewEnvironmentByRoot(ctx context.Context, rootAppID string) (PRPreviewEnvironment, error) {
	if _, err := uuid.Parse(rootAppID); err != nil {
		return PRPreviewEnvironment{}, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		with selected as (
			select installation_id, repo_full_name, pr_number, commit_sha, root_app_id,
			       member_app_ids, closed_at is not null as closed
			from pr_preview_sets where root_app_id = $1::uuid
			order by updated_at desc limit 1
		)
		select selected.installation_id, selected.repo_full_name, selected.pr_number,
		       selected.commit_sha, selected.closed, expected.app_id,
		       coalesce(a.slug, ''), coalesce(a.preview_of_slug, ''),
		       coalesce(nullif(a.workload_name, ''), a.slug, expected.app_id),
		       coalesce(a.status, 'missing'), coalesce(a.preview_pr_state, ''),
		       coalesce(d.id::text, ''), coalesce(d.status, 'missing')
		from selected
		cross join lateral unnest(selected.member_app_ids) with ordinality as expected(app_id, ordinal)
		left join apps root on root.id = selected.root_app_id
		left join apps a on a.id = expected.app_id::uuid
		  and a.account_id = root.account_id
		  and a.project_id is not distinct from root.project_id
		  and a.preview_pr_number = selected.pr_number
		left join lateral (
			select id, status from deployments
			where app_id = a.id and kind = 'preview' and commit_sha = selected.commit_sha
			order by created_at desc, id desc limit 1
		) d on true
		order by expected.ordinal`, rootAppID)
	if err != nil {
		return PRPreviewEnvironment{}, fmt.Errorf("state: load PR preview environment: %w", err)
	}
	defer rows.Close()
	var environment PRPreviewEnvironment
	environment.Members = []previewset.Member{}
	for rows.Next() {
		var member previewset.Member
		if err := rows.Scan(&environment.Set.InstallationID, &environment.Set.RepoFullName,
			&environment.Set.PRNumber, &environment.Set.CommitSHA, &environment.Set.Closed,
			&member.AppID, &member.Slug, &member.PreviewOfSlug, &member.WorkloadName,
			&member.AppStatus, &member.PreviewState, &member.DeploymentID, &member.DeploymentStatus); err != nil {
			return PRPreviewEnvironment{}, fmt.Errorf("state: scan PR preview environment: %w", err)
		}
		environment.Members = append(environment.Members, member)
	}
	if err := rows.Err(); err != nil {
		return PRPreviewEnvironment{}, fmt.Errorf("state: iterate PR preview environment: %w", err)
	}
	if len(environment.Members) == 0 {
		return PRPreviewEnvironment{}, ErrNotFound
	}
	environment.Set.RootAppID = rootAppID
	return environment, nil
}

func (m *MemStore) PutPRPreviewSet(_ context.Context, set PRPreviewSet) error {
	if err := validatePRPreviewSet(set); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	root, ok := m.apps[set.RootAppID]
	if !ok {
		return ErrNotFound
	}
	for _, id := range set.MemberAppIDs {
		member, ok := m.apps[id]
		if !ok || member.AccountID != root.AccountID || member.ProjectID != root.ProjectID ||
			member.PreviewPrNumber != set.PRNumber || member.PreviewOfSlug == "" || member.Status == AppDeleted {
			return fmt.Errorf("state: PR preview members do not share the root scope: %w", ErrConflict)
		}
	}
	if m.previewSets == nil {
		m.previewSets = make(map[string]PRPreviewSet)
	}
	key := previewSetKey(set.InstallationID, set.RepoFullName, set.PRNumber)
	previous := m.previewSets[key]
	set.MemberAppIDs = append([]string(nil), set.MemberAppIDs...)
	set.Closed = false
	m.previewSets[key] = set
	for _, id := range previous.MemberAppIDs {
		inUse := false
		for _, current := range m.previewSets {
			for _, memberID := range current.MemberAppIDs {
				if id == memberID {
					inUse = true
					break
				}
			}
			if inUse {
				break
			}
		}
		if inUse {
			continue
		}
		app, ok := m.apps[id]
		if !ok || app.PreviewOfSlug == "" || app.Status == AppDeleted {
			continue
		}
		now := time.Now().UTC()
		app.PreviewPrState, app.PreviewExpiresAt = PreviewPrStateStale, &now
		m.apps[id] = app
	}
	return nil
}

func (m *MemStore) ClosePRPreviewSet(_ context.Context, installationID int64, repo string, prNumber int) error {
	if installationID <= 0 || repo == "" || prNumber <= 0 {
		return fmt.Errorf("state: invalid PR preview set identity: %w", ErrConflict)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := previewSetKey(installationID, repo, prNumber)
	if set, ok := m.previewSets[key]; ok {
		set.Closed = true
		m.previewSets[key] = set
	}
	return nil
}

func (m *MemStore) GetPRPreviewSet(_ context.Context, installationID int64, repo string, prNumber int) (PRPreviewSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.previewSets[previewSetKey(installationID, repo, prNumber)]
	if !ok {
		return PRPreviewSet{}, ErrNotFound
	}
	set.MemberAppIDs = append([]string(nil), set.MemberAppIDs...)
	return set, nil
}

func (m *MemStore) PRPreviewEnvironmentByRoot(_ context.Context, rootAppID string) (PRPreviewEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var set PRPreviewSet
	found := false
	for _, candidate := range m.previewSets {
		if candidate.RootAppID == rootAppID {
			set, found = candidate, true
			break
		}
	}
	if !found {
		return PRPreviewEnvironment{}, ErrNotFound
	}
	root, rootExists := m.apps[rootAppID]
	environment := PRPreviewEnvironment{Set: set, Members: make([]previewset.Member, 0, len(set.MemberAppIDs))}
	environment.Set.MemberAppIDs = append([]string(nil), set.MemberAppIDs...)
	for _, id := range set.MemberAppIDs {
		member := previewset.Member{AppID: id, WorkloadName: id, AppStatus: "missing", DeploymentStatus: "missing"}
		app, ok := m.apps[id]
		if ok && rootExists && app.AccountID == root.AccountID && app.ProjectID == root.ProjectID && app.PreviewPrNumber == set.PRNumber {
			member.Slug = app.Slug
			member.PreviewOfSlug = app.PreviewOfSlug
			member.WorkloadName = app.WorkloadName
			if member.WorkloadName == "" {
				member.WorkloadName = app.Slug
			}
			member.AppStatus = string(app.Status)
			member.PreviewState = app.PreviewPrState
			var latest Deployment
			for _, deployment := range m.deployments {
				if deployment.AppID != id || deployment.Kind != DeploymentKindPreview || deployment.CommitSHA != set.CommitSHA {
					continue
				}
				if latest.ID == "" || deployment.CreatedAt.After(latest.CreatedAt) ||
					(deployment.CreatedAt.Equal(latest.CreatedAt) && deployment.ID > latest.ID) {
					latest = deployment
				}
			}
			if latest.ID != "" {
				member.DeploymentID = latest.ID
				member.DeploymentStatus = string(latest.Status)
			}
		}
		environment.Members = append(environment.Members, member)
	}
	return environment, nil
}
