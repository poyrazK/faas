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
		`insert into accounts (email, plan, status) values ($1, $2, 'active') returning id, email, plan, status, coalesce(stripe_customer_id,''), created_at`,
		email, string(plan))
	return scanAccount(row)
}

func (s *PgStore) AccountByID(ctx context.Context, id string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), created_at from accounts where id = $1`, id)
	return scanAccount(row)
}

func (s *PgStore) AccountByEmail(ctx context.Context, email string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), created_at from accounts where email = $1`, email)
	return scanAccount(row)
}

func (s *PgStore) AccountByKeyHash(ctx context.Context, hash []byte) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select a.id, a.email, a.plan, a.status, coalesce(a.stripe_customer_id,''), a.created_at
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

// AccountByStripeCustomerID resolves the account behind a Stripe webhook
// payload. The unique index makes this O(log n); MemStore does it with a
// map.
func (s *PgStore) AccountByStripeCustomerID(ctx context.Context, stripeCustomerID string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), created_at
		 from accounts where stripe_customer_id = $1`,
		stripeCustomerID)
	var a Account
	var plan, status string
	if err := row.Scan(&a.ID, &a.Email, &plan, &status, &a.StripeCustomerID, &a.CreatedAt); err != nil {
		// pgx returns ErrNoRows when the SELECT finds nothing; map to the
		// store sentinel so callers can errors.Is(err, state.ErrNotFound).
		return Account{}, ErrNotFound
	}
	a.Plan = api.Plan(plan)
	a.Status = AccountStatus(status)
	return a, nil
}

// ListAllAccounts returns every account. Meterd walks this on the quota
// tick + hourly Stripe push; bounded by the customer count on the box.
func (s *PgStore) ListAllAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`select id, email, plan, status, coalesce(stripe_customer_id,''), created_at
		 from accounts order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		var plan, status string
		if err := rows.Scan(&a.ID, &a.Email, &plan, &status, &a.StripeCustomerID, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Plan = api.Plan(plan)
		a.Status = AccountStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
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
		`insert into apps (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest)
		 values ($1, $2, $3, $4, $5, $6, $7, 'active', $8::jsonb)
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at`,
		app.AccountID, app.Slug, string(app.Type), runtime, app.RAMMB, idle, app.MaxConcurrency, manifestBytes)
	return scanApp(row)
}

func (s *PgStore) AppByID(ctx context.Context, id string) (App, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at
		 from apps where id = $1`, id)
	return scanApp(row)
}

func (s *PgStore) AppBySlug(ctx context.Context, slug string) (App, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at
		 from apps where slug = $1 and status <> 'deleted'`, slug)
	return scanApp(row)
}

func (s *PgStore) ListApps(ctx context.Context, accountID string) ([]App, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		        max_concurrency, status, manifest, created_at
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
		        max_concurrency, status, manifest, created_at
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
		   manifest        = case when $7 then $8::jsonb else manifest end
		 where id = $1
		 returning id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
		           max_concurrency, status, manifest, created_at`,
		id,
		p.RAMMB, p.SetIdleTimeout, derefInt(p.IdleTimeoutS),
		p.MaxConcurrency, nullAppStatus(p.Status),
		p.Manifest != nil, manifestBytes)
	return scanApp(row)
}

func (s *PgStore) DeleteApp(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update apps set status = 'deleted' where id = $1`, id)
	return err
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
		        status, coalesce(error,''), created_at
		 from deployments where id = $1`, id)
	return scanDeployment(row)
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
		           coalesce(failure_class,''), coalesce(log_path,''), created_at`,
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
		 returning domain, app_id, challenge_token, verified_at`,
		domain, appID, token)
	d := CustomDomain{}
	if err := row.Scan(&d.Domain, &d.AppID, &d.ChallengeToken, &d.VerifiedAt); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	return d, nil
}

func (s *PgStore) DomainByName(ctx context.Context, domain string) (CustomDomain, error) {
	row := s.pool.QueryRow(ctx,
		`select domain, app_id, challenge_token, verified_at from custom_domains where domain = $1`, domain)
	d := CustomDomain{}
	if err := row.Scan(&d.Domain, &d.AppID, &d.ChallengeToken, &d.VerifiedAt); err != nil {
		return CustomDomain{}, mapErr(err)
	}
	return d, nil
}

func (s *PgStore) ListDomainsForApp(ctx context.Context, appID string) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx,
		`select domain, app_id, challenge_token, verified_at from custom_domains where app_id = $1 order by domain`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomains(rows)
}

func (s *PgStore) ListDomainsForAccount(ctx context.Context, accountID string) ([]CustomDomain, error) {
	rows, err := s.pool.Query(ctx,
		`select d.domain, d.app_id, d.challenge_token, d.verified_at
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
		 returning id, app_id, schedule, path, enabled, created_at`,
		appID, schedule, path, enabled)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
		return Cron{}, mapErr(err)
	}
	return c, nil
}

func (s *PgStore) CronByID(ctx context.Context, id string) (Cron, error) {
	row := s.pool.QueryRow(ctx,
		`select id, app_id, schedule, path, enabled, created_at from crons where id = $1`, id)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
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
	_, err := s.pool.Exec(ctx,
		`insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests)
		 values ($1, $2, $3, $4, $5, $6)
		 on conflict (instance_id, minute) do update
		   set mb_seconds = usage_minutes.mb_seconds + excluded.mb_seconds,
		       requests = usage_minutes.requests + excluded.requests`,
		accountID, appID, instanceID, minute, mbSeconds, requests)
	return err
}

func (s *PgStore) UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error) {
	monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, month, mb_seconds, requests from usage_monthly
		 where account_id = $1 and month = $2 order by app_id`,
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

// --- row scanners ------------------------------------------------------------

func scanAccount(row pgx.Row) (Account, error) {
	a := Account{}
	var planStr, statusStr string
	if err := row.Scan(&a.ID, &a.Email, &planStr, &statusStr, &a.StripeCustomerID, &a.CreatedAt); err != nil {
		return Account{}, mapErr(err)
	}
	a.Plan = api.Plan(planStr)
	a.Status = AccountStatus(statusStr)
	return a, nil
}

func scanApp(row pgx.Row) (App, error) {
	a := App{}
	var typeStr, statusStr string
	var manifestBytes []byte
	if err := row.Scan(&a.ID, &a.AccountID, &a.Slug, &typeStr, &a.Runtime, &a.RAMMB, &a.IdleTimeoutS,
		&a.MaxConcurrency, &statusStr, &manifestBytes, &a.CreatedAt); err != nil {
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
			&a.MaxConcurrency, &statusStr, &manifestBytes, &a.CreatedAt); err != nil {
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
	if err := row.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &b.StartedAt, &b.FinishedAt); err != nil {
		return Build{}, mapErr(err)
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
		if err := rows.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
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
