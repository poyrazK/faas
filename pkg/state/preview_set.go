package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

var (
	_             PRPreviewSetStore = (*PgStore)(nil)
	_             PRPreviewSetStore = (*MemStore)(nil)
	previewSetSHA                   = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
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
// A redelivery of the same head is idempotent; a new head supersedes old
// deployment notifications without deleting historical deployment rows.
func (s *PgStore) PutPRPreviewSet(ctx context.Context, set PRPreviewSet) error {
	if err := validatePRPreviewSet(set); err != nil {
		return err
	}
	var valid int
	err := s.pool.QueryRow(ctx, `
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
	_, err = s.pool.Exec(ctx, `
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
	set.MemberAppIDs = append([]string(nil), set.MemberAppIDs...)
	set.Closed = false
	m.previewSets[previewSetKey(set.InstallationID, set.RepoFullName, set.PRNumber)] = set
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
