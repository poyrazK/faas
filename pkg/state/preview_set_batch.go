package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// PRPreviewHead identifies the exact PR revision being reserved. The root and
// member app IDs are derived from the atomically reserved batch.
type PRPreviewHead struct {
	InstallationID int64
	RepoFullName   string
	PRNumber       int
	CommitSHA      string
}

// PRPreviewSetBatchStore replaces a PR set and reserves its app rows in one
// transaction. Exclusive removed members become deleted/stale before quota is
// counted; the janitor completes their resource cleanup asynchronously.
type PRPreviewSetBatchStore interface {
	ReservePRPreviewSet(context.Context, PRPreviewHead, []App, api.Limits) ([]App, error)
}

var (
	_ PRPreviewSetBatchStore = (*PgStore)(nil)
	_ PRPreviewSetBatchStore = (*MemStore)(nil)
)

func validatePRPreviewHead(head PRPreviewHead, apps []App) error {
	if err := validatePRPreviewBatch(apps); err != nil {
		return err
	}
	if len(apps) == 0 || len(apps) > 100 || head.InstallationID <= 0 || head.RepoFullName == "" ||
		head.PRNumber != apps[0].PreviewPrNumber || !previewSetSHA.MatchString(head.CommitSHA) {
		return fmt.Errorf("state: invalid PR preview head: %w", ErrConflict)
	}
	for _, app := range apps {
		if app.PreviewExpiresAt == nil || app.PreviewPrState != PreviewPrStateOpen {
			return fmt.Errorf("state: PR preview batch has no open lease: %w", ErrConflict)
		}
	}
	return nil
}

func (s *PgStore) ReservePRPreviewSet(ctx context.Context, head PRPreviewHead, apps []App, limits api.Limits) ([]App, error) {
	if err := validatePRPreviewHead(head, apps); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: begin PR preview replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, apps[0].AccountID).Scan(&locked); err != nil {
		return nil, fmt.Errorf("state: lock PR preview account: %w", mapErr(err))
	}
	var previous []string
	var previousAccount, previousProject string
	err = tx.QueryRow(ctx, `select preview_set.member_app_ids, root.account_id::text,
		coalesce(root.project_id::text, '')
		from pr_preview_sets preview_set join apps root on root.id = preview_set.root_app_id
		where preview_set.installation_id = $1 and preview_set.repo_full_name = $2 and preview_set.pr_number = $3`,
		head.InstallationID, head.RepoFullName, head.PRNumber).Scan(&previous, &previousAccount, &previousProject)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("state: load previous PR preview set: %w", err)
	}
	if err == nil && (previousAccount != apps[0].AccountID || previousProject != apps[0].ProjectID) {
		return nil, ErrConflict
	}
	if err := retireReplacedPreviewMembersTx(ctx, tx, head, apps, previous); err != nil {
		return nil, err
	}
	reserved := make([]App, 0, len(apps))
	for _, desired := range apps {
		app, createErr := createAppIfUnderQuotaTx(ctx, tx, desired, limits)
		if errors.Is(createErr, ErrConflict) {
			app, createErr = scanApp(tx.QueryRow(ctx,
				`select `+appsSelectColumns+` from apps where slug = $1 and status <> 'deleted'`, desired.Slug))
			if createErr == nil && (!samePRPreview(app, desired) || app.PreviewPrState == PreviewPrStateTearingDown || app.PreviewPrState == PreviewPrStateTornDown) {
				return nil, ErrConflict
			}
		}
		if createErr != nil {
			return nil, createErr
		}
		app, err = scanApp(tx.QueryRow(ctx, `update apps set preview_pr_state = $2, preview_expires_at = $3
			where id = $1 and status <> 'deleted' and preview_pr_state is distinct from 'tearing_down'
			returning `+appsSelectColumns, app.ID, PreviewPrStateOpen, desired.PreviewExpiresAt))
		if err != nil {
			return nil, fmt.Errorf("state: refresh reserved PR preview: %w", err)
		}
		reserved = append(reserved, app)
	}
	set := PRPreviewSet{InstallationID: head.InstallationID, RepoFullName: head.RepoFullName,
		PRNumber: head.PRNumber, CommitSHA: head.CommitSHA, RootAppID: reserved[0].ID}
	for _, app := range reserved {
		set.MemberAppIDs = append(set.MemberAppIDs, app.ID)
	}
	if err := validatePRPreviewSet(set); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into pr_preview_sets
		(installation_id, repo_full_name, pr_number, commit_sha, root_app_id, member_app_ids)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (installation_id, repo_full_name, pr_number)
		do update set commit_sha = excluded.commit_sha, root_app_id = excluded.root_app_id,
		member_app_ids = excluded.member_app_ids, closed_at = null, updated_at = now()`,
		set.InstallationID, set.RepoFullName, set.PRNumber, set.CommitSHA, set.RootAppID, set.MemberAppIDs); err != nil {
		return nil, fmt.Errorf("state: replace PR preview set: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit PR preview replacement: %w", err)
	}
	return reserved, nil
}

func retireReplacedPreviewMembersTx(ctx context.Context, tx pgx.Tx, head PRPreviewHead, desired []App, previous []string) error {
	if len(previous) == 0 {
		return nil
	}
	// Lock app rows before the set row, matching PutPRPreviewSet's ordering
	// against the janitor's root-deletion trigger.
	rows, err := tx.Query(ctx, `select id::text from apps where id::text = any($1::text[]) order by id for update`, previous)
	if err != nil {
		return fmt.Errorf("state: lock old PR preview members: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan old PR preview member: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("state: iterate old PR preview members: %w", err)
	}
	rows.Close()
	slugs := make([]string, 0, len(desired))
	for _, app := range desired {
		slugs = append(slugs, app.Slug)
	}
	rows, err = tx.Query(ctx, `select app.id::text from apps app
		where app.id::text = any($1::text[])
		  and app.account_id = $2
		  and coalesce(app.project_id::text, '') = $3
		  and app.preview_pr_number = $4
		  and app.preview_of_slug is not null
		  and app.status <> 'deleted'
		  and app.preview_pr_state is distinct from 'tearing_down'
		  and app.preview_pr_state is distinct from 'torn_down'
		  and app.slug <> all($5::text[])
		  and not exists (select 1 from pr_preview_sets other_set
		    where (other_set.installation_id, other_set.repo_full_name, other_set.pr_number)
		      <> ($6, $7, $4)
		      and other_set.member_app_ids @> array[app.id::text])`,
		previous, desired[0].AccountID, desired[0].ProjectID, head.PRNumber, slugs,
		head.InstallationID, head.RepoFullName)
	if err != nil {
		return fmt.Errorf("state: find replaceable PR preview members: %w", err)
	}
	var retired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("state: scan replaceable PR preview member: %w", err)
		}
		retired = append(retired, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("state: iterate replaceable PR preview members: %w", err)
	}
	rows.Close()
	if len(retired) == 0 {
		return nil
	}
	var activeBuckets bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from object_buckets
		where app_id::text = any($1::text[]) and state <> 'deleted')`, retired).Scan(&activeBuckets); err != nil {
		return fmt.Errorf("state: check replaceable preview buckets: %w", err)
	}
	if activeBuckets {
		return ErrConflict
	}
	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, `update apps set status = 'deleted', preview_pr_state = $2,
		preview_expires_at = $3, deleted_at = coalesce(deleted_at, $3),
		delete_grace_until = coalesce(delete_grace_until, $4)
		where id::text = any($1::text[]) and status <> 'deleted'`,
		retired, PreviewPrStateStale, now, now.Add(AppDeleteGraceDuration()))
	if err != nil {
		return fmt.Errorf("state: retire replaced PR preview members: %w", err)
	}
	if tag.RowsAffected() != int64(len(retired)) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) ReservePRPreviewSet(_ context.Context, head PRPreviewHead, apps []App, limits api.Limits) ([]App, error) {
	if err := validatePRPreviewHead(head, apps); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[apps[0].AccountID]; !ok {
		return nil, ErrNotFound
	}
	key := previewSetKey(head.InstallationID, head.RepoFullName, head.PRNumber)
	previous := m.previewSets[key]
	if previous.RootAppID != "" {
		root, ok := m.apps[previous.RootAppID]
		if !ok || root.AccountID != apps[0].AccountID || root.ProjectID != apps[0].ProjectID {
			return nil, ErrConflict
		}
	}
	original := make(map[string]App)
	var inserted []string
	rollback := func(err error) ([]App, error) {
		for _, id := range inserted {
			delete(m.apps, id)
		}
		for id, app := range original {
			m.apps[id] = app
		}
		return nil, err
	}
	desiredSlugs := make(map[string]bool, len(apps))
	for _, app := range apps {
		desiredSlugs[app.Slug] = true
	}
	for _, id := range previous.MemberAppIDs {
		old, ok := m.apps[id]
		if !ok || old.AccountID != apps[0].AccountID || old.ProjectID != apps[0].ProjectID ||
			old.PreviewPrNumber != head.PRNumber || old.PreviewOfSlug == "" || old.Status == AppDeleted || desiredSlugs[old.Slug] ||
			old.PreviewPrState == PreviewPrStateTearingDown || old.PreviewPrState == PreviewPrStateTornDown {
			continue
		}
		shared := false
		for otherKey, set := range m.previewSets {
			if otherKey == key {
				continue
			}
			for _, memberID := range set.MemberAppIDs {
				shared = shared || memberID == id
			}
		}
		if shared {
			continue
		}
		for _, bucket := range m.objectBuckets {
			if bucket.AppID == id && bucket.State != "deleted" {
				return rollback(ErrConflict)
			}
		}
		original[id] = old
		now := time.Now().UTC()
		deadline := now.Add(AppDeleteGraceDuration())
		old.Status, old.PreviewPrState, old.PreviewExpiresAt = AppDeleted, PreviewPrStateStale, &now
		if old.DeletedAt == nil {
			old.DeletedAt = &now
		}
		if old.DeleteGraceUntil == nil {
			old.DeleteGraceUntil = &deadline
		}
		m.apps[id] = old
	}
	reserved := make([]App, 0, len(apps))
	for _, desired := range apps {
		row, err := m.createAppIfUnderQuotaLocked(desired, limits)
		newlyInserted := err == nil
		if errors.Is(err, ErrConflict) {
			for _, existing := range m.apps {
				if existing.Slug == desired.Slug && existing.Status != AppDeleted {
					row = existing
					if samePRPreview(existing, desired) && existing.PreviewPrState != PreviewPrStateTearingDown && existing.PreviewPrState != PreviewPrStateTornDown {
						err = nil
					}
					break
				}
			}
		} else if err == nil {
			inserted = append(inserted, row.ID)
		}
		if err != nil {
			return rollback(err)
		}
		if !newlyInserted {
			original[row.ID] = row
		}
		row.PreviewPrState = PreviewPrStateOpen
		row.PreviewExpiresAt = desired.PreviewExpiresAt
		m.apps[row.ID] = row
		reserved = append(reserved, row)
	}
	set := PRPreviewSet{InstallationID: head.InstallationID, RepoFullName: head.RepoFullName,
		PRNumber: head.PRNumber, CommitSHA: head.CommitSHA, RootAppID: reserved[0].ID}
	for _, app := range reserved {
		set.MemberAppIDs = append(set.MemberAppIDs, app.ID)
	}
	if err := validatePRPreviewSet(set); err != nil {
		return rollback(err)
	}
	if m.previewSets == nil {
		m.previewSets = make(map[string]PRPreviewSet)
	}
	m.previewSets[key] = set
	return reserved, nil
}
