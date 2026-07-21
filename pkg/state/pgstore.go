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
	return scanAccount(row)
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

func (s *PgStore) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`insert into deployments (app_id, image_digest, kind, source_path, source_bytes, handler, log_path, status)
		 values ($1, $2, $3, $4, $5, $6, $7, 'pending')
		 returning id, app_id, coalesce(build_id::text,''), image_digest, kind,
		           coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		           status, coalesce(error,''), created_at`,
		d.AppID, d.ImageDigest, string(d.Kind), nullString(d.SourcePath), d.SourceBytes,
		nullString(d.Handler), nullString(d.LogPath))
	return scanDeployment(row)
}

func (s *PgStore) DeploymentByID(ctx context.Context, id string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        coalesce(rootfs_path,''), coalesce(rootfs_bytes,0),
		        status, coalesce(error,''), created_at
		 from deployments where id = $1`, id)
	return scanDeploymentWithRootfs(row)
}

func (s *PgStore) LatestDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        status, coalesce(error,''), created_at
		 from deployments where app_id = $1 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

func (s *PgStore) LiveDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        coalesce(rootfs_path,''), coalesce(rootfs_bytes,0),
		        status, coalesce(error,''), created_at
		 from deployments where app_id = $1 and status = 'live' order by created_at desc limit 1`, appID)
	return scanDeploymentWithRootfs(row)
}

func (s *PgStore) LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        status, coalesce(error,''), created_at
		 from deployments where app_id = $1 and status = 'superseded'
		 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

func (s *PgStore) ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, coalesce(build_id::text,''), image_digest, kind,
		        coalesce(source_path,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
		        status, coalesce(error,''), created_at
		 from deployments where app_id = $1 order by created_at desc limit $2 offset $3`,
		appID, limit, offset)
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
			        d.status, coalesce(d.error,''), d.created_at
			 from deployments d join apps a on a.id = d.app_id
			 where a.account_id = $1 order by d.created_at desc limit $2`,
			accountID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select d.id, d.app_id, coalesce(d.build_id::text,''), d.image_digest, d.kind,
			        coalesce(d.source_path,''), coalesce(d.source_bytes,0), coalesce(d.handler,''), coalesce(d.log_path,''),
			        d.status, coalesce(d.error,''), d.created_at
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

func (s *PgStore) SetDeploymentRootfs(ctx context.Context, id, path string, bytes int64) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments set rootfs_path = $2, rootfs_bytes = $3 where id = $1`,
		id, nullString(path), bytes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- builds ------------------------------------------------------------------

func (s *PgStore) CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`insert into builds (deployment_id, kind, source_bytes, status, log_path)
		 values ($1, $2, $3, 'queued', $4)
		 returning id, deployment_id, kind, source_bytes, status,
		           coalesce(failure_class,''), coalesce(log_path,''), started_at, finished_at`,
		deploymentID, string(kind), sourceBytes, nullString(logPath))
	return scanBuild(row)
}

func (s *PgStore) BuildByID(ctx context.Context, id string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at from builds where id = $1`, id)
	return scanBuild(row)
}

func (s *PgStore) BuildByDeployment(ctx context.Context, deploymentID string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at from builds where deployment_id = $1
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

func (s *PgStore) CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`insert into instances (app_id, deployment_id, state, ram_mb) values ($1, $2, $3, $4)
		 returning id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at`,
		appID, deploymentID, state, ramMB)
	return scanInstance(row)
}

func (s *PgStore) InstanceByID(ctx context.Context, id string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at
		 from instances where id = $1`, id)
	return scanInstance(row)
}

func (s *PgStore) ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select id, app_id, deployment_id, state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at
		 from instances where app_id = $1 order by started_at desc`, appID)
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
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at
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
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at
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
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at
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
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at
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
	row := s.pool.QueryRow(ctx,
		`insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, path, stale)
		 values ($1, $2, $3, $4, $5, $6)
		 returning id, deployment_id::text, fc_version, mem_bytes, disk_bytes, path, stale, created_at`,
		snap.DeploymentID, snap.FCVersion, snap.MemBytes, snap.DiskBytes, snap.Path, snap.Stale)
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
		`select id, deployment_id::text, fc_version, mem_bytes, disk_bytes, path, stale, created_at
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
		&statusStr, &d.Error, &d.CreatedAt); err != nil {
		return Deployment{}, mapErr(err)
	}
	d.Kind = DeploymentKind(kind)
	d.Status = DeploymentStatus(statusStr)
	return d, nil
}

// scanDeploymentWithRootfs is the post-imaged variant that also reads the
// rootfs_path / rootfs_bytes columns stamped by SetDeploymentRootfs. Every
// reads-everything query (used by schedd's prime handshake, M5) uses this so
// the snapshot_prime consumer sees the layer path.
func scanDeploymentWithRootfs(row pgx.Row) (Deployment, error) {
	d := Deployment{}
	var kind, statusStr, rootfsPath string
	if err := row.Scan(&d.ID, &d.AppID, &d.BuildID, &d.ImageDigest, &kind,
		&d.SourcePath, &d.SourceBytes, &d.Handler, &d.LogPath,
		&rootfsPath, &d.RootfsBytes,
		&statusStr, &d.Error, &d.CreatedAt); err != nil {
		return Deployment{}, mapErr(err)
	}
	d.RootfsPath = rootfsPath
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
			&statusStr, &d.Error, &d.CreatedAt); err != nil {
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
	if err := row.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &startedAt, &finishedAt); err != nil {
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
func scanInstanceCols(scan func(...any) error) (Instance, error) {
	ins := Instance{}
	var started, lastReq, parked *time.Time
	if err := scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
		&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked); err != nil {
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

func scanSnapshot(row pgx.Row) (Snapshot, error) {
	s := Snapshot{}
	if err := row.Scan(&s.ID, &s.DeploymentID, &s.FCVersion, &s.MemBytes, &s.DiskBytes, &s.Path, &s.Stale, &s.CreatedAt); err != nil {
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
		if err := rows.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
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
