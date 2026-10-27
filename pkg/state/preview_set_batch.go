package state

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"strings"
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

// PreviewTombstoneStore frees a preview slug that a deleted row of the same
// preview still holds. Teardown soft-deletes a preview, and the tombstone
// keeps its globally unique slug for the seven-day restore window, so a push
// to an expired PR's branch, or a PR reopened after its close grace, could
// not re-create its preview at the same pr-<n>-<slug> host until the purge.
type PreviewTombstoneStore interface {
	ReleaseDeletedPreviewSlug(ctx context.Context, desired App) (bool, error)
}

var (
	_ PreviewTombstoneStore = (*PgStore)(nil)
	_ PreviewTombstoneStore = (*MemStore)(nil)
)

// releasesPreviewTombstone reports whether existing is a deleted row of the
// preview desired re-creates: same account, organization, project, parent and
// pull request. Any other holder keeps the slug.
func releasesPreviewTombstone(existing, desired App) bool {
	return existing.Status == AppDeleted && existing.PreviewOfSlug != "" && samePRPreview(existing, desired)
}

// previewTombstoneSlug is the ID-derived slug a released tombstone moves to,
// the one retired PR set members already use.
func previewTombstoneSlug(id string) string {
	return "retired-pr-" + strings.ReplaceAll(id, "-", "")
}

func releasePreviewTombstoneTx(ctx context.Context, tx pgx.Tx, id string) error {
	tag, err := tx.Exec(ctx, `update apps set slug = 'retired-pr-' || replace(id::text, '-', '')
		where id = $1 and status = 'deleted'`, id)
	if err != nil {
		return fmt.Errorf("state: release deleted preview slug: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ReleaseDeletedPreviewSlug(ctx context.Context, desired App) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: begin preview slug release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	existing, err := scanApp(tx.QueryRow(ctx, `select `+appsSelectColumns+` from apps where slug = $1 for update`, desired.Slug))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !releasesPreviewTombstone(existing, desired) {
		return false, nil
	}
	if err := releasePreviewTombstoneTx(ctx, tx, existing.ID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit preview slug release: %w", err)
	}
	return true, nil
}

func (m *MemStore) ReleaseDeletedPreviewSlug(_ context.Context, desired App) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, existing := range m.apps {
		if existing.Slug == desired.Slug && releasesPreviewTombstone(existing, desired) {
			existing.Slug = previewTombstoneSlug(id)
			m.apps[id] = existing
			return true, nil
		}
	}
	return false, nil
}

// PRPreviewReopenStore renews a GitHub PR preview's lease and reopens it
// under the environment allowance: a closed preview holds none, so
// reopening it takes one. ErrNotFound means the row is no longer a live PR
// preview.
type PRPreviewReopenStore interface {
	ReopenPRPreview(ctx context.Context, appID string, expiresAt time.Time, limits api.Limits) (App, error)
}

var (
	_ PRPreviewReopenStore = (*PgStore)(nil)
	_ PRPreviewReopenStore = (*MemStore)(nil)
)

func reopenablePRPreview(app App) bool {
	return app.PreviewOfSlug != "" && app.PreviewPrNumber > 0 && app.Status != AppDeleted &&
		app.PreviewPrState != PreviewPrStateTearingDown && app.PreviewPrState != PreviewPrStateTornDown
}

func (s *PgStore) ReopenPRPreview(ctx context.Context, appID string, expiresAt time.Time, limits api.Limits) (App, error) {
	current, err := s.AppByID(ctx, appID)
	if err != nil {
		return App{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, fmt.Errorf("state: begin preview reopen: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after Commit
	// Account, then app row, then the quota owner: the order preview set
	// reservations take.
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, current.AccountID).Scan(&locked); err != nil {
		return App{}, fmt.Errorf("state: lock preview account: %w", mapErr(err))
	}
	app, err := scanApp(tx.QueryRow(ctx, `select `+appsSelectColumns+` from apps where id = $1 for update`, appID))
	if err != nil {
		return App{}, err
	}
	if !reopenablePRPreview(app) {
		return App{}, ErrNotFound
	}
	if app.PreviewPrState != PreviewPrStateOpen {
		reopened := app
		reopened.PreviewPrState = PreviewPrStateOpen
		if err := checkAppQuotaDeltaTx(ctx, tx, reopened, limits, 1); err != nil {
			return App{}, err
		}
	}
	app, err = scanApp(tx.QueryRow(ctx, `update apps set preview_pr_state = $2, preview_expires_at = $3
		where id = $1 returning `+appsSelectColumns, appID, PreviewPrStateOpen, expiresAt))
	if err != nil {
		return App{}, fmt.Errorf("state: reopen preview: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, fmt.Errorf("state: commit preview reopen: %w", err)
	}
	return app, nil
}

func (m *MemStore) ReopenPRPreview(_ context.Context, appID string, expiresAt time.Time, limits api.Limits) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || !reopenablePRPreview(app) {
		return App{}, ErrNotFound
	}
	if app.PreviewPrState != PreviewPrStateOpen {
		reopened := app
		reopened.PreviewPrState = PreviewPrStateOpen
		if err := m.checkAppQuotaDeltaInLocked(reopened, limits, 1, m.apps); err != nil {
			return App{}, err
		}
	}
	app.PreviewPrState = PreviewPrStateOpen
	app.PreviewExpiresAt = &expiresAt
	m.apps[appID] = app
	return app, nil
}

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
	apps = append([]App(nil), apps...)
	needsPersonalOrg := false
	for _, app := range apps {
		if app.OrgID == "" {
			needsPersonalOrg = true
			break
		}
	}
	if needsPersonalOrg {
		personalOrg, err := s.OrgByPersonalAccount(ctx, apps[0].AccountID)
		if err != nil {
			return nil, err
		}
		for i := range apps {
			if apps[i].OrgID == "" {
				apps[i].OrgID = personalOrg.ID
			}
		}
	}
	tx, err := s.beginAccountAppTrafficMutation(ctx, apps[0].AccountID, "")
	if err != nil {
		return nil, fmt.Errorf("state: begin PR preview replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after Commit
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
		// A deleted row still owns its globally unique slug. Detect it before
		// INSERT; a uniqueness error would abort the whole PostgreSQL transaction.
		app, lookupErr := scanApp(tx.QueryRow(ctx,
			`select `+appsSelectColumns+` from apps where slug = $1 for update`, desired.Slug))
		if lookupErr == nil && releasesPreviewTombstone(app, desired) {
			if err := releasePreviewTombstoneTx(ctx, tx, app.ID); err != nil {
				return nil, err
			}
			lookupErr = ErrNotFound
		}
		switch {
		case lookupErr == nil:
			if app.Status == AppDeleted || !samePRPreview(app, desired) ||
				app.PreviewPrState == PreviewPrStateTearingDown || app.PreviewPrState == PreviewPrStateTornDown {
				return nil, ErrConflict
			}
			// A closed preview holds no allowance; reopening it takes one.
			if app.PreviewPrState != PreviewPrStateOpen {
				reopened := app
				reopened.PreviewPrState = PreviewPrStateOpen
				if err := checkAppQuotaDeltaTx(ctx, tx, reopened, limits, 1); err != nil {
					return nil, err
				}
			}
			if err := refreshPreviewGatesTx(ctx, tx, app.ID, desired); err != nil {
				return nil, err
			}
		case errors.Is(lookupErr, ErrNotFound):
			app, err = createAppIfUnderQuotaTx(ctx, tx, desired, limits)
			if err != nil {
				return nil, err
			}
		default:
			return nil, lookupErr
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
		member_app_ids = excluded.member_app_ids, closed_at = null, setup_error = null, updated_at = now()`,
		set.InstallationID, set.RepoFullName, set.PRNumber, set.CommitSHA, set.RootAppID, set.MemberAppIDs); err != nil {
		return nil, fmt.Errorf("state: replace PR preview set: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit PR preview replacement: %w", err)
	}
	return reserved, nil
}

// refreshPreviewGatesTx copies the desired access and egress gates onto a
// reused preview row. githubd derives them from the production app on every
// PR head, so a gate the production app gained since the preview was created
// (or a preview created before gates were copied) applies on the next push.
// A changed row is announced so gateways drop their cached copy.
func refreshPreviewGatesTx(ctx context.Context, tx pgx.Tx, id string, desired App) error {
	publicAuthMode := desired.PublicAuthMode
	if publicAuthMode == "" {
		publicAuthMode = AppPublicAuthModeOpen
	}
	consumerAuth := desired.ConsumerAuthMode
	if consumerAuth == "" {
		consumerAuth = ConsumerAuthModeOptional
	}
	visibility := string(api.NormalizeAppVisibility(desired.Visibility))
	_, err := tx.Exec(ctx, `with changed as (
			update apps set require_authn = $2, public_auth_mode = $3, public_auth_basic = $4,
			       public_auth_ip_allowlist = $5, consumer_auth_mode = $6, require_signed = $7,
			       egress_allowlist = $8, visibility = $9
			where id = $1
			  and (require_authn, public_auth_mode, public_auth_basic, public_auth_ip_allowlist,
			       consumer_auth_mode, require_signed, egress_allowlist, visibility)
			      is distinct from ($2, $3, $4::bytea, $5::cidr[], $6, $7, $8::cidr[], $9)
			returning id)
		select pg_notify('app_changed', id::text) from changed`,
		id, desired.RequireAuthn, publicAuthMode, desired.PublicAuthBasicSealed,
		cidrPrefixesToArray(desired.PublicAuthIPAllowlist), string(consumerAuth), desired.RequireSigned,
		cidrPrefixesToArray(desired.EgressAllowlist), visibility)
	if err != nil {
		return fmt.Errorf("state: refresh PR preview gates: %w", err)
	}
	return nil
}

// copyPreviewGates is refreshPreviewGatesTx for MemStore.
func copyPreviewGates(row, desired App) App {
	row.RequireAuthn = desired.RequireAuthn
	row.PublicAuthMode = desired.PublicAuthMode
	row.PublicAuthBasicSealed = append([]byte(nil), desired.PublicAuthBasicSealed...)
	row.PublicAuthIPAllowlist = append([]netip.Prefix(nil), desired.PublicAuthIPAllowlist...)
	row.ConsumerAuthMode = desired.ConsumerAuthMode
	row.RequireSigned = desired.RequireSigned
	row.EgressAllowlist = append([]netip.Prefix(nil), desired.EgressAllowlist...)
	row.Visibility = desired.Visibility
	return row
}

// PRPreviewSetupFailureStore records why githubd could not reserve or start
// a PR head. Recording moves the set to that head, so deployments of the
// previous head can no longer report for the PR, and the set reads as failed
// until a later head is reserved. A PR without a recorded set has nothing to
// record against; ErrNotFound reports it.
type PRPreviewSetupFailureStore interface {
	FailPRPreviewSetup(ctx context.Context, head PRPreviewHead, reason string) error
}

var (
	_ PRPreviewSetupFailureStore = (*PgStore)(nil)
	_ PRPreviewSetupFailureStore = (*MemStore)(nil)
)

// PRPreviewSetupErrorMaxLen bounds the recorded reason (pr_preview_sets_setup_error_chk).
const PRPreviewSetupErrorMaxLen = 1000

func boundedSetupError(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		reason = "preview setup failed"
	}
	if runes := []rune(reason); len(runes) > PRPreviewSetupErrorMaxLen {
		reason = string(runes[:PRPreviewSetupErrorMaxLen-1]) + "…"
	}
	return reason
}

func (s *PgStore) FailPRPreviewSetup(ctx context.Context, head PRPreviewHead, reason string) error {
	if head.InstallationID <= 0 || head.RepoFullName == "" || head.PRNumber <= 0 || !previewSetSHA.MatchString(head.CommitSHA) {
		return fmt.Errorf("state: invalid PR preview head: %w", ErrConflict)
	}
	tag, err := s.pool.Exec(ctx, `update pr_preview_sets set commit_sha = $4, setup_error = $5, updated_at = now()
		where installation_id = $1 and repo_full_name = $2 and pr_number = $3 and closed_at is null`,
		head.InstallationID, head.RepoFullName, head.PRNumber, head.CommitSHA, boundedSetupError(reason))
	if err != nil {
		return fmt.Errorf("state: record PR preview setup failure: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *MemStore) FailPRPreviewSetup(_ context.Context, head PRPreviewHead, reason string) error {
	if head.InstallationID <= 0 || head.RepoFullName == "" || head.PRNumber <= 0 || !previewSetSHA.MatchString(head.CommitSHA) {
		return fmt.Errorf("state: invalid PR preview head: %w", ErrConflict)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := previewSetKey(head.InstallationID, head.RepoFullName, head.PRNumber)
	set, ok := m.previewSets[key]
	if !ok || set.Closed {
		return ErrNotFound
	}
	set.CommitSHA, set.SetupError = head.CommitSHA, boundedSetupError(reason)
	m.previewSets[key] = set
	return nil
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
	// Slugs are globally unique even on deleted rows. Move only these retired
	// PR previews to ID-derived tombstone slugs so a later PR head can add the
	// same dependency again without reviving old builds or changing app IDs.
	tag, err := tx.Exec(ctx, `update apps set status = 'deleted',
		slug = 'retired-pr-' || replace(id::text, '-', ''), preview_pr_state = $2,
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

func (m *MemStore) ReservePRPreviewSet(ctx context.Context, head PRPreviewHead, apps []App, limits api.Limits) ([]App, error) {
	if err := validatePRPreviewHead(head, apps); err != nil {
		return nil, err
	}
	apps = append([]App(nil), apps...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[apps[0].AccountID]; !ok {
		return nil, ErrNotFound
	}
	for i := range apps {
		m.ensureAppOrgLocked(&apps[i])
	}
	key := previewSetKey(head.InstallationID, head.RepoFullName, head.PRNumber)
	previous := m.previewSets[key]
	if previous.RootAppID != "" {
		root, ok := m.apps[previous.RootAppID]
		if !ok || root.AccountID != apps[0].AccountID || root.ProjectID != apps[0].ProjectID {
			return nil, ErrConflict
		}
	}
	stagedApps := maps.Clone(m.apps)
	desiredSlugs := make(map[string]bool, len(apps))
	for _, app := range apps {
		desiredSlugs[app.Slug] = true
	}
	for _, id := range previous.MemberAppIDs {
		old, ok := stagedApps[id]
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
				return nil, ErrConflict
			}
		}
		now := time.Now().UTC()
		deadline := now.Add(AppDeleteGraceDuration())
		old.Slug = "retired-pr-" + strings.ReplaceAll(old.ID, "-", "")
		old.Status, old.PreviewPrState, old.PreviewExpiresAt = AppDeleted, PreviewPrStateStale, &now
		if old.DeletedAt == nil {
			old.DeletedAt = &now
		}
		if old.DeleteGraceUntil == nil {
			old.DeleteGraceUntil = &deadline
		}
		stagedApps[id] = old
	}
	reserved := make([]App, 0, len(apps))
	var inserted []App
	for _, desired := range apps {
		var row App
		found := false
		for id, existing := range stagedApps {
			if existing.Slug != desired.Slug {
				continue
			}
			if releasesPreviewTombstone(existing, desired) {
				existing.Slug = previewTombstoneSlug(id)
				stagedApps[id] = existing
				continue
			}
			if existing.Status == AppDeleted || !samePRPreview(existing, desired) ||
				existing.PreviewPrState == PreviewPrStateTearingDown || existing.PreviewPrState == PreviewPrStateTornDown {
				return nil, ErrConflict
			}
			if existing.PreviewPrState != PreviewPrStateOpen {
				reopened := existing
				reopened.PreviewPrState = PreviewPrStateOpen
				if err := m.checkAppQuotaDeltaInLocked(reopened, limits, 1, stagedApps); err != nil {
					return nil, err
				}
			}
			row, found = copyPreviewGates(existing, desired), true
			break
		}
		if !found {
			var err error
			row, err = m.prepareAppIfUnderQuotaLocked(desired, limits, stagedApps)
			if err != nil {
				return nil, err
			}
			inserted = append(inserted, row)
		}
		row.PreviewPrState = PreviewPrStateOpen
		row.PreviewExpiresAt = desired.PreviewExpiresAt
		stagedApps[row.ID] = row
		reserved = append(reserved, row)
	}
	set := PRPreviewSet{InstallationID: head.InstallationID, RepoFullName: head.RepoFullName,
		PRNumber: head.PRNumber, CommitSHA: head.CommitSHA, RootAppID: reserved[0].ID}
	for _, app := range reserved {
		set.MemberAppIDs = append(set.MemberAppIDs, app.ID)
	}
	if err := validatePRPreviewSet(set); err != nil {
		return nil, err
	}
	if err := appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, apps[0].AccountID, nil, "", memTrafficPolicyChange{Apps: stagedApps})); err != nil {
		return nil, err
	}
	if err := m.enrollNewAppsLocked(inserted); err != nil {
		return nil, err
	}
	m.apps = stagedApps
	for _, app := range inserted {
		m.ensureServiceAddressIndexLocked(&app)
	}
	if m.previewSets == nil {
		m.previewSets = make(map[string]PRPreviewSet)
	}
	m.previewSets[key] = set
	return reserved, nil
}
