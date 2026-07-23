// Package state — Postgres-backed Store (spec §5, ADR-006, CLAUDE.md
// "SQL via sqlc only").
//
// pgstore.go implements the Store interface against the Postgres schema in
// migrations/*.sql. The SQL itself lives in queries.sql so the codegen
// tooling (sqlc.yaml) is the canonical source — this file is the thin
// adapter that maps sqlc-style params/rows to the domain types and surfaces
// ErrNotFound / ErrConflict at the right boundaries.
//
// Why not the sqlc-generated *.sql.go files? sqlc couldn't be built in this
// environment (the pganalyze/pg_query_go dependency fails to compile on the
// macOS SDK's _string.h). The hand-written adapter here is structured so it
// can be swapped for the generated package one-for-one once sqlc is
// available; the public Store surface is unchanged.
//
// TODO(M5.1): regenerate via `sqlc generate` against pkg/state/queries.sql
// once the CI sqlc pin is clean on the macOS SDK. See ADR-017
// (docs/adr/017-hand-written-pgstore.md) for the migration plan and
// reviewer checklist.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// PgStore implements Store against Postgres. It holds a connection pool and
// is safe for concurrent use.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore wraps a pool. The pool is owned by the caller; PgStore does not
// close it on shutdown so daemons can share a single pool across a Store and
// their LISTEN goroutine.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// Compile-time check.
var _ Store = (*PgStore)(nil)

// --- accounts ---------------------------------------------------------------

func (s *PgStore) CreateAccount(ctx context.Context, email string, plan api.Plan) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`insert into accounts (email, plan, status) values ($1, $2, 'active') returning id, email, plan, status, coalesce(stripe_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at`,
		email, string(plan))
	acct, err := scanAccount(row)
	if err != nil {
		// Funnel through mapErr so a unique-email collision surfaces as
		// state.ErrConflict (the same shape every other insert returns).
		// A future hardening could use `on conflict (email) do nothing
		// returning ...` to make the race atomic; today the handler
		// ladder AccountByEmail → CreateAccount relies on this funnel
		// to detect the dup-key outcome.
		return Account{}, mapErr(err)
	}
	return acct, nil
}

func (s *PgStore) AccountByID(ctx context.Context, id string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at from accounts where id = $1`, id)
	return scanAccount(row)
}

func (s *PgStore) AccountByEmail(ctx context.Context, email string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at from accounts where email = $1`, email)
	return scanAccount(row)
}

func (s *PgStore) AccountByKeyHash(ctx context.Context, hash []byte) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select a.id, a.email, a.plan, a.status, coalesce(a.stripe_customer_id,''), coalesce(a.stripe_subscription_item,''), a.created_at, a.deletion_requested_at, a.last_quota_warning_at, a.past_due_at
		 from accounts a join api_keys k on k.account_id = a.id where k.key_sha256 = $1`, hash)
	return scanAccount(row)
}

func (s *PgStore) UpdateAccountPlan(ctx context.Context, id string, plan api.Plan) error {
	_, err := s.pool.Exec(ctx, `update accounts set plan = $2 where id = $1`, id, string(plan))
	return err
}

func (s *PgStore) UpdateAccountStatus(ctx context.Context, id string, status AccountStatus) error {
	_, err := s.pool.Exec(ctx, `update accounts set status = $2 where id = $1`, id, string(status))
	return err
}

// UpdateAccountStripeCustomerID records the Stripe `cus_…` ID on the
// account row. Schema carries a unique index on stripe_customer_id so a
// second customer picking up an old ID would fail at the DB; MemStore
// mirrors that with the same shape (single-value index map).
func (s *PgStore) UpdateAccountStripeCustomerID(ctx context.Context, id, stripeCustomerID string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts set stripe_customer_id = $2 where id = $1`,
		id, stripeCustomerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAccountStripeSubscriptionItem records the Stripe subscription
// item ID (si_…) on the account row (issue #52). meterd's hourly push
// reads this to know where to POST the UsageRecord; the value is empty
// until pkg/stripex::EnsureCustomer receives
// customer.subscription.created. MemStore mirrors the column shape.
func (s *PgStore) UpdateAccountStripeSubscriptionItem(ctx context.Context, id, subItem string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts set stripe_subscription_item = $2 where id = $1`,
		id, subItem)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AccountByStripeCustomerID resolves the account behind a Stripe webhook
// payload. The unique index makes this O(log n); MemStore does it with a
// map.
func (s *PgStore) AccountByStripeCustomerID(ctx context.Context, stripeCustomerID string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at
		 from accounts where stripe_customer_id = $1`,
		stripeCustomerID)
	return scanAccount(row)
}

// ListAllAccounts returns every account. Meterd walks this on the quota
// tick + hourly Stripe push; bounded by the customer count on the box.
func (s *PgStore) ListAllAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at
		 from accounts order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

// scanAccounts reads a rows iterator of account rows. Shared with
// ListAllAccounts so MemStore doesn't have to duplicate the scan
// logic on top of the per-row scanner.
func scanAccounts(rows pgx.Rows) ([]Account, error) {
	var out []Account
	for rows.Next() {
		a, err := scanAccountCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanAccountCols is the shared column reader for the Account shape
// used by every read path. deletion_requested_at, last_quota_warning_at,
// and past_due_at are nullable; we scan into *time.Time and lift them
// onto the Account when non-NULL.
func scanAccountCols(scan func(...any) error) (Account, error) {
	a := Account{}
	var planStr, statusStr string
	var deletionAt, lastWarnAt, pastDueAt *time.Time
	if err := scan(&a.ID, &a.Email, &planStr, &statusStr, &a.StripeCustomerID, &a.StripeSubscriptionItem, &a.CreatedAt, &deletionAt, &lastWarnAt, &pastDueAt); err != nil {
		return Account{}, err
	}
	a.Plan = api.Plan(planStr)
	a.Status = AccountStatus(statusStr)
	if deletionAt != nil {
		a.DeletionRequestedAt = deletionAt
	}
	if lastWarnAt != nil {
		a.LastQuotaWarningAt = lastWarnAt
	}
	if pastDueAt != nil {
		a.PastDueAt = pastDueAt
	}
	return a, nil
}

// --- api keys ----------------------------------------------------------------

func (s *PgStore) CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label) values ($1, $2, $3)
		 returning id, account_id, key_sha256, coalesce(label,''), created_at`,
		accountID, hash, nullString(label))
	k := APIKey{}
	var hashBytes []byte
	if err := row.Scan(&k.ID, &k.AccountID, &hashBytes, &k.Label, &k.CreatedAt); err != nil {
		return APIKey{}, mapErr(err)
	}
	k.Hash = hashBytes
	return k, nil
}

func (s *PgStore) DeleteAPIKey(ctx context.Context, accountID, keyID string) error {
	tag, err := s.pool.Exec(ctx, `delete from api_keys where id = $1 and account_id = $2`, keyID, accountID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, key_sha256, coalesce(label,''), created_at from api_keys where account_id = $1 order by created_at desc`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k := APIKey{}
		if err := rows.Scan(&k.ID, &k.AccountID, &k.Hash, &k.Label, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *PgStore) TouchKeyLastUsed(ctx context.Context, keyID string) error {
	_, err := s.pool.Exec(ctx, `update api_keys set last_used_at = now() where id = $1`, keyID)
	return err
}

// --- apps --------------------------------------------------------------------

func (s *PgStore) CreateApp(ctx context.Context, app App) (App, error) {
	manifest := app.Manifest
	if manifest.Entrypoint == nil && manifest.Env == nil {
		manifest = AppManifest{}
	}
	manifestBytes, _ := json.Marshal(manifest)
	runtime := nullString(app.Runtime)
	idle := nullableInt(app.IdleTimeoutS)
	row := s.pool.QueryRow(ctx,
		`insert into apps (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest, min_instances)
		 values ($1, $2, $3, $4, $5, $6, $7, 'active', $8::jsonb, $9)
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at, min_instances`,
		app.AccountID, app.Slug, string(app.Type), runtime, app.RAMMB, idle, app.MaxConcurrency, manifestBytes, app.MinInstances)
	return scanApp(row)
}

// CreateAppIfUnderQuota inserts an app iff the account currently holds
// fewer than limits.DeployedApps live apps (active + evicted_cold). The
// count + insert run inside a single transaction that SELECT … FOR UPDATE
// locks the parent accounts row, so two concurrent calls on a metered
// plan cannot both pass the cap check (closes the TOCTOU in the handler).
//
// Returns:
//   - (App, nil) on success
//   - (App{}, *QuotaError) when the cap is reached
//   - (App{}, ErrConflict) on slug collision (apps.slug unique index)
//   - (App{}, ErrNotFound) when the account row is gone
//
// The lock is on the single accounts row — the request blocks behind any
// other createApp for the same account only. Cross-account inserts don't
// contend, so the one-box stays well under its max_concurrency ceiling.
func (s *PgStore) CreateAppIfUnderQuota(ctx context.Context, app App, limits api.Limits) (App, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return App{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Lock the parent accounts row. SELECT 1 + FOR UPDATE keeps the
	//    lock acquisition in one round-trip; the FOR UPDATE blocks any
	//    concurrent createApp for the same account until COMMIT/ROLLBACK.
	//    apps_account_idx (account_id, status) exists from migration 00001
	//    so the lock search is an index hit.
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from accounts where id = $1 for update`, app.AccountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return App{}, ErrNotFound
		}
		return App{}, fmt.Errorf("state: lock account %s: %w", app.AccountID, err)
	}

	// 2. Authoritative count under the lock. Same predicate as
	//    CountDeployedApps — matches the MemStore shape so handlers
	//    don't have to know which store is in use.
	var observed int
	if err := tx.QueryRow(ctx,
		`select count(*) from apps where account_id = $1 and status in ('active','evicted_cold')`,
		app.AccountID).Scan(&observed); err != nil {
		return App{}, fmt.Errorf("state: count apps for account %s: %w", app.AccountID, err)
	}
	if observed >= limits.DeployedApps {
		return App{}, &QuotaError{Limit: limits.DeployedApps, Observed: observed}
	}

	// 3. Conditional insert. The slug unique index surfaces a collision
	//    as a pgx unique-violation SQLSTATE; mapErr wraps it in ErrConflict.
	manifest := app.Manifest
	if manifest.Entrypoint == nil && manifest.Env == nil {
		manifest = AppManifest{}
	}
	manifestBytes, _ := json.Marshal(manifest)
	runtime := nullString(app.Runtime)
	idle := nullableInt(app.IdleTimeoutS)
	row := tx.QueryRow(ctx,
		`insert into apps (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest, min_instances)
		 values ($1, $2, $3, $4, $5, $6, $7, 'active', $8::jsonb, $9)
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at, min_instances`,
		app.AccountID, app.Slug, string(app.Type), runtime, app.RAMMB, idle, app.MaxConcurrency, manifestBytes, app.MinInstances)
	created, err := scanApp(row)
	if err != nil {
		return App{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, fmt.Errorf("state: commit create app: %w", err)
	}
	return created, nil
}

func (s *PgStore) AppByID(ctx context.Context, id string) (App, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at, min_instances
		 from apps where id = $1`, id)
	return scanApp(row)
}

func (s *PgStore) AppBySlug(ctx context.Context, slug string) (App, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at, min_instances
		 from apps where slug = $1 and status <> 'deleted'`, slug)
	return scanApp(row)
}

func (s *PgStore) ListApps(ctx context.Context, accountID string) ([]App, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at, min_instances
		 from apps where account_id = $1 and status <> 'deleted' order by created_at desc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

func (s *PgStore) ListAllApps(ctx context.Context) ([]App, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at, min_instances
		 from apps where status <> 'deleted' order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

func (s *PgStore) CountDeployedApps(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from apps where account_id = $1 and status in ('active','evicted_cold')`,
		accountID).Scan(&n)
	return n, err
}

func (s *PgStore) UpdateApp(ctx context.Context, id string, p UpdateAppParams) (App, error) {
	manifestBytes := []byte(nil)
	if p.Manifest != nil {
		manifestBytes, _ = json.Marshal(*p.Manifest)
	}
	row := s.pool.QueryRow(ctx,
		`update apps set
		   ram_mb          = coalesce($2, ram_mb),
		   idle_timeout_s  = case when $3 then $4 else idle_timeout_s end,
		   max_concurrency = coalesce($5, max_concurrency),
		   status          = coalesce($6, status),
		   manifest        = case when $7 then $8::jsonb else manifest end,
		   min_instances   = case when $9 then $10 else min_instances end
		 where id = $1
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at, min_instances`,
		id,
		p.RAMMB, p.SetIdleTimeout, derefInt(p.IdleTimeoutS),
		p.MaxConcurrency, nullAppStatus(p.Status),
		p.Manifest != nil, manifestBytes,
		p.SetMinInstances, derefInt(p.MinInstances))
	return scanApp(row)
}

// SetAppMinInstances stamps the per-app floor (ux_spec §6.5). Plan-tier
// gating is the apid handler's job — the store writes the column
// unconditionally. Returns ErrNotFound when the app is gone so a
// redelivered PATCH returns 404 cleanly.
func (s *PgStore) SetAppMinInstances(ctx context.Context, appID string, min int) error {
	tag, err := s.pool.Exec(ctx,
		`update apps set min_instances = $2 where id = $1`, appID, min)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RenameApp changes an app's slug atomically (issue #63). The UPDATE
// is scoped to (account_id, oldSlug, status<>'deleted') so a wrong
// accountID or unknown slug returns ErrNotFound via mapErr → pgx.ErrNoRows.
// The apps.slug unique constraint surfaces a duplicate newSlug as
// ErrConflict via mapErr → unique-violation SQLSTATE. RETURNING mirrors
// the same scanApp shape used by AppByID.
//
// Both PgStore and MemStore share the same error contract so the apid
// handler can branch on errors.Is without checking the concrete type.
func (s *PgStore) RenameApp(ctx context.Context, accountID, oldSlug, newSlug string) (App, error) {
	row := s.pool.QueryRow(ctx,
		`update apps set slug = $3
		 where account_id = $1 and slug = $2 and status <> 'deleted'
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at, min_instances`,
		accountID, oldSlug, newSlug)
	return scanApp(row)
}

func (s *PgStore) DeleteApp(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update apps set status = 'deleted' where id = $1`, id)
	return err
}

// RecordGitHubBinding writes the (install_id, repo_full_name,
// production_branch) tuple onto the apps row. Idempotent: re-binding
// the same app overwrites the prior values. The migration's unique
// partial index on (install_id, repo_full_name) rejects the write
// if a different app already holds that pair — pgx returns a
// unique-violation we surface as ErrNotFound + a wrapped error so
// the /oauth/callback handler can render a clean 409.
//
// Per migration 00007: apps.github_install_id is BIGINT NULL,
// apps.github_repo_full_name is TEXT NULL,
// apps.github_production_branch is TEXT NULL.
func (s *PgStore) RecordGitHubBinding(ctx context.Context, appID string, installID int64, repoFullName, productionBranch string) error {
	_, err := s.pool.Exec(ctx,
		`update apps
		 set github_install_id = $2,
		     github_repo_full_name = $3,
		     github_production_branch = $4
		 where id = $1`,
		appID, installID, repoFullName, nullString(productionBranch))
	return err
}

// GitHubBindingForApp reads the binding columns off the apps row.
// Returns ErrNotFound when the app has never been GitHub-connected
// (install_id is NULL).
func (s *PgStore) GitHubBindingForApp(ctx context.Context, appID string) (GitHubBinding, error) {
	var b GitHubBinding
	var installID *int64
	var repoFullName *string
	var branch *string
	err := s.pool.QueryRow(ctx,
		`select id, github_install_id, github_repo_full_name, github_production_branch
		 from apps where id = $1`, appID,
	).Scan(&b.AppID, &installID, &repoFullName, &branch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubBinding{}, ErrNotFound
		}
		return GitHubBinding{}, err
	}
	if installID == nil {
		return GitHubBinding{}, ErrNotFound
	}
	b.InstallID = *installID
	if repoFullName != nil {
		b.RepoFullName = *repoFullName
	}
	if branch != nil {
		b.ProductionBranch = *branch
	}
	return b, nil
}

// InstallationIDForRepo is the reverse lookup githubd's checks.go
// uses to mint the right per-install access token for a push
// (review finding #1+#2 closure). Uses the
// apps_github_install_id_idx partial index when available (most
// installations bind one repo to one app), but the query also
// filters on repo_full_name so the index isn't strictly required.
func (s *PgStore) InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error) {
	var installID int64
	err := s.pool.QueryRow(ctx,
		`select github_install_id
		 from apps
		 where github_repo_full_name = $1
		   and github_install_id is not null
		 limit 1`, repoFullName,
	).Scan(&installID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return installID, nil
}

// --- deployments -------------------------------------------------------------

// CreateDeployment writes a pending deployment row only if the parent app is
// currently active. The active-app gate is the PR-A fix for the TOCTOU race
// where apid's AppBySlug could return a row whose status was flipped to
// `deleted` between the read and the INSERT — the previous shape silently
// stranded an orphan deployments row pointing at a soft-deleted app.
//
// Shape mirrors CreateAppIfUnderQuota (lines 287-343 above): a tx-scoped
// SELECT 1 FROM apps … FOR UPDATE serialises with concurrent updates to
// apps.status, and ErrNotFound on a 0-row result so apid's existing
// s.notFound path returns 404 without any change at the call site.
//
// AppDeleted apps must NOT accept new deployments; subsequent UpdateApp
// calls (PATCH /v1/apps/{slug}) reject status flips back to active for
// already-deleted rows anyway, so the invariant "an app either accepts
// deploys OR is deleted" is one-directional here.
func (s *PgStore) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Lock the parent apps row. SELECT 1 + FOR UPDATE keeps lock
	//    acquisition in one round-trip; apps.status flips are blocked
	//    behind this lock until COMMIT/ROLLBACK. apps_pkey is the
	//    primary key on id, so the lock search is an index hit.
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status = 'active' for update`,
		d.AppID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, fmt.Errorf("state: lock app %s: %w", d.AppID, err)
	}

	// 2. Insert under the lock. The FOR UPDATE above guarantees the app
	//    cannot transition to AppDeleted between this point and COMMIT.
	row := tx.QueryRow(ctx,
		`insert into deployments (app_id, image_digest, kind, source_path, source_bytes, handler, log_path, status)
		 values ($1, $2, $3, $4, $5, $6, $7, 'pending')
		 returning id, app_id, coalesce(build_id::text,''), image_digest, kind,
		           coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		           status, coalesce(error,''), coalesce(error_code,''), created_at`,
		d.AppID, d.ImageDigest, string(d.Kind), nullString(d.SourcePath), d.SourceBytes,
		nullString(d.Handler), nullString(d.LogPath))
	created, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: commit create deployment: %w", err)
	}
	return created, nil
}

func (s *PgStore) DeploymentByID(ctx context.Context, id string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        coalesce(rootfs_path,''), coalesce(rootfs_key,''), coalesce(rootfs_bytes,0),
		        status, coalesce(error,''), coalesce(error_code,''), created_at
		 from deployments where id = $1`, id)
	return scanDeploymentWithRootfs(row)
}

func (s *PgStore) LatestDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        status, coalesce(error,''), coalesce(error_code,''), created_at
		 from deployments where app_id = $1 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

func (s *PgStore) LiveDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        coalesce(rootfs_path,''), coalesce(rootfs_key,''), coalesce(rootfs_bytes,0),
		        status, coalesce(error,''), coalesce(error_code,''), created_at
		 from deployments where app_id = $1 and status = 'live' order by created_at desc limit 1`, appID)
	return scanDeploymentWithRootfs(row)
}

func (s *PgStore) LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        status, coalesce(error,''), coalesce(error_code,''), created_at
		 from deployments where app_id = $1 and status = 'superseded'
		 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

// ListDeploymentsForApp returns deployments for an app, ordered DESC by
// created_at. limit <= 0 means "no row cap" (every remaining row after
// offset) — same semantics as MemStore. F-10: the prior version forwarded
// `limit=0` to Postgres which treats LIMIT 0 as "0 rows"; the imaged
// caller (cleanupAppFiles → pkg/imaged/handler.go:932) walked an empty
// slice and silently kept every appsRoot/<slug>/ directory across an app
// delete. Now both backends return the full tail when limit<=0.
//
// Cursor note: callers that want page-by-page behaviour should pass an
// explicit positive limit (apids dashboard does — limit=25). The no-cap
// shape exists for the "iterate over every deployment we own" use case
// (imaged hard-delete on app change, code migrations, audit dumps). At
// v1 scale the per-app deployment count is O(deploy rate × app lifetime),
// bounded by spec §4.2 (DeployedApps ≤ plan_max). The index on
// (app_id, created_at desc) added in migration 00007 keeps this scan cheap.
func (s *PgStore) ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error) {
	if offset < 0 {
		offset = 0
	}
	// F-10: branch on limit rather than passing LIMIT 0 / LIMIT NULL to
	// Postgres; both yield 0 rows on the bare version, which is the bug
	// we're closing.
	var (
		rows pgx.Rows
		err  error
	)
	if limit > 0 {
		rows, err = s.pool.Query(ctx,
			`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
			        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
			        status, coalesce(error,''), coalesce(error_code,''), created_at
			 from deployments where app_id = $1 order by created_at desc limit $2 offset $3`,
			appID, limit, offset)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
			        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
			        status, coalesce(error,''), coalesce(error_code,''), created_at
			 from deployments where app_id = $1 order by created_at desc offset $2`,
			appID, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListDeploymentsForAccount returns every deployment whose app belongs
// to the account, ordered DESC by created_at. Cursor pagination: pass
// the previous response's last created_at as `before` to page
// backwards. before.IsZero() = first page.
//
// LIMIT/OFFSET isn't quite right here (timestamps can collide); we
// instead use a keyset filter `created_at < $2`. With an index on
// (account_id, created_at desc) — added in slice 4's migration as a
// forward-only addition so this stays cheap.
func (s *PgStore) ListDeploymentsForAccount(ctx context.Context, accountID string, before time.Time, limit int) ([]Deployment, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if before.IsZero() {
		rows, err = s.pool.Query(ctx,
			`select d.id, d.app_id, coalesce(d.build_id::text,''), d.image_digest, d.kind,
			        coalesce(d.source_path,''), coalesce(d.source_bytes,0), coalesce(d.handler,''), coalesce(d.log_path,''),
			        d.status, coalesce(d.error,''), coalesce(d.error_code,''), d.created_at
			 from deployments d join apps a on a.id = d.app_id
			 where a.account_id = $1 order by d.created_at desc limit $2`,
			accountID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select d.id, d.app_id, coalesce(d.build_id::text,''), d.image_digest, d.kind,
			        coalesce(d.source_path,''), coalesce(d.source_bytes,0), coalesce(d.handler,''), coalesce(d.log_path,''),
			        d.status, coalesce(d.error,''), coalesce(d.error_code,''), d.created_at
			 from deployments d join apps a on a.id = d.app_id
			 where a.account_id = $1 and d.created_at < $2
			 order by d.created_at desc limit $3`,
			accountID, before, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

func (s *PgStore) UpdateDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errMsg string) error {
	tag, err := s.pool.Exec(ctx, `update deployments set status = $2, error = $3 where id = $1`, id, string(status), nullString(errMsg))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) MarkDeploymentSuperseded(ctx context.Context, id string) error {
	return s.UpdateDeploymentStatus(ctx, id, DeploySuperseded, "")
}

func (s *PgStore) MarkDeploymentLive(ctx context.Context, id string) error {
	return s.UpdateDeploymentStatus(ctx, id, DeployLive, "")
}

func (s *PgStore) SetDeploymentRootfs(ctx context.Context, id, path, key string, bytes int64) error {
	// Issue #96 / ADR-025 axis 2 (PR #116): rootfs_key is the canonical
	// StorageBackend key (e.g. "apps/<slug>/<depID>.ext4") schedd carries
	// on the wake wire. Local backends map the key to the same file as
	// rootfs_path; remote backends (OCI registry) resolve over HTTP. Both
	// columns are stamped on the same UPDATE so a fresh imaged build
	// always leaves the row with both fields non-empty. The legacy
	// rootfs_path is preserved for back-compat paths (apic dump, audit
	// logs, the `appsRoot` filesystem cleanup pass).
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set rootfs_path = $2, rootfs_key = $3, rootfs_bytes = $4
		  where id = $1`,
		id, nullString(path), nullString(key), bytes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDeploymentFailed is the failure-specific helper ADR-021 introduced
// alongside the deployments.error_code column. Status is pinned to
// 'failed' (no caller choice — use UpdateDeploymentStatus for other
// transitions). code is the RFC 7807 code pkg/api.SentinelToCode lifted
// from the wrapping error; "" when the failure did not map to a
// sentinel. message is the free-text string for debugging / the
// existing error column. Returns the refreshed row.
//
// Idempotent on (status='failed') rows: a redeploy after a fix will
// overwrite both columns.
func (s *PgStore) SetDeploymentFailed(ctx context.Context, id, code, message string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`update deployments
		    set status = 'failed', error = $2, error_code = $3
		  where id = $1
		  returning id, app_id, coalesce(build_id::text,''), image_digest, kind,
		            coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		            coalesce(rootfs_path,''), coalesce(rootfs_key,''), coalesce(rootfs_bytes,0),
		            status, coalesce(error,''), coalesce(error_code,''), created_at`,
		id, nullString(message), nullString(code))
	return scanDeploymentWithRootfs(row)
}

// --- builds ------------------------------------------------------------------

func (s *PgStore) CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`insert into builds (deployment_id, kind, source_bytes, status, log_path)
		 values ($1, $2, $3, 'queued', $4)
		 returning id, deployment_id, kind, source_bytes, status,
		           coalesce(failure_class,''), coalesce(log_path,''), started_at, finished_at, enqueued_at`,
		deploymentID, string(kind), sourceBytes, nullString(logPath))
	return scanBuild(row)
}

func (s *PgStore) BuildByID(ctx context.Context, id string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at, enqueued_at from builds where id = $1`, id)
	return scanBuild(row)
}

func (s *PgStore) BuildByDeployment(ctx context.Context, deploymentID string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at, enqueued_at from builds where deployment_id = $1
		 order by started_at desc nulls last limit 1`, deploymentID)
	return scanBuild(row)
}

func (s *PgStore) UpdateBuildStatus(ctx context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error {
	tag, err := s.pool.Exec(ctx,
		`update builds set
		   status        = $2,
		   failure_class = case when $3 = '' then failure_class else $3 end,
		   started_at    = case when $4 then now() else started_at end,
		   finished_at   = case when $5 then now() else finished_at end
		 where id = $1`,
		id, string(status), string(fc), started, finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimQueuedBuild atomically transitions queued → running via a single
// UPDATE … RETURNING and sets started_at = now(). Returns ErrNotFound
// when the row is missing OR already in another status — that second
// case is what lets builderd drop duplicate build_queued notifications
// (apid write path + imaged reaper) without spawning two builder VMs.
// Equivalent to a compare-and-swap at the row level.
func (s *PgStore) ClaimQueuedBuild(ctx context.Context, id string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`update builds
		   set status = 'running', started_at = now()
		 where id = $1 and status = 'queued'
		 returning id, deployment_id, kind, source_bytes, status,
		           coalesce(failure_class,''), coalesce(log_path,''),
		           started_at, finished_at, enqueued_at`,
		id)
	b, err := scanBuild(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Build{}, ErrNotFound
		}
		return Build{}, fmt.Errorf("state: claim queued build %s: %w", id, err)
	}
	return b, nil
}

// ListStaleQueuedBuilds is the imaged reaper's read surface (PR-A).
// Returns every row still in BuildQueued whose enqueued_at is older
// than `now() - threshold`. Uses the index on (status, enqueued_at)
// implicitly via the WHERE predicate; the builds table is bounded by
// in-flight builds (spec §9 keeps the queue shallow) so a full scan
// stays cheap. If the queue depth grows we should add a partial index
// on (status, enqueued_at) WHERE status='queued' — pinned as a
// follow-up.
//
// Builds is on the critical wake path: when apid emits
// db.NotifyBuildQueued right after CreateBuild (deploy_inputs.go:167),
// a transient Postgres blip between INSERT and NOTIFY can drop the
// notification. The reaper scans this set on a tick and re-emits
// the notify, recovering without a manual operator action.
func (s *PgStore) ListStaleQueuedBuilds(ctx context.Context, threshold time.Duration) ([]Build, error) {
	rows, err := s.pool.Query(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at, enqueued_at
		   from builds
		  where status = 'queued'
		    and enqueued_at < now() - $1::interval
		  order by enqueued_at asc`,
		threshold)
	if err != nil {
		return nil, fmt.Errorf("state: list stale queued builds: %w", err)
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list stale queued builds iterate: %w", err)
	}
	return out, nil
}

// --- custom domains ---------------------------------------------------------

func (s *PgStore) CreateCustomDomain(ctx context.Context, domain, appID, token string) (CustomDomain, error) {
	row := s.pool.QueryRow(ctx,
		`insert into custom_domains (domain, app_id, challenge_token) values ($1, $2, $3)
		 returning domain, app_id, challenge_token, coalesce(verified_at, 'epoch'::timestamptz)`,
		domain, appID, token)
	d := CustomDomain{}
	if err := row.Scan(&d.Domain, &d.AppID, &d.ChallengeToken, &d.VerifiedAt); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	return d, nil
}

func (s *PgStore) DomainByName(ctx context.Context, domain string) (CustomDomain, error) {
	row := s.pool.QueryRow(ctx,
		`select domain, app_id, challenge_token, coalesce(verified_at, 'epoch'::timestamptz)
		   from custom_domains where domain = $1`, domain)
	d := CustomDomain{}
	if err := row.Scan(&d.Domain, &d.AppID, &d.ChallengeToken, &d.VerifiedAt); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	return d, nil
}

func (s *PgStore) ListDomainsForApp(ctx context.Context, appID string) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx,
		`select domain, app_id, challenge_token, coalesce(verified_at, 'epoch'::timestamptz)
		   from custom_domains where app_id = $1 order by domain`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomains(rows)
}

func (s *PgStore) ListDomainsForAccount(ctx context.Context, accountID string) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx,
		`select d.domain, d.app_id, d.challenge_token, coalesce(d.verified_at, 'epoch'::timestamptz)
		 from custom_domains d join apps a on a.id = d.app_id
		 where a.account_id = $1 order by d.domain`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomains(rows)
}

func (s *PgStore) MarkDomainVerified(ctx context.Context, domain string) error {
	tag, err := s.pool.Exec(ctx, `update custom_domains set verified_at = now() where domain = $1`, domain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) DeleteCustomDomain(ctx context.Context, domain string) error {
	tag, err := s.pool.Exec(ctx, `delete from custom_domains where domain = $1`, domain)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- crons -------------------------------------------------------------------

func (s *PgStore) CreateCron(ctx context.Context, appID, schedule, path string, enabled bool) (Cron, error) {
	row := s.pool.QueryRow(ctx,
		`insert into crons (app_id, schedule, path, enabled) values ($1, $2, $3, $4)
		 returning id, app_id, schedule, path, enabled`,
		appID, schedule, path, enabled)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled); err != nil {
		return Cron{}, mapErr(err)
	}
	return c, nil
}

func (s *PgStore) CronByID(ctx context.Context, id string) (Cron, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, schedule, path, enabled from crons where id = $1`, id)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled); err != nil {
		return Cron{}, mapErr(err)
	}
	return c, nil
}

func (s *PgStore) UpdateCron(ctx context.Context, id string, schedule, path *string, enabled *bool, createdAt *time.Time) (Cron, error) {
	var createdAtArg any
	if createdAt != nil {
		createdAtArg = createdAt.UTC()
	}
	row := s.pool.QueryRow(ctx,
		`update crons set
		   schedule   = coalesce($2, schedule),
		   path       = coalesce($3, path),
		   enabled    = coalesce($4, enabled),
		   created_at = coalesce($5, created_at)
		 where id = $1
		 returning id, app_id, schedule, path, enabled, created_at`,
		id, schedule, path, enabled, createdAtArg)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
		return Cron{}, mapErr(err)
	}
	return c, nil
}

func (s *PgStore) DeleteCron(ctx context.Context, id, appID string) error {
	tag, err := s.pool.Exec(ctx, `delete from crons where id = $1 and app_id = $2`, id, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkCronFired stamps the last_fired_at column. Schema migration
// 00003_cron_last_fired.sql added the column.
func (s *PgStore) MarkCronFired(ctx context.Context, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update crons set last_fired_at = $2 where id = $1`, id, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListCronsForApp(ctx context.Context, appID string) ([]Cron, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, schedule, path, enabled, created_at from crons where app_id = $1 order by created_at`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCrons(rows)
}

func (s *PgStore) ListEnabledCrons(ctx context.Context) ([]Cron, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, schedule, path, enabled, created_at from crons where enabled = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCrons(rows)
}

// --- instances --------------------------------------------------------------

func (s *PgStore) CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (Instance, error) {
	// started_at is stamped explicitly here in addition to the
	// BEFORE INSERT trigger from migration 00015. The trigger is the
	// belt; this is the braces. Either alone works; both together
	// make the contract obvious to anyone reading PgStore and prevent
	// a future trigger drop from silently regressing the watchdog
	// (commit 3, spec §6.1).
	//
	// nodeID is the compute_node the instance lives on
	// (issue #97 / ADR-025 axis 3). The NOT NULL constraint added
	// by migrations/00024_compute_nodes enforces non-null at the
	// schema layer; passing an empty string here would surface as a
	// Postgres error from the INSERT. schedd's Wake flow resolves
	// the id via sched.ChoosePlacement before reaching this point;
	// tests that don't exercise routing pass DefaultLocalNodeName's
	// resolved UUID (or the name itself if the table isn't seeded).
	//
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). Passing an empty string lets the column default
	// gen_random_uuid() fire — safe for ad-hoc INSERTs in backfill
	// scripts. schedd mints a UUIDv7 Go-side before reaching here so
	// production traffic always lands the explicit value. The RETURNING
	// clause is widened to surface wake_id for the engine and dashboard.
	// COALESCE on the SELECT guards against any pre-migration-00027
	// path that left the column NULL — though migration 00027 enforces
	// NOT NULL post-apply, the COALESCE keeps scanInstance round-tripping
	// a non-empty string even on half-migrated test DBs.
	row := s.pool.QueryRow(ctx,
		`insert into instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at)
		 values ($1, $2, $3, $4, $5, coalesce(nullif($6, ''), gen_random_uuid()), now())
		 returning id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id`,
		appID, deploymentID, state, ramMB, nodeID, wakeID)
	return scanInstance(row)
}

func (s *PgStore) InstanceByID(ctx context.Context, id string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id
		 from instances where id = $1`, id)
	return scanInstance(row)
}

func (s *PgStore) ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id
		 from instances where app_id = $1 order by started_at desc`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListLatestInstancesForApp returns up to `limit` instance rows for
// appID, ordered by started_at DESC. Used by the dashboard's app-detail
// "Recent wakes" table (gaps analysis 2026-07-23). The LIMIT pushdown
// bounds the per-render scan at the SQL layer so a long-lived app with
// hundreds of parked history rows doesn't pull its full history on
// every dashboard render. limit ≤ 0 returns an empty slice — the
// caller is required to pass a positive bound; a zero-bound here
// would silently mean "all", which is the unbounded-scan footgun we
// just escaped. See Store interface doc for the supporting-index note.
func (s *PgStore) ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id
		 from instances where app_id = $1 order by started_at desc limit $2`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListAllInstances returns every instance in a reaper-relevant state. Used
// by schedd's G7 conntrack warm (pkg/sched/flowcount): one bulk read feeds
// the per-tick warm list, avoiding a per-app loop. The state filter matches
// the set the reaper actually considers — parked/stopped/failed instances
// have no veth and no flows, so excluding them keeps the conntrack parse
// O(live instances) instead of O(all instances ever).
func (s *PgStore) ListAllInstances(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id
		 from instances
		 where state in ('running','waking','cold_booting','snapshotting')
		 order by started_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListInstancesForAccount joins instances→apps in SQL so the meterd
// quota loop can park every live instance for an account in one round
// trip. Filtered to instances.state ∈ {WAKING, COLD_BOOTING, RUNNING,
// SNAPSHOTTING} would be tempting, but the meterd caller's CountsForRAM
// guard is the canonical filter — keeping the SQL narrow and the state
// semantics in Go makes the test surface match both stores.
func (s *PgStore) ListInstancesForAccount(ctx context.Context, accountID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select i.id, i.app_id, i.deployment_id, i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id
		 from instances i
		 join apps a on a.id = i.app_id
		 where a.account_id = $1
		 order by i.started_at desc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListLatestInstancePerApp returns the most-recently-started instance
// for each app owned by the account, keyed by app ID. Used by the
// dashboard cold-wake badge (PR #48 follow-up); collapses N per-app
// ListInstancesForApp calls into a single round-trip.
//
// DISTINCT ON keeps one row per app_id with the largest started_at;
// NULLS LAST matches the column semantics — fresh deployments have
// nil started_at until vmmd stamps SetInstanceRuntime on first wake.
// Apps with no instance rows simply don't appear in the result map;
// callers must handle that case (no badge → ◌ sleeping via
// BadgeForDefault).
//
// No dedicated index yet: at one-box scale (≤ Pro 25 apps/account) the
// join on apps.account_id + seq-scan over instances is sub-millisecond.
// Add `instances(account_id, app_id, started_at DESC)` if the box grows.
func (s *PgStore) ListLatestInstancePerApp(ctx context.Context, accountID string) (map[string]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select distinct on (i.app_id)
		        i.id, i.app_id, i.deployment_id, i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id
		 from instances i
		 join apps a on a.id = i.app_id
		 where a.account_id = $1
		 order by i.app_id, i.started_at desc nulls last`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Instance{}
	for rows.Next() {
		ins, err := scanInstanceCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[ins.AppID] = ins
	}
	return out, rows.Err()
}

func (s *PgStore) UpdateInstanceState(ctx context.Context, id, state string) error {
	tag, err := s.pool.Exec(ctx, `update instances set state = $2 where id = $1`, id, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateInstanceStateWithTimestamp stamps parked_at on the same
// statement that writes the new state. schedd's snapshotAndPark calls
// this when transitioning into SNAPSHOTTING — the §6.1 watchdog reads
// parked_at on SNAPSHOTTING rows to compute "age of state", distinct
// from started_at which is now stamped on creation (migration 00015).
func (s *PgStore) UpdateInstanceStateWithTimestamp(ctx context.Context, id, state string, parkedAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set state = $2, parked_at = $3 where id = $1`,
		id, state, parkedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateInstanceStateToTerminal writes state AND stamps terminal_at on
// the same UPDATE (PR #74). Engine.transition routes here for
// {STOPPED, FAILED}; terminal_at is the dedicated retention anchor the
// §17 sweep reads (started_at means "row creation"; parked_at is
// overloaded). One statement, atomic — same RowAffected/ErrNotFound
// shape as UpdateInstanceState.
func (s *PgStore) UpdateInstanceStateToTerminal(ctx context.Context, id, state string, terminalAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set state = $2, terminal_at = $3 where id = $1`,
		id, state, terminalAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListInstancesByStatesOlderThan is the watchdog's lookup (spec §6.1).
// Filters on state ∈ states and a state-aware "age" column:
// started_at for WAKING / COLD_BOOTING (stamped on creation by the
// trigger in migration 00015), parked_at for SNAPSHOTTING (stamped on
// entry into SNAPSHOTTING by UpdateInstanceStateWithTimestamp).
//
// The CASE shape is load-bearing — the original coalesce(started_at,
// parked_at) predicate silently used parked_at for any row with NULL
// started_at, which is true for every row that existed before
// migration 00015 shipped. Such a row would compare against its
// historical parked_at (often weeks old) and look stuck even though
// it's normal. The partial index
// instances_watchdog_state_idx (migration 00016) covers the state
// predicate; the CASE comparison runs on the row payload.
func (s *PgStore) ListInstancesByStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error) {
	stateStrs := make([]string, len(states))
	for i, s := range states {
		stateStrs[i] = string(s)
	}
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id
		 from instances
		 where state = any($1)
		   and case when state = 'snapshotting' then parked_at else started_at end < $2`,
		stateStrs, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListInstancesInTerminalStatesOlderThan is the §17 retention sweep's
// lookup (PR #74). Reads the dedicated terminal_at column — distinct
// from the watchdog's state-aware started_at/parked_at comparison.
// Today only {STOPPED, FAILED} are terminal; we still parameterize
// states to keep the door open if a future state earns the same
// treatment. Migration 00017's partial index
// `instances_terminal_at_idx` covers this query.
func (s *PgStore) ListInstancesInTerminalStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error) {
	stateStrs := make([]string, len(states))
	for i, s := range states {
		stateStrs[i] = string(s)
	}
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, terminal_at
		 from instances
		 where state = any($1)
		   and terminal_at is not null
		   and terminal_at < $2`,
		stateStrs, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstancesWithTerminal(rows)
}

// DeleteInstance removes one instance row unconditionally (PR #74).
// Returns ErrNotFound when the row is gone (the sweep swallows that
// case for redelivery). No FK cascade — events.subject and
// usage_minutes.instance_id carry no FK today.
func (s *PgStore) DeleteInstance(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from instances where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) SetInstanceRuntime(ctx context.Context, id, netns, hostIP string, guestUID int) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set netns = $2, host_ip = $3::inet, guest_uid = $4, started_at = now()
		 where id = $1`, id, netns, hostIP, guestUID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) RunningInstanceForApp(ctx context.Context, appID string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id
		 from instances where app_id = $1 and state = 'running'
		 order by started_at desc nulls last limit 1`, appID)
	return scanInstance(row)
}

// TouchInstancesLastSeen applies a last_request_at batch in one round-trip via
// unnest, updating only rows that still exist (a reaped instance's touch is
// silently dropped). Returns the number of rows updated.
func (s *PgStore) TouchInstancesLastSeen(ctx context.Context, touches []InstanceTouch) (int, error) {
	if len(touches) == 0 {
		return 0, nil
	}
	ids := make([]string, len(touches))
	ts := make([]time.Time, len(touches))
	for i, t := range touches {
		ids[i] = t.InstanceID
		ts[i] = t.LastRequest
	}
	tag, err := s.pool.Exec(ctx,
		`update instances i set last_request_at = b.ts
		 from (select unnest($1::uuid[]) as id, unnest($2::timestamptz[]) as ts) b
		 where i.id = b.id`, ids, ts)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// --- snapshots --------------------------------------------------------------

// CreateSnapshot writes the immutable snapshot row imaged produces after the
// rootfs layer is built. Conflicts (same deployment_id) collapse to ErrConflict
// so imaged can ignore a duplicate emission; the rest of imaged treats the
// first successful write as truth.
func (s *PgStore) CreateSnapshot(ctx context.Context, snap Snapshot) (Snapshot, error) {
	// StorageKey is required. The migration's `NOT NULL DEFAULT ''`
	// is a safety net for any path we miss, but the contract here is
	// that the caller populates it explicitly (production: imaged
	// copies it from the snapshot_written payload; tests: call
	// sched.SnapshotMemKey(deploymentID) at the fixture's
	// CreateSnapshot site — see pkg/sched/paths.go). An empty value
	// used to silently default to the legacy-path form, which masked
	// bugs in callers that forgot the field — that loophole is now
	// closed. pkg/state can't import pkg/sched (cycle: sched →
	// state), so the helper lives in sched and callers wire it.
	if snap.StorageKey == "" {
		return Snapshot{}, fmt.Errorf("state: CreateSnapshot: storage_key required (populate via state.SnapMemKey at the call site)")
	}
	row := s.pool.QueryRow(ctx,
		`insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, stale)
		 values ($1, $2, $3, $4, $5, $6)
		 returning id, deployment_id::text, fc_version, mem_bytes, disk_bytes, storage_key, stale, created_at`,
		snap.DeploymentID, snap.FCVersion, snap.MemBytes, snap.DiskBytes, snap.StorageKey, snap.Stale)
	out, err := scanSnapshot(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return Snapshot{}, ErrConflict
		}
		return Snapshot{}, err
	}
	return out, nil
}

// LatestSnapshot returns the freshest non-stale snapshot for a deployment.
// schedd's wake path calls this to decide between restore and cold boot
// (ADR-005 — cold boot must always work, snapshot is cache).
func (s *PgStore) LatestSnapshot(ctx context.Context, deploymentID string) (Snapshot, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id::text, fc_version, mem_bytes, disk_bytes, storage_key, stale, created_at
		 from snapshots where deployment_id = $1 and stale = false
		 order by created_at desc limit 1`, deploymentID)
	return scanSnapshot(row)
}

// MarkSnapshotStale flags a snapshot unusable after a failed restore (ADR-005):
// the next wake cold-boots and the next park re-snapshots. Idempotent.
func (s *PgStore) MarkSnapshotStale(ctx context.Context, snapshotID string) error {
	tag, err := s.pool.Exec(ctx, `update snapshots set stale = true where id = $1`, snapshotID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSnapshotsForGC returns every non-stale snapshot joined with its
// deployment + app + account, ordered newest-first. The SQL filter on
// apps.status='deleted' is what implements "soft-deleted apps' snapshots
// are GC-eligible" — the row delete cascade in DeleteAccount only touches
// rows, not on-disk files, so imaged still has to scrub them.
//
// The JOIN is bounded by snapshotDashboardCap (10k) for the same reason
// ListLiveSnapshotStats is: the GC algorithm is O(N) per tick and a 10k
// fleet is plenty for the v1 box (the 452 GB budget fires well before that).
// Raise this when we go multi-box.
func (s *PgStore) ListSnapshotsForGC(ctx context.Context) ([]SnapshotForGC, error) {
	rows, err := s.pool.Query(ctx,
		`select s.id, s.deployment_id::text, d.app_id::text, a.account_id::text,
		        s.fc_version, s.mem_bytes, s.disk_bytes, s.storage_key, s.stale, s.created_at
		   from snapshots s
		   join deployments d on d.id = s.deployment_id
		   join apps a       on a.id = d.app_id
		  where s.stale = false
		    and a.status <> 'deleted'
		  order by s.created_at desc
		  limit 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotForGC
	for rows.Next() {
		var r SnapshotForGC
		if err := rows.Scan(&r.ID, &r.DeploymentID, &r.AppID, &r.AccountID,
			&r.FCVersion, &r.MemBytes, &r.DiskBytes, &r.StorageKey, &r.Stale, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSnapshotsByID bulk-removes the named rows. No cascade; schedd's
// runtime accounting (instances table) doesn't reference snapshots, so a
// snapshot can be deleted without affecting live wakes — ADR-005 says
// "cold boot must always work" precisely so this can be done in any
// state. Idempotent: a second call returns 0 and nil.
func (s *PgStore) DeleteSnapshotsByID(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `delete from snapshots where id = any($1::uuid[])`, ids)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkAllSnapshotsStaleByFCVersion flips every non-stale row whose
// fc_version != currentVersion stale (ADR-005). Idempotent. Returns
// the number of rows affected; a 0-row result on a stable box is the
// expected steady state.
func (s *PgStore) MarkAllSnapshotsStaleByFCVersion(ctx context.Context, currentVersion string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`update snapshots set stale = true where stale = false and fc_version <> $1`,
		currentVersion)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkOldSnapshotsStale marks the given snapshot IDs stale. Used by the
// imaged GC's per-app "current + previous" enforcement: the per-app walk
// identifies the IDs to drop, marks them stale first (so a concurrent
// wake's "is this usable?" check refuses them safely), and then calls
// DeleteSnapshotsByID. Marking stale first instead of deleting directly
// lets schedd's per-row freshness check remain the source of truth in
// the brief window between mark and delete.
func (s *PgStore) MarkOldSnapshotsStale(ctx context.Context, beforeSnapshotIDs []string) (int64, error) {
	if len(beforeSnapshotIDs) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`update snapshots set stale = true where id = any($1::uuid[])`, beforeSnapshotIDs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteSnapshotsStaleOlderThan removes stale snapshots past the
// retention window. Used by imaged's F2 startup sweep after the
// mark-stale step — keeps stale rows restorable for a grace period
// (typically 7 days per api.SnapshotStaleRetention) so an operator
// rollback across an FC upgrade doesn't pay an extra cold boot.
// F-07 closes the gap where the prior sweep only flipped stale=true
// and stale rows accumulated indefinitely.
func (s *PgStore) DeleteSnapshotsStaleOlderThan(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`delete from snapshots where stale = true and created_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int64(retention.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListLiveSnapshotStats returns mem_bytes + disk_bytes for every non-stale
// snapshot. Feeds the §12 dashboard gauge `fcvm_snapshot_fleet_avg_bytes`
// (and the p95 sibling). One round-trip; the dashboard wrapper caches
// the result for 5 s so this isn't on the hot scrape path. The "live"
// filter matches the dashboard's notion of "parked apps taking up
// disk": stale snapshots are GC'd by imaged nightly (spec §4.6) and
// should not contribute to the fleet average.
//
// Bounded by snapshotDashboardCap (10k) — the dashboard only renders a
// fleet average + p95, so the precision loss from truncating past 10k
// snapshots is invisible. The cap prevents M10-scale fleet growth from
// degrading the dashboard scrape path (PG reads O(N) snapshots every
// 5 s otherwise). Raise this when the dashboard gains per-app panels.
func (s *PgStore) ListLiveSnapshotStats(ctx context.Context) ([]SnapshotSize, error) {
	rows, err := s.pool.Query(ctx,
		`select mem_bytes, disk_bytes from snapshots where stale = false order by mem_bytes desc limit 10000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotSize
	for rows.Next() {
		var sz SnapshotSize
		if err := rows.Scan(&sz.MemBytes, &sz.DiskBytes); err != nil {
			return nil, err
		}
		out = append(out, sz)
	}
	return out, rows.Err()
}

// SnapshotSize is the per-row projection used by the dashboard gauge.
// VMStateBytes is folded into MemBytes today (the `snapshots` table
// stores a single bytes value for the parked footprint); a future
// migration splitting the columns can add the field without breaking
// callers. Keeping it here (not in pkg/fcvm) so the SQL → struct
// mapping stays in the package that owns the schema.
type SnapshotSize struct {
	MemBytes  int64
	DiskBytes int64
}

// --- compute nodes (issue #97 / ADR-025 axis 3) -----------------------------
//
// schedd is the sole reader (single-leader CP); apid is the sole writer
// (POST /v1/compute-nodes admin endpoint). The synthetic 'default-local'
// row is seeded by migrations/00024_compute_nodes.sql — production never
// inserts it. The per-vm overhead (8 MB) used by ComputeNodeUsedMB is
// referenced from pkg/api.PerVMOverheadMB — the single source of truth
// for the per-vm fixed cost (spec §4.7 / §6.2-2). Importing pkg/api here
// is safe: pkg/api has no outbound dependency on pkg/state, so no cycle.

func scanComputeNode(row pgx.Row) (ComputeNode, error) {
	n := ComputeNode{}
	if err := row.Scan(&n.ID, &n.Name, &n.TargetURL, &n.VPCPUs, &n.MemMB,
		&n.MaxConcurrency, &n.AdmissionCeilingMB, &n.Active,
		&n.LastHeartbeatAt, &n.CreatedAt); err != nil {
		return ComputeNode{}, mapErr(err)
	}
	return n, nil
}

func (s *PgStore) ActiveComputeNodes(ctx context.Context) ([]ComputeNode, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, active, last_heartbeat_at, created_at
		  from compute_nodes
		 where active = true
		 order by name
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list active compute_nodes: %w", err)
	}
	defer rows.Close()
	var out []ComputeNode
	for rows.Next() {
		n, err := scanComputeNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ListAllComputeNodes returns every compute_node row (active +
// inactive) ordered by name. apid's GET /v1/compute-nodes
// operator surface (PR #114) reads this so a recently-drained
// node is still visible. Sequential scan; the fleet is
// single-digit for v1.0, so the missing partial index is fine.
func (s *PgStore) ListAllComputeNodes(ctx context.Context) ([]ComputeNode, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, active, last_heartbeat_at, created_at
		  from compute_nodes
		 order by name
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list all compute_nodes: %w", err)
	}
	defer rows.Close()
	var out []ComputeNode
	for rows.Next() {
		n, err := scanComputeNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *PgStore) ComputeNodeByID(ctx context.Context, id string) (ComputeNode, error) {
	row := s.pool.QueryRow(ctx, `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, active, last_heartbeat_at, created_at
		  from compute_nodes
		 where id = $1
	`, id)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, err
	}
	return n, nil
}

func (s *PgStore) ComputeNodeByName(ctx context.Context, name string) (ComputeNode, error) {
	row := s.pool.QueryRow(ctx, `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, active, last_heartbeat_at, created_at
		  from compute_nodes
		 where name = $1
	`, name)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, err
	}
	return n, nil
}

// ComputeNodeUsedMB returns the Σ(ram_mb + api.PerVMOverheadMB) for live
// instances on the given node. Mirrors the §6.2-2 invariant re-stated
// per-node: Σ ≤ admission_ceiling_mb per active node. Live = state ∈
// ('waking','cold_booting','running'); SNAPSHOTTING is excluded because
// the watchdog considers a snapshotting instance parked-from-RAM (its
// resident memory is being flushed to disk, not held for requests).
// The 8 MB per-vm constant lives in pkg/api (pkg/api.PerVMOverheadMB)
// — the single source of truth shared with sched.Ledger's reservation
// math and the §4.7 billing model. Reading from pkg/api rather than a
// local duplicate keeps ledger + aggregate in lockstep (F-1 in the
// PR #112 review).
func (s *PgStore) ComputeNodeUsedMB(ctx context.Context, nodeID string) (int64, error) {
	var used int64
	err := s.pool.QueryRow(ctx, `
		select coalesce(sum(ram_mb + $2), 0)::bigint
		  from instances
		 where node_id = $1
		   and state in ('waking','cold_booting','running')
	`, nodeID, api.PerVMOverheadMB).Scan(&used)
	if err != nil {
		return 0, fmt.Errorf("state: compute_node %s used_mb: %w", nodeID, err)
	}
	return used, nil
}

func (s *PgStore) HeartbeatComputeNode(ctx context.Context, nodeID string) error {
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set last_heartbeat_at = now() where id = $1`, nodeID)
	if err != nil {
		return fmt.Errorf("state: heartbeat compute_node %s: %w", nodeID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkComputeNodeInactive flips active=false on the row (PR #114,
// schedd heartbeat path). Idempotent: the UPDATE matches regardless
// of current value, so re-flipping an inactive row is a no-op. We
// preserve the row rather than DELETE so an operator can re-enable
// it without re-provisioning the target_url / cert.
func (s *PgStore) MarkComputeNodeInactive(ctx context.Context, nodeID string) error {
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set active = false where id = $1`, nodeID)
	if err != nil {
		return fmt.Errorf("state: mark compute_node %s inactive: %w", nodeID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) CreateComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error) {
	// Caller supplies zero id for "use the column default
	// (gen_random_uuid)"; we surface whatever Postgres picked in the
	// RETURNING so the caller can persist it. A pre-set id is rare —
	// only useful for restoring a backup or testing.
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, active)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, active, last_heartbeat_at, created_at
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB, node.Active)
	return scanComputeNode(row)
}

// UpsertComputeNode inserts or updates a row by name (issue #98 /
// ADR-028). vmmd's self-registration calls this at startup; a node
// rebooting brings itself back without operator intervention. The ON
// CONFLICT branch re-applies operator-tunable capacity and re-activates
// a row that an operator had previously drained (active=false → true).
// last_heartbeat_at and created_at are not touched on conflict: the
// former is the watchdog's heartbeat stamp (next task); the latter is
// the row's creation time and stays monotonic.
func (s *PgStore) UpsertComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error) {
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, active)
		values ($1, $2, $3, $4, $5, $6, true)
		on conflict (name) do update
		  set target_url          = excluded.target_url,
		      vpcpus              = excluded.vpcpus,
		      mem_mb              = excluded.mem_mb,
		      max_concurrency     = excluded.max_concurrency,
		      admission_ceiling_mb = excluded.admission_ceiling_mb,
		      active              = true
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, active, last_heartbeat_at, created_at
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, fmt.Errorf("state: upsert compute_node %q: %w", node.Name, err)
	}
	return n, nil
}

// SetComputeNodeActive flips active on a row by id (issue #98 /
// ADR-028). The watchdog uses this to mark a row drained when
// last_heartbeat_at ages past 90s, and the heartbeat goroutine uses it
// again to reactivate a drained row on the next successful dial. The
// pg_notify trigger on compute_nodes (operator-visible via
// pkg/db/notify.NotifyComputeNodeChanged) fires on the UPDATE so
// gatewayd's per-node client cache can drop/add entries without
// restart.
func (s *PgStore) SetComputeNodeActive(ctx context.Context, id string, active bool) error {
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set active = $2 where id = $1`, id, active)
	if err != nil {
		return fmt.Errorf("state: set active compute_node %s = %v: %w", id, active, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListComputeNodes returns every compute_node in name order. The
// optional includeInactive flag controls whether drained rows are
// visible; apid's GET /v1/compute-nodes passes true so operators can
// audit drained rows. Backed by compute_nodes_active_idx (the partial
// index on active=true used by placement; this method is admin-only
// and so pays the full-table scan cost only on operator dashboards).
func (s *PgStore) ListComputeNodes(ctx context.Context, includeInactive bool) ([]ComputeNode, error) {
	q := `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, active, last_heartbeat_at, created_at
		  from compute_nodes
	`
	if !includeInactive {
		q += ` where active = true`
	}
	q += ` order by name`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("state: list compute_nodes (inactive=%t): %w", includeInactive, err)
	}
	defer rows.Close()
	var out []ComputeNode
	for rows.Next() {
		n, err := scanComputeNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteComputeNode hard-deletes a compute_nodes row by id (issue #98 /
// ADR-028). apid's DELETE /v1/compute-nodes/{name}?hard=1 is the only
// caller; soft-delete via SetComputeNodeActive(false) is the routine
// operator path. Returns ErrNotFound when the id is unknown so the
// caller can surface a 404.
//
// Note: callers should NOT delete the synthetic default-local row
// (state.DefaultLocalNodeName) — every legacy instance row from
// migration 00024's backfill references it via FK. The handler in
// cmd/apid/compute_nodes.go rejects the request before reaching this
// method; we leave the safety check at the seam so the state layer
// stays a thin SQL wrapper.
func (s *PgStore) DeleteComputeNode(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from compute_nodes where id = $1`, id)
	if err != nil {
		return fmt.Errorf("state: delete compute_node %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- events ------------------------------------------------------------------

func (s *PgStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	var subj *uuid.UUID
	if subject != nil {
		u, err := uuid.Parse(*subject)
		if err == nil {
			subj = &u
		}
	}
	_, err := s.pool.Exec(ctx,
		`insert into events (actor, kind, subject, data) values ($1, $2, $3, $4::jsonb)`,
		actor, kind, subj, data)
	return err
}

func (s *PgStore) ListEvents(ctx context.Context, subject string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	var subj *uuid.UUID
	if subject != "" {
		u, err := uuid.Parse(subject)
		if err == nil {
			subj = &u
		}
	}
	rows, err := s.pool.Query(ctx,
		`select id, at, actor, kind, subject, data from events where subject = $1 order by at desc limit $2`,
		subj, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var rawData []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Kind, &e.Subject, &rawData); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(rawData)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- usage -------------------------------------------------------------------

func (s *PgStore) AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error {
	// Idempotent on (instance_id, minute). Mirrors the sqlc source in
	// queries.sql::AppendUsage — make sqlc-check verifies these stay in
	// lockstep. The first write wins; a redelivered minute is a no-op so a
	// meterd restart / network blip / two meterd instances cannot inflate
	// billing. M7 hardening, PR feat/m7-beta-hardening.
	_, err := s.pool.Exec(ctx,
		`insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests)
		 values ($1, $2, $3, $4, $5, $6)
		 on conflict (instance_id, minute) do nothing`,
		accountID, appID, instanceID, minute, mbSeconds, requests)
	return err
}

func (s *PgStore) UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error) {
	monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Compare via date_trunc('month', ...) on the parameter side too.
	// The view's month column is timestamptz (date_trunc('month', timestamptz)
	// is timestamptz). A plain `month = $2::timestamptz` still depends on the
	// session timezone for the parameter's interpretation, which breaks on
	// non-UTC hosts (issue #52 PR #59 follow-up: pgx encodes time.Time as a
	// bare timestamp literal; in TZ=Europe/Istanbul that becomes
	// 2026-07-01 00:00:00+03, while the view value is 2026-07-01 00:00:00+03
	// but anchored to UTC internally — they compare unequal even with the
	// explicit cast). date_trunc normalizes both sides to the month's start
	// in UTC, sidestepping session-TZ semantics entirely.
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, month, mb_seconds, requests from usage_monthly
		 where account_id = $1 and date_trunc('month', $2::timestamptz) = month order by app_id`,
		accountID, monthStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		u := Usage{}
		if err := rows.Scan(&u.AccountID, &u.AppID, &u.Month, &u.MBSeconds, &u.Requests); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageByHour returns per-app usage rolled up from the per-minute rows
// in the [start, end) window. The Stripe pusher calls this hourly;
// (start, end) is the [now-1h, now) hour window so the SQL is an
// indexed range scan on usage_minutes.minute.
func (s *PgStore) UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]Usage, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id,
		        date_trunc('hour', minute) as hour,
		        sum(mb_seconds)::bigint as mb_seconds,
		        sum(requests)::bigint as requests
		 from usage_minutes
		 where account_id = $1 and minute >= $2 and minute < $3
		 group by account_id, app_id, hour
		 order by app_id`,
		accountID, start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		u := Usage{}
		var hour time.Time
		if err := rows.Scan(&u.AccountID, &u.AppID, &hour, &u.MBSeconds, &u.Requests); err != nil {
			return nil, err
		}
		u.Month = hour
		out = append(out, u)
	}
	return out, rows.Err()
}

// HasStripePushHour is the dedupe gate the meterd hourly pusher reads
// before issuing the Stripe call. Backed by a unique index on
// (account_id, hour) in the stripe_push_dedupe table (added in
// migration 00004).
func (s *PgStore) HasStripePushHour(ctx context.Context, accountID string, hour time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`select exists(select 1 from stripe_push_dedupe where account_id = $1 and hour = $2)`,
		accountID, hour.UTC()).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// RecordStripePushHour inserts the dedupe row. ON CONFLICT DO NOTHING so
// a redelivered push is idempotent.
func (s *PgStore) RecordStripePushHour(ctx context.Context, accountID string, hour time.Time) error {
	_, err := s.pool.Exec(ctx,
		`insert into stripe_push_dedupe (account_id, hour) values ($1, $2)
		 on conflict (account_id, hour) do nothing`,
		accountID, hour.UTC())
	return err
}

// --- idempotency -------------------------------------------------------------

func (s *PgStore) GetIdempotent(ctx context.Context, accountID, key string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.pool.QueryRow(ctx,
		`select response_status, response_body from idempotency_keys
		 where account_id = $1 and key = $2 and created_at > now() - interval '24 hours'`,
		accountID, key).Scan(&status, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrNotFound
		}
		return 0, nil, err
	}
	return status, body, nil
}

func (s *PgStore) PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into idempotency_keys (key, account_id, response_status, response_body)
		 values ($1, $2, $3, $4)
		 on conflict (account_id, key) do update set response_status = excluded.response_status, response_body = excluded.response_body`,
		key, accountID, status, body)
	return err
}

// --- secrets -----------------------------------------------------------------
//
// Customer secrets (spec §11/G2). Ciphertext only — apid seals server-side
// with the host X25519 recipient (pkg/secretbox), schedd reads ciphertext at
// wake time and hands it to vmmd which unseals. The plaintext VALUE never
// touches the Store layer.
//
// All four methods enforce (account_id, app_id) ownership: a secret is only
// readable/writable by the account that owns the app. apid looks up the
// app_id from the slug via AppBySlug before calling, so the ownership
// guarantee reduces to "the caller's acct.ID equals the row's account_id".
// We still pass accountID so the SQL is self-contained and the row's FK to
// accounts(id) is honored (no FK on app_id today; see migration 00005).

// UpsertAppSecret inserts or replaces the (app_id, key) ciphertext row.
// updated_at is bumped on conflict so schedd's "freshest per app" cache
// can re-stage drive1 even if the value didn't change (matters for
// rotation flows that re-seal with the same plaintext).
func (s *PgStore) UpsertAppSecret(ctx context.Context, accountID, appID, key string, ciphertext []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_secrets (account_id, app_id, key, ciphertext)
		 values ($1, $2, $3, $4)
		 on conflict (app_id, key) do update
		   set ciphertext = excluded.ciphertext,
		       updated_at = now()`,
		accountID, appID, key, ciphertext)
	return err
}

// DeleteAppSecret removes the (app_id, key) row scoped to accountID.
// Returns ErrNotFound when no row matches the (account_id, app_id, key)
// triple — the handler renders 400 CodeSecretNotFound (intentional: the
// URL resource IS the secret name, by design).
func (s *PgStore) DeleteAppSecret(ctx context.Context, accountID, appID, key string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_secrets where account_id = $1 and app_id = $2 and key = $3`,
		accountID, appID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAppSecrets returns every (key, ciphertext) row on the app, scoped
// to accountID. Order: by key ASC for deterministic wake staging (so a
// rotated order of upserts doesn't shuffle the env map on every wake).
// Returns nil slice (not error) when the app has no secrets — schedd
// treats that as "no env file to write".
func (s *PgStore) ListAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, key, ciphertext, created_at, updated_at
		 from app_secrets
		 where account_id = $1 and app_id = $2
		 order by key asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppSecret
	for rows.Next() {
		var s AppSecret
		if err := rows.Scan(&s.AccountID, &s.AppID, &s.Key, &s.Ciphertext, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountAppSecrets is the quota helper. Used by apid's PUT handler to
// enforce Limits.SecretCountMax BEFORE UpsertAppSecret so a quota-exceeded
// request never overwrites an existing (app_id, key) row.
func (s *PgStore) CountAppSecrets(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_secrets where account_id = $1 and app_id = $2`,
		accountID, appID).Scan(&n)
	return n, err
}

// --- row scanners ------------------------------------------------------------

func scanAccount(row pgx.Row) (Account, error) {
	a, err := scanAccountCols(row.Scan)
	if err != nil {
		return Account{}, mapErr(err)
	}
	return a, nil
}

func scanApp(row pgx.Row) (App, error) {
	a := App{}
	var typeStr, statusStr string
	var manifestBytes []byte
	if err := row.Scan(&a.ID, &a.AccountID, &a.Slug, &typeStr, &a.Runtime, &a.RAMMB, &a.IdleTimeoutS,
		&a.MaxConcurrency, &statusStr, &manifestBytes, &a.CreatedAt, &a.MinInstances); err != nil {
		return App{}, mapErr(err)
	}
	a.Type = AppType(typeStr)
	a.Status = AppStatus(statusStr)
	if len(manifestBytes) > 0 {
		_ = json.Unmarshal(manifestBytes, &a.Manifest)
	}
	return a, nil
}

func scanApps(rows pgx.Rows) ([]App, error) {
	var out []App
	for rows.Next() {
		a := App{}
		var typeStr, statusStr string
		var manifestBytes []byte
		if err := rows.Scan(&a.ID, &a.AccountID, &a.Slug, &typeStr, &a.Runtime, &a.RAMMB, &a.IdleTimeoutS,
			&a.MaxConcurrency, &statusStr, &manifestBytes, &a.CreatedAt, &a.MinInstances); err != nil {
			return nil, err
		}
		a.Type = AppType(typeStr)
		a.Status = AppStatus(statusStr)
		if len(manifestBytes) > 0 {
			_ = json.Unmarshal(manifestBytes, &a.Manifest)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanDeployment(row pgx.Row) (Deployment, error) {
	d := Deployment{}
	var kind, statusStr string
	if err := row.Scan(&d.ID, &d.AppID, &d.BuildID, &d.ImageDigest, &kind,
		&d.SourcePath, &d.SourceBytes, &d.Handler, &d.LogPath,
		&statusStr, &d.Error, &d.ErrorCode, &d.CreatedAt); err != nil {
		return Deployment{}, mapErr(err)
	}
	d.Kind = DeploymentKind(kind)
	d.Status = DeploymentStatus(statusStr)
	return d, nil
}

// scanDeploymentWithRootfs is the post-imaged variant that also reads the
// rootfs_path / rootfs_key / rootfs_bytes columns stamped by
// SetDeploymentRootfs. Every reads-everything query (used by schedd's prime
// handshake, M5, and by the engine's Wake flow at LiveDeployment) uses this
// so the snapshot_prime consumer sees the layer path AND schedd's wake wire
// can carry the layer key (issue #96 / ADR-025 axis 2 / PR #116). Ordering
// matches the SELECT projections in DeploymentByID, LiveDeployment, and
// SetDeploymentFailed.
func scanDeploymentWithRootfs(row pgx.Row) (Deployment, error) {
	d := Deployment{}
	var kind, statusStr, rootfsPath, rootfsKey string
	if err := row.Scan(&d.ID, &d.AppID, &d.BuildID, &d.ImageDigest, &kind,
		&d.SourcePath, &d.SourceBytes, &d.Handler, &d.LogPath,
		&rootfsPath, &rootfsKey, &d.RootfsBytes,
		&statusStr, &d.Error, &d.ErrorCode, &d.CreatedAt); err != nil {
		return Deployment{}, mapErr(err)
	}
	d.RootfsPath = rootfsPath
	d.RootfsKey = rootfsKey
	d.Kind = DeploymentKind(kind)
	d.Status = DeploymentStatus(statusStr)
	return d, nil
}

func scanDeployments(rows pgx.Rows) ([]Deployment, error) {
	var out []Deployment
	for rows.Next() {
		d := Deployment{}
		var kind, statusStr string
		if err := rows.Scan(&d.ID, &d.AppID, &d.BuildID, &d.ImageDigest, &kind,
			&d.SourcePath, &d.SourceBytes, &d.Handler, &d.LogPath,
			&statusStr, &d.Error, &d.ErrorCode, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Kind = DeploymentKind(kind)
		d.Status = DeploymentStatus(statusStr)
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanBuild(row pgx.Row) (Build, error) {
	b := Build{}
	var kind, statusStr, fc string
	var startedAt, finishedAt *time.Time
	if err := row.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &startedAt, &finishedAt, &b.EnqueuedAt); err != nil {
		return Build{}, mapErr(err)
	}
	if startedAt != nil {
		b.StartedAt = *startedAt
	}
	if finishedAt != nil {
		b.FinishedAt = *finishedAt
	}
	b.Kind = DeploymentKind(kind)
	b.Status = BuildStatus(statusStr)
	b.FailureClass = FailureClass(fc)
	return b, nil
}

func scanDomains(rows pgx.Rows) ([]CustomDomain, error) {
	var out []CustomDomain
	for rows.Next() {
		d := CustomDomain{}
		if err := rows.Scan(&d.Domain, &d.AppID, &d.ChallengeToken, &d.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanCrons(rows pgx.Rows) ([]Cron, error) {
	var out []Cron
	for rows.Next() {
		c := Cron{}
		if err := rows.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanInstance(row pgx.Row) (Instance, error) {
	ins, err := scanInstanceCols(row.Scan)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	return ins, nil
}

func scanInstances(rows pgx.Rows) ([]Instance, error) {
	var out []Instance
	for rows.Next() {
		ins, err := scanInstanceCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

// scanInstanceCols scans one instances row. started_at, last_request_at, and
// parked_at are nullable (a cold_booting instance has none yet), so they scan
// through *time.Time intermediates and stay the zero Time when NULL.
// node_id is the 12th column (issue #97 / ADR-025 axis 3) — NOT NULL since
// migrations/00024_compute_nodes but scanned into a string so a future
// regression that re-allows NULL surfaces as an empty string in Go rather
// than a scan error (the SELECT column list pins the contract; a divergence
// from there is a louder failure than a Scan error).
func scanInstanceCols(scan func(...any) error) (Instance, error) {
	ins := Instance{}
	var started, lastReq, parked *time.Time
	// wake_id is the 13th column (migration 00027). It's NOT NULL post-
	// 00027 but scanned into a string so any pre-migration-00027 row that
	// somehow surfaced surfaces as "" rather than a NULL scan error — the
	// SELECT column list is the contract that prevents column-order drift
	// from silently swallowing wake_id into an unrelated field.
	if err := scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
		&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked, &ins.NodeID, &ins.WakeID); err != nil {
		return Instance{}, err
	}
	if started != nil {
		ins.StartedAt = *started
	}
	if lastReq != nil {
		ins.LastRequestAt = *lastReq
	}
	if parked != nil {
		ins.ParkedAt = *parked
	}
	return ins, nil
}

// scanInstancesWithTerminal is the 13-column variant of scanInstanceCols
// that also lifts terminal_at (PR #74) and node_id (issue #97). Used only
// by ListInstancesInTerminalStatesOlderThan — the rest of the codebase
// reads 12-column instances rows (incl. node_id) and doesn't need
// terminal_at, so threading it into scanInstanceCols would force every
// SELECT to expose it for no reason. node_id is included here so the
// retention sweep's row carries the same node info as a live row — the
// GC delete later (DeleteInstance) doesn't need it, but a future
// per-node retention policy might, and surfacing it now keeps the row
// shape uniform across the read paths.
func scanInstancesWithTerminal(rows pgx.Rows) ([]Instance, error) {
	var out []Instance
	for rows.Next() {
		ins := Instance{}
		var started, lastReq, parked, terminal *time.Time
		// Column order matches ListInstancesInTerminalStatesOlderThan's
		// SELECT (now 14 columns after migration 00027 added wake_id
		// before terminal_at).
		if err := rows.Scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
			&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked, &ins.NodeID, &ins.WakeID, &terminal); err != nil {
			return nil, err
		}
		if started != nil {
			ins.StartedAt = *started
		}
		if lastReq != nil {
			ins.LastRequestAt = *lastReq
		}
		if parked != nil {
			ins.ParkedAt = *parked
		}
		if terminal != nil {
			ins.TerminalAt = terminal
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

func scanSnapshot(row pgx.Row) (Snapshot, error) {
	s := Snapshot{}
	if err := row.Scan(&s.ID, &s.DeploymentID, &s.FCVersion, &s.MemBytes, &s.DiskBytes, &s.StorageKey, &s.Stale, &s.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	return s, nil
}

// --- error mapping -----------------------------------------------------------

// ErrConflict is returned when a unique constraint is violated. MemStore
// returns plain errors; PgStore maps pgx's unique-violation SQLSTATE here so
// callers don't need to know about pgerrcode.
var ErrConflict = errors.New("state: conflict")

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
		return fmt.Errorf("%w: %s", ErrConflict, pgErr.ConstraintName)
	}
	return err
}

// --- small helpers ----------------------------------------------------------

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullAppStatus(p *AppStatus) any {
	if p == nil {
		return nil
	}
	return string(*p)
}

// ensure net import isn't dropped if other helpers move into this file.
var _ = net.IPv4len

// IssueLoginToken persists a magic-link token hash → account_id with
// the given expiry. The raw token is never stored — only its SHA-256
// hash. Conflict (same hash re-issued) is a no-op insert: the same
// token can't be re-issued because the raw token is single-use.
func (s *PgStore) IssueLoginToken(ctx context.Context, tokenHash []byte, accountID string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`insert into login_tokens (token_hash, account_id, expires_at) values ($1, $2, $3)
		 on conflict (token_hash) do nothing`,
		tokenHash, accountID, expiresAt)
	return err
}

// ConsumeLoginToken atomically marks the token consumed and returns
// the bound account_id. A replay (token already consumed) or expired
// token returns ErrNotFound — never a stale account. Single-statement
// compare-and-set keeps the consume race-free.
func (s *PgStore) ConsumeLoginToken(ctx context.Context, tokenHash []byte) (string, error) {
	var accountID string
	err := s.pool.QueryRow(ctx,
		`update login_tokens
		 set consumed_at = now()
		 where token_hash = $1
		   and consumed_at is null
		   and expires_at > now()
		 returning account_id`,
		tokenHash,
	).Scan(&accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return accountID, nil
}

// DeleteOldLoginTokens prunes tokens whose expires_at < before,
// including those that were consumed long ago. Returns the row count.
// Used by a maintenance job or a daily cleanup hook.
func (s *PgStore) DeleteOldLoginTokens(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from login_tokens where expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// IssueCliAuthCode persists a freshly-minted code's SHA-256 hash with
// no account binding (account_id NULL until the dashboard claims it).
// Conflict (same hash re-issued) is a no-op insert; the same code is
// effectively single-use because the dashboard /cli-auth POST must
// claim a still-pending row, and a re-issue collides on the hash.
func (s *PgStore) IssueCliAuthCode(ctx context.Context, tokenHash []byte, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`insert into cli_auth_codes (token_hash, expires_at) values ($1, $2)
		 on conflict (token_hash) do nothing`,
		tokenHash, expiresAt)
	return err
}

// PeekCliAuthCode returns the row's status without mutating it. Used
// by the dashboard GET /cli-auth render to decide whether the user
// sees the email-input form or the "code unavailable" error page.
// A missing or expired row returns (Expired, "", ErrNotFound) — the
// dashboard treats every not-pending state identically.
func (s *PgStore) PeekCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	var status string
	var accountID *string
	err := s.pool.QueryRow(ctx,
		`select status, account_id
		 from cli_auth_codes
		 where token_hash = $1 and expires_at > now()`,
		tokenHash).Scan(&status, &accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.CliAuthStatusExpired, "", ErrNotFound
		}
		return "", "", err
	}
	var aid string
	if accountID != nil {
		aid = *accountID
	}
	return api.CliAuthStatus(status), aid, nil
}

// ClaimCliAuthCode atomically transitions pending → consumed and binds
// account_id in one statement. Two error shapes distinguish the
// reasons a claim can fail (handler renders different banners):
//
//	ErrNotFound  — row missing OR expired (never minted or TTL passed)
//	ErrConflict  — row exists but status != 'pending' (already used)
//
// IMPORTANT: this MUST NOT touch consumed_at — that field is the
// exclusive mint-gate for ConsumeCliAuthCode. Pre-setting consumed_at
// here would short-circuit the CAS that the CLI's exchange relies on
// to mint exactly one API key per code (review finding F4).
//
// Implementation: a single UPDATE returns 0 rows on either failure;
// a follow-up SELECT classifies which one (no TOCTOU window because
// the UPDATE is still atomic — the post-classification SELECT only
// affects the error we report, not the state).
func (s *PgStore) ClaimCliAuthCode(ctx context.Context, tokenHash []byte, accountID string) error {
	tag, err := s.pool.Exec(ctx,
		`update cli_auth_codes
		 set status = 'consumed', account_id = $2
		 where token_hash = $1
		   and status = 'pending'
		   and expires_at > now()`,
		tokenHash, accountID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 0 {
		return nil
	}
	// Classify the zero-rows case. If the row doesn't exist at all
	// (never minted) or has expired, the user typed a stale code and
	// gets the "expired" banner. If the row exists and isn't expired
	// it must have been claimed already → ErrConflict.
	var exists, fresh bool
	err = s.pool.QueryRow(ctx,
		`select true, expires_at > now()
		 from cli_auth_codes where token_hash = $1`,
		tokenHash,
	).Scan(&exists, &fresh)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !fresh {
		return ErrNotFound
	}
	return ErrConflict
}

// ConsumeCliAuthCode is the CLI's poll-side read PLUS mint gate. It
// is a CAS in the same shape as ConsumeLoginToken: mutates
// `consumed_at` from NULL to NOW on the FIRST call only, returning
// the bound account_id; every subsequent call returns ErrNotFound.
// The handler mints the API key only when this returns success, so
// a buggy / replaying CLI cannot mint multiple keys for the same
// code (review finding F4).
//
// Filter: `account_id IS NOT NULL` is required — without a
// dashboard-side claim the row is still pending and the CLI should
// keep polling, NOT see the (Consumed, "", nil) shape that
// otherwise lets it mint a key for an unbound code (which would be
// a useless NULL FK insert into api_keys).
//
// Return contract (CLI key-mints only on Consumed + non-empty acct):
//
//	pending (or empty account_id) → (Pending,  "",       nil)        keep polling
//	consumed (first call)        → (Consumed, acct_id,  nil)        mint API key
//	consumed (replay) / expired / unknown → (Expired, "", ErrNotFound)
func (s *PgStore) ConsumeCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	var accountID string
	err := s.pool.QueryRow(ctx,
		`update cli_auth_codes
		 set consumed_at = now()
		 where token_hash = $1
		   and status = 'consumed'
		   and account_id is not null
		   and consumed_at is null
		   and expires_at > now()
		 returning account_id`,
		tokenHash,
	).Scan(&accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either pending, expired, already-consumed, or never
			// minted. Disambiguate pending vs not-found for the
			// polling CLI: if the row exists and is still pending
			// we tell it to keep waiting; otherwise we stop.
			var status string
			err2 := s.pool.QueryRow(ctx,
				`select status from cli_auth_codes
				 where token_hash = $1 and expires_at > now()`,
				tokenHash,
			).Scan(&status)
			if err2 == nil && status == string(api.CliAuthStatusPending) {
				return api.CliAuthStatusPending, "", nil
			}
			return api.CliAuthStatusExpired, "", ErrNotFound
		}
		return "", "", err
	}
	return api.CliAuthStatusConsumed, accountID, nil
}

// AppendDeploymentLog inserts one row and returns the seq Postgres
// assigned via the per-deployment bigserial PK.
//
// Used by builderd (slice 7/8/9) and the deployment status flips in
// imaged. The SSE tail (slice 5+6) pages by seq.
func (s *PgStore) AppendDeploymentLog(ctx context.Context, deploymentID, stream, line string) (int64, error) {
	var seq int64
	err := s.pool.QueryRow(ctx,
		`insert into deployment_logs (deployment_id, stream, line)
		 values ($1, $2, $3) returning seq`,
		deploymentID, stream, line).Scan(&seq)
	return seq, err
}

// ListDeploymentLogs returns the page of rows with seq < beforeSeq
// (zero → all rows), DESC, capped at limit. hasMore is true if there's
// at least one older row beyond the page.
//
// Review finding #7: the previous implementation set hasMore=true
// whenever the page was full (len(out) == limit), which is also true
// on the actual last page. We now fetch limit+1 rows in both query
// branches, trim back to limit, and set hasMore from the trimmed
// length — matching the MemStore contract (an exact full page
// returns hasMore=false iff the caller hit the literal end).
func (s *PgStore) ListDeploymentLogs(ctx context.Context, deploymentID string, beforeSeq int64, limit int) ([]LogEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = clampLogLimit(limit)
	// Fetch one extra row so we can tell whether the caller hit a
	// boundary exactly. The trim happens after the scan loop so the
	// scan doesn't need to know about the over-fetch.
	queryLimit := limit + 1
	var rows pgx.Rows
	var err error
	if beforeSeq <= 0 {
		rows, err = s.pool.Query(ctx,
			`select deployment_id, seq, stream, line, written_at
			 from deployment_logs where deployment_id = $1
			 order by seq desc limit $2`, deploymentID, queryLimit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select deployment_id, seq, stream, line, written_at
			 from deployment_logs where deployment_id = $1 and seq < $2
			 order by seq desc limit $3`, deploymentID, beforeSeq, queryLimit)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]LogEntry, 0, queryLimit)
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.DeploymentID, &e.Seq, &e.Stream, &e.Line, &e.WrittenAt); err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// --- G6 account self-service (spec §17 G6, ADR-021) -------------------------
//
// DELETE /v1/account schedules a 30-day grace window; pkg/grace in apid
// sweeps on a 60s timer and calls DeleteAccount once the window lapses.
// RestoreAccount flips the row back to active iff called inside the
// grace window — past that the only honest answer is ErrConflict and
// the handler returns 409 account_not_restorable.
//
// DeleteAccount is a single transaction that walks the FK graph in
// dependency order (app_secrets → custom_domains → crons → instances
// → snapshots → builds → deployments → apps → api_keys → idempotency
// keys → usage_minutes → accounts). Returns ErrNotFound when the
// final accounts row is already gone, so a redelivered grace tick is
// idempotent (and pkg/grace.RunOnce swallows the error).
//
// The DeletionGraceDuration helper is defined in memstore.go so both
// stores share the same canonical 30-day constant — apid, pkg/grace,
// and dashboard/email templates all read from the single declaration.

// DeleteAccount removes every row tied to the account inside a single
// transaction. Walks the FK graph in dependency order; the final
// `delete from accounts` is the sentinel — 0 rows affected means the
// account was already gone (idempotent retry by pkg/grace).
func (s *PgStore) DeleteAccount(ctx context.Context, id string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// Sentinel + race guard: the conditional DELETE on the parent row is
	// the single source of truth for "did this delete do anything?".
	//
	//   - RowsAffected == 0 → row didn't exist OR wasn't deleted_pending.
	//     Either way there's nothing to cascade. Returning ErrNotFound
	//     makes the call idempotent (a redelivered grace tick) AND
	//     closes the restore→tick race: if POST /v1/account/restore
	//     flipped status='active' in between ListAllAccounts and this
	//     tx, our DELETE matches 0 rows and we leave the row alone.
	//   - RowsAffected == 1 → row was in deleted_pending, our delete
	//     locks it for the rest of the tx, child cascades are safe.
	//
	// IMPORTANT: the sentinel runs LAST, after every child table has
	// been emptied. The original draft put the parent DELETE first; that
	// trips the FK constraint on `apps.account_id → accounts.id` and
	// aborts the whole transaction. Walking children first lets the
	// `delete from accounts` at the bottom be the natural sentinel.
	steps := []struct {
		name string
		sql  string
	}{
		{"app_secrets", `delete from app_secrets where account_id = $1`},
		{"custom_domains", `delete from custom_domains
		   where app_id in (select id from apps where account_id = $1)`},
		{"crons", `delete from crons
		   where app_id in (select id from apps where account_id = $1)`},
		{"instances", `delete from instances
		   where app_id in (select id from apps where account_id = $1)`},
		{"snapshots", `delete from snapshots
		   where deployment_id in
		     (select id from deployments where app_id in
		        (select id from apps where account_id = $1))`},
		{"builds", `delete from builds
		   where deployment_id in
		     (select id from deployments where app_id in
		        (select id from apps where account_id = $1))`},
		{"deployments", `delete from deployments
		   where app_id in (select id from apps where account_id = $1)`},
		{"apps", `delete from apps where account_id = $1`},
		{"api_keys", `delete from api_keys where account_id = $1`},
		{"idempotency_keys", `delete from idempotency_keys where account_id = $1`},
		{"usage_minutes", `delete from usage_minutes where account_id = $1`},
		// `events` is included (per spec §17 G6 right-to-erasure):
		// audit rows whose subject or payload references the account
		// must not outlive the customer's data. The data->>'account_id'
		// predicate is unindexed today; for the one-box this is fine
		// (small event count, scan cost stays in the microseconds) and
		// a follow-up ADR can add a GIN(events.data) when the volume
		// warrants it.
		{"events", `delete from events
		   where subject = $1::uuid
		      or (data ? 'account_id' and data->>'account_id' = $1::text)`},
	}
	for _, step := range steps {
		if _, err := tx.Exec(ctx, step.sql, id); err != nil {
			return fmt.Errorf("state: delete %s for account %s: %w", step.name, id, err)
		}
	}

	// Walk children first so the FK back to accounts is empty by the
	// time this fires. The conditional WHERE re-checks status in case
	// POST /v1/account/restore raced between the walk and here — if it
	// did, RowsAffected == 0 and we surface ErrNotFound, leaving every
	// child row in place.
	tag, err := tx.Exec(ctx,
		`delete from accounts where id = $1 and status = 'deleted_pending'`, id)
	if err != nil {
		return fmt.Errorf("state: delete accounts for %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit delete account %s: %w", id, err)
	}
	return nil
}

// ListBuildsForAccount returns every build across the account's
// deployments, ordered by created_at DESC. Used by the GDPR export
// bundle (spec §17 G6).
func (s *PgStore) ListBuildsForAccount(ctx context.Context, accountID string) ([]Build, error) {
	rows, err := s.pool.Query(ctx,
		`select b.id, b.deployment_id, b.kind, b.source_bytes, b.status,
		        coalesce(b.failure_class,''), coalesce(b.log_path,''),
		        b.started_at, b.finished_at
		 from builds b
		 join deployments d on d.id = b.deployment_id
		 join apps a on a.id = d.app_id
		 where a.account_id = $1
		 order by b.started_at desc nulls last`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		b := Build{}
		var kind, statusStr, fc string
		if err := rows.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &b.StartedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		b.Kind = DeploymentKind(kind)
		b.Status = BuildStatus(statusStr)
		b.FailureClass = FailureClass(fc)
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListCronsForAccount walks every cron tied to the account's apps.
// Used by the GDPR export bundle.
//
// NOTE: crons has no created_at column on origin/main (only
// enabled + schedule + path are tracked); the export bundle
// doesn't need a stable order, so we sort by id instead.
func (s *PgStore) ListCronsForAccount(ctx context.Context, accountID string) ([]Cron, error) {
	rows, err := s.pool.Query(ctx,
		`select c.id, c.app_id, c.schedule, c.path, c.enabled
		 from crons c
		 join apps a on a.id = c.app_id
		 where a.account_id = $1
		 order by c.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cron
	for rows.Next() {
		c := Cron{}
		// crons table has no created_at column (see NOTE above); scan
		// only the 5 selected columns. Cron.CreatedAt stays at the zero
		// value for rows read by this query — the export bundle omits
		// it because the GDPR surface doesn't need it.
		if err := rows.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UsageByAccount returns every per-app usage row for the account whose
// minute >= since. since.IsZero() → every row. Used by the GDPR
// export bundle (the spec calls for "all usage data" — the per-minute
// grain is the most honest representation).
func (s *PgStore) UsageByAccount(ctx context.Context, accountID string, since time.Time) ([]Usage, error) {
	var rows pgx.Rows
	var err error
	if since.IsZero() {
		rows, err = s.pool.Query(ctx,
			`select account_id, app_id, date_trunc('month', minute) as month,
			        sum(mb_seconds)::bigint, sum(requests)::bigint
			 from usage_minutes
			 where account_id = $1
			 group by account_id, app_id, month
			 order by app_id, month`, accountID)
	} else {
		rows, err = s.pool.Query(ctx,
			`select account_id, app_id, date_trunc('month', minute) as month,
			        sum(mb_seconds)::bigint, sum(requests)::bigint
			 from usage_minutes
			 where account_id = $1 and minute >= $2
			 group by account_id, app_id, month
			 order by app_id, month`, accountID, since.UTC())
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		u := Usage{}
		var month time.Time
		if err := rows.Scan(&u.AccountID, &u.AppID, &month, &u.MBSeconds, &u.Requests); err != nil {
			return nil, err
		}
		u.Month = month
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkAccountDeletionPending flips status to deleted_pending and
// stamps deletion_requested_at with now(). Idempotent: a repeat call
// leaves the timestamp untouched so the grace window's anchor stays
// at the original moment the customer asked (COALESCE keeps the
// original timestamp; the WHERE re-matches a row already in
// deleted_pending so the second call still affects 1 row).
//
// Defence-in-depth: the WHERE scopes to status in
// ('active', 'deleted_pending'). A row in past_due or any other
// suspended state must not be re-armed into deletion by a stale
// session cookie.
func (s *PgStore) MarkAccountDeletionPending(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts
		   set status = 'deleted_pending',
		       deletion_requested_at = coalesce(deletion_requested_at, now())
		 where id = $1 and status in ('active', 'deleted_pending')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RestoreAccount flips status back to active and clears
// deletion_requested_at iff the row is still inside the 30-day grace
// window. Past grace → ErrConflict so the handler renders 409.
func (s *PgStore) RestoreAccount(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts
		   set status = 'active',
		       deletion_requested_at = null
		 where id = $1
		   and status = 'deleted_pending'
		   and deletion_requested_at > now() - interval '30 days'`,
		id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// AppendGdprRequest records a customer-facing GDPR action against the
// account email captured at the moment of request. The ledger is
// INSERT-only from the application side; PgStore does not expose an
// UPDATE/DELETE path on this table. CompletedAt stays NULL until
// CompleteGdprRequest stamps it.
func (s *PgStore) AppendGdprRequest(ctx context.Context, r GdprRequest) error {
	if r.ID == "" {
		return fmt.Errorf("AppendGdprRequest: id is required")
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx,
		`insert into gdpr_requests
		   (id, account_id, account_email, action, requested_at, completed_at)
		 values ($1, $2, $3, $4, $5, $6)`,
		r.ID, r.AccountID, r.AccountEmail, string(r.Action),
		r.RequestedAt.UTC(), nullableTimestamptz(r.CompletedAt))
	return err
}

// ListGdprRequestsForAccount returns the ledger rows for an account
// in requested_at desc order. Bounded by limit; passing 0 means "no
// rows" (MemStore mirrors this so the call site never has to special-
// case the zero).
func (s *PgStore) ListGdprRequestsForAccount(ctx context.Context, accountID string, limit int) ([]GdprRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, account_id, account_email, action, requested_at, completed_at
		   from gdpr_requests
		  where account_id = $1
		  order by requested_at desc
		  limit $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GdprRequest
	for rows.Next() {
		var (
			g           GdprRequest
			completedAt pgtype.Timestamptz
		)
		if err := rows.Scan(&g.ID, &g.AccountID, &g.AccountEmail,
			&g.Action, &g.RequestedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			g.CompletedAt = completedAt.Time
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// nullableTimestamptz returns a pgx-friendly NULL when t.IsZero(), so
// AppendGdprRequest can keep completed_at NULL while the downstream
// action is in flight. Local helper: there's no shared equivalent in
// pkg/state yet (other INSERTs in this file use coalesce/default
// inside SQL, not nullable Go values).
func nullableTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// CompleteGdprRequest stamps completed_at on the most recent
// un-completed row of (account_id, action). Returns ErrNotFound when
// there is no matching row, so pkg/grace after a successful
// DeleteAccount can detect a stale tick and skip the log.
func (s *PgStore) CompleteGdprRequest(ctx context.Context, accountID, action string) error {
	// empty accountID can't bind to a uuid column; better to return
	// the contract-level ErrNotFound than the raw SQLSTATE 22P02 a
	// caller would otherwise see. Mirrors the MemStore branch, which
	// already does the empty-input short-circuit implicitly via the
	// loop's "no match" path.
	if accountID == "" || action == "" {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx,
		`update gdpr_requests
		   set completed_at = coalesce(completed_at, now())
		 where id = (
		   select id from gdpr_requests
		    where account_id = $1 and action = $2 and completed_at is null
		    order by requested_at desc
		    limit 1
		 )`, accountID, action)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LoadAndStampLastQuotaWarning is the atomic compare-and-set that lets
// pkg/meter.EnforceQuota emit exactly one paid-tier quota_warning per
// UTC day (spec §4.7). The query:
//   - Truncates `day` to its UTC midnight so the comparison is "same
//     calendar day, not same 24h window".
//   - Uses a CTE that captures the OLD stamp (pre-UPDATE) so the
//     scalar subquery can compare it to today's anchor — a naive
//     `returning last_quota_warning_at = $2` reads the post-update
//     column, which is trivially `$2` and yields `already=true` on
//     every call (CI caught this on PR #69).
//   - Returns one row even when the id is missing, with `already =
//     NULL` as the sentinel for "row doesn't exist" (a bare coalesce
//     can't distinguish "exists with NULL old stamp" from "missing
//     row", so we use a CASE explicitly). pkg/meter/EnforceQuota only
//     calls this against a freshly-read account id, so the missing
//     path is purely a safety net.
//   - Returns (true, nil) on a same-day repeat (UPDATE predicate
//     rejects the row, OLD stamp already equals $2), (false, nil) on
//     a first-today or next-day call (UPDATE happened), ErrNotFound
//     when no row matches the id at all.
func (s *PgStore) LoadAndStampLastQuotaWarning(ctx context.Context, id string, day time.Time) (bool, error) {
	dayStart := day.UTC().Truncate(24 * time.Hour)
	var already *bool
	err := s.pool.QueryRow(ctx,
		`with existing as (
		    select last_quota_warning_at as old
		      from accounts where id = $1
		 ),
		 upd as (
		    update accounts
		       set last_quota_warning_at = $2
		     where id = $1
		       and (last_quota_warning_at is null or last_quota_warning_at < $2)
		    returning 1
		 )
		 select case
		           when not exists(select 1 from existing) then null
		           when (select old from existing) = $2 then true
		           else false
		         end`,
		id, dayStart).Scan(&already)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if already == nil {
		return false, ErrNotFound
	}
	return *already, nil
}

// ClearQuotaWarning nulls last_quota_warning_at so the next call to
// LoadAndStampLastQuotaWarning (e.g. on the next quota tick) starts
// fresh. Used by apid's invoice.payment_succeeded webhook to make sure
// a paying customer doesn't get skipped tomorrow because of a stamp
// from the day they crossed the threshold.
func (s *PgStore) ClearQuotaWarning(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`update accounts set last_quota_warning_at = null
		  where id = $1 and last_quota_warning_at is not null`,
		id)
	return err
}

// MarkDunningStep is the meterd.Dunning timer's compare-and-advance
// primitive (spec §4.7, §17 dunning). Atomically:
//   - flips status from `from` to `to` (when from != to),
//   - stamps past_due_at only when transitioning *into* past_due
//     (coalesce preserves any pre-existing stamp so a back-and-forth
//     status flip doesn't lose the original anchor),
//   - returns ErrNotFound when no row matched (gone OR status didn't
//     match `from` — the latter is the redelivery race between two
//     concurrent dunning ticks).
//
// The from==to case is NOT short-circuited — it serves as the
// backfill-stamp path used by pkg/meter.Dunning to plant a stamp on
// a legacy row that entered past_due before the migration column
// existed (audit finding #2 data-integrity guard).
func (s *PgStore) MarkDunningStep(ctx context.Context, id string, from, to AccountStatus) error {
	var stamp *time.Time
	if to == AccountPastDue {
		now := time.Now().UTC()
		stamp = &now
	}
	tag, err := s.pool.Exec(ctx,
		`update accounts
		    set status = $2,
		        past_due_at = case when $2 = 'past_due' then coalesce(past_due_at, $3) else past_due_at end
		  where id = $1 and status = $4`,
		id, string(to), stamp, string(from))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
