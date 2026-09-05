// pgstore.go is the ADR-017 hand-written M5 adapter. It implements the
// Store interface against the Postgres schema in migrations/*.sql; the SQL
// itself lives in queries.sql so sqlc.yaml is the canonical source. This
// file is the thin adapter that maps sqlc-style params/rows to the domain
// types and surfaces ErrNotFound / ErrConflict at the right boundaries.
//
// `make sqlc-check` regenerates pkg/state/sqlc/ in CI and fails when it
// drifts from queries.sql + schema.sql. TODO(M5.1): replace this adapter's
// query bodies with calls into the generated package. See ADR-017.
package state

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
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

// Ping tests database connectivity through the underlying connection pool.
func (s *PgStore) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("state: pgstore has nil pool")
	}
	return s.pool.Ping(ctx)
}

// Compile-time check.
var _ Store = (*PgStore)(nil)

// heartbeatHistoryMaxRows is the upper bound used to pre-size the
// result slice in ListComputeNodeHeartbeats. The apid handler
// enforces a 2000-row hard cap on ?limit= and the store defaults
// to 200 when limit <= 0; 2000 is the highest value the slice
// ever grows to in practice. Pre-sizing with a constant keeps
// CodeQL's slice-allocation rule from flagging the call site as
// user-input-driven (the rule wants a constant capacity; append
// will grow past it if a future change ever raises the cap).
const heartbeatHistoryMaxRows = 2000

// DefaultEnvScope is the package-local mirror of api.DefaultEnvScope.
// ADR-092 PR-A deliberately duplicates the literal here rather than
// importing pkg/api, because pkg/state → pkg/api would close a cycle
// (every consumer of pkg/state can import pkg/api on its own, but
// pkg/api must not import pkg/state — see pkg/api/dto.go for the
// reverse direction's rules). Kept in sync via a unit test in
// pkg/state/api_compat_test.go that asserts equality of the
// string literals — not an import, just a runtime assertion.
const DefaultEnvScope = "default"

// --- accounts ---------------------------------------------------------------

func (s *PgStore) CreateAccount(ctx context.Context, email string, plan api.Plan) (Account, error) {
	// PR 6 (issue #190 / IAM-6 / ADR-061): CreateAccount now ALSO
	// writes the personal-org + owner-membership rows so the legacy
	// entry point stays drop-in compatible with the migration 00129
	// NOT NULL flip on api_keys.org_id. Pre-PR-6 callers (CLI login,
	// dev seed, all pgstore tests) used CreateAccount + then a
	// CreateAPIKey* that joins on the personal org — the join
	// returned NULL after PR-6's flip and 23502'd every insert.
	// CreateAccountWithPersonalOrg (PR 3) is the canonical path and
	// is the same shape under the hood; we delegate to it so the
	// logic is in one place. PR 9 deletes CreateAccount when the
	// legacy dual-write window closes.
	res, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{
		Email: email,
		Plan:  plan,
	})
	if err != nil {
		return Account{}, err
	}
	return res.Account, nil
}

// CreateAccountWithPersonalOrg is the PR 3 canonical
// account-creation entry point (issue #190 / ADR-061). Runs the
// three INSERTs under one tx so the "every account has exactly one
// personal org" invariant is atomic at the SQL layer.
//
// The partial unique orgs_one_personal_per_account_uniq
// (migrations/00099) is the SQL-level tripwire against any future
// concurrent caller; ReadCommitted isolation is sufficient because
// the tripwire fires at any level. The pgerrcode.MapErr funnel
// maps 23505 (accounts.email UNIQUE) to state.ErrConflict so the
// postSignup / OAuth ladders collapse to the idempotent-signin
// path on a duplicate email.
func (s *PgStore) CreateAccountWithPersonalOrg(ctx context.Context, params CreateAccountWithPersonalOrgParams) (CreateAccountWithPersonalOrgResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateAccountWithPersonalOrgResult{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	// 1. Account INSERT.
	acct, err := scanAccountCols(tx.QueryRow(ctx,
		`insert into accounts (email, plan, status) values ($1, $2, 'active')
		 returning id, email, plan, status, coalesce(provider_customer_id,''),
		           coalesce(stripe_subscription_item,''), created_at,
		           deletion_requested_at, last_quota_warning_at, past_due_at,
		           mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash,
		           mfa_required, egress_allowlist_extra`,
		params.Email, string(params.Plan)).Scan)
	if err != nil {
		return CreateAccountWithPersonalOrgResult{}, mapErr(err)
	}

	// 2. Personal org INSERT. personal_owner_account_id points back at
	//    the freshly minted account; the partial unique enforces
	//    at most one personal org per account.
	org, err := scanOrg(tx.QueryRow(ctx, `
		insert into orgs (
		    id, slug, name, personal_org, personal_owner_account_id,
		    plan, status, created_at, updated_at
		) values (
		    gen_random_uuid(), $1, 'Personal', true, $2,
		    $3, 'active', now(), now()
		)
		returning id, slug, name, personal_org, personal_owner_account_id,
		          plan, status, provider_customer_id, stripe_subscription_item,
		          deleted_pending, created_at, updated_at
	`, PersonalOrgSlug(acct.ID), acct.ID, string(acct.Plan)))
	if err != nil {
		return CreateAccountWithPersonalOrgResult{}, mapErr(err)
	}

	// 3. Owner membership INSERT. The exactly-one-owner partial unique
	//    org_memberships_one_owner_idx enforces at the SQL layer.
	if _, err := tx.Exec(ctx, `
		insert into org_memberships (org_id, account_id, role, invited_by_account_id)
		values ($1, $2, 'owner', null)
	`, org.ID, acct.ID); err != nil {
		return CreateAccountWithPersonalOrgResult{}, mapErr(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateAccountWithPersonalOrgResult{}, fmt.Errorf("state: commit: %w", err)
	}
	return CreateAccountWithPersonalOrgResult{Account: acct, PersonalOrg: org}, nil
}

func (s *PgStore) AccountByID(ctx context.Context, id string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(provider_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required, egress_allowlist_extra from accounts where id = $1`, id)
	return scanAccount(row)
}

// AccountsByIDs is the batch equivalent of AccountByID — one round-
// trip replaces N per-row lookups. Returns a map keyed by account
// ID; missing IDs are absent (not an error). The map-absence
// contract mirrors AccountByID's per-row missing case so the
// dashboard render can keep the "(deleted account)" fallback.
//
// Wide projection intentionally matches AccountByID/AccountByEmail/
// AccountByKeyHash so mfa_* / deletion_requested_at / past_due_at
// are present and the requireMFA middleware sees post-enrollment
// state (see scanAccountCols doc-comment at pkg/state/pgstore.go).
//
// PR-9 §1: closes the N+1 fan-out in
// cmd/apid/handlers_dashboard.go's renderOrgDetail member loop.
func (s *PgStore) AccountsByIDs(ctx context.Context, ids []string) (map[string]Account, error) {
	out := make(map[string]Account, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, email, plan, status, coalesce(provider_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required, egress_allowlist_extra from accounts where id = any($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("state: accounts by IDs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAccountCols(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("state: accounts by IDs: row: %w", err)
		}
		out[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: accounts by IDs: rows: %w", err)
	}
	return out, nil
}

func (s *PgStore) AccountByEmail(ctx context.Context, email string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(provider_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required, egress_allowlist_extra from accounts where email = $1`, email)
	return scanAccount(row)
}

func (s *PgStore) AccountByKeyHash(ctx context.Context, hash []byte) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select a.id, a.email, a.plan, a.status, coalesce(a.provider_customer_id,''), coalesce(a.stripe_subscription_item,''), a.created_at, a.deletion_requested_at, a.last_quota_warning_at, a.past_due_at, a.mfa_enrolled_at, a.mfa_secret_encrypted, a.mfa_recovery_codes_hash, a.mfa_required, a.egress_allowlist_extra
		 from accounts a join api_keys k on k.account_id = a.id where k.key_sha256 = $1`, hash)
	return scanAccount(row)
}

// APIKeyByHash resolves an api_keys row by its SHA-256 hash. Used by
// the post-login audit log (cmd/apid/handlers_auth.go) so an operator
// investigating "who signed in as alice?" can identify which key
// authenticated. Returns ErrNotFound when no row matches. Same O(log n)
// index-backed lookup as AccountByKeyHash — same key_sha256 UNIQUE
// constraint in migrations/00001_init.sql.
//
// The projection reads the IAM-5 columns (expires_at, status,
// revoked_at, rotated_from_id) so the auth path can enforce
// expiry / revoked gates without a second round-trip. The new
// columns default to NULL / 'active', so existing rows round-trip
// cleanly with Status='active' and ExpiresAt=nil.
func (s *PgStore) APIKeyByHash(ctx context.Context, hash []byte) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		 from api_keys where key_sha256 = $1`, hash)
	return scanAPIKey(row)
}

// AuthenticateKey resolves a bearer token to its account + key. It is
// the canonical lookup for the apid auth middleware (cmd/apid server.go
// s.auth). Implementations are expected to be cheap (index-backed lookup
// on key_sha256) and to return ErrNotFound when the hash has no matching
// key (the auth middleware maps that to 401). The account and key are
// read with two queries in PgStore — the principal is assembled
// in-process. TODO(perf): both queries hit the same UNIQUE index, so
// collapsing to a single SQL JOIN halves the round-trips on every
// authenticated request. Not blocking: index hits are fast enough that
// the perf cost is negligible at current scale. Revisit when auth
// latency shows up on the dashboard. See ADR-034 rev2.
//
// IAM-5 (issue #189) gate: after the key row is loaded, three checks
// run in order —
//
//  1. status='revoked' → return ErrAPIKeyRevoked (terminal, idempotent).
//  2. expires_at != NULL && expires_at < now() → lazy-flip to
//     status='revoked' atomically (one UPDATE, coalesce revoked_at),
//     then return ErrAPIKeyExpired. The next auth attempt sees the
//     revoked state via check (1).
//  3. otherwise return (account, key, nil).
//
// The audit `key.expired` row is emitted by the auth middleware (which
// has the Auditor dependency), not here. The store is
// dependency-free.
// AuthenticateOIDCBearer resolves an OIDC-derived short-lived bearer
// (issue #270 / ADR-101). Hash lookup hits
// oidc_exchanged_tokens.token_hash (UNIQUE index); rows past
// ExpiresAt return ErrNotFound (the 5-min TTL is the natural expiry
// path; no lazy-flip required). Returns the Account + a synthetic
// APIKey projection with Scopes=["deploy:write"] and Status="active"
// so the principal stamp + downstream requireScope chain works
// unchanged.
//
// The SQL lands when migrations 00265/00266 merge. Until then the
// method returns ErrNotFound unconditionally (the table doesn't
// exist yet — the integration test is the gate, not unit tests
// against PgStore).
func (s *PgStore) AuthenticateOIDCBearer(ctx context.Context, hash []byte) (Account, APIKey, error) {
	q := sqlc.New()
	row, err := q.GetOIDCExchangedTokenByHash(ctx, s.pool, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, APIKey{}, ErrNotFound
		}
		return Account{}, APIKey{}, err
	}
	acct, err := s.AccountByID(ctx, uuidFromPgtype(row.AccountID).String())
	if err != nil {
		// FK CASCADE means a token row cannot outlive its account
		// in steady state; if we hit ErrNotFound here the cascade
		// is racing a delete — treat the bearer as gone.
		return Account{}, APIKey{}, err
	}
	token := OIDCExchangedToken{
		ID:        uuidFromPgtype(row.ID).String(),
		AccountID: uuidFromPgtype(row.AccountID).String(),
		TokenHash: row.TokenHash,
		ExpiresAt: timeFromPgtype(row.ExpiresAt),
		IssuerURL: row.IssuerUrl,
		Subject:   row.Subject,
		Audience:  row.Audience,
		JTI:       row.Jti,
		CreatedAt: timeFromPgtype(row.CreatedAt),
	}
	return acct, token.ToAPIKey(), nil
}

// AccountByOIDCSubject resolves an OIDC subject to the platform
// account it's bound to. Used by pkg/oidc/handler.go step 4 to
// determine the (account_id, issuer_url) trust-policy row.
//
// The SQL lands when migration 00265 merges. Until then the method
// returns ErrNotFound unconditionally.
func (s *PgStore) AccountByOIDCSubject(ctx context.Context, issuerURL, subject string) (Account, error) {
	q := sqlc.New()
	row, err := q.AccountByOIDCIssuerSubject(ctx, s.pool, sqlc.AccountByOIDCIssuerSubjectParams{
		IssuerUrl:      issuerURL,
		SubjectPattern: pgtype.Text{String: subject, Valid: subject != ""},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			repo, ok := githubActionsRepositoryFromSubject(issuerURL, subject)
			if !ok {
				return Account{}, ErrNotFound
			}
			// First secretless deploy: resolve exactly one account through an
			// existing repo binding whose installation was proven by user OAuth.
			var accountID string
			bootstrapErr := s.pool.QueryRow(ctx, `
				select min(a.github_install_account_id::text)
				from apps a
				join github_installations gi
				  on gi.account_id = a.github_install_account_id
				 and gi.installation_id = a.github_install_id
				where lower(a.github_repo_full_name) = lower($1)
				  and a.github_install_account_id = a.account_id
				  and a.deleted_at is null
				having count(distinct a.github_install_account_id) = 1`, repo).Scan(&accountID)
			if errors.Is(bootstrapErr, pgx.ErrNoRows) {
				return Account{}, ErrNotFound
			}
			if bootstrapErr != nil {
				return Account{}, bootstrapErr
			}
			return s.AccountByID(ctx, accountID)
		}
		return Account{}, err
	}
	return s.AccountByID(ctx, uuidFromPgtype(row.ID).String())
}

// UpsertOIDCTrustPolicy is the per-(account, issuer) insert-or-update
// path (issue #270 / ADR-101). See Store.UpsertOIDCTrustPolicy for
// the contract.
//
// The SQL lands when migration 00265 merges. Until then the method
// returns ErrNotFound unconditionally.
func (s *PgStore) UpsertOIDCTrustPolicy(ctx context.Context, p *OIDCTrustPolicy) (*OIDCTrustPolicy, error) {
	if p.AccountID == "" || p.IssuerURL == "" {
		return nil, ErrNotFound
	}
	accountUUID, err := uuid.Parse(p.AccountID)
	if err != nil {
		return nil, ErrNotFound
	}
	claims := json.RawMessage(`{}`)
	if len(p.RequiredClaims) > 0 {
		buf, marshalErr := json.Marshal(p.RequiredClaims)
		if marshalErr != nil {
			return nil, marshalErr
		}
		claims = buf
	}
	q := sqlc.New()
	row, err := q.UpsertOIDCTrustPolicy(ctx, s.pool, sqlc.UpsertOIDCTrustPolicyParams{
		AccountID:      pgtypeFromUUID(accountUUID),
		IssuerUrl:      p.IssuerURL,
		JwksUrl:        p.JWKSURL,
		Audience:       p.Audience,
		SubjectPattern: pgtype.Text{String: p.SubjectPattern, Valid: p.SubjectPattern != ""},
		Algorithms:     p.Algorithms,
		RequiredClaims: claims,
		AuditLogin:     p.AuditLogin,
	})
	if err != nil {
		return nil, err
	}
	return trustPolicyFromRow(
		row.AccountID, row.IssuerUrl, row.JwksUrl, row.Audience,
		row.SubjectPattern, row.Algorithms, row.RequiredClaims,
		row.CreatedAt, row.UpdatedAt, row.AuditLogin,
	), nil
}

// GetOIDCTrustPolicy is the (account_id, issuer_url) lookup.
// See Store.GetOIDCTrustPolicy for the contract.
//
// The SQL lands when migration 00265 merges. Stub for now.
func (s *PgStore) GetOIDCTrustPolicy(ctx context.Context, accountID, issuerURL string) (*OIDCTrustPolicy, error) {
	accountUUID, err := uuid.Parse(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.GetOIDCTrustPolicy(ctx, s.pool, sqlc.GetOIDCTrustPolicyParams{
		AccountID: pgtypeFromUUID(accountUUID),
		IssuerUrl: issuerURL,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return trustPolicyFromRow(
		row.AccountID, row.IssuerUrl, row.JwksUrl, row.Audience,
		row.SubjectPattern, row.Algorithms, row.RequiredClaims,
		row.CreatedAt, row.UpdatedAt, row.AuditLogin,
	), nil
}

// ListOIDCTrustPoliciesForAccount returns every trust policy the
// account owns. See Store.ListOIDCTrustPoliciesForAccount for the
// contract.
//
// The SQL lands when migration 00265 merges. Stub for now.
func (s *PgStore) ListOIDCTrustPoliciesForAccount(ctx context.Context, accountID string) ([]*OIDCTrustPolicy, error) {
	accountUUID, err := uuid.Parse(accountID)
	if err != nil {
		return nil, ErrNotFound
	}
	q := sqlc.New()
	rows, err := q.ListOIDCTrustPoliciesForAccount(ctx, s.pool, pgtypeFromUUID(accountUUID))
	if err != nil {
		return nil, err
	}
	out := make([]*OIDCTrustPolicy, 0, len(rows))
	for _, r := range rows {
		out = append(out, trustPolicyFromRow(
			r.AccountID, r.IssuerUrl, r.JwksUrl, r.Audience,
			r.SubjectPattern, r.Algorithms, r.RequiredClaims,
			r.CreatedAt, r.UpdatedAt, r.AuditLogin,
		))
	}
	return out, nil
}

// InsertOIDCExchangedToken stores a fresh exchanged-token row and
// returns the server-minted row id (gen_random_uuid at the SQL
// layer). The id is the audit-correlation key — pkg/oidc/handler.go
// echoes it in the response and stamps it on the audit row so a
// customer can correlate "which fp_oidc_<…> bearer did CI ship?"
// to "which row in oidc_exchanged_tokens".
//
// Returns ErrNotFound when the inputs are empty so the handler can
// surface a 500 cleanly without leaking the underlying SQL error
// to the wire.
func (s *PgStore) InsertOIDCExchangedToken(ctx context.Context, t *OIDCExchangedToken) (string, error) {
	if t.AccountID == "" || len(t.TokenHash) == 0 || t.ExpiresAt.IsZero() {
		return "", ErrNotFound
	}
	accountUUID, err := uuid.Parse(t.AccountID)
	if err != nil {
		return "", ErrNotFound
	}
	q := sqlc.New()
	row, err := q.InsertOIDCExchangedToken(ctx, s.pool, sqlc.InsertOIDCExchangedTokenParams{
		AccountID: pgtypeFromUUID(accountUUID),
		TokenHash: t.TokenHash,
		ExpiresAt: pgtypeFromTime(t.ExpiresAt),
		IssuerUrl: t.IssuerURL,
		Subject:   t.Subject,
		Audience:  t.Audience,
		Jti:       pgtype.Text{String: t.JTI, Valid: t.JTI != ""},
	})
	if err != nil {
		return "", err
	}
	return uuidFromPgtype(row.ID).String(), nil
}

// GetOIDCExchangedTokenByHash returns the row whose TokenHash
// equals the input. See Store.GetOIDCExchangedTokenByHash for the
// contract.
//
// The SQL lands when migration 00266 merges. Stub for now.
func (s *PgStore) GetOIDCExchangedTokenByHash(ctx context.Context, hash []byte) (*OIDCExchangedToken, error) {
	if len(hash) == 0 {
		return nil, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.GetOIDCExchangedTokenByHash(ctx, s.pool, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return oidcExchangedTokenFromRow(row), nil
}

// DeleteOIDCExchangedToken is the operator-driven revoke path.
// See Store.DeleteOIDCExchangedToken for the contract.
//
// The SQL lands when migration 00266 merges. Stub for now.
func (s *PgStore) DeleteOIDCExchangedToken(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	tokenUUID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	q := sqlc.New()
	return q.DeleteOIDCExchangedToken(ctx, s.pool, pgtypeFromUUID(tokenUUID))
}

func (s *PgStore) AuthenticateKey(ctx context.Context, hash []byte) (Account, APIKey, error) {
	acct, err := s.AccountByKeyHash(ctx, hash)
	if err != nil {
		return Account{}, APIKey{}, err
	}
	key, err := s.APIKeyByHash(ctx, hash)
	if err != nil {
		return Account{}, APIKey{}, err
	}
	// IAM-5 gate. Run before the success return so the
	// middleware can translate the sentinel to the right 401.
	if key.Status == string(APIKeyStatusRevoked) {
		return Account{}, APIKey{}, ErrAPIKeyRevoked
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
		// Lazy expiry: flip to revoked in a single UPDATE
		// guarded by `status <> 'revoked'` so a concurrent
		// auth attempt doesn't double-write revoked_at. Best
		// effort: a failure here means the next attempt
		// re-observes the expired state and retries the
		// flip. The auth path still rejects with
		// ErrAPIKeyExpired regardless.
		_, _ = s.pool.Exec(ctx,
			`update api_keys
			    set status = 'revoked',
			        revoked_at = coalesce(revoked_at, now())
			  where id = $1 and status <> 'revoked'`, key.ID)
		return Account{}, APIKey{}, ErrAPIKeyExpired
	}
	return acct, key, nil
}

// scanAPIKey reads the eleven-column api_keys projection (id,
// account_id, org_id, key_sha256, label, scopes, created_at,
// last_used_at, expires_at, status, revoked_at, rotated_from_id).
// Columns not enumerated in the tuple (e.g. legacy callers from
// before IAM-1) still work because every query in this package
// writes the full column list; the shared helper makes the
// projections stay in lockstep. The four trailing columns are the
// IAM-5 (issue #189) surface — pgtype's native nullable support
// means a NULL column produces a pgtype.Timestamptz{Valid:false}
// that the scan helper converts to a nil *time.Time.
// rotated_from_id is a uuid pointer; the others are nullable
// timestamps. PR 6 (issue #190 / IAM-6) inserts org_id as a
// NOT-NULL column (migration 00127), seated between account_id
// and key_sha256 so every pgstore INSERT/RETURNING list puts it
// at the same index — the move is mechanical and isolated to
// this file.
//
// The list-sites that pre-date IAM-5 (e.g. CreateAPIKey,
// DeleteAPIKeyReturning, ListAPIKeys) keep the seven-column
// projection by composing the helper with default values — the
// helper is a per-call writer, not a global registry, so a
// caller that wants only the seven columns uses a local
// scan and ignores the new fields. To keep the diff small,
// every existing call site now writes the full eleven columns
// (the new ones are NULL by default; the constraint is the
// floor, the store is the wall). The same shape applies for the
// PR 6 org_id: every SELECT/RETURNING reads the full twelve
// columns.
func scanAPIKey(row pgx.Row) (APIKey, error) {
	var (
		k         APIKey
		hashBytes []byte
		expiresAt pgtype.Timestamptz
		revokedAt pgtype.Timestamptz
		rotated   *string
		createdIP *string
		parent    *string
	)
	if err := row.Scan(&k.ID, &k.AccountID, &k.OrgID, &hashBytes, &k.Label, &k.Scopes, &k.CreatedAt, &k.LastUsedAt,
		&expiresAt, &k.Status, &revokedAt, &rotated, &createdIP, &k.CreatedUA, &parent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, mapErr(err)
	}
	k.Hash = hashBytes
	if expiresAt.Valid {
		t := expiresAt.Time
		k.ExpiresAt = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		k.RevokedAt = &t
	}
	k.RotatedFromID = rotated
	if createdIP != nil {
		k.CreatedIP = *createdIP
	}
	k.ParentKeyID = parent
	return k, nil
}

func (s *PgStore) UpdateAccountPlan(ctx context.Context, id string, plan api.Plan) error {
	_, err := s.pool.Exec(ctx, `update accounts set plan = $2 where id = $1`, id, string(plan))
	return err
}

func (s *PgStore) UpdateAccountStatus(ctx context.Context, id string, status AccountStatus) error {
	_, err := s.pool.Exec(ctx, `update accounts set status = $2 where id = $1`, id, string(status))
	return err
}

// UpdateAccountProviderCustomerID records the Stripe `cus_…` ID on the
// account row. Schema carries a unique index on provider_customer_id so a
// second customer picking up an old ID would fail at the DB; MemStore
// mirrors that with the same shape (single-value index map).
func (s *PgStore) UpdateAccountProviderCustomerID(ctx context.Context, id, stripeCustomerID string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts set provider_customer_id = $2 where id = $1`,
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
// until pkg/billing/stripe::EnsureCustomer receives
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

// --- MFA (IAM-2, issue #186) -------------------------------------------------
//
// The four mfa_* columns are projected by every account SELECT via
// scanAccountCols; the consume path runs inside a SELECT … FOR UPDATE
// + UPDATE pair so two concurrent /recover calls on the same account
// cannot both observe and burn the same hash.

// ConsumeRecoveryCode atomically matches `presented` against the
// stored recovery-code hashes and removes the matching hash from the
// array. Returns:
//
//   - (false, 0, false, ErrNotFound) when the row is missing
//   - (false, 0, false, nil)         when the presented code doesn't
//     match any stored hash
//   - (true, lastCode, remaining, nil) when the code matches and was
//     removed; lastCode is true iff exactly one hash remained (the
//     handler refuses to burn the last code and prompts for a
//     password reset instead); remaining is the count of hashes on
//     the row AFTER the consume committed, used by the handler to
//     render the post-burn customer email with the right tone
//     (one-of-many vs warning vs last-code) — see issue #329.
//
// The sealed TOTP secret is preserved across consumes: the customer
// can still /verify after burning every recovery code. The transaction
// guarantees that two concurrent /recover calls on the same account
// cannot both observe and burn the same hash.
func (s *PgStore) ConsumeRecoveryCode(ctx context.Context, id string, presented []byte) (matched bool, lastCode bool, remaining int, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, false, 0, fmt.Errorf("state: mfa consume tx: %w", err)
	}
	// Best-effort rollback: a successful Commit absorbs the deferred
	// rollback into a no-op, so we don't branch on err.
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var hashes [][]byte
	row := tx.QueryRow(ctx,
		`select mfa_recovery_codes_hash
		   from accounts
		  where id = $1
		  for update`, id)
	if scanErr := row.Scan(&hashes); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return false, false, 0, ErrNotFound
		}
		return false, false, 0, mapErr(scanErr)
	}
	matchedIdx := -1
	for i, h := range hashes {
		if Sha256Equal(h, presented) {
			matchedIdx = i
			break
		}
	}
	if matchedIdx < 0 {
		// No match: no UPDATE needed, just commit the empty tx so the
		// row lock releases promptly.
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, false, 0, fmt.Errorf("state: mfa consume commit (no-match): %w", commitErr)
		}
		return false, false, 0, nil
	}
	lastCode = len(hashes) == 1
	// Defensive copy: rebuild without the matched element. len(hashes)
	// is bounded at 10 (api.RecoveryCodeCount), so this is a 10-element
	// allocation at most.
	next := make([][]byte, 0, len(hashes)-1)
	next = append(next, hashes[:matchedIdx]...)
	next = append(next, hashes[matchedIdx+1:]...)
	if _, execErr := tx.Exec(ctx,
		`update accounts
		    set mfa_recovery_codes_hash = $2
		  where id = $1`, id, next); execErr != nil {
		return false, false, 0, mapErr(execErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return false, false, 0, fmt.Errorf("state: mfa consume commit: %w", commitErr)
	}
	return true, lastCode, len(next), nil
}

// MatchRecoveryCode tests a presented SHA-256 hash against the
// stored set WITHOUT mutating. Used by /recover to refuse-the-burn
// on the customer's last code (issue #186 review Finding #5).
// The handler sequence is:
//
//  1. MatchRecoveryCode(presented) → (matched, lastCode, nil).
//  2. If matched && lastCode → return 409 "use password instead"
//     without touching the store.
//  3. Otherwise ConsumeRecoveryCode(presented) to do the burn.
//
// Without step 1 we can't refuse the burn atomically: the prior
// shape was consume-then-check, which would commit the delete
// before the lastCode branch fired — the customer would land
// with zero codes even though the wire said 409.
//
// The SELECT runs FOR SHARE so a concurrent /disable's
// ConsumeRecoveryCode (which takes FOR UPDATE) blocks until we
// commit, serialising the refuse vs the disable race correctly.
// A short read tx is enough — we never write. NOTE: the tx must NOT
// be opened READ ONLY — PostgreSQL rejects `SELECT ... FOR SHARE`
// inside a read-only transaction (SQLSTATE 25006), which would make
// every MatchRecoveryCode call fail at runtime. The FOR SHARE lock
// itself is the concurrency control; the method never issues a write.
func (s *PgStore) MatchRecoveryCode(ctx context.Context, id string, presented []byte) (bool, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, false, fmt.Errorf("state: mfa match tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var hashes [][]byte
	row := tx.QueryRow(ctx,
		`select mfa_recovery_codes_hash from accounts where id = $1 for share`, id)
	if scanErr := row.Scan(&hashes); scanErr != nil {
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return false, false, ErrNotFound
		}
		return false, false, mapErr(scanErr)
	}
	matchedIdx := -1
	for i, h := range hashes {
		if Sha256Equal(h, presented) {
			matchedIdx = i
			break
		}
	}
	if matchedIdx < 0 {
		return false, false, nil
	}
	return true, len(hashes) == 1, nil
}

// SetMFASecret writes the sealed secret + recovery-code hashes
// WITHOUT stamping mfa_enrolled_at. Idempotent re-enrollment:
// overwrites any prior state. The bytea[] round-trip goes through
// pgx's native [][]byte codec (the same one used by 00016 for
// app_secrets.acl_groups), so no pq.Array wrapper is needed.
func (s *PgStore) ReadMFASecret(ctx context.Context, id string) ([]byte, error) {
	var secret []byte
	row := s.pool.QueryRow(ctx,
		`select mfa_secret_encrypted
		   from accounts where id = $1`, id)
	if err := row.Scan(&secret); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, mapErr(err)
	}
	if secret == nil {
		return nil, ErrNotFound
	}
	return secret, nil
}

func (s *PgStore) SetMFASecret(ctx context.Context, id string, encrypted []byte, recoveryHashes [][]byte) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts
		    set mfa_secret_encrypted = $2,
		        mfa_recovery_codes_hash = $3
		  where id = $1`,
		id, encrypted, recoveryHashes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkMFAEnrolled stamps mfa_enrolled_at = now() and clears
// mfa_required = false. The two columns flip together so the
// requireMFA middleware sees (MFARequired=false && MFAEnrolled=true)
// the moment the row is visible — the audit Emit fires only after
// this returns nil.
func (s *PgStore) MarkMFAEnrolled(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts
		    set mfa_enrolled_at = now(),
		        mfa_required = false
		  where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearMFA nulls mfa_secret_encrypted, mfa_recovery_codes_hash, and
// mfa_enrolled_at. mfa_required is intentionally untouched so an
// explicit policy remains in force after disable. The audit trail lives
// in the events table (handlers_mfa.go emits the `account.mfa_disabled`
// row before/after this call).
func (s *PgStore) ClearMFA(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update accounts
		    set mfa_secret_encrypted = null,
		        mfa_recovery_codes_hash = null,
		        mfa_enrolled_at = null
		  where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetMFARequired writes the explicit mfa_required policy flag and
// reports whether the row was actually changed. Returns ErrNotFound when the
// account row is missing (UNKNOWN differs from a no-op — a missing
// row is a 404, a no-op write is a 200 with no audit event).
func (s *PgStore) SetMFARequired(ctx context.Context, id string, required bool) (changed bool, err error) {
	tag, err := s.pool.Exec(ctx,
		`update accounts set mfa_required = $2 where id = $1 and mfa_required <> $2`, id, required)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// Either the row is missing OR the value was already what was
		// requested. Distinguish with a follow-up existence check so
		// the handler can 404 the missing case.
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`select exists(select 1 from accounts where id = $1)`, id).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, ErrNotFound
		}
		return false, nil
	}
	return true, nil
}

// CountDeployments returns the total deployment count for the
// account across all apps. The per-app invariant ("scale to one
// deploy per app at a time") is enforced by CreateDeployment's
// same-row supersede; this counter captures the cross-app live
// workload.
//
// Status 'failed' + 'superseded' are excluded: a failed build
// counts as nothing, and a superseded deployment is one that was
// already replaced. Soft-deleted apps are excluded (a.status <>
// 'deleted') so the counter doesn't double-count rows whose owner
// has triggered a GDPR hard-delete or app.Delete. The remaining
// statuses ('pending','building','imaging','live') are the live
// workload the chokepoint cares about. Index-backed via
// apps_account_idx (account_id, status).
func (s *PgStore) CountDeployments(ctx context.Context, id string) (int, error) {
	var n int
	row := s.pool.QueryRow(ctx,
		`select count(*)::int
		   from deployments d
		   join apps a on a.id = d.app_id
		  where a.account_id = $1
		    and a.status <> 'deleted'
		    and d.status not in ('failed','superseded')`, id)
	if err := row.Scan(&n); err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// Sha256Equal compares two byte slices in constant time (one shot
// per compare, length check + crypto/subtle.ConstantTimeCompare).
// The recovery-code store/compare path uses it to avoid leaking the
// matched-index timing. Lives in pgstore.go so the tx-wrapped
// ConsumeRecoveryCode has direct access without an import cycle;
// memstore.go carries its own mirror (the function is also used by
// the in-memory parity tests in pkg/state/memstore_test.go).
func Sha256Equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// --- end MFA -----------------------------------------------------------------

// AccountByProviderCustomerID resolves the account behind a Stripe webhook
// payload. The unique index makes this O(log n); MemStore does it with a
// map.
func (s *PgStore) AccountByProviderCustomerID(ctx context.Context, stripeCustomerID string) (Account, error) {
	row := s.pool.QueryRow(ctx,
		`select id, email, plan, status, coalesce(provider_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required, egress_allowlist_extra
		 from accounts where provider_customer_id = $1`,
		stripeCustomerID)
	return scanAccount(row)
}

// ListAllAccounts returns every account. Meterd walks this on the quota
// tick + hourly Stripe push; bounded by the customer count on the box.
func (s *PgStore) ListAllAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`select id, email, plan, status, coalesce(provider_customer_id,''), coalesce(stripe_subscription_item,''), created_at, deletion_requested_at, last_quota_warning_at, past_due_at, mfa_enrolled_at, mfa_secret_encrypted, mfa_recovery_codes_hash, mfa_required, egress_allowlist_extra
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
// past_due_at, mfa_enrolled_at, and mfa_secret_encrypted are nullable;
// we scan into *time.Time / *[]byte and lift them onto the Account
// when non-NULL. The four mfa_* fields are projected by every SELECT
// (CreateAccount, AccountByID/Email/KeyHash/ProviderCustomerID, and
// ListAllAccounts) so the account policy + requireMFA middleware
// always see the post-enrollment state — Review Finding #1 fix.
//
// egress_allowlist_extra (issue #679 / PR-B / ADR-082) is the
// per-account additive budget on top of the plan's
// apps.egress_allowlist cap. NOT NULL DEFAULT 0, so the scan
// uses an int (no *int): the column is never NULL. The
// validator at cmd/apid/handlers_ext.go:104 reads this value
// off the loaded Account; the apid admin handler is the only
// writer.
func scanAccountCols(scan func(...any) error) (Account, error) {
	a := Account{}
	var planStr, statusStr string
	var deletionAt, lastWarnAt, pastDueAt *time.Time
	var mfaEnrolledAt *time.Time
	if err := scan(&a.ID, &a.Email, &planStr, &statusStr, &a.ProviderCustomerID, &a.StripeSubscriptionItem, &a.CreatedAt, &deletionAt, &lastWarnAt, &pastDueAt, &mfaEnrolledAt, &a.MFASecretEncrypted, &a.MFARecoveryCodesHash, &a.MFARequired, &a.EgressAllowlistExtra); err != nil {
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
	if mfaEnrolledAt != nil {
		a.MFAEnrolledAt = mfaEnrolledAt
	}
	return a, nil
}

// --- api keys ----------------------------------------------------------------

func (s *PgStore) CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string, scopes []string) (APIKey, error) {
	// IAM-5: the additive migration adds expires_at + status +
	// revoked_at + rotated_from_id. The five-arg CreateAPIKey
	// signature is preserved (17+ callers in cmd/apid/*_test.go
	// and cmd/apid/handlers_ext.go); the new columns default to
	// NULL / 'active' / NULL / NULL. Production handlers use
	// CreateAPIKeyWithExpiry (added below) so the dashboard
	// sees expires_at on every fresh non-admin key.
	//
	// PR 6 (issue #190 / IAM-6) extends the INSERT to stamp
	// org_id from the account's personal org via a subquery.
	// The partial unique orgs_one_personal_per_account_uniq
	// (migration 00099) guarantees the subquery returns at most
	// one row. The subquery is the SQL-level equivalent of the
	// caller passing orgID = personal org; the seven legacy
	// account-scoped callers (CLI login, dev seed, e2e
	// fixtures) don't have an active-org hint but the column is
	// NOT NULL post-migration 00127, so the store resolves the
	// personal org id on the caller's behalf. PR 9 (ADR-061
	// §C) plans to drop the legacy `account_id` column at the
	// end of the dual-write window.
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label, scopes, org_id)
		 values ($1, $2, $3, $4,
		         (select id from orgs
		            where personal_owner_account_id = $1
		              and personal_org = true
		            limit 1))
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, hash, nullString(label), scopes)
	return scanAPIKey(row)
}

// CreateAPIKeyWithExpiry is the IAM-5 (issue #189) shape. expiresAt
// may be nil for the "never expires" admin contract; non-nil sets
// the column directly. The five-arg CreateAPIKey stays for the
// 17+ existing test/handler call sites that don't care about
// expiry; production apid.createKey uses this new shape. The
// signature is the same as CreateAPIKey plus one *time.Time so
// a Go caller can pass nil for "never expires" without needing
// a separate bool.
func (s *PgStore) CreateAPIKeyWithExpiry(ctx context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error) {
	// PR 6 (issue #190 / IAM-6): org_id stamped from the caller's
	// personal org (see CreateAPIKey docstring for the rationale;
	// the SQL subquery mirrors migration 00099's partial unique).
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label, scopes, expires_at, org_id)
		 values ($1, $2, $3, $4, $5,
		         (select id from orgs
		            where personal_owner_account_id = $1
		              and personal_org = true
		            limit 1))
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, hash, nullString(label), scopes, nullableTimestamptzPtr(expiresAt))
	return scanAPIKey(row)
}

// CreateAPIKeyWithExpiryAndProvenance is the IAM-hardening-mega-PR
// (logical change 2) variant. Identical to CreateAPIKeyWithExpiry
// but adds the three optional provenance columns. Used by the
// legacy /v1/keys POST handler's fallback path (no personal org
// yet — pre-00127 fixtures).
func (s *PgStore) CreateAPIKeyWithExpiryAndProvenance(ctx context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label, scopes, expires_at, org_id, created_ip, created_ua, parent_key_id)
		 values ($1, $2, $3, $4, $5,
		         (select id from orgs
		            where personal_owner_account_id = $1
		              and personal_org = true
		            limit 1),
		         $6, $7, $8)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, hash, nullString(label), scopes, nullableTimestamptzPtr(expiresAt),
		nullString(createdIP), nullString(createdUA), parent)
	return scanAPIKey(row)
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

// DeleteAPIKeyReturning deletes the key and returns the row in a
// single DELETE ... RETURNING statement (IAM-1, ADR-034 rev2). The
// handler uses the returned row's Scopes to emit a key.deleted audit
// event so an operator investigating "what just got revoked?" can see
// the dismissed permission set without re-deriving it from logs.
// Returns ErrNotFound when no matching key exists (account mismatch
// is indistinguishable from a missing id, matching DeleteAPIKey).
func (s *PgStore) DeleteAPIKeyReturning(ctx context.Context, accountID, keyID string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`delete from api_keys where id = $1 and account_id = $2
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		keyID, accountID)
	return scanAPIKey(row)
}

func (s *PgStore) ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		 from api_keys where account_id = $1 order by created_at desc`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetAPIKey returns a single api_keys row by (accountID, keyID).
// Cross-account reads collapse to ErrNotFound at the SQL level — the
// WHERE pins both account_id and id so a probe with a foreign key id
// returns the same ErrNotFound as a missing row. Used by the legacy
// rotateKey (PR 6 dual-write) to discover the old row's org_id before
// the rotation. Returns ErrNotFound when no matching row exists.
//
// Issue #190 / IAM-6, PR 6.
func (s *PgStore) GetAPIKey(ctx context.Context, accountID, keyID string) (APIKey, error) {
	return scanAPIKey(s.pool.QueryRow(ctx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		   from api_keys where account_id = $1 and id = $2`,
		accountID, keyID))
}

func (s *PgStore) TouchKeyLastUsed(ctx context.Context, keyID string) error {
	_, err := s.pool.Exec(ctx, `update api_keys set last_used_at = now() where id = $1`, keyID)
	return err
}

// CountAPIKeys returns the number of non-revoked keys for the
// account. Matches the partial index api_keys_active_grace_idx
// (status IN ('active','grace')) so the query is O(1) per account
// in the common case. Used by create + rotate handlers to enforce
// limits.KeysMax BEFORE minting a new key. Returns 0 for a fresh
// account.
//
// Issue #189 / IAM-5.
func (s *PgStore) CountAPIKeys(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from api_keys
		 where account_id = $1 and status in ('active','grace')`,
		accountID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// MarkAPIKeyRevoked is the IAM-5 (issue #189) soft-delete path.
// Flips status to 'revoked' and stamps revoked_at IF NOT ALREADY
// SET — repeated calls are idempotent (returns the same row, no
// error). Returns ErrNotFound when the key doesn't exist or
// belongs to a different account. Audit emission is the caller's
// responsibility.
func (s *PgStore) MarkAPIKeyRevoked(ctx context.Context, accountID, keyID string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`update api_keys
		    set status = 'revoked',
		        revoked_at = coalesce(revoked_at, now())
		  where id = $1 and account_id = $2
		  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		            coalesce(last_used_at, 'epoch'::timestamptz),
		            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		keyID, accountID)
	return scanAPIKey(row)
}

// RotateAPIKey atomically mints a new key (status='active') and
// demotes the old key in a single transaction. The new key's
// hash is the caller-supplied plaintext-hash (no placeholder +
// post-step patch). The old key's expires_at is OVERWRITTEN to
// now() + graceWindow (atomic when graceWindow == 0 means
// status flips to 'revoked' and revoked_at = now()).
//
// The two returned rows are (newKey, oldKey) in that order. The
// caller surfaces newKey.plaintext (generated upstream) and
// oldKey.ExpiresAt (the grace deadline) in the API response.
//
// The transaction is a single CTE that locks the old row
// FOR UPDATE, inserts the new row from the locked data, and
// updates the old row in one statement. The two RETURNING
// projections are stitched together with a discriminator column
// and split in Go by reading 'which' first.
//
// Errors:
//   - ErrNotFound          — old key doesn't exist or wrong account.
//   - ErrAPIKeyRevoked     — old key is already in 'revoked' state.
//
// Issue #189 / IAM-5.
func (s *PgStore) RotateAPIKey(ctx context.Context, accountID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort on early return

	// Step 1: lock the old row + verify account + status.
	var (
		oldAcct string
		oldStat string
	)
	if err := tx.QueryRow(ctx,
		`select account_id, status from api_keys where id = $1 for update`, oldKeyID).
		Scan(&oldAcct, &oldStat); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, APIKey{}, ErrNotFound
		}
		return APIKey{}, APIKey{}, err
	}
	if oldAcct != accountID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if oldStat == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}

	// Step 2: read the old row's content (label, scopes) so the
	// new key inherits them. We re-read with FOR UPDATE so the
	// projection is consistent with the lock above.
	old, err := scanAPIKeyRow(ctx, tx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		   from api_keys where id = $1`, oldKeyID)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 3: insert the new key. The new label defaults to the
	// old label if the caller passed "" (the handler is the
	// single caller and always supplies the old label).
	// PR 6 (issue #190 / IAM-6): org_id is inherited from the
	// predecessor. The rotation is org-local: a key with
	// org_id = A rotated in place stays org_id = A on the new
	// row. Revoking old + handing the customer a new plaintext
	// is the only contract; replacing the binding would
	// silently leak key material across orgs.
	if newLabel == "" {
		newLabel = old.Label
	}
	newKey, err := scanAPIKeyRow(ctx, tx,
		`insert into api_keys (account_id, key_sha256, label, scopes, status, rotated_from_id, org_id)
		 values ($1, $2, $3, $4, 'active', $5, $6)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, newHash, newLabel, old.Scopes, oldKeyID, old.OrgID)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 4: update the old key. expires_at is overwritten to
	// the grace deadline regardless of its prior value (the
	// issue is explicit: the grace period IS the new expires_at
	// for the old key). status flips per the graceWindow branch.
	if graceWindow == 0 {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'revoked',
			        expires_at = now(),
			        revoked_at = coalesce(revoked_at, now())
			  where id = $1
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	} else {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'grace',
			        expires_at = now() + ($1)::interval
			  where id = $2
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			graceWindow.String(), oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, APIKey{}, err
	}

	return newKey, old, nil
}

// GetAccountKeyGraceWindow returns the per-account override
// (accounts.key_grace_window_days). nil means "no override";
// the caller falls through to the plan default. The auth hot
// path does NOT call this — only the rotate handler does, via
// a short-TTL in-process cache (cmd/apid.graceWindowCache).
//
// Issue #189 / IAM-5.
func (s *PgStore) GetAccountKeyGraceWindow(ctx context.Context, accountID string) (*int, error) {
	var n *int
	err := s.pool.QueryRow(ctx,
		`select key_grace_window_days from accounts where id = $1`, accountID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return n, nil
}

// SetAccountKeyGraceWindow sets the per-account override. days
// == nil clears the override (column → NULL). The handler
// invalidates the in-process graceWindowCache entry after a
// successful write. The audit `key.grace_window_set` event is
// emitted by the handler, not the store.
//
// Issue #189 / IAM-5.
func (s *PgStore) SetAccountKeyGraceWindow(ctx context.Context, accountID string, days *int) error {
	_, err := s.pool.Exec(ctx,
		`update accounts set key_grace_window_days = $1 where id = $2`, days, accountID)
	return err
}

// GetAccountEgressAllowlistExtra returns the per-account
// additive budget on top of the plan's apps.egress_allowlist
// cap (issue #679 / PR-B / ADR-082). 0 = no override; the plan
// cap is authoritative. The validator at
// cmd/apid/handlers_ext.go:104 adds this value to the plan cap
// before the >-maxSize check. The DB CHECK constraint
// (egress_allowlist_extra >= 0) defends against wire-bypasses
// that skip the apid gate.
func (s *PgStore) GetAccountEgressAllowlistExtra(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select egress_allowlist_extra from accounts where id = $1`, accountID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return n, nil
}

// SetAccountEgressAllowlistExtra sets the per-account additive
// budget. n == 0 falls through to the plan cap. The audit
// `account.egress_allowlist_extra_set` event is emitted by the
// handler, not the store. The DB CHECK constraint
// (egress_allowlist_extra >= 0) is the wire-bypass backstop;
// the apid handler enforces the + 1024 ceiling before the
// call lands here.
//
// Issue #679 / PR-B / ADR-082.
func (s *PgStore) SetAccountEgressAllowlistExtra(ctx context.Context, accountID string, n int) error {
	_, err := s.pool.Exec(ctx,
		`update accounts set egress_allowlist_extra = $1 where id = $2`, n, accountID)
	return err
}

// --- org-bound api keys (issue #190 / IAM-6, PR 6) ---

// CreateOrgAPIKey persists a key with an explicit org binding.
// The caller supplies both orgID (principal.Membership.OrgID
// from loadOrg) and accountID (the resolved membership's account
// owner) so the INSERT is single-statement and the NOT NULL
// constraint on api_keys.org_id (migration 00127) is satisfied
// without a subquery. The returning projection reads the same
// 12-column shape scanAPIKey consumes — the column is at index
// 3, between account_id and key_sha256, so all five new
// methods can share scanAPIKey / scanAPIKeyRow without a
// bespoke scanner.
func (s *PgStore) CreateOrgAPIKey(ctx context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label, scopes, expires_at, org_id)
		 values ($1, $2, $3, $4, $5, $6)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, hash, nullString(label), scopes, nullableTimestamptzPtr(expiresAt), orgID)
	return scanAPIKey(row)
}

// CreateOrgAPIKeyWithProvenance is the IAM-hardening-mega-PR
// (logical change 2) variant. Three optional columns land on the
// new row: created_ip (inet), created_ua (text), parent_key_id
// (uuid FK). NULL inputs stamp NULL columns; the optional parent
// is the provenance lineage (distinct from rotated_from_id which
// is the rotation-internal stamp).
func (s *PgStore) CreateOrgAPIKeyWithProvenance(ctx context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`insert into api_keys (account_id, key_sha256, label, scopes, expires_at, org_id, created_ip, created_ua, parent_key_id)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		accountID, hash, nullString(label), scopes, nullableTimestamptzPtr(expiresAt), orgID,
		nullString(createdIP), nullString(createdUA), parent)
	return scanAPIKey(row)
}

// ListOrgAPIKeys returns every non-revoked key for the org.
// "Non-revoked" = status IN ('active', 'grace'); the partial
// index api_keys_active_grace_idx keeps the common case O(1) —
// the plan's org_id-NOT NULL flip forces the index to be hit
// for any non-pathological key query.
func (s *PgStore) ListOrgAPIKeys(ctx context.Context, orgID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		 from api_keys
		 where org_id = $1
		   and status in ('active','grace')
		 order by created_at desc`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetOrgAPIKey fetches a single key by id, scoped to orgID. The
// WHERE clause pins (id, org_id) so a key that exists but
// belongs to a different org returns ErrNotFound at the SQL
// layer — the IDOR-safe collapse (matches DeleteAPIKeyReturning's
// (accountID, keyID) predicate).
func (s *PgStore) GetOrgAPIKey(ctx context.Context, orgID, keyID string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		 from api_keys
		 where id = $1 and org_id = $2`,
		keyID, orgID)
	return scanAPIKey(row)
}

// RevokeOrgAPIKey is the org-scoped soft-delete path. Same
// idempotent contract as MarkAPIKeyRevoked but the WHERE clause
// pins (id, org_id) so a cross-org probe returns ErrNotFound.
// Returns the post-update row so the handler can stamp the
// audit `api_key.revoked` event with the dismissed scopes.
func (s *PgStore) RevokeOrgAPIKey(ctx context.Context, orgID, keyID string) (APIKey, error) {
	row := s.pool.QueryRow(ctx,
		`update api_keys
		    set status = 'revoked',
		        revoked_at = coalesce(revoked_at, now())
		  where id = $1 and org_id = $2
		  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		            coalesce(last_used_at, 'epoch'::timestamptz),
		            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		keyID, orgID)
	return scanAPIKey(row)
}

// RotateOrgAPIKey mirrors RotateAPIKey but the lock predicate
// is (id, org_id). The new row's org_id is inherited from the
// old row (a rotation is org-local — the customer's binding to
// the active org stays; replacing it would silently leak key
// material across orgs). Two returned keys are (newKey, oldKey)
// in that order, matching RotateAPIKey.
func (s *PgStore) RotateOrgAPIKey(ctx context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort on early return

	// Step 1: lock the old row + verify (id, org_id) + status.
	var (
		oldOrg  string
		oldStat string
	)
	if err := tx.QueryRow(ctx,
		`select org_id, status from api_keys where id = $1 for update`, oldKeyID).
		Scan(&oldOrg, &oldStat); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, APIKey{}, ErrNotFound
		}
		return APIKey{}, APIKey{}, err
	}
	if oldOrg != orgID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if oldStat == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}

	// Step 2: read the old row's content (label, scopes, account_id) so the
	// new key inherits them.
	old, err := scanAPIKeyRow(ctx, tx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		   from api_keys where id = $1`, oldKeyID)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 3: insert the new key. account_id + org_id are inherited
	// from the predecessor; rotation is org-local.
	if newLabel == "" {
		newLabel = old.Label
	}
	newKey, err := scanAPIKeyRow(ctx, tx,
		`insert into api_keys (account_id, key_sha256, label, scopes, status, rotated_from_id, org_id)
		 values ($1, $2, $3, $4, 'active', $5, $6)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		old.AccountID, newHash, newLabel, old.Scopes, oldKeyID, old.OrgID)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 4: update the old key. expires_at is overwritten to
	// the grace deadline (same graceWindow semantics as the
	// legacy RotateAPIKey).
	if graceWindow == 0 {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'revoked',
			        expires_at = now(),
			        revoked_at = coalesce(revoked_at, now())
			  where id = $1
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	} else {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'grace',
			        expires_at = now() + ($1)::interval
			  where id = $2
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
		            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			graceWindow.String(), oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, APIKey{}, err
	}
	return newKey, old, nil
}

// RotateOrgAPIKeyWithProvenance is the IAM-hardening-mega-PR
// (logical change 2) variant of RotateOrgAPIKey. The transaction
// shape is identical; the new row's INSERT additionally stamps
// created_ip / created_ua / parent_key_id from the caller's
// request. The rotated_from_id column is unchanged (still set
// to oldKeyID, the rotation-internal predecessor stamp).
func (s *PgStore) RotateOrgAPIKeyWithProvenance(ctx context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration, createdIP, createdUA string, parent *string) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // best-effort on early return

	// Step 1: lock the old row + verify (id, org_id) + status.
	var (
		oldOrg  string
		oldStat string
	)
	if err := tx.QueryRow(ctx,
		`select org_id, status from api_keys where id = $1 for update`, oldKeyID).
		Scan(&oldOrg, &oldStat); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, APIKey{}, ErrNotFound
		}
		return APIKey{}, APIKey{}, err
	}
	if oldOrg != orgID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if oldStat == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}

	// Step 2: read the old row's content (label, scopes, account_id)
	old, err := scanAPIKeyRow(ctx, tx,
		`select id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		        coalesce(last_used_at, 'epoch'::timestamptz),
		        expires_at, status, revoked_at, rotated_from_id,
		        coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id
		   from api_keys where id = $1`, oldKeyID)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 3: insert the new key with provenance.
	if newLabel == "" {
		newLabel = old.Label
	}
	newKey, err := scanAPIKeyRow(ctx, tx,
		`insert into api_keys (account_id, key_sha256, label, scopes, status, rotated_from_id, org_id, created_ip, created_ua, parent_key_id)
		 values ($1, $2, $3, $4, 'active', $5, $6, $7, $8, $9)
		 returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
		           coalesce(last_used_at, 'epoch'::timestamptz),
		           expires_at, status, revoked_at, rotated_from_id,
		           coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
		old.AccountID, newHash, newLabel, old.Scopes, oldKeyID, old.OrgID,
		nullString(createdIP), nullString(createdUA), parent)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Step 4: update the old key. expires_at is overwritten to the grace deadline.
	if graceWindow == 0 {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'revoked',
			        expires_at = now(),
			        revoked_at = coalesce(revoked_at, now())
			  where id = $1
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
			            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	} else {
		old, err = scanAPIKeyRow(ctx, tx,
			`update api_keys
			    set status = 'grace',
			        expires_at = now() + ($1)::interval
			  where id = $2
			  returning id, account_id, org_id, key_sha256, coalesce(label,''), scopes, created_at,
			            coalesce(last_used_at, 'epoch'::timestamptz),
			            expires_at, status, revoked_at, rotated_from_id,
			            coalesce(host(created_ip),'') as created_ip, coalesce(created_ua,'') as created_ua, parent_key_id`,
			graceWindow.String(), oldKeyID)
		if err != nil {
			return APIKey{}, APIKey{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, APIKey{}, err
	}
	return newKey, old, nil
}

// --- apps --------------------------------------------------------------------

func (s *PgStore) CreateApp(ctx context.Context, app App) (App, error) {
	// ADR-093 / PR-E: DB call seam — propagate the inbound budget
	// with a 10 ms overhead reservation. Hot-path Store methods
	// follow this pattern; the wrapper is a no-op when no Budget
	// is attached.
	ctx = db.WithBudget(ctx)
	manifest := app.Manifest
	if manifest.IsZero() {
		manifest = AppManifest{}
	}
	manifestBytes, _ := json.Marshal(manifest)
	runtime := nullString(app.Runtime)
	idle := nullableInt(app.IdleTimeoutS)
	// type: NOT NULL CHECK (type IN ('app','function')) with DEFAULT 'app'.
	// DEFAULT is bypassed because we pass the column explicitly; empty Go
	// string would trip the CHECK. Coerce "" to AppTypeApp so the
	// default's value is preserved whenever the caller hasn't picked.
	// maxConcurrency: NOT NULL DEFAULT 1 CHECK (>= 1). Coerce <= 0 to 1.
	// ramMB: NOT NULL CHECK (ram_mb > 0). Coerce <= 0 to 128 (Free plan
	// minimum, pkg/api/limits.go:242) — the smallest legal value the
	// column accepts.
	// project_id + workload_name (migration 00074). project_id is nullable
	// (empty → NULL via nullString); workload_name is NOT NULL DEFAULT ''
	// so empty stays as ''. Together with the unique index
	// apps_project_workload_uniq (project_id, workload_name) WHERE
	// project_id IS NOT NULL, a project-bound insert must carry both
	// columns and a non-project insert lands with (NULL, '') which the
	// index filters out.
	// node_id (migration 00090, Phase 2 / Gate A): nullable FK to
	// compute_nodes(id) + empty-uuid CHECK (migration 00091 relaxed
	// NOT NULL → nullable so schedd's PlacementClaimSubscriber can
	// stamp the owner asynchronously — pkg/sched/placement_claim.go).
	// apid inserts with node_id = NULL and emits NotifyAppChanged
	// "created"; every schedd races to claim via Store.SetAppNodeID,
	// whose UPDATE … WHERE node_id IS NULL serialises N schedds into
	// exactly one winner. The empty-uuid CHECK stays in force; the
	// pgx driver passes nil for an empty string here so the column
	// defaults to NULL (no 22P02 invalid_text_representation — pgx
	// understands the nilable interface). A future apid bug that
	// tries to bind a literal zero-uuid still trips 23514.
	appType := app.Type
	if appType == "" {
		appType = AppTypeApp
	}
	maxConcurrency := app.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	// ramMB is NOT NULL with CHECK (ram_mb > 0) (migrations/00001_init.sql:36).
	// Callers that leave it at the Go int zero (no plan resolution / no
	// explicit mem cap set) would trip the CHECK if passed as-is; coerce
	// <= 0 to 128 — the Free plan minimum from pkg/api/limits.go:242 —
	// the smallest legal value the column accepts.
	ramMB := app.RAMMB
	if ramMB <= 0 {
		ramMB = 128
	}
	// warm_snapshot_min_requests / warm_snapshot_min_ms have CHECK bounds
	// (1..100 / 100..60000) added by migration 00109. A caller that
	// leaves them at the Go int zero trips the CHECK at insert time —
	// mirror the ramMB / maxConcurrency floors above and clamp to the
	// smallest legal value (1 / 100). apid still applies the plan-gated
	// default from pkg/api/limits.go at create time, so production code
	// always arrives with non-zero values; the floor is purely defensive
	// for tests and internal callers that build an App struct by hand.
	warmMinRequests := app.WarmSnapshotMinRequests
	if warmMinRequests <= 0 {
		warmMinRequests = 1
	}
	warmMinMs := app.WarmSnapshotMinMs
	if warmMinMs <= 0 {
		warmMinMs = 100
	}
	// Issue #470 / ADR-055 + issue #533 / ADR-066: warm_snapshot_* values
	// arrive populated on the App struct from apid (which applies the
	// plan-gated default from pkg/api/limits.go). The SQL CHECK bounds
	// are enforced both at the column layer and at the apid handler;
	// the Go floor above is the last-line defence for tests / internal
	// callers that build an App by hand with the int zero. node_id
	// (migration 00090, Phase 2 / Gate A) is the durable shard key.
	//
	// root_dir (migration 00074): NOT NULL DEFAULT '' but written
	// explicitly here because the apply / reconcile path needs the
	// convention detector's (RootDir, Name) tuple to round-trip.
	// The schema DEFAULT would yield '' for the convention
	// workload and merge it with a compose workload of the same
	// slug on re-apply, tripping apps_slug_key. See ADR-068
	// amendment for the diff path that depends on this.
	// Issue #475: eviction_priority is NOT NULL DEFAULT 'best_effort'
	// (migration 00135). The Go zero-value "" is NOT in the CHECK set
	// (apps_eviction_priority_chk) — snap to 'best_effort' via the
	// shared helper so the pre-#475 create path keeps the schema
	// DEFAULT behaviour bit-for-bit. apid still applies the per-plan
	// gate (Plan.EvictionPriorityReservedAllowed) at create time for
	// explicit 'reserved' values.
	evictionPriority := EvictionPriorityOrBestEffort(app.EvictionPriority)
	// Issue #695 / ADR-080: public_auth_mode is included in the column
	// list so the App struct's value is written verbatim. Pre-#695 the
	// schema default ('open') shadowed any value the caller passed,
	// which broke the per-plan default path on Pro/Scale (default
	// 'bearer' was overwritten back to 'open' on insert). Same shape
	// for both CreateApp and CreateAppIfUnderQuota below.
	//
	// Issue #676 / PR-3: websocket_enabled is also written explicitly
	// (same Set-bit-aware shape) so the per-plan default doesn't get
	// shadowed by the schema DEFAULT.
	//
	// ADR-093: route_metrics_enabled is written explicitly (same
	// shape) so the per-plan default doesn't get shadowed by the
	// schema DEFAULT. The CreateApp site is the only place the
	// column is written at create time — there's no separate
	// CreateAppIfUnderQuota path to keep in sync because the
	// explicit per-plan default is applied by apid before
	// reaching this path.
	//
	// Tier A10 / ADR-088: overflow_node preference is in the
	// column list so the App struct's value is written verbatim
	// at create time. apid resolved the wire name → UUID
	// server-side via Store.ComputeNodeByName before reaching
	// this path; the store is a plain write. NULL preference
	// (the A9 default fallback) round-trips via nullString
	// ("" → SQL NULL). The empty-uuid CHECK + the FK with
	// ON DELETE SET NULL (migration 00167) enforce the
	// integrity contract downstream.
	insertAppSQL := `insert into apps (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest, min_instances, egress_allowlist, public_auth_ip_allowlist, streaming_enabled, project_id, root_dir, workload_name, node_id, warm_snapshot_enabled, warm_snapshot_min_requests, warm_snapshot_min_ms, eviction_priority, require_authn, public_auth_mode, websocket_enabled, route_metrics_enabled, overflow_node, preview_of_slug, preview_pr_number, preview_pr_state, preview_expires_at, preview_destroy_commented_at, maintenance_mode, app_protocol)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11::cidr[], $12::cidr[], $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33)
		returning ` + appsSelectColumns
	// status: pull from app.Status when non-empty (the API surfaces it on
	// update / restore paths); fall back to 'active' on the Go zero so the
	// create path keeps the schema DEFAULT behaviour. The column is NOT
	// NULL with a CHECK (status IN ('active','evicted_cold','deleted')),
	// so the empty-string fallback would trip 23514 — coerce to AppActive.
	statusValue := app.Status
	if statusValue == "" {
		statusValue = AppActive
	}
	// Coerce an empty PublicAuthMode to AppPublicAuthModeOpen so the
	// NOT NULL CHECK (public_auth_mode IN ('open','bearer','basic'))
	// is satisfied. Mirrors the Type=="" / Status=="" floor above;
	// apid always stamps the per-plan default before reaching this
	// path, so the floor is a last-line defence for internal callers
	// that build an App by hand.
	publicAuthMode := app.PublicAuthMode
	if publicAuthMode == "" {
		publicAuthMode = AppPublicAuthModeOpen
	}
	// Coerce an empty AppProtocol to 'http1' so the column
	// default and the explicit write converge on the same
	// universal default. The closed-set CHECK
	// apps_app_protocol_chk rejects empty strings — without
	// this floor, a hand-built App{} would trip 23514.
	appProtocol := app.AppProtocol
	if appProtocol == "" {
		appProtocol = api.AppProtocolHTTP1
	}
	row := s.pool.QueryRow(ctx, insertAppSQL,
		app.AccountID, app.Slug, string(appType), runtime, ramMB, idle, maxConcurrency, string(statusValue), manifestBytes, app.MinInstances, cidrPrefixesToArray(app.EgressAllowlist), cidrPrefixesToArray(app.PublicAuthIPAllowlist), app.StreamingEnabled, nullString(app.ProjectID), app.RootDir, app.WorkloadName, nullString(app.NodeID),
		app.WarmSnapshotEnabled, warmMinRequests, warmMinMs, evictionPriority, app.RequireAuthn, publicAuthMode, app.WebSocketEnabled, app.RouteMetricsEnabled,
		// Tier A10 / ADR-088: overflow_node preference (nullable
		// UUID). nullString coerces a nil pointer or empty
		// string to SQL NULL; Postgres infers the UUID type
		// from the column, same as NodeID above.
		nullString(derefString(app.OverflowNode)),
		// Issue #272 / ADR-094: per-app preview metadata. Empty
		// strings + zero ints + nil time all land as SQL NULL
		// via the existing nullString / nullable helpers — the
		// create path is the production path, and production
		// apps never carry preview metadata. The bind site is
		// the canonical "all four columns are NULL" producer.
		nullString(app.PreviewOfSlug), app.PreviewPrNumber,
		nullString(app.PreviewPrState), nullableTimestamptzPtr(app.PreviewExpiresAt),
		// Mega-C PR-1 / issue #961 leaf 3: dedupe carrier.
		// nullableTimestamptzPtr coerces a nil *time.Time to SQL
		// NULL, which is the correct shape for both production
		// rows (never commented) and freshly-provisioned
		// preview rows (comment post happens AFTER CreateApp).
		nullableTimestamptzPtr(app.PreviewDestroyCommentedAt),
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag (apps.maintenance_mode). Written
		// explicitly so the App struct's value (default false)
		// round-trips through CREATE — the schema DEFAULT also
		// evaluates to false, but the explicit write is the
		// established convention (websocket_enabled,
		// route_metrics_enabled) and matches the column-list
		// shape above. Mirrors the Set-bit-aware contract used
		// in the PATCH path (handler_ext.go).
		app.MaintenanceMode,
		// ADR-124: per-app wire-protocol selector
		// (apps.app_protocol). Empty-string App.AppProtocol is
		// coerced to 'http1' so the schema DEFAULT and the
		// explicit-write path converge on the same universal
		// default. apid always stamps the per-plan default
		// before reaching this path, so the floor is a
		// last-line defence for internal callers that build an
		// App by hand.
		appProtocol)
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
	if manifest.IsZero() {
		manifest = AppManifest{}
	}
	manifestBytes, _ := json.Marshal(manifest)
	runtime := nullString(app.Runtime)
	idle := nullableInt(app.IdleTimeoutS)
	// Coerce MaxConcurrency <= 0 to 1 so the NOT NULL CHECK (>= 1) is
	// satisfied (matches the CreateApp / ApplyProjectPlan paths).
	maxConcurrency := app.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	// Coerce RAMMB <= 0 to 128 (Free plan minimum, pkg/api/limits.go:242)
	// so the NOT NULL CHECK (ram_mb > 0) is satisfied (matches CreateApp).
	ramMB := app.RAMMB
	if ramMB <= 0 {
		ramMB = 128
	}
	// Issue #470 / ADR-055: warm_snapshot_min_* have CHECK bounds (1..100 /
	// 100..60000) added by migration 00109. Mirror the ramMB floor above
	// so a zero-value App struct (test fixtures, internal callers) lands
	// inside the bound instead of tripping the CHECK at insert time.
	warmMinRequests := app.WarmSnapshotMinRequests
	if warmMinRequests <= 0 {
		warmMinRequests = 1
	}
	warmMinMs := app.WarmSnapshotMinMs
	if warmMinMs <= 0 {
		warmMinMs = 100
	}
	// Coerce Type=="" to AppTypeApp so the NOT NULL CHECK
	// (type IN ('app','function')) is satisfied (matches CreateApp).
	appType := app.Type
	if appType == "" {
		appType = AppTypeApp
	}
	// project_id + workload_name are added by migration 00074. The
	// reconcile path passes project-bound Apps and AppsForProject
	// filters by project_id; the unique index apps_project_workload_uniq
	// (project_id, workload_name) WHERE project_id IS NOT NULL means
	// every project-bound insert must carry both columns or it
	// collides with the prior row. project_id is nullable (empty →
	// NULL via nullString); workload_name is NOT NULL DEFAULT '' so
	// the empty string stays as '' (matches the v1 default).
	// node_id (migration 00090, Phase 2 / Gate A): nullable FK +
	// empty-uuid CHECK (migration 00091 relaxed NOT NULL so schedd's
	// PlacementClaimSubscriber can stamp the owner asynchronously).
	// See CreateApp for the post-00091 contract — pgx passes nil for
	// the empty string so the column defaults to NULL.
	// Issue #470 / ADR-055: same warm_snapshot_* projection as CreateApp —
	// the column default would write false/5/2000 for an unset caller,
	// but apid always populates the App struct with the plan-gated
	// defaults from pkg/api/limits.go before reaching either insert path;
	// the Go floor above is the last-line defence for tests / internal
	// callers that build an App by hand with the int zero.
	//
	// root_dir (migration 00074): same rationale as CreateApp —
	// schema DEFAULT '' but written explicitly so the
	// (RootDir, WorkloadName) tuple round-trips through the diff
	// path (ADR-068 amendment).
	// Issue #475: eviction_priority is NOT NULL DEFAULT 'best_effort'
	// (migration 00135). Same snap-to-default shape as CreateApp above
	// — the Go zero-value "" is NOT in the CHECK set, so the insert
	// path coerces to 'best_effort' to preserve the pre-#475 create
	// behaviour bit-for-bit.
	evictionPriority := EvictionPriorityOrBestEffort(app.EvictionPriority)
	// Issue #695 / ADR-080: public_auth_mode is in the column list so
	// the App struct's value is written verbatim (same rationale as
	// CreateApp above — schema default 'open' would otherwise shadow
	// the per-plan default).
	//
	// Issue #676 / PR-3: websocket_enabled follows the same shape.
	//
	// ADR-093: route_metrics_enabled follows the same shape; the
	// per-plan default is applied by apid before reaching this
	// path so the App struct's value is authoritative.
	//
	// Tier A10 / ADR-088: overflow_node preference is in the
	// column list so the App struct's value is written verbatim
	// at create time. apid resolved the wire name → UUID
	// server-side via Store.ComputeNodeByName before reaching
	// this path; the store is a plain write. NULL preference
	// (the A9 default fallback) round-trips via nullString
	// ("" → SQL NULL). The empty-uuid CHECK + the FK with
	// ON DELETE SET NULL (migration 00167) enforce the
	// integrity contract downstream.
	insertAppSQL := `insert into apps (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency, status, manifest, min_instances, streaming_enabled, project_id, root_dir, workload_name, node_id, warm_snapshot_enabled, warm_snapshot_min_requests, warm_snapshot_min_ms, eviction_priority, require_authn, public_auth_mode, websocket_enabled, route_metrics_enabled, overflow_node, preview_of_slug, preview_pr_number, preview_pr_state, preview_expires_at, preview_destroy_commented_at, maintenance_mode, app_protocol)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31)
		returning ` + appsSelectColumns
	// status: same fallback as CreateApp above — empty Go Status would
	// trip 23514 on the CHECK constraint, so coerce to AppActive. The
	// column DEFAULT is documented as 'active' but the explicit INSERT
	// would trip the check_violation otherwise.
	statusValue := app.Status
	if statusValue == "" {
		statusValue = AppActive
	}
	// Coerce an empty PublicAuthMode to AppPublicAuthModeOpen so the
	// NOT NULL CHECK is satisfied (mirrors CreateApp above). Apid
	// always stamps the per-plan default before reaching this path.
	publicAuthMode := app.PublicAuthMode
	if publicAuthMode == "" {
		publicAuthMode = AppPublicAuthModeOpen
	}
	// Coerce an empty AppProtocol to 'http1' (mirrors CreateApp
	// above). ADR-124 closed-set CHECK rejects empty strings; the
	// floor is a last-line defence for internal callers that build
	// an App by hand.
	appProtocol := app.AppProtocol
	if appProtocol == "" {
		appProtocol = api.AppProtocolHTTP1
	}
	row := tx.QueryRow(ctx, insertAppSQL,
		app.AccountID, app.Slug, string(appType), runtime, ramMB, idle, maxConcurrency, string(statusValue), manifestBytes, app.MinInstances, app.StreamingEnabled, nullString(app.ProjectID), app.RootDir, app.WorkloadName, nullString(app.NodeID),
		app.WarmSnapshotEnabled, warmMinRequests, warmMinMs, evictionPriority, app.RequireAuthn, publicAuthMode, app.WebSocketEnabled, app.RouteMetricsEnabled,
		// Tier A10 / ADR-088: overflow_node preference (nullable
		// UUID). nullString coerces a nil pointer or empty
		// string to SQL NULL; Postgres infers the UUID type
		// from the column, same as NodeID above.
		nullString(derefString(app.OverflowNode)),
		// Issue #272 / ADR-094: per-app preview metadata. Same
		// NULL-all shape as CreateApp above — production apps
		// (and quota-counted inserts that happen to land via
		// this path) never carry preview metadata.
		nullString(app.PreviewOfSlug), app.PreviewPrNumber,
		nullString(app.PreviewPrState), nullableTimestamptzPtr(app.PreviewExpiresAt),
		// Mega-C PR-1 / issue #961 leaf 3: dedupe carrier.
		// nullableTimestamptzPtr coerces a nil *time.Time to SQL
		// NULL, which is the correct shape for both production
		// rows (never commented) and freshly-provisioned
		// preview rows (comment post happens AFTER CreateApp).
		nullableTimestamptzPtr(app.PreviewDestroyCommentedAt),
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag. Same explicit-write posture as
		// CreateApp above — the schema DEFAULT would yield
		// false but the explicit write matches the
		// websocket_enabled / route_metrics_enabled column-list
		// discipline and the Set-bit-aware PATCH contract.
		app.MaintenanceMode,
		// ADR-124: per-app wire-protocol selector
		// (apps.app_protocol). Empty-string App.AppProtocol is
		// coerced to 'http1' so the schema DEFAULT and the
		// explicit-write path converge on the same universal
		// default. Mirrors the binding in CreateApp above.
		appProtocol)
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
	sel := `select ` + appsSelectColumns + ` from apps where id = $1`
	row := s.pool.QueryRow(ctx, sel, id)
	return scanApp(row)
}

func (s *PgStore) AppBySlug(ctx context.Context, slug string) (App, error) {
	sel := `select ` + appsSelectColumns + ` from apps where slug = $1 and status <> 'deleted'`
	row := s.pool.QueryRow(ctx, sel, slug)
	return scanApp(row)
}

// PreviewAppsByParent (ADR-095 / issue #272) returns every preview
// app whose preview_of_slug = parentSlug, scoped to accountID. The
// query plan uses the partial index apps_preview_of_slug_idx
// (migration 00220), which carries the same WHERE preview_of_slug IS
// NOT NULL predicate. Soft-deleted previews (apps.status = 'deleted')
// are filtered out so the dashboard doesn't render "torn down" rows
// in the live pane — they remain queryable via the janitor's
// tombstone-aware ListPreviewsForTeardown sweep.
//
// The account_id predicate is non-negotiable: a customer should
// never see another customer's preview rows even if the
// preview_of_slug happened to collide (it can't today — slugs are
// globally unique — but defence-in-depth matches the pattern every
// other apps query uses).
func (s *PgStore) PreviewAppsByParent(ctx context.Context, accountID, parentSlug string) ([]App, error) {
	rows, err := s.pool.Query(ctx,
		`select `+appsSelectColumns+` from apps where account_id = $1 and preview_of_slug = $2 and status <> 'deleted' order by created_at desc`,
		accountID, parentSlug)
	if err != nil {
		return nil, fmt.Errorf("state: preview apps by parent %q/%q: %w", accountID, parentSlug, err)
	}
	defer rows.Close()
	return scanApps(rows)
}

// ListPreviewsForAccount (Mega-C PR-1 / issue #961 leaf 3) is the
// global "all my open PRs" view that backs the new
// /dashboard/previews page. Same shape as PreviewAppsByParent but
// no parent_slug filter — returns every non-deleted preview row
// for the account. The query plan uses the same
// apps_preview_of_slug_idx partial index because every row in the
// result set satisfies preview_of_slug IS NOT NULL (production
// apps are filtered out).
//
// Soft-deleted previews are excluded (matching the per-parent
// pane's contract — "torn down" rows live in the janitor's
// sweep, not the customer-facing list). The account_id predicate
// is the same defence-in-depth as PreviewAppsByParent.
func (s *PgStore) ListPreviewsForAccount(ctx context.Context, accountID string) ([]App, error) {
	rows, err := s.pool.Query(ctx,
		`select `+appsSelectColumns+` from apps where account_id = $1 and preview_of_slug is not null and status <> 'deleted' order by created_at desc`,
		accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list previews for account %q: %w", accountID, err)
	}
	defer rows.Close()
	return scanApps(rows)
}

// ListPreviewsForTeardown (ADR-095 PR-C / issue #272) returns the
// preview rows the teardown janitor should consider on this tick.
// See the Store interface docstring for the full contract; the
// load-bearing points restated here because they look like bugs:
//
//  1. No `status <> 'deleted'` filter. The janitor sets that status
//     itself; excluding tombstoned rows would strand any row whose
//     tombstone write landed but whose preview_pr_state write did
//     not (apid crash between the two). The torn_down predicate
//     below is what actually bounds the result set — once a row
//     reaches torn_down it never comes back.
//
//  2. `now` is a parameter, not now(). The janitor owns its clock
//     so tests (and the e2e TTL case) can drive a 7-day expiry
//     without sleeping. Passing the Go-side clock also keeps the
//     sweep deterministic when apid and Postgres disagree by a few
//     hundred ms.
//
// The predicate is an OR of "PR is in a terminal-ish state" and
// "TTL elapsed", which lets the partial index
// apps_preview_expires_at_idx serve the second arm. preview_of_slug
// is not null is what keeps production rows out entirely.
func (s *PgStore) ListPreviewsForTeardown(ctx context.Context, now time.Time, maxPerTick int) ([]App, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	sel := `select ` + appsSelectColumns + `
	          from apps
	         where preview_of_slug is not null
	           and coalesce(preview_pr_state, '') <> $1
	           and (coalesce(preview_pr_state, '') in ($2, $3)
	                or (preview_expires_at is not null and preview_expires_at < $4))
	         order by preview_expires_at asc nulls last
	         limit $5`
	rows, err := s.pool.Query(ctx, sel,
		PreviewPrStateTornDown, PreviewPrStateClosed, PreviewPrStateStale,
		now.UTC(), maxPerTick)
	if err != nil {
		return nil, fmt.Errorf("state: list previews for teardown: %w", err)
	}
	return scanApps(rows)
}

// SetPreviewPrState (ADR-095 PR-C / issue #272) advances one preview
// row's lifecycle label. The `preview_of_slug is not null` predicate
// is a safety interlock, not an optimisation: it means a bug in the
// janitor's sweep can never relabel a customer's production app —
// the UPDATE simply matches zero rows and returns ErrNotFound.
//
// The Go-side PreviewPrStateIsValid check runs first so an invalid
// value surfaces as a wrapped Go error with the offending string,
// rather than as SQLSTATE 23514 from apps_preview_pr_state_chk.
// Both gates are kept: the CHECK is authoritative for any writer,
// this one is for a legible error message.
func (s *PgStore) SetPreviewPrState(ctx context.Context, appID, prState string) (App, error) {
	if !PreviewPrStateIsValid(prState) {
		return App{}, fmt.Errorf("state: set preview pr_state %q for app %q: %w", prState, appID, ErrInvalidPreviewPrState)
	}
	var a App
	row := s.pool.QueryRow(ctx, `
		update apps set preview_pr_state = $2
		where id = $1 and preview_of_slug is not null
		returning `+appsSelectColumns, appID, prState)
	if err := scanAppInto(&a, row); err != nil {
		return App{}, mapErr(err)
	}
	return a, nil
}

// RefreshDevSession renews the lease on a CLI-created developer preview.
// The preview_pr_number=0 guard keeps this path from reopening or extending a
// GitHub PR preview, while the status predicate prevents reviving a row the
// janitor has already tombstoned.
func (s *PgStore) RefreshDevSession(ctx context.Context, appID string, expiresAt time.Time) (App, error) {
	var a App
	row := s.pool.QueryRow(ctx, `
		update apps
		set preview_pr_state = $2, preview_expires_at = $3
		where id = $1
		  and preview_of_slug is not null
		  and coalesce(preview_pr_number, 0) = 0
		  and status <> 'deleted'
		returning `+appsSelectColumns, appID, PreviewPrStateOpen, expiresAt)
	if err := scanAppInto(&a, row); err != nil {
		return App{}, mapErr(err)
	}
	return a, nil
}

// StampPreviewDestroyCommentedAt (Mega-C PR-1 / issue #961 leaf 3)
// records that the one-click PR comment destroy hint was posted
// to GitHub for this preview row. githubd's previewCommentOnce
// helper calls this after every successful POST so a closed →
// reopen → closed cycle does not spam the customer.
//
// The WHERE preview_of_slug IS NOT NULL guard mirrors
// SetPreviewPrState above: a buggy caller cannot stamp a
// production app's dedupe column. Returns ErrNotFound when the
// row is missing OR when the row is a production app — same
// code path because both are zero-rows-updated.
//
// Idempotent: re-stamping the column with the same timestamp is
// a no-op (the column value is the dedupe key, not the row
// identity). The githubd caller's invariant is "stamp exactly
// once per (app, PR) tuple"; the column carries the audit row
// for that single post.
func (s *PgStore) StampPreviewDestroyCommentedAt(ctx context.Context, appID string, when time.Time) (App, error) {
	var a App
	row := s.pool.QueryRow(ctx, `
		update apps set preview_destroy_commented_at = $2
		where id = $1 and preview_of_slug is not null
		returning `+appsSelectColumns, appID, when)
	if err := scanAppInto(&a, row); err != nil {
		return App{}, mapErr(err)
	}
	return a, nil
}

func (s *PgStore) ListApps(ctx context.Context, accountID string) ([]App, error) {
	sel := `select ` + appsSelectColumns + ` from apps where account_id = $1 and status <> 'deleted' order by created_at desc`
	rows, err := s.pool.Query(ctx, sel, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

func (s *PgStore) ListAllApps(ctx context.Context) ([]App, error) {
	sel := `select ` + appsSelectColumns + ` from apps where status <> 'deleted' order by created_at desc`
	rows, err := s.pool.Query(ctx, sel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

// ListAppsByNodeID returns every non-deleted app whose owner is the
// given compute_nodes.id (Phase 2 / Gate A, migration 00090). This is
// the schedd's own list: the reaper, the cron dispatcher, the
// scale-up trigger, and the watchdog all want "apps I'm responsible
// for", not "apps on this account". The apps_node_id_idx index makes
// this a single index scan + a filter on status; without it the
// reaper would do a seq scan on apps per schedd per minute (the
// memory: "schedd watchdog tick" notes the 1s tick budget).
//
// The default-local node carries the same shape as any other node;
// the schedd that resolves to default-local (the single-box posture)
// reads its owner list from this method unchanged.
func (s *PgStore) ListAppsByNodeID(ctx context.Context, nodeID string) ([]App, error) {
	sel := `select ` + appsSelectColumns + ` from apps where node_id = $1 and status <> 'deleted' order by created_at desc`
	rows, err := s.pool.Query(ctx, sel, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

// ListInstancesByNodeID returns every instance whose owning app's
// owner_node is the given compute_nodes.id. Phase 2 / Gate A — the
// reaper (parked-instance timer) and the watchdog (kill-stuck) both
// want "instances I'm responsible for". The JOIN through apps lets
// the planner hit apps_node_id_idx first and then do a nested-loop
// on instances.app_id via instances_app_id_idx; for a fleet of <10
// nodes × <100 apps/node × <20 instances/app this stays under 5 ms.
// The projection matches ListInstancesForAccount exactly so
// scanInstanceCols decodes the same base columns (id, app_id,
// deployment_id, state, netns, guest_uid, host_ip, ram_mb, started_at,
// last_request_at, parked_at, node_id, wake_id, framework_ready_at,
// tail_count, mode).
func (s *PgStore) ListInstancesByNodeID(ctx context.Context, nodeID string) ([]Instance, error) {
	sel := `select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
		   from instances i
		   join apps a on a.id = i.app_id
		  where a.node_id = $1`
	rows, err := s.pool.Query(ctx, sel, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListInstancesOnNodeID returns every instance physically resident on the
// given compute node. It deliberately filters instances.node_id rather than
// joining through apps.node_id: live migration commits the physical move
// first, while the app's scheduler ownership remains on the original node.
// Drain safety and node observability must not miss that interval.
func (s *PgStore) ListInstancesOnNodeID(ctx context.Context, nodeID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		       coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count
		  from instances i
		 where i.node_id = $1
		 order by i.started_at desc nulls last, i.id::text desc`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListInstancesForLifecycleReconciliation returns live instances belonging to
// deleted apps or accounts in the deletion grace window. Unlike the normal
// reaper lists, it intentionally includes soft-deleted apps and the
// evicting_account_deleting state: those rows are exactly the durable cleanup
// backlog that a missed pg_notify or a schedd restart must recover.
func (s *PgStore) ListInstancesForLifecycleReconciliation(ctx context.Context, nodeID string, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, nil
	}

	nodeClause := ""
	args := make([]any, 0, 2)
	limitArg := 1
	if nodeID != "" {
		nodeClause = " and a.node_id = $1"
		args = append(args, nodeID)
		limitArg = 2
	}
	args = append(args, limit)

	sel := fmt.Sprintf(`select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
		   from instances i
		   join apps a on a.id = i.app_id
		   join accounts ac on ac.id = a.account_id
		  where (
		        (a.status = 'deleted' and i.state in ('waking','cold_booting','running','snapshotting','migrating'))
		     or (ac.status = 'deleted_pending' and i.state in ('waking','cold_booting','running','snapshotting','migrating','evicting_account_deleting'))
		  )%s
		  order by i.started_at asc, i.id asc
		  limit $%d`, nodeClause, limitArg)
	rows, err := s.pool.Query(ctx, sel, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// FailRunningInstanceIfOwnedByNode is the healthy-node stale-instance
// reconciliation primitive. vmmd capacity reports are authoritative only
// after two identical complete reports; this conditional update then makes
// the state transition race-safe against a concurrent park, wake, or
// migration. ErrConflict means another lifecycle writer won the race.
func (s *PgStore) FailRunningInstanceIfOwnedByNode(ctx context.Context, id, nodeID string, terminalAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'failed', terminal_at = $3
		  where id = $1 and node_id = $2 and state = 'running'`,
		id, nodeID, terminalAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// ListOwnedCronsByNodeID returns every cron whose owning app's owner
// node is the given compute_nodes.id. Phase 2 / Gate A — the cron
// dispatcher runs once per node and only fires for crons on apps it
// owns; without this filter every schedd would fire every cron and
// the duplicate-dispatch hazard would corrupt the
// cron_fired_audit row. The apps_node_id_idx covers the JOIN.
// Projection matches scanCrons: id, app_id, schedule, path, enabled,
// created_at (6 columns).
func (s *PgStore) ListOwnedCronsByNodeID(ctx context.Context, nodeID string) ([]Cron, error) {
	sel := `select c.id, c.app_id, c.schedule, c.path, c.enabled, c.created_at
		   from crons c
		   join apps a on a.id = c.app_id
		  where a.node_id = $1`
	rows, err := s.pool.Query(ctx, sel, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCrons(rows)
}

// ListUnplacedApps returns every non-deleted app whose node_id is
// NULL — the cold-start sweep input for schedd's
// PlacementClaimSubscriber (Phase 2 / Gate A migration 00091). The
// post-00091 schema allows node_id NULL at insert time so apid can
// INSERT a fresh app with the owner undecided; schedd races to
// stamp the owner on NotifyAppChanged "created". This method is
// the cold-start sweep path that handles a schedd that was down
// while a notify landed (pg_notify is fire-and-forget; missed
// events surface as NULL-row apps at the next start).
func (s *PgStore) ListUnplacedApps(ctx context.Context) ([]App, error) {
	sel := `select ` + appsSelectColumns + `
			  from apps
			 where node_id is null
			   and status <> 'deleted'
			 order by created_at desc`
	rows, err := s.pool.Query(ctx, sel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

// SetAppNodeID atomically claims the owner for an unplaced app. The
// UPDATE is conditional on node_id IS NULL so exactly one schedd
// wins the race; losers receive ErrConflict and the subscriber
// drops silently. Returns ErrNotFound when the app row is gone
// (hard-deleted between notify and claim — possible on the M7
// path; the subscriber treats this as a no-op drop). The FK +
// empty-uuid CHECK on apps.node_id reject bad values via the
// existing 23503 / 23514 paths.
func (s *PgStore) SetAppNodeID(ctx context.Context, appID, nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("state: set app node_id: empty nodeID")
	}
	tag, err := s.pool.Exec(ctx,
		`update apps set node_id = $2 where id = $1 and node_id is null`,
		appID, nodeID)
	if err != nil {
		return fmt.Errorf("state: set app node_id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either a peer already claimed (most common) or the row
		// is gone. Disambiguate via AppByID so the caller can drop
		// silently in both cases without a second round-trip on
		// the hot path.
		if _, err := s.AppByID(ctx, appID); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

// ListOrphanedApps returns every non-deleted app whose
// node_id points at an inactive compute_node — the input
// set for pkg/sched/rebalancer.go (Tier A4 / migration 00092).
//
// SQL shape:
//
//	select <appsSelectColumns>
//	  from apps a
//	 where a.node_id is not null
//	   and a.status in ('active', 'evicted_cold')
//	   and not exists (
//	     select 1 from compute_nodes n
//	      where n.id = a.node_id and n.active = true)
//	   and (a.reassigned_at is null
//	        or a.reassigned_at < now() - make_interval(secs => $1))
//	 order by a.reassigned_at asc nulls first, a.id asc
//	 limit $2
//
// The status filter is the apps.status CHECK minus
// `deleted` — the rebalancer reassigns live apps (active
// or evicted_cold), not soft-deleted ones. The not-exists
// clause is the orphaning predicate; the make_interval
// bound is the cooldown filter so the SQL already does the
// right thing on a flap-loop (a fresh reassignment stays
// on its current owner for at least cooldownSeconds). The
// partial index from migration 00093
// (apps_node_id_status_partial_idx) covers the leading
// WHERE clause; the partial index from migration 00092
// (apps_reassigned_at_idx) covers the trailing cooldown
// filter. Both indexes are bounded by the non-deleted app
// fleet — a busy multi-node install has at most one
// reassignment per app per cooldown window, so the indexes
// stay narrow.
//
// cooldownSeconds < 0 disables the cooldown filter (the
// rebalancer passes the default; tests sometimes pass 0 to
// force-eligibility). maxPerTick < 1 returns an empty set
// (the cap is a defensive zero — production always passes
// RebalanceMaxPerTickPerNode > 0).
func (s *PgStore) ListOrphanedApps(ctx context.Context, cooldownSeconds, maxPerTick int) ([]App, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	// cooldownSeconds < 0 disables the cooldown filter
	// (production never does this; only tests that want
	// every orphaned app to be eligible). The SQL renders
	// the filter clause only when the value is non-negative.
	var cooldownClause string
	var args []any
	if cooldownSeconds >= 0 {
		cooldownClause = ` and (a.reassigned_at is null or a.reassigned_at < now() - make_interval(secs => $1))`
		args = append(args, cooldownSeconds, maxPerTick)
	} else {
		args = append(args, maxPerTick)
	}
	sel := `select ` + appsSelectColumns + `
	          from apps a
	         where a.node_id is not null
	           and a.status in ('active', 'evicted_cold')
	           and not exists (
	             select 1 from compute_nodes n
	              where n.id = a.node_id and n.active = true)` +
		cooldownClause + `
	         order by a.reassigned_at asc nulls first, a.id asc
	         limit $` + strconv.Itoa(len(args))
	rows, err := s.pool.Query(ctx, sel, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list orphaned apps: %w", err)
	}
	defer rows.Close()
	return scanApps(rows)
}

// ReassignAppOwner atomically transfers app ownership from
// fromNodeID to toNodeID and stamps reassigned_at = now()
// (Tier A4 / migration 00092). Closes the Phase-2
// follow-up "apps pinned to a dead node" gap
// (ADR-064). The conditional UPDATE is
//
//	update apps
//	   set node_id = $3, reassigned_at = now()
//	 where id = $1 and node_id = $2
//	   and status in ('active', 'evicted_cold')
//
// The fromNodeID + status predicates are load-bearing:
//   - fromNodeID: a peer-claim-then-second-peer-claim race
//     never silently succeeds with a stale from-node. If
//     the first peer's UPDATE landed first, the second
//     peer's UPDATE finds node_id = $3 (not $2) and
//     RowsAffected() == 0.
//   - status: the UPDATE only touches non-deleted apps
//     (status IN 'active','evicted_cold'). A soft-deleted
//     app row stays soft-deleted; the rebalancer
//     ListOrphanedApps already filters those out at the
//     SQL level (the apps.status CHECK has `deleted` as
//     the third value), but the defence-in-depth here
//     protects against a race between the read and the
//     write.
//
// Both node IDs are FK-validated by apps_node_id_fkey (set
// in migration 00090); a bad toNodeID returns 23503, a
// bad fromNodeID just fails the WHERE clause (RowsAffected
// == 0). Returns ErrConflict on RowsAffected()==0; the
// caller (rebalancer) treats that as "peer won" / "app
// gone" / "app moved to live" and drops silently.
func (s *PgStore) ReassignAppOwner(ctx context.Context, appID, fromNodeID, toNodeID string) error {
	if appID == "" {
		return fmt.Errorf("state: reassign app owner: empty appID")
	}
	if fromNodeID == "" {
		return fmt.Errorf("state: reassign app owner: empty fromNodeID")
	}
	if toNodeID == "" {
		return fmt.Errorf("state: reassign app owner: empty toNodeID")
	}
	tag, err := s.pool.Exec(ctx,
		`update apps
		    set node_id = $3, reassigned_at = now()
		  where id = $1
		    and node_id = $2
		    and status in ('active', 'evicted_cold')`,
		appID, fromNodeID, toNodeID)
	if err != nil {
		return fmt.Errorf("state: reassign app owner: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Peer already won, app moved to live, or app gone.
		// The rebalancer treats all three as drop-silently;
		// the disambiguation is logged at Debug, not surfaced
		// to the caller (who would otherwise need a second
		// round-trip on the hot path).
		return ErrConflict
	}
	return nil
}

// ListLiveInstancesOnNode returns every live instance owned by
// nodeID — the candidate set for Engine.MigrateLiveInstances
// (Tier A5 / migration 00097, ADR-066). Filtered to state='running'
// only — the MarkInstanceMigrating predicate that gates Phase 2
// requires state='running' + node_id=currentNodeID, so a WAKING /
// COLD_BOOTING / SNAPSHOTTING instance on the dying node would
// fail Phase 2 and bump outcome="conflict" (polluting the metric).
// Those states stay on the dying node and the dying vmmd drives
// the cold-boot to completion (ADR-005 cold-boot-from-disk); the
// migration path is only the right primitive for RUNNING instances.
//
// Returns an empty slice (not ErrNotFound) when nodeID has no
// live instances; callers treat that as "nothing to migrate this
// tick".
//
// Sorted by instance id ASC for determinism so two peers observing
// the same drain event read the same input set (they still race on
// MigrateInstanceOwner; this list is just the candidate set).
//
// When nodeID == "" (cold-start sweep), the query ignores the
// node-id filter and returns every live instance whose owning
// compute_node is inactive. Mirrors ListOrphanedApps's empty-input
// convention (Tier A4).
func (s *PgStore) ListLiveInstancesOnNode(ctx context.Context, nodeID string, maxPerTick int) ([]Instance, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	var nodeClause string
	var args []any
	if nodeID != "" {
		nodeClause = ` and i.node_id = $1`
		args = append(args, nodeID, maxPerTick)
	} else {
		// Cold-start variant: filter to inactive-node owners
		// (same shape as ListOrphanedApps).
		nodeClause = ` and not exists (
		                select 1 from compute_nodes n
		                 where n.id = i.node_id and n.active = true)`
		args = append(args, maxPerTick)
	}
	sel := `select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''),
	               coalesce(i.guest_uid,0), coalesce(host(i.host_ip),''), i.ram_mb,
	               i.started_at, i.last_request_at, i.parked_at,
	               coalesce(i.node_id::text, ''), i.wake_id, i.framework_ready_at,
	               i.migrated_from_node_id::text, i.migrated_at, coalesce(i.lease_token, ''),
	               i.tail_count, i.mode
	          from instances i
	         where i.state = 'running'` +
		nodeClause + `
	         order by i.id asc
	         limit $` + strconv.Itoa(len(args))
	rows, err := s.pool.Query(ctx, sel, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list live instances on node: %w", err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		ins, err := scanInstanceColsWithMigration(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

// MarkInstanceMigrating is the Phase-2 atom of the four-phase
// cross-node live-instance handoff (Tier A5 / ADR-066). Transitions
// the instance to state='migrating' under a conditional UPDATE that
// requires state='running' and node_id = currentNodeID. Returns
// ErrConflict on RowsAffected()==0 — peer rollback, owner change, or
// row gone. The conditional predicate is load-bearing: only RUNNING
// instances are eligible for live migration; WAKING/COLD_BOOTING
// stay on the dying node and the dying vmmd drives the cold-boot to
// completion. A peer re-owner that already flipped node_id would
// fail this UPDATE on the node_id predicate.
//
// The lease_token is NOT stamped here — Phase 1 mints the lease on
// the new owner and Phase 3 writes it via MigrateInstanceOwner. The
// intermediate state-transition just flips state='migrating' so a
// peer claim that races us sees the new state and bails.
func (s *PgStore) MarkInstanceMigrating(ctx context.Context, instanceID, currentNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: mark instance migrating: empty instanceID")
	}
	if currentNodeID == "" {
		return fmt.Errorf("state: mark instance migrating: empty currentNodeID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: mark instance migrating: empty leaseToken")
	}
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'migrating',
		        lease_token = $3
		  where id = $1
		    and node_id = $2
		    and state = 'running'`,
		instanceID, currentNodeID, leaseToken)
	if err != nil {
		return fmt.Errorf("state: mark instance migrating: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Peer rollback / owner change / row gone. The migration
		// orchestrator treats this as "drop silently"; the next
		// compute_node_changed event retries. The state machine
		// prevents the instance from getting stuck in 'migrating'
		// — if no peer picks up the lease, MigrateLiveLeaseSeconds
		// (pkg/api/limits.go) fires CancelInstanceMigration.
		return ErrConflict
	}
	return nil
}

// MigrateInstanceOwner is the Phase-3 commit of the four-phase
// cross-node live-instance handoff (Tier A5 / ADR-066). Conditional
// UPDATE that flips instances.node_id, stamps the migration lineage
// columns (migrated_from_node_id, migrated_at, lease_token),
// transitions state from 'migrating' back to 'running', AND stamps
// apps.migrated_at in the same transaction so the app-level lineage
// column stays coherent with the instance row.
//
// The conditional predicates are load-bearing:
//  1. state = 'migrating' (peer rollback would have moved back to
//     'parked' already)
//  2. node_id = fromNodeID (peer re-owner would have flipped
//     this already)
//  3. lease_token = leaseToken (a stale lease can never commit the
//     handoff after a newer migration has claimed the row)
//
// Returns ErrConflict on RowsAffected()==0 — peer rollback, peer
// re-owner, or row gone.
func (s *PgStore) MigrateInstanceOwner(ctx context.Context, instanceID, fromNodeID, toNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: migrate instance owner: empty instanceID")
	}
	if fromNodeID == "" || toNodeID == "" {
		return fmt.Errorf("state: migrate instance owner: empty from/to nodeID")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: migrate instance owner begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Two-UPDATE transaction:
	//   1. instances row: conditional on state='migrating' +
	//      node_id=fromNodeID, flips node_id, stamps lineage cols,
	//      restores state='running'. Returns the app_id for the
	//      second UPDATE.
	//   2. apps row: stamps migrated_at = now() so the dashboard's
	//      "fleet live-migration throughput" panel stays coherent.
	var appID string
	err = tx.QueryRow(ctx,
		`update instances
		    set node_id = $3,
		        migrated_from_node_id = $2,
		        migrated_at = now(),
		        lease_token = $4,
		        state = 'running'
		where id = $1
		    and state = 'migrating'
		    and node_id = $2
		    and lease_token = $4
		returning app_id`,
		instanceID, fromNodeID, toNodeID, leaseToken).Scan(&appID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("state: migrate instance owner: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`update apps set migrated_at = now() where id = $1`,
		appID); err != nil {
		return fmt.Errorf("state: migrate instance owner (apps.migrated_at): %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: migrate instance owner commit: %w", err)
	}
	return nil
}

// CancelInstanceMigration is the Phase-4 rollback of the four-phase
// cross-node live-instance handoff (Tier A5 / ADR-066). Conditional
// UPDATE that transitions the instance back from 'migrating' to
// 'parked' on the original owner. The dying vmmd resumes the VM;
// the snapshot stays where it was. Predicates:
//  1. state = 'migrating'
//  2. node_id = originalNodeID (the rollback is owner-local; a
//     peer commit racing us would have flipped node_id already)
//  3. lease_token = leaseToken (stale-lease safety)
//
// Returns ErrConflict on RowsAffected()==0 — peer already committed
// (no rollback needed), lease expired, or row gone. The UPDATE
// also clears lease_token so a future re-attempt at migration
// mints a fresh lease.
func (s *PgStore) CancelInstanceMigration(ctx context.Context, instanceID, originalNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: cancel instance migration: empty instanceID")
	}
	if originalNodeID == "" {
		return fmt.Errorf("state: cancel instance migration: empty originalNodeID")
	}
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'parked',
		        lease_token = NULL
		  where id = $1
		    and state = 'migrating'
		    and node_id = $2
		    and lease_token = $3`,
		instanceID, originalNodeID, leaseToken)
	if err != nil {
		return fmt.Errorf("state: cancel instance migration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// ListExpiredMigrations returns every instance row in
// state='migrating' (Tier A6 / ADR-067 migrating-instance
// watchdog). The watchdog is the only writer that can move a
// row out of 'migrating' without a peer commit, so the
// unresolved row is the input set. The SQL also enforces
// lease_token IS NOT NULL — every wedged migration must
// carry the lease the watchdog needs to drive the gRPC
// re-invite; a row in 'migrating' without a lease is a
// corrupted state and the watchdog drops it silently (the
// next watch-dog tick is no-op idempotent).
//
// Sorted by instance id ASC for determinism so two peers
// observing the same bad-owner event read the same input
// set (they still race on the conditional UPDATEs; this list
// is just the candidate set).
//
// Returns an empty slice (not ErrNotFound) when no rows
// match; callers treat that as "nothing to reconcile this
// tick". Symmetric with ListLiveInstancesOnNode (Tier A5).
func (s *PgStore) ListExpiredMigrations(ctx context.Context, maxPerTick int) ([]Instance, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	sel := `select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''),
	               coalesce(i.guest_uid,0), coalesce(host(i.host_ip),''), i.ram_mb,
	               i.started_at, i.last_request_at, i.parked_at,
	               coalesce(i.node_id::text, ''), i.wake_id, i.framework_ready_at,
	               i.migrated_from_node_id::text, i.migrated_at, coalesce(i.lease_token, ''),
	               i.tail_count, i.mode
	          from instances i
	         where i.state = 'migrating'
	           and i.lease_token is not null
	         order by i.id asc
	         limit $1`
	rows, err := s.pool.Query(ctx, sel, maxPerTick)
	if err != nil {
		return nil, fmt.Errorf("state: list expired migrations: %w", err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		ins, err := scanInstanceColsWithMigration(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, rows.Err()
}

// ReinviteMigratingInstance is the active-owner ack gate of the
// Tier A6 / ADR-067 migrating-instance watchdog. Conditional
// UPDATE that flips state='migrating' → 'running', stamps
// migrated_at = now(), and clears lease_token — the same work
// the A5 Phase-3 commit (MigrateInstanceOwner) does, but launched
// by the watchdog after a re-invite to the new owner vmmd. The
// conditional predicates are load-bearing:
//  1. state = 'migrating' (peer rollback would have moved back
//     to 'parked' already)
//  2. lease_token = leaseToken (a stale lease can never silently
//     commit; the watchdog must present the same UUID the new
//     owner minted at Phase 1)
//
// Returns ErrConflict on RowsAffected()==0 — peer already
// committed, peer rolled back, lease expired, or row gone.
func (s *PgStore) ReinviteMigratingInstance(ctx context.Context, instanceID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: reinvite migrating instance: empty instanceID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: reinvite migrating instance: empty leaseToken")
	}
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'running',
		        migrated_at = now(),
		        lease_token = NULL
		  where id = $1
		    and state = 'migrating'
		    and lease_token = $2`,
		instanceID, leaseToken)
	if err != nil {
		return fmt.Errorf("state: reinvite migrating instance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// AbortMigratingInstance is the dead-owner hard-delete gate of
// the Tier A6 / ADR-067 migrating-instance watchdog. Conditional
// UPDATE that flips state='migrating' → 'parked' and clears
// lease_token so a future re-attempt at migration mints a fresh
// lease. node_id is left UNCHANGED — the row's node_id is still
// the OLD owner (A5 Phase-2 MarkInstanceMigrating flipped state
// but did not flip node_id; Phase-3 MigrateInstanceOwner never
// ran), and there is no better destination to point at: the OLD
// owner is the one whose vmmd died, the NEW owner never wrote a
// snapshot, and migrated_from_node_id is NULL pre-Phase-3 (so
// setting node_id = migrated_from_node_id would zero it out and
// break the wake path's WakeResult.NodeID — see engine.go:681).
// The wake path dispatches via app.NodeID (engine.go:1394-1400)
// so a parked row on a dead instance.NodeID is fine; the next
// customer request wakes cold on the live apps.node_id.
//
// The conditional predicates are the same as
// ReinviteMigratingInstance. Returns ErrConflict on
// RowsAffected()==0.
func (s *PgStore) AbortMigratingInstance(ctx context.Context, instanceID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: abort migrating instance: empty instanceID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: abort migrating instance: empty leaseToken")
	}
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'parked',
		        lease_token = NULL
		  where id = $1
		    and state = 'migrating'
		    and lease_token = $2`,
		instanceID, leaseToken)
	if err != nil {
		return fmt.Errorf("state: abort migrating instance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// FailRunningInstanceOnDeadNode flips a RUNNING row to FAILED when its
// owning node is gone, stamping terminal_at so the §17 retention sweep
// can age the row out.
//
// The predicate is the load-bearing race-safety guarantee, exactly as
// in AbortMigratingInstance: `state = 'running' AND node_id = $2`. If
// the node came back and its vmmd re-registered, or a peer already
// transitioned the row (park, evict, migrate), RowsAffected() is 0 and
// we return ErrConflict so the caller counts it and moves on rather
// than second-guessing the state machine. Pinning node_id means a row
// that migrated to a healthy node between the SELECT and this UPDATE
// is never failed by a stale read.
//
// running → failed is a legal edge (machine.go validTransitions), and
// FAILED is excluded from CountsForRAM(), which is what actually stops
// the billing leak: meterd's sampler skips the row on its next tick.
// FAILED (not PARKED) because no snapshot was taken — the VM died with
// its host. The wake path treats FAILED as cold-bootable (ADR-005), so
// the customer's next request still serves.
func (s *PgStore) FailRunningInstanceOnDeadNode(ctx context.Context, instanceID, nodeID string) error {
	if instanceID == "" {
		return fmt.Errorf("state: fail running instance on dead node: empty instanceID")
	}
	if nodeID == "" {
		return fmt.Errorf("state: fail running instance on dead node: empty nodeID")
	}
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = 'failed',
		        terminal_at = now()
		  where id = $1
		    and state = 'running'
		    and node_id = $2`,
		instanceID, nodeID)
	if err != nil {
		return fmt.Errorf("state: fail running instance on dead node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) CountDeployedApps(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from apps where account_id = $1 and status in ('active','evicted_cold')`,
		accountID).Scan(&n)
	return n, err
}

// CountAppsWithEvictionPriority returns the per-account count of
// apps whose eviction_priority equals the given tier (issue #475).
// Counts APPS (not instances) — a single reserved app with 5
// concurrent instances counts as 1 against the per-account cap
// (Plan.ReservedConcurrencyPerAccount). Excludes soft-deleted apps
// (status='deleted') so a recently-deleted reserved app doesn't
// leak into the cap and reject a subsequent recreate. The
// `apps_account_idx (account_id, status)` partial composite index
// keeps this O(N_per_account) — bounded by the acked per-account
// cap (Hobby 1, Pro 2, Scale 4) so the lock-pending apid path
// (CreateCronIfUnderQuota pattern) is constant-time in practice.
func (s *PgStore) CountAppsWithEvictionPriority(ctx context.Context, accountID, priority string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from apps where account_id = $1 and status <> 'deleted' and eviction_priority = $2`,
		accountID, priority).Scan(&n)
	return n, err
}

// CountAuthDefaultFlippedApps (issue #695 / ADR-080) returns the
// per-account count of apps stamped by the apps-auth-default
// grand-father migration. Migration 00155 sets
// auth_default_flipped_at on every pre-flip row; this query reads
// back the live pre-flip count so the apid dashboard banner can
// turn itself off when the customer has PATCHed every pre-flip app
// back to public. Excludes soft-deleted apps so the banner tracks
// the customer's actual surface. The apps_account_idx (account_id,
// status) composite index keeps this O(N_per_account) — bounded by
// the per-account app cap (Free 1, Hobby 5, Pro 25, Scale 100), so
// the dashboard request stays constant-time in practice.
//
// The auth_default_flipped_at IS NOT NULL predicate picks up only
// grand-fathered rows; a fresh post-flip create never has a stamp,
// so a brand-new customer with no pre-flip apps reads 0 and the
// banner is silent.
func (s *PgStore) CountAuthDefaultFlippedApps(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from apps where account_id = $1 and status <> 'deleted' and auth_default_flipped_at is not null`,
		accountID).Scan(&n)
	return n, err
}

// AuthDefaultFlippedAt (issue #695 / ADR-080) reads the
// `apps.auth_default_global_flipped` event's `at` timestamp. The
// migration emits exactly one of these rows on apply (guarded by
// `WHERE NOT EXISTS`); a replay produces no additional row, so the
// MIN/MAX query always returns the original migration time. Returns
// the zero time when no such event has been recorded yet (the apid
// caller falls back to a "Recently" copy rather than blocking the
// banner on a successful store read).
func (s *PgStore) AuthDefaultFlippedAt(ctx context.Context) (time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx,
		`select min(at) from events where kind = 'apps.auth_default_global_flipped'`,
	).Scan(&t)
	if err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

func (s *PgStore) UpdateApp(ctx context.Context, id string, p UpdateAppParams) (App, error) {
	manifestBytes := []byte(nil)
	if p.Manifest != nil {
		manifestBytes, _ = json.Marshal(*p.Manifest)
	}
	// Issue #462 / ADR-058 / PR-A: the scaling policy is the jsonb
	// column `apps.scaling_policy`. The store writes the jsonb
	// AND keeps the legacy `min_instances` column in sync so the
	// reaper + the SDK see the same floor without a fan-out read.
	// The sync is unconditional when the policy is set: the
	// legacy column is the projection of the policy's
	// MinInstances (Hobby+ writes 1; Free stays 0).
	scalingPolicyBytes := []byte(nil)
	keepMinInstancesInSync := false
	if p.SetScalingPolicy && p.ScalingPolicy != nil {
		scalingPolicyBytes, _ = json.Marshal(*p.ScalingPolicy)
		keepMinInstancesInSync = true
	}
	upd := `update apps set
		   ram_mb          = coalesce($2, ram_mb),
		   idle_timeout_s  = case when $3 then $4 else idle_timeout_s end,
		   max_concurrency = coalesce($5, max_concurrency),
		   status          = coalesce($6, status),
		   manifest        = case when $7 then $8::jsonb else manifest end,
		   min_instances   = case
		                        when $27 then $28
		                        when $9  then $10
		                        else min_instances
		                      end,
		   egress_allowlist = case when $11 then $12::cidr[] else egress_allowlist end,
		   autoscale_target_rps    = case when $13 then $14 else autoscale_target_rps end,
		   autoscale_target_cpu_pct = case when $15 then $16 else autoscale_target_cpu_pct end,
		   streaming_enabled = case when $17 then $18 else streaming_enabled end,
		   require_signed = case when $19 then $20 else require_signed end,
		   root_dir       = case when $21 then $22 else root_dir end,
		   workload_name  = case when $23 then $24 else workload_name end,
		   start_command  = case when $25 then $26::text else start_command end,
		   scaling_policy = case when $29 then $30::jsonb else scaling_policy end,
		   warm_snapshot_enabled = case when $31 then $32 else warm_snapshot_enabled end,
		   warm_snapshot_min_requests = case when $33 then $34 else warm_snapshot_min_requests end,
		   warm_snapshot_min_ms = case when $35 then $36 else warm_snapshot_min_ms end,
		   eviction_priority = case when $37 then $38 else eviction_priority end,
		   require_authn = case when $39 then $40 else require_authn end,
		   -- Issue #477 / ADR-079: per-app public_auth. The mode
		   -- column is the canonical per-app config; the
		   -- basic-sealed blob is the secretbox-encrypted
		   -- credential pair (mode='basic' only). The two
		   -- columns are SET together so a partial PATCH
		   -- (mode='open' with stale sealed blob from a prior
		   -- mode='basic' PATCH) clears the blob atomically.
		   public_auth_mode   = case when $41 then $42::text  else public_auth_mode   end,
		   public_auth_basic  = case when $41 then $43::bytea else public_auth_basic  end,
-- Issue #695 / ADR-080: grand-father marker. Cleared
			   -- when the customer makes a deliberate PATCH choice
			   -- on a grandfathered app (ClearAuthDefaultFlippedAt
			   -- flag set by apid when SetRequireAuthn OR
			   -- SetPublicAuth is true). The dashboard banner
			   -- counts apps.auth_default_flipped_at IS NOT NULL
			   -- per account; clearing the stamp brings the count
			   -- toward zero and stops the banner from re-rendering.
			   auth_default_flipped_at = case when $44 then NULL else auth_default_flipped_at end,
			   -- Issue #676 / PR-3: per-app raw-bytes Upgrade
			   -- bridge. Same Set-bit convention as streaming_enabled
			   -- above; apid gates PATCH-true through
			   -- Plan.WebSocketResponseAllowed() (Free → 403
			   -- plan_websocket_not_allowed).
			   websocket_enabled = case when $45 then $46 else websocket_enabled end,
			   -- ADR-093: per-route observability opt-in.
			   -- Same Set-bit convention as websocket_enabled
			   -- above; apid gates PATCH-true through
			   -- Plan.RouteMetricsResponseAllowed() (Free →
			   -- 403 plan_route_metrics_not_allowed). The
			   -- companion SetRouteMetricsEnabled flag
			   -- distinguishes "don't touch" (default) from
			   -- "explicit false" (opt out).
			   route_metrics_enabled = case when $47 then $48 else route_metrics_enabled end,
			   -- Tier A10 / ADR-088: per-app overflow_node
			   -- preference. Same Set-bit convention as the
			   -- surrounding fields — SetOverflowNode
			   -- distinguishes "don't touch" (default)
			   -- from "explicit NULL" (clear — back to A9
			   -- fallback). The empty-uuid CHECK + the FK
			   -- with ON DELETE SET NULL (migration 00167)
			   -- enforce the integrity contract; the store
			   -- is a plain column write.
			   overflow_node = case when $49 then $50 else overflow_node end,
				   -- ADR-091 amendment / §4.1.2.0: coarse-gate per-app
				   -- maintenance flag. Same Set-bit convention as the
				   -- surrounding fields — SetMaintenanceMode
				   -- distinguishes "don't touch" (default) from
				   -- "explicit false" (opt out). No plan gate
				   -- (Free+ may opt in). The companion
				   -- apps_maintenance_mode_notify trigger fires
				   -- pg_notify('app_changed', NEW.id::text) ONLY
				   -- when maintenance_mode IS DISTINCT FROM old, so
				   -- the cmd-side listener
				   -- (cmd/gatewayd-internal/backend.go) sees one
				   -- event per flip, not one per app UPDATE.
				   maintenance_mode = case when $51 then $52 else maintenance_mode end,
			   -- ADR-091 CORS improvements D1: per-app
			   -- default CORS opt-in. SetCORSDefaultEnabled
			   -- distinguishes "don't touch" from
			   -- "explicit false" (opt out); apid gates
			   -- the request shape so the SQL never sees
			   -- an illegal value. Plan-tier agnostic -
			   -- the fallback is the "just allow my
			   -- origin" surface customers expect from
			   -- any FaaS, so no plan gate.
				   cors_default_enabled = case when $53 then $54 else cors_default_enabled end,
			   -- ADR-091 CORS improvements D1:
			   -- per-app default CORS allowlist. Set bit
			   -- gates the ARRAY write; nil + Set=true
			   -- is rejected upstream by apid (the
			   -- validator requires a value when the
			   -- Set bit is true, same convention as
			   -- EgressAllowlist). The array shape
			   -- matches edge_rules_cors.allow_origins
			   -- so the gateway reuses the matchOrigin
			   -- matcher verbatim.
				   cors_default_origins = case when $55 then $56::text[] else cors_default_origins end,
-- ADR-119: per-app static egress IP
				   -- (customer BYOIP). SetStaticEgressIP
				   -- distinguishes "don't touch" (don't run
				   -- the SET clause) from "explicit IP"
				   -- (write the IP + stamp the audit
				   -- timestamp atomically). NULL with
				   -- SetStaticEgressIP=true clears the
				   -- pin (DELETE wire shape). The inet
				   -- text-encoding is the canonical pgx
				   -- representation; family=4 CHECK + the
				   -- partial unique index enforce the v1
				   -- contract (IPv4 only, no two apps on
				   -- the same account pin the same IP).
				   static_egress_ip = case when $57 then $58::inet else static_egress_ip end,
				   static_egress_ip_set_at = case when $57 and $58 is not null then NOW() when $57 and $58 is null then NULL else static_egress_ip_set_at end,
			   -- ADR-118: per-app ingress IP allowlist. Same Set-bit
			   -- pattern as EgressAllowlist above (SetPublicAuthIPAllowlist
			   -- distinguishes "don't touch" from "explicit empty"). The
			   -- DB trigger at migrations/00308_apps_public_auth_ip_allowlist.sql
			   -- rejects non-v4/v6 families and masklen /0 (defence in
			   -- depth on top of the apid parse step).
				   public_auth_ip_allowlist = case when $59 then $60::cidr[] else public_auth_ip_allowlist end,
				   -- ADR-124: per-app wire-protocol selector
				   -- (apps.app_protocol). Same Set*/optional-pointer
				   -- pattern as websocket_enabled above. The Set bit
				   -- distinguishes "don't touch" from "explicit
				   -- 'http1'" — without it the NOT NULL DEFAULT
				   -- 'http1' would mask an explicit reset. Closed-set
				   -- CHECK apps_app_protocol_chk admits only
				   -- {http1, http2, grpc}; apid validates the value
				   -- (Plan.AppProtocolAllowed gates 'grpc' to
				   -- Hobby+) before reaching this UPDATE.
					   app_protocol = case when $61 then $62 else app_protocol end
		 where id = $1
		 returning ` + appsSelectColumns
	// `policyMinInstances` is the value to push into the legacy
	// column when the policy is set. The two SET sources race on
	// the same column; the policy-comes-first CASE preserves the
	// policy author as the canonical writer at PR-A.
	//
	// Issue #470 / ADR-055: warm_snapshot_* updates follow the same
	// Set*/optional-pointer pattern as require_signed / streaming_enabled
	// so unset-vs-explicit-false is distinguishable on the wire.
	var policyMinInstances int
	if p.ScalingPolicy != nil {
		policyMinInstances = p.ScalingPolicy.MinInstances
	}
	row := s.pool.QueryRow(ctx, upd,
		id,
		p.RAMMB, p.SetIdleTimeout, intOrZero(p.IdleTimeoutS),
		p.MaxConcurrency, nullAppStatus(p.Status),
		p.Manifest != nil, manifestBytes,
		p.SetMinInstances, intOrZero(p.MinInstances),
		p.SetEgressAllowlist, cidrPrefixesToArray(derefPrefixes(p.EgressAllowlist)),
		p.SetAutoscaleTargetRPS, intOrZero(p.AutoscaleTargetRPS),
		p.SetAutoscaleTargetCPUPct, intOrZero(p.AutoscaleTargetCPUPct),
		p.SetStreamingEnabled, boolOrFalse(p.StreamingEnabled),
		p.SetRequireSigned, boolOrFalse(p.RequireSigned),
		p.RootDir != nil, p.RootDir,
		p.WorkloadName != nil, p.WorkloadName,
		p.StartCommand != nil, nullString(derefString(p.StartCommand)),
		keepMinInstancesInSync, policyMinInstances,
		p.SetScalingPolicy, scalingPolicyBytes,
		p.SetWarmSnapshotEnabled, boolOrFalse(p.WarmSnapshotEnabled),
		p.SetWarmSnapshotMinRequests, intOrZero(p.WarmSnapshotMinRequests),
		p.SetWarmSnapshotMinMs, intOrZero(p.WarmSnapshotMinMs),
		// Issue #475: eviction_priority. Plain column write — apid
		// already validates the value (must be 'best_effort' or
		// 'reserved'), gates 'reserved' behind the plan, and
		// enforces the per-account cap. The Set bit distinguishes
		// "unset" from "explicit best_effort" (opt out of reserved).
		p.SetEvictionPriority, derefString(p.EvictionPriority),
		// Issue #560: see the Set*/optional-pointer pattern as
		// require_signed / streaming_enabled — the Set bit
		// distinguishes "don't touch" (don't run the SET clause)
		// from "explicit false" (write false). Plan-gated
		// upstream: apid returns 403
		// plan_require_authn_not_allowed on Free/Hobby + true
		// so the SQL never sees an illegal value. Free customers
		// may PATCH true → false on a Pro-upgraded app; Hobby
		// customers may opt back out the same way.
		p.SetRequireAuthn, boolOrFalse(p.RequireAuthn),
		// Issue #477 / ADR-079: public_auth block. The Set bit
		// gates BOTH columns via the same CASE so a stale
		// sealed blob from a prior mode='basic' PATCH is
		// cleared when the customer PATCHes mode='open' or
		// mode='bearer' (the apid handler always passes
		// p.PublicAuth with Sealed=nil in that case).
		p.SetPublicAuth,
		derefString(ptrOrEmpty(p.PublicAuth)),
		nilOrBytes(p.PublicAuth),
		// Issue #695 / ADR-080: grand-father clear path. apid sets
		// this when the customer PATCHed require_authn or public_auth,
		// which is the deliberate-choice signal the dashboard banner
		// looks for. No-op for new post-flip apps (column is already
		// NULL). A no-touch PATCH (RAM_MB-only, etc.) leaves the
		// stamp alone so the banner keeps re-rendering.
		p.ClearAuthDefaultFlippedAt,
		// Issue #676 / PR-3: per-app raw-bytes Upgrade bridge.
		// Same Set*/optional-pointer pattern as streaming_enabled.
		p.SetWebSocketEnabled, boolOrFalse(p.WebSocketEnabled),
		// ADR-093: per-route observability opt-in. Same Set*/optional-
		// pointer pattern as websocket_enabled above. The per-plan
		// gate runs upstream in apid (Plan.RouteMetricsResponseAllowed)
		// so by the time this UPDATE runs, the value is authoritative.
		p.SetRouteMetricsEnabled, boolOrFalse(p.RouteMetricsEnabled),
		// Tier A10 / ADR-088: overflow_node preference. The
		// Set bit controls the CASE; the value slot is a
		// nullable UUID — nullString coerces nil/empty to
		// SQL NULL, and Postgres infers the UUID type from
		// the column. The Set bit distinguishes "don't
		// touch" (don't run the SET clause) from "explicit
		// NULL" (clear — back to A9 fallback).
		p.SetOverflowNode, nullString(derefString(p.OverflowNode)),
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag. The Set bit distinguishes "don't
		// touch" (default) from "explicit false" (opt out); the
		// companion trigger fires pg_notify ONLY on the flip
		// (migration 00237) so the cmd-side listener sees one
		// event per flip rather than one per app UPDATE.
		p.SetMaintenanceMode, boolOrFalse(p.MaintenanceMode),
		// ADR-091 CORS improvements D1: per-app default CORS
		// opt-in + allowlist. Same Set*/optional-pointer
		// pattern as overflow_node / streaming_enabled.
		// The validator on the apid side rejects
		// CORSDefaultEnabled = true with an empty
		// CORSDefaultOrigins (applies the same
		// explicit-empty-must-mean-something rule as
		// EgressAllowlist).
		p.SetCORSDefaultEnabled, boolOrFalse(p.CORSDefaultEnabled),
		p.SetCORSDefaultOrigins, derefStrings(p.CORSDefaultOrigins),
		// ADR-119: per-app static egress IP. SetStaticEgressIP
		// is the "don't touch" sentinel; StaticEgressIP is the
		// value (nil = clear). derefAddr lifts the *netip.Addr
		// into a string-encodable shape — pgx accepts the inet
		// text representation. The CASE in the SET clause
		// above stamps NOW() on every non-null write so the
		// audit timestamp is always current when the customer
		// re-pins.
		p.SetStaticEgressIP, derefAddr(p.StaticEgressIP),
		// ADR-118: per-app ingress IP allowlist. Same nil-pointer +
		// Set-bit convention as EgressAllowlist above. cidrPrefixesToArray
		// renders an empty slice as '{}' (the column DEFAULT) so a
		// PATCH with an empty IPAllowlist + SetPublicAuthIPAllowlist=true
		// clears the column, not the Set bit.
		p.SetPublicAuthIPAllowlist, cidrPrefixesToArray(derefPrefixes(p.PublicAuthIPAllowlist)),
		// ADR-124: per-app wire-protocol selector. Same
		// Set*/optional-pointer pattern as websocket_enabled
		// above; derefString coerces a nil pointer to "" so the
		// apid-side default of "http1" is preserved on PATCHes
		// that don't touch the field. The Set bit distinguishes
		// "don't touch" from "explicit http1".
		p.SetAppProtocol, derefString(p.AppProtocol))
	return scanApp(row)
}

// nilOrBytes returns p.PublicAuth.Sealed when SetPublicAuth is
// true (the apid seal step produced a non-nil blob for
// mode='basic'; apid passes nil Sealed for mode='open'/'bearer'
// so the same CASE writes NULL atomically). The pgx driver
// maps a nil []byte to SQL NULL, which is exactly what
// public_auth_basic expects when no creds are stored.
func nilOrBytes(p *AppPublicAuthUpdate) []byte {
	if p == nil {
		return nil
	}
	return p.Sealed
}

// ptrOrEmpty returns a pointer to p.PublicAuth.Mode when
// SetPublicAuth is true (so derefString sees the canonical
// value), or nil otherwise. The CASE guards the read site
// so a nil pointer is harmless.
func ptrOrEmpty(p *AppPublicAuthUpdate) *string {
	if p == nil {
		return nil
	}
	s := p.Mode
	return &s
}

// derefString returns the dereferenced value of a *string, or "" if
// nil. Mirrors derefInt at this file's helper section; both are safe
// to call with a nil pointer because the CASE guards the read site.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefAddr returns the string-encoded form of a *netip.Addr, or
// nil if the pointer is nil. Used by the UpdateApp SQL wrapper for
// the STATIC_EGRESS_IP arg (ADR-119); the CASE in the SQL guards
// the nil so a missing pointer never touches the wire (Set bit is
// false in that case). When the Set bit is true with a nil pointer,
// the customer is clearing the pin (DELETE wire shape) — the CASE
// writes NULL atomically. pgx accepts the inet text representation
// directly; the family=4 CHECK at the DB layer enforces IPv4-only.
func derefAddr(a *netip.Addr) any {
	if a == nil {
		return nil
	}
	return a.String()
}

// derefStrings returns the dereferenced value of a *[]string, or nil if
// the pointer is nil. Mirrors derefString above for the string case.
// Used by the UpdateApp SQL wrapper for the CORS_DEFAULT_ORIGINS arg;
// the CASE in the SQL guards the nil so a missing pointer never
// touches the wire (Set bit is false in that case). When the Set bit
// is true, the apid validator guarantees a non-nil pointer, so the
// dereference is safe.
func derefStrings(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
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

// SetAppWorkloadClass overwrites apps.workload_class (ADR-050 §3,
// ADR-051). Single round-trip UPDATE … RETURNING; the RETURNING
// projection matches appsSelectColumns so scanAppInto can decode it.
// SQLSTATE 23514 (apps_workload_class_chk) maps to ErrInvalidArgument
// via mapErr; SQLSTATE 23502 (not_null) is unreachable because the
// column carries a DEFAULT in the migration.
//
// The `source` argument is caller metadata only — the store does NOT
// log or persist it. The engine/reconcile caller writes an audit row
// carrying {app_id, observed_class, source} with the same value
// (ADR-035 best-effort).
//
// Returns the fresh App row so the cold-boot path in pkg/sched
// (PR-D) can pass it on to SetInstanceRuntime without a second read.
// Returns ErrNotFound when the app is gone.
func (s *PgStore) SetAppWorkloadClass(ctx context.Context, appID string, class WorkloadClass, source string) (App, error) {
	_ = source // metadata only — see comment above
	if class == "" {
		return App{}, ErrInvalidArgument
	}
	var a App
	row := s.pool.QueryRow(ctx,
		`update apps set workload_class = $2 where id = $1
		 returning `+appsSelectColumns,
		appID, string(class))
	if err := scanAppInto(&a, row); err != nil {
		// Funnel through mapErr so a CHECK violation on
		// apps_workload_class_chk (SQLSTATE 23514) surfaces as
		// ErrInvalidArgument instead of a raw pgx error. The empty
		// class guard above covers the Go-side validation; this
		// covers the schema-side defence-in-depth.
		return App{}, mapErr(err)
	}
	return a, nil
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
	upd := `update apps set slug = $3
		 where account_id = $1 and slug = $2 and status <> 'deleted'
		 returning ` + appsSelectColumns
	row := s.pool.QueryRow(ctx, upd,
		accountID, oldSlug, newSlug)
	return scanApp(row)
}

func (s *PgStore) DeleteApp(ctx context.Context, id string) error {
	// Legacy thin wrapper retained for the apid deleteApp handler
	// (Phase 5 pkg/reconcile calls SoftDeleteAppCascade directly).
	_, err := s.SoftDeleteAppCascade(ctx, id)
	return err
}

// SoftDeleteAppCascade marks the app deleted (status='deleted') and
// returns the freshly-deleted App row. Per Phase 5 user decision the
// cascade is status-only — child rows survive for slug-reuse (an app
// deleted then recreated under the same slug keeps its envs and
// secrets; cf. memstore_test.go:309-312). GDPR-style hard cascade
// still lives in DeleteAccount. Returns ErrNotFound when no row
// matches; the subsequent mapErr funnel wraps SQLSTATE 23502
// (not_null) the same way as SetAppWorkloadClass.
func (s *PgStore) SoftDeleteAppCascade(ctx context.Context, id string) (App, error) {
	var a App
	// The apps table does NOT have an updated_at column (appsSelectColumns
	// at pgstore.go:6531 doesn't include it; no migration adds it). The
	// earlier PR-E code touched updated_at = now() here, which trips
	// SQLSTATE 42703 on every soft-delete. The deleted row is filtered
	// out of every list/read by status <> 'deleted', so the deletion
	// timestamp is implicit — no separate column needed.
	row := s.pool.QueryRow(ctx, `
		update apps set status = 'deleted'
		where id = $1
		returning `+appsSelectColumns, id)
	if err := scanAppInto(&a, row); err != nil {
		return App{}, mapErr(err)
	}
	return a, nil
}

// --- Projects (ADR-050, Phase 1) ----------------------------------
//
// Phase 1 lands the storage seam: a project row + 7 methods. The
// patterns below mirror the existing App methods (RETURNING-id style,
// scanApp helper, ErrConflict/ErrNotFound mapErr translation). The
// monotonic-upgrade check in SetProjectScanSource is enforced by a
// rank comparison in pure SQL — Phase 5 reconcile calls it; Phase 1
// only exercises the no-op path and the upgrade path through tests.

func scanProject(row pgx.Row) (Project, error) {
	var (
		p          Project
		scanSource string
	)
	if err := row.Scan(
		&p.ID, &p.AccountID, &p.Slug, &p.RepoFullName, &p.ProductionBranch,
		&p.InstallID, &scanSource, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return Project{}, mapErr(err)
	}
	p.ScanSource = ProjectScanSource(scanSource)
	return p, nil
}

func scanProjects(rows pgx.Rows) ([]Project, error) {
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateProject inserts a new project row. The accounts FK is enforced
// by Postgres; an unknown accountID surfaces as ErrFKViolation via
// mapErr → 23503. The (account_id, slug) unique projects_account_slug_uniq
// surfaces as ErrConflict via 23505. scan_source defaults to 'unknown'
// at the DB level when the caller passes the empty string.
func (s *PgStore) CreateProject(ctx context.Context, p Project) (Project, error) {
	if p.ScanSource == "" {
		p.ScanSource = ProjectScanSourceUnknown
	}
	row := s.pool.QueryRow(ctx, `
		insert into projects
		    (account_id, slug, repo_full_name, production_branch, install_id, scan_source)
		values ($1, $2, $3, $4, $5, $6)
		returning id, account_id, slug, coalesce(repo_full_name,''),
		          coalesce(production_branch,''), coalesce(install_id,0),
		          scan_source, created_at, updated_at
	`,
		p.AccountID, p.Slug, nullString(p.RepoFullName), nullString(p.ProductionBranch),
		p.InstallID, string(p.ScanSource),
	)
	proj, err := scanProject(row)
	if err != nil {
		// FK violation on account_id means the owning account row is
		// gone. Surface as ErrNotFound so handlers can branch on a
		// single sentinel instead of distinguishing 23503 from
		// pgx.ErrNoRows. The pgErr.ConstraintName preserves the
		// diagnosis for operator logs.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			return Project{}, fmt.Errorf("%w: %s", ErrNotFound, pgErr.ConstraintName)
		}
		return Project{}, mapErr(err)
	}
	return proj, nil
}

func (s *PgStore) ProjectByID(ctx context.Context, projectID string) (Project, error) {
	row := s.pool.QueryRow(ctx, `
		select id, account_id, slug, coalesce(repo_full_name,''),
		       coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
		       created_at, updated_at
		  from projects
		 where id = $1
	`, projectID)
	return scanProject(row)
}

func (s *PgStore) ProjectBySlug(ctx context.Context, accountID, slug string) (Project, error) {
	row := s.pool.QueryRow(ctx, `
		select id, account_id, slug, coalesce(repo_full_name,''),
		       coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
		       created_at, updated_at
		  from projects
		 where account_id = $1 and slug = $2
	`, accountID, slug)
	return scanProject(row)
}

// ProjectByRepo is the push-dispatch lookup. install_id is non-null on
// bound projects only; we surface ErrNotFound when the row is gone
// rather than swallowing it as an empty result. The account_id filter
// is only applied when a non-empty accountID is supplied — passing ""
// (the cross-account push dispatch, or a test that hasn't bound an
// account yet) skips the account predicate so the uuid→text coercion
// in `account_id = ”` doesn't trip on an empty string.
func (s *PgStore) ProjectByRepo(ctx context.Context, accountID string, installID int64, repoFullName string) (Project, error) {
	if accountID == "" {
		row := s.pool.QueryRow(ctx, `
			select id, account_id, slug, coalesce(repo_full_name,''),
			       coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
			       created_at, updated_at
			  from projects
			 where install_id = $1 and repo_full_name = $2
			 limit 1
		`, installID, repoFullName)
		return scanProject(row)
	}
	row := s.pool.QueryRow(ctx, `
		select id, account_id, slug, coalesce(repo_full_name,''),
		       coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
		       created_at, updated_at
		  from projects
		 where install_id = $1 and repo_full_name = $2
		   and account_id = $3
		 limit 1
	`, installID, repoFullName, accountID)
	return scanProject(row)
}

func (s *PgStore) ListProjectsForAccount(ctx context.Context, accountID string) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `
		select id, account_id, slug, coalesce(repo_full_name,''),
		       coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
		       created_at, updated_at
		  from projects
		 where account_id = $1
		 order by created_at desc
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjects(rows)
}

// AppsForProject returns live apps currently bound to projectID. The
// account scope is enforced at the SQL level: a project_id owned by
// a different account returns ErrNotFound (not an empty slice) so
// handlers can 404 cleanly without leaking membership.
func (s *PgStore) AppsForProject(ctx context.Context, accountID, projectID string) ([]App, error) {
	// Project ownership check first: returns ErrNotFound cleanly.
	proj, err := s.ProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if proj.AccountID != accountID {
		return nil, ErrNotFound
	}
	sel := `select ` + appsSelectColumns + `
		   from apps
		  where project_id = $1 and status <> 'deleted'
		  order by workload_name asc, created_at asc`
	rows, err := s.pool.Query(ctx, sel, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanApps(rows)
}

// SetProjectScanSource updates scan_source monotonically upward. The
// rank lookup delegates to the SQL function pg_tier_rank added in
// migration 00073 — that function is the single source of truth for
// the rank table, mirrored in Go by tierRank in pkg/state/types.go.
// If requested_rank <= stored_rank, the UPDATE zero-rows-affects and
// we return ErrScanSourceDowngrade.
func (s *PgStore) SetProjectScanSource(ctx context.Context, projectID string, src ProjectScanSource) (Project, error) {
	if src == "" {
		src = ProjectScanSourceUnknown
	}
	// First round-trip: probe the existing row to learn its current
	// scan_source. We need this to distinguish downgrade (existing
	// rank > requested rank) from row-gone (no row at all) without
	// relying on a single UPDATE+RETURNING where the WHERE double-
	// duties both predicates. Splitting it gives us two clean signals:
	// pgx.ErrNoRows on the SELECT means row-gone; a non-empty result
	// lets us compare ranks directly.
	existing, getErr := s.ProjectByID(ctx, projectID)
	if getErr != nil {
		return Project{}, getErr
	}
	if tierRank(existing.ScanSource) > tierRank(src) {
		return Project{}, ErrScanSourceDowngrade
	}
	// Second round-trip: apply the upgrade (or same-tier no-op). The
	// CASE preserves updated_at on same-tier writes so the timestamp
	// moves only when scan_source actually changes.
	row := s.pool.QueryRow(ctx, `
		update projects
		   set scan_source = $2,
		       updated_at  = case when scan_source <> $2 then now() else updated_at end
		 where id = $1
		returning id, account_id, slug, coalesce(repo_full_name,''),
		          coalesce(production_branch,''), coalesce(install_id, 0), scan_source,
		          created_at, updated_at
	`, projectID, string(src))
	return scanProject(row)
}

// DeleteProject removes a project row by ID. The apps.project_id
// FK is declared ON DELETE SET NULL (migration 00074:74), so apps
// already pointing at this project have their project_id
// nulled on delete; reconcile-managed apps that were soft-deleted
// by the reconciler before this DeleteProject is called stay
// soft-deleted (the FK trigger runs after our DELETE). Returns
// ErrNotFound when no row matches.
//
// Used by cmd/apid's scan_service to roll back a half-created
// project when the subsequent reconcile errors out (PR-GH.6
// review H9).
func (s *PgStore) DeleteProject(ctx context.Context, projectID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
	if err != nil {
		return fmt.Errorf("pgstore: delete project %q: %w", projectID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ApplyProjectPlan persists a project + its member apps + crons in
// one transaction. The critical section sits behind a
// `SELECT … FOR UPDATE` on the parent accounts row so two concurrent
// applies on the same account serialise; an over-quota call returns
// *QuotaError with Kind set and zero rows inserted. Per ADR-050 §3
// and repo_decomposition_implementation.md §3 (lines 268-276):
// quota is evaluated before any write, the limit problem carries
// limit + observed + docs URL, and nothing is created on a tripped
// cap.
//
// The Cron input carries AppID already resolved against the just-
// inserted apps — the apply handler runs in two passes: first it
// collects the workload → app_id map by inserting each app and
// reading RETURNING, then it inserts crons referencing those IDs.
// Persisting crons by AppID (not by name) avoids re-running the
// lookup inside the Tx and keeps the input shape identical to what
// CreateCronIfUnderQuota produces.
//
// Errors:
//   - *QuotaError: Kind="apps" or "crons"; "crons"+NotAllowed=true
//     when the plan tier has CronLimitPerAccount==0 (Free plan).
//   - ErrConflict: projects_account_slug_uniq 23505, or
//     apps_project_workload_uniq 23505 inside the apply batch.
//   - ErrNotFound: accounts row gone (23503 on project insert).
func (s *PgStore) ApplyProjectPlan(
	ctx context.Context,
	project Project,
	apps []App,
	crons []Cron,
	limits api.Limits,
) (Project, []App, []Cron, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Project{}, nil, nil, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Lock the parent accounts row. SELECT 1 + FOR UPDATE keeps the
	//    lock acquisition in one round-trip; the FOR UPDATE blocks any
	//    concurrent CreateAppIfUnderQuota or ApplyProjectPlan on the
	//    same account until COMMIT/ROLLBACK. apps_account_idx
	//    (account_id, status) is not relevant here — accounts_pkey is.
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from accounts where id = $1 for update`, project.AccountID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Project{}, nil, nil, ErrNotFound
		}
		return Project{}, nil, nil, fmt.Errorf("state: lock account %s: %w", project.AccountID, err)
	}

	// 2. Authoritative deployed-app count under the lock. Same predicate
	//    as CreateAppIfUnderQuota so the MemStore mirror matches.
	var observedApps int
	if err := tx.QueryRow(ctx,
		`select count(*) from apps where account_id = $1
		 and status in ('active','evicted_cold')`,
		project.AccountID,
	).Scan(&observedApps); err != nil {
		return Project{}, nil, nil, fmt.Errorf("state: count apps for account %s: %w", project.AccountID, err)
	}
	if observedApps+len(apps) > limits.DeployedApps {
		return Project{}, nil, nil, &QuotaError{
			Kind:     QuotaErrorKindApps,
			Limit:    limits.DeployedApps,
			Observed: observedApps + len(apps),
		}
	}

	// 3. Cron quota pre-check. Free plan (CronLimitPerAccount==0)
	//    short-circuits with NotAllowed; paid plans compare against
	//    the current count. We join through apps so deleted apps'
	//    crons don't poison the cap (mirrors CreateCronIfUnderQuota).
	//    Skipped entirely when len(crons) == 0 — a Free account with
	//    pre-existing crons (from a prior plan downgrade) must still
	//    be able to apply a cron-less project. PR #454 review F2
	//    finding: the cap check must not block zero-cron applies.
	if len(crons) > 0 {
		if limits.CronLimitPerAccount == 0 {
			return Project{}, nil, nil, &QuotaError{
				Kind:       QuotaErrorKindCrons,
				NotAllowed: true,
			}
		}
		var observedCrons int
		if err := tx.QueryRow(ctx,
			`select count(*) from crons c
			 join apps a on a.id = c.app_id
			 where a.account_id = $1 and a.status <> 'deleted'`,
			project.AccountID,
		).Scan(&observedCrons); err != nil {
			return Project{}, nil, nil, fmt.Errorf("state: count crons for account %s: %w", project.AccountID, err)
		}
		if observedCrons+len(crons) > limits.CronLimitPerAccount {
			return Project{}, nil, nil, &QuotaError{
				Kind:     QuotaErrorKindCrons,
				Limit:    limits.CronLimitPerAccount,
				Observed: observedCrons + len(crons),
			}
		}
	}

	// 4. Insert the project. 23503 (FK violation on account_id) maps
	//    to ErrNotFound (same shape as CreateProject); 23505
	//    (account_slug unique) maps to ErrConflict via mapErr.
	scanSrc := project.ScanSource
	if scanSrc == "" {
		scanSrc = ProjectScanSourceUnknown
	}
	projRow := tx.QueryRow(ctx, `
		insert into projects
		    (account_id, slug, repo_full_name, production_branch, install_id, scan_source)
		values ($1, $2, $3, $4, $5, $6)
		returning id, account_id, slug, coalesce(repo_full_name,''),
		          coalesce(production_branch,''), coalesce(install_id,0),
		          scan_source, created_at, updated_at
	`,
		project.AccountID, project.Slug, nullString(project.RepoFullName),
		nullString(project.ProductionBranch), project.InstallID, string(scanSrc),
	)
	insertedProject, err := scanProject(projRow)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
			return Project{}, nil, nil, fmt.Errorf("%w: %s", ErrNotFound, pgErr.ConstraintName)
		}
		return Project{}, nil, nil, mapErr(err)
	}

	// 5. Insert apps one at a time, inside the same Tx, populating
	//    the workload fields. Reuse appsSelectColumns so the column
	//    list stays in one place (scanAppInto is the matching
	//    positional reader). 23505 on apps_project_workload_uniq maps
	//    to ErrConflict — handlers must dedupe workload_name upstream.
	insertedApps := make([]App, 0, len(apps))
	for _, a := range apps {
		manifest := a.Manifest
		if manifest.IsZero() {
			manifest = AppManifest{}
		}
		manifestBytes, _ := json.Marshal(manifest)
		runtime := nullString(a.Runtime)
		idle := nullableInt(a.IdleTimeoutS)
		// Same rationale as CreateApp: coerce MaxConcurrency <= 0
		// to 1 so the NOT NULL CHECK (>= 1) is satisfied. Autoscale
		// "disabled" is autoscale_target_rps / autoscale_target_cpu_pct,
		// not max_concurrency.
		maxConcurrency := a.MaxConcurrency
		if maxConcurrency <= 0 {
			maxConcurrency = 1
		}
		// Coerce RAMMB <= 0 to 128 (Free plan minimum) so the NOT NULL
		// CHECK (ram_mb > 0) is satisfied (matches CreateApp /
		// CreateAppIfUnderQuota).
		ramMB := a.RAMMB
		if ramMB <= 0 {
			ramMB = 128
		}
		// Coerce Type=="" to AppTypeApp so the NOT NULL CHECK
		// (type IN ('app','function')) is satisfied (matches CreateApp).
		appType := a.Type
		if appType == "" {
			appType = AppTypeApp
		}
		insertAppSQL := `insert into apps
		    (account_id, slug, type, runtime, ram_mb, idle_timeout_s, max_concurrency,
		     status, manifest, min_instances, egress_allowlist, public_auth_ip_allowlist,
		     project_id, root_dir, workload_name, workload_class, start_command,
			     preview_of_slug, preview_pr_number, preview_pr_state, preview_expires_at)
		values ($1, $2, $3, $4, $5, $6, $7, 'active', $8::jsonb, $9, $10::cidr[], $11::cidr[],
		        $12, $13, $14, $15, $16, $17, $18, $19, $20)
		returning ` + appsSelectColumns
		row := tx.QueryRow(ctx, insertAppSQL,
			project.AccountID, a.Slug, string(appType), runtime, ramMB, idle, maxConcurrency,
			manifestBytes, a.MinInstances, cidrPrefixesToArray(a.EgressAllowlist), cidrPrefixesToArray(a.PublicAuthIPAllowlist),
			insertedProject.ID, a.RootDir, a.WorkloadName, string(a.WorkloadClass),
			nullString(a.StartCommand),
			// Issue #272 / ADR-094: preview columns default to
			// NULL on ApplyProjectPlan — repo-decomposed
			// projects never carry preview metadata at create
			// time. The preview path provisions rows via
			// CreateApp / CreateAppIfUnderQuota directly.
			nullString(a.PreviewOfSlug), a.PreviewPrNumber,
			nullString(a.PreviewPrState), nullableTimestamptzPtr(a.PreviewExpiresAt),
		)
		app, err := scanApp(row)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
				return Project{}, nil, nil, ErrConflict
			}
			return Project{}, nil, nil, err
		}
		insertedApps = append(insertedApps, app)
	}

	// 6. Insert crons attached to the freshly inserted apps. AppID is
	//    set by the caller; an empty AppID is treated as "deferred"
	//    so the apply handler can resolve the workload-name → ID map
	//    from the returned insertedApps and re-insert via CreateCron.
	//    The cron quota check (step 3) already ran so a deferred cron
	//    cannot bypass it; the worst case on a name-resolution bug is
	//    a missing cron row + a 500 the handler can retry.
	insertedCrons := make([]Cron, 0, len(crons))
	for _, c := range crons {
		if c.AppID == "" {
			continue // deferred to the handler
		}
		row := tx.QueryRow(ctx,
			`insert into crons (app_id, schedule, path, enabled) values ($1, $2, $3, $4)
			 returning id, app_id, schedule, path, enabled, created_at`,
			c.AppID, c.Schedule, c.Path, c.Enabled,
		)
		var out Cron
		if err := row.Scan(&out.ID, &out.AppID, &out.Schedule, &out.Path, &out.Enabled, &out.CreatedAt); err != nil {
			return Project{}, nil, nil, mapErr(err)
		}
		insertedCrons = append(insertedCrons, out)
	}

	if err := tx.Commit(ctx); err != nil {
		return Project{}, nil, nil, fmt.Errorf("state: commit apply project plan: %w", err)
	}
	return insertedProject, insertedApps, insertedCrons, nil
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
		`select min(github_install_id)
		 from apps
		 where github_repo_full_name = $1
		   and github_install_id is not null
		 having count(distinct github_install_id) = 1`, repoFullName,
	).Scan(&installID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return installID, nil
}

// UpsertGithubInstallBinding persists the (account → app → install,
// repo, branch) edge on the apps row. PR-B (ADR-012 closure). The
// (account_id, binding_id) unique partial index added in migration
// 00047 makes the upsert idempotent on retry: a second call with the
// same payload overwrites the prior values without a new row.
//
// The zero/empty BindingID is rejected so the dashboard's bind flow
// always carries a deterministic id (it builds "bind-<appID>-<repo>"
// client-side). A blank appID returns ErrNotFound rather than a
// silent 0-row update so the dashboard's "app not found" path
// stays correct.
func (s *PgStore) UpsertGithubInstallBinding(ctx context.Context, b GitHubBinding) error {
	if b.AppID == "" {
		return ErrNotFound
	}
	if b.BindingID == "" {
		return fmt.Errorf("state: BindingID required")
	}
	if b.AccountID == "" {
		return fmt.Errorf("state: AccountID required")
	}
	if b.LinkedAt.IsZero() {
		b.LinkedAt = time.Now()
	}
	tag, err := s.pool.Exec(ctx,
		`update apps
		 set github_install_id = $2,
		     github_repo_full_name = $3,
		     github_production_branch = $4,
		     github_install_binding_id = $5,
		     github_install_account_id = $6,
		     github_install_linked_at = $7
		 where id = $1 and account_id = $6`,
		b.AppID, b.InstallID, nullString(b.RepoFullName), nullString(b.ProductionBranch),
		b.BindingID, b.AccountID, b.LinkedAt,
	)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteGithubInstallBinding clears the (install, repo, branch,
// binding, account, linked_at) columns on an app. Idempotent: a
// no-prior-binding row updates zero rows and returns nil so the
// dashboard's "unbind" action is safe to retry. Returns ErrNotFound
// when the app row itself is missing — the caller can distinguish
// "not bound" from "app not found".
func (s *PgStore) DeleteGithubInstallBinding(ctx context.Context, appID string) error {
	if appID == "" {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx,
		`update apps
		 set github_install_id = null,
		     github_repo_full_name = null,
		     github_production_branch = null,
		     github_install_binding_id = null,
		     github_install_account_id = null,
		     github_install_linked_at = null
		 where id = $1`,
		appID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertGitHubInstall persists the durable OAuth handshake state
// for one account's GitHub App install (PR-C, audit-gap closure).
// Idempotent on (AccountID, InstallationID) via the composite conflict key
// so the OAuth flow can retry without crashing on the PK. The FK
// to accounts(id) ON DELETE CASCADE makes this the §17 G2 GDPR
// path — deleting an account removes its install row in the same
// tx. SealedToken is written verbatim (bytea); the plaintext
// token was sealed by githubd via pkg/secretbox.SealOne before
// it ever reached this method, so the database never sees a
// plaintext "ghs_…" value.
//
// AccountID is required; a blank input returns ErrNotFound so the
// caller can distinguish "unknown account" from a transient write
// failure without parsing SQL error messages.
func (s *PgStore) UpsertGitHubInstall(ctx context.Context, inst GitHubInstall) error {
	if inst.AccountID == "" {
		return ErrNotFound
	}
	if inst.AuditGithubLogin == "" {
		return fmt.Errorf("state: AuditGithubLogin required (§11 paper trail)")
	}
	if inst.SealedAt.IsZero() {
		inst.SealedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx,
		`insert into github_installations
		     (account_id, installation_id, default_branch,
		      sealed_install_token, token_expires_at, sealed_at,
		      audit_github_login)
		 values ($1::uuid, $2, $3, $4, $5, $6, $7)
		 on conflict (account_id, installation_id) do update set
		     default_branch        = excluded.default_branch,
		     sealed_install_token  = excluded.sealed_install_token,
		     token_expires_at      = excluded.token_expires_at,
		     sealed_at             = excluded.sealed_at,
		     audit_github_login    = excluded.audit_github_login`,
		inst.AccountID, inst.InstallationID, inst.DefaultBranch,
		inst.SealedToken, inst.TokenExpiresAt, inst.SealedAt,
		inst.AuditGithubLogin,
	)
	return err
}

// GitHubInstallForAccount reads the durable install row for an
// account. Used by githubd's cold-start rehydrate path; on a miss,
// returns ErrNotFound so the caller can distinguish "the OAuth
// handshake hasn't happened yet" from "the database is down".
// Translates pgx.ErrNoRows to ErrNotFound at the boundary so the
// caller can use errors.Is(err, state.ErrNotFound) directly.
func (s *PgStore) GitHubInstallForAccount(ctx context.Context, accountID string) (GitHubInstall, error) {
	if accountID == "" {
		return GitHubInstall{}, ErrNotFound
	}
	var inst GitHubInstall
	err := s.pool.QueryRow(ctx,
		`select installation_id, default_branch,
		        sealed_install_token, token_expires_at, sealed_at,
		        audit_github_login
		   from github_installations
		  where account_id = $1::uuid
		  order by sealed_at desc, installation_id desc
		  limit 1`,
		accountID,
	).Scan(
		&inst.InstallationID, &inst.DefaultBranch,
		&inst.SealedToken, &inst.TokenExpiresAt, &inst.SealedAt,
		&inst.AuditGithubLogin,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubInstall{}, ErrNotFound
		}
		return GitHubInstall{}, err
	}
	inst.AccountID = accountID
	return inst, nil
}

// GitHubInstallForAccountInstallation returns the exact account/install row.
// It is the authorization lookup for multi-install list, bind, source-fetch,
// and webhook paths; a mismatch fails closed as ErrNotFound.
func (s *PgStore) GitHubInstallForAccountInstallation(ctx context.Context, accountID string, installationID int64) (GitHubInstall, error) {
	if accountID == "" || installationID <= 0 {
		return GitHubInstall{}, ErrNotFound
	}
	var inst GitHubInstall
	err := s.pool.QueryRow(ctx,
		`select installation_id, default_branch,
		        sealed_install_token, token_expires_at, sealed_at,
		        audit_github_login
		   from github_installations
		  where account_id = $1::uuid and installation_id = $2`,
		accountID, installationID,
	).Scan(
		&inst.InstallationID, &inst.DefaultBranch,
		&inst.SealedToken, &inst.TokenExpiresAt, &inst.SealedAt,
		&inst.AuditGithubLogin,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubInstall{}, ErrNotFound
		}
		return GitHubInstall{}, err
	}
	inst.AccountID = accountID
	return inst, nil
}

// GetGithubInstallBindingForApp returns the (appID, accountID)
// bind row. accountID scopes the lookup so a forged session cannot
// read another tenant's binding — when the app is bound but to a
// different account, returns ErrNotFound. The (unbound app) case
// also returns ErrNotFound. Uses the apps_account_idx composite
// index (apps.account_id, apps.status) for the account check.
func (s *PgStore) GetGithubInstallBindingForApp(ctx context.Context, appID, accountID string) (GitHubBinding, error) {
	if appID == "" || accountID == "" {
		return GitHubBinding{}, ErrNotFound
	}
	var b GitHubBinding
	var installID *int64
	var branch *string
	var bindingID *string
	var linkedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`select id, github_install_id, github_repo_full_name, github_production_branch,
		        github_install_binding_id, github_install_account_id, github_install_linked_at
		 from apps
		 where id = $1
		   and account_id = $2
		   and github_install_id is not null`, appID, accountID,
	).Scan(&b.AppID, &installID, &b.RepoFullName, &branch, &bindingID, &b.AccountID, &linkedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubBinding{}, ErrNotFound
		}
		return GitHubBinding{}, err
	}
	if installID != nil {
		b.InstallID = *installID
	}
	if branch != nil {
		b.ProductionBranch = *branch
	}
	if bindingID != nil {
		b.BindingID = *bindingID
	}
	if linkedAt != nil {
		b.LinkedAt = *linkedAt
	}
	return b, nil
}

// GithubInstallBindingForRepoBranch is the inbound-webhook dispatch
// lookup. githubd's push receiver uses it to find the owning app
// from a (repo, branch) push. Uses the
// apps_github_install_repo_branch_idx partial index from migration
// 00047. Returns ErrNotFound when no app is bound to that pair.
func (s *PgStore) GithubInstallBindingForRepoBranch(ctx context.Context, repoFullName, productionBranch string) (GitHubBinding, error) {
	return s.githubInstallBindingForRepoBranch(ctx, repoFullName, productionBranch, 0)
}

func (s *PgStore) GithubInstallBindingForRepoBranchInstallation(ctx context.Context, repoFullName, productionBranch string, installationID int64) (GitHubBinding, error) {
	if installationID <= 0 {
		return GitHubBinding{}, ErrNotFound
	}
	return s.githubInstallBindingForRepoBranch(ctx, repoFullName, productionBranch, installationID)
}

func (s *PgStore) githubInstallBindingForRepoBranch(ctx context.Context, repoFullName, productionBranch string, installationID int64) (GitHubBinding, error) {
	if repoFullName == "" {
		return GitHubBinding{}, ErrNotFound
	}
	if productionBranch == "" {
		productionBranch = "main"
	}
	var b GitHubBinding
	var installID *int64
	var branch *string
	var accountID *string
	var bindingID *string
	var linkedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`select id, github_install_account_id, github_install_binding_id, github_install_linked_at,
		        github_install_id, github_repo_full_name, github_production_branch
		 from apps
		 where github_repo_full_name = $1
		   and github_production_branch = $2
		   and github_install_id is not null
		   and ($3::bigint = 0 or github_install_id = $3)
		 order by id
		 limit 1`, repoFullName, productionBranch, installationID,
	).Scan(&b.AppID, &accountID, &bindingID, &linkedAt, &installID, &b.RepoFullName, &branch)
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
	if accountID != nil {
		b.AccountID = *accountID
	}
	if bindingID != nil {
		b.BindingID = *bindingID
	}
	if linkedAt != nil {
		b.LinkedAt = *linkedAt
	}
	if branch != nil {
		b.ProductionBranch = *branch
	}
	return b, nil
}

// ListGithubInstallBindingsForAccount returns the map[appID]GitHubBinding
// the dashboard uses to render the per-app bind state. Uses the
// apps_github_install_account_idx partial index. Returns an empty
// (non-nil) map when the account has no bindings.
func (s *PgStore) ListGithubInstallBindingsForAccount(ctx context.Context, accountID string) (map[string]GitHubBinding, error) {
	out := make(map[string]GitHubBinding)
	if accountID == "" {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, github_install_id, github_repo_full_name, github_production_branch,
		        github_install_binding_id, github_install_linked_at
		 from apps
		 where github_install_account_id = $1
		   and github_install_id is not null`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b GitHubBinding
		b.AccountID = accountID
		var installID *int64
		var branch *string
		var bindingID *string
		var linkedAt *time.Time
		if err := rows.Scan(&b.AppID, &installID, &b.RepoFullName, &branch, &bindingID, &linkedAt); err != nil {
			return nil, err
		}
		if installID != nil {
			b.InstallID = *installID
		}
		if branch != nil {
			b.ProductionBranch = *branch
		}
		if bindingID != nil {
			b.BindingID = *bindingID
		}
		if linkedAt != nil {
			b.LinkedAt = *linkedAt
		}
		out[b.AppID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
//
// PR-B folds the prior-deployment supersede INTO this transaction so
// the apid → state boundary can never leave a live deployment as
// 'superseded' with no replacement. The previous "supersede then
// create" two-step bug (only on the image: branch — the tarball
// branch never superseded at all) is closed by reading the latest
// non-superseded-non-failed row under FOR UPDATE, marking it
// 'superseded', and inserting the new row, all in the same tx. A
// concurrent CreateDeployment against the same app serialises behind
// the row lock (Step 2.5 below); if our subsequent INSERT fails the
// defer tx.Rollback reverts both writes together.
func (s *PgStore) CreateDeployment(ctx context.Context, d Deployment) (Deployment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	if d.RolloutState == "" {
		d.RolloutState = "pending"
	}
	serviceRollout := IsServiceRollout(d)
	if serviceRollout && d.RolloutStartedAt == nil {
		now := time.Now().UTC()
		d.RolloutStartedAt = &now
	}

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
	if d.CanaryTotalSteps <= 0 && !serviceRollout {
		// Stable creation may end an active canary, so lock the whole
		// live set in deterministic id order before the prior-row
		// lookup. This preserves the lock ordering used by traffic
		// updates and avoids a create-vs-rebalance deadlock.
		rows, err := tx.Query(ctx,
			`select id from deployments
			  where app_id = $1 and status = 'live'
			  order by id for update`, d.AppID)
		if err != nil {
			return Deployment{}, fmt.Errorf("state: lock live deployments: %w", err)
		}
		for rows.Next() {
			var ignored string
			if err := rows.Scan(&ignored); err != nil {
				rows.Close()
				return Deployment{}, fmt.Errorf("state: scan live deployment lock: %w", err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Deployment{}, fmt.Errorf("state: iterate live deployment locks: %w", err)
		}
		rows.Close()
	}

	// 2. Lock + supersede the prior live/pending row, if any. PR-B: the
	//    supersede is in-tx so a failed INSERT below rolls it back too.
	//    The (app_id, created_at desc) index from migration 00007 covers
	//    the search.
	//
	//    The status set is NARROW ('pending' | 'live') on purpose. A
	//    row in 'building' / 'imaging' / 'snapshotting' represents a
	//    build pipeline already in flight — the prior vmmd VM is
	//    running, builderd is mid-build, imaged is rendering an ext4.
	//    Flipping such a row to 'superseded' would orphan those
	//    pipelines and lose the deployment they were producing.
	//    Instead, the second deploy creates a fresh row, both rows
	//    run their pipelines in parallel, and the second-deploy's
	//    post-build chain (snapshot_prime → schedd → snap-and-park)
	//    wins because the upstream builderd/notif chain only races the
	//    last writer. Termination is handled by schedd's watchdog
	//    idle-reaper; this tx must not race the build itself.
	//
	//    We exclude 'failed' explicitly per PR-A's
	//    LatestSupersededDeployment: failure history stays observable.
	//
	//    Callers that need the just-superseded row (apid's
	//    NotifyDeploymentChanged fan-out) read it BEFORE the call via
	//    LatestDeployment(ctx, appID) — by the time this tx commits,
	//    that row is already visible as 'superseded' to the next read.
	//    The 2-return shape keeps the signature backward-compatible
	//    with pre-PR-B call sites (the slice-3 cascade test on main
	//    relies on `dep, err :=` form).
	//
	//    Stable deployments supersede the prior row here. Canary
	//    deployments intentionally leave it live as the residual traffic
	//    bucket until the canary reaches its terminal stage.
	var priorID string
	if err := tx.QueryRow(ctx,
		`select id from deployments
		  where app_id = $1
		    and status in ('pending','live')
		  order by created_at desc
		  limit 1
		  for update`,
		d.AppID).Scan(&priorID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, fmt.Errorf("state: lock prior deployment: %w", err)
		}
		// pgx.ErrNoRows → no prior; that's fine. Move on.
	} else if d.CanaryTotalSteps <= 0 && !serviceRollout {
		// Issue #556 PR-A: zero the prior row's traffic_percent in
		// the same tx so Σ over live rows remains 100 by construction
		// (the new INSERT defaults traffic_percent to 100 below).
		// Two-write update is intentional: the first write flips
		// status to 'superseded' (the pre-#556 contract); the second
		// zeroes the new column. Combining them into one UPDATE SET
		// list would also work but makes the diff against #556's
		// "minimal blast radius" intent harder to review.
		if _, err := tx.Exec(ctx,
			`update deployments
			    set status = 'superseded', traffic_percent = 0
			  where id = $1`,
			priorID); err != nil {
			return Deployment{}, fmt.Errorf("state: supersede prior %s: %w", priorID, err)
		}
	}
	if d.CanaryTotalSteps <= 0 && !serviceRollout {
		// A stable deployment terminates any active canary overlap as
		// well as the newest prior row. The app-level partial unique
		// index requires every other live revision to be gone before
		// this deployment is activated.
		if _, err := tx.Exec(ctx,
			`update deployments
			    set status = 'superseded', traffic_percent = 0
			  where app_id = $1 and status = 'live'`,
			d.AppID); err != nil {
			return Deployment{}, fmt.Errorf("state: supersede live canary siblings: %w", err)
		}
	}

	// 3. Insert the new deployment row. The FOR UPDATE above guarantees
	//    the app cannot transition to AppDeleted between Steps 1 and 4
	//    (COMMIT). If INSERT fails, the prior UPDATE at Step 2 is rolled
	//    back by the defer.
	// Tier 3 (issue #197 B3.10) — include source_url / commit_sha in the
	// new-row insert so the Deployment DTO Scan in scanDeployment has the
	// 15 destinations it expects (the failing test
	// TestPgStore_InstancesStateCheck_RejectsInjection got "got 13 and 15"
	// before this fix). Both columns are nullable; empty string on the
	// write side mirrors the rest of the read-side coalesce shape.
	//
	// Issue #460 / ADR-053: include the six override_* columns. Empty
	// text[] is signalled by the caller passing a nil []string — pgx
	// marshals nil to NULL which the column accepts (nullable).
	// jsonb columns accept NULL too; the handler marshals an empty
	// map to "{}" rather than NULL so a downstream consumer never
	// has to branch on "is the jsonb column populated but the JSON
	// string empty?".
	// Issue #556 PR-A: traffic_percent is read from d.TrafficPercent
	// (set by the handler to 100 when the caller omits the optional
	// pointer, 0..100 otherwise) and written into the new row. The
	// prior supersede above stamped 0 on the predecessor so Σ over
	// live rows stays 100 by construction. The server-side default
	// (NOT NULL DEFAULT 100) handles the empty-input case at the
	// schema layer; the explicit write here mirrors the
	// operator-supplied case.
	//
	// Defensive default for stable deployments: if a caller hands us
	// d.TrafficPercent=0 (the Go zero value, or the wire-omitted case),
	// stamp 100 here. A canary's zero can be a meaningful custom first
	// stage, and the APID handler has already copied that stage onto the
	// row. Mirrors memstore.CreateDeployment while preserving the schema
	// NOT NULL DEFAULT 100 contract for non-canary rows.
	if d.TrafficPercent == 0 && d.CanaryTotalSteps <= 0 && !serviceRollout {
		d.TrafficPercent = 100
	}
	row := tx.QueryRow(ctx,
		`insert into deployments (app_id, image_digest, kind, source_path, source_root, source_bytes, handler, log_path, source_url, commit_sha,
		                          override_entrypoint, override_cmd, override_env, override_env_secrets, override_port, override_healthcheck,
		                          override_liveness_probe,
			                          sidecars,
			                          status,
		                          min_instances,
		                          traffic_percent,
		                          rollout_state,
		                          rollout_started_at,
		                          scope,
		                          deployed_by_user_id, deployed_via, deployed_from_ip, pusher_login,
		                          reason, tag, deployed_by, pr_number, workflows,
		                          full_rootfs_allow_auto, full_rootfs_override)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, 'pending', $19, $20, $21, $22, coalesce(nullif($23, ''), 'default'),
		         nullif($24, '')::uuid, coalesce(nullif($25, ''), 'api'), nullif($26, '')::inet, nullif($27, ''),
		         $28, $29, $30, nullif($31, 0), $32, $33, $34)
		 returning `+deploymentSelectColumnsWithRootfs,
		d.AppID, d.ImageDigest, string(d.Kind), nullString(d.SourcePath), nullString(d.SourceRoot), d.SourceBytes,
		nullString(d.Handler), nullString(d.LogPath),
		nullString(d.SourceURL), nullString(d.CommitSHA),
		d.OverrideEntrypoint, d.OverrideCmd,
		nullJSONRaw(d.OverrideEnv), nullJSONRaw(d.OverrideEnvSecrets),
		nullableOverridePort(d.OverridePort), nullJSONRaw(d.OverrideHealthcheck),
		nullJSONRaw(d.OverrideLivenessProbe),
		notNullEmptyJSONRaw(d.Sidecars),
		d.MinInstances,
		d.TrafficPercent,
		d.RolloutState,
		d.RolloutStartedAt,
		// ADR-091 / PR-D: empty caller Scope collapses to the
		// literal 'default' (matches the schema DEFAULT). A non-empty
		// Scope is passed through verbatim. Mirrors the handler's
		// scope-default collapse so pgstore never inserts a literal
		// '' (which would fail the deployments_scope_shape CHECK).
		d.Scope,
		// Issue #606 — actor attribution columns (migration 00305).
		// Each of the four empty-string Go values collapses to NULL
		// via nullif() so an "anonymous / pre-FK / GitHub-push"
		// caller (or a Go-zero struct from a test) never inserts
		// a literal '' that would trip the deployments_deployed_via
		// CHECK or the FK's NOT-VALID-UUID parse path. The
		// coalesce on deployed_via is the backstop for the
		// NOT NULL DEFAULT 'api' contract — a caller that omits
		// DeployedVia entirely (empty string after the nullif)
		// still gets 'api' rather than NULL, so pre-feature rows
		// stay valid without a backfill.
		d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin,
		// Issue #977 / ADR-116: annotation columns. reason / tag /
		// deployed_by are nullable text; pr_number uses nullif($N, 0)
		// so a Go-zero PRNumber (the wire-omitted case from a test
		// or a future handler that forgets to set the field)
		// collapses to NULL rather than tripping the
		// deployments_pr_number_positive_chk CHECK (which rejects 0).
		nullString(d.Reason), nullString(d.Tag), nullString(d.DeployedBy), d.PRNumber,
		notNullEmptyJSONRaw(d.Workflows),
		d.FullRootfsAllowAuto, d.FullRootfsOverride)
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
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where id = $1`, id)
	return scanDeploymentWithRootfs(row)
}

func (s *PgStore) LatestDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where app_id = $1 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

// DeploymentOrdinal (issue #976 / ADR-122 / SAFE-RELEASES-C.2) returns
// the per-app 1-based rank of the deployment row ordered by
// (created_at, id). Mirrors memstore's sort: id breaks the
// created_at tie so two rows stamped in the same millisecond
// resolve to a stable rank.
//
// Uses a window function rather than the COUNT(*) over a sub-query
// for two reasons: (1) the per-deployment ordinal is read on every
// /v1/deployments/{id}/url call, so the inner ORDER BY gives the
// optimizer a stable plan shape; (2) COUNT(*) recomputes on every
// rank row, which scales as O(N²) for an app with N deploys — a
// concern once the platform reaches the §6.1 max_concurrency
// ceilings (Scale plan = 20+ deploys per app). The window function
// is O(N log N) and uses the index on (app_id, created_at) added
// in migration 00006.
func (s *PgStore) DeploymentOrdinal(ctx context.Context, appID, deploymentID string) (int, error) {
	var ord int
	err := s.pool.QueryRow(ctx,
		`select ord from (
		   select id, app_id, row_number() over (partition by app_id order by created_at, id) as ord
		   from deployments
		 ) ranks
		 where id = $1 and app_id = $2`, deploymentID, appID).Scan(&ord)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("deployment ordinal: %w", err)
	}
	return ord, nil
}

func (s *PgStore) LiveDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where app_id = $1 and status = 'live'
		 order by (traffic_percent > 0) desc, created_at desc, id desc limit 1`, appID)
	return scanDeploymentWithRootfs(row)
}

// LiveDeploymentForScope (ADR-091 / PR-D) returns the newest live
// deployment for the (app_id, scope) pair. Stable deployments remain
// unique in Postgres, while an active canary deliberately overlaps its
// predecessor (issue #976); ordering keeps the wake path deterministic
// and selects the canary revision for new capacity. Returns ErrNotFound
// when no live row exists for the scope — the wake path converts that
// into a 404 via the ErrNoDeployment sentinel already used by
// LiveDeployment.
func (s *PgStore) LiveDeploymentForScope(ctx context.Context, appID, scope string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where app_id = $1 and scope = $2 and status = 'live'
		 order by (traffic_percent > 0) desc, created_at desc, id desc limit 1`, appID, scope)
	return scanDeploymentWithRootfs(row)
}

// LiveDeployments (issue #556 / PR-B) returns every row where
// app_id=$1 AND status='live', ordered created_at DESC. Used by the
// gateway's per-deployment weighted picker: on every NotifyDeploymentChanged
// the gateway reads the full live set to rebuild its (deployment_id,
// traffic_percent) cache. The query is index-only against the
// deployments_live_traffic_idx partial index added in migration 00162
// (the INCLUDE clause carries traffic_percent + id so the planner
// never touches the heap).
//
// Returns (nil, nil) when the app has no live rows — the gateway
// treats that as "no live deployment, 503". Per-row errors during
// the rows.Next() loop are propagated by scanDeployments via the
// returned error (no partial result is returned on failure).
//
// Determinism note: created_at is the canonical sort key. The
// (app_id, created_at desc) index from migration 00007 covers the
// search; the planner switches to deployments_live_traffic_idx when
// the partial predicate is selective (typical: one or two live rows
// per app, vs. O(N) total rows).
func (s *PgStore) LiveDeployments(ctx context.Context, appID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments
		 where app_id = $1 and status = 'live'
		 order by created_at desc`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list live deployments app=%s: %w", appID, err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListCanaryInFlight (issue #976 / ADR-122 / SAFE-RELEASES-A + F)
// returns every deployment that is mid-canary across all apps:
// status='live' AND canary_total_steps > 0 AND canary_step <
// canary_total_steps AND rollout_state IN ('pending','rolling_out').
// The canary_progression meterd tick walks the per-deployment
// ladder; the safe-deploy orchestrator walks the rollout-state
// machine. Both walk this same set — the indexes on (status,
// canary_total_steps) and on (rollout_state) keep the scan
// sub-millisecond at fleet scale.
//
// Uses a partial-index-friendly predicate: the planner can switch
// to deployments_live_traffic_idx for the status='live' slice. No
// new index needed for this PR; the column defaults + status
// index from migration 00162 are sufficient.
func (s *PgStore) ListCanaryInFlight(ctx context.Context) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments
		 where status = 'live'
		   and canary_total_steps > 0
		   and canary_step < canary_total_steps
		   and rollout_state in ('pending','rolling_out')
		 order by created_at asc`)
	if err != nil {
		return nil, fmt.Errorf("state: list canary in-flight: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// SafedeployListPendingRollouts (issue #976 / ADR-122 /
// SAFE-RELEASES-F) walks the orchestrator's tick set. The
// predicate is a strict superset of ListCanaryInFlight: it
// includes rows with canary_total_steps=0 (the "no canary ladder"
// case where rollout_state still walks pending → complete on
// first tick). The orchestrator skips those rows internally when
// advancing the state machine, but they still need to appear
// here so the orchestrator can stamp rollout_completed_at on
// them.
//
// Ordering is (rollout_started_at NULLS FIRST, created_at ASC):
// a brand-new pending row walks first (no started_at yet), then
// in-flight rolling_out rows in FIFO order. The fairness
// property matches the alert evaluator's per-rule-walk shape so
// the operator dashboard's "oldest pending" panel reads true.
func (s *PgStore) SafedeployListPendingRollouts(ctx context.Context) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments
		 where rollout_state in ('pending','rolling_out')
		   and status = 'live'
		   and not (canary_total_steps = 0 and rollout_state = 'rolling_out')
		 order by rollout_started_at asc nulls first, created_at asc`)
	if err != nil {
		return nil, fmt.Errorf("state: safedeploy list pending rollouts: %w", err)
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// SafedeployStampRollout (issue #976 / ADR-122 / SAFE-RELEASES-F)
// is the atomic-write primitive the orchestrator uses to
// transition rollout_state. The write is a single UPDATE that
// moves the (rollout_state, rollout_started_at,
// rollout_completed_at, rollout_aborted_at, rollout_aborted_reason)
// tuple in lock-step — partial writes would leave the row in a
// half-finished state the next ListPendingRollouts walk would
// re-pick.
//
// Locking: takes FOR UPDATE on the deployment row inside a tx so
// a concurrent orchestrator tick (or the AlertEvaluator's
// ActionDispatcher demote/promote path) cannot interleave. The
// audit emit is the caller's responsibility — the orchestrator
// calls AppendDeploymentAudit explicitly so the audit row
// carries the orchestrator's actor sentinel.
//
// Returns the post-write row so the orchestrator can decide
// whether to emit additional audit fields (e.g. the rollout's
// terminal canary step when transitioning to 'complete').
func (s *PgStore) SafedeployStampRollout(ctx context.Context, id string, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: safedeploy stamp begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	row := tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where id = $1
		 for update`, id)
	dep, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: safedeploy stamp load: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`update deployments set
			rollout_state = $2,
			rollout_started_at = $3,
			rollout_completed_at = $4,
			rollout_aborted_at = $5,
			rollout_aborted_reason = $6
		 where id = $1`,
		id, rolloutState, startedAt, completedAt, abortedAt, abortedReason); err != nil {
		return Deployment{}, fmt.Errorf("state: safedeploy stamp update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: safedeploy stamp commit: %w", err)
	}
	return dep, nil
}

// CountLiveInstancesByDeployment returns the number of instances in
// {WAKING, COLD_BOOTING, RUNNING} for the given deployment_id (issue
// #555 PR-6). The DeploymentCounterWatcher
// (pkg/sched/deployment_counter_watcher.go) uses this to detect the
// "last live instance parked" transition. The SQL is a single
// count(*) against the existing deployment_id column (ADR-072); no
// new index needed at the deployment_id cardinality we expect.
//
// The state strings are lowercase to match the convention in the
// instances_state_check constraint (migrations/00020_instance_evicting_state.sql)
// and the SQL writes in MarkInstanceMigrating / ListLiveInstancesOnNode.
func (s *PgStore) CountLiveInstancesByDeployment(ctx context.Context, deploymentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from instances
		where deployment_id = $1
		  and state in ('waking', 'cold_booting', 'running')
	`, deploymentID).Scan(&n)
	return n, err
}

func (s *PgStore) LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where app_id = $1 and status = 'superseded'
		 order by created_at desc limit 1`, appID)
	return scanDeployment(row)
}

// GetDeploymentByIDScopedToSuperseded returns the deployment only if it
// (a) belongs to appID and (b) has status='superseded'. Used by SAFE-RELEASES-G
// (issue #976, PR-G) to let callers rollback to a specific historical
// deployment rather than only the most-recent superseded one.
//
// Returns ErrNoRollbackTarget if no row matches (deployment missing or
// belongs to a different app). Returns ErrRollbackTargetAlreadyLive if
// the row exists and belongs to appID but its status is not 'superseded'
// (e.g. status='live' means caller is asking to "rollback" to the
// already-current deployment — rejected explicitly rather than silently
// no-op'd, per the SAFE-RELEASES-G plan).
//
// Uses scanDeploymentWithRootfs (matches DeploymentByID) so the caller has
// the rootfs_path/key/bytes needed for downstream wake and audit. The
// rollback handler deliberately does NOT add a "snapshot must exist" gate
// here — per ADR-005 ("cold boot must always work") and CLAUDE.md invariant
// #3, the wake path cold-boots from the returned rootfs when the rollback
// target's snapshot is missing/stale, so this loader is purely a state
// lookup and intentionally not coupled to snapshot retention.
func (s *PgStore) GetDeploymentByIDScopedToSuperseded(ctx context.Context, appID, deploymentID string) (Deployment, error) {
	if appID == "" {
		return Deployment{}, fmt.Errorf("state: get deployment by id scoped to superseded: empty appID")
	}
	if deploymentID == "" {
		return Deployment{}, fmt.Errorf("state: get deployment by id scoped to superseded: empty deploymentID")
	}
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where id = $1 and app_id = $2`,
		deploymentID, appID)
	d, scanErr := scanDeploymentWithRootfs(row)
	if scanErr != nil {
		if errors.Is(scanErr, ErrNotFound) {
			return Deployment{}, fmt.Errorf("state: rollback target %q for app %q: %w", deploymentID, appID, ErrNoRollbackTarget)
		}
		return Deployment{}, fmt.Errorf("state: get deployment by id scoped to superseded: %w", scanErr)
	}
	if d.Status != DeploySuperseded {
		return Deployment{}, fmt.Errorf("state: rollback target %q for app %q has status %q: %w",
			deploymentID, appID, d.Status, ErrRollbackTargetAlreadyLive)
	}
	return d, nil
}

// HasSnapshotHistory reports whether the snapshots table contains any
// row (stale or non-stale) for the deployment. Used by the rollback
// handler (SAFE-RELEASES-G) to gate the snapshot-GC race check: if the
// deployment has never had a snapshot (typical for test-only or
// freshly-created deployments that haven't been snapshotted yet), a
// "no non-stale snapshot" lookup is not meaningful — the handler skips
// the race check. If the deployment DOES have snapshot history but no
// non-stale row remains, the GC race is real and the handler returns
// ErrRollbackTargetSnapshotGone (409).
func (s *PgStore) HasSnapshotHistory(ctx context.Context, deploymentID string) (bool, error) {
	if deploymentID == "" {
		return false, fmt.Errorf("state: has snapshot history: empty deploymentID")
	}
	var exists bool
	err := s.pool.QueryRow(ctx,
		`select exists(select 1 from snapshots where deployment_id = $1)`,
		deploymentID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("state: has snapshot history: %w", err)
	}
	return exists, nil
}

// ListAllDeployments returns every non-deleted deployment (parent
// app is not 'deleted'). Issue #557 closure / ADR-072 — the floor
// reconciler's wake sweep walks this list when no owner-node sharding
// is configured (the one-box posture). The single-box posture
// reads it; multi-box reads ListDeploymentsByNodeID.
//
// The filter on apps.status excludes deployments whose parent app
// has been soft-deleted (status='deleted'). The deployment rows
// themselves do not carry a `deleted` flag — the deployment is
// treated as inert once its parent app is gone. The JOIN through
// apps uses the apps_pkey index (primary key) so the planner
// resolves it as a nested-loop with an inner index scan per
// deployment row. At v1 scale (O(deploy rate × app lifetime)
// per the ListDeploymentsForApp comment) this stays sub-10ms.
func (s *PgStore) ListAllDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsQualified+`
		   from deployments d
		   join apps a on a.id = d.app_id
		  where a.status <> 'deleted'
		  order by d.created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListDeploymentsByNodeID returns every deployment whose parent
// app's owner_node is the given compute_nodes.id. Phase 2 / Gate A
// / issue #557 closure — the floor trigger's owner-shard walk.
// Mirrors ListAppsByNodeID and ListOwnedCronsByNodeID. The planner
// hits apps_node_id_idx first and then does a nested-loop on
// deployments.app_id via the deployments_app_id_idx index (added in
// migration 00007). At v1 scale (one box, ~100 apps × ~10 deploys)
// the plan stays a single index scan + nested loop, sub-5ms.
func (s *PgStore) ListDeploymentsByNodeID(ctx context.Context, nodeID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsQualified+`
		   from deployments d
		   join apps a on a.id = d.app_id
		  where a.node_id = $1
		    and a.status <> 'deleted'
		  order by d.created_at desc`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ConcurrencyForDeployment returns the live-instance count for a
// (app, deployment) pair. Used by the floor trigger's per-deployment
// floor arithmetic and the reaper's per-deployment idle floor
// check. The three live states (RUNNING, WAKING, COLD_BOOTING) match
// pkg/state/machine.go CountsForConcurrency. PARKING / PARKED /
// STOPPED do not count (they're shutting down or idle).
//
// Backed by the partial index `instances_app_deployment_idx`
// (migration 00132) which restricts the index to the three live
// states. A pre-00132 deploy has the index in place but the
// instances.deployment_id column may be NULL on legacy rows — the
// predicate `deployment_id = $2` excludes those rows from the
// match, which under-counts but is safe (the trigger floors on
// max(), so under-count means the trigger wakes one extra — the
// engine's NodeLedger.Admit remains the absolute backstop).
func (s *PgStore) ConcurrencyForDeployment(ctx context.Context, appID, deploymentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from instances
		 where app_id = $1
		   and deployment_id = $2
		   and state in ('RUNNING', 'WAKING', 'COLD_BOOTING')
	`, appID, deploymentID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// UpdateDeploymentMinInstances overwrites deployments.min_instances.
// Issue #557 closure / ADR-072 — the PATCH route at
// /v1/deployments/{id} writes through this method. Returns the
// fresh Deployment row (via the canonical scanDeployment) so the
// handler can build the response without a second round-trip.
//
// The caller (apid) validates the value against the parent app's
// plan ceiling (api.Plan.MaxMinInstances) before reaching this
// method. The DB-level CHECK constraint (migration 00131) is the
// belt-and-suspenders bound.
func (s *PgStore) UpdateDeploymentMinInstances(ctx context.Context, id string, min int) (Deployment, error) {
	row := s.pool.QueryRow(ctx, `
		update deployments set min_instances = $2
		 where id = $1
		 returning `+deploymentSelectColumnsWithRootfs, id, min)
	d, err := scanDeployment(row)
	if err != nil {
		// pgx returns ErrNoRows when the UPDATE matches zero rows;
		// translate to the store's canonical not-found error so the
		// handler emits RFC 7807 not_found.
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, err
	}
	return d, nil
}

// UpdateDeploymentTraffic stamps the per-deployment traffic-split
// weight (issue #556 PR-A/PR-C). PR-A used the "zero siblings"
// rebalance form: setting row R's traffic_percent to newPercent
// forced every other live row in the same app to 0, keeping Σ = 100
// by construction — but made every non-100 value a hard error since
// Σ was structurally newPercent. PR-C upgrades to proportional
// redistribution via the largest-remainder method
// (RedistributeTraffic): a `faas traffic set --percent 25` on a
// prior 100/0 app now leaves 75 on the prior deployment, Σ = 100,
// no error. The Σ post-write assertion stays as a defensive
// tripwire.
//
// Atomicity contract:
//  1. The transaction opens with pgx.TxOptions{} (read-committed
//     is fine — the FOR UPDATE below serialises).
//  2. The app's live rows are locked with SELECT … FOR UPDATE so
//     a concurrent UpdateDeploymentTraffic against the same app
//     serialises behind us until COMMIT.
//  3. The target row is validated to be 'live' (operator can't
//     move traffic to a superseded/failed/pending row).
//  4. The target row is updated to newPercent; sibling live rows
//     are updated proportionally via RedistributeTraffic. Σ is
//     guaranteed by the algorithm; the post-write SELECT … sum()
//     is a defence-in-depth tripwire that fails the transaction
//     on Σ != 100.
//  5. Range-check + status guard return ErrInvalidTrafficPercent;
//     Σ != 100 returns ErrTrafficPercentSumInvalid (handler
//     translates to 422 / 409).
//
// Range-checking newPercent here is a backstop — the handler
// validates [0, 100] and emits ErrInvalidTrafficPercent (422) on
// the request path. The CHECK constraint (migration 00160) is the
// third layer; any out-of-range value reaching this method trips a
// 23514 SQLSTATE.
func (s *PgStore) UpdateDeploymentTraffic(ctx context.Context, id string, newPercent int) (Deployment, error) {
	if newPercent < 0 || newPercent > 100 {
		return Deployment{}, fmt.Errorf("state: update deployment traffic %d: %w", newPercent, ErrInvalidTrafficPercent)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Deployment{}, fmt.Errorf("state: begin update traffic tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// (1) Lock the app's live rows. FOR UPDATE serialises concurrent
	// UpdateDeploymentTraffic / CreateDeployment calls.
	var appID string
	if err := tx.QueryRow(ctx,
		`select app_id from deployments where id = $1 for update`,
		id).Scan(&appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, fmt.Errorf("state: lock deployment %s: %w", id, err)
	}

	// (2) Confirm the target row is 'live'. A superseded/failed row
	// can't accept traffic — operators route traffic to live rows.
	var status string
	if err := tx.QueryRow(ctx,
		`select status from deployments where id = $1`,
		id).Scan(&status); err != nil {
		return Deployment{}, fmt.Errorf("state: read deployment status %s: %w", id, err)
	}
	if status != string(DeployLive) {
		return Deployment{}, fmt.Errorf("state: deployment %s status=%s: %w",
			id, status, ErrInvalidTrafficPercent)
	}

	// (3) Lock sibling live rows in the same app so a concurrent
	// CreateDeployment (which supersedes prior live rows) doesn't
	// race us. This is the FOR UPDATE on the (app_id, status)
	// subset; the deployment_pkey index handles the id lookup above.
	if _, err := tx.Exec(ctx,
		`select id from deployments
		  where app_id = $1 and status = 'live'
		  for update`,
		appID); err != nil {
		return Deployment{}, fmt.Errorf("state: lock sibling live rows: %w", err)
	}

	// (4) Stamp target + redistribute residual across siblings via
	// the largest-remainder method (see RedistributeTraffic).
	//
	// Read sibling weights first so the algorithm has the prior
	// distribution to redistribute proportionally. We re-stamp
	// inside the same tx that holds the FOR UPDATE locks above.
	rows, err := tx.Query(ctx,
		`select id, traffic_percent
		   from deployments
		  where app_id = $1 and status = 'live' and id != $2
		  order by id`,
		appID, id)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: read sibling weights: %w", err)
	}
	type sibling struct {
		ID    string
		Prior int
	}
	var siblings []sibling
	for rows.Next() {
		var s sibling
		if err := rows.Scan(&s.ID, &s.Prior); err != nil {
			rows.Close()
			return Deployment{}, fmt.Errorf("state: scan sibling weight: %w", err)
		}
		siblings = append(siblings, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Deployment{}, fmt.Errorf("state: iterate sibling weights: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`update deployments set traffic_percent = $2 where id = $1`,
		id, newPercent); err != nil {
		return Deployment{}, fmt.Errorf("state: stamp traffic_percent %s: %w", id, mapErr(err))
	}

	// RedistributeTraffic returns weights that sum to (100 - newPercent);
	// siblings[idx].ID gets newWeights[idx]. Σ + newPercent = 100 by
	// construction (the algorithm enforces it; see helper doc).
	helperSiblings := make([]struct {
		ID    string
		Prior int
	}, len(siblings))
	for i, s := range siblings {
		helperSiblings[i].ID = s.ID
		helperSiblings[i].Prior = s.Prior
	}
	newWeights := RedistributeTraffic(helperSiblings, 100-newPercent)
	for i, s := range siblings {
		if _, err := tx.Exec(ctx,
			`update deployments set traffic_percent = $2 where id = $1`,
			s.ID, newWeights[i]); err != nil {
			return Deployment{}, fmt.Errorf("state: stamp sibling %s traffic_percent: %w", s.ID, mapErr(err))
		}
	}

	// (5) Σ invariant assertion. Structurally guaranteed by
	// RedistributeTraffic + the newPercent stamp above, but the
	// test suite pins the check (defence in depth).
	var sum int
	if err := tx.QueryRow(ctx,
		`select coalesce(sum(traffic_percent), 0)
		   from deployments
		  where app_id = $1 and status = 'live'`,
		appID).Scan(&sum); err != nil {
		return Deployment{}, fmt.Errorf("state: read Σ traffic_percent: %w", err)
	}
	if sum != 100 {
		return Deployment{}, fmt.Errorf("state: Σ traffic_percent = %d, want 100: %w",
			sum, ErrTrafficPercentSumInvalid)
	}

	// Read back the stamped row + commit.
	row := tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments where id = $1`, id)
	d, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, fmt.Errorf("state: read deployment after stamp: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, fmt.Errorf("state: commit update traffic: %w", err)
	}
	return d, nil
}

// AdvanceCanary atomically commits one automatic canary step. The expected
// step is checked while the deployment row is locked; traffic redistribution,
// terminal promotion, sibling supersede, and the audit row all share the same
// transaction.
func (s *PgStore) AdvanceCanary(ctx context.Context, id string, params CanaryAdvanceParams) (Deployment, int64, error) {
	if params.ExpectedStep < 0 || params.TrafficPercent < 0 || params.TrafficPercent > 100 {
		return Deployment{}, 0, ErrCanaryStateInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	dep, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments where id = $1 for update`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
			return Deployment{}, 0, ErrNotFound
		}
		return Deployment{}, 0, fmt.Errorf("state: advance canary load: %w", err)
	}
	if dep.Status != DeployLive || (dep.RolloutState != "pending" && dep.RolloutState != "rolling_out") ||
		dep.CanaryTotalSteps <= 0 || params.ExpectedStep >= dep.CanaryTotalSteps {
		return Deployment{}, 0, ErrCanaryStateInvalid
	}
	if dep.CanaryStep != params.ExpectedStep {
		return Deployment{}, 0, ErrCanaryStepConflict
	}
	if _, err := uuid.Parse(dep.ID); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary deployment id %q: %w", dep.ID, err)
	}

	rows, err := tx.Query(ctx,
		`select id, traffic_percent
		   from deployments
		  where app_id = $1 and status = 'live' and id != $2
		  order by id
		  for update`, dep.AppID, dep.ID)
	if err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary lock siblings: %w", err)
	}
	var siblings []struct {
		ID    string
		Prior int
	}
	for rows.Next() {
		var sibling struct {
			ID    string
			Prior int
		}
		if err := rows.Scan(&sibling.ID, &sibling.Prior); err != nil {
			rows.Close()
			return Deployment{}, 0, fmt.Errorf("state: advance canary scan sibling: %w", err)
		}
		siblings = append(siblings, sibling)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary iterate siblings: %w", err)
	}

	now := time.Now().UTC()
	newStep := params.ExpectedStep + 1
	terminal := newStep >= dep.CanaryTotalSteps-1
	persistedStep := newStep
	if terminal {
		params.TrafficPercent = 100
		// Keep the established terminal sentinel: in-flight stages are
		// zero-indexed, while a completed rollout stores step=total.
		persistedStep = dep.CanaryTotalSteps
	} else if len(siblings) == 0 {
		return Deployment{}, 0, ErrTrafficPercentSumInvalid
	}
	newWeights := RedistributeTraffic(siblings, 100-params.TrafficPercent)
	if terminal {
		if _, err := tx.Exec(ctx,
			`update deployments set status = 'superseded', traffic_percent = 0
			  where app_id = $1 and status = 'live' and id != $2`, dep.AppID, dep.ID); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: advance canary supersede siblings: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`update deployments set
			canary_step = $2,
			canary_step_started_at = $3,
			traffic_percent = $4,
			rollout_state = case when $2 >= canary_total_steps then 'complete' else rollout_state end,
			rollout_started_at = coalesce(rollout_started_at, $3),
			rollout_completed_at = case when $2 >= canary_total_steps then $3 else rollout_completed_at end
		 where id = $1`, dep.ID, persistedStep, now, params.TrafficPercent); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary update: %w", err)
	}
	if !terminal {
		for i, sibling := range siblings {
			if _, err := tx.Exec(ctx,
				`update deployments set traffic_percent = $2 where id = $1`,
				sibling.ID, newWeights[i]); err != nil {
				return Deployment{}, 0, fmt.Errorf("state: advance canary sibling %s: %w", sibling.ID, err)
			}
		}
	}

	auditAt := params.Audit.At
	if auditAt.IsZero() {
		auditAt = now
	}
	auditKind := params.Audit.Kind
	if auditKind == "" {
		auditKind = DeployTrafficChanged
	}
	var auditID int64
	if err := tx.QueryRow(ctx,
		`insert into deployment_audit
		    (deployment_id, account_id, kind, actor, at, data)
		 values ($1::uuid, $2, $3, $4, $5, $6::jsonb)
		 returning id`, dep.ID, params.Audit.AccountID, string(auditKind), params.Audit.Actor, auditAt, []byte(params.Audit.Data)).Scan(&auditID); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary audit: %w", err)
	}
	updated, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments where id = $1`, dep.ID))
	if err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary readback: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: advance canary commit: %w", err)
	}
	return updated, auditID, nil
}

// RedistributeTraffic assigns weights to N siblings that sum to
// residual (typically 100 - newPercent from UpdateDeploymentTraffic)
// using the largest-remainder method. The returned slice is in the
// same order as siblings; caller maps weights[i] → siblings[i].ID.
//
// Algorithm (largest-remainder / Hamilton's method):
//  1. Let Σ = Σ_{i=1..n} siblings[i].Prior. (Σ > 0 — a sole live
//     row at 100 has no siblings and the caller handles that case
//     before calling.)
//  2. For each i, exact = siblings[i].Prior / Σ * residual.
//  3. base[i] = floor(exact). remainder[i] = exact - base[i].
//  4. Sort (remainder, ID) by (remainder DESC, ID ASC).
//  5. The first k indices (where k = residual - Σ base[i]) each
//     get +1, so the Σ of new weights = residual by construction.
//     The tie-break is (fraction DESC, ID ASC) — stable across
//     runs so two operators seeing the same state see the same
//     rebalance.
//
// Worked example (PR-C test pin): {A:50, B:30, C:20} → set A:25
// residual=75. Σ_{prior-target} = B:30 + C:20 = 50.
//
//	exact_B = 30/50*75 = 45.0   base=45, remainder=0.0
//	exact_C = 20/50*75 = 30.0   base=30, remainder=0.0
//
// k = 75 - (45+30) = 0 → no +1 awarded. Result: {B:45, C:30}.
// Σ = 75. Σ + A:25 = 100. ✓
//
// Tie-break example: {A:50, B:50} → set A:0 residual=100.
// Σ = 100. exact_A = exact_B = 50.0. base=50 each, remainder=0.
// k = 100 - 100 = 0 → no tie-break needed; both = 50.
// For a tie that triggers the +1 (e.g. {A:33, B:33, C:34},
// set A:0, residual=100, Σ=67):
//
//	exact_B = 33/67*100 = 49.25..., base=49, remainder=0.25
//	exact_C = 34/67*100 = 50.74..., base=50, remainder=0.74
//
// k = 100 - 99 = 1 → C gets +1 (largest remainder).
// Result: {B:49, C:51}. Σ = 100.
//
// Defensive: if Σ ≤ 0 (all siblings were 0 before — degenerate),
// every sibling gets `residual / n` and the residual mod n is
// absorbed by the first n%min siblings in ID ASC order. Σ = residual.
func RedistributeTraffic(siblings []struct {
	ID    string
	Prior int
}, residual int) []int {
	n := len(siblings)
	if n == 0 {
		return nil
	}
	if residual <= 0 {
		// Caller asked for zero or negative residual (target ≥ 100).
		// All siblings drop to 0 — single edge case for `target=100`
		// on an N-row app where the caller must already have
		// stamped the target to 100 and we just zero the rest.
		out := make([]int, n)
		return out
	}
	// Σ prior weight of siblings.
	var sumPrior int
	for _, s := range siblings {
		sumPrior += s.Prior
	}
	if sumPrior <= 0 {
		// Degenerate: distribute residual evenly across n siblings,
		// first `residual mod n` (in ID-ASC order) absorb the ±1.
		// This shouldn't happen in practice (a Σ=0 sibling would
		// mean a deployment with 0% traffic — supersede-zeroed —
		// sitting live alongside another live row, which the
		// supersede path disallows), but a sane fallback is
		// mandatory for the integration tests to be deterministic.
		base := residual / n
		out := make([]int, n)
		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
			out[i] = base
		}
		// Sort indices by siblings[i].ID ASC so the tie-break is
		// stable.
		sort.SliceStable(indices, func(a, b int) bool {
			return siblings[indices[a]].ID < siblings[indices[b]].ID
		})
		for i := 0; i < residual%n; i++ {
			out[indices[i]]++
		}
		return out
	}
	// Normal path.
	type slot struct {
		idx       int
		base      int
		remainder float64
	}
	slots := make([]slot, n)
	var sumBase int
	for i, s := range siblings {
		exact := float64(s.Prior) / float64(sumPrior) * float64(residual)
		b := int(exact) // floor for non-negative; exact ≥ 0
		slots[i] = slot{idx: i, base: b, remainder: exact - float64(b)}
		sumBase += b
	}
	// Award +1 to the top-k slots by (remainder DESC, ID ASC) where
	// k = residual - sumBase. sumBase ≤ residual by definition of
	// floor; k ∈ [0, n).
	k := residual - sumBase
	sort.SliceStable(slots, func(a, b int) bool {
		if slots[a].remainder != slots[b].remainder {
			return slots[a].remainder > slots[b].remainder
		}
		return siblings[slots[a].idx].ID < siblings[slots[b].idx].ID
	})
	out := make([]int, n)
	for i, s := range slots {
		out[s.idx] = s.base
		if i < k {
			out[s.idx]++
		}
	}
	return out
}

// SetDeploymentParked stamps the per-deployment parked_reason +
// parked_at columns (issue #554 / ADR-079 follow-up, migration
// 00157). Idempotent: re-parking an already-parked deployment is
// a no-op — the WHERE filter `parked_reason is null` guarantees
// parked_at is set exactly once. A second park during a schedd
// restart cycle must NOT repaint the timestamp, otherwise the
// apid GET /v1/apps/{slug}.parked_deployment surface would drift
// on every crash loop.
//
// The audit row (engine.ParkDeployment →
// "instances.parked_liveness_exhausted" event) is the durable
// source of truth; this method is the projection that powers the
// customer-facing wire.
//
// Closed-set vocabulary is enforced at the schema layer via the
// deployments_parked_reason_check constraint; an out-of-set
// reason surfaces as a Postgres 23514 (check_violation) which
// mapErr translates to a wrap-wrapped error so callers see the
// underlying reason.
//
// Returns ErrNotFound when the deployment id is genuinely absent
// (a stale id from the engine's appMu guard would otherwise
// silently no-op).
func (s *PgStore) SetDeploymentParked(ctx context.Context, id, reason string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set parked_reason = $2, parked_at = $3
		  where id = $1
		    and parked_reason is null`,
		id, reason, at.UTC())
	if err != nil {
		return fmt.Errorf("state: set deployment parked: %w", mapErr(err))
	}
	if tag.RowsAffected() == 0 {
		// Two paths land here:
		//   1. Row exists but parked_reason is already set — a
		//      no-op idempotent re-stamp. Not an error.
		//   2. Row does not exist — disambiguate via a probe.
		var exists bool
		if probeErr := s.pool.QueryRow(ctx,
			`select exists(select 1 from deployments where id = $1)`,
			id).Scan(&exists); probeErr != nil {
			return fmt.Errorf("state: probe deployment parked: %w", mapErr(probeErr))
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// LatestParkedDeploymentForApp returns the most recently parked
// deployment for an app, or ErrNotFound if none. Powers the apid
// GET /v1/apps/{slug}.parked_deployment reference (AC #3 wire).
//
// The match is `parked_reason is not null order by parked_at desc
// limit 1`. Superseded deployments still match — their
// parked_reason/parked_at columns are not cleared on supersede,
// so a customer who deployed, parked, then redeployed will see
// the parked deployment reference pointing at the older row.
func (s *PgStore) LatestParkedDeploymentForApp(ctx context.Context, appID string) (Deployment, error) {
	row := s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments
		  where app_id = $1
		    and parked_reason is not null
		  order by parked_at desc
		  limit 1`, appID)
	d, err := scanDeployment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, err
	}
	return d, nil
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
			`select `+deploymentSelectColumnsWithRootfs+`
			 from deployments where app_id = $1 order by created_at desc limit $2 offset $3`,
			appID, limit, offset)
	} else {
		rows, err = s.pool.Query(ctx,
			`select `+deploymentSelectColumnsWithRootfs+`
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
			`select `+deploymentSelectColumnsQualified+`
			 from deployments d join apps a on a.id = d.app_id
			 where a.account_id = $1 and a.status <> 'deleted'
			 order by d.created_at desc limit $2`,
			accountID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select `+deploymentSelectColumnsQualified+`
			 from deployments d join apps a on a.id = d.app_id
			 where a.account_id = $1 and a.status <> 'deleted' and d.created_at < $2
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

// RecoverRollout (issue #976 / ADR-122 / SAFE-RELEASES-R) is the
// Postgres-backed counterpart of MemStore.RecoverRollout — the
// atomic-tx primitive that drives the operator CLI's manual
// recovery path. The whole flow runs inside a single transaction
// so the canary_step bump + traffic redistribution + audit emit
// land together or not at all; a concurrent canary_progression
// tick or alert-driven action executor is serialised behind the
// FOR UPDATE lock.
//
// action ∈ {"advance", "promote", "abort"}; see
// MemStore.RecoverRollout for the closed-set semantics.
//
// Returns the refreshed Deployment row + the audit row id (so
// the CLI's terminal can echo "audit_id=N"). Both backends share
// the same closed-set guards so handler tests can pin the same
// shape against either store.
func (s *PgStore) RecoverRollout(ctx context.Context, appID string, action, reason string) (Deployment, int64, error) {
	switch action {
	case "advance", "promote", "abort":
	default:
		return Deployment{}, 0, ErrInvalidRecoverAction
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, 0, fmt.Errorf("state: recover_rollout begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	// (1) Find + lock the active rollout row for this app.
	// Mirrors SafedeployListPendingRollouts' predicate; the FOR
	// UPDATE serialises a concurrent canary tick or alert-driven
	// action executor on the same row.
	row := tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments
		  where app_id = $1
		    and status = 'live'
		    and rollout_state in ('pending','rolling_out')
		  order by created_at desc
		  limit 1
		  for update`, appID)
	dep, scanErr := scanDeploymentWithRootfs(row)
	if scanErr != nil {
		if errors.Is(scanErr, ErrNotFound) {
			return Deployment{}, 0, ErrNotFound
		}
		return Deployment{}, 0, fmt.Errorf("state: recover_rollout load: %w", scanErr)
	}

	now := time.Now().UTC()

	var (
		auditKind   DeploymentAuditKind
		auditData   []byte
		newTrafficP int
	)
	switch action {
	case "advance":
		// Stuck-detection gate. canary_step_started_at NULL or
		// within the stuck-after window both trip
		// ErrRolloutNotStuck; the CLI distinguishes "fix a
		// stuck rollout" (advance) from "force-step a
		// healthy rollout" (promote).
		if dep.CanaryStepStartedAt == nil {
			return Deployment{}, 0, ErrRolloutNotStuck
		}
		if now.Sub(*dep.CanaryStepStartedAt) < RecoverRolloutStuckAfter {
			return Deployment{}, 0, ErrRolloutNotStuck
		}
		if dep.CanaryTotalSteps <= 0 || dep.CanaryStep >= dep.CanaryTotalSteps {
			return Deployment{}, 0, ErrRolloutStateInvalid
		}

		newStep := dep.CanaryStep + 1
		newTrafficP = stepToPercent(newStep, dep.CanaryTotalSteps)

		// Bump step, stamp started_at, stamp traffic_percent.
		if _, err := tx.Exec(ctx,
			`update deployments set
				canary_step = $2,
				canary_step_started_at = $3,
				traffic_percent = $4
			 where id = $1`,
			dep.ID, newStep, now, newTrafficP); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp advance: %w", err)
		}

		// Redistribute residual across siblings (largest-
		// remainder Σ = 100).
		siblingRows, err := tx.Query(ctx,
			`select id, traffic_percent
			   from deployments
			  where app_id = $1 and status = 'live' and id != $2
			  order by id`,
			appID, dep.ID)
		if err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout read siblings: %w", err)
		}
		var siblings []struct {
			ID    string
			Prior int
		}
		for siblingRows.Next() {
			var s struct {
				ID    string
				Prior int
			}
			if err := siblingRows.Scan(&s.ID, &s.Prior); err != nil {
				siblingRows.Close()
				return Deployment{}, 0, fmt.Errorf("state: recover_rollout scan sibling: %w", err)
			}
			siblings = append(siblings, s)
		}
		siblingRows.Close()
		if err := siblingRows.Err(); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout iterate siblings: %w", err)
		}
		newWeights := RedistributeTraffic(siblings, 100-newTrafficP)
		for i, s := range siblings {
			if _, err := tx.Exec(ctx,
				`update deployments set traffic_percent = $2 where id = $1`,
				s.ID, newWeights[i]); err != nil {
				return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp sibling %s: %w", s.ID, err)
			}
		}

		// If the bump reaches the top of the ladder, flip
		// rollout_state='complete'.
		if newStep >= dep.CanaryTotalSteps {
			if _, err := tx.Exec(ctx,
				`update deployments set
					rollout_state = 'complete',
					rollout_completed_at = $2
				 where id = $1`,
				dep.ID, now); err != nil {
				return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp complete: %w", err)
			}
		}

		auditKind = DeployTrafficChanged
		auditData = []byte(fmt.Sprintf(`{"action":"advance","reason":%q}`, reason))

	case "promote":
		if dep.CanaryTotalSteps <= 0 || dep.CanaryStep >= dep.CanaryTotalSteps {
			return Deployment{}, 0, ErrRolloutStateInvalid
		}

		// Short-circuit: step = total, traffic_percent = 100,
		// siblings zeroed, rollout_state = 'complete'.
		if _, err := tx.Exec(ctx,
			`update deployments set
				canary_step = $2,
				canary_step_started_at = $3,
				traffic_percent = 100,
				rollout_state = 'complete',
				rollout_completed_at = $3
			 where id = $1`,
			dep.ID, dep.CanaryTotalSteps, now); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp promote: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`update deployments set traffic_percent = 0
			  where app_id = $1 and status = 'live' and id != $2`,
			appID, dep.ID); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout zero siblings: %w", err)
		}

		auditKind = DeployTrafficChanged
		auditData = []byte(fmt.Sprintf(`{"action":"promote","reason":%q}`, reason))

	case "abort":
		if _, err := tx.Exec(ctx,
			`update deployments set
				rollout_state = 'aborted',
				traffic_percent = 0,
				rollout_aborted_at = $2,
				rollout_aborted_reason = $3
			 where id = $1`,
			dep.ID, now, reason); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp abort: %w", err)
		}
		// An aborted canary must not retain its old traffic weight. Rebuild
		// the sibling weights in the same transaction so abort/demote cannot
		// leave the app below or above the Σ=100 invariant.
		siblingRows, err := tx.Query(ctx,
			`select id, traffic_percent
			   from deployments
			  where app_id = $1 and status = 'live' and id != $2
			  order by id`,
			appID, dep.ID)
		if err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout read abort siblings: %w", err)
		}
		var siblings []struct {
			ID    string
			Prior int
		}
		for siblingRows.Next() {
			var sibling struct {
				ID    string
				Prior int
			}
			if err := siblingRows.Scan(&sibling.ID, &sibling.Prior); err != nil {
				siblingRows.Close()
				return Deployment{}, 0, fmt.Errorf("state: recover_rollout scan abort sibling: %w", err)
			}
			siblings = append(siblings, sibling)
		}
		siblingRows.Close()
		if err := siblingRows.Err(); err != nil {
			return Deployment{}, 0, fmt.Errorf("state: recover_rollout iterate abort siblings: %w", err)
		}
		newWeights := RedistributeTraffic(siblings, 100)
		for i, sibling := range siblings {
			if _, err := tx.Exec(ctx,
				`update deployments set traffic_percent = $2 where id = $1`,
				sibling.ID, newWeights[i]); err != nil {
				return Deployment{}, 0, fmt.Errorf("state: recover_rollout stamp abort sibling %s: %w", sibling.ID, err)
			}
		}
		auditKind = DeployRolledBack
		auditData = []byte(fmt.Sprintf(`{"action":"abort","reason":%q}`, reason))
	}

	// Audit emit rides the same tx as the deployment stamp —
	// failures roll back the deployment update. The audit row's
	// actor sentinel "operator:cli:recover_rollout" distinguishes
	// the operator-driven path from the meterd-driven
	// canary_progression / safedeploy orchestrator paths.
	var auditID int64
	if err := tx.QueryRow(ctx,
		`insert into deployment_audit
		    (deployment_id, account_id, kind, actor, at, data)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6::jsonb)
		 returning id`,
		dep.ID,
		nil, // account_id is nullable; the CLI carries the actor via the actor column
		string(auditKind),
		"operator:cli:recover_rollout",
		now,
		auditData,
	).Scan(&auditID); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: recover_rollout append audit: %w", err)
	}

	// Read back the post-write row.
	row2 := tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments where id = $1`, dep.ID)
	updated, scanErr := scanDeploymentWithRootfs(row2)
	if scanErr != nil {
		return Deployment{}, 0, fmt.Errorf("state: recover_rollout readback: %w", scanErr)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, 0, fmt.Errorf("state: recover_rollout commit: %w", err)
	}
	return updated, auditID, nil
}

func (s *PgStore) MarkDeploymentSuperseded(ctx context.Context, id string) error {
	return s.UpdateDeploymentStatus(ctx, id, DeploySuperseded, "")
}

func (s *PgStore) MarkDeploymentLive(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: mark deployment live begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	dep, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments where id = $1 for update`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("state: mark deployment live load: %w", err)
	}
	if dep.Status == DeployLive || dep.CanaryTotalSteps <= 0 {
		if _, err := tx.Exec(ctx,
			`update deployments set status = $2, error = '' where id = $1`, id, string(DeployLive)); err != nil {
			return fmt.Errorf("state: mark deployment live update: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("state: mark deployment live commit: %w", err)
		}
		return nil
	}

	rows, err := tx.Query(ctx,
		`select id, traffic_percent
		   from deployments
		  where app_id = $1 and status = 'live'
		  for update`, dep.AppID)
	if err != nil {
		return fmt.Errorf("state: mark canary live lock siblings: %w", err)
	}
	var siblings []struct {
		ID    string
		Prior int
	}
	for rows.Next() {
		var sibling struct {
			ID    string
			Prior int
		}
		if err := rows.Scan(&sibling.ID, &sibling.Prior); err != nil {
			rows.Close()
			return fmt.Errorf("state: mark canary live scan sibling: %w", err)
		}
		if sibling.ID != id {
			siblings = append(siblings, sibling)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("state: mark canary live iterate siblings: %w", err)
	}

	now := time.Now().UTC()
	if len(siblings) == 0 && dep.TrafficPercent != 100 {
		// A first deployment has no residual bucket. Complete it at
		// 100% rather than exposing an invalid one-row split.
		if _, err := tx.Exec(ctx,
			`update deployments set
				status = 'live', error = '',
				traffic_percent = 100,
				canary_step = canary_total_steps,
				canary_step_started_at = $2,
				rollout_state = 'complete',
				rollout_completed_at = $2
			 where id = $1`, id, now); err != nil {
			return fmt.Errorf("state: mark first canary live: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx,
			`update deployments set
				status = 'live', error = '',
				rollout_state = 'rolling_out',
				rollout_started_at = $2,
				canary_step_started_at = $2
			 where id = $1`, id, now); err != nil {
			return fmt.Errorf("state: mark canary live: %w", err)
		}
		newWeights := RedistributeTraffic(siblings, 100-dep.TrafficPercent)
		for i, sibling := range siblings {
			if _, err := tx.Exec(ctx,
				`update deployments set traffic_percent = $2 where id = $1`,
				sibling.ID, newWeights[i]); err != nil {
				return fmt.Errorf("state: mark canary sibling %s: %w", sibling.ID, err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: mark deployment live commit: %w", err)
	}
	return nil
}

// UpdateDeploymentOpenAPISnapshot (ADR-121, migration 00358)
// upserts the deployment_openapi_snapshots row for the given
// deployment. PR-B refactors MarkDeploymentLive to call this
// method inside the same transaction as the status='live'
// UPDATE.
func (s *PgStore) UpdateDeploymentOpenAPISnapshot(ctx context.Context, snap OpenAPISnapshot) error {
	if snap.DeploymentID == "" {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: empty deployment_id")
	}
	if snap.AppID == "" {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: empty app_id")
	}
	if snap.Scope == "" {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: empty scope")
	}
	if len(snap.Snapshot) == 0 {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: empty snapshot bytes")
	}
	if snap.SHA256 == "" {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: empty sha256")
	}
	if snap.SchemaVersion < 1 {
		return errors.New("pgstore: UpdateDeploymentOpenAPISnapshot: schema_version must be >= 1")
	}
	if snap.CapturedAt.IsZero() {
		snap.CapturedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		insert into deployment_openapi_snapshots
			(deployment_id, app_id, scope, snapshot, sha256, schema_version, captured_at)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
		on conflict (deployment_id) do update
		   set app_id = excluded.app_id,
		       scope = excluded.scope,
		       snapshot = excluded.snapshot,
		       sha256 = excluded.sha256,
		       schema_version = excluded.schema_version,
		       captured_at = excluded.captured_at
	`, snap.DeploymentID, snap.AppID, snap.Scope, []byte(snap.Snapshot), snap.SHA256, snap.SchemaVersion, snap.CapturedAt)
	if err != nil {
		return fmt.Errorf("pgstore: UpdateDeploymentOpenAPISnapshot: %w", err)
	}
	return nil
}

// LatestOpenAPISnapshotForScope returns the most recently
// captured snapshot for (appID, scope).
func (s *PgStore) LatestOpenAPISnapshotForScope(ctx context.Context, appID, scope string) (OpenAPISnapshot, error) {
	row := s.pool.QueryRow(ctx, `
		select deployment_id, app_id, scope, snapshot, sha256, schema_version, captured_at
		  from deployment_openapi_snapshots
		 where app_id = $1::uuid and scope = $2
		 order by captured_at desc
		 limit 1
	`, appID, scope)
	return scanOpenAPISnapshot(row)
}

// OpenAPISnapshotByDeployment returns the snapshot row for a
// specific deployment id.
func (s *PgStore) OpenAPISnapshotByDeployment(ctx context.Context, deploymentID string) (OpenAPISnapshot, error) {
	row := s.pool.QueryRow(ctx, `
		select deployment_id, app_id, scope, snapshot, sha256, schema_version, captured_at
		  from deployment_openapi_snapshots
		 where deployment_id = $1::uuid
	`, deploymentID)
	return scanOpenAPISnapshot(row)
}

// scanOpenAPISnapshot is the pgstore scan helper for one
// deployment_openapi_snapshots row.
func scanOpenAPISnapshot(row pgx.Row) (OpenAPISnapshot, error) {
	var (
		snap     OpenAPISnapshot
		rawSnap  []byte
		captured time.Time
	)
	if err := row.Scan(&snap.DeploymentID, &snap.AppID, &snap.Scope, &rawSnap, &snap.SHA256, &snap.SchemaVersion, &captured); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OpenAPISnapshot{}, ErrNotFound
		}
		return OpenAPISnapshot{}, fmt.Errorf("pgstore: scan openapi snapshot: %w", err)
	}
	snap.Snapshot = json.RawMessage(rawSnap)
	snap.CapturedAt = captured
	return snap, nil
}

// MarkDeploymentCancelled (ADR-124) atomically flips the row to
// DeployCancelled while honoring the cancel-eligible CAS guard.
// The single UPDATE covers the (a) status transition, (b) audit
// stamp, and (c) reason column — all in one round-trip so
// concurrent transitions are last-write-wins safe per the
// existing pattern at pgstore.go:5675-5683 (build CAS guard).
//
// Tag.RowsAffected() == 0 has three distinct failure modes:
//   - id is unknown                       → ErrNotFound
//   - id is known, status = DeployLive    → ErrCancelLiveForbidden
//   - id is known, status in {failed,
//     superseded, cancelled}              → ErrInvalidStateTransition
//
// The two-tier SQL guard surfaces the right sentinel in one
// round-trip via RETURNING — the row's pre-UPDATE status is
// captured by a single SELECT before the UPDATE so the caller
// (apid handler) can pick the correct RFC 7807 code.
func (s *PgStore) MarkDeploymentCancelled(ctx context.Context, id, principal string, reason CancelReason, when time.Time) error {
	if !reason.IsValid() {
		return ErrInvalidStateTransition
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("MarkDeploymentCancelled: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatus DeploymentStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM deployments WHERE id = $1 FOR UPDATE`, id).Scan(&currentStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("MarkDeploymentCancelled: select for update: %w", err)
	}
	if currentStatus == DeployLive {
		return ErrCancelLiveForbidden
	}
	if !currentStatus.IsCancelEligible() {
		return ErrInvalidStateTransition
	}

	if _, err := tx.Exec(ctx, `
		UPDATE deployments
		   SET status = $2,
		       cancelled_at = $3,
		       cancelled_by_principal = $4,
		       cancel_reason = $5
		 WHERE id = $1
		   AND status = $2_old`,
		id,
		string(DeployCancelled),
		when.UTC(),
		principal,
		string(reason),
		string(currentStatus),
	); err != nil {
		return fmt.Errorf("MarkDeploymentCancelled: update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("MarkDeploymentCancelled: commit: %w", err)
	}
	return nil
}

// CancelDeploymentTx (ADR-124) is the single-transaction
// orchestrator. The shape mirrors AutoRollbackDeploymentsTx from
// ADR-118 (`worktree-feat-deploy-ux-mega-c-pr1` commit c76fd64e6,
// not yet on main): one tx that (a) locks the parent apps row,
// (b) flips the deployment to DeployCancelled, (c) cascade-flips
// every non-terminal build attached to the deployment, then
// returns the post-flip deployment + the list of build IDs that
// flipped. The Firecracker VM tear-down is the consumer of the
// deployment_changed + build_changed pg_notify payloads and lives
// outside this transaction (best-effort; SweepStuckRunningBuilds
// is the durable backstop).
func (s *PgStore) CancelDeploymentTx(ctx context.Context, id, principal string, reason CancelReason) (Deployment, []string, error) {
	if !reason.IsValid() {
		return Deployment{}, nil, ErrInvalidStateTransition
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var d Deployment
	var appID string
	var status DeploymentStatus
	if err := tx.QueryRow(ctx, `
		SELECT app_id, status
		  FROM deployments
		 WHERE id = $1
		 FOR UPDATE`, id).Scan(&appID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, nil, ErrNotFound
		}
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: select deployment for update: %w", err)
	}
	if status == DeployLive {
		return Deployment{}, nil, ErrCancelLiveForbidden
	}
	if !status.IsCancelEligible() {
		return Deployment{}, nil, ErrInvalidStateTransition
	}

	// Lock the parent apps row to serialise against concurrent
	// UpdateApp flips and another CreateDeployment on the same
	// app. Mirrors pkg/state/pgstore.go:4185-4199 (the canonical
	// CreateDeployment tx-pattern).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM apps WHERE id = $1 AND status = 'active' FOR UPDATE`, appID); err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: lock apps row: %w", err)
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE deployments
		   SET status = $2,
		       cancelled_at = $3,
		       cancelled_by_principal = $4,
		       cancel_reason = $5
		 WHERE id = $1
		   AND status IN ('pending', 'building', 'imaging', 'snapshotting')`,
		id, string(DeployCancelled), now, principal, string(reason),
	); err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: update deployment: %w", err)
	}

	// Cascade-cancel every non-terminal build row attached to
	// this deployment. The cascade flag tells the build-cancel
	// audit column which side this row flip came from. We
	// RETURNING id so the handler can fire a build_changed
	// pg_notify per row (the LISTEN goroutine in cmd/builderd
	// calls VM.Cancel for each).
	rows, err := tx.Query(ctx, `
		UPDATE builds
		   SET status = $2,
		       cancelled_at = $3,
		       cancelled_by_deployment_cascade = true
		 WHERE deployment_id = $1
		   AND status IN ('queued', 'running')
		RETURNING id`,
		id, string(BuildCancelled), now,
	)
	if err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: cascade-cancel builds: %w", err)
	}
	var cancelledBuildIDs []string
	for rows.Next() {
		var bid string
		if err := rows.Scan(&bid); err != nil {
			rows.Close()
			return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: scan build id: %w", err)
		}
		cancelledBuildIDs = append(cancelledBuildIDs, bid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: iterate build ids: %w", err)
	}

	// Use non-nil scratch vars for the rootfs triple. pgx v5 panics
	// with "invalid memory address or nil pointer dereference" when a
	// typed-nil pointer is passed as a Scan destination (see the
	// matching comment on scanDeployment at pkg/state/pgstore.go:14347).
	// The scratch values are discarded — the caller doesn't need rootfs
	// fields, and the deployments row's rootfs_path / rootfs_key /
	// rootfs_bytes are coalesced to '' / '' / 0 in the SELECT projection
	// anyway.
	var rootfsPath, rootfsKey string
	var rootfsBytes int64
	if err := scanDeploymentInto(&d, tx.QueryRow(ctx, `SELECT `+deploymentSelectColumnsWithRootfs+` FROM deployments WHERE id = $1`, id), &rootfsPath, &rootfsKey, &rootfsBytes); err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: scan deployment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, nil, fmt.Errorf("CancelDeploymentTx: commit: %w", err)
	}
	return d, cancelledBuildIDs, nil
}

// ReorderDeployment (ADR-124) atomically bumps the row's priority
// if-and-only-if status='pending'. The CAS guard prevents a
// reorder that races with the builderd claim path (which moves
// pending → building atomically).
func (s *PgStore) ReorderDeployment(ctx context.Context, id string, newPriority int, principal string) error {
	if newPriority < 0 || newPriority > 1000 {
		return ErrPriorityOutOfRange
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		   SET priority = $2,
		       reordered_at = NOW(),
		       reordered_by_principal = $3
		 WHERE id = $1
		   AND status = 'pending'`,
		id, newPriority, principal,
	)
	if err != nil {
		return fmt.Errorf("ReorderDeployment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish ErrNotFound from ErrReorderNotPending.
		var exists bool
		if e := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployments WHERE id = $1)`, id).Scan(&exists); e != nil {
			return fmt.Errorf("ReorderDeployment: post-check: %w", e)
		}
		if !exists {
			return ErrNotFound
		}
		return ErrReorderNotPending
	}
	return nil
}

// ClearDeployment (ADR-124) soft-deletes a non-live deployment
// row. Status is intentionally untouched so the admin audit
// trail remains visible; customer-side list surfaces filter by
// `deleted_at IS NULL`.
func (s *PgStore) ClearDeployment(ctx context.Context, id, principal string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ClearDeployment: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status DeploymentStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM deployments WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("ClearDeployment: select for update: %w", err)
	}
	if status == DeployLive {
		return ErrCancelLiveForbidden
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE deployments
		   SET deleted_at = $2,
		       deleted_by_principal = $3
		 WHERE id = $1`,
		id, now, principal,
	); err != nil {
		return fmt.Errorf("ClearDeployment: update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ClearDeployment: commit: %w", err)
	}
	return nil
}

// ClearObsoleteDeployments (ADR-124) bulk-soft-deletes terminal
// rows (status IN {superseded, failed, cancelled}) outside the
// per-app "current + previous" retention window. The retention
// invariant (§6.2 INV 3) forbids us from clearing a row that is
// either the current live deployment OR the immediate previous.
// Per-app recent-row exclusion is computed in Go after a single
// SELECT; for accounts with N apps and O(N) obsolete rows each,
// this is bounded by the per-app LIMIT 5 sub-query.
func (s *PgStore) ClearObsoleteDeployments(ctx context.Context, appID string, olderThan time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("ClearObsoleteDeployments: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	// Subquery "retention_id" returns the most-recent 2
	// deployment_ids per app_id (the current + immediate previous
	// snapshot retention). We explicitly do NOT touch them.
	//
	// `enqueued_at` is a builds-table column, not a deployments
	// column — `deployments` carries `created_at` as its FIFO
	// tiebreaker. (Surfaced in CI round-5 dispatch 32671691532
	// pg-shard-2 — SQLSTATE 42703 column does not exist.)
	tag, err := tx.Exec(ctx, `
		UPDATE deployments
		   SET deleted_at = $3,
		       deleted_by_principal = 'system'
		 WHERE app_id = $1
		   AND status IN ('superseded', 'failed', 'cancelled')
		   AND created_at < $2
		   AND deleted_at IS NULL
		   AND id NOT IN (
		         SELECT id FROM (
		           SELECT id,
		                  ROW_NUMBER() OVER (
		                    PARTITION BY app_id
		                    ORDER BY created_at DESC, id
		                  ) AS rn
		             FROM deployments
		            WHERE app_id = $1
		         ) t
		        WHERE t.rn <= 2
		   )`,
		appID, olderThan, now,
	)
	if err != nil {
		return 0, fmt.Errorf("ClearObsoleteDeployments: bulk update: %w", err)
	}
	count := int(tag.RowsAffected())
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("ClearObsoleteDeployments: commit: %w", err)
	}
	return count, nil
}

// MarkBuildCancelled (ADR-124) atomically flips a builds row to
// BuildCancelled. The CAS guard enforces status ∈ {queued,
// running}; the cascade column records whether the cancel came
// from CancelDeploymentTx (true) or a future direct build-cancel
// path (false).
func (s *PgStore) MarkBuildCancelled(ctx context.Context, buildID, _ string, cascade bool, when time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE builds
		   SET status = $2,
		       cancelled_at = $3,
		       cancelled_by_deployment_cascade = $4
		 WHERE id = $1
		   AND status IN ('queued', 'running')`,
		buildID, string(BuildCancelled), when.UTC(), cascade,
	)
	if err != nil {
		return fmt.Errorf("MarkBuildCancelled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if e := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM builds WHERE id = $1)`, buildID).Scan(&exists); e != nil {
			return fmt.Errorf("MarkBuildCancelled: post-check: %w", e)
		}
		if !exists {
			return ErrNotFound
		}
		return ErrInvalidStateTransition
	}
	return nil
}

// AppendDeploymentStage (ADR-117, migration 00302) atomically
// appends a stage transition to deployments.stage_state.
//
// Shape:
//   - On entry (from != to): close the previous `current` row into
//     `history` with `ended_at = at`, `duration_ms = (at - current_started_at)`,
//     and `status = "completed"`. Then set the new `current` to `to`
//     with `current_started_at = at`.
//   - On failure (from == to): overwrite the active `current` row's
//     `status` to `"failed"` and stamp `reason`. `history` is
//     untouched — the active row stays active until a future
//     `from != to` call closes it.
//
// Implementation: read-modify-write at the Go layer. The existing
// `UpdateDeploymentStatus` (and `transition` chokepoint at
// pkg/imaged/handler.go:2349) is itself a bare UPDATE with no
// per-deployment mutex — concurrent transitions are "last write
// wins" by design, so this method preserves that same posture. A
// future PR could move the merge into a single SQL expression
// with `jsonb_set + jsonb_build_array`, but the Go-side shape is
// easier to reason about and matches the codebase's existing
// transition-write pattern.
//
// The from / to StageName vocabulary is enforced at the schema
// layer via `deployments_stage_state_current_check`
// (migrations/00302_deployments_stage_state.sql) so a typo from a
// future contributor lands as SQLSTATE 23514 at the storage layer
// before it can leak as a wire-frame typo on `event: stage {name}`.
// Stage-state status enum (ADR-117 §3). Local to this file so the
// goconst tripwire (3+ occurrences across the codebase) stays under
// the threshold for "failed" — every reference here is via the
// const.
const (
	stageHistoryStatusCompleted = "completed"
	stageHistoryStatusFailed    = "failed"
)

func (s *PgStore) AppendDeploymentStage(ctx context.Context, id string, from, to StageName, at time.Time, reason string) (Deployment, error) {
	existing, err := s.DeploymentByID(ctx, id)
	if err != nil {
		return Deployment{}, err
	}
	var state StageState
	if len(existing.StageState) > 0 {
		if err := json.Unmarshal(existing.StageState, &state); err != nil {
			return Deployment{}, fmt.Errorf("AppendDeploymentStage: decode stage_state for %s: %w", id, err)
		}
	}
	// Sanity: refuse if the row's `current` doesn't match `from`.
	// The caller is the transition chokepoint — drift here means a
	// future transition was queued behind a stale read and the
	// active row has already moved. Bail with ErrNotFound rather
	// than silently re-write history with a phantom entry.
	if state.Current != from {
		// Schema default for stage_state.current is
		// "source_download" (migrations/00302). A pre-existing row
		// that came through CreateDeployment without an explicit
		// StageState has Current == "" in the decoded view — but
		// the JSONB column on the Postgres row has the default
		// applied by the migration's DEFAULT clause. The
		// read-modify-write unmarshals to "" in our typed view
		// because the test fixture (memstore) doesn't run the
		// migration; the production path is fine. We treat the
		// empty string as the default so first-transition calls
		// don't surface a spurious ErrNotFound.
		if state.Current != "" {
			return Deployment{}, ErrNotFound
		}
		if from != StageSourceDownload {
			return Deployment{}, ErrNotFound
		}
		state.Current = StageSourceDownload
	}
	// Forward transition vs. failure-stamp. The two cases dispatch
	// on `from == to` (sentinel). After the review-cluster fix the
	// failure path is owned by `MarkDeploymentStageFailed` (below);
	// `from == to` here is a programming error and we surface it
	// loudly rather than silently mutating history[len-1] (the
	// previously-closed stage) which the previous version did.
	if from == to {
		return Deployment{}, fmt.Errorf("AppendDeploymentStage: from==to is reserved for MarkDeploymentStageFailed (deployment=%s, stage=%s)", id, from)
	}
	// Normal transition: close the active row, advance.
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	startedAt := at
	endedAt := at
	state.History = append(state.History, StageStateItem{
		Name:       from,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusCompleted,
	})
	state.Current = to
	state.CurrentStartedAt = &startedAt
	// ADR-117 §Production-ready follow-on, C1 — cap stage history
	// at MaxStageHistory entries (FIFO). Schema unchanged; the
	// migration 00340 docblock documents the cap. The trim lives
	// here (not as a jsonb CHECK) so future contributors can't
	// widen the field without seeing the cap. `state.Current` is
	// never trimmed — only the historical archive.
	if len(state.History) > MaxStageHistory {
		state.History = state.History[len(state.History)-MaxStageHistory:]
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("AppendDeploymentStage: encode stage_state for %s: %w", id, err)
	}
	tag, err := s.pool.Exec(ctx, `update deployments set stage_state = $2 where id = $1`, id, encoded)
	if err != nil {
		return Deployment{}, err
	}
	if tag.RowsAffected() == 0 {
		return Deployment{}, ErrNotFound
	}
	return s.DeploymentByID(ctx, id)
}

// derefTime dereferences *time.Time to time.Time, returning the
// zero value when the pointer is nil. Used by
// AppendDeploymentStage to flatten the optional
// current_started_at into the history entry's started_at — the
// first transition writes `started_at` from the migration's seed
// default. Pair with ptrTime to round-trip back to *time.Time
// when assigning into StageStateItem.StartedAt so the JSON wire
// shape emits JSON null (not the literal "0001-01-01T00:00:00Z"
// string time.Time{}.MarshalJSON would otherwise produce).
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ptrTime returns &t when t is non-zero, nil otherwise. Used to
// round-trip a derefTime result into the StageStateItem.StartedAt
// *time.Time field so the JSON wire shape preserves the null-vs-set
// distinction — time.Time{} would otherwise marshall as the literal
// "0001-01-01T00:00:00Z" string and break any consumer that treats
// "no start time" as null.
func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// MarkDeploymentStageFailed stamps the in-flight `state.Current`
// stage with status="failed" and the caller-supplied reason. The
// active stage is recorded as a NEW history entry (with status
// "failed") so the SSE consumer emits one final frame for the
// failing stage rather than overwriting the previously-closed
// stage (the previous "from == to" overload did the latter, which
// was the wire-shape bug the review cluster surfaced).
//
// Returns ErrNotFound when the deployment row does not exist or
// when state.Current is the zero value (no stage ever started).
func (s *PgStore) MarkDeploymentStageFailed(ctx context.Context, id string, at time.Time, reason string) (Deployment, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `select stage_state from deployments where id = $1`, id).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, fmt.Errorf("MarkDeploymentStageFailed: read stage_state for %s: %w", id, err)
	}
	var state StageState
	if uerr := json.Unmarshal(raw, &state); uerr != nil {
		return Deployment{}, fmt.Errorf("MarkDeploymentStageFailed: decode stage_state for %s: %w", id, uerr)
	}
	if state.Current == "" {
		return Deployment{}, ErrNotFound
	}
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	endedAt := at
	// The active stage is moved into history as a "failed" entry so
	// the wire shape is consistent: every stage that ever ran is in
	// history; the customer's ticker walks history in order. The
	// active row is cleared (Current = "") so the deployment row
	// reflects "no stage in flight" — the status column carries the
	// DeployFailed terminal value, set by the caller's separate
	// transition() call.
	state.History = append(state.History, StageStateItem{
		Name:       state.Current,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusFailed,
		Reason:     reason,
	})
	state.Current = ""
	state.CurrentStartedAt = nil
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("MarkDeploymentStageFailed: encode stage_state for %s: %w", id, err)
	}
	tag, err := s.pool.Exec(ctx, `update deployments set stage_state = $2 where id = $1`, id, encoded)
	if err != nil {
		return Deployment{}, err
	}
	if tag.RowsAffected() == 0 {
		return Deployment{}, ErrNotFound
	}
	return s.DeploymentByID(ctx, id)
}

// CloseDeploymentStage — pgstore mirror of the Store contract.
// See pkg/state/store.go::CloseDeploymentStage for the docblock.
// Closes the in-flight `state.Current` stage into history with
// status="completed" so the customer-facing wire shape carries a
// `duration_ms` for the readiness stage on a successful deploy.
func (s *PgStore) CloseDeploymentStage(ctx context.Context, id string, name StageName, at time.Time) (Deployment, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `select stage_state from deployments where id = $1`, id).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, fmt.Errorf("CloseDeploymentStage: read stage_state for %s: %w", id, err)
	}
	var state StageState
	if uerr := json.Unmarshal(raw, &state); uerr != nil {
		return Deployment{}, fmt.Errorf("CloseDeploymentStage: decode stage_state for %s: %w", id, uerr)
	}
	if state.Current == "" || state.Current != name {
		// Either nothing in flight, or the caller asked to close a
		// stage that isn't the active one. Both are programming
		// errors — the caller (imaged.MarkDeploymentLive) drives
		// the close immediately after the snapshot_prepare →
		// readiness transition, so the active row is always
		// `readiness` at this point. Surface ErrNotFound so the
		// caller logs a warning rather than silently dropping the
		// stamp.
		return Deployment{}, ErrNotFound
	}
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	endedAt := at
	state.History = append(state.History, StageStateItem{
		Name:       state.Current,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusCompleted,
	})
	state.Current = ""
	state.CurrentStartedAt = nil
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("CloseDeploymentStage: encode stage_state for %s: %w", id, err)
	}
	tag, err := s.pool.Exec(ctx, `update deployments set stage_state = $2 where id = $1`, id, encoded)
	if err != nil {
		return Deployment{}, err
	}
	if tag.RowsAffected() == 0 {
		return Deployment{}, ErrNotFound
	}
	return s.DeploymentByID(ctx, id)
}

// RetryDeploymentFromStage (ADR-117 §Production-ready follow-on,
// C2) inserts a fresh `deployments` row copying every input
// primitive from `failedID` and seeds `stage_state.current` to
// `fromStage` with an empty history. See Store.RetryDeploymentFromStage
// docblock for the wire contract.
//
// Implementation:
//  1. Validate fromStage against pkg/state.AllStageNames
//     (ErrInvalidArgument on unknown).
//  2. Read the failed row by DeploymentByID.
//  3. Build a new Deployment struct copying the input primitives
//     (ImageDigest, Kind, SourcePath/Root/Bytes, Handler, LogPath,
//     SourceURL, CommitSHA, Override*, Sidecars, MinInstances,
//     TrafficPercent, Scope). The actor attribution columns
//     (DeployedByUserID/Via/FromIP/PusherLogin) get a fresh
//     stamp at INSERT time — a retry is a new operator action.
//  4. INSERT a new row at status='pending' with stage_state =
//     `{current: fromStage, current_started_at: NULL, history: []}`.
//     The new row's id is uuid.NewString() so the wire SSE channel
//     can detect the retry as a row-creation event.
//
// Reversibility: the failed row is NOT mutated. The retry is a
// new row, not a status flip. This is intentional — failure
// history stays observable, and the customer-facing UI shows
// both the failed attempt and the retry in the same dashboard
// list.
func (s *PgStore) RetryDeploymentFromStage(ctx context.Context, failedID string, fromStage StageName) (Deployment, error) {
	// Step 1 — closed-vocab guard. A caller-supplied unknown stage
	// returns ErrInvalidArgument which the apid handler maps to a
	// 400 RFC 7807 problem.
	if !stageNameClosedSet[fromStage] {
		return Deployment{}, ErrInvalidArgument
	}
	// Step 2 — read the failed row.
	src, err := s.DeploymentByID(ctx, failedID)
	if err != nil {
		return Deployment{}, err
	}
	// Step 3 — build the new Deployment. The id is fresh; the
	// actor attribution columns (DeployedVia / DeployedByUserID /
	// DeployedFromIP / PusherLogin) carry over from the source
	// row. The retry represents the same deploy intent — the
	// operator who triggered the original failure also triggered
	// the retry (via the dashboard form or `gregale deploys
	// retry`), and the SOC 2 / GDPR audit-trail queries walk from
	// the failed row back to the deployer; stripping these
	// columns would break that linkage. See memstore mirror for
	// the code-review finding rationale.
	newDep := Deployment{
		ID:                    uuid.NewString(),
		AppID:                 src.AppID,
		BuildID:               "",
		ImageDigest:           src.ImageDigest,
		Kind:                  src.Kind,
		SourcePath:            src.SourcePath,
		SourceRoot:            src.SourceRoot,
		SourceBytes:           src.SourceBytes,
		Handler:               src.Handler,
		LogPath:               "",
		SourceURL:             src.SourceURL,
		CommitSHA:             src.CommitSHA,
		OverrideEntrypoint:    src.OverrideEntrypoint,
		OverrideCmd:           src.OverrideCmd,
		OverrideEnv:           src.OverrideEnv,
		OverrideEnvSecrets:    src.OverrideEnvSecrets,
		OverridePort:          src.OverridePort,
		OverrideHealthcheck:   src.OverrideHealthcheck,
		OverrideLivenessProbe: src.OverrideLivenessProbe,
		Sidecars:              src.Sidecars,
		MinInstances:          src.MinInstances,
		TrafficPercent:        src.TrafficPercent,
		Scope:                 src.Scope,
		DeployedVia:           src.DeployedVia,
		DeployedByUserID:      src.DeployedByUserID,
		DeployedFromIP:        src.DeployedFromIP,
		PusherLogin:           src.PusherLogin,
	}
	// Step 4 — INSERT with stage_state seeded to the requested
	// fromStage. We do not call CreateDeployment because that path
	// supersedes the prior live row (a retry is independent of
	// the prior row's status — it doesn't replace it). The seed
	// jsonb is marshalled here so the SQL is a single INSERT.
	stageSeed, err := json.Marshal(StageState{
		Current:          fromStage,
		CurrentStartedAt: nil,
		History:          []StageStateItem{},
	})
	if err != nil {
		return Deployment{}, fmt.Errorf("RetryDeploymentFromStage: encode stage_state seed: %w", err)
	}
	row := s.pool.QueryRow(ctx,
		`insert into deployments (app_id, image_digest, kind, source_path, source_root, source_bytes, handler, log_path, source_url, commit_sha,
		                          override_entrypoint, override_cmd, override_env, override_env_secrets, override_port, override_healthcheck,
		                          override_liveness_probe,
		                          sidecars,
		                          status,
		                          min_instances,
		                          traffic_percent,
		                          scope,
		                          deployed_by_user_id, deployed_via, deployed_from_ip, pusher_login,
		                          stage_state)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, 'pending', $19, $20,
		         coalesce(nullif($21, ''), 'default'),
		         nullif($22, '')::uuid, coalesce(nullif($23, ''), 'api'), nullif($24, '')::inet, nullif($25, ''),
		         $26)
		 returning `+deploymentSelectColumnsWithRootfs,
		newDep.AppID, newDep.ImageDigest, string(newDep.Kind),
		nullString(newDep.SourcePath), nullString(newDep.SourceRoot), newDep.SourceBytes,
		nullString(newDep.Handler), nullString(newDep.LogPath),
		nullString(newDep.SourceURL), nullString(newDep.CommitSHA),
		newDep.OverrideEntrypoint, newDep.OverrideCmd,
		nullJSONRaw(newDep.OverrideEnv), nullJSONRaw(newDep.OverrideEnvSecrets),
		nullableOverridePort(newDep.OverridePort), nullJSONRaw(newDep.OverrideHealthcheck),
		nullJSONRaw(newDep.OverrideLivenessProbe),
		notNullEmptyJSONRaw(newDep.Sidecars),
		newDep.MinInstances,
		newDep.TrafficPercent,
		newDep.Scope,
		// Code-review finding #3: actor attribution columns mirror
		// the CreateDeployment nullif/coalesce pattern (see
		// CreateDeployment at this file's earlier site). The retry
		// carries these from the source row (set above) so the
		// audit-trail linkage from failed row → deployer chips
		// survives a retry.
		newDep.DeployedByUserID, newDep.DeployedVia, newDep.DeployedFromIP, newDep.PusherLogin,
		stageSeed)
	created, err := scanDeployment(row)
	if err != nil {
		return Deployment{}, err
	}
	return created, nil
}

// StampFirstWake sets first_wake_at + first_5xx_window_ends_at if
// both are NULL. Idempotent: a second wake (the dashboard healthz
// probe waking the same deploy) is a no-op; only the first wake
// opens the 5xx window. Returns the post-stamp Deployment so the
// caller can decide whether to subscribe wake.response_5xx for
// this deployment. The window is anchored at first_wake_at +
// windowMinutes (default 5; RollbackOn5xxWindowMinutes).
//
// If the deploy has rollback_on_5xx=false (default), we still
// stamp the columns — schedd might query them later when the
// customer flips the flag (mid-window upgrades Hobby → Pro, see
// ADR-118 risk (g)).
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (s *PgStore) StampFirstWake(ctx context.Context, deploymentID string, windowMinutes int) (Deployment, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	row := s.pool.QueryRow(ctx, `
		update deployments
		   set first_wake_at = coalesce(first_wake_at, now()),
		       first_5xx_window_ends_at = coalesce(first_5xx_window_ends_at, now() + ($2::text || ' minutes')::interval)
		 where id = $1
		 returning `+deploymentSelectColumnsWithRootfs, deploymentID, windowMinutes)
	d, err := scanDeploymentWithRootfs(row)
	if err != nil {
		return Deployment{}, mapErr(err)
	}
	return d, nil
}

// BumpFirst5xxCount atomically increments deployments.first_5xx_count
// and returns the post-increment count. The schedd-side threshold
// check happens after this returns. Atomic via UPDATE ... RETURNING,
// so concurrent wake.response_5xx events on the same deploy can never
// lose an increment. Replay-safety is built-in: re-emitting the same
// event bumps the counter again, which is the conservative direction
// (it can trigger a no-op auto-rollback but cannot miss one).
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (s *PgStore) BumpFirst5xxCount(ctx context.Context, deploymentID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`update deployments
		    set first_5xx_count = first_5xx_count + 1
		  where id = $1
		  returning first_5xx_count`, deploymentID).Scan(&n)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// MarkAutoRollback stamps last_auto_rollback_at + last_auto_rollback_reason
// on the current (failed) deploy. Idempotent on the (id, reason) pair;
// re-stamping with the same reason is a no-op via the WHERE clause,
// so a duplicated auto-rollback signal from schedd does not double-write.
// reason MUST be 'threshold_exceeded' or 'first_window_expired' —
// the CHECK constraint deployments_last_auto_rollback_reason_check
// rejects anything else.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (s *PgStore) MarkAutoRollback(ctx context.Context, deploymentID, reason string, when time.Time) (Deployment, error) {
	if reason == "" {
		return Deployment{}, fmt.Errorf("pkgstate: MarkAutoRollback reason required")
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	row := s.pool.QueryRow(ctx, `
		update deployments
		   set last_auto_rollback_at = $2,
		       last_auto_rollback_reason = $3
		 where id = $1
		   and last_auto_rollback_reason is null
		 returning `+deploymentSelectColumnsWithRootfs, deploymentID, when, reason)
	d, err := scanDeploymentWithRootfs(row)
	if err != nil {
		return Deployment{}, mapErr(err)
	}
	return d, nil
}

// AutoRollbackDeploymentsTx performs the §6.2-1-safe rollback inside
// a single tx: supersede the current (failed) deploy and promote the
// latest superseded deploy back to live, stamping last_auto_rollback_at
// + last_auto_rollback_reason on the failed row. The instances-park
// belongs to schedd (the ONLY writer to instances per CLAUDE.md);
// this method mutates deployments only.
//
// Returns the new live deployment ID, or (empty, nil) if no
// superseded deploy exists (the rollback is a no-op — the failed
// deploy was the only one). Returns ErrNotFound when the current
// deployment row does not exist.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (s *PgStore) AutoRollbackDeploymentsTx(ctx context.Context, appID, currentDeploymentID string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// (1) Verify current row exists and is live. Prevents a stale
	// auto-rollback signal from schedd from rolling back an already-
	// superseded deploy (which would leave the previous live row
	// unstamped).
	var currentExists bool
	if err := tx.QueryRow(ctx,
		`select exists(select 1 from deployments where id = $1 and app_id = $2 and status = 'live')`,
		currentDeploymentID, appID).Scan(&currentExists); err != nil {
		return "", mapErr(err)
	}
	if !currentExists {
		return "", ErrNotFound
	}

	// (2) Find the latest superseded deploy on this app (the rollback
	// target). ORDER BY created_at DESC mirrors LatestSupersededDeployment
	// in cmd/apid so the manual + auto-rollback paths agree on the same
	// target.
	var targetID string
	err = tx.QueryRow(ctx, `
		select id from deployments
		 where app_id = $1 and status = 'superseded' and id <> $2
		 order by created_at desc
		 limit 1`, appID, currentDeploymentID).Scan(&targetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No rollback target — succeed as a no-op so schedd does
			// not retry forever. The failed deploy is left in place;
			// §6.2-1 is preserved because we did not promote a new
			// live row.
			return "", nil
		}
		return "", mapErr(err)
	}

	// (3) Status swap. The order matches the manual rollback path:
	// supersede current THEN promote target. Both are conditional on
	// the source status to keep the path idempotent against a
	// concurrent rollback signal.
	if _, err := tx.Exec(ctx,
		`update deployments set status = 'superseded' where id = $1 and status = 'live'`,
		currentDeploymentID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`update deployments set status = 'live' where id = $1 and status = 'superseded'`,
		targetID); err != nil {
		return "", err
	}

	// (4) Stamp the audit anchor on the failed deploy.
	if _, err := tx.Exec(ctx, `
		update deployments
		   set last_auto_rollback_at = coalesce(last_auto_rollback_at, now()),
		       last_auto_rollback_reason = coalesce(last_auto_rollback_reason, 'threshold_exceeded')
		 where id = $1`, currentDeploymentID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return targetID, nil
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

// UpsertDeploymentScanResult records the per-deploy grype CVE
// scan on the deployment row (issue #464 / ADR-055 / PR-3).
// The whole row's scan columns are overwritten — scan_result +
// scan_status + scanned_at — so a re-imaged rebuild's new
// scan replaces the prior scan in place. scanned_at is stamped
// at the SQL layer (now()) so the value is the server clock,
// not whatever the imaged client thinks it is (clock drift
// between imaged and apid is the typical cause of "scan older
// than the deploy" surprises on the dashboard).
//
// status MUST be 'complete' or 'failed' — the CHECK constraint
// deployments_scan_status_chk rejects any other value. The
// imaged-side call site (cmd/apid/scan_sink.go) is the only
// producer; the closed enum is enforced by the sink adapter
// before this function is reached.
//
// Returns ErrNotFound when the deployment row doesn't exist.
// In practice the FK CASCADE on deployments makes this
// unreachable; the explicit error mirrors SetDeploymentSidecarLayer.
func (s *PgStore) UpsertDeploymentScanResult(ctx context.Context, deploymentID string, scanResult []byte, status string) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set scan_result = $2, scan_status = $3, scanned_at = now()
		  where id = $1`,
		deploymentID, scanResult, nullString(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertDeploymentSecretFindings writes the secret-scan audit row
// (migrations/00221, secret-scan v2). Mirrors UpsertDeploymentScanResult:
// scopes to one deployment row, overwrites (not creates a second),
// and returns ErrNotFound on a missing row so a misuse at the call
// site fails closed. The scan_status on the deployment row is
// updated to the closed-set value the caller passes
// ('complete_with_redactions' on a non-empty findings list) so the
// dashboard pill reflects what the customer actually uploaded.
//
// We update scan_status in the SAME statement (rather than two
// round-trips) so the audit row + dashboard pill can never disagree:
// a future reader either sees both updated or neither. The
// `complete_with_redactions` value is enforced by the
// migrations/00221 CHECK widening.
func (s *PgStore) UpsertDeploymentSecretFindings(ctx context.Context, deploymentID string, findings []byte, status string, scannedAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set secret_findings = $2,
		        scan_status = $3,
		        secret_scanned_at = $4
		  where id = $1`,
		deploymentID, findings, nullString(status), scannedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordRestart (issue #586 / ADR-129 / cluster C commit 12)
// bumps the persisted deployments.liveness_restart_count column
// by 1 in a single statement. Mirrors
// UpsertDeploymentSecretFindings' IDOR + idempotency contract:
// scopes to one deployment row, returns ErrNotFound on a
// missing row so a misuse fails closed. The CHECK constraint
// (deployments_liveness_restart_count_nonneg_chk,
// migrations/00411) rejects a negative bump at the SQL layer.
// Called from pkg/sched/Engine alongside the in-memory
// LivenessWindow.RecordRestart call so the column is the source
// of truth across schedd restarts.
func (s *PgStore) RecordRestart(ctx context.Context, deploymentID string) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set liveness_restart_count = liveness_restart_count + 1
		  where id = $1`,
		deploymentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertStatusIncident (issue #599 / ADR-130 / cluster D commit 14)
// appends an open row to the status_incidents table. The CHECK
// constraints on component + severity enforce the closed-set
// vocabulary at the SQL layer so a typo at the CLI surface fails
// closed (23514). The id is BIGSERIAL; we RETURN it for the CLI
// to render ("incident <id> posted").
func (s *PgStore) InsertStatusIncident(ctx context.Context, component, severity, message string) (StatusIncident, error) {
	var inc StatusIncident
	inc.Component = component
	inc.Severity = severity
	inc.Message = message
	err := s.pool.QueryRow(ctx,
		`insert into status_incidents (component, severity, message)
		 values ($1, $2, $3)
		 returning id, posted_at`,
		component, severity, message,
	).Scan(&inc.ID, &inc.PostedAt)
	if err != nil {
		return StatusIncident{}, err
	}
	return inc, nil
}

// ResolveStatusIncident (issue #599 / ADR-130) stamps resolved_at
// on the row identified by id. Idempotent: a second call on an
// already-resolved row returns nil so the CLI can re-issue a
// resolve without surfacing 23514 / not-found. ErrNotFound when
// the id doesn't exist.
func (s *PgStore) ResolveStatusIncident(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`update status_incidents
		    set resolved_at = coalesce(resolved_at, now())
		  where id = $1`,
		id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOpenStatusIncidents (issue #599 / ADR-130) reads the partial
// index (status_incidents_open WHERE resolved_at IS NULL) sorted
// by posted_at DESC. The /v1/internal/slo.json endpoint composes
// its response from this list.
func (s *PgStore) ListOpenStatusIncidents(ctx context.Context) ([]StatusIncident, error) {
	rows, err := s.pool.Query(ctx,
		`select id, component, severity, message, posted_at, resolved_at
		   from status_incidents
		  where resolved_at is null
		  order by posted_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusIncident
	for rows.Next() {
		var inc StatusIncident
		if err := rows.Scan(&inc.ID, &inc.Component, &inc.Severity,
			&inc.Message, &inc.PostedAt, &inc.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// SetDeploymentSidecarLayer is the per-workload filesystem handle
// for sidecars (issue #463 / ADR-069 / PR-B). Upserts one row
// keyed by (deployment_id, sidecar_name). The whole row is
// overwritten on conflict — bytes + content_digest + storage_key
// — so a re-imaged rebuild's new key replaces the prior build's
// key without orphaned-key drift (the cleanupAppFiles path in
// pkg/imaged/handler.go deletes the OLD key before this returns,
// keeping storage in sync). updated_at is refreshed on every
// conflict; created_at is stamped once on the initial INSERT.
//
// Returns ErrNotFound when the deployment row doesn't exist —
// the FK CASCADE in migration 00119 makes this case unreachable
// in practice, but we surface it explicitly so a misuse at the
// caller (e.g. imaged on a removed deployment) fails closed.
func (s *PgStore) SetDeploymentSidecarLayer(ctx context.Context, l DeploymentSidecarLayer) (DeploymentSidecarLayer, error) {
	// Defence-in-depth: confirm the FK target row exists so the
	// caller gets a clean ErrNotFound before Postgres raises 23503
	// on the INSERT. The FK CASCADE handles delete-orphaning; this
	// check is for read-then-write paths in imaged.
	var exists string
	if err := s.pool.QueryRow(ctx,
		`select id from deployments where id = $1`, l.DeploymentID,
	).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DeploymentSidecarLayer{}, ErrNotFound
		}
		return DeploymentSidecarLayer{}, fmt.Errorf("state: sidecar layer parent check: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values ($1, $2, $3, $4, $5)
		on conflict (deployment_id, sidecar_name) do update
		set storage_key    = excluded.storage_key,
		    bytes          = excluded.bytes,
		    content_digest = excluded.content_digest,
		    updated_at     = now()
		returning deployment_id, sidecar_name, storage_key, bytes, content_digest, created_at, updated_at
	`, l.DeploymentID, l.SidecarName, l.StorageKey, l.Bytes, l.ContentDigest)
	var got DeploymentSidecarLayer
	if err := row.Scan(&got.DeploymentID, &got.SidecarName, &got.StorageKey,
		&got.Bytes, &got.ContentDigest, &got.CreatedAt, &got.UpdatedAt); err != nil {
		return DeploymentSidecarLayer{}, fmt.Errorf("state: sidecar layer upsert: %w", err)
	}
	return got, nil
}

// ListDeploymentSidecarLayers returns the deployment's full sidecar
// set ordered by sidecar_name ASC (issue #463 / ADR-069 / PR-B).
// Returns an empty slice when no sidecars exist; ErrNotFound only
// when the deployment itself is missing. vmmd's Wake path
// consumes this eagerly — Order-by-name keeps the workload slice
// deterministic across restarts so snapshots hash to the same
// drive set every time.
func (s *PgStore) ListDeploymentSidecarLayers(ctx context.Context, deploymentID string) ([]DeploymentSidecarLayer, error) {
	rows, err := s.pool.Query(ctx, `
		select deployment_id, sidecar_name, storage_key, bytes, content_digest, created_at, updated_at
		from deployment_sidecar_layers
		where deployment_id = $1
		order by sidecar_name asc
	`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("state: list sidecar layers: %w", err)
	}
	defer rows.Close()
	out := []DeploymentSidecarLayer{}
	for rows.Next() {
		var l DeploymentSidecarLayer
		if err := rows.Scan(&l.DeploymentID, &l.SidecarName, &l.StorageKey,
			&l.Bytes, &l.ContentDigest, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("state: scan sidecar layer: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate sidecar layers: %w", err)
	}
	return out, nil
}

// SetDeploymentSourceURL stamps the upstream URL + commit SHA on a
// deployment (Tier 3 / issue #197 B3.10 schema half, migrations/00047).
// Populated by githubd's CreateDeployment callback once the deployment
// row exists. Phase 2 (provenance) reads source_url + commit_sha from
// the deployment row when stamping build_provenance.source_url, so
// the two phases don't have to re-derive from the trigger.
//
// Both columns are nullable; passing empty strings is the normal case
// for image: deploys that don't have an upstream commit. The
// commit_sha length cap (64) is enforced by the DB CHECK
// (deployments_commit_sha_len_chk); a too-long value surfaces here
// as the same error the DB would have raised, so a unit-test path
// that goes through memstore and a production path that goes through
// pgstore both fail the same way.
func (s *PgStore) SetDeploymentSourceURL(ctx context.Context, id, sourceURL, commitSHA string) error {
	tag, err := s.pool.Exec(ctx,
		`update deployments
		    set source_url = $2, commit_sha = $3
		  where id = $1`,
		id, nullString(sourceURL), nullString(commitSHA))
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
		  returning `+deploymentSelectColumnsWithRootfs,
		id, nullString(message), nullString(code))
	return scanDeploymentWithRootfs(row)
}

// SetDeploymentFailedEx is the error-explanations cluster (spec §6.4
// amendment 1) extension of SetDeploymentFailed. It writes the four
// customer-facing prose fields (error_hint, error_why, error_fix,
// error_relevant_logs) alongside error_code so post-mortem retrieval
// via `gregale inspect <slug> --errors` surfaces the same hint/why/
// fix/relevant_logs block the deploy-time Problem emitted. Empty
// strings on the prose fields are normalised to NULL via nullString
// (mirroring the error_code column shape from migration 00021).
//
// error_relevant_logs is passed as a jsonb-encoded byte slice via
// logExcerptsJSON — pgx's jsonb codec writes []api.LogExcerpt
// cleanly. Empty input maps to NULL so post-migration rows that
// never wrote explanation prose stay NULL on this column. The
// SELECT projection in deploymentSelectColumnsWithRootfs does NOT
// coalesce error_relevant_logs — pgx scans the column into a
// []api.LogExcerpt slice directly, with NULL → nil.
//
// Idempotent on (status='failed') rows: a redeploy after a fix
// overwrites all four columns. The legacy SetDeploymentFailed
// (above) stays in place for callers that have only the code +
// message available (the imaged pre-build hook is the canonical
// pre-cluster caller).
func (s *PgStore) SetDeploymentFailedEx(
	ctx context.Context, id, code, message, hint, why, fix string, logs []api.LogExcerpt,
) (Deployment, error) {
	logsJSON := logExcerptsJSON(logs)
	row := s.pool.QueryRow(ctx,
		`update deployments
		    set status = 'failed', error = $2, error_code = $3,
		        error_hint = $4, error_why = $5, error_fix = $6,
		        error_relevant_logs = $7
		  where id = $1
		  returning `+deploymentSelectColumnsWithRootfs,
		id, nullString(message), nullString(code),
		nullString(hint), nullString(why), nullString(fix),
		logsJSON)
	return scanDeploymentWithRootfs(row)
}

// --- builds ------------------------------------------------------------------

func (s *PgStore) CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`insert into builds (deployment_id, kind, source_bytes, status, log_path)
		 values ($1, $2, $3, 'queued', $4)
		 returning id, deployment_id, kind, source_bytes, status,
		           coalesce(failure_class,''), coalesce(log_path,''), started_at, finished_at, enqueued_at,
		           cancelled_at, cancelled_by_deployment_cascade`,
		deploymentID, string(kind), sourceBytes, nullString(logPath))
	return scanBuild(row)
}

// CreateBuildWithID publishes a pre-uploaded source into the queue and
// advances its deployment under the same lock used by cancellation.
func (s *PgStore) CreateBuildWithID(ctx context.Context, id, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Build{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `update deployments set status='building' where id=$1 and status in ('pending','building')`, deploymentID)
	if err != nil {
		return Build{}, err
	}
	if tag.RowsAffected() != 1 {
		return Build{}, ErrNotFound
	}
	build, err := scanBuild(tx.QueryRow(ctx, `insert into builds(id,deployment_id,kind,source_bytes,status,log_path)
	values($1,$2,$3,$4,'queued',$5)
	returning id,deployment_id,kind,source_bytes,status,coalesce(failure_class,''),coalesce(log_path,''),started_at,finished_at,enqueued_at,cancelled_at,cancelled_by_deployment_cascade`, id, deploymentID, kind, sourceBytes, nullString(logPath)))
	if err != nil {
		return Build{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Build{}, err
	}
	return build, nil
}

func (s *PgStore) FailSourceDeployment(ctx context.Context, id, message string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A commit response may be lost while the queue transaction still owns
	// this row. Acquire its lock before checking builds on a fresh snapshot.
	var status DeploymentStatus
	if err := tx.QueryRow(ctx, `select status from deployments where id=$1 for update`, id).Scan(&status); err != nil {
		return mapErr(err)
	}
	if status != DeployPending && status != DeployBuilding {
		return nil
	}
	if _, err := tx.Exec(ctx, `update deployments set status='failed',error=$2 where id=$1
	and not exists(select 1 from builds where deployment_id=$1)`, id, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) BuildByID(ctx context.Context, id string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at, enqueued_at,
		        cancelled_at, cancelled_by_deployment_cascade
		 from builds where id = $1`, id)
	return scanBuild(row)
}

func (s *PgStore) BuildByDeployment(ctx context.Context, deploymentID string) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id, kind, source_bytes, status, coalesce(failure_class,''), coalesce(log_path,''),
		        started_at, finished_at, enqueued_at,
		        cancelled_at, cancelled_by_deployment_cascade
		 from builds where deployment_id = $1
		 order by started_at desc nulls last limit 1`, deploymentID)
	return scanBuild(row)
}

// UpdateBuildStatus flips the build row to a new status, with an
// optional failure_class + started/finished timestamps.
//
// CAS guard (issue #195 B1.4): when the requested status is a
// TERMINAL state (BuildSucceeded or BuildFailed), the WHERE clause
// requires the current status to be 'running'. A late-arriving
// markSucceeded from a builderd process that finishes AFTER the
// reaper sweep has flipped its row to 'failed(timeout)' must NOT
// resurrect the row. With the guard, the late writer's UPDATE
// matches 0 rows and returns ErrNotFound — the caller logs WARN and
// moves on.
//
// Non-terminal transitions (BuildRunning, BuildQueued) are NOT
// guarded — ClaimQueuedBuild and the legacy UpdateBuildStatus(
// BuildRunning, started=true) path both rely on a clean queued→
// running flip. The race we're guarding is exclusively the
// terminal-write-after-reaper path.
//
// Same shape for MemStore (the in-process equivalent of the SQL
// guard). FailureClass is the build's typed reason (`infra`,
// `user_error`, `timeout`); empty string preserves the existing value.
func (s *PgStore) UpdateBuildStatus(ctx context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error {
	var query string
	if status == BuildSucceeded || status == BuildFailed {
		// CAS guard: terminal write only succeeds if the row is
		// still 'running'. Catches the late-markSucceeded race.
		query = `update builds set
		   status        = $2,
		   failure_class = case when $3 = '' then failure_class else $3 end,
		   started_at    = case when $4 then now() else started_at end,
		   finished_at   = case when $5 then now() else finished_at end
		 where id = $1 and status = 'running'`
	} else {
		query = `update builds set
		   status        = $2,
		   failure_class = case when $3 = '' then failure_class else $3 end,
		   started_at    = case when $4 then now() else started_at end,
		   finished_at   = case when $5 then now() else finished_at end
		 where id = $1`
	}
	tag, err := s.pool.Exec(ctx, query, id, string(status), string(fc), started, finished)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateBuildProvenance stamps the post-mortem "what ran?" row for a
// successful Build (ADR-038, Tier 3 / issue #197 B3.1). The
// `ON CONFLICT (build_id) DO UPDATE` clause makes a redelivered
// build (LISTEN race between the apid write path and imaged's
// reaper; PR-A's redelivery dedupe) idempotent — the row is updated
// in place with the same values rather than failing with 23505.
//
// Builderd is best-effort: a failed call returns the error and
// logs at WARN inside pkg/builderd.recordProvenance. The build
// itself still succeeds (the builds row is the authoritative
// customer-visible transition; provenance is metadata). The apid
// reader renders 404 when the row is missing — a paging event
// whose root cause is "populator INSERT failed", not "build
// failed".
//
// The columns stamped cover all spec §9 fields; nullable ones use
// nullString so an empty input maps to NULL (e.g. cache-hit builds
// have empty buildkit_version / railpack_version / base_digest).
func (s *PgStore) CreateBuildProvenance(ctx context.Context, prov BuildProvenance) error {
	return createBuildProvenance(ctx, s.pool, prov)
}

type buildProvenanceWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func createBuildProvenance(ctx context.Context, writer buildProvenanceWriter, prov BuildProvenance) error {
	_, err := writer.Exec(ctx,
		`insert into build_provenance
		   (build_id, buildkit_version, railpack_version, base_digest, source_sha256,
		    source_url, commit_sha, plan, runner_digest, builder_node_id,
		    started_at, finished_at, sbom_storage_key, framework_version)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 on conflict (build_id) do update set
		   buildkit_version = excluded.buildkit_version,
		   railpack_version = excluded.railpack_version,
		   base_digest      = excluded.base_digest,
		   source_sha256    = excluded.source_sha256,
		   source_url       = excluded.source_url,
		   commit_sha       = excluded.commit_sha,
		   plan             = excluded.plan,
		   runner_digest    = excluded.runner_digest,
		   builder_node_id  = excluded.builder_node_id,
		   started_at       = excluded.started_at,
		   finished_at      = excluded.finished_at,
		   sbom_storage_key = coalesce(excluded.sbom_storage_key, build_provenance.sbom_storage_key),
		   framework_version = excluded.framework_version`,
		prov.BuildID,
		nullString(prov.BuildkitVer),
		nullString(prov.RailpackVer),
		nullString(prov.BaseDigest),
		prov.SourceSHA256,
		nullString(prov.SourceURL),
		nullString(prov.CommitSHA),
		nullString(prov.Plan),
		nullString(prov.RunnerDigest),
		nullString(prov.BuilderNodeID),
		prov.StartedAt,
		prov.FinishedAt,
		nullString(prov.SBOMStorageKey),
		nullString(prov.FrameworkVer),
	)
	return err
}

// BuildProvenanceByBuildID resolves the row by build_id. Returns
// ErrNotFound when the build has no provenance row — a pre-PR
// build, or a successful build whose populator INSERT failed and
// was logged at WARN inside builderd. The apid handler turns
// ErrNotFound into 404 with code=build_provenance_not_found.
func (s *PgStore) BuildProvenanceByBuildID(ctx context.Context, buildID string) (BuildProvenance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, build_id, coalesce(buildkit_version,''), coalesce(railpack_version,''),
		        coalesce(base_digest,''), source_sha256, coalesce(source_url,''), coalesce(commit_sha,''),
		        coalesce(plan,''), coalesce(runner_digest,''), coalesce(builder_node_id,''),
		        started_at, finished_at, coalesce(sbom_storage_key,''),
		        coalesce(framework_version,'')
		   from build_provenance where build_id = $1`, buildID)
	return scanBuildProvenance(row)
}

// UpdateBuildProvenanceSBOM stamps the SBOM storage key onto an
// existing build_provenance row (issue #299 / ADR-038 Phase 3).
// The SBOM populator runs in imaged AFTER the row is created by
// builderd's recordProvenance: by the time imaged has the source
// tree to enumerate, the build is already marked succeeded and
// the provenance row is in place. Empty sbomKey clears the
// column (best-effort: a syft failure leaves the cell NULL).
//
// Returns ErrNotFound when no row exists for buildID. The imaged
// call site logs at WARN and continues — the apid GET renders
// 503 build_sbom_unavailable rather than failing the build. We
// guard against the "build_provenance row never landed" race
// (a future builderd refactor that doesn't call
// CreateBuildProvenance) by explicit RowsAffected check.
func (s *PgStore) UpdateBuildProvenanceSBOM(ctx context.Context, buildID, sbomKey string) error {
	tag, err := s.pool.Exec(ctx,
		`update build_provenance set sbom_storage_key = $1 where build_id = $2`,
		nullString(sbomKey), buildID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SweepStuckRunningBuilds is the reaper sweep (issue #195 B1.4).
// Returns the number of build rows flipped. The owning in-flight
// deployment is failed in the same transaction so a crashed builder
// cannot leave the deployment permanently stuck in building.
// A partial index on builds(status='running') keeps this O(matches)
// instead of O(table).
func (s *PgStore) SweepStuckRunningBuilds(ctx context.Context, threshold time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var buildsFlipped, deploymentsFailed int
	err = tx.QueryRow(ctx, `
		with stuck as (
			select id, deployment_id
			  from builds
			 where status = 'running' and started_at < $1
			 for update skip locked
		),
		flipped as (
			update builds b
			   set status = 'failed',
			       failure_class = 'timeout',
			       finished_at = now()
			  from stuck s
			 where b.id = s.id
			returning s.deployment_id
		),
		failed_deployments as (
			update deployments d
			   set status = 'failed',
			       error = 'build timed out',
			       error_code = $2
			  from flipped f
			 where d.id = f.deployment_id
			   and d.status in ('pending', 'building', 'imaging', 'snapshotting')
			returning d.id
		)
		select
			(select count(*) from flipped),
			(select count(*) from failed_deployments)
	`, threshold, api.CodeBuildTimeout).Scan(&buildsFlipped, &deploymentsFailed)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return buildsFlipped, nil
}

// QueuedBuildsCount (operator-side observability mega-PR / Commit 7
// — P5) returns the number of builds currently in 'queued' state
// across the fleet. Per-node labeling is deferred (builds.target_node_id
// is not yet a column — adding it is a follow-up migration per the
// PR-open checklist). Today the gauge is a fleet-total; the operator
// dashboard renders "X builds in the queue" without per-schedd
// attribution. A partial index on builds(status='queued') keeps
// this O(matches) instead of O(table).
func (s *PgStore) QueuedBuildsCount(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from builds where status = 'queued'`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count queued builds: %w", err)
	}
	return n, nil
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
		           started_at, finished_at, enqueued_at,
		           cancelled_at, cancelled_by_deployment_cascade`,
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

// ClaimNextQueuedBuild is the durable worker surface (PR-B). Single
// statement so the lock + flip + RETURNING happens in one round-trip:
// the SKIP LOCKED subquery picks the earliest queued row that no
// concurrent claimer (pg_cron, a second builderd process) holds,
// the outer UPDATE flips it to 'running' and stamps started_at = now().
// Returns ErrNotFound when no queued row is available so the worker
// can sleep without surfacing an error. The shape mirrors
// ClaimQueuedBuild so ProcessOne's existing handling of ErrNotFound
// (drop silently) Just Works.
func (s *PgStore) ClaimNextQueuedBuild(ctx context.Context) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`update builds
		   set status = 'running', started_at = now()
		 where id = (
		   select id from builds
		    where status = 'queued'
		    order by enqueued_at asc
		    limit 1
		    for update skip locked
		 )
		 returning id, deployment_id, kind, source_bytes, status,
		           coalesce(failure_class,''), coalesce(log_path,''),
		           started_at, finished_at, enqueued_at,
		           cancelled_at, cancelled_by_deployment_cascade`)
	b, err := scanBuild(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Build{}, ErrNotFound
		}
		return Build{}, fmt.Errorf("state: claim next queued build: %w", err)
	}
	return b, nil
}

// ClaimNextQueuedBuildWithFairness prefers accounts without a recent claim,
// then falls back to FIFO. Lock the build row itself before returning it so
// polling workers and direct notification claims cannot execute one build
// twice. Ranking rather than filtering also lets a worker skip a locked quiet
// account's build and use another available row.
func (s *PgStore) ClaimNextQueuedBuildWithFairness(ctx context.Context, fairnessWindow time.Duration) (Build, error) {
	row := s.pool.QueryRow(ctx,
		`update builds set status='running', started_at = now()
    where status = 'queued' and id = (
      select b.id
        from builds b
        join deployments d on d.id = b.deployment_id
        join apps a on a.id = d.app_id
       where b.status = 'queued'
       order by exists (
         select 1 from recent_build_claims r
          where r.account_id = a.account_id
            and r.claimed_at > now() - $1::interval
       ), b.enqueued_at, b.id
       limit 1
       for update of b skip locked
    )
    returning id, deployment_id, kind, source_bytes, status,
              coalesce(failure_class,''), coalesce(log_path,''),
              started_at, finished_at, enqueued_at,
              cancelled_at, cancelled_by_deployment_cascade`,
		fmt.Sprintf("%d milliseconds", fairnessWindow.Milliseconds()))
	b, err := scanBuild(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Build{}, ErrNotFound
		}
		return Build{}, fmt.Errorf("state: claim next queued build (fairness): %w", err)
	}
	return b, nil
}

// RecordRecentBuildClaim records a single claim into recent_build_claims
// so the next ClaimNextQueuedBuildWithFairness round excludes this
// account from the "fresh" set. Called by builderd after a successful
// ClaimNextQueuedBuildWithFairness, with the build's account_id loaded
// from app.AccountID via the existing deployment → app join.
//
// The insert is intentionally append-only with no UPSERT. Multiple rows
// for the same account within the window are fine — the WHERE clause
// is "claimed_at > now() - interval", not "exists some row for this
// account", so a noisiy customer with N concurrent builds just gets N
// rows in the window. Keeping the row count = "claims within the
// window" is also useful operator telemetry in the meantime.
func (s *PgStore) RecordRecentBuildClaim(ctx context.Context, accountID, buildID string) error {
	if accountID == "" {
		return fmt.Errorf("state: record recent build claim: empty account_id")
	}
	_, err := s.pool.Exec(ctx,
		`insert into recent_build_claims (account_id, build_id) values ($1::uuid, $2::uuid)`,
		accountID, buildID)
	if err != nil {
		return fmt.Errorf("state: record recent build claim: %w", err)
	}
	return nil
}

// RequeueBuild resets a build row to queued when builderd's slot
// allocator (DecideSlot) rules it out (PR-B). enqueued_at is preserved
// verbatim so the row slots back into its original FIFO position;
// started_at is cleared. Returns ErrNotFound when the row is missing.
// The worker only requeues on a "no slot" verdict — never on
// transient errors (those bubble up and the supervisor restarts us).
func (s *PgStore) RequeueBuild(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update builds
		   set status = 'queued', started_at = NULL
		 where id = $1 and status = 'running'`,
		id)
	if err != nil {
		return fmt.Errorf("state: requeue build %s: %w", id, err)
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

// --- domain_doctor_observations (ADR-120) ----------------------

// UpsertDoctorObservation writes (or refreshes) the
// per-domain observation row. The poller is the sole
// writer. ON CONFLICT (domain) DO UPDATE means a second
// pass overwrites every field; the handler reads the
// latest row, so "race" between poller and handler
// is benign (the handler may see a slightly older row
// or a slightly newer one, both are correct).
//
// The COALESCE on $2 keeps the surface_id stable across
// passes: the poller enumerates both legacy custom_domains
// (surface_id=NULL) and tenant_hostnames (surface_id set);
// passing NULL on a row that already has a non-null
// surface_id would un-anchor the row from its surface.
// The legacy case (surface_id="" passed in) is translated
// to nil at the store boundary so the column gets NULL.
func (s *PgStore) UpsertDoctorObservation(ctx context.Context, obs DomainDoctorObservation) error {
	var surfaceID any
	if obs.SurfaceID != "" {
		surfaceID = obs.SurfaceID
	}
	var caaPermits any
	if obs.CAAPermits != nil {
		caaPermits = *obs.CAAPermits
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO domain_doctor_observations (
			domain, surface_id, observed_at,
			dns_record_found, points_to_gregale, caa_permits, ipv6_conflict,
			observed_target, observed_aaaa, caa_observed,
			cert_state, cert_not_after, last_error,
			dns_checked_at, cert_checked_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7,
			$8, $9, $10,
			$11, $12, $13,
			$14, $15
		)
		ON CONFLICT (domain) DO UPDATE SET
			surface_id        = COALESCE(EXCLUDED.surface_id, domain_doctor_observations.surface_id),
			observed_at       = EXCLUDED.observed_at,
			dns_record_found  = EXCLUDED.dns_record_found,
			points_to_gregale = EXCLUDED.points_to_gregale,
			caa_permits       = EXCLUDED.caa_permits,
			ipv6_conflict     = EXCLUDED.ipv6_conflict,
			observed_target   = EXCLUDED.observed_target,
			observed_aaaa     = EXCLUDED.observed_aaaa,
			caa_observed      = EXCLUDED.caa_observed,
			cert_state        = EXCLUDED.cert_state,
			cert_not_after    = EXCLUDED.cert_not_after,
			last_error        = EXCLUDED.last_error,
			dns_checked_at    = EXCLUDED.dns_checked_at,
			cert_checked_at   = EXCLUDED.cert_checked_at
	`, obs.Domain, surfaceID, obs.ObservedAt,
		obs.DNSRecordFound, obs.PointsToGregale, caaPermits, obs.IPv6Conflict,
		nullableStr(obs.ObservedTarget), nullableStr(obs.ObservedAAAA), nullableStr(obs.CAAObserved),
		obs.CertState, nullableTime(obs.CertNotAfter), nullableStr(obs.LastError),
		nullableTime(obs.DNSCheckedAt), nullableTime(obs.CertCheckedAt))
	return err
}

// GetDoctorObservation reads the latest row for a domain.
// Returns ErrNotFound when the poller has not yet written
// one — the handler treats this as stale:true and triggers
// a synchronous re-probe. Returns the row otherwise; the
// caller is responsible for the stale-check against
// observed_at vs FAAS_DOMAIN_DOCTOR_TTL_SECONDS.
func (s *PgStore) GetDoctorObservation(ctx context.Context, domain string) (DomainDoctorObservation, error) {
	var obs DomainDoctorObservation
	var surfaceID, observedTarget, observedAAAA, caaObserved, lastError *string
	var certNotAfter, dnsCheckedAt, certCheckedAt *time.Time
	var caaPermits *bool
	row := s.pool.QueryRow(ctx, `
		SELECT
			domain,
			surface_id::text,
			observed_at,
			dns_record_found, points_to_gregale, caa_permits, ipv6_conflict,
			observed_target, observed_aaaa, caa_observed,
			cert_state, cert_not_after, last_error,
			dns_checked_at, cert_checked_at
		FROM domain_doctor_observations
		WHERE domain = $1
	`, domain)
	err := row.Scan(
		&obs.Domain, &surfaceID, &obs.ObservedAt,
		&obs.DNSRecordFound, &obs.PointsToGregale, &caaPermits, &obs.IPv6Conflict,
		&observedTarget, &observedAAAA, &caaObserved,
		&obs.CertState, &certNotAfter, &lastError,
		&dnsCheckedAt, &certCheckedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return obs, ErrNotFound
		}
		return obs, err
	}
	if surfaceID != nil {
		obs.SurfaceID = *surfaceID
	}
	if observedTarget != nil {
		obs.ObservedTarget = *observedTarget
	}
	if observedAAAA != nil {
		obs.ObservedAAAA = *observedAAAA
	}
	if caaObserved != nil {
		obs.CAAObserved = *caaObserved
	}
	if lastError != nil {
		obs.LastError = *lastError
	}
	if caaPermits != nil {
		obs.CAAPermits = caaPermits
	}
	if certNotAfter != nil {
		obs.CertNotAfter = *certNotAfter
	}
	if dnsCheckedAt != nil {
		obs.DNSCheckedAt = *dnsCheckedAt
	}
	if certCheckedAt != nil {
		obs.CertCheckedAt = *certCheckedAt
	}
	return obs, nil
}

// ListAllCustomDomainsForDoctor returns the union of
// custom_domains.domain and tenant_hostnames.hostname
// so the poller has a single enumeration seam. The poller
// does NOT need the app_id or surface_id at enumeration
// time — those are joined lazily inside the per-domain
// probe pass to avoid carrying app/surface state on the
// poller goroutine.
//
// UNION ALL (not UNION) so Postgres skips the sort+hash
// dedup pass: the consumer is runDoctorForDomain, which
// upserts on domain_doctor_observations keyed by citext,
// so a duplicate row is a no-op. Dedup is the caller's
// job (the citext PK on the upsert target), not the
// query's.
func (s *PgStore) ListAllCustomDomainsForDoctor(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT domain FROM custom_domains
		UNION ALL
		SELECT hostname FROM tenant_hostnames
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// nullableStr returns nil for "" so the doctor's empty
// string columns translate to SQL NULL. A present-but-
// empty value is functionally equivalent to NULL (the
// handler treats "" and NULL identically when rendering).
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTime returns nil for the zero time so the
// doctor's unset timestamps translate to SQL NULL.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// OldestDoctorObservation (ADR-120 Tier A1) returns
// MIN(observed_at) across domain_doctor_observations. Returns
// the zero time.Time on an empty table so the dns_poller can
// distinguish "cold start" from "stalled loop" — a stale poll
// yields a non-zero MIN(...) that grows monotonically with
// time.Since(now) while a fresh cold start yields zero. The
// query is a single row scan; no transaction needed.
func (s *PgStore) OldestDoctorObservation(ctx context.Context) (time.Time, error) {
	var oldest *time.Time
	err := s.pool.QueryRow(ctx,
		`select min(observed_at) from domain_doctor_observations`,
	).Scan(&oldest)
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	if oldest == nil {
		return time.Time{}, nil
	}
	return *oldest, nil
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

// CreateCronIfUnderQuota is the customer-facing variant of
// CreateCron that enforces the per-app and per-account caps under
// an apps FOR UPDATE row lock (mirrors CreateAppIfUnderQuota). The
// per-app cap is checked first because the lock is on the apps row,
// not the account row; two concurrent POSTs for different apps on the
// same account serialise through the per-account count read inside
// the same tx.
//
// Failure modes:
//   - *CronQuotaError when either cap trips (Scope names which).
//   - ErrNotFound when the app row is gone or already deleted.
//   - mapErr-wrapped unique-violation on a future uuid collision
//     (today crons.id is gen_random_uuid() default; left as future-
//     proof so the surface doesn't change when a uuid scheme lands).
//
// The lock uses apps_pkey (id) — no extra index needed.
func (s *PgStore) CreateCronIfUnderQuota(ctx context.Context, appID, schedule, path string, enabled bool, limits api.Limits) (Cron, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Cron{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Lock the parent apps row. SELECT 1 + FOR UPDATE keeps the lock
	//    acquisition in one round-trip; the FOR UPDATE blocks any
	//    concurrent createCron for the same app until COMMIT/ROLLBACK.
	//    apps_pkey serves the lock search.
	var locked int
	err = tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status <> 'deleted' for update`, appID,
	).Scan(&locked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cron{}, ErrNotFound
		}
		return Cron{}, fmt.Errorf("state: lock app %s: %w", appID, err)
	}

	// 2. Per-app count, authoritative under the lock. crons_app_idx
	//    (app_id) WHERE enabled (migration 00002) covers this for the
	//    common case; disabled crons still count toward the cap.
	var appCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from crons where app_id = $1`, appID,
	).Scan(&appCount); err != nil {
		return Cron{}, fmt.Errorf("state: count crons for app %s: %w", appID, err)
	}
	if appCount >= limits.CronLimitPerApp {
		return Cron{}, &CronQuotaError{
			Scope:    CronQuotaScopeApp,
			Limit:    limits.CronLimitPerApp,
			Observed: appCount,
		}
	}

	// 3. Per-account count under the same tx. account_id is read off
	//    the apps row we just locked (no second round-trip to
	//    accounts). The join through apps excludes deleted apps so
	//    their cron rows don't poison the cap.
	var accountID string
	if err := tx.QueryRow(ctx,
		`select account_id from apps where id = $1`, appID,
	).Scan(&accountID); err != nil {
		return Cron{}, fmt.Errorf("state: read account_id for app %s: %w", appID, err)
	}
	var accountCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from crons c
		 join apps a on a.id = c.app_id
		 where a.account_id = $1 and a.status <> 'deleted'`,
		accountID,
	).Scan(&accountCount); err != nil {
		return Cron{}, fmt.Errorf("state: count crons for account %s: %w", accountID, err)
	}
	if accountCount >= limits.CronLimitPerAccount {
		return Cron{}, &CronQuotaError{
			Scope:    CronQuotaScopeAccount,
			Limit:    limits.CronLimitPerAccount,
			Observed: accountCount,
		}
	}

	// 4. Insert under the same lock. mapErr wraps unique-violation
	//    in ErrConflict for future-proofing.
	row := tx.QueryRow(ctx,
		`insert into crons (app_id, schedule, path, enabled) values ($1, $2, $3, $4)
		 returning id, app_id, schedule, path, enabled, created_at`,
		appID, schedule, path, enabled)
	c := Cron{}
	if err := row.Scan(&c.ID, &c.AppID, &c.Schedule, &c.Path, &c.Enabled, &c.CreatedAt); err != nil {
		return Cron{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Cron{}, fmt.Errorf("state: commit create cron: %w", err)
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

// StampAppScaleOut (PR-C, issue #462) writes the apps
// last_scale_out_at column to now(). Migration 00082 added the
// column. The stamp is best-effort and non-atomic with the
// instances INSERT — see Store.StampAppScaleOut's doc for the
// "stamp miss is safe" rationale. ErrNotFound returns when no
// row matches appID (defensive — schedd never calls this for an
// unknown app); callers should log and continue.
func (s *PgStore) StampAppScaleOut(ctx context.Context, appID string) error {
	tag, err := s.pool.Exec(ctx,
		`update apps set last_scale_out_at = now() where id = $1`, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// StampAppScaleIn (PR-C, issue #462) writes the apps
// last_scale_in_at column to now(). Same shape as
// StampAppScaleOut.
func (s *PgStore) StampAppScaleIn(ctx context.Context, appID string) error {
	tag, err := s.pool.Exec(ctx,
		`update apps set last_scale_in_at = now() where id = $1`, appID)
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

// --- alert rules (issue #396, ADR-045) ---------------------------------------
//
// Schema: migrations/00062_alert_rules.sql. Account-scoped webhook
// delivery for the {error rate, latency p50/p95/p99, cold-start %,
// request count, failed invocations} condition model, evaluated by
// meterd (PR 4). ClaimAlertFire mirrors LoadAndStampLastQuotaWarning:
// CTE captures the OLD stamp before the UPDATE so a "stamps equal"
// predicate can't trivially succeed post-write (PR #69 regression
// caught by CI; see pkg-state-usage-monthly-tz-compare.md).
//
// All write paths emit db.NotifyAlertRuleChanged so meterd can drop
// its enabled-rules cache before the next sweep; meterd's evaluator
// still re-reads on every signal because the cache stays advisory.
//
// Implementation notes that bit PR-A:
//   - app_id is a uuid; the scan helpers use *string so a NULL row
//     surfaces as "" while a real id is preserved. We deliberately
//     do NOT use *time.Time for uuid columns — the converter is
//     silent on bad type and a uuid through time.Time throws the
//     value away.
//   - failure_source is *string so the same select works whether the
//     row uses the failure-source dimension (failed_invocations) or
//     leaves it NULL (every other metric).
//   - webhook_secret_sealed is []byte (NOT NULL); the handler seals
//     via pkg/secretbox.SealOne before calling.

const alertRuleSelectCols = `id, account_id, app_id, name, enabled, metric, comparison,
       threshold, window_spec, failure_source, webhook_url,
       webhook_secret_sealed, cooldown_minutes, state,
       last_fired_at, last_evaluated_at, created_at, updated_at`

func scanAlertRule(row pgx.Row) (AlertRule, error) {
	r, err := scanAlertRuleCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AlertRule{}, ErrNotFound
		}
		return AlertRule{}, err
	}
	return r, nil
}

func scanAlertRules(rows pgx.Rows) ([]AlertRule, error) {
	var out []AlertRule
	for rows.Next() {
		r, err := scanAlertRuleCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanAlertRuleCols is the single source of column order for
// alert_rules. The select clause above is the contract — every
// SELECT against alert_rules lists these columns in this order, and
// the SELECT statement binds Scan's positional arguments against
// this list. A future column add lands here first, in the same
// commit, so a SELECT-write drift cannot silently swallow a column.
func scanAlertRuleCols(scan func(...any) error) (AlertRule, error) {
	r := AlertRule{}
	var metric, comparison, windowSpec, state string
	var appID, failureSource *string
	var secret []byte
	var lastFired, lastEvaluated *time.Time
	if err := scan(
		&r.ID, &r.AccountID, &appID, &r.Name, &r.Enabled,
		&metric, &comparison, &r.Threshold, &windowSpec, &failureSource,
		&r.WebhookURL, &secret, &r.CooldownMinutes, &state,
		&lastFired, &lastEvaluated, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return AlertRule{}, err
	}
	r.Metric = AlertMetric(metric)
	r.Comparison = AlertComparison(comparison)
	r.WindowSpec = AlertWindowSpec(windowSpec)
	r.State = AlertState(state)
	if failureSource != nil && *failureSource != "" {
		r.FailureSource = AlertFailureSource(*failureSource)
	}
	if appID != nil {
		r.AppID = *appID
	}
	if len(secret) > 0 {
		r.WebhookSecretSealed = secret
	}
	if lastFired != nil {
		r.LastFiredAt = *lastFired
	}
	if lastEvaluated != nil {
		r.LastEvaluatedAt = *lastEvaluated
	}
	return r, nil
}

func (s *PgStore) CreateAlertRule(ctx context.Context, in AlertRule) (AlertRule, error) {
	var appIDArg any
	if in.AppID != "" {
		appIDArg = in.AppID
	}
	var sourceArg any
	if in.FailureSource != "" {
		sourceArg = string(in.FailureSource)
	}
	stateArg := string(in.State)
	if stateArg == "" {
		stateArg = string(AlertStateOk)
	}
	row := s.pool.QueryRow(ctx, `
		insert into alert_rules (
			account_id, app_id, name, enabled, metric, comparison,
			threshold, window_spec, failure_source, webhook_url,
			webhook_secret_sealed, cooldown_minutes, state
		) values (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13
		)
		returning `+alertRuleSelectCols,
		in.AccountID, appIDArg, in.Name, in.Enabled,
		string(in.Metric), string(in.Comparison), in.Threshold,
		string(in.WindowSpec), sourceArg, in.WebhookURL,
		in.WebhookSecretSealed, in.CooldownMinutes, stateArg,
	)
	r, err := scanAlertRule(row)
	if err != nil {
		return AlertRule{}, mapErr(err)
	}
	return r, nil
}

// CreateAlertRuleIfUnderQuota — see Store interface. Account-wide
// rules (AppID == "") bypass the per-app row lock + per-app count;
// both flavours still hit the per-account count and the per-account
// cap. Returns:
//   - (AlertRule{}, ErrNotFound) when the app row is gone (only the
//     app-scoped branch can return this — account-wide rules with
//     a missing account row fall through to the FK violation on insert
//     and surface as ErrConflict)
//   - (AlertRule{}, *AlertRuleQuotaError) when either cap trips
func (s *PgStore) CreateAlertRuleIfUnderQuota(ctx context.Context, in AlertRule, limits api.Limits) (AlertRule, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AlertRule{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	if in.AppID != "" {
		var locked int
		err = tx.QueryRow(ctx,
			`select 1 from apps where id = $1 and status <> 'deleted' for update`, in.AppID,
		).Scan(&locked)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AlertRule{}, ErrNotFound
			}
			return AlertRule{}, fmt.Errorf("state: lock app %s: %w", in.AppID, err)
		}

		var appCount int
		if err := tx.QueryRow(ctx,
			`select count(*) from alert_rules where app_id = $1`, in.AppID,
		).Scan(&appCount); err != nil {
			return AlertRule{}, fmt.Errorf("state: count alert_rules for app %s: %w", in.AppID, err)
		}
		if appCount >= limits.AlertRuleLimitPerApp {
			return AlertRule{}, &AlertRuleQuotaError{
				Scope:    AlertRuleQuotaScopeApp,
				Limit:    limits.AlertRuleLimitPerApp,
				Observed: appCount,
			}
		}
	}

	// Per-account count excludes soft-deleted apps' alert rows.
	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from alert_rules r
		 where r.account_id = $1
		   and (r.app_id is null
		        or exists(select 1 from apps a
		                   where a.id = r.app_id and a.status <> 'deleted'))`,
		in.AccountID,
	).Scan(&accountCount); err != nil {
		return AlertRule{}, fmt.Errorf("state: count alert_rules for account %s: %w", in.AccountID, err)
	}
	if accountCount >= limits.AlertRuleLimitPerAccount {
		return AlertRule{}, &AlertRuleQuotaError{
			Scope:    AlertRuleQuotaScopeAccount,
			Limit:    limits.AlertRuleLimitPerAccount,
			Observed: accountCount,
		}
	}

	var appIDArg any
	if in.AppID != "" {
		appIDArg = in.AppID
	}
	var sourceArg any
	if in.FailureSource != "" {
		sourceArg = string(in.FailureSource)
	}
	row := tx.QueryRow(ctx, `
		insert into alert_rules (
			account_id, app_id, name, enabled, metric, comparison,
			threshold, window_spec, failure_source, webhook_url,
			webhook_secret_sealed, cooldown_minutes, state
		) values (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, 'ok'
		)
		returning `+alertRuleSelectCols,
		in.AccountID, appIDArg, in.Name, in.Enabled,
		string(in.Metric), string(in.Comparison), in.Threshold,
		string(in.WindowSpec), sourceArg, in.WebhookURL,
		in.WebhookSecretSealed, in.CooldownMinutes,
	)
	r, err := scanAlertRule(row)
	if err != nil {
		return AlertRule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AlertRule{}, fmt.Errorf("state: commit create alert rule: %w", err)
	}
	return r, nil
}

func (s *PgStore) AlertRuleByID(ctx context.Context, id string) (AlertRule, error) {
	row := s.pool.QueryRow(ctx,
		`select `+alertRuleSelectCols+` from alert_rules where id = $1`, id)
	return scanAlertRule(row)
}

// AlertRuleByAccountAppAndPresetName resolves the alert_rules row
// that was instantiated from a catalog preset. ADR-123 deliberately
// rejected a preset_id FK on alert_rules — the binding is the parsed
// display-name prefix "<DisplayName> (<app_slug>)". The query joins
// alert_presets on name to translate the catalog key into the prefix,
// then matches rules via LIKE '<prefix>%'. The existing
// alert_rules_account_name_uniq index covers the LIKE as a prefix
// range scan on (account_id, name).
//
// Returns:
//   - ErrNotFound when no rule matches the (account, app, preset)
//     tuple — the handler maps this to 404.
//   - ErrConflict when the LIKE matches >1 row (cannot happen today;
//     the name column is UNIQUE per (account_id, app_id), but the
//     surface stays defensive).
//
// Refs: ADR-123 PR-C, issue #1233, plan §Commit 2.
func (s *PgStore) AlertRuleByAccountAppAndPresetName(ctx context.Context, accountID, appID, presetName string) (AlertRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+alertRuleSelectCols+`
		FROM alert_rules r
		WHERE r.account_id = $1
		  AND r.app_id = $2
		  AND r.name LIKE (
		    SELECT (display_name || ' (%') FROM alert_presets WHERE name = $3
		  )
		ORDER BY r.created_at DESC
		LIMIT 2`, accountID, appID, presetName)
	if err != nil {
		return AlertRule{}, err
	}
	defer rows.Close()
	matched, err := scanAlertRules(rows)
	if err != nil {
		return AlertRule{}, err
	}
	switch len(matched) {
	case 0:
		return AlertRule{}, ErrNotFound
	case 1:
		return matched[0], nil
	default:
		// Defensive: catalog display_name uniqueness + the
		// (account_id, app_id, name) UNIQUE constraint should make
		// this unreachable. Returning ErrConflict keeps the handler
		// clean — 409 with a sane message beats a panic or a silent
		// "send test alert to a stale rule" outcome.
		return AlertRule{}, ErrConflict
	}
}

// UpdateAlertRule coalesces the optional fields onto alert_rules.
// All fields share the nil-skip pattern; WebhookSecretSealed is *[]byte
// because a nil means "leave the seal alone" (a non-rotation update
// path) and a non-nil replaces the column. FailureSource is not on
// UpdateAlertRuleParams — see the struct godoc for why metric and
// source rotate together or not at all.
func (s *PgStore) UpdateAlertRule(ctx context.Context, id string, p UpdateAlertRuleParams) (AlertRule, error) {
	var nameArg, urlArg any
	if p.Name != nil {
		nameArg = *p.Name
	}
	if p.WebhookURL != nil {
		urlArg = *p.WebhookURL
	}
	var secretArg any
	if p.WebhookSecretSealed != nil {
		secretArg = *p.WebhookSecretSealed
	}
	var metricArg, comparisonArg, windowArg any
	if p.Metric != nil {
		metricArg = string(*p.Metric)
	}
	if p.Comparison != nil {
		comparisonArg = string(*p.Comparison)
	}
	if p.WindowSpec != nil {
		windowArg = string(*p.WindowSpec)
	}

	row := s.pool.QueryRow(ctx, `
		update alert_rules set
			name    = coalesce($2, name),
			enabled = coalesce($3, enabled),
			metric  = coalesce($4, metric),
			comparison = coalesce($5, comparison),
			threshold  = coalesce($6, threshold),
			window_spec = coalesce($7, window_spec),
			webhook_url = coalesce($8, webhook_url),
			webhook_secret_sealed = coalesce($9, webhook_secret_sealed),
			cooldown_minutes = coalesce($10, cooldown_minutes),
			action  = coalesce($11, action),
			updated_at = now()
		where id = $1
		returning `+alertRuleSelectCols,
		id, nameArg, p.Enabled, metricArg, comparisonArg, p.Threshold,
		windowArg, urlArg, secretArg, p.CooldownMinutes, p.Action,
	)
	r, err := scanAlertRule(row)
	if err != nil {
		return AlertRule{}, mapErr(err)
	}
	return r, nil
}

func (s *PgStore) DeleteAlertRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from alert_rules where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAlertRulesForAccount(ctx context.Context, accountID string) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx,
		`select `+alertRuleSelectCols+` from alert_rules
		 where account_id = $1 order by created_at desc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertRules(rows)
}

func (s *PgStore) ListEnabledAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.pool.Query(ctx,
		`select `+alertRuleSelectCols+` from alert_rules where enabled = true order by account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertRules(rows)
}

// ----------------------------------------------------------------------------
// Edge rules (ADR-089, planned). Schema: migrations/00192_edge_rules.sql.
// apid is the only writer; gatewayd-internal reads via
// MatchEdgeRulesForHost. Per-app scope only — there is no
// account-wide flavour. The action column is jsonb (kind-tagged
// union); see EdgeRuleAction in types.go for the per-kind shapes.
//
// All write paths emit db.NotifyEdgeRuleChanged so the gatewayd LRU
// (pkg/gateway/edge_rule_cache.go, lands in PR 8) drops its cached
// host→rules snapshot. The cache stays advisory — the gatewayd
// matcher re-reads on every miss anyway, so a stale snapshot only
// widens the cache-hit window, never causes a wrong rule to fire.
// ----------------------------------------------------------------------------

const edgeRuleSelectCols = `id, account_id, app_id, match_host, match_path,
       match_methods, priority, enabled, kind, action,
       cors_preset_id, validate_mode, created_at, updated_at`

// scanEdgeRule reads a single row. ErrNotFound on no-rows; raw error
// otherwise. The kind column comes back as text; Action comes back
// as raw bytes (jsonb round-trips through encoding/json in
// scanEdgeRuleCols).
func scanEdgeRule(row pgx.Row) (EdgeRule, error) {
	r, err := scanEdgeRuleCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EdgeRule{}, ErrNotFound
		}
		return EdgeRule{}, err
	}
	return r, nil
}

// scanEdgeRules reads every row from a Rows iterator. The caller
// owns the rows.Close() lifetime — this helper only walks the
// iterator.
func scanEdgeRules(rows pgx.Rows) ([]EdgeRule, error) {
	var out []EdgeRule
	for rows.Next() {
		r, err := scanEdgeRuleCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanEdgeRuleCols is the single source of column order for
// edge_rules. The select clause above is the contract — every
// SELECT against edge_rules lists these columns in this order, and
// the SELECT statement binds Scan's positional arguments against
// this list. A future column add lands here first, in the same
// commit, so a SELECT-write drift cannot silently swallow a column.
func scanEdgeRuleCols(scan func(...any) error) (EdgeRule, error) {
	var (
		r            EdgeRule
		kind         string
		matchMethods []string
		actionBytes  []byte
		corsPresetID *string
	)
	if err := scan(
		&r.ID, &r.AccountID, &r.AppID, &r.MatchHost, &r.MatchPath,
		&matchMethods, &r.Priority, &r.Enabled, &kind, &actionBytes,
		&corsPresetID, &r.ValidateMode, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return EdgeRule{}, err
	}
	r.Kind = EdgeRuleKind(kind)
	r.MatchMethods = matchMethods
	if corsPresetID != nil {
		r.CorsPresetID = corsPresetID
	}
	if len(actionBytes) > 0 {
		if err := json.Unmarshal(actionBytes, &r.Action); err != nil {
			return EdgeRule{}, fmt.Errorf("state: decode edge_rules.action for %s: %w", r.ID, err)
		}
	}
	// Mirror the FK column into the action JSONB mirror so the
	// runtime edge_rules.go can read Action.CORS.CorsPresetID
	// without a second SELECT. The compile path also reads the
	// top-level field directly via MergeCorsPresetIntoRule.
	if r.CorsPresetID != nil && r.Action.CORS != nil {
		r.Action.CORS.CorsPresetID = r.CorsPresetID
	}
	return r, nil
}

// CreateEdgeRule is the un-capped insert path used by tests. The
// customer-facing handler always calls CreateEdgeRuleIfUnderQuota.
func (s *PgStore) CreateEdgeRule(ctx context.Context, in CreateEdgeRuleParams) (EdgeRule, error) {
	actionBytes, err := json.Marshal(in.Action)
	if err != nil {
		return EdgeRule{}, fmt.Errorf("state: marshal edge_rule.action: %w", err)
	}
	methods := in.MatchMethods
	if methods == nil {
		methods = []string{}
	}
	var corsPresetIDArg any
	if in.CorsPresetID != nil {
		corsPresetIDArg = *in.CorsPresetID
	}
	row := s.pool.QueryRow(ctx, `
		insert into edge_rules (
			account_id, app_id, match_host, match_path,
			match_methods, priority, enabled, kind, action,
			cors_preset_id, validate_mode
		) values (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9::jsonb,
			$10::uuid, coalesce(nullif($11, ''), 'block')
		)
		returning `+edgeRuleSelectCols,
		in.AccountID, in.AppID, in.MatchHost, in.MatchPath,
		methods, in.Priority, in.Enabled, string(in.Kind), actionBytes,
		// $10: cors_preset_id nullable FK (migration 00428). nil
		// pointer → SQL NULL = inline-only rule. The action
		// jsonb mirror is updated separately by scanEdgeRuleCols.
		corsPresetIDArg,
		// $11: empty string is the apid-handler convention for
		// "use the strictest mode"; coalesce turns it into
		// 'block' per ADR-128. The SQL-side default at 00293
		// would also fire on a column omission, but the explicit
		// coalesce keeps the wire surface consistent with the
		// empty-handler default at pkg/gateway/handler.go:2694.
		in.ValidateMode,
	)
	r, err := scanEdgeRule(row)
	if err != nil {
		return EdgeRule{}, mapErr(err)
	}
	return r, nil
}

// CreateEdgeRuleIfUnderQuota — see Store interface. Returns:
//   - (EdgeRule{}, *EdgeRuleQuotaError) when the per-app cap trips
//   - (EdgeRule{}, ErrNotFound) when the parent app row is missing
//   - (EdgeRule{}, ErrConflict) on FK violation (account gone)
//
// The FOR UPDATE row lock on the apps row is the TOCTOU defence:
// concurrent inserts serialise on the apps row before reading the
// count, so a burst of N parallel inserts can't race past the cap
// by N-1.
func (s *PgStore) CreateEdgeRuleIfUnderQuota(ctx context.Context, in CreateEdgeRuleParams, limits api.Limits) (EdgeRule, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeRule{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status <> 'deleted' for update`, in.AppID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EdgeRule{}, ErrNotFound
		}
		return EdgeRule{}, fmt.Errorf("state: lock app %s: %w", in.AppID, err)
	}

	var appCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from edge_rules where app_id = $1`, in.AppID,
	).Scan(&appCount); err != nil {
		return EdgeRule{}, fmt.Errorf("state: count edge_rules for app %s: %w", in.AppID, err)
	}
	if appCount >= limits.EdgeRulesPerApp {
		return EdgeRule{}, &EdgeRuleQuotaError{
			Limit:    limits.EdgeRulesPerApp,
			Observed: appCount,
		}
	}
	// Per-kind quota (ADR-091 D22). The general EdgeRulesPerApp
	// count above covers the cheap-tier guardrail; geo gets its own
	// tighter cap (Free=1, Hobby=5, Pro=25, Scale=100) so the
	// abuse-desk customer persona ("block everything except DE")
	// has one rule before the upgrade path kicks in. Both branches
	// share the same FOR UPDATE lock on apps for race-freedom.
	if in.Kind == EdgeRuleKindGeo && limits.EdgeRulesGeoPerApp > 0 {
		var geoPerApp int
		if err := tx.QueryRow(ctx,
			`select count(*) from edge_rules where app_id = $1 and kind = 'geo'`, in.AppID,
		).Scan(&geoPerApp); err != nil {
			return EdgeRule{}, fmt.Errorf("state: count edge_rules by kind=geo for app %s: %w", in.AppID, err)
		}
		if geoPerApp >= limits.EdgeRulesGeoPerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesGeoPerApp,
				Observed:   geoPerApp,
				Kind:       string(EdgeRuleKindGeo),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}
	// kind='throttle' per-app quota (ADR-091 D20.5 amendment, issue
	// #881). Mirrors the geo shape: tighter cap than
	// EdgeRulesPerApp because per-route throttles are a
	// higher-touch cardinality lever. Free customers get 1 rule
	// under EdgeRulesThrottlePerApp; Hobby 5; Pro 25; Scale 100.
	// Same FOR UPDATE lock on apps carried by the count above.
	if in.Kind == EdgeRuleKindThrottle && limits.EdgeRulesThrottlePerApp > 0 {
		var throttlePerApp int
		if err := tx.QueryRow(ctx,
			`select count(*) from edge_rules where app_id = $1 and kind = 'throttle'`, in.AppID,
		).Scan(&throttlePerApp); err != nil {
			return EdgeRule{}, fmt.Errorf("state: count edge_rules by kind=throttle for app %s: %w", in.AppID, err)
		}
		if throttlePerApp >= limits.EdgeRulesThrottlePerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesThrottlePerApp,
				Observed:   throttlePerApp,
				Kind:       string(EdgeRuleKindThrottle),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}
	// kind='cache' per-app quota (ADR-122 §Decision). Mirrors the
	// throttle shape: tighter cap than EdgeRulesPerApp because per
	// (host, path, vary) cache rules expand the route cardinality
	// and a single customer could otherwise pin the in-process store
	// (pkg/gateway/response_cache.go) to a fixed-size byte ceiling
	// with one rule per `vary_on` value. Free customers get 0 rules
	// under EdgeRulesCachePerApp (closed-set "Free cannot cache");
	// Hobby 1; Pro 5; Scale 20. Same FOR UPDATE lock on apps carried
	// by the throttle count above.
	if in.Kind == EdgeRuleKindCache && limits.EdgeRulesCachePerApp > 0 {
		var cachePerApp int
		if err := tx.QueryRow(ctx,
			`select count(*) from edge_rules where app_id = $1 and kind = 'cache'`, in.AppID,
		).Scan(&cachePerApp); err != nil {
			return EdgeRule{}, fmt.Errorf("state: count edge_rules by kind=cache for app %s: %w", in.AppID, err)
		}
		if cachePerApp >= limits.EdgeRulesCachePerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesCachePerApp,
				Observed:   cachePerApp,
				Kind:       string(EdgeRuleKindCache),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}

	actionBytes, err := json.Marshal(in.Action)
	if err != nil {
		return EdgeRule{}, fmt.Errorf("state: marshal edge_rule.action: %w", err)
	}
	methods := in.MatchMethods
	if methods == nil {
		methods = []string{}
	}
	row := tx.QueryRow(ctx, `
		insert into edge_rules (
			account_id, app_id, match_host, match_path,
			match_methods, priority, enabled, kind, action,
			validate_mode
		) values (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9::jsonb,
			coalesce(nullif($10, ''), 'block')
		)
		returning `+edgeRuleSelectCols,
		in.AccountID, in.AppID, in.MatchHost, in.MatchPath,
		methods, in.Priority, in.Enabled, string(in.Kind), actionBytes,
		// $10: same empty-string→'block' coalesce as the un-capped
		// CreateEdgeRule path (ADR-128).
		in.ValidateMode,
	)
	r, err := scanEdgeRule(row)
	if err != nil {
		return EdgeRule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EdgeRule{}, fmt.Errorf("state: commit create edge rule: %w", err)
	}
	return r, nil
}

func (s *PgStore) ListEdgeRulesForAccount(ctx context.Context, accountID string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx,
		`select `+edgeRuleSelectCols+` from edge_rules
		 where account_id = $1 order by priority asc, created_at desc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

func (s *PgStore) ListEdgeRulesForApp(ctx context.Context, appID string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx,
		`select `+edgeRuleSelectCols+` from edge_rules
		 where app_id = $1 order by priority asc, created_at desc`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

func (s *PgStore) GetEdgeRuleByID(ctx context.Context, id string) (EdgeRule, error) {
	row := s.pool.QueryRow(ctx,
		`select `+edgeRuleSelectCols+` from edge_rules where id = $1`, id)
	return scanEdgeRule(row)
}

// corsPresetSelectCols is the single source of column order for
// cors_presets. The select clause above is the contract — every
// SELECT against cors_presets lists these columns in this order, and
// the SELECT statement binds Scan's positional arguments against
// this list. A future column add lands here first, in the same
// commit, so a SELECT-write drift cannot silently swallow a column.
//
// Ordering matches the table definition in
// migrations/00294_cors_presets.sql (column list 1:1). nullable
// pointers (Description, AppID) come before the NOT NULL fields so
// Scan can target them.
const corsPresetSelectCols = `id, account_id, app_id, name, description,
       allow_origins, allow_methods, allow_headers, expose_headers,
       allow_credentials, max_age_seconds, created_at, updated_at`

// scanCorsPreset reads a single row. ErrNotFound on no-rows; raw
// error otherwise. All non-uuid / non-text[] fields are scanned
// directly; the text[] fields are pulled into []string by pgx's
// built-in array codec (registered on the pool's AfterConnect
// hook).
func scanCorsPreset(row pgx.Row) (CorsPreset, error) {
	r, err := scanCorsPresetCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CorsPreset{}, ErrNotFound
		}
		return CorsPreset{}, err
	}
	return r, nil
}

// scanCorsPresets reads every row from a Rows iterator. The caller
// owns the rows.Close() lifetime — this helper only walks the
// iterator.
func scanCorsPresets(rows pgx.Rows) ([]CorsPreset, error) {
	var out []CorsPreset
	for rows.Next() {
		r, err := scanCorsPresetCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanCorsPresetCols is the column-ordered Scan shared by both the
// single-row and Rows-iterator paths. Keeping a single function
// guarantees the SELECT statement, the const above, and the
// positional Scan arguments can never drift apart.
func scanCorsPresetCols(scan func(...any) error) (CorsPreset, error) {
	var (
		r             CorsPreset
		appID         *string
		description   *string
		allowOrigins  []string
		allowMethods  []string
		allowHeaders  []string
		exposeHeaders []string
	)
	if err := scan(
		&r.ID, &r.AccountID, &appID, &r.Name, &description,
		&allowOrigins, &allowMethods, &allowHeaders, &exposeHeaders,
		&r.AllowCredentials, &r.MaxAgeSeconds, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return CorsPreset{}, err
	}
	if appID != nil {
		r.AppID = *appID
	}
	if description != nil {
		r.Description = *description
	}
	// pgx decodes a DB empty text[] as a Go nil []string. The
	// merge helper treats len==0 as "take the other side", so a
	// round-trip of a preset that deliberately shipped no
	// headers would silently inherit the rule's value. Coalesce
	// nil to an empty slice so the round-trip is identity.
	r.AllowOrigins = nilToEmpty(allowOrigins)
	r.AllowMethods = nilToEmpty(allowMethods)
	r.AllowHeaders = nilToEmpty(allowHeaders)
	r.ExposeHeaders = nilToEmpty(exposeHeaders)
	return r, nil
}

// nilToEmpty coalesces a Go nil []string to an empty slice. Used
// at the pgx scan boundary so the round-trip of an empty text[]
// column is identity (the DB default '{}' reads back as a Go nil
// slice; callers cannot distinguish nil from [] otherwise).
func nilToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ListCorsPresetsForAccount returns every preset the account owns.
// Both account-wide (app_id IS NULL) and app-scoped (app_id = $2)
// presets are returned so the gatewayd compile path can apply the
// per-app overlay from a single round trip. The (account_id, name)
// ordering matches the deterministic cache key the compile path
// hashes — keeping the order stable lets the gatewayd side compute
// a content hash for "no change since last refresh" without
// re-sorting.
func (s *PgStore) ListCorsPresetsForAccount(ctx context.Context, accountID string) ([]CorsPreset, error) {
	rows, err := s.pool.Query(ctx,
		`select `+corsPresetSelectCols+` from cors_presets
		 where account_id = $1 order by app_id NULLS FIRST, name asc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCorsPresets(rows)
}

// ListCorsPresetsForApp returns the app-scoped presets for one
// app, scoped to the caller's account. (account-wide presets —
// app_id IS NULL — are not returned here; the compile path merges
// them via ListCorsPresetsForAccount + ListCorsPresetsForApp.)
// accountID is defense-in-depth: the apps row is already FK-scoped
// to one account so a WHERE on app_id alone is sufficient, but the
// Store boundary enforces tenancy at the API surface so a future
// caller can't probe by appID without knowing the account.
func (s *PgStore) ListCorsPresetsForApp(ctx context.Context, accountID, appID string) ([]CorsPreset, error) {
	rows, err := s.pool.Query(ctx,
		`select `+corsPresetSelectCols+` from cors_presets
		 where account_id = $1 and app_id = $2 order by name asc`, accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCorsPresets(rows)
}

// GetCorsPresetByID returns one preset scoped to the caller's
// account or ErrNotFound. The account_id predicate is in the WHERE
// clause so a cross-tenant lookup returns ErrNotFound (the
// pgx row.Scan errors on no rows), never a row from another
// tenant. The compile path uses this to resolve the preset_id
// stamped on a kind=cors rule; PR-B's apid CRUD surface also
// calls this and benefits from the same tenancy-at-the-boundary
// guarantee.
func (s *PgStore) GetCorsPresetByID(ctx context.Context, accountID, id string) (CorsPreset, error) {
	row := s.pool.QueryRow(ctx,
		`select `+corsPresetSelectCols+` from cors_presets
		 where account_id = $1 and id = $2`, accountID, id)
	return scanCorsPreset(row)
}

// CreateCorsPresetIfUnderQuota — see Store interface. Returns:
//   - (CorsPreset{}, *CorsPresetQuotaError) when either cap trips
//   - (CorsPreset{}, ErrNotFound) when AppID is set and the apps
//     row is gone (only the app-scoped branch can return this —
//     account-wide presets with a missing account row fall
//     through to the FK violation on insert and surface as
//     ErrConflict)
//   - (CorsPreset{}, ErrConflict) on UNIQUE collision
//     ((account_id, COALESCE(app_id, ...), name))
//
// The FOR UPDATE row lock on the apps row is the TOCTOU defence
// for app-scoped presets: concurrent inserts serialise on the
// apps row before reading the count, so a burst of N parallel
// inserts cannot race past the cap by N-1. Account-wide presets
// skip the apps-row lock and rely on the per-account count alone.
// The pg_notify trigger cors_presets_changed_notify (migration
// 00428) fires AFTER the INSERT commits, so the gatewayd-internal
// listener reloads the affected account's preset overlay.
func (s *PgStore) CreateCorsPresetIfUnderQuota(ctx context.Context, p CorsPreset, limits api.Limits) (CorsPreset, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CorsPreset{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	if p.AppID != "" {
		var locked int
		err = tx.QueryRow(ctx,
			`select 1 from apps where id = $1 and status <> 'deleted' for update`, p.AppID,
		).Scan(&locked)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CorsPreset{}, ErrNotFound
			}
			return CorsPreset{}, fmt.Errorf("state: lock app %s: %w", p.AppID, err)
		}

		var appCount int
		if err := tx.QueryRow(ctx,
			`select count(*) from cors_presets where app_id = $1`, p.AppID,
		).Scan(&appCount); err != nil {
			return CorsPreset{}, fmt.Errorf("state: count cors_presets for app %s: %w", p.AppID, err)
		}
		if appCount >= limits.CorsPresetsPerApp {
			return CorsPreset{}, &CorsPresetQuotaError{
				Scope:    CorsPresetQuotaScopeApp,
				Limit:    limits.CorsPresetsPerApp,
				Observed: appCount,
			}
		}
	}

	// Per-account count excludes soft-deleted apps' preset rows.
	// Mirrors the alert_rules per-account query at pgstore.go:7213
	// so the soft-delete semantics are consistent.
	var accountCount int
	if err := tx.QueryRow(ctx, `
		select count(*) from cors_presets p
		 where p.account_id = $1
		   and (p.app_id is null
		        or exists(select 1 from apps a
		                   where a.id = p.app_id and a.status <> 'deleted'))`,
		p.AccountID,
	).Scan(&accountCount); err != nil {
		return CorsPreset{}, fmt.Errorf("state: count cors_presets for account %s: %w", p.AccountID, err)
	}
	if accountCount >= limits.CorsPresetsPerAccount {
		return CorsPreset{}, &CorsPresetQuotaError{
			Scope:    CorsPresetQuotaScopeAccount,
			Limit:    limits.CorsPresetsPerAccount,
			Observed: accountCount,
		}
	}

	var appIDArg any
	if p.AppID != "" {
		appIDArg = p.AppID
	}
	var descriptionArg any
	if p.Description != "" {
		descriptionArg = p.Description
	}
	row := tx.QueryRow(ctx, `
		insert into cors_presets (
			account_id, app_id, name, description,
			allow_origins, allow_methods, allow_headers, expose_headers,
			allow_credentials, max_age_seconds
		) values (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10
		)
		returning `+corsPresetSelectCols,
		p.AccountID, appIDArg, p.Name, descriptionArg,
		nilToEmpty(p.AllowOrigins), nilToEmpty(p.AllowMethods),
		nilToEmpty(p.AllowHeaders), nilToEmpty(p.ExposeHeaders),
		p.AllowCredentials, p.MaxAgeSeconds,
	)
	r, err := scanCorsPreset(row)
	if err != nil {
		// UNIQUE collision on (account_id, COALESCE(app_id, ...),
		// name) → ErrConflict (the apid boundary maps to 409
		// "name already in use"). mapErr translates 23505 →
		// ErrConflict per the pgstore 23505-→-ErrConflict
		// precedent.
		return CorsPreset{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CorsPreset{}, fmt.Errorf("state: commit cors_preset insert: %w", err)
	}
	return r, nil
}

// UpdateCorsPreset replaces the entire row (the customer re-sends
// the full preset body, like UpdateEdgeRule). The
// (account_id, COALESCE(app_id, ...), name) UNIQUE constraint is
// the write-side defence against cross-tenant IDOR — a malicious
// caller cannot UPDATE a preset owned by another account because
// the WHERE clause pins account_id. The pg_notify trigger fires
// AFTER the UPDATE commits so the gatewayd-internal listener
// reloads the affected account's preset overlay.
func (s *PgStore) UpdateCorsPreset(ctx context.Context, accountID, id string, p CorsPreset) (CorsPreset, error) {
	var appIDArg any
	if p.AppID != "" {
		appIDArg = p.AppID
	}
	var descriptionArg any
	if p.Description != "" {
		descriptionArg = p.Description
	}
	row := s.pool.QueryRow(ctx, `
		update cors_presets set
			app_id           = $2,
			name             = $3,
			description      = $4,
			allow_origins    = $5,
			allow_methods    = $6,
			allow_headers    = $7,
			expose_headers   = $8,
			allow_credentials = $9,
			max_age_seconds  = $10
		where account_id = $11 and id = $1
		returning `+corsPresetSelectCols,
		id, appIDArg, p.Name, descriptionArg,
		nilToEmpty(p.AllowOrigins), nilToEmpty(p.AllowMethods),
		nilToEmpty(p.AllowHeaders), nilToEmpty(p.ExposeHeaders),
		p.AllowCredentials, p.MaxAgeSeconds, accountID,
	)
	r, err := scanCorsPreset(row)
	if err != nil {
		return CorsPreset{}, mapErr(err)
	}
	return r, nil
}

// DeleteCorsPreset removes a preset by id (scoped to the caller's
// account; cross-account deletes return ErrNotFound). The
// pg_notify trigger fires AFTER the DELETE commits so any
// gatewayd-internal compile cache that references this preset
// via edge_rules.cors_preset_id is invalidated; the FK ON DELETE
// SET NULL clears the rule's FK column atomically with the
// preset's removal, so the next compile reads the preset as
// missing and MergeCorsPresetIntoRule fails closed (ADR-129 D3).
func (s *PgStore) DeleteCorsPreset(ctx context.Context, accountID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from cors_presets where account_id = $1 and id = $2`, accountID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- alert_presets (issue #1233, ADR-123) -----------------------------------
//
// Catalog rows are system-owned. Customers have SELECT-only via the
// apid GET surface. The Store interface exposes two read methods
// (ListAlertPresets, AlertPresetByName) and zero mutators — the only
// write path is migration 00348's idempotent seed.
//
// Hand-written (not sqlc) per the cors_presets precedent at
// pgstore.go:7047-7060: the partial-index / closed-vocab shape makes
// sqlc's emit lossy and we prefer a single hand-written const that
// the SELECT clauses and scan functions bind against byte-for-byte.

// alertPresetSelectCols is the single source of column order for
// alert_presets. Every SELECT against alert_presets lists these
// columns in this order, and the SELECT statement binds Scan's
// positional arguments against this list. Mirrors the
// corsPresetSelectCols pattern at pgstore.go:7047-7060.
//
// Ordering matches migrations/00347_alert_presets.sql (column list
// 1:1). Nullable fields (none today — all columns are NOT NULL
// per the migration) would come before NOT NULL so Scan can
// target them; since every column is NOT NULL the order is the
// same as the table definition.
const alertPresetSelectCols = `id, name, display_name, description,
       category, metric, comparison, threshold, window_spec,
       default_cooldown_minutes, enabled_in_catalog, minimum_plan,
       created_at, updated_at`

func scanAlertPreset(row pgx.Row) (AlertPreset, error) {
	r, err := scanAlertPresetCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AlertPreset{}, ErrNotFound
		}
		return AlertPreset{}, err
	}
	return r, nil
}

func scanAlertPresets(rows pgx.Rows) ([]AlertPreset, error) {
	var out []AlertPreset
	for rows.Next() {
		r, err := scanAlertPresetCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanAlertPresetCols(scan func(...any) error) (AlertPreset, error) {
	var r AlertPreset
	if err := scan(
		&r.ID, &r.Name, &r.DisplayName, &r.Description,
		&r.Category, &r.Metric, &r.Comparison, &r.Threshold, &r.WindowSpec,
		&r.DefaultCooldownMinutes, &r.EnabledInCatalog, &r.MinimumPlan,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return AlertPreset{}, err
	}
	return r, nil
}

// ListAlertPresets returns every catalog row, ordered by category
// then name. The dashboard grid renders the same order so the
// (category, name) sort key is the canonical render order. No
// filtering by enabled_in_catalog here — the apid handler filters
// out disabled rows after consulting the customer's plan tier
// (defence-in-depth: the closed-set check on minimum_plan is the
// authoritative gate, not a SQL filter).
//
// Catalog cardinality is bounded (8 rows today) so no pagination
// is needed; the slice fits in a single round trip.
func (s *PgStore) ListAlertPresets(ctx context.Context) ([]AlertPreset, error) {
	rows, err := s.pool.Query(ctx,
		`select `+alertPresetSelectCols+` from alert_presets
		 order by category asc, name asc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertPresets(rows)
}

// AlertPresetByName returns the catalog row for a stable name key
// ('error_rate_2pct', 'p95_latency_1s', ...) or ErrNotFound.
// The apid enable handler calls this to resolve the preset before
// pre-filling the createAlertRule payload. name is the catalog
// primary key by convention (UNIQUE on the DB) so the result is
// at most one row.
func (s *PgStore) AlertPresetByName(ctx context.Context, name string) (AlertPreset, error) {
	row := s.pool.QueryRow(ctx,
		`select `+alertPresetSelectCols+` from alert_presets
		 where name = $1`, name)
	return scanAlertPreset(row)
}

// CountFailedDeploymentsSince mirrors CountFailedInvocationsSince
// but walks the deployments table instead of invocations. Used by
// the alert evaluator's deployment_failed metric case (issue #1233,
// ADR-123). appID "" means "any app on this account" via the
// subquery against apps.account_id — the deployments table does
// not carry account_id directly.
//
// Walks the deployments table per migrations/00001_init.sql: the
// status CHECK constraint includes 'failed' so a `status = 'failed'`
// predicate is the canonical match. The deployments index on
// (app_id, created_at) keeps the per-app scan bounded.
func (s *PgStore) CountFailedDeploymentsSince(ctx context.Context, accountID, appID string, since time.Time) (int, error) {
	var appArg any
	if appID != "" {
		appArg = appID
	}
	var n int
	row := s.pool.QueryRow(ctx, `
		select count(*) from deployments d
		 join apps a on a.id = d.app_id
		 where a.account_id = $1
		   and d.status = 'failed'
		   and d.created_at >= $2
		   and ($3::uuid is null or d.app_id = $3::uuid)`,
		accountID, since.UTC(), appArg)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// WasInvokedSuccessfullySince returns true iff at least one
// successful (terminal state != 'failed') invocation exists for
// (account, app) in the window. Used by the alert evaluator's
// api_up metric case (issue #1233, ADR-123) — the binary reachability
// signal. Returns false when the window is empty (cold start).
//
// appID "" means "any app on this account" via IS NOT DISTINCT FROM
// NULL — the same pattern as CountFailedInvocationsSince.
func (s *PgStore) WasInvokedSuccessfullySince(ctx context.Context, accountID, appID string, since time.Time) (bool, error) {
	var appArg any
	if appID != "" {
		appArg = appID
	}
	var exists bool
	row := s.pool.QueryRow(ctx, `
		select exists(
			select 1 from invocations
			 where account_id = $1
			   and state <> 'failed'
			   and created_at >= $2
			   and ($3::uuid is null or app_id is not distinct from $3::uuid)
			 limit 1)`,
		accountID, since.UTC(), appArg)
	if err := row.Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// MTDSpendEurCents returns the SUM(eur_cents) of every
// account_spend_snapshot row for the account whose period_start
// is within the current UTC month-to-date window. Used by the
// alert evaluator's account_spend_eur metric case (issue #1233,
// ADR-123).
//
// MTD boundary is computed at evaluation time (now() at the UTC
// midnight of the first day of the current month) so the window
// is stable across meterd restarts. The (account_id, period_start
// DESC) partial index at migrations/00350 keeps the scan bounded.
func (s *PgStore) MTDSpendEurCents(ctx context.Context, accountID string) (int64, error) {
	var total int64
	row := s.pool.QueryRow(ctx, `
		select coalesce(sum(eur_cents), 0)::bigint from account_spend_snapshot
		 where account_id = $1
		   and period_start >= date_trunc('month', now() at time zone 'utc')`,
		accountID)
	if err := row.Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// UpsertAccountSpendSnapshot is called by the meterd tick loop on
// every AlertEvalInterval. Inserts a fresh row tagged with the
// tick's (period_start, period_end). The ON CONFLICT (account_id,
// source, period_end) clause at migrations/00350 makes a double-fire
// (e.g. meterd restart mid-tick) idempotent.
func (s *PgStore) UpsertAccountSpendSnapshot(ctx context.Context, accountID string, periodStart, periodEnd time.Time, gbSeconds float64, eurCents int64, source string) error {
	_, err := s.pool.Exec(ctx, `
		insert into account_spend_snapshot
			(account_id, period_start, period_end, gb_seconds, eur_cents, source)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (account_id, source, period_end) do update set
			gb_seconds = excluded.gb_seconds,
			eur_cents = excluded.eur_cents`,
		accountID, periodStart.UTC(), periodEnd.UTC(), gbSeconds, eurCents, source)
	return err
}

// MinCertExpiryForApp returns the smallest remaining seconds until
// cert expiry across all per-app tenant_surfaces for the given
// (account, app), or -1 when no surface has a cert (the alert
// evaluator treats -1 as "no signal"). Used by the alert
// evaluator's cert_expiry_seconds metric case (issue #1233,
// ADR-123).
//
// Walks the meterd_tenant_surface_cert_expiry_state table built by
// the meterd refresher (migrations/00351) — the meterd side
// keeps the per-host state (CLAUDE.md "apid is the ONLY writer
// to customer-intent tables" rule: this is a derived signal
// cache, not customer intent, so the meter daemon owns it); the
// evaluator reads the min.
func (s *PgStore) MinCertExpiryForApp(ctx context.Context, accountID, appID string) (int64, error) {
	var minSeconds *int64
	row := s.pool.QueryRow(ctx, `
		select min(extract(epoch from (last_observed_cert_not_after - now())))::bigint
		  from meterd_tenant_surface_cert_expiry_state
		 where account_id = $1
		   and app_id = $2
		   and last_walk_status = 'ok'
		   and last_observed_cert_not_after is not null`,
		accountID, appID)
	if err := row.Scan(&minSeconds); err != nil {
		return 0, err
	}
	if minSeconds == nil {
		return -1, nil
	}
	return *minSeconds, nil
}

// RefreshCertExpiryStates walks every tenant_surfaces row whose
// cert_state='issued', upserts a meterd_tenant_surface_cert_expiry_state
// mirror row per (surface, hostname) pair, and stamps
// last_refreshed_at=now(). Called by the meterd cert-expiry
// refresher goroutine on a 1-hour cadence (issue #1233 / ADR-123).
// Returns the number of rows upserted.
//
// tenant_surfaces carries the cert metadata (cert_state,
// cert_not_after) and a one-to-many to tenant_hostnames. The mirror
// row needs hostname for the dashboard gauge label set, so the
// SELECT JOINs tenant_hostnames on surface_id = ts.id and emits
// one row per (surface, hostname) pair. ON CONFLICT
// (tenant_surface_id, hostname) DO UPDATE keeps the per-host
// last_observed_cert_not_after in sync with the parent's
// cert_not_after (which the renewer bot may rotate daily).
// The status is 'ok' on a clean upsert; 'cert_unissued' on a
// parent whose cert_not_after is NULL despite cert_state='issued'
// (defensive — the CHECK in 00243 should prevent it but we don't
// crash on the defensive read).
func (s *PgStore) RefreshCertExpiryStates(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		insert into meterd_tenant_surface_cert_expiry_state (
			tenant_surface_id, account_id, app_id, hostname,
			last_observed_cert_not_after, last_walk_status, last_refreshed_at
		)
		select ts.id, ts.account_id, ts.app_id, th.hostname,
		       ts.cert_not_after,
		       case when ts.cert_not_after is null then 'cert_unissued' else 'ok' end,
		       now()
		  from tenant_surfaces ts
		  join tenant_hostnames th on th.surface_id = ts.id
		 where ts.cert_state = 'issued'
		on conflict (tenant_surface_id, hostname) do update set
			last_observed_cert_not_after = excluded.last_observed_cert_not_after,
			last_walk_status              = excluded.last_walk_status,
			last_refreshed_at             = excluded.last_refreshed_at`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ListCertExpiryStateForWalker returns every row in
// meterd_tenant_surface_cert_expiry_state whose last_refreshed_at
// is fresher than (now() - staleCutoff). The refresher uses this
// to stamp the meterd_tenant_surface_cert_expiry_seconds gauge.
func (s *PgStore) ListCertExpiryStateForWalker(ctx context.Context, staleCutoff time.Duration) ([]TenantSurfaceCertExpiryState, error) {
	rows, err := s.pool.Query(ctx, `
		select tenant_surface_id, account_id, app_id, hostname,
		       last_observed_cert_not_after, last_walk_status, last_refreshed_at
		  from meterd_tenant_surface_cert_expiry_state
		 where last_refreshed_at >= now() - make_interval(secs => $1)`,
		staleCutoff.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TenantSurfaceCertExpiryState
	for rows.Next() {
		var r TenantSurfaceCertExpiryState
		var notAfter *time.Time
		if err := rows.Scan(&r.TenantSurfaceID, &r.AccountID, &r.AppID, &r.Hostname,
			&notAfter, &r.LastWalkStatus, &r.LastRefreshedAt); err != nil {
			return nil, err
		}
		r.LastObservedCertNotAfter = notAfter
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateEdgeRule coalesces the optional fields onto edge_rules. The
// nil-skip pattern is identical to UpdateAlertRule; Action uses the
// `case when $N then $N+1::jsonb else action end` shape so a nil
// pointer leaves the jsonb column untouched. The kind column is
// NOT on UpdateEdgeRuleParams — rotating kind mid-life would break
// the action union (a 'cors' action has no fields a 'route' rule
// expects); the customer deletes + recreates instead.
func (s *PgStore) UpdateEdgeRule(ctx context.Context, id string, p UpdateEdgeRuleParams) (EdgeRule, error) {
	var (
		hostArg, pathArg any
		methodsArg       any
		actionArg        any
		validateModeArg  any
		corsPresetSet    bool
		corsPresetValue  any
	)
	if p.MatchHost != nil {
		hostArg = *p.MatchHost
	}
	if p.MatchPath != nil {
		pathArg = *p.MatchPath
	}
	if p.MatchMethods != nil {
		methodsArg = *p.MatchMethods
	}
	if p.Action != nil {
		bytes, err := json.Marshal(*p.Action)
		if err != nil {
			return EdgeRule{}, fmt.Errorf("state: marshal edge_rule.action: %w", err)
		}
		actionArg = bytes
	}
	// CorsPresetID tri-state (ADR-129 D1): the wire layer
	// distinguishes three cases for an UPDATE — absent (don't
	// touch), JSON null (set column to NULL), JSON string (set
	// column to UUID). The State mirror uses **string so the
	// outer nil = absent, inner nil = JSON null, inner non-nil
	// = JSON string. The case when $11 pattern below matches
	// the action column's contract one line above.
	if p.CorsPresetID != nil {
		corsPresetSet = true
		if *p.CorsPresetID != nil {
			corsPresetValue = **p.CorsPresetID
		}
	}
	// ValidateMode nil-skip mirrors Action: a nil pointer means
	// "do not touch the column". A non-nil empty string is a
	// valid explicit-clear — coalesce(nullif(...)) will leave the
	// existing value intact, which is what the customer expects
	// from an empty-string request body. The wire surface
	// (cmd/apid/handlers_edge_rules.go) coerces empty to 'block'
	// before it reaches this layer per ADR-128 §D2's deprecation
	// window contract.
	if p.ValidateMode != nil {
		validateModeArg = *p.ValidateMode
	}

	row := s.pool.QueryRow(ctx, `
		update edge_rules set
			match_host    = coalesce($2, match_host),
			match_path    = coalesce($3, match_path),
			match_methods = coalesce($4, match_methods),
			priority      = coalesce($5, priority),
			enabled       = coalesce($6, enabled),
			action        = case when $7 then $8::jsonb else action end,
			cors_preset_id = case when $10 then $11::uuid else cors_preset_id end,
			validate_mode = coalesce(nullif($9, ''), validate_mode)
		where id = $1
		returning `+edgeRuleSelectCols,
		id, hostArg, pathArg, methodsArg, p.Priority, p.Enabled,
		p.Action != nil, actionArg,
		// $9: nil-skip via coalesce (nil → keep existing).
		// nullif('', '') collapses an explicit empty string
		// to NULL too — same outcome. The wire layer coerces
		// '' to 'block' before this point so the round-trip
		// is observable in the response.
		validateModeArg,
		// $10 / $11: tri-state FK update (ADR-129 D1). When
		// $10 is false, the column is untouched (the
		// case-when default is cors_preset_id, the
		// current value). When $10 is true, the column
		// is set to $11, which is nil → SQL NULL for
		// the "customer cleared the preset" signal or
		// a UUID for the "set preset" signal.
		corsPresetSet, corsPresetValue,
	)
	r, err := scanEdgeRule(row)
	if err != nil {
		return EdgeRule{}, mapErr(err)
	}
	return r, nil
}

func (s *PgStore) DeleteEdgeRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from edge_rules where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) CountEdgeRulesForApp(ctx context.Context, appID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from edge_rules where app_id = $1`, appID,
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountEdgeRulesByKindForApp is the per-kind quota reader
// (ADR-091 D22). Same shape as CountEdgeRulesForApp but filtered
// by kind. The Postgres runtime index edge_rules_app_kind_idx
// (composite on (app_id, kind)) makes this O(log n) without
// needing a sequence scan. The check that's load-bearing for race
// freedom lives inside CreateEdgeRuleIfUnderQuoTa (FOR UPDATE on
// the apps row) — this public method is the apid handler's
// read-side probe to surface "X/Y used" in 200 responses.
func (s *PgStore) CountEdgeRulesByKindForApp(ctx context.Context, appID string, kind EdgeRuleKind) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from edge_rules where app_id = $1 and kind = $2`, appID, string(kind),
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// MatchEdgeRulesForHost is the gateway hot-path read (ADR-089 §8).
// Returns every enabled rule whose match_host matches `host` (or
// "*"), ordered by priority ASC. The gatewayd matcher iterates in
// priority order and short-circuits on first match — the ORDER BY
// is the load-bearing detail.
//
// The host-pattern matching uses LIKE on the match_host column
// served by the partial B-tree on text_pattern_ops
// (edge_rules_match_host_pattern_idx). "*" maps to "%";
// "*.example.com" maps to "%.example.com"; exact hosts match as
// themselves. The gateway's Compile() pass re-checks the full glob
// in Go for correctness — the LIKE is the index-friendly proxy.
func (s *PgStore) MatchEdgeRulesForHost(ctx context.Context, host string) ([]EdgeRule, error) {
	rows, err := s.pool.Query(ctx, `
		select `+edgeRuleSelectCols+` from edge_rules
		 where enabled = true
		   and (
		   	match_host = $1
		   	or match_host = '*'
		   	or $1 like replace(replace(match_host, '*', '%'), '?', '_')
		   )
		 order by priority asc, created_at asc
	`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdgeRules(rows)
}

// ClaimAlertFire: rides the alert_rules row itself rather than a
// separate dedupe table. idempotencyKey is the bucket
// (rule_id + ':' + floor(epoch/cooldown_seconds)); last_fired_at is
// the stamp. A NULL or stale stamp = the claim wins; a fresh stamp
// inside the bucket = the claim loses.
//
// The Postgres impl is a stamped-only gate (PR 4's evaluator builds
// the idempotency_key per rule when it wants two-tier dedupe — the
// rule-id is the wider gate, the bucket is the inner). The bucket
// parameter is accepted here so the Store interface stays a single
// signature for both stores; MemStore enforces the bucket-level dedupe
// via alertClaimKeys (see memstore.go:1149) and PgStore enforces the
// rule-id-level gate via this CTE.
//
// CTE shape captures the OLD stamp explicitly so the "stamp already
// set" branch is distinguishable from "stamp was NULL". The PR #69
// regression used returning col = $2; that's always true post-update
// and silently breaks the entire dedupe gate. The two-return form
// (old + won) is the load-bearing detail.
func (s *PgStore) ClaimAlertFire(ctx context.Context, ruleID, idempotencyKey string, payload []byte, observed float64, at time.Time) (deliveryID string, won bool, err error) {
	// The bucket-level dedupe primitive is
	// `alert_deliveries.idempotency_key` UNIQUE: the FIRST claim
	// inside a cool-down bucket inserts the delivery row with
	// status='pending' (the dispatch layer later promotes it to
	// delivered/failed via UpdateAlertDeliveryStatus), every
	// subsequent claim within the bucket fails at INSERT time with
	// a UNIQUE violation we map to (false, nil). The meterd
	// evaluator loop also reads ClaimAlertFire's won bit so it can
	// short-circuit before doing the webhook dial — a defensive
	// parallel to the UNIQUE.
	//
	// `last_fired_at` is stamped alongside the INSERT under the
	// same tx so the rule-row state is observably consistent with
	// the deliveries table. The stamp is a coarse cross-bucket gate
	// (it suppresses duplicate work when the bucket advances), but
	// it is NOT the dedupe primitive — `old.Before(at)` is true for
	// every tick inside a bucket (now keeps advancing), so without
	// the UNIQUE-protected INSERT each tick inside one bucket would
	// re-fire. The PR #409 e2e fix that relied on the stamp alone
	// (without the INSERT) re-introduced that exact bug (CI run
	// 30436032609: `alert_deliveries_idempotency_uniq` 23505 every
	// 2 s).
	//
	// payload and observed are stamped at INSERT time so the
	// alert_deliveries row is observable with its full envelope from
	// the dashboard scrape. nil is allowed — the INSERT writes '{}'
	// so dashboard queries against payload are safe.
	//
	// Wrapped in a single tx so the dedupe-INSERT and the
	// last_fired_at stamp are atomic. The CTE captures the OLD stamp
	// BEFORE the UPDATE so the predicate "$3 = old" doesn't trivially
	// succeed post-write (the exact bug CI caught in PR #69 — see
	// pkg-state-usage-monthly-tz-compare.md).
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	var exists bool
	if err := tx.QueryRow(ctx,
		`select true from alert_rules where id = $1`, ruleID,
	).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, ErrNotFound
		}
		return "", false, fmt.Errorf("state: claim alert fire lookup %s: %w", ruleID, err)
	}

	// Insert pending delivery as the dedupe gate. UNIQUE on
	// idempotency_key makes this atomic — a duplicate within the
	// bucket is a SQLSTATE 23505, mapped to (false, nil). payload is
	// JSONB so callers can index into it later (issue #396 acceptance
	// criterion 7: dashboard shows observed value). Empty payload
	// becomes '{}' so dashboard queries against payload are safe
	// (NULL payload would break `payload ->> 'metric'`).
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	if err := tx.QueryRow(ctx, `
		insert into alert_deliveries
			(rule_id, account_id, app_id, idempotency_key, payload, status, observed_value, fired_at)
		select $1, account_id, app_id, $2, $3::jsonb, 'pending', $4, $5
		  from alert_rules where id = $1
		returning id`,
		ruleID, idempotencyKey, payload, observed, at.UTC(),
	).Scan(&deliveryID); err != nil {
		// 23505 = unique_violation; the per-bucket dedupe hit.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", false, nil
		}
		return "", false, fmt.Errorf("state: claim alert fire insert %s: %w", ruleID, err)
	}

	// Stamp last_fired_at alongside the INSERT. The stamp is a
	// coarse cross-bucket gate but it is NOT the dedupe primitive
	// (see the doc-comment above).
	if _, err := tx.Exec(ctx,
		`update alert_rules set last_fired_at = $2 where id = $1`,
		ruleID, at.UTC(),
	); err != nil {
		return "", false, fmt.Errorf("state: claim alert fire stamp write %s: %w", ruleID, err)
	}
	won = true

	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("state: claim alert fire commit: %w", err)
	}
	return deliveryID, won, nil
}

func (s *PgStore) SetAlertRuleState(ctx context.Context, ruleID string, to AlertState, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update alert_rules
		   set state      = $2,
		       updated_at = $3
		 where id = $1 and state <> $2`,
		ruleID, string(to), at.UTC())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		// Either the row is missing OR already in `to`. Distinguish
		// via a follow-up SELECT — if the row is gone we MUST
		// return ErrNotFound; if it's there with the desired
		// state we return (false, nil) so the caller treats it as
		// a no-op and skips duplicate work (audit emission etc).
		var current string
		err := s.pool.QueryRow(ctx, `select state from alert_rules where id = $1`, ruleID).Scan(&current)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, ErrNotFound
			}
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (s *PgStore) SetAlertRuleLastEvaluated(ctx context.Context, ruleID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update alert_rules set last_evaluated_at = $2 where id = $1`, ruleID, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- alert_deliveries ------------------------------------------------------

const alertDeliverySelectCols = `id, rule_id, account_id, app_id, idempotency_key, payload,
       status, attempt_count, last_status_code, last_error,
       observed_value, fired_at, delivered_at, is_test`

func scanAlertDelivery(row pgx.Row) (AlertDelivery, error) {
	d := AlertDelivery{}
	var status string
	var appID *string
	var lastError *string
	var deliveredAt *time.Time
	var payload []byte
	var attemptCount int
	var lastStatusCode *int
	if err := row.Scan(
		&d.ID, &d.RuleID, &d.AccountID, &appID, &d.IdempotencyKey, &payload,
		&status, &attemptCount, &lastStatusCode, &lastError, &d.ObservedValue,
		&d.FiredAt, &deliveredAt, &d.IsTest,
	); err != nil {
		return AlertDelivery{}, err
	}
	d.Status = AlertDeliveryStatus(status)
	d.AttemptCount = attemptCount
	// last_status_code is nullable; mirror the slice-scanner above.
	if lastStatusCode != nil {
		d.LastStatusCode = *lastStatusCode
	}
	if lastError != nil {
		d.LastError = *lastError
	}
	if deliveredAt != nil {
		d.DeliveredAt = *deliveredAt
	}
	if appID != nil {
		d.AppID = *appID
	}
	if len(payload) > 0 {
		d.Payload = payload
	}
	return d, nil
}

func scanAlertDeliveries(rows pgx.Rows) ([]AlertDelivery, error) {
	var out []AlertDelivery
	for rows.Next() {
		d := AlertDelivery{}
		var status string
		var appID *string
		var lastError *string
		var deliveredAt *time.Time
		var payload []byte
		var attemptCount int
		var lastStatusCode *int
		if err := rows.Scan(
			&d.ID, &d.RuleID, &d.AccountID, &appID, &d.IdempotencyKey, &payload,
			&status, &attemptCount, &lastStatusCode, &lastError, &d.ObservedValue,
			&d.FiredAt, &deliveredAt, &d.IsTest,
		); err != nil {
			return nil, err
		}
		d.Status = AlertDeliveryStatus(status)
		d.AttemptCount = attemptCount
		// last_status_code is nullable in alert_deliveries (a pending
		// row from ClaimAlertFire hasn't been dispatched yet — no
		// status code). Mirror lastError/deliveredAt/appID and treat
		// NULL as 0 on the read side; UpdateAlertDeliveryStatus fills
		// the column once dispatch lands.
		if lastStatusCode != nil {
			d.LastStatusCode = *lastStatusCode
		}
		if lastError != nil {
			d.LastError = *lastError
		}
		if deliveredAt != nil {
			d.DeliveredAt = *deliveredAt
		}
		if appID != nil {
			d.AppID = *appID
		}
		if len(payload) > 0 {
			d.Payload = payload
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *PgStore) RecordAlertDelivery(ctx context.Context, in AlertDelivery) (AlertDelivery, error) {
	var appIDArg any
	if in.AppID != "" {
		appIDArg = in.AppID
	}
	var deliveredAtArg any
	if !in.DeliveredAt.IsZero() {
		deliveredAtArg = in.DeliveredAt.UTC()
	}
	var lastErrorArg any
	if in.LastError != "" {
		lastErrorArg = in.LastError
	}
	// Default status to 'pending' when the caller leaves it empty
	// (coalesce treats '' as not-null, so we map empty → NULL →
	// 'pending' explicitly rather than relying on coalesce alone).
	var statusArg any
	if in.Status != "" {
		statusArg = string(in.Status)
	}
	row := s.pool.QueryRow(ctx, `
		insert into alert_deliveries (
			rule_id, account_id, app_id, idempotency_key, payload,
			status, attempt_count, last_status_code, last_error,
			observed_value, fired_at, delivered_at, is_test
		) values (
			$1, $2, $3, $4, $5,
			coalesce($6, 'pending'), $7, $8, $9,
			$10, coalesce($11, now()), $12, $13
		)
		returning `+alertDeliverySelectCols,
		in.RuleID, in.AccountID, appIDArg, in.IdempotencyKey, []byte(in.Payload),
		statusArg, in.AttemptCount, in.LastStatusCode, lastErrorArg,
		in.ObservedValue, in.FiredAt, deliveredAtArg, in.IsTest,
	)
	d, err := scanAlertDelivery(row)
	if err != nil {
		// The UNIQUE on idempotency_key is the cool-down dedupe
		// primitive; surface it as ErrConflict so callers can map
		// it to a no-op without parsing SQLSTATE themselves.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return AlertDelivery{}, ErrConflict
		}
		return AlertDelivery{}, err
	}
	return d, nil
}

func (s *PgStore) UpdateAlertDeliveryStatus(ctx context.Context, id string, status AlertDeliveryStatus, attempt int, statusCode int, lastErr string, deliveredAt *time.Time) error {
	var lastErrArg any
	if lastErr != "" {
		lastErrArg = lastErr
	}
	var deliveredAtArg any
	if deliveredAt != nil {
		deliveredAtArg = deliveredAt.UTC()
	}
	tag, err := s.pool.Exec(ctx, `
		update alert_deliveries set
			status           = $2,
			attempt_count    = $3,
			last_status_code = $4,
			last_error       = $5,
			delivered_at     = coalesce($6, delivered_at)
		where id = $1`,
		id, string(status), attempt, statusCode, lastErrArg, deliveredAtArg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAlertDeliveriesForRule(ctx context.Context, ruleID string, limit int, includeTest bool) ([]AlertDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// includeTest=false → the production hot path; covered by the
	// partial index alert_deliveries_rule_fired_production_idx
	// (migrations/00528) so this stays index-only even as test
	// rows accumulate. includeTest=true → unconditional read; the
	// full alert_deliveries_rule_fired_idx covers the predicate.
	var rows pgx.Rows
	var err error
	if includeTest {
		rows, err = s.pool.Query(ctx,
			`select `+alertDeliverySelectCols+` from alert_deliveries
			 where rule_id = $1 order by fired_at desc limit $2`, ruleID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select `+alertDeliverySelectCols+` from alert_deliveries
			 where rule_id = $1 and is_test = false order by fired_at desc limit $2`, ruleID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlertDeliveries(rows)
}

// CountFailedInvocationsSince counts terminal-failed invocations on
// (accountID, appID, source) since `since`. The invocations table
// carries created_at (NOT NULL); for "how many failures in the last
// window" that's the right grain — terminal rows have a created_at
// that anchors when they entered the queue, not when they failed.
// Mirrors the time-window predicate in
// CountInstanceInvocationsInMinute (pgstore.go:2435).
//
// appID == "" → no app filter (cross-app aggregate). The caller
// is responsible for expanding source == 'any' to the four concrete
// InvocationSource values before the call.
func (s *PgStore) CountFailedInvocationsSince(ctx context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error) {
	// appID "" means "all apps on this account" (Store contract). We
	// pass nil to the SQL and use IS NOT DISTINCT FROM so the NULL
	// app_id of an account-wide invocation is included in the count.
	// source "" means "any source" — the enum is a closed vocabulary
	// so an empty string is the wildcard sentinel (mirrors MemStore).
	var appArg any
	if appID != "" {
		appArg = appID
	}
	var sourceArg any
	if source != "" {
		sourceArg = string(source)
	}
	var n int
	row := s.pool.QueryRow(ctx, `
		select count(*) from invocations
		 where account_id = $1
		   and state = 'failed'
		   and created_at >= $2
		   and ($3::uuid is null or app_id is not distinct from $3::uuid)
		   and ($4::text is null or source = $4::text)`,
		accountID, since.UTC(), appArg, sourceArg)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// --- invocations (Move 1 event-shaped queue) ---------------------------------
//
// Schema: migrations/00030_invocations.sql. The drain's hot path is
// ListDueInvocations, which uses invocations_due_idx +
// `for update skip locked` so two schedds could co-exist safely
// without double-claiming. Schedd is currently the sole writer
// (CLAUDE.md); single-writer invariant means the lock is preventive
// rather than required today. State transitions inside a transaction
// so claim/complete/fail cannot race the cron rewrite in loop.go.

const invocationSelectCols = `id, app_id, account_id, source, state, method, path,
       payload, headers, due_at, scheduled_at, cron_id, ack_url,
       result, lease_expires_at, received_at, completed_at, attempts,
       last_error, created_at, instance_id, outcome,
       deadline_at, retry_policy, result_retention_until,
       last_replayed_at`

func (s *PgStore) EnqueueInvocation(ctx context.Context, inv Invocation) (Invocation, error) {
	payload, err := jsonOrEmpty(inv.Payload)
	if err != nil {
		return Invocation{}, fmt.Errorf("state: invocations payload: %w", err)
	}
	headers, err := jsonOrEmpty(inv.Headers)
	if err != nil {
		return Invocation{}, fmt.Errorf("state: invocations headers: %w", err)
	}
	var scheduledAt, leaseExpires any
	if inv.ScheduledAt != nil {
		scheduledAt = inv.ScheduledAt.UTC()
	}
	if inv.LeaseExpiresAt != nil {
		leaseExpires = inv.LeaseExpiresAt.UTC()
	}
	var cronID any
	if inv.CronID != nil && *inv.CronID != "" {
		cronID = *inv.CronID
	}
	var deadlineAt, retentionUntil any
	if inv.DeadlineAt != nil {
		deadlineAt = inv.DeadlineAt.UTC()
	}
	if inv.ResultRetentionUntil != nil {
		retentionUntil = inv.ResultRetentionUntil.UTC()
	}
	var retryPolicy any
	if len(inv.RetryPolicyJSON) > 0 {
		retryPolicy = inv.RetryPolicyJSON
	}
	row := s.pool.QueryRow(ctx, `
		insert into invocations
			(app_id, account_id, source, state, method, path,
			 payload, headers, due_at, scheduled_at, cron_id,
			 ack_url, lease_expires_at,
			 deadline_at, retry_policy, result_retention_until)
		values
			($1, $2, $3, coalesce(nullif($4,''),'pending'), $5, $6,
			 $7, $8, $9, $10, $11,
			 nullif($12,''), $13,
			 $14, $15, $16)
		returning `+invocationSelectCols,
		inv.AppID, inv.AccountID, string(inv.Source), string(inv.State),
		inv.Method, inv.Path, payload, headers, inv.DueAt.UTC(),
		scheduledAt, cronID, inv.AckURL, leaseExpires,
		deadlineAt, retryPolicy, retentionUntil)
	out, err := scanInvocation(row)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	// Allow the caller to bind result/last_error before insert (rare,
	// mainly used by tests).
	if len(inv.Result) > 0 {
		out.Result = inv.Result
	}
	if inv.LastError != "" {
		out.LastError = inv.LastError
	}
	if inv.CompletedAt != nil {
		out.CompletedAt = inv.CompletedAt
	}
	return out, nil
}

func (s *PgStore) InvocationByID(ctx context.Context, id string) (Invocation, error) {
	row := s.pool.QueryRow(ctx, `select `+invocationSelectCols+` from invocations where id = $1`, id)
	return scanInvocation(row)
}

// ListDueInvocations is the drain's hot path. Wraps the SELECT in a
// tx, sorts by due_at, and skips rows another writer already locked.
// The drain's batched loop decides when to call it again (the drain
// bounds by `batchSize` and exits when the slice is shorter than the
// bound).
func (s *PgStore) ListDueInvocations(ctx context.Context, now time.Time, limit int) ([]Invocation, error) {
	if limit <= 0 {
		limit = 64
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("state: invocations begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select `+invocationSelectCols+`
		  from invocations
		 where state = 'pending' and due_at <= $1
		 order by due_at
		 for update skip locked
		 limit $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("state: invocations list-due: %w", err)
	}
	out, err := scanInvocations(rows)
	if err != nil {
		return nil, err
	}
	// SKIP LOCKED implies we hold a row lock until commit. The drain
	// re-fetches the row by id (via ClaimInvocation) which is fine —
	// the lock release at commit allows the claim to resolve in a
	// fresh transaction. Commit immediately so we don't carry the
	// long-running tx the drain's per-app loop would otherwise sit on.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: invocations list-due commit: %w", err)
	}
	return out, nil
}

// ClaimInvocation transitions pending → dispatching, stamps the
// lease, and writes the live instance handle (when known — empty
// string from the drain's first pass is overwritten by
// Store.StampInstanceInvocation once engine.Wake returns). Optimistic
// update: a row already in dispatching with an unexpired lease
// rejects with ErrNotFound so the drain retries on the next tick
// (matches MemStore and matches the SKIP LOCKED precedent).
func (s *PgStore) ClaimInvocation(ctx context.Context, id, instanceID string, leaseSeconds int) (Invocation, error) {
	// pgx v5.10's text-format encoder can't carry an int through a
	// `||` text-concat in `text || text → interval`. Local Postgres
	// accepts the implicit form, but the Postgres 15 image on GH
	// Actions rejects the lookup with "unable to encode 30 into text
	// format for text (OID 25)". Format the lease in Go with the
	// unit suffix so pgx encodes a string (no encode-plan lookup)
	// and Postgres parses it as interval.
	leaseText := strconv.Itoa(leaseSeconds) + " seconds"
	row := s.pool.QueryRow(ctx, `
		update invocations
		   set state = 'dispatching',
		       lease_expires_at = now() + $3::interval,
		       instance_id = coalesce(nullif($2, ''), instance_id),
		       received_at = now(),
		       attempts = attempts + 1
		 where id = $1 and state = 'pending'
		 returning `+invocationSelectCols, id, instanceID, leaseText)
	inv, err := scanInvocation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invocation{}, ErrNotFound
		}
		return Invocation{}, mapErr(err)
	}
	return inv, nil
}

// RequeueExpiredInvocations returns abandoned dispatches to pending. The
// row-locking CTE makes the bounded sweep safe if more than one schedd-like
// worker is ticking at once; only rows still carrying an expired lease can be
// reclaimed.
func (s *PgStore) RequeueExpiredInvocations(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 64
	}
	// PR-B fixup (code-review #1185 finding #4): the dispatching→pending
	// transition and the per-account counter decrement share one
	// transaction. Without the tx, a crash between the two leaked
	// the slot until the next cap hit. Two Execs inside one tx:
	// the requeue returns the affected-row count we report to the
	// caller, then the decrement updates every distinct account the
	// requeue produced.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: invocations reclaim expired begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		with expired as (
			select id
			  from invocations
			 where state = 'dispatching'
			   and lease_expires_at is not null
			   and lease_expires_at <= $1
			 order by lease_expires_at, id
			 for update skip locked
			 limit $2
		)
		update invocations as i
		   set state = 'pending',
		       due_at = $1,
		       lease_expires_at = null,
		       instance_id = null,
		       last_error = 'dispatch lease expired; requeued'
		  from expired
		 where i.id = expired.id
		returning account_id`, now.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("state: invocations reclaim expired: %w", err)
	}
	requeued := int(tag.RowsAffected())
	if requeued > 0 {
		if _, err := tx.Exec(ctx, `
			update account_async_quota
			   set current_inflight = greatest(current_inflight - 1, 0),
			       updated_at = now()
			 where account_id in (
			     select distinct account_id
			       from invocations
			      where last_error = 'dispatch lease expired; requeued'
			        and due_at = $1
			 )`, now.UTC()); err != nil {
			return 0, fmt.Errorf("state: invocations reclaim decrement: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: invocations reclaim expired commit: %w", err)
	}
	return requeued, nil
}

func (s *PgStore) CompleteInvocation(ctx context.Context, id string, result json.RawMessage) error {
	// outcome (issue #791) is stamped alongside state so the cron
	// run-history read never has to infer success from state.
	//
	// PR-B fixup (code-review #1185 finding #5): the state UPDATE
	// and the per-account counter decrement share one transaction
	// so a crash between the two can't leak a slot.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: invocations complete begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	if err := tx.QueryRow(ctx, `
		update invocations
		   set state = 'completed',
		       outcome = 'success',
		       completed_at = now(),
		       received_at = coalesce(received_at, now()),
		       result = coalesce($2, result)
		 where id = $1 and state = 'dispatching'
		 returning account_id`, id, nullableJSON(result)).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := decrementAccountAsyncInflightTx(ctx, tx, accountID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: invocations complete commit: %w", err)
	}
	return nil
}

// decrementAccountAsyncInflightTx is the tx-bound variant of
// DecrementAccountAsyncInflight. Used by CompleteInvocation /
// FailInvocation / CancelInvocation / RequeueExpiredInvocations so the
// decrement and the parent state transition commit or roll back as one
// unit. Tolerant of a missing cap row (the increment never happened).
func decrementAccountAsyncInflightTx(ctx context.Context, tx pgx.Tx, accountID string) error {
	if _, err := tx.Exec(ctx, `
		update account_async_quota
		   set current_inflight = greatest(current_inflight - 1, 0),
		       updated_at = now()
		 where account_id = $1`, accountID); err != nil {
		return fmt.Errorf("state: account_async_quota decrement tx: %w", err)
	}
	return nil
}

func (s *PgStore) FailInvocation(ctx context.Context, id string, lastError string, retryAfter time.Duration, budget int, opts ...FailOption) error {
	// Issue #394 — dead-letter path. When retryAfter > 0 and budget > 0
	// and attempts is already at the ceiling (ClaimInvocation already
	// bumped attempts to the current attempt count — see pgstore.go
	// ClaimInvocation line 2327), the row is routed to
	// state='dead_letter' (terminal) and attempts is NOT bumped past
	// the ceiling. Three call shapes:
	//
	//   retryAfter > 0, budget <= 0          → legacy infinite retry
	//                                          (state='pending', bump
	//                                          attempts, set due_at).
	//   retryAfter > 0, budget > 0           → transient decision via
	//                                          the CASE in the SET
	//                                          clause: dead_letter if
	//                                          attempts >= budget,
	//                                          else pending. attempts
	//                                          and completed_at follow
	//                                          the same case.
	//   retryAfter == 0 (any budget)         → permanent 'failed'. The
	//                                          drain uses this branch
	//                                          for shape/capacity
	//                                          errors; the budget gate
	//                                          does not apply.
	//
	// PR-B: per-account counter decrement only on the terminal
	// branches (state IN ('dead_letter','failed')). The transient
	// requeue branch returns the row to (pending), which keeps the
	// counter incremented — the row is still in flight from the
	// quota's POV.
	var query string
	var args []any
	var terminalSelect bool
	// issue #791 — outcome. The permanent branch stamps the caller's
	// classification (default 'failed', 'timeout' from the drain's
	// deadline paths); the transient branch clears it because the row
	// returns to a non-terminal state; the dead-letter arm of the
	// budget CASE stamps 'dead_letter' regardless of what the caller
	// asked for, mirroring how that branch already overrides state.
	failOpts := ApplyFailOptions(opts)
	switch {
	case retryAfter > 0 && budget > 0:
		// Same int→text concat workaround as ClaimInvocation: pass
		// the microseconds as a pre-formatted string with the unit
		// suffix so Postgres parses it as interval. Avoids the OID 25
		// encode-plan lookup failure on the GH Actions Postgres 15
		// image.
		retryText := strconv.FormatInt(retryAfter.Microseconds(), 10) + " microseconds"
		query = `update invocations
				    set state = case when attempts >= $4 then 'dead_letter' else 'pending' end,
				        outcome = case when attempts >= $4 then 'dead_letter' else null end,
				        due_at = case when attempts >= $4 then due_at else now() + $2::interval end,
				        completed_at = case when attempts >= $4 then now() else completed_at end,
				        lease_expires_at = null,
				        last_error = $3
				    -- Do NOT bump attempts on transient re-queue;
				    -- ClaimInvocation (line 2327) already incremented
				    -- it for this dispatch attempt. Double-bumping would
				    -- make MaxQueueAttempts=10 dead-letter after 5
				    -- iterations instead of 10.
				  where id = $1 and state in ('dispatching','pending')
				  returning account_id, state`
		args = []any{id, retryText, lastError, budget}
		terminalSelect = true
	case retryAfter > 0:
		retryText := strconv.FormatInt(retryAfter.Microseconds(), 10) + " microseconds"
		query = `update invocations
				    set state = 'pending',
				        outcome = null,
				        due_at = now() + $2::interval,
				        lease_expires_at = null,
				        last_error = $3,
				        attempts = attempts + 1
				  where id = $1 and state in ('dispatching','pending')
				  returning account_id, state`
		args = []any{id, retryText, lastError}
		terminalSelect = true
	default:
		query = `update invocations
				    set state = 'failed',
				        outcome = $3,
				        completed_at = now(),
				        last_error = $2
				  where id = $1 and state in ('dispatching','pending')
				  returning account_id, state`
		args = []any{id, lastError, string(failOpts.Outcome)}
		terminalSelect = true
	}
	if !terminalSelect {
		// Defensive: if a future PR adds a non-terminal branch,
		// the compiler must force the author to wire the
		// counter here. Should be unreachable today.
		return fmt.Errorf("state: FailInvocation: missing terminal-select wiring")
	}
	// PR-B fixup (code-review #1185 finding #1 + #5): all three
	// branches now return (account_id, state) so Scan destinations
	// are uniform; the state UPDATE and the per-account counter
	// decrement commit in one tx so a crash between the two can't
	// leak a slot.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: invocations fail begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	var newState string
	if err := tx.QueryRow(ctx, query, args...).Scan(&accountID, &newState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// Decrement only on terminal transitions.
	if newState == "dead_letter" || newState == "failed" {
		if err := decrementAccountAsyncInflightTx(ctx, tx, accountID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: invocations fail commit: %w", err)
	}
	return nil
}

// CountPendingInvocations is the index-backed apid cap check.
// Mirrors the `invocations_app_pending_idx` partial index predicate
// (state in ('pending','dispatching')) so the planner uses it.
func (s *PgStore) CountPendingInvocations(ctx context.Context, appID string, source InvocationSource) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from invocations
		 where app_id = $1 and source = $2
		   and state in ('pending','dispatching')`, appID, string(source)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (s *PgStore) CancelInvocation(ctx context.Context, id string) error {
	// PR-B fixup (code-review #1185 finding #5): the state UPDATE
	// and the per-account counter decrement share one transaction.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: invocations cancel begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountID string
	err = tx.QueryRow(ctx, `
		update invocations
		   set state = 'cancelled',
		       completed_at = coalesce(completed_at, now())
		 where id = $1 and state in ('pending','dispatching')
		 returning account_id`, id).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish "already terminal" from "not found" so the
		// apid handler can choose the right response. Read
		// inside the tx for read-after-write consistency.
		var exists bool
		if e := tx.QueryRow(ctx, `select exists(select 1 from invocations where id = $1)`, id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := decrementAccountAsyncInflightTx(ctx, tx, accountID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: invocations cancel commit: %w", err)
	}
	return nil
}

func (s *PgStore) ListInvocationsForAccount(ctx context.Context, accountID string, limit int, before string) ([]Invocation, error) {
	if limit <= 0 {
		limit = 50
	}
	// Cursor is an Invocation.ID. The inner ORDER BY is created_at DESC,
	// id DESC (id is the tie-breaker) — the cursor predicate is
	// (created_at, id) < (cursor_row.created_at, cursor_row.id) which is
	// monotonic under the same index. The id btree on PK carries the
	// sort; created_at is the user's primary ordering.
	//
	// Move 2 simplification: a single id cursor
	// (`created_at < (cursor's created_at) or (created_at = cursor.created_at and id < cursor.id)`)
	// would be more exact but requires two extra round-trips per page
	// (one to fetch the cursor row, one to scan). Per-account page
	// counts are small (single customer scale) so the planner picks the
	// existing PK + sort anyway.
	//
	// The empty-cursor case must NOT reference the subquery at all —
	// PostgreSQL type-checks the entire statement, so `id = $2` with a
	// text parameter against a uuid column raises 42883 (uuid = text)
	// even when the `$2 = ''` short-circuit would skip it at runtime.
	// Branch on the cursor in Go instead.
	var rows pgx.Rows
	var err error
	if before == "" {
		rows, err = s.pool.Query(ctx, `select `+invocationSelectCols+`
			from invocations
			where account_id = $1
			order by created_at desc, id desc
			limit $2`, accountID, limit)
	} else {
		rows, err = s.pool.Query(ctx, `select `+invocationSelectCols+`
			from invocations
			where account_id = $1
			  and created_at < (
			      select created_at from invocations where id = $2 and account_id = $1)
			order by created_at desc, id desc
			limit $3`, accountID, before, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanInvocations(rows)
}

// ListInvocationsForApp is the per-app filtered variant used by
// deleteApp's GC sweep. The states filter is variadic; an empty slice
// returns all rows for the app (rare / debug use). The most common call
// (deleteApp) passes the partial-index predicate states.
//
// Index choice: `invocations_app_pending_idx` covers
// (app_id, source, state) WHERE state IN ('pending','dispatching'). When
// the variadic states argument is the partial-index predicate, the
// planner uses it. Other state combinations fall back to a sequential
// scan, which is fine for the rare delete path.
//
// Predicate shape: we filter on array_length before the ANY so the
// planner can short-circuit the empty-states case to "all rows" without
// evaluating the IN list. cardinality(NULL) returns NULL not 0, which
// is why we use array_length (returns NULL too, but the OR is guarded
// by the states-empty Go-side check instead — see the :states = $2
// pre-amble).
func (s *PgStore) ListInvocationsForApp(ctx context.Context, appID string, states ...InvocationState) ([]Invocation, error) {
	// Empty variadic: semantics = "all rows for this app". Return
	// early to avoid an empty-array ANY() that the planner may render
	// as never-true.
	if len(states) == 0 {
		rows, err := s.pool.Query(ctx, `select `+invocationSelectCols+`
			from invocations
			where app_id = $1
			order by created_at desc, id desc`, appID)
		if err != nil {
			return nil, err
		}
		return scanInvocations(rows)
	}
	stateStrs := make([]string, len(states))
	for i, s := range states {
		stateStrs[i] = string(s)
	}
	rows, err := s.pool.Query(ctx, `select `+invocationSelectCols+`
		from invocations
		where app_id = $1
		  and state = any($2::text[])
		order by created_at desc, id desc`, appID, stateStrs)
	if err != nil {
		return nil, err
	}
	return scanInvocations(rows)
}

// ListCronRunsForCron is the per-cron run-history read (issue #791)
// behind GET /v1/crons/{id}/runs. Index-backed by
// invocations_cron_idx (migrations/00166), whose
// `(cron_id, created_at DESC) WHERE cron_id IS NOT NULL` shape matches
// this predicate and ORDER BY exactly.
//
// The cursor is the same opaque `before`-is-an-id convention as
// ListInvocationsForAccount, including the correlated subselect that
// resolves the cursor row's created_at. It intentionally does NOT
// re-check ownership: the caller has already proven the cron belongs
// to it (apid's listCronRuns runs the CronByID → AppByID → AccountID
// check first), and the cron_id filter is total — a row can belong to
// exactly one cron.
func (s *PgStore) ListCronRunsForCron(ctx context.Context, cronID string, limit int, before string) ([]Invocation, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows pgx.Rows
	var err error
	if before == "" {
		rows, err = s.pool.Query(ctx, `select `+invocationSelectCols+`
			from invocations
			where cron_id = $1
			order by created_at desc, id desc
			limit $2`, cronID, limit)
	} else {
		rows, err = s.pool.Query(ctx, `select `+invocationSelectCols+`
			from invocations
			where cron_id = $1
			  and created_at < (
			      select created_at from invocations where id = $2 and cron_id = $1)
			order by created_at desc, id desc
			limit $3`, cronID, before, limit)
	}
	if err != nil {
		return nil, err
	}
	return scanInvocations(rows)
}

// QueueState (issue #394) is the read-side counter aggregator. One
// round-trip returns three numbers: depth (pending+dispatching),
// in_flight (dispatching with a live lease), oldest_pending_at (min
// created_at over the pending slice).
//
// Index-backed by invocations_app_pending_idx — the partial index is
// declared on `(app_id, source, state) WHERE state IN
// ('pending','dispatching')`, so the outer WHERE MUST include both
// the partial-index predicate and the leading-column filter for the
// planner to pick the index. Without the outer state constraint the
// planner falls back to a sequential scan over the app's entire
// history (including completed/failed/cancelled/dead_letter rows),
// which would be unbounded on a healthy queue.
//
// In-flight predicate: only rows with a non-NULL lease_expires_at in
// the future count as in-flight. ClaimInvocation always installs a
// non-NULL lease, so a NULL value is malformed/manual data — not an
// active lease — and shouldn't be counted toward in_flight. The
// FILTER (state = 'dispatching') precondition excludes any state
// that hasn't gone through ClaimInvocation.
func (s *PgStore) QueueState(ctx context.Context, appID string) (QueueStats, error) {
	var stats QueueStats
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `
		select
		  count(*)                                                              as depth,
		  count(*) filter (where state = 'dispatching'
		                    and lease_expires_at is not null
		                    and lease_expires_at > now())                        as in_flight,
		  min(created_at) filter (where state = 'pending')                      as oldest_pending_at
		from invocations
		where app_id = $1
		  and source = 'queue'
		  and state in ('pending','dispatching')
	`, appID).Scan(&stats.Depth, &stats.InFlight, &oldest)
	if err != nil {
		return QueueStats{}, err
	}
	if oldest != nil {
		stats.OldestPendingAt = *oldest
	}
	return stats, nil
}

// QueuePeek (issue #394) lists the oldest pending queue messages for
// an app without acquiring a lease. Read-only — no FOR UPDATE, no
// FOR SHARE, no advisory lock. Cursor convention mirrors
// ListInvocationsForAccount: `before` is an invocation id (uuid);
// empty means "start from the oldest". The subquery resolves the
// anchor row's created_at + id so we page by the (created_at, id)
// tuple strictly *after* the anchor under the ORDER BY direction.
//
// Because the ORDER BY is ASC (oldest first), the cursor predicate
// is `(created_at, id) > (anchor.created_at, anchor.id)` — "rows
// strictly newer than the anchor in the same sort direction." Page 1
// returns the oldest N rows; page 2 (with `before=<last id of page
// 1>`) returns the next N rows newer than that anchor. The DESC
// counterpart (QueueDeadLetter) flips the predicate to `<` to match
// its DESC sort order. Both predicates use the (created_at, id) pair
// as the tie-breaker so rows with identical created_at do not skip
// or duplicate across pages.
//
// The query goes through invocations_app_pending_idx when the planner
// can satisfy both the app filter and the state predicate from the
// partial index; on hot apps the index-only path also covers the
// payload column for small payloads, keeping the read off the heap.
func (s *PgStore) QueuePeek(ctx context.Context, appID string, limit int, before string) ([]Invocation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var beforeParam any
	if before != "" {
		beforeParam = before
	}
	rows, err := s.pool.Query(ctx, `
		with anchor as (
		    select created_at, id from invocations where id = $2
		)
		select `+invocationSelectCols+`
		  from invocations
		 where app_id = $1
		   and source = 'queue'
		   and state = 'pending'
		   and ($2::uuid is null or
		        (created_at, id) > (select created_at, id from anchor))
		 order by created_at asc, id asc
		 limit $3
	`, appID, beforeParam, limit)
	if err != nil {
		return nil, err
	}
	return scanInvocations(rows)
}

// QueueDeadLetter (issue #394) lists dead-letter rows (state =
// 'dead_letter') for an app. Same cursor / limit / ordering
// conventions as QueuePeek but the index is
// invocations_app_dead_letter_idx and the order is DESC so the
// newest dead_letter surfaces first. Because DESC + `<` walks the
// anchor's "older" rows forward, the predicate uses the (created_at,
// id) tuple strictly *before* the anchor under the DESC sort order.
// The id tie-breaker is required because the partial index orders on
// (app_id, created_at DESC) only — two rows with identical
// created_at would otherwise swap pages under non-deterministic
// ordering.
func (s *PgStore) QueueDeadLetter(ctx context.Context, appID string, limit int, before string) ([]Invocation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var beforeParam any
	if before != "" {
		beforeParam = before
	}
	rows, err := s.pool.Query(ctx, `
		with anchor as (
		    select created_at, id from invocations where id = $2
		)
		select `+invocationSelectCols+`
		  from invocations
		 where app_id = $1
		   and source = 'queue'
		   and state = 'dead_letter'
		   and ($2::uuid is null or
		        (created_at, id) < (select created_at, id from anchor))
		 order by created_at desc, id desc
		 limit $3
	`, appID, beforeParam, limit)
	if err != nil {
		return nil, err
	}
	return scanInvocations(rows)
}

// CountInstanceInvocationsInMinute is the meter sampler hook.
// `state='dispatching'` matches the MemStore predicate exactly —
// only rows the drain actually drove across the wake gate count
// toward usage_minutes.requests.
func (s *PgStore) CountInstanceInvocationsInMinute(ctx context.Context, instanceID string, minute time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from invocations
		 where instance_id = $1
		   and state = 'dispatching'
		   and due_at >= $2 and due_at < $3`,
		instanceID, minute.UTC(), minute.Add(time.Minute).UTC()).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// StampInstanceInvocation writes the live instance handle onto a
// dispatching row. State guard mirrors the drain's lifecycle: a
// Complete / Fail call could land in any of pending/dispatching
// (Fail allows the retry path) but the instance_id stamp must
// only land while the row is dispatching — after that the row is
// either terminal or back in pending with no instance binding.
func (s *PgStore) StampInstanceInvocation(ctx context.Context, id, instanceID string) error {
	tag, err := s.pool.Exec(ctx, `
		update invocations
		   set instance_id = $2
		 where id = $1 and state = 'dispatching'`, id, instanceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// jsonOrEmpty normalises a json.RawMessage to a Postgres jsonb value
// — empty/null bodies are kept as the literal '{}' so the column
// NOT NULL + default '{}' shape holds.
func jsonOrEmpty(b json.RawMessage) ([]byte, error) {
	if len(b) == 0 {
		return []byte("{}"), nil
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("invalid json: %s", string(b))
	}
	return b, nil
}

// nullableJSON returns nil for empty bytes so COALESCE in
// CompleteInvocation leaves the previously-stored result intact.
func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	if !json.Valid(b) {
		return nil
	}
	return []byte(b)
}

func scanInvocation(row pgx.Row) (Invocation, error) {
	inv, err := scanInvocationCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invocation{}, ErrNotFound
		}
		return Invocation{}, err
	}
	return inv, nil
}

func scanInvocations(rows pgx.Rows) ([]Invocation, error) {
	var out []Invocation
	for rows.Next() {
		inv, err := scanInvocationCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// scanInvocationCols scans one row. Nullable columns are taken via
// *time.Time / *string so a NULL turns into the zero value rather
// than blowing the Scan. The select clause above is the contract —
// if anyone touches the column list there, this function must move
// with it.
func scanInvocationCols(scan func(...any) error) (Invocation, error) {
	inv := Invocation{}
	var source, state string
	var scheduledAt, leaseExpires, receivedAt, completedAt *time.Time
	var cronID, ackURL, lastErr, instanceID *string
	var payload, headers, result []byte
	var outcome *string
	var deadlineAt, retentionUntil, lastReplayedAt *time.Time
	var retryPolicy []byte
	if err := scan(
		&inv.ID, &inv.AppID, &inv.AccountID, &source, &state, &inv.Method, &inv.Path,
		&payload, &headers, &inv.DueAt, &scheduledAt, &cronID, &ackURL,
		&result, &leaseExpires, &receivedAt, &completedAt, &inv.Attempts,
		&lastErr, &inv.CreatedAt, &instanceID, &outcome,
		&deadlineAt, &retryPolicy, &retentionUntil,
		&lastReplayedAt,
	); err != nil {
		return Invocation{}, err
	}
	inv.Source = InvocationSource(source)
	inv.State = InvocationState(state)
	if len(payload) > 0 {
		inv.Payload = payload
	}
	if len(headers) > 0 {
		inv.Headers = headers
	}
	if len(result) > 0 {
		inv.Result = result
	}
	if scheduledAt != nil {
		inv.ScheduledAt = scheduledAt
	}
	if leaseExpires != nil {
		inv.LeaseExpiresAt = leaseExpires
	}
	if receivedAt != nil {
		inv.ReceivedAt = receivedAt
	}
	if completedAt != nil {
		inv.CompletedAt = completedAt
	}
	if cronID != nil {
		id := *cronID
		inv.CronID = &id
	}
	if ackURL != nil {
		inv.AckURL = *ackURL
	}
	if lastErr != nil {
		inv.LastError = *lastErr
	}
	if instanceID != nil {
		inv.InstanceID = *instanceID
	}
	if outcome != nil {
		o := InvocationOutcome(*outcome)
		inv.Outcome = &o
	}
	if deadlineAt != nil {
		inv.DeadlineAt = deadlineAt
	}
	if len(retryPolicy) > 0 {
		inv.RetryPolicyJSON = retryPolicy
	}
	if retentionUntil != nil {
		inv.ResultRetentionUntil = retentionUntil
	}
	if lastReplayedAt != nil {
		inv.LastReplayedAt = lastReplayedAt
	}
	return inv, nil
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
	// COALESCE on the SELECT guards against any pre-migration-00028
	// path that left the column NULL — though migration 00028 enforces
	// NOT NULL post-apply, the COALESCE keeps scanInstance round-tripping
	// a non-empty string even on half-migrated test DBs.
	// Keep every reference to $6 on the text path until after the
	// empty-string test. A direct $6::uuid in the ELSE branch makes
	// Postgres type the bind parameter as uuid before CASE evaluation,
	// so an empty wakeID fails before the generated-UUID branch runs.
	//
	// Multi-host safety cluster PR-5 (audit F4): the partial unique
	// index instances_wake_attempt_active_idx (migration 00384)
	// makes a duplicate INSERT with the same wake_id + state IN
	// ('waking', 'cold_booting') fail with SQLSTATE 23505. We translate
	// the raw pgconn.PgError into the typed sentinel
	// state.ErrConcurrentWake so the engine's retry loop (Layer 2
	// of the cluster-wide wakeCoord) can recover via
	// ReadActiveInstanceForWakeID. A 23505 with state OUTSIDE the
	// in-flight set would be a different bug (the index wouldn't
	// have fired), so the typed translation is safe.
	//
	// We call scanInstanceCols(row.Scan) directly rather than
	// scanInstance(row) because the latter wraps err through
	// mapErr() at scanInstance:14112, which strips pgconn.PgError
	// from the chain by re-wrapping with fmt.Errorf. The typed
	// errors.As(err, &pgErr) below would then return false on the
	// very 23505 we want to translate. Bypassing mapErr preserves
	// the chain and lets the typed sentinel surface.
	row := s.pool.QueryRow(ctx,
		`insert into instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at)
		 values ($1, nullif($2::text, '')::uuid, $3, $4, $5, case when $6::text = '' then gen_random_uuid() else ($6::text)::uuid end, now())
		 returning id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode`,
		appID, deploymentID, state, ramMB, nodeID, wakeID)
	return scanCreatedInstance(row, wakeID, appID)
}

// scanCreatedInstance is shared by both instance insert shapes. The partial
// wake_id index is the same cluster-coordination boundary for normal and
// mode-aware rows, so both methods must translate SQLSTATE 23505 into the
// same typed error instead of leaking pgx-specific details to schedd.
func scanCreatedInstance(row pgx.Row, wakeID, appID string) (Instance, error) {
	inst, err := scanInstanceCols(row.Scan)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Instance{}, fmt.Errorf(
				"state: %w: wake_id=%s app_id=%s already in-flight — recover via ReadActiveInstanceForWakeID",
				ErrConcurrentWake, wakeID, appID,
			)
		}
		return Instance{}, fmt.Errorf("state: create instance %q (app=%s): %w", wakeID, appID, err)
	}
	return inst, nil
}

// CreateInstanceWithMode (issue #72 / ADR-125 PR-A3) is the
// mode-aware overload the schedd's mirror admission path uses to
// stamp mode='mirror' on canary-shadow instances. The SQL is
// 1:1 with CreateInstance's INSERT — only the explicit `mode`
// column on the values list differs. The CHECK constraint on
// the mode column (migrations/00385) rejects any value other
// than 'normal' or 'mirror' with SQLSTATE 23514 at write time,
// so a typo at the call site fails fast rather than leaking
// as a wire-shape typo on the wake proto. mode must be a
// non-empty string from state.InstanceMode{normal,mirror};
// the engine validates before calling (Engine.AdmitMirrorInstance).
func (s *PgStore) CreateInstanceWithMode(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID, mode string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`insert into instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at, mode)
		 values ($1, nullif($2::text, '')::uuid, $3, $4, $5, case when $6::text = '' then gen_random_uuid() else ($6::text)::uuid end, now(), $7)
		 returning id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode`,
		appID, deploymentID, state, ramMB, nodeID, wakeID, mode)
	return scanCreatedInstance(row, wakeID, appID)
}

// CreateJobInstance writes the job-task instance shape. Job definitions use
// their image_ref directly, so unlike an app wake there is no deployment row
// to reference; migration 00589 makes deployment_id nullable for this kind.
// Keeping this separate from CreateInstanceWithMode prevents a job caller
// from accidentally relying on the app/deployment pair CHECK and defaulting
// the row to kind='wake'.
func (s *PgStore) CreateJobInstance(ctx context.Context, instanceID, jobID, runID string, taskIndex int, state string, ramMB int, nodeID, wakeID string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`insert into instances (id, app_id, deployment_id, job_id, kind, state, ram_mb, node_id, wake_id, started_at, mode)
		 values ($1::uuid, null, null, $2::uuid, 'job_task', $3, $4, $5::uuid,
		         case when $6::text = '' then gen_random_uuid() else ($6::text)::uuid end, now(), 'job')
		 returning id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode`,
		instanceID, jobID, state, ramMB, nodeID, wakeID)
	inst, err := scanInstanceCols(row.Scan)
	if err != nil {
		return Instance{}, fmt.Errorf("state: create job instance (instance=%s job=%s run=%s task=%d): %w", instanceID, jobID, runID, taskIndex, err)
	}
	inst.Kind = "job_task"
	inst.JobID = jobID
	inst.JobRunID = runID
	inst.JobTaskIndex = taskIndex
	return inst, nil
}

func (s *PgStore) InstanceByID(ctx context.Context, id string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
		 from instances where id = $1`, id)
	return scanInstance(row)
}

// MigrationInstanceByID is the migration-aware instance lookup used by
// vmmd's destination adoption and lease-expiry cleanup paths. The ordinary
// InstanceByID shape intentionally remains narrow for legacy callers; this
// method includes the migration lineage and lease token columns.
func (s *PgStore) MigrationInstanceByID(ctx context.Context, id string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at,
		        coalesce(node_id::text, ''), wake_id, framework_ready_at,
		        migrated_from_node_id::text, migrated_at, coalesce(lease_token, ''), tail_count
		 from instances where id = $1`, id)
	inst, err := scanInstanceColsWithMigration(row.Scan)
	if err != nil {
		return Instance{}, mapErr(err)
	}
	return inst, nil
}

// ReadActiveInstanceForWakeID returns the in-flight instance row
// for the given wake_id (state IN ('waking', 'cold_booting',
// 'running')) — the winner of the cluster-coord race that
// instances_wake_attempt_active_idx (migration 00384, audit F4 /
// ADR-098 amendment) protects. Returns ErrNotFound when the
// wake_id has no in-flight row (the race lost and the winner
// already parked — unusual but possible if the caller retried
// after a long sleep).
//
// Used by pkg/sched.Engine.EnsureWake's recovery path: on a 23505
// from CreateInstance, the engine calls ReadActiveInstanceForWakeID
// to discover the winner's instance_id and observes the winner's
// state transition (waking → cold_booting → running) with the same
// in-process state machine the winner is running.
//
// State values are lowercased to match the instances.state CHECK
// constraint from migration 00001 ('parked','waking','cold_booting',
// 'running','parking',...). An uppercase predicate would never
// match any row and the function would always return ErrNotFound,
// defeating the loser-recovery path.
func (s *PgStore) ReadActiveInstanceForWakeID(ctx context.Context, wakeID string) (Instance, error) {
	row := s.pool.QueryRow(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
		 from instances where wake_id = $1
		   and state in ('waking','cold_booting','running')
		 order by started_at desc limit 1`, wakeID)
	inst, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Instance{}, ErrNotFound
		}
		return Instance{}, fmt.Errorf("state: read active instance for wake_id %q: %w", wakeID, err)
	}
	return inst, nil
}

func (s *PgStore) ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
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
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
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
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
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
		`select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
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

// ListInstancesForAccountPaged is the cursor-paginated variant of
// ListInstancesForAccount (issue #393). Cursor is the instances.id;
// the SQL filter `id < $before` partitions rows by id and the
// handler emits `out[len-1].ID` as the next cursor, so the walk
// visits every row exactly once regardless of how `id` is generated
// (the column default is gen_random_uuid() — UUIDv4 — so the cursor
// order is not insertion-time-ordered, but each row's id is unique
// and `id::text < cursor` strictly partitions).
//
// Note: PR #338 added UUIDv7 for `wake_id`, not `id`. An earlier
// comment claimed `id` was UUIDv7; that was wrong. We deliberately
// ORDER BY id DESC (not started_at DESC) so the cursor walk matches
// the cursor predicate: with ORDER BY (started_at DESC, id DESC) the
// cursor (last id of the page) can be lexicographically larger than
// earlier rows' ids — same started_at, smaller id — and the walk
// stalls (test CursorPagination). `id DESC` is the only order whose
// cursor walk is well-defined for both UUIDv4 and UUIDv7.
//
// Cross-account safety is the JOIN on apps.account_id = $1 — there
// is no per-account guard at the handler layer because the SQL is
// the only path. The handler validates `limit` (1..100) before this
// call so the SQL stays narrow.
func (s *PgStore) ListInstancesForAccountPaged(ctx context.Context, accountID string, limit int, before string) ([]Instance, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx,
		`select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
		 from instances i
		 join apps a on a.id = i.app_id
		 where a.account_id = $1
		   and ($2 = '' or i.id::text < $2)
		 order by i.id::text desc
		 limit $3`, accountID, before, limit)
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
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
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

// UpdateInstanceStateIf atomically changes an instance state only when the
// caller's read is still current. Recovery's recreate path uses this
// conditional UPDATE to prevent a stale read from parking a row that a
// concurrent migration, watchdog, or deletion reconciler already claimed.
// A missing row and a predicate miss intentionally share ErrConflict: both
// are benign race losers to a reconciliation caller.
func (s *PgStore) UpdateInstanceStateIf(ctx context.Context, id, expectedState, nextState string) error {
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set state = $3,
		        parked_at = case when $3 = 'parked' then now() else parked_at end
		  where id = $1
		    and state = $2`, id, expectedState, nextState)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
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

// IncInstanceRequestCount (ADR-098 C8) bumps the per-instance
// request_count column by the supplied delta. The writer is
// additive ("request_count = request_count + delta") on purpose:
// schedd batches 250ms of per-instance request events into a
// single UPDATE, and a Phase-4-loser re-apply (the same delta
// computed twice) must be idempotent on the row. The batched
// flush path (C9) is the only caller; the writer is deliberately
// not used at single-request granularity because that would
// re-create the per-request UPDATE hot-path the gate's
// amortization is closing.
//
// The writer returns the post-increment total so the caller can
// spot instances that have crossed the per-app WarmSnapshotMinRequests
// threshold without a separate SELECT — the gate's
// "count >= min" comparison is a single conditional on the
// returned value. Returns int64(-1) when the row is gone (Phase-4
// loser landed after the instance was evicted); the caller
// treats this as a no-op (the gate falls through to the cold-boot
// path and the next instance will start fresh at request_count=0).
func (s *PgStore) IncInstanceRequestCount(ctx context.Context, id string, delta int64) (int64, error) {
	if delta == 0 {
		// The writer is additive; a zero delta is a spare round-trip
		// we can skip. But callers may still want the post-increment
		// value, so we read it.
		var cur int64
		err := s.pool.QueryRow(ctx, `select request_count from instances where id = $1`, id).Scan(&cur)
		if err != nil {
			return -1, err
		}
		return cur, nil
	}
	tag, err := s.pool.Exec(ctx,
		`update instances set request_count = request_count + $2 where id = $1`,
		id, delta)
	if err != nil {
		return -1, err
	}
	if tag.RowsAffected() == 0 {
		return -1, nil //nolint:nilerr // row gone; not a hard error
	}
	var cur int64
	if err := s.pool.QueryRow(ctx, `select request_count from instances where id = $1`, id).Scan(&cur); err != nil {
		return -1, err
	}
	return cur, nil
}

// SetInstanceFrameworkReadyAt stamps `framework_ready_at` on the
// instances row for the vmmd gRPC `FrameworkReady` handler
// (PR #470-FU-B). Mirrors the no-op-on-missing-row convention of
// UpdateInstanceState: zero rows affected -> ErrNotFound; callers
// can distinguish "instance already gone" from transient DB errors.
// Caller passes the wall-clock time the vmmd received the guest-init
// vsock DGRAM (port 1027, msg=4). The engine in PR #470-FU-A waits
// on this column before issuing the second PauseAndSnapshot that
// captures the warm tier.
func (s *PgStore) SetInstanceFrameworkReadyAt(ctx context.Context, id string, readyAt time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set framework_ready_at = $2 where id = $1`,
		id, readyAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetInstanceMode (issue #72 / ADR-125) flips instances.mode to the
// supplied value. Idempotent. Used by the schedd mirror admission
// path (Engine.AdmitInstance stamps the value at INSERT time, but a
// late retrofit on a RUNNING row reads as the same call). Returns
// ErrNotFound when the row is missing. The CHECK constraint on
// instances.mode (migrations/00349) enforces the value set.
func (s *PgStore) SetInstanceMode(ctx context.Context, id string, mode InstanceMode) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set mode = $2 where id = $1`,
		id, string(mode))
	if err != nil {
		return fmt.Errorf("state: set instance %s mode: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearInstanceFrameworkReadyAt resets `framework_ready_at` to NULL.
// Used by the engine at the start of each warm-capture cycle so a
// stale stamp from the previous cycle doesn't leak into the next wake
// decision. Same missing-row semantics as SetInstanceFrameworkReadyAt.
func (s *PgStore) ClearInstanceFrameworkReadyAt(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update instances set framework_ready_at = NULL where id = $1`,
		id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpInstanceTailCount atomically applies delta to the instance's
// `tail_count` column and returns the post-update value (issue #667
// / ADR-078). The arithmetic happens in SQL so concurrent receipts
// (a runner firing several terminal events in quick succession)
// cannot lose increments. The GREATEST(…, 0) floor mirrors
// DecrementInstanceTailCount's safety property — a stale receipt
// from a guest that just parked cannot underflow the counter, and
// snapshotAndPark's 5s watchdog force-parks regardless.
//
// Uses RETURNING tail_count so the caller (vmmd's
// MarkInstanceTailTerminal) learns the new value without a
// follow-up SELECT. The post-update value is what the runner's
// WaitGroup view of the world now matches — schedd's reaper
// decision in PR 4 reads the same value from
// Instance.TailCount.
//
// Returns ErrNotFound when the instance row is missing (the
// receipt raced a Park / Destroy and the row is gone). The
// vmmd receipt path logs a Debug and drops — same convention
// as the framework_ready / sidecar-event DGRAM receipts.
//
// SQL mirrors the sqlc source in queries.sql::BumpInstanceTailCount;
// `make sqlc-check` enforces lockstep between the raw SQL below
// and the generated sqlc method. The Store interface uses `string`
// for the instance id (matching the rest of the public surface)
// while the sqlc-generated method takes `pgtype.UUID` — the bridge
// is omitted here to keep pgstore's parameter shape consistent
// with the existing inline-SQL precedent for `UpdateInstanceState`
// and `AppendComputeNodeHeartbeat`.
func (s *PgStore) BumpInstanceTailCount(ctx context.Context, id string, delta int32) (int32, error) {
	var post int32
	row := s.pool.QueryRow(ctx,
		`update instances
		    set tail_count = GREATEST(tail_count + $2, 0)
		  where id = $1
		returning tail_count`,
		id, delta)
	if err := row.Scan(&post); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return post, nil
}

// DecrementInstanceTailCount is the canonical "tail task reached
// terminal" path (issue #667 / ADR-078). Equivalent to
// BumpInstanceTailCount(ctx, id, -n) — kept as a separate method
// because every decrement site is a terminal event receipt, and
// the explicit name makes the call sites self-documenting.
//
// GREATEST(tail_count - n, 0) is the safety floor: a stale
// decrement on a counter at 0 leaves it at 0 rather than
// underflowing. Without the floor, a burst of decrements after
// the runner exited cleanly would push the counter negative and
// the schedd reaper's `tail_count > 0` early-out would
// permanently stall the wake. The 5s watchdog in
// snapshotAndPark force-parks regardless; the floor is a
// defence-in-depth guard, not the load-bearing piece.
//
// Returns ErrNotFound when the instance row is missing.
//
// SQL mirrors the sqlc source in queries.sql::DecrementInstanceTailCount;
// `make sqlc-check` enforces lockstep. See the BumpInstanceTailCount
// comment for the rationale on the `string` vs `pgtype.UUID` bridge.
func (s *PgStore) DecrementInstanceTailCount(ctx context.Context, id string, n int32) error {
	tag, err := s.pool.Exec(ctx,
		`update instances
		    set tail_count = GREATEST(tail_count - $2, 0)
		  where id = $1`,
		id, n)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetInstanceTailCount returns the instance's current tail_count
// (issue #667 / ADR-078). Used by the snapshotAndPark watchdog to
// poll for drain completion. Single SELECT … FROM instances WHERE
// id = $1 — the column is on the hot path so the row is already
// in shared_buffers under normal load; pgx returns the column as
// int32 per the migration's ADD COLUMN type.
// Returns ErrNotFound when the instance row is missing.
//
// SQL mirrors the sqlc source in queries.sql::GetInstanceTailCount;
// `make sqlc-check` enforces lockstep. See the BumpInstanceTailCount
// comment for the rationale on the `string` vs `pgtype.UUID` bridge.
func (s *PgStore) GetInstanceTailCount(ctx context.Context, id string) (int32, error) {
	var n int32
	err := s.pool.QueryRow(ctx,
		`select tail_count from instances where id = $1`, id).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return n, nil
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
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
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

// ListRunningInstancesOnDeadNodes is the dead-node billing
// reconciler's lookup. Joins instances → compute_nodes and returns
// RUNNING rows whose owning node is not alive.
//
// Liveness is the OR of two predicates on purpose:
//
//   - `active = false` — schedd's heartbeat sweep already flipped the
//     node (pkg/sched/heartbeat.go). This is the steady-state signal.
//   - `last_heartbeat_at < threshold` — the flip has not landed yet.
//     MarkComputeNodeInactive is only called from the heartbeat
//     goroutine, so a schedd restart (or a schedd that never came
//     back) leaves a dead node with active = true indefinitely.
//     Without this second predicate the reconciler would inherit the
//     exact liveness blind spot it exists to close.
//
// ORDER BY last_heartbeat_at ASC so a capped tick drains the
// longest-dead nodes first — those are the rows accruing the most
// incorrect billing. The limit keeps one tick's write burst bounded
// on a fleet where a whole node's worth of instances goes dead at
// once; the caller re-runs on the next tick until the set is empty.
func (s *PgStore) ListRunningInstancesOnDeadNodes(ctx context.Context, threshold time.Time, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("state: list running instances on dead nodes: limit must be > 0, got %d", limit)
	}
	rows, err := s.pool.Query(ctx,
		`select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		        coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at,
		        i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode
		 from instances i
		 join compute_nodes n on n.id = i.node_id
		 where i.state = 'running'
		   and (n.active = false or n.last_heartbeat_at < $1)
		 -- Tie-break on instance id so a capped query is
		 -- deterministic when many rows share the same heartbeat
		 -- timestamp (MemStore's ListRunningInstancesOnDeadNodes
		 -- does the same). Without this, a multi-host fleet where
		 -- N>cap rows die at once can leave different rows waiting
		 -- an extra tick between runs of identical input.
		 order by n.last_heartbeat_at asc, i.id asc
		 limit $2`,
		threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list running instances on dead nodes: %w", err)
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
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, terminal_at, mode
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
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		           coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode
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

// TouchInstancesWithRequestDelta (ADR-098 C9) is the batched
// request_count writer. Same shape as TouchInstancesLastSeen
// (unnest, single round-trip) but additionally bumps
// request_count by the supplied delta on each row. The delta is
// additive ("request_count = request_count + delta") so a
// re-delivered batch is idempotent on Phase-4-loser re-applies.
//
// Mirrors IncInstanceRequestCount's writer contract — the engine
// prefers this batched path because ReportActivity already carries
// a per-instance touch batch; piggybacking avoids a separate
// per-instance UPDATE round-trip.
//
// Returns the number of rows updated. Rows whose instance_id is
// gone (a reaped instance) are silently dropped — the SQL filter
// `i.id = b.id` does not match deleted rows, so a re-delivered
// batch loses that delta. Same shape as TouchInstancesLastSeen.
func (s *PgStore) TouchInstancesWithRequestDelta(ctx context.Context, touches []InstanceTouch) (int, error) {
	if len(touches) == 0 {
		return 0, nil
	}
	ids := make([]string, len(touches))
	ts := make([]time.Time, len(touches))
	deltas := make([]int64, len(touches))
	for i, t := range touches {
		ids[i] = t.InstanceID
		ts[i] = t.LastRequest
		deltas[i] = t.RequestDelta
	}
	tag, err := s.pool.Exec(ctx,
		`update instances i
			set last_request_at = b.ts,
			    request_count   = coalesce(i.request_count, 0) + b.delta
		 from (select unnest($1::uuid[]) as id,
		              unnest($2::timestamptz[]) as ts,
		              unnest($3::bigint[]) as delta) b
		 where i.id = b.id`,
		ids, ts, deltas)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// --- snapshots --------------------------------------------------------------

// CreateSnapshot writes the immutable snapshot row imaged produces after the
// rootfs layer is built. Conflicts (same deployment_id, tier) collapse to
// ErrConflict so imaged can ignore a duplicate emission; the rest of imaged
// treats the first successful write as truth.
//
// Tier (issue #470 / ADR-055): empty tier defaults to "init" for legacy
// callers; new warm-tier capture code passes SnapshotTierWarm explicitly.
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
	tier := snap.Tier
	if tier == "" {
		tier = SnapshotTierInit
	}
	row := s.pool.QueryRow(ctx,
		`insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, stale, tier)
		 values ($1, $2, $3, $4, $5, $6, $7)
		 returning id, deployment_id::text, fc_version, mem_bytes, disk_bytes, storage_key, stale, created_at, tier`,
		snap.DeploymentID, snap.FCVersion, snap.MemBytes, snap.DiskBytes, snap.StorageKey, snap.Stale, tier)
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

// LatestSnapshot returns the freshest non-stale snapshot for a deployment
// across BOTH tiers. Warm wins on a created_at tie (issue #470 / ADR-055):
// the order-by clause ranks (tier='warm') before created_at so a fresh
// warm-tier promotion pre-empts a stale-or-equal init-tier row.
//
// schedd's wake path now calls LatestSnapshotForTier (per-tier decision)
// instead of this helper — LatestSnapshot is kept for legacy callers
// (dashboard queries, snapshot dashboards, manual SQL ops).
func (s *PgStore) LatestSnapshot(ctx context.Context, deploymentID string) (Snapshot, error) {
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id::text, fc_version, mem_bytes, disk_bytes, storage_key, stale, created_at, tier
		 from snapshots where deployment_id = $1 and stale = false
		 order by (tier = 'warm') desc, created_at desc limit 1`, deploymentID)
	return scanSnapshot(row)
}

// LatestSnapshotForTier returns the freshest non-stale snapshot for a
// deployment at a specific tier (issue #470 / ADR-055). Empty tier is
// treated as "init" for legacy callers; the returned Snapshot has its
// Tier field populated so schedd can detect the warm-tier hit.
//
// Returns ErrNotFound when no non-stale row exists for the
// (deployment, tier) pair — schedd's tier-fallback chain treats this as
// "fall through to the next tier".
func (s *PgStore) LatestSnapshotForTier(ctx context.Context, deploymentID, tier string) (Snapshot, error) {
	if tier == "" {
		tier = SnapshotTierInit
	}
	row := s.pool.QueryRow(ctx,
		`select id, deployment_id::text, fc_version, mem_bytes, disk_bytes, storage_key, stale, created_at, tier
		 from snapshots where deployment_id = $1 and tier = $2 and stale = false
		 order by created_at desc limit 1`, deploymentID, tier)
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
//
// B1.1 (issue #195): also selects a.slug so the GC loop can build the
// apps/<slug>/<dep>.ext4 storage key without re-issuing a
// DeploymentByID + AppByID round-trip per eviction.
//
// Issue #470 / ADR-055: also projects s.tier so the GC loop can keep
// (current warm + previous init) per app for warm-tier apps, while
// Free/Hobby apps keep just the single init-tier row. The tier column
// arrives as a 9th value via Scan's last argument.
//
// Issue #470 / PR C / ADR-072: also projects a.warm_snapshot_enabled as
// the 13th Scan value so the per-tier GC policy can decide whether to
// apply the 2+2 floor (warm-enabled apps) or the 2-init-only floor
// (warm-disabled apps) without a per-row AppByID round-trip. Same
// denormalisation pattern as AppSlug.
func (s *PgStore) ListSnapshotsForGC(ctx context.Context) ([]SnapshotForGC, error) {
	rows, err := s.pool.Query(ctx,
		`select s.id, s.deployment_id::text, d.app_id::text, a.account_id::text, a.slug,
		        s.fc_version, s.mem_bytes, s.disk_bytes, s.storage_key, s.stale, s.created_at, s.tier,
		        a.warm_snapshot_enabled
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
		if err := rows.Scan(&r.ID, &r.DeploymentID, &r.AppID, &r.AccountID, &r.AppSlug,
			&r.FCVersion, &r.MemBytes, &r.DiskBytes, &r.StorageKey, &r.Stale, &r.CreatedAt, &r.Tier,
			&r.AppWarmSnapshotEnabled); err != nil {
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

// MarkAllSnapshotsStaleByAppProtocol flips every non-stale snapshot
// whose deployment's app.app_protocol ∈ appProtocols stale
// (ADR-127 §D1, Layer 6, the imaged F3 sweep). Idempotent.
//
// This is the app-protocol dimension of the F2/F3 split: F2
// (MarkAllSnapshotsStaleByFCVersion above) handles Firecracker-
// version mismatch (ADR-005); F3 handles base-image mismatch for
// the wire-protocol-capable slice. app_protocol=http1 snapshots
// are never affected — they ride the unchanged H1+chunked bridge
// path (ADR-126 §Decision 6).
//
// A 0-row result on a stable box is the expected steady state;
// ops monitors snapshot_fleet_avg_mb to detect the cold-boot spike
// during an FAAS_BASE_IMAGE_VERSION bump.
func (s *PgStore) MarkAllSnapshotsStaleByAppProtocol(ctx context.Context, appProtocols []string) (int64, error) {
	if len(appProtocols) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`update snapshots s
		    set stale = true
		   from deployments d
		   join apps a on a.id = d.app_id
		  where s.deployment_id = d.id
		    and a.app_protocol = any($1::text[])
		    and s.stale = false`,
		appProtocols)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkSnapshotStaleByAppProtocol is the single-row mirror of
// MarkAllSnapshotsStaleByAppProtocol (mirrors MarkSnapshotStale
// above). Returns ErrNotFound when no snapshot matches the id
// AND the deployment's app.app_protocol ∈ appProtocols — so the
// caller can distinguish "row doesn't exist" from "row exists but
// is on http1 (wrong protocol for this sweep)".
func (s *PgStore) MarkSnapshotStaleByAppProtocol(ctx context.Context, snapshotID string, appProtocols []string) error {
	if len(appProtocols) == 0 {
		return errors.New("pgstore: MarkSnapshotStaleByAppProtocol: empty appProtocols set")
	}
	if snapshotID == "" {
		return errors.New("pgstore: MarkSnapshotStaleByAppProtocol: empty snapshotID")
	}
	tag, err := s.pool.Exec(ctx,
		`update snapshots s
		    set stale = true
		   from deployments d
		   join apps a on a.id = d.app_id
		  where s.id = $1::uuid
		    and s.deployment_id = d.id
		    and a.app_protocol = any($2::text[])`,
		snapshotID, appProtocols)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// scanComputeNode reads a single compute_nodes row, projecting the
// canonical 24-column layout (matches the SELECT / RETURNING lists
// in ActiveComputeNodes, ListAllComputeNodes, ComputeNodeByID,
// ComputeNodeByName, ListComputeNodes, CreateComputeNode,
// UpsertComputeNode, UpsertComputeNodeFromOperator,
// UpsertComputeNodeFromVmmd).
//
// Column order (must stay locked against the SQL projections):
//
//	id, name, target_url, vpcpus, mem_mb, max_concurrency,
//	admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
//	region, zone, schedd_target_url, gateway_target_url,
//	public_ip, public_ip_set_at,
//	release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
//	lifecycle
//
// `active` is STORED GENERATED from `lifecycle` (migration 00579,
// ADR-137), so every SELECT still projects both — legacy callers
// read `active`, Workstream B callers read `lifecycle`. The
// generated `active` predates the enum; the additional column is
// appended last so the wire-order contract is preserved.
//
// A mismatch between this Scan arg list and any of the SQL projections
// fails at runtime with pgx's column count error — the wire-level
// contract every helper above enforces.
//
// PR-3a (issue #911 / ADR-110) widened the projection from 14 to 22;
// migration 00468 adds gateway_target_url for a 23-column projection;
// migration 00579 adds lifecycle for a 24-column projection. The
// earlier 22-column additions were public_ip / public_ip_set_at
// (migration 00174 closure) + release_id / manifest_hash /
// host_certificate / cert_fingerprint /
// role / generation (migration 00266). Pre-PR-3a callers that hand-rolled
// SQL against the 14-column layout must be updated together; the only
// readers of the wider shape are the 9 helpers listed in the comment
// above (5 SELECTs + 4 INSERT/UPSERTs).
func scanComputeNode(row pgx.Row) (ComputeNode, error) {
	var n ComputeNode
	if err := row.Scan(&n.ID, &n.Name, &n.TargetURL, &n.VPCPUs, &n.MemMB,
		&n.MaxConcurrency, &n.AdmissionCeilingMB, &n.VCPUBudget, &n.Active,
		&n.LastHeartbeatAt, &n.CreatedAt, &n.Region, &n.Zone,
		&n.ScheddTargetURL, &n.GatewayTargetURL, &n.PublicIp, &n.PublicIpSetAt,
		&n.ReleaseID, &n.ManifestHash, &n.HostCertificate, &n.CertFingerprint,
		&n.Role, &n.Generation, &n.Lifecycle); err != nil {
		return ComputeNode{}, mapErr(err)
	}
	return n, nil
}

func (s *PgStore) ActiveComputeNodes(ctx context.Context) ([]ComputeNode, error) {
	rows, err := s.pool.Query(ctx, `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		       region, zone, schedd_target_url, gateway_target_url,
		       public_ip, public_ip_set_at,
		       release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		       lifecycle
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
		       admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		       region, zone, schedd_target_url, gateway_target_url,
		       public_ip, public_ip_set_at,
		       release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		       lifecycle
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
		       admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		       region, zone, schedd_target_url, gateway_target_url,
		       public_ip, public_ip_set_at,
		       release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		       lifecycle
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
		       admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		       region, zone, schedd_target_url, gateway_target_url,
		       public_ip, public_ip_set_at,
		       release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		       lifecycle
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

// ComputeNodeUsedMBByNode returns live resident usage for all requested
// nodes in one aggregate query. This is the placement fallback for a fleet
// whose vmmd capacity streams have not produced a fresh sample yet.
func (s *PgStore) ComputeNodeUsedMBByNode(ctx context.Context, nodeIDs []string) (map[string]int64, error) {
	used := make(map[string]int64, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return used, nil
	}
	parsedIDs := make([]uuid.UUID, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		parsed, err := uuid.Parse(nodeID)
		if err != nil {
			return nil, fmt.Errorf("state: compute node %q is not a UUID: %w", nodeID, err)
		}
		parsedIDs = append(parsedIDs, parsed)
	}
	rows, err := s.pool.Query(ctx, `
		select node_id::text, coalesce(sum(ram_mb + $2), 0)::bigint
		  from instances
		 where node_id = any($1::uuid[])
		   and state in ('waking','cold_booting','running')
		 group by node_id
	`, parsedIDs, api.PerVMOverheadMB)
	if err != nil {
		return nil, fmt.Errorf("state: compute nodes used_mb: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID string
		var value int64
		if err := rows.Scan(&nodeID, &value); err != nil {
			return nil, fmt.Errorf("state: scan compute nodes used_mb: %w", err)
		}
		used[nodeID] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate compute nodes used_mb: %w", err)
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

// AppendComputeNodeHeartbeat appends one row to the append-only
// heartbeat history (CP-1, migration 00065). The parent
// compute_node row is checked first so a known-missing node returns
// ErrNotFound (matching the MemStore branch); the INSERT itself
// raises a unique-violation SQLSTATE 23505 on (node_id, received_at)
// collision, which we surface as ErrConflict so the writer can log
// it as a warning rather than silently dedup. The FK on delete
// cascade is automatic — see migration 00065.
//
// Why raw SQL and not the sqlc-generated method: the generated
// method uses pgtype.UUID for the node_id parameter, which would
// force every caller to thread UUID values through pgtype — the
// rest of the compute-node surface (HeartbeatComputeNode,
// MarkComputeNodeInactive, etc.) takes string ids. The raw SQL
// path is consistent with that surface.
func (s *PgStore) AppendComputeNodeHeartbeat(ctx context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string) error {
	// Parent existence check first. A hot tick that races a
	// concurrent admin DELETE will reach this point and bail with
	// ErrNotFound rather than racing the FK insert.
	var parentExists bool
	if err := s.pool.QueryRow(ctx,
		`select exists(select 1 from compute_nodes where id = $1)`, nodeID,
	).Scan(&parentExists); err != nil {
		return fmt.Errorf("state: append compute_node_heartbeat: parent check: %w", err)
	}
	if !parentExists {
		return ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `
		insert into compute_node_heartbeats (node_id, received_at, last_heartbeat_at, source)
		values ($1, $2, $3, $4)
	`, nodeID, receivedAt, lastHeartbeatAt, source); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return fmt.Errorf("%w: compute_node_heartbeats (node_id, received_at) duplicate", ErrConflict)
		}
		return fmt.Errorf("state: append compute_node_heartbeat: %w", err)
	}
	return nil
}

// ListComputeNodeHeartbeats returns up to limit rows for the
// given node, newest first. since.IsZero() ⇒ no lower bound;
// the SQL `$2::timestamptz is null or received_at >= $2` predicate
// collapses both cases into a single query (the MemStore
// implementation mirrors the same shape in Go). The composite
// index compute_node_heartbeats_node_at_idx (node_id, received_at
// desc) matches this read. An empty result set is NOT an error —
// a fresh node has no history rows, the endpoint surfaces that
// as { "heartbeats": [] }.
func (s *PgStore) ListComputeNodeHeartbeats(ctx context.Context, nodeID string, since time.Time, limit int) ([]ComputeNodeHeartbeat, error) {
	if limit <= 0 {
		limit = 200
	}
	var sinceArg interface{}
	if !since.IsZero() {
		sinceArg = since.UTC()
	}
	rows, err := s.pool.Query(ctx, `
		select id, node_id, received_at, last_heartbeat_at, source
		from compute_node_heartbeats
		where node_id = $1
		  and ($2::timestamptz is null or received_at >= $2)
		order by received_at desc
		limit $3
	`, nodeID, sinceArg, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list compute_node_heartbeats: %w", err)
	}
	defer rows.Close()
	// Pre-size with a bounded capacity. limit is already capped
	// above (200 default; the apid handler caps at 2000). The
	// constant capacity avoids CodeQL flagging the slice
	// allocation as user-input-driven; append will grow the slice
	// past the capacity if limit is genuinely a larger value.
	out := make([]ComputeNodeHeartbeat, 0, heartbeatHistoryMaxRows)
	for rows.Next() {
		var h ComputeNodeHeartbeat
		var nodeUUID string
		if err := rows.Scan(&h.ID, &nodeUUID, &h.ReceivedAt, &h.LastHeartbeatAt, &h.Source); err != nil {
			return nil, fmt.Errorf("state: scan compute_node_heartbeat: %w", err)
		}
		h.NodeID = nodeUUID
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate compute_node_heartbeats: %w", err)
	}
	return out, nil
}

// AppendComputeNodeHeartbeatWithStats (PR #4 / ADR-091 §3.6
// amendment) extends AppendComputeNodeHeartbeat with the cpu_pct_60s
// and disk_used_bytes columns added by migration 00199. The two new
// columns are nullable (pre-PR #4 rows keep NULL — see migration
// 00199's comment) and default to NULL on INSERT so an un-upgraded
// vmmd writer hitting a post-PR #4 schema still works. The
// (node_id, received_at) unique constraint and parent-exists check
// mirror AppendComputeNodeHeartbeat verbatim — the test code path
// must see the same ErrConflict surface.
func (s *PgStore) AppendComputeNodeHeartbeatWithStats(ctx context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string, cpuPct60s float64, diskUsedBytes int64) error {
	// Parent existence check first (same rationale as
	// AppendComputeNodeHeartbeat: avoid racing the FK insert).
	var parentExists bool
	if err := s.pool.QueryRow(ctx,
		`select exists(select 1 from compute_nodes where id = $1)`, nodeID,
	).Scan(&parentExists); err != nil {
		return fmt.Errorf("state: append compute_node_heartbeat_with_stats: parent check: %w", err)
	}
	if !parentExists {
		return ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `
		insert into compute_node_heartbeats (node_id, received_at, last_heartbeat_at, source, cpu_pct_60s, disk_used_bytes)
		values ($1, $2, $3, $4, $5, $6)
	`, nodeID, receivedAt, lastHeartbeatAt, source, cpuPct60s, diskUsedBytes); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return fmt.Errorf("%w: compute_node_heartbeats (node_id, received_at) duplicate", ErrConflict)
		}
		return fmt.Errorf("state: append compute_node_heartbeat_with_stats: %w", err)
	}
	return nil
}

// LatestHeartbeatStats (PR #4) returns the most-recent heartbeat
// per compute node. DISTINCT ON (node_id) collapses the per-node
// rows to the latest received_at in one pass — Postgres-only
// idiom, the MemStore mirrors it in Go. The obsListNodes handler
// folds this onto the per-node projection; a node with no
// heartbeats yet is NOT returned here (the LEFT JOIN in the
// handler renders it as "no data" with the rest of the
// compute_nodes row intact).
func (s *PgStore) LatestHeartbeatStats(ctx context.Context) ([]ComputeNodeHeartbeatStats, error) {
	return s.latestHeartbeatStatsWhere(ctx, "")
}

// LatestBuilderHeartbeatStats (operator-side observability
// mega-PR / Commit 7 — P5) returns the most-recent heartbeat
// filtered to source='builder_tick'. cmd/builderd publishes
// these rows independently of the build queue so idle builders
// remain observable.
func (s *PgStore) LatestBuilderHeartbeatStats(ctx context.Context) ([]ComputeNodeHeartbeatStats, error) {
	return s.latestHeartbeatStatsWhere(ctx, "where source = 'builder_tick'")
}

// latestHeartbeatStatsWhere is the shared implementation for
// the LatestHeartbeatStats / LatestBuilderHeartbeatStats twins.
// The whereClause string is either empty (all sources) or a
// bare `where source = '<name>'` clause — interpolated as a
// literal here, NOT user input, so SQL injection is not a
// concern; the two callers live in this file.
func (s *PgStore) latestHeartbeatStatsWhere(ctx context.Context, whereClause string) ([]ComputeNodeHeartbeatStats, error) {
	rows, err := s.pool.Query(ctx, `
		select distinct on (node_id)
		       node_id, received_at, cpu_pct_60s, disk_used_bytes
		from compute_node_heartbeats
		`+whereClause+`
		order by node_id, received_at desc
	`)
	if err != nil {
		return nil, fmt.Errorf("state: list latest heartbeat stats: %w", err)
	}
	defer rows.Close()
	// Pre-size to a reasonable upper bound; the cluster won't have
	// more than a few hundred nodes in the multi-host story, so
	// 512 is generous headroom without bloating the slice.
	out := make([]ComputeNodeHeartbeatStats, 0, 64)
	for rows.Next() {
		var h ComputeNodeHeartbeatStats
		var nodeUUID string
		var cpu *float64
		var disk *int64
		if err := rows.Scan(&nodeUUID, &h.ReceivedAt, &cpu, &disk); err != nil {
			return nil, fmt.Errorf("state: scan latest heartbeat stats: %w", err)
		}
		h.NodeID = nodeUUID
		h.CPUPct60s = cpu
		h.DiskUsedBytes = disk
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate latest heartbeat stats: %w", err)
	}
	return out, nil
}

// PerNodeLiveStats (PR #4) is the read-side aggregate for the
// /v1/admin/obs/nodes handler. Groups live instances (state in
// {RUNNING, WAKING, COLD_BOOTING}, the §6.2 invariant #1 set) by
// instances.node_id and joins onto compute_nodes for the human-
// friendly node name.
//
// Revision 2 (PR #4 prep): the original draft joined on a separate
// instance_node_bindings table; after re-reading migration 00024
// during implementation we discovered instances.node_id is already
// a NOT NULL FK to compute_nodes(id), backfilled on pre-existing
// rows. ADR-092 §8 amends §2.1 to drop the binding-table design.
// This query mirrors the corrected design: the inner GROUP BY
// walks the instances table directly. The +8 on ram_mb mirrors
// §6.2 invariant #2 — Σ(ram_mb + 8) ≤ 47,600 MB — so per-node
// RAMUsedMB sums to the fleet ceiling. COUNT(*) FILTER (WHERE
// state = ...) projects each state bucket without four separate
// scans. The JOIN onto compute_nodes is INNER today because the
// obs handler only wants useful per-node aggregates; a node with
// no instances simply doesn't appear in this result and the
// handler surfaces "no live instances" via the dedicated empty
// state in the response.
func (s *PgStore) PerNodeLiveStats(ctx context.Context) ([]PerNodeStats, error) {
	rows, err := s.pool.Query(ctx, `
		select n.name                                           as node_name,
		       count(*)                                         as instances_live,
		       count(*) filter (where i.state = 'RUNNING')     as instances_running,
		       count(*) filter (where i.state = 'WAKING')      as instances_waking,
		       count(*) filter (where i.state = 'COLD_BOOTING') as instances_cold_booting,
		       coalesce(sum(i.ram_mb + 8), 0)                    as ram_used_mb
		from instances i
		join compute_nodes n on n.id = i.node_id
		where i.state in ('RUNNING', 'WAKING', 'COLD_BOOTING')
		group by n.name
		order by n.name
	`)
	if err != nil {
		return nil, fmt.Errorf("state: per-node live stats: %w", err)
	}
	defer rows.Close()
	// Pre-size to a reasonable upper bound; the cluster won't have
	// more than a few hundred nodes in the multi-host story.
	out := make([]PerNodeStats, 0, 64)
	for rows.Next() {
		var s PerNodeStats
		if err := rows.Scan(
			&s.NodeName,
			&s.InstancesLive,
			&s.InstancesRunning,
			&s.InstancesWaking,
			&s.InstancesColdBooting,
			&s.RAMUsedMB,
		); err != nil {
			return nil, fmt.Errorf("state: scan per-node live stats: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate per-node live stats: %w", err)
	}
	return out, nil
}

// OperatorCapacity returns the fleet-wide capacity projection for the
// operator console. The two CTEs aggregate live instances and placed apps
// before joining to compute_nodes, which preserves rows for empty nodes and
// avoids the O(instances) response allocation that a ListAllInstances-based
// implementation would require.
func (s *PgStore) OperatorCapacity(ctx context.Context) (OperatorCapacitySnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		with live as (
			select i.node_id,
			       count(*) as instances_live,
			       count(*) filter (where i.state = 'RUNNING') as instances_running,
			       count(*) filter (where i.state = 'WAKING') as instances_waking,
			       count(*) filter (where i.state = 'COLD_BOOTING') as instances_cold_booting,
			       coalesce(sum(i.ram_mb + 8), 0)::bigint as ram_used_mb
			  from instances i
			 where i.state in ('RUNNING', 'WAKING', 'COLD_BOOTING')
			 group by i.node_id
		), placed as (
			select a.node_id,
			       count(*)::bigint as apps_count,
			       count(distinct a.account_id)::bigint as tenants_count
			  from apps a
			 where a.status <> 'deleted' and a.node_id is not null
			 group by a.node_id
		)
		select n.id, n.name, n.active, n.vpcpus, n.vcpu_budget, n.mem_mb,
		       n.admission_ceiling_mb,
		       coalesce(l.instances_live, 0)::bigint,
		       coalesce(l.instances_running, 0)::bigint,
		       coalesce(l.instances_waking, 0)::bigint,
		       coalesce(l.instances_cold_booting, 0)::bigint,
		       coalesce(l.ram_used_mb, 0)::bigint,
		       coalesce(p.apps_count, 0)::bigint,
		       coalesce(p.tenants_count, 0)::bigint
		  from compute_nodes n
		  left join live l on l.node_id = n.id
		  left join placed p on p.node_id = n.id
		 order by n.active desc, n.name asc
	`)
	if err != nil {
		return OperatorCapacitySnapshot{}, fmt.Errorf("state: operator capacity nodes: %w", err)
	}
	defer rows.Close()

	out := OperatorCapacitySnapshot{Nodes: make([]OperatorCapacityNode, 0, 64)}
	for rows.Next() {
		var node OperatorCapacityNode
		if err := rows.Scan(
			&node.ID, &node.Name, &node.Active, &node.VPCPUs, &node.VCPUBudget,
			&node.MemMB, &node.AdmissionCeilingMB, &node.InstancesLive,
			&node.InstancesRunning, &node.InstancesWaking, &node.InstancesColdBooting,
			&node.RAMUsedMB, &node.AppsCount, &node.TenantsCount,
		); err != nil {
			return OperatorCapacitySnapshot{}, fmt.Errorf("state: scan operator capacity node: %w", err)
		}
		out.Nodes = append(out.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return OperatorCapacitySnapshot{}, fmt.Errorf("state: iterate operator capacity nodes: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `
		select count(*) filter (where status <> 'deleted')::bigint,
		       count(distinct account_id) filter (where status <> 'deleted')::bigint,
		       count(*) filter (where status <> 'deleted' and node_id is null)::bigint
		  from apps
	`).Scan(&out.AppsTotal, &out.TenantsTotal, &out.UnplacedApps); err != nil {
		return OperatorCapacitySnapshot{}, fmt.Errorf("state: operator capacity totals: %w", err)
	}
	return out, nil
}

// MarkComputeNodeInactive flips the row's lifecycle to `unavailable`
// (Workstream B, ADR-137). The legacy pre-00579 implementation wrote
// the (now STORED GENERATED) `active` column directly; PG rejects
// `UPDATE compute_nodes SET active = $X` with SQLSTATE 428C9 because
// generated columns are derived from `lifecycle`. Idempotent at the
// PG level: the UPDATE matches regardless of current lifecycle, so
// re-flipping an unavailable row is a no-op. We preserve the row
// rather than DELETE so an operator can re-enable it without
// re-provisioning the target_url / cert.
func (s *PgStore) MarkComputeNodeInactive(ctx context.Context, nodeID string) error {
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set lifecycle = 'unavailable'::compute_node_lifecycle where id = $1`, nodeID)
	if err != nil {
		return fmt.Errorf("state: mark compute_node %s unavailable: %w", nodeID, err)
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
	//
	// region/zone (issue #254 / PR #429) are nullable and default to
	// NULL on INSERT — schedd's placement chooser doesn't yet surface
	// them at register time, so we explicitly project NULL when the
	// caller leaves the pointer nil. The PR-3a release-bundle columns
	// (release_id, manifest_hash, host_certificate, cert_fingerprint,
	// role, generation) are also nullable on INSERT — operator-added
	// pre-PR-3a rows accept the schema without a backfill. RETURNING
	// projects all 23 columns to match scanComputeNode's scan width.
	lifecycle := node.Lifecycle
	if lifecycle == "" {
		if node.Active {
			lifecycle = NodeLifecycleActive
		} else {
			lifecycle = NodeLifecycleUnavailable
		}
	}
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, vcpu_budget, lifecycle,
		     region, zone, gateway_target_url,
		     public_ip, public_ip_set_at,
		     release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation)
		values ($1, $2, $3, $4, $5, $6, $7, $8,
		        $9, $10, $11,
		        $12, $13,
		        $14, $15, $16, $17, $18, $19)
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		          region, zone, schedd_target_url, gateway_target_url,
		          public_ip, public_ip_set_at,
		          release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		          lifecycle
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB, node.VCPUBudget, string(lifecycle),
		node.Region, node.Zone, node.GatewayTargetURL,
		node.PublicIp, node.PublicIpSetAt,
		node.ReleaseID, node.ManifestHash, node.HostCertificate, node.CertFingerprint,
		node.Role, node.Generation)
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
//
// vcpu_budget (Tier A2, migration 00123) is operator-tunable per
// node. The upsert re-applies the caller's value on conflict so
// a vmmd self-registering with its config.toml value wins against
// a stale row; the operator can re-tune later via
// PUT /v1/compute-nodes/{id}. Migration 00123 backfilled existing
// rows to api.VCPUSlots (160); pre-migration rows see the same
// default via the column DEFAULT clause.
func (s *PgStore) UpsertComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error) {
	// region/zone are projected to match scanComputeNode's 23-column
	// scan. On conflict the existing region/zone values are preserved
	// (operator-driven locality label, not a vmmd-side knob); see
	// migrations/00069_compute_nodes_region_zone.sql for the
	// default-local backfill. The PR-3a release-bundle columns
	// (release_id, manifest_hash, host_certificate, cert_fingerprint,
	// role, generation) mirror the existing owner-driven split:
	// release_id / manifest_hash / role are operator-tunable (bundle
	// install + renderer stamp them) and re-apply on conflict;
	// host_certificate / cert_fingerprint are secrets-init-driven and
	// use COALESCE to preserve any value PR-X wrote first; generation
	// is doctor-driven and uses COALESCE so the doctor's bump is
	// monotonic (a later UPSERT with nil generation must not lower the
	// counter). RETURNING projects all 23 columns.
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, vcpu_budget, lifecycle,
		     region, zone, gateway_target_url,
		     public_ip, public_ip_set_at,
		     release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation)
		values ($1, $2, $3, $4, $5, $6, $7, 'active'::compute_node_lifecycle,
		        $8, $9, $10,
		        $11, $12,
		        $13, $14, $15, $16, $17, $18)
		on conflict (name) do update
		  set target_url          = excluded.target_url,
		      vpcpus              = excluded.vpcpus,
		      mem_mb              = excluded.mem_mb,
		      max_concurrency     = excluded.max_concurrency,
		      admission_ceiling_mb = excluded.admission_ceiling_mb,
		      vcpu_budget         = excluded.vcpu_budget,
		      lifecycle           = 'active'::compute_node_lifecycle,
		      region              = excluded.region,
		      zone                = excluded.zone,
		      schedd_target_url   = excluded.schedd_target_url,
		      gateway_target_url  = excluded.gateway_target_url,
		      release_id          = excluded.release_id,
		      manifest_hash       = excluded.manifest_hash,
		      role                = excluded.role,
		      host_certificate    = coalesce(compute_nodes.host_certificate, excluded.host_certificate),
		      cert_fingerprint    = coalesce(compute_nodes.cert_fingerprint, excluded.cert_fingerprint),
		      generation          = coalesce(compute_nodes.generation, excluded.generation)
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		          region, zone, schedd_target_url, gateway_target_url,
		          public_ip, public_ip_set_at,
		          release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		          lifecycle
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB, node.VCPUBudget,
		node.Region, node.Zone, node.GatewayTargetURL,
		node.PublicIp, node.PublicIpSetAt,
		node.ReleaseID, node.ManifestHash, node.HostCertificate, node.CertFingerprint,
		node.Role, node.Generation)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, fmt.Errorf("state: upsert compute_node %q: %w", node.Name, err)
	}
	return n, nil
}

// UpsertComputeNodeFromOperator is the apid POST /v1/compute-nodes
// write path. The operator owns target_url; the on-conflict
// branch re-applies target_url from the excluded row so the
// operator's POST wins on every field. Identical schema to
// UpsertComputeNode today — split out as a distinct method so
// the ownership boundary is visible at the call site
// (cmd/apid/compute_nodes.go) and so future divergence (e.g.
// an operator-side COALESCE for region/zone that vmmd shouldn't
// touch) has exactly one place to land.
func (s *PgStore) UpsertComputeNodeFromOperator(ctx context.Context, node ComputeNode) (ComputeNode, error) {
	// Operator owns the release-bundle metadata too: PR-X secrets init
	// stamps host_certificate / cert_fingerprint at first contact, the
	// renderer (PR-2) stamps manifest_hash + role, release install
	// (PR-3) stamps release_id. Every PR-3a column re-applies on
	// conflict so the operator's POST wins across the board. The
	// generation counter still uses COALESCE because the doctor's bump
	// must be monotonic — a subsequent operator POST must not lower it.
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, vcpu_budget, lifecycle,
		     region, zone, gateway_target_url,
		     public_ip, public_ip_set_at,
		     release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation)
		values ($1, $2, $3, $4, $5, $6, $7, 'active'::compute_node_lifecycle,
		        $8, $9, $10,
		        $11, $12,
		        $13, $14, $15, $16, $17, $18)
		on conflict (name) do update
		  set target_url          = excluded.target_url,
		      vpcpus              = excluded.vpcpus,
		      mem_mb              = excluded.mem_mb,
		      max_concurrency     = excluded.max_concurrency,
		      admission_ceiling_mb = excluded.admission_ceiling_mb,
		      vcpu_budget         = excluded.vcpu_budget,
		      lifecycle           = 'active'::compute_node_lifecycle,
		      region              = excluded.region,
		      zone                = excluded.zone,
		      schedd_target_url   = excluded.schedd_target_url,
		      gateway_target_url  = excluded.gateway_target_url,
		      release_id          = excluded.release_id,
		      manifest_hash       = excluded.manifest_hash,
		      host_certificate    = excluded.host_certificate,
		      cert_fingerprint    = excluded.cert_fingerprint,
		      role                = excluded.role,
		      generation          = coalesce(compute_nodes.generation, excluded.generation)
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		          region, zone, schedd_target_url, gateway_target_url,
		          public_ip, public_ip_set_at,
		          release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		          lifecycle
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB, node.VCPUBudget,
		node.Region, node.Zone, node.GatewayTargetURL,
		node.PublicIp, node.PublicIpSetAt,
		node.ReleaseID, node.ManifestHash, node.HostCertificate, node.CertFingerprint,
		node.Role, node.Generation)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, fmt.Errorf("state: upsert compute_node (operator) %q: %w", node.Name, err)
	}
	return n, nil
}

// UpsertComputeNodeFromVmmd is the vmmd self-registration write
// path (cmd/vmmd/register.go). Writes the vmmd-owned resource
// numbers but does not change the operator-controlled active bit.
// On conflict, target_url is
// PRESERVED — `coalesce(compute_nodes.target_url,
// excluded.target_url)` keeps the existing operator-POSTed
// value intact. The COALESCE handles the cold-INSERT case where
// no operator POST has happened yet: the seed row from migration
// 00024 carries a non-empty target_url, but a fresh
// `registerComputeNode` on a brand-new box (no seed, no POST)
// still gets a non-null target_url via excluded.target_url.
//
// This is the load-bearing fix for the second-box cutover:
// without the COALESCE, vmmd's startup UPSERT silently
// overwrote the operator's carefully-POSTed `tcp://vmmd-2.faas:50051`
// with whatever `listen_addr` contained (often
// `tcp://0.0.0.0:50051`), routing wakes to the local host
// instead of the second box. See
// docs/runbooks/multi-host-rollout.md §3.5 + §4.5.
//
// Multi-host safety cluster PR-4 (audit F6, ADR-052 amendment)
// adds a pre-flight check: if the existing row's cert_fingerprint
// is set AND differs from node.CertFingerprint, the upsert refuses
// with ErrCertFingerprintDrift rather than silently COALESCEing
// the old value (the previous "silent preserve" semantics was a
// load-bearing fix for the cutover, but a leak that replaced the
// local cert would also be silently preserved). The failing-closed
// path requires an explicit operator reconcile via `gregale pki
// reconcile` before vmmd can start — the migration 00347 unique
// partial index is the DB-level belt-and-braces companion.
//
// The check is pre-flight (a SELECT before the upsert) rather than
// post-flight (compare RETURNING vs input) because a post-flight
// refusal would have already done the conflict-path UPDATE
// (a no-op on cert_fingerprint under the COALESCE, but still
// touches last_heartbeat_at indirectly). Pre-flight refuses cleanly.
// VmmdReregisterStaleWindow is how stale a compute_node's heartbeat must
// be before a re-registering vmmd is allowed to clear an inactive flag.
//
// Shorter than this and the row is assumed to have been drained
// deliberately by an operator (a drain targets a live node, so its
// heartbeat is fresh); longer and it was reaped by the watchdog for
// going silent, which a restart legitimately resolves.
//
// Mirrors sched.DefaultHeartbeatStaleness. Duplicated rather than
// imported because pkg/state must not depend on pkg/sched.
const VmmdReregisterStaleWindow = 90 * time.Second

func (s *PgStore) UpsertComputeNodeFromVmmd(ctx context.Context, node ComputeNode) (ComputeNode, error) {
	if node.Name != "" && node.CertFingerprint != nil && *node.CertFingerprint != "" {
		existingFP, err := s.loadComputeNodeCertFingerprint(ctx, node.Name)
		if err != nil {
			return ComputeNode{}, fmt.Errorf("state: pre-flight cert fingerprint read %q: %w", node.Name, err)
		}
		if existingFP != nil && *existingFP != "" && *existingFP != *node.CertFingerprint {
			return ComputeNode{}, fmt.Errorf(
				"state: %w: node %q existing fingerprint %q differs from local leaf %q — reconcile via `gregale pki reconcile %s`",
				ErrCertFingerprintDrift, node.Name, *existingFP, *node.CertFingerprint, node.Name,
			)
		}
	}
	// vmmd self-registration — the vmmd-owned resource numbers win on
	// conflict, while operator-POSTed values are PRESERVED via COALESCE
	// (the load-bearing fix for the second-box cutover, see the prose
	// comment on this method). The PR-3a release-bundle columns follow
	// the same shape: release_id / manifest_hash / role are operator-
	// POSTed and use COALESCE (vmmd must not overwrite them), and
	// host_certificate / cert_fingerprint are secrets-init-driven and
	// use COALESCE on the existing value too (vmmd doesn't write cert
	// material; only PR-X secrets init does). generation uses
	// COALESCE so the doctor's monotonic counter survives.
	row := s.pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, vcpu_budget, lifecycle,
		     region, zone, schedd_target_url, gateway_target_url,
		     public_ip, public_ip_set_at,
		     release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation)
		values ($1, $2, $3, $4, $5, $6, $7, 'active'::compute_node_lifecycle,
		        $8, $9, $10,
		        $11, $12,
		        $13, $14, $15, $16, $17, $18, $19)
		on conflict (name) do update
		  set vpcpus              = excluded.vpcpus,
		      mem_mb              = excluded.mem_mb,
		      max_concurrency     = excluded.max_concurrency,
		      admission_ceiling_mb = excluded.admission_ceiling_mb,
		      vcpu_budget         = excluded.vcpu_budget,
		      target_url          = coalesce(compute_nodes.target_url, excluded.target_url),
		      region              = coalesce(compute_nodes.region, excluded.region),
		      zone                = coalesce(compute_nodes.zone, excluded.zone),
		      schedd_target_url   = coalesce(compute_nodes.schedd_target_url, excluded.schedd_target_url),
		      gateway_target_url  = coalesce(compute_nodes.gateway_target_url, excluded.gateway_target_url),
		      public_ip           = coalesce(compute_nodes.public_ip, excluded.public_ip),
		      public_ip_set_at    = coalesce(compute_nodes.public_ip_set_at, excluded.public_ip_set_at),
		      release_id          = coalesce(compute_nodes.release_id, excluded.release_id),
		      manifest_hash       = coalesce(compute_nodes.manifest_hash, excluded.manifest_hash),
		      host_certificate    = coalesce(compute_nodes.host_certificate, excluded.host_certificate),
		      cert_fingerprint    = coalesce(compute_nodes.cert_fingerprint, excluded.cert_fingerprint),
		      role                = coalesce(compute_nodes.role, excluded.role),
		      generation          = coalesce(compute_nodes.generation, excluded.generation)
		returning id, name, target_url, vpcpus, mem_mb, max_concurrency,
		          admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		          region, zone, schedd_target_url, gateway_target_url,
		          public_ip, public_ip_set_at,
		          release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		          lifecycle
	`, node.Name, node.TargetURL, node.VPCPUs, node.MemMB, node.MaxConcurrency,
		node.AdmissionCeilingMB, node.VCPUBudget,
		node.Region, node.Zone, node.ScheddTargetURL, node.GatewayTargetURL,
		node.PublicIp, node.PublicIpSetAt,
		node.ReleaseID, node.ManifestHash, node.HostCertificate, node.CertFingerprint,
		node.Role, node.Generation)
	n, err := scanComputeNode(row)
	if err != nil {
		return ComputeNode{}, fmt.Errorf("state: upsert compute_node (vmmd) %q: %w", node.Name, err)
	}
	return n, nil
}

// loadComputeNodeCertFingerprint reads the cert_fingerprint column
// for an existing compute_nodes row. Returns (nil, nil) when the row
// does not exist (a fresh INSERT case where the pre-flight check
// trivially passes — there is no existing row to compare against).
//
// Used by UpsertComputeNodeFromVmmd's pre-flight check (multi-host
// safety cluster PR-4 / audit F6). Lives on PgStore rather than the
// Store interface because it's an internal pre-flight helper; an
// external caller (e.g. the future doctor drift detector) will use
// the existing ListComputeNodes path.
func (s *PgStore) loadComputeNodeCertFingerprint(ctx context.Context, name string) (*string, error) {
	var fp *string
	err := s.pool.QueryRow(ctx, `
		select cert_fingerprint from compute_nodes where name = $1
	`, name).Scan(&fp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return fp, nil
}

// UpsertNodeKey inserts or updates a (compute_node_id, key_id) row
// in compute_node_keys (migration 00076, ADR-053). vmmd's
// self-registration calls this on startup once it has loaded its
// node signing key (cmd/vmmd/main.go::loadNodeSigningKey) and
// computed the key_id (the SHA-256 hex of the SubjectPublicKeyInfo).
//
// ON CONFLICT is a no-op (DO NOTHING) because key material is
// write-once — re-applying public_key_pem on conflict would
// silently overwrite a rotation that produced a different key
// (the PK is (compute_node_id, key_id), so a rotated key on the
// same node has a different key_id and lands as a fresh row).
// Re-stamping public_key_pem on the same key_id would be a
// defensive no-op anyway (the bytes are deterministic) but we
// keep the explicit semantics to flag the omit-intent at review
// time. Migration 00075's CHECK constraints
// (compute_node_keys_key_id_shape, compute_node_keys_pem_shape)
// reject malformed shapes at INSERT — a vmmd that mints a
// non-64-hex-char key_id or a non-PEM block fails loud at the
// persist step rather than corrupting the registry.
func (s *PgStore) UpsertNodeKey(ctx context.Context, nodeID string, keyID string, publicKeyPEM string) error {
	_, err := s.pool.Exec(ctx, `
		insert into compute_node_keys (compute_node_id, key_id, public_key_pem)
		values ($1, $2, $3)
		on conflict (compute_node_id, key_id) do nothing
	`, nodeID, keyID, publicKeyPEM)
	if err != nil {
		return fmt.Errorf("state: upsert compute_node_keys (node=%q, key=%s): %w", nodeID, keyID, err)
	}
	return nil
}

// SetComputeNodeActive flips the row's lifecycle (Workstream B,
// ADR-137). Legacy pre-00599 implementation wrote the (now STORED
// GENERATED) `active` column directly; PG rejects that with SQLSTATE
// 428C9, so the rewrite maps the boolean onto lifecycle semantics:
//
//	true  → lifecycle='active'     (heartbeat reactivation; row admits wakes)
//	false → lifecycle='unavailable' (watchdog drain-on-stale; row rejects wakes)
//
// The watchdog uses `false` to mark a row drained when
// last_heartbeat_at ages past 90s, and the heartbeat goroutine uses
// `true` to reactivate a drained row on the next successful dial. The
// pg_notify trigger on compute_nodes (operator-visible via
// pkg/db/notify.NotifyComputeNodeChanged) fires on the UPDATE so
// gatewayd-internal's per-node client cache can drop/add entries
// without restart. Note: this method does NOT distinguish between
// operator-initiated drains (which the drain API records as
// `draining` lifecycle for audit) and watchdog-initiated drains —
// it always lands on `unavailable`. Use NodeSetLifecycle directly
// for the operator-initiated `draining` path.
func (s *PgStore) SetComputeNodeActive(ctx context.Context, id string, active bool) error {
	target := NodeLifecycleUnavailable
	if active {
		target = NodeLifecycleActive
	}
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set lifecycle = $2::compute_node_lifecycle where id = $1`, id, string(target))
	if err != nil {
		return fmt.Errorf("state: set lifecycle compute_node %s = %v: %w", id, target, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetComputeNodeRole overwrites the role column on a row by id
// (ADR-112 PR-B). The role value is validated against the
// {control-plane, compute-only} allow-list at the Go boundary
// (empty role is rejected with a loud error — use
// UpsertComputeNodeFromOperator with role=NULL if the un-templated
// sentinel is ever needed). The migration 00271 column is nullable
// text (no CHECK constraint); the Go-side validation is the
// effective constraint.
//
// Allow-list divergence vs pkg/releaseinstall: the manifest
// renderer's SetComputeNodeRole (pkg/releaseinstall.store.go:131,
// name-keyed, manifest-time) accepts {empty, single-box,
// control-plane, compute-only}; this runtime path
// (id-keyed, PR-B) accepts only {control-plane, compute-only}.
// The asymmetry is intentional and load-bearing:
//
//   - `single-box` is the legacy single-box dev posture
//     (pkg/roleTemplating/role.go:64-69). The renderer allows it
//     because pre-PR-A manifests still pass it through for
//     backwards compat with v1 bootstrap.sh.
//   - PR-B is the runtime re-role contract; "re-role a box to
//     single-box" is not a thing the architecture supports after
//     the ADR-112 collapse. A box that was first-boot templated as
//     single-box CANNOT be re-rolled via this path; the operator
//     must image-rebuild if they need to leave single-box.
//
// Future "re-role to single-box" support, if ever needed, is a
// separate ADR — bumping the allow-list here without one would
// re-introduce the per-role-image pathology ADR-112 fixed.
//
// The pg_notify trigger on compute_nodes (PR-3a) fires on every UPDATE
// regardless of which column changed, so gatewayd-internal's per-node
// cache and the schedd chooser re-rank immediately. The chooser
// currently treats role as a tie-break only (ADR-110 PR-2), but the
// load-bearing invariant is that the cluster-wide view in
// compute_nodes.role matches the box's on-disk FAAS_BOX_ROLE (read
// from the per-daemon drop-in); a drift is loud via doctor --deep.
//
// Callers MUST short-circuit when current == target — every
// unconditional UPDATE here fires the trigger + notify storm. The
// cmd/gregalectl role-branch does this idempotency short-circuit
// at the caller side; this method itself stays unconditional.
func (s *PgStore) SetComputeNodeRole(ctx context.Context, id string, role string) error {
	if err := validateRoleForState(role); err != nil {
		return fmt.Errorf("state: set role compute_node %s: %w", id, err)
	}
	tag, err := s.pool.Exec(ctx,
		`update compute_nodes set role = $2 where id = $1`, id, role)
	if err != nil {
		return fmt.Errorf("state: set role compute_node %s = %q: %w", id, role, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// validateRoleForState enforces the {empty, control-plane,
// compute-only} allow-list at the Go boundary. Lives next to the
// implementation (not in pkg/roleTemplating) because pkg/state must
// not import pkg/roleTemplating — the chain is pkg/state → pkg/role
// (the type layer), and pkg/roleTemplating sits on top of both.
//
// Empty role is rejected (the legacy un-templated sentinel is gone
// post-PR-A; UpsertComputeNodeFromOperator writes NULL on insert and
// preserves it on conflict via COALESCE, which is the canonical
// "un-templated" path).
var allowedRolesForState = map[string]struct{}{
	"control-plane": {},
	"compute-only":  {},
}

func validateRoleForState(role string) error {
	if role == "" {
		return fmt.Errorf("role cannot be empty; use UpsertComputeNodeFromOperator for un-templated rows")
	}
	if len(role) > 32 {
		return fmt.Errorf("role %q exceeds 32-char column limit", role)
	}
	if _, ok := allowedRolesForState[role]; !ok {
		return fmt.Errorf("role %q not in {control-plane, compute-only}", role)
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
	// Column order is locked to scanComputeNode's 22-arg projection
	// (pgstore.go:8346). PR-3a (issue #911 / ADR-110) widened it
	// from 14 to 22 by adding public_ip / public_ip_set_at (migration
	// 00174 closure) + release_id / manifest_hash / host_certificate
	// / cert_fingerprint / role / generation (migration 00266).
	// Drift here surfaces as pgx's "number of field descriptions
	// must equal number of destinations, got 14 and 22" — the same
	// class of failure TestPg_CoverageInstanceLists pins.
	q := `
		select id, name, target_url, vpcpus, mem_mb, max_concurrency,
		       admission_ceiling_mb, vcpu_budget, active, last_heartbeat_at, created_at,
		       region, zone, schedd_target_url, gateway_target_url,
		       public_ip, public_ip_set_at,
		       release_id, manifest_hash, host_certificate, cert_fingerprint, role, generation,
		       lifecycle
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

// nodeRowToComputeNode converts a sqlc.NodeGetByNameRow / NodeGetRow
// (the field set is identical for the lifecycle-projection queries in
// queries.sql) back to the legacy ComputeNode model. The legacy
// fields are the first 22; the lifecycle fields follow.
func nodeRowToComputeNode(r sqlc.NodeGetRow) ComputeNode {
	n := ComputeNode{
		ID:                 uuidString(r.ID),
		Name:               r.Name,
		TargetURL:          r.TargetUrl,
		VPCPUs:             int(r.Vpcpus),
		MemMB:              int(r.MemMb),
		MaxConcurrency:     int(r.MaxConcurrency),
		AdmissionCeilingMB: int(r.AdmissionCeilingMb),
		Active:             r.Active.Bool,
		LastHeartbeatAt:    timestamptzToTime(r.LastHeartbeatAt),
		CreatedAt:          timestamptzToTime(r.CreatedAt),
	}
	if r.Region.Valid {
		v := r.Region.String
		n.Region = &v
	}
	if r.Zone.Valid {
		v := r.Zone.String
		n.Zone = &v
	}
	if r.ScheddTargetUrl.Valid {
		v := r.ScheddTargetUrl.String
		n.ScheddTargetURL = &v
	}
	if r.VcpuBudget != 0 {
		n.VCPUBudget = int(r.VcpuBudget)
	}
	if r.PublicIp != nil {
		ip := *r.PublicIp
		n.PublicIp = &ip
	}
	n.PublicIpSetAt = timestamptzToTimePtr(r.PublicIpSetAt)
	if r.ReleaseID.Valid {
		v := r.ReleaseID.String
		n.ReleaseID = &v
	}
	if r.ManifestHash.Valid {
		v := r.ManifestHash.String
		n.ManifestHash = &v
	}
	if r.HostCertificate.Valid {
		v := r.HostCertificate.String
		n.HostCertificate = &v
	}
	if r.CertFingerprint.Valid {
		v := r.CertFingerprint.String
		n.CertFingerprint = &v
	}
	if r.Role.Valid {
		v := r.Role.String
		n.Role = &v
	}
	if r.Generation.Valid {
		v := int(r.Generation.Int32)
		n.Generation = &v
	}
	if r.GatewayTargetUrl.Valid {
		v := r.GatewayTargetUrl.String
		n.GatewayTargetURL = &v
	}
	// Lifecycle fields (Workstream B, 00579 + 00582).
	n.Lifecycle = NodeLifecycle(r.Lifecycle)
	n.DrainInitiatedAt = timestamptzToTimePtr(r.DrainInitiatedAt)
	n.DrainCompletedAt = timestamptzToTimePtr(r.DrainCompletedAt)
	n.RecoveryInitiatedAt = timestamptzToTimePtr(r.RecoveryInitiatedAt)
	if r.LastRecoveryOutcome.Valid {
		v := r.LastRecoveryOutcome.String
		n.LastRecoveryOutcome = &v
	}
	return n
}

// uuidString returns the canonical hyphenated form for a pgtype.UUID.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}

// timestamptzToTime converts a pgtype.Timestamptz to time.Time, treating
// the zero/invalid value as the zero time.
func timestamptzToTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// timestamptzToTimePtr is the *time.Time variant of timestamptzToTime.
func timestamptzToTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// NodeGet returns one ComputeNode by id with all lifecycle fields
// populated. Wraps the sqlc NodeGet query.
func (s *PgStore) NodeGet(ctx context.Context, id string) (ComputeNode, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return ComputeNode{}, fmt.Errorf("state: invalid uuid %q: %w", id, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	row, err := s.triggerQueries().NodeGet(ctx, s.pool, p)
	if err != nil {
		return ComputeNode{}, mapErr(err)
	}
	return nodeRowToComputeNode(row), nil
}

// NodeGetByName mirrors NodeGet keyed by name.
func (s *PgStore) NodeGetByName(ctx context.Context, name string) (ComputeNode, error) {
	row, err := s.triggerQueries().NodeGetByName(ctx, s.pool, name)
	if err != nil {
		return ComputeNode{}, mapErr(err)
	}
	return nodeRowToComputeNodeFromNamed(row), nil
}

// nodeRowToComputeNodeFromNamed mirrors nodeRowToComputeNode for the
// NodeGetByName query (sqlc emits a separate Row struct per :one query).
func nodeRowToComputeNodeFromNamed(r sqlc.NodeGetByNameRow) ComputeNode {
	// Both Row types share the same field set; the helper above
	// expects a NodeGetRow, so copy through.
	return nodeRowToComputeNode(sqlc.NodeGetRow(r))
}

// NodeList returns every compute_node in name order, optionally
// filtered by lifecycle. Empty string = any lifecycle.
func (s *PgStore) NodeList(ctx context.Context, lifecycle NodeLifecycle) ([]ComputeNode, error) {
	rows, err := s.triggerQueries().NodeList(ctx, s.pool, string(lifecycle))
	if err != nil {
		return nil, err
	}
	out := make([]ComputeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, nodeRowToComputeNode(sqlc.NodeGetRow(r)))
	}
	return out, nil
}

// NodeSetLifecycle is the CAS transition added by 00579. The expected
// predicate (lifecycle::text = $expected) blocks two-writer races
// from 'active' to 'draining' vs 'unavailable' concurrently. Returns
// ErrNotFound when the id has no row, ErrConflict when the CAS didn't
// land (the caller re-reads via NodeGet and decides whether to retry).
func (s *PgStore) NodeSetLifecycle(ctx context.Context, id string, expected, next NodeLifecycle) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("state: invalid uuid %q: %w", id, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	rows, err := s.triggerQueries().NodeSetLifecycle(ctx, s.pool, sqlc.NodeSetLifecycleParams{
		ID:               p,
		Lifecycle:        sqlc.ComputeNodeLifecycle(string(expected)),
		Column3:          sqlc.ComputeNodeLifecycle(string(next)),
		DrainInitiatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return mapErr(err)
	}
	if rows == 0 {
		// A zero row count is ambiguous: either another writer won the
		// CAS, or the row was deleted after the caller's read. Re-read
		// by id so the Store contract can distinguish ErrNotFound from
		// an ordinary lifecycle race.
		if _, getErr := s.NodeGet(ctx, id); getErr != nil {
			if errors.Is(getErr, ErrNotFound) {
				return ErrNotFound
			}
			return getErr
		}
		return ErrConflict
	}
	return nil
}

// NodeListRecoverable returns every node in ('unavailable','recovering') —
// the recovery arbiter's input set.
func (s *PgStore) NodeListRecoverable(ctx context.Context) ([]ComputeNode, error) {
	rows, err := s.triggerQueries().NodeListRecoverable(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]ComputeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, nodeRowToComputeNode(sqlc.NodeGetRow(r)))
	}
	return out, nil
}

// NodeListDrainable returns every 'active' node with zero live
// instances — the set the drain handler is allowed to flip to
// 'draining' without operator override.
func (s *PgStore) NodeListDrainable(ctx context.Context) ([]ComputeNode, error) {
	rows, err := s.triggerQueries().NodeListDrainable(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]ComputeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, nodeRowToComputeNode(sqlc.NodeGetRow(r)))
	}
	return out, nil
}

// NodeMarkDrainCompleted stamps drain_completed_at + flips lifecycle
// to 'active' (CAS on 'draining').
func (s *PgStore) NodeMarkDrainCompleted(ctx context.Context, id string, completedAt time.Time) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("state: invalid uuid %q: %w", id, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	rows, err := s.triggerQueries().NodeMarkDrainCompleted(ctx, s.pool, sqlc.NodeMarkDrainCompletedParams{
		ID:               p,
		DrainCompletedAt: pgtype.Timestamptz{Time: completedAt, Valid: true},
	})
	if err != nil {
		return mapErr(err)
	}
	if rows == 0 {
		return ErrConflict
	}
	return nil
}

// NodeMarkRecovered stamps last_recovery_outcome='succeeded' + flips
// lifecycle to 'active' (CAS on 'recovering').
func (s *PgStore) NodeMarkRecovered(ctx context.Context, id string) error {
	uid, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("state: invalid uuid %q: %w", id, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	rows, err := s.triggerQueries().NodeMarkRecovered(ctx, s.pool, p)
	if err != nil {
		return mapErr(err)
	}
	if rows == 0 {
		return ErrConflict
	}
	return nil
}

// InstanceListByNodeForRecovery returns the live instances on a node
// the recovery arbiter can act on.
func (s *PgStore) InstanceListByNodeForRecovery(ctx context.Context, nodeID string) ([]RecoveryInstance, error) {
	uid, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, fmt.Errorf("state: invalid uuid %q: %w", nodeID, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	rows, err := s.triggerQueries().InstanceListByNodeForRecovery(ctx, s.pool, p)
	if err != nil {
		return nil, err
	}
	out := make([]RecoveryInstance, len(rows))
	for i, r := range rows {
		out[i] = RecoveryInstance{
			ID:           uuidString(r.ID),
			State:        r.State,
			AppID:        uuidString(r.AppID),
			DeploymentID: uuidString(r.DeploymentID),
		}
	}
	return out, nil
}

// DeploymentRecordSnapshotMiss stamps per-deployment backoff state.
// The retry-after math lives in pkg/sched/snapshot_backoff.go; this
// is the state write only.
func (s *PgStore) DeploymentRecordSnapshotMiss(ctx context.Context, deploymentID string, backoffUntil time.Time) error {
	uid, err := uuid.Parse(deploymentID)
	if err != nil {
		return fmt.Errorf("state: invalid uuid %q: %w", deploymentID, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	return s.triggerQueries().DeploymentRecordSnapshotMiss(ctx, s.pool, sqlc.DeploymentRecordSnapshotMissParams{
		ID:                       p,
		SnapshotMissBackoffUntil: pgtype.Timestamptz{Time: backoffUntil, Valid: true},
	})
}

// DeploymentClearSnapshotBackoff resets the counter + clears the
// backoff. Called by the recovery arbiter after a successful sweep.
func (s *PgStore) DeploymentClearSnapshotBackoff(ctx context.Context, deploymentID string) error {
	uid, err := uuid.Parse(deploymentID)
	if err != nil {
		return fmt.Errorf("state: invalid uuid %q: %w", deploymentID, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	return s.triggerQueries().DeploymentClearSnapshotBackoff(ctx, s.pool, p)
}

// DeploymentSnapshotBackoffActive returns the stored row whenever a
// backoff timestamp is present. The bool reports whether that timestamp
// is still active; expired rows are retained so the caller can preserve
// the miss count when computing the next backoff.
func (s *PgStore) DeploymentSnapshotBackoffActive(ctx context.Context, deploymentID string) (Deployment, bool, error) {
	uid, err := uuid.Parse(deploymentID)
	if err != nil {
		return Deployment{}, false, fmt.Errorf("state: invalid uuid %q: %w", deploymentID, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	row, err := s.triggerQueries().DeploymentSnapshotBackoffActive(ctx, s.pool, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, false, nil
		}
		return Deployment{}, false, mapErr(err)
	}
	if !row.SnapshotMissBackoffUntil.Valid {
		return Deployment{}, false, nil
	}
	d := Deployment{
		ID:                       uuidString(p),
		SnapshotMissCount:        int(row.SnapshotMissCount),
		SnapshotMissBackoffUntil: timestamptzToTimePtr(row.SnapshotMissBackoffUntil),
	}
	return d, row.SnapshotMissBackoffUntil.Time.After(time.Now().UTC()), nil
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

// AppendEvent writes one row to the events table. The subject, when
// non-nil, MUST be a canonical UUID (with hyphens). The hex-only
// fallback that MemStore accepts (engine.go walks MemStore.newID
// which returns 32-char hex; parseSubjectID converts the hex back
// to UUID bytes) is intentionally NOT mirrored here — PgStore is
// the production path and only canonical UUIDs reach it from
// sqlc / migrations. A non-canonical subject is silently dropped
// to NULL rather than failing the INSERT; the audit row's subject
// column is filtered server-side by listAuditEvents (handlers_audit.go)
// so a NULL subject means "won't show up under any acct-scoped
// query" — which matches the "unparseable subject" path on the
// MemStore side (memstore.go:3135-3139).
//
// Adding a hex fallback here would let a future contributor stamp
// events with engine-hex IDs without realising the events_subject_idx
// expects canonical UUIDs, leading to "the row landed but
// ListEvents(subject=<hex>) returns nothing" — a silent-drop bug
// AppendEvent (pre-PR-#TBD shim) delegates to AppendEventWithTrace
// with traceID=nil. Retained so the existing Store interface stays
// source-compatible for the many test doubles that only override the
// four-arg signature.
func (s *PgStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	return s.AppendEventWithTrace(ctx, actor, kind, subject, data, nil)
}

// AppendEventWithTrace writes one row to events with an optional
// OTel W3C 32-char hex trace_id (migrations/00486). When traceID
// is nil the column is left NULL — pre-PR rows + cron-fired rows
// without an inbound trace_id keep that shape. The regex CHECK
// on events.trace_id is enforced by Postgres on INSERT; a non-hex
// value surfaces as SQLSTATE 23514 to the caller.
func (s *PgStore) AppendEventWithTrace(ctx context.Context, actor, kind string, subject *string, data []byte, traceID *string) error {
	var subj *uuid.UUID
	if subject != nil {
		u, err := uuid.Parse(*subject)
		if err == nil {
			subj = &u
		}
	}
	_, err := s.pool.Exec(ctx,
		`insert into events (actor, kind, subject, trace_id, data) values ($1, $2, $3, $4, $5::jsonb)`,
		actor, kind, subj, traceID, data)
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

// ListEventsByWakeID (issue #517 / PR-C, ADR-064) — the
// production read-side query for the customer-facing
// GET /v1/apps/{slug}/wakes/{wake_id}/timeline endpoint. Filters
// on the jsonb expression index events_wake_id_idx
// (migrations/00113_events_wake_id_idx.sql) and orders by at ASC
// so the timeline reads as a forward narrative. Uses raw SQL
// (mirroring AppendEvent / ListEvents) so the method shape stays
// consistent with the rest of the events table surface — the
// sqlc-generated ListEventsByWakeID in pkg/state/sqlc is used by
// the migration test suite, not the production reader.
func (s *PgStore) ListEventsByWakeID(ctx context.Context, wakeID string, since time.Time, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows pgx.Rows
	var err error
	if since.IsZero() {
		rows, err = s.pool.Query(ctx,
			`select id, at, actor, kind, subject, data from events
			 where data->>'wake_id' = $1
			 order by at asc limit $2`,
			wakeID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, at, actor, kind, subject, data from events
			 where data->>'wake_id' = $1 and at > $2
			 order by at asc limit $3`,
			wakeID, since, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0, 16)
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

// LookupBootStartedForWakes (ADR-123) returns the wake-boot telemetry
// for each wake_id in the input slice, indexed by wake_id. One SQL
// round-trip via the events_wake_id_idx jsonb expression index
// (migrations/00114_events_wake_id_idx.sql) — DISTINCT ON picks the
// earliest wake.boot_started row per wake_id so a re-wake that emits
// a second boot_started still maps to the original cause. Empty map
// when no rows match (pre-ADR-123 fleet or a wake_id mismatch).
// Nil/empty input returns an empty map without touching the pool.
func (s *PgStore) LookupBootStartedForWakes(ctx context.Context, wakeIDs []string) (map[string]WakeBootMeta, error) {
	if len(wakeIDs) == 0 {
		return map[string]WakeBootMeta{}, nil
	}
	// PR-A: extended SQL adds (a) at_capacity from the boot_started
	// jsonb (COALESCE false for pre-PR-A rows) and (b) ready_in_ms
	// computed as the total elapsed milliseconds between the
	// boot_started row's `at` and the matching boot_completed row's
	// `at` via a LEFT JOIN LATERAL against the events table.
	//
	// ready_in_ms uses EXTRACT(EPOCH FROM (bc.completed_at -
	// bs.started_at)) * 1000 NOT EXTRACT(MILLISECONDS FROM …)
	// because PostgreSQL intervals are stored as months/days/seconds
	// and EXTRACT(MILLISECONDS FROM interval) returns ONLY the
	// seconds-field milliseconds (verified via psql: an interval of
	// '65.5 seconds' extracts as 5500 ms, not 65500 ms). EXTRACT(EPOCH
	// …) returns total elapsed seconds as numeric and multiplying by
	// 1000 yields the wall-clock millisecond delta — accurate for any
	// duration. This is the spec §14 V6 "wake latency must be wall-
	// clock accurate" invariant; ready_in_ms is the customer-facing
	// counterpart.
	//
	// at_capacity_present distinguishes pre-PR-A fleet rows (jsonb
	// key absent) from PR-A rows that explicitly stamped false — the
	// dashboard's em-dash-on-absent convention depends on this.
	// Computed inline via `data ? 'at_capacity'` (jsonb contains
	// operator), NULL on the outermost SELECT when the boot_started
	// row has no at_capacity key.
	//
	// Both LATERAL subqueries hit the existing events_wake_id_idx
	// partial index from migration 00114 — no new index, no new
	// migration. The DISTINCT ON still prefers the earliest
	// wake.boot_started row (canonical) over the vmmd mirror
	// fallback (pkg/vmmdgrpc/server.go:emitBootStartedMirror).
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (bs.wake_id)
		        bs.wake_id                       AS wake_id,
		        bs.trigger                       AS trigger,
		        bs.queued_count                  AS queued_count,
		        bs.concurrency_at_admit          AS concurrency_at_admit,
		        bs.at_capacity                   AS at_capacity,
		        bs.at_capacity_present           AS at_capacity_present,
		        COALESCE((EXTRACT(EPOCH FROM (bc.completed_at - bs.started_at)) * 1000)::int, 0) AS ready_in_ms
		 FROM (
		   SELECT DISTINCT ON (data->>'wake_id')
		          data->>'wake_id'             AS wake_id,
		          data->>'trigger'             AS trigger,
		          (data->>'queued_count')::int        AS queued_count,
		          (data->>'concurrency_at_admit')::int AS concurrency_at_admit,
		          COALESCE((data->>'at_capacity')::bool, false) AS at_capacity,
		          (data ? 'at_capacity')             AS at_capacity_present,
		          at                                     AS started_at
		     FROM events
		    WHERE kind = 'wake.boot_started'
		      AND data->>'wake_id' = ANY($1)
		    ORDER BY data->>'wake_id', at ASC
		 ) bs
		 LEFT JOIN LATERAL (
		   SELECT at AS completed_at
		     FROM events
		    WHERE kind = 'wake.boot_completed'
		      AND data->>'wake_id' = bs.wake_id
		    ORDER BY at ASC LIMIT 1
		 ) bc ON true
		 ORDER BY bs.wake_id`,
		wakeIDs)
	if err != nil {
		return nil, fmt.Errorf("LookupBootStartedForWakes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]WakeBootMeta, len(wakeIDs))
	for rows.Next() {
		var wakeID string
		var meta WakeBootMeta
		var trigger *string
		var atCapPresent *bool
		if err := rows.Scan(&wakeID, &trigger, &meta.QueuedCount, &meta.ConcurrencyAtAdmit, &meta.AtCapacity, &atCapPresent, &meta.ReadyInMS); err != nil {
			return nil, fmt.Errorf("LookupBootStartedForWakes: scan: %w", err)
		}
		if trigger != nil {
			meta.Trigger = *trigger
		}
		if atCapPresent != nil {
			meta.AtCapacityPresent = *atCapPresent
		}
		out[wakeID] = meta
	}
	return out, rows.Err()
}

// CountWakeBootStarted24h (per-app dashboard, Hobby+) returns the
// count of wake.boot_started events the schedd recorded for the
// given app in the trailing 24 hours. Hand-written raw-SQL path
// — the sqlc-generated binding was deleted because it had the
// wrong parameter shape (bound the whole jsonb row against a UUID
// literal, which would always return 0). See the rationale
// comment at pkg/state/queries.sql for the full story.
//
// Performance note: the (data->>'app_id')::uuid predicate is NOT
// covered by the existing events_wake_id_idx jsonb expression
// index (migration 00114 indexes data->>'wake_id', not app_id).
// On a Scale-tier app with a large wake fleet the planner will
// seq-scan the trailing-24h wake.boot_started rows and
// re-evaluate the jsonb cast per row. The PR description's
// "sub-second via the existing index" claim was therefore wrong;
// a follow-up migration adding a covering index on
// (data->>'app_id', at) is tracked separately. Returns 0 on an
// empty app, a degraded store call, or when the events table
// predates the post-ADR-123 schema (pre-ADR-123 boot_started
// rows carry no app_id field, so the cast returns NULL which
// COUNT(*) coerces to 0 — same posture as the wake-timeline
// view's `WakeCountWithMeta` denominator at
// cmd/apid/handlers_dashboard.go:2659).
func (s *PgStore) CountWakeBootStarted24h(ctx context.Context, appID string) (int64, error) {
	const q = `SELECT COUNT(*) FROM events
WHERE kind = 'wake.boot_started'
  AND (data->>'app_id')::uuid = $1::uuid
  AND at >= now() - interval '24 hours'`
	var n int64
	if err := s.pool.QueryRow(ctx, q, appID).Scan(&n); err != nil {
		return 0, fmt.Errorf("CountWakeBootStarted24h: %w", err)
	}
	return n, nil
}

// ListAllEventsPaged (ADR-091 §3.7 / PR #3) is the operator-obs
// backend's read-side query for the live events table. Mirrors the
// SQL in pkg/state/queries.sql::ListAllEventsPaged; the raw-SQL
// fallback here keeps the param semantics identical to the sqlc
// version (no string-built queries, all parameters bound).
//
// Bounded by the handler to api.ObsAdminEventsLimitMax (500). The
// interface{} params for the discriminator columns (actor, kind_prefix,
// subject, since) match the sqlc-emitted shape — the SQL uses the
// ($1 = ” OR ...) predicate so the column type cannot be inferred.
// Bound as string / time.Time at the call site.
func (s *PgStore) ListAllEventsPaged(ctx context.Context, actor, kindPrefix, subject string, since time.Time, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if !since.IsZero() {
		since = since.UTC()
	}
	rows, err := s.pool.Query(ctx,
		`select id, at, actor, kind, subject, data from events
		 where ($1 = '' or actor = $1)
		   and ($2 = '' or kind like $2 || '%')
		   and ($3 = '' or subject = $3::uuid)
		   and ($4 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $4)
		 order by at desc, id desc
		 limit $5`,
		actor, kindPrefix, subject, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list_all_events_paged: %w", err)
	}
	defer rows.Close()
	out := make([]Event, 0, 16)
	for rows.Next() {
		var e Event
		var rawData []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Kind, &e.Subject, &rawData); err != nil {
			return nil, fmt.Errorf("state: list_all_events_paged: %w", err)
		}
		e.Data = json.RawMessage(rawData)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListRecentEventsForAccount (ADR-091 §3.7 / PR #3) is the
// per-account events drill-down. Backed by the partial
// events_actor_account_idx on (actor_account_id) WHERE actor_account_id IS NOT NULL
// (migrations/00099_orgs_memberships_invitations.sql). Same raw-SQL
// shape as ListAllEventsPaged; the actor_account_id is the indexed
// column so the planner picks the partial index on the per-account
// filter regardless of the since filter.
func (s *PgStore) ListRecentEventsForAccount(ctx context.Context, actorAccountID string, since time.Time, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if !since.IsZero() {
		since = since.UTC()
	}
	parsedUUID, err := uuid.Parse(actorAccountID)
	if err != nil {
		// Unparseable actor_account_id — match the existing
		// ListEvents behaviour where a malformed filter returns
		// an empty slice rather than a SQL error.
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, at, actor, kind, subject, data from events
		 where actor_account_id = $1
		   and ($2 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $2)
		 order by at desc, id desc
		 limit $3`,
		parsedUUID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list_recent_events_for_account: %w", err)
	}
	defer rows.Close()
	out := make([]Event, 0, 16)
	for rows.Next() {
		var e Event
		var rawData []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Kind, &e.Subject, &rawData); err != nil {
			return nil, fmt.Errorf("state: list_recent_events_for_account: %w", err)
		}
		e.Data = json.RawMessage(rawData)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEventsBySidecar (issue #463 / ADR-069 / PR-B) is the
// sidecar-aware read-side twin of ListEventsByWakeID. Filters on
// the jsonb expression data->>'sidecar_name' AND the closed
// kind IN ('wake.sidecar_init_exit', 'wake.sidecar_restart') so
// the query never returns non-sidecar rows even if a future
// event reuses the field name. Orders by at ASC; respects the
// same since / limit contract as ListEventsByWakeID.
//
// Index: the existing events_wake_id_idx jsonb expression index
// (migrations/00113_events_wake_id_idx.sql) covers
// data->>'wake_id', NOT data->>'sidecar_name'. PR-B does not
// add a parallel sidecar index — the kind filter is selective
// enough that the planner picks an events_kind_at_idx scan and
// the sidecar_name jsonb filter is applied as a residual. If
// sidecar event volume climbs, a follow-up migration adds
// events_sidecar_name_idx with the same shape as
// events_wake_id_idx.
func (s *PgStore) ListEventsBySidecar(ctx context.Context, sidecarName string, since time.Time, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	// Closed kind enum — mirrors the constants in
	// pkg/events/wake.go (WakeSidecarInitExit,
	// WakeSidecarRestart). The closed list keeps the planner
	// honest (an unknown kind won't quietly satisfy the
	// filter) and matches the in-memory twin's filter in
	// memstore.go.
	//
	// Index: events_sidecar_name_idx (migration 00121) is a
	// partial expression index restricted to the same closed
	// kinds, keyed on (data->>'sidecar_name')::text. The
	// planner picks it up for this query's predicate (verified
	// by TestMigrations_00121_EventsSidecarNameIdx's EXPLAIN
	// check). A future PR that adds a new closed sidecar-kind
	// must update the index's WHERE clause in lockstep.
	const kindFilter = "kind in ('wake.sidecar_init_exit', 'wake.sidecar_restart')"
	var rows pgx.Rows
	var err error
	if since.IsZero() {
		rows, err = s.pool.Query(ctx,
			`select id, at, actor, kind, subject, data from events
			 where `+kindFilter+` and data->>'sidecar_name' = $1
			 order by at asc limit $2`,
			sidecarName, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, at, actor, kind, subject, data from events
			 where `+kindFilter+` and data->>'sidecar_name' = $1 and at > $2
			 order by at asc limit $3`,
			sidecarName, since, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0, 16)
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

func (s *PgStore) AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error {
	// Idempotent on (instance_id, minute) for mb_seconds / requests
	// (mirrors the sqlc source in queries.sql::AppendUsage — make
	// sqlc-check verifies these stay in lockstep). The first write
	// of those columns wins; a redelivered minute is a no-op for
	// them so a meterd restart / network blip / two meterd
	// instances cannot inflate billing. M7 hardening, PR
	// feat/m7-beta-hardening.
	//
	// cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count,
	// and tail_seconds are ADDITIVE on the same conflict key: the
	// schedd / meterd accumulators (pkg/sched/instancestats/poller.go
	// for cpu; the vmmd netstats.Cache + gateway statusRecorder fed
	// by the meterd sampler's egress adapters for tx_bytes /
	// net_tx_bytes; the new ingress Tx cache for net_rx_bytes; the
	// new LastWakeMethod propagation for cold_boot_count; the new
	// Manager.ReadAndResetTailSeconds seam for tail_seconds) can
	// each call AppendUsage many times within the same minute. We
	// add EXCLUDED.<col> to the existing row so the columns are the
	// sum of all per-tick deltas. The pusher (meter → billing)
	// deduplicates on a coarser window before pushing, so the
	// additive merge is safe end-to-end.
	//
	//   cpu_usec         — issue #279 / PR-B / ADR-039
	//   tx_bytes         — ADR-046 (gateway HTTP response body bytes)
	//   net_tx_bytes     — ADR-046 (root-side vethHost.rx_bytes delta)
	//   net_rx_bytes     — ADR-048 (root-side vethHost.tx_bytes delta; ingress)
	//   cold_boot_count  — ADR-048 (WAKE_RESTORE→WAKE_COLD_BOOT transitions)
	//   tail_seconds     — issue #667 / ADR-078 (per-minute wall-clock
	//                      seconds draining waitUntil tasks;
	//                      INFORMATIONAL ONLY — does not enter billing;
	//                      pinned by
	//                      pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds)
	_, err := s.pool.Exec(ctx,
		`insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 on conflict (instance_id, minute) do update
		   set cpu_usec        = usage_minutes.cpu_usec        + EXCLUDED.cpu_usec,
		       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
		       net_tx_bytes    = usage_minutes.net_tx_bytes    + EXCLUDED.net_tx_bytes,
		       net_rx_bytes    = usage_minutes.net_rx_bytes    + EXCLUDED.net_rx_bytes,
		       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count,
		       tail_seconds    = usage_minutes.tail_seconds    + EXCLUDED.tail_seconds`,
		accountID, appID, instanceID, minute, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes, coldBootCount, tailSeconds)
	return err
}

// AppendBuilderUsage records one builder-time usage row at build
// completion (ADR-048 §4). Idempotent on (build_id): a redelivered
// meterd / webhook / builderd restart sees ON CONFLICT DO NOTHING
// and the row stays as the first write. The per-build grain lives
// in a separate `builder_usage` table (PK build_id) created by
// migrations/00067_extend_metering_telemetry.sql so it can be rolled
// up into usage_daily.builder_seconds via the meterd rollup cron.
//
// seconds is wall-clock seconds from builds.started_at to
// finishedAt — matches the existing builderd histogram
// (`build_duration_seconds{outcome}`). Caller computes
// finishedAt.Sub(startedAt) before calling; this function does NOT
// re-read the build row (cheap + race-free).
func (s *PgStore) AppendBuilderUsage(ctx context.Context, accountID, appID, buildID string, finishedAt time.Time, kind string, seconds int64) error {
	_, err := s.pool.Exec(ctx,
		`insert into builder_usage (build_id, account_id, app_id, finished_at, kind, seconds)
		 values ($1, $2, $3, $4, $5, $6)
		 on conflict (build_id) do nothing`,
		buildID, accountID, appID, finishedAt, kind, seconds)
	return err
}

func (s *PgStore) UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error) {
	monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	// Compare against a UTC-bucketed range on usage_minutes; the
	// usage_monthly view defines `month = date_trunc('month', minute)`
	// against a timestamptz, which means the view's bucket itself is
	// session-TZ dependent. Querying usage_minutes directly with a
	// UTC-anchored half-open range (same shape as UsageByHour /
	// UsageByAccount) sidesteps the view's TZ shape AND avoids
	// re-introducing `date_trunc('month', ...)` on either side of the
	// comparison. The literal `monthStart` is returned as Usage.Month
	// so the API surface stays byte-stable for callers that format
	// the value. (memory: pkg-state-usage-monthly-tz-compare)
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id,
		        $2::timestamptz as month,
		        sum(mb_seconds)::bigint     as mb_seconds,
		        sum(cpu_usec)::bigint       as cpu_usec,
		        sum(requests)::bigint       as requests,
		        sum(tx_bytes)::bigint       as tx_bytes,
		        sum(net_tx_bytes)::bigint   as net_tx_bytes,
		        sum(net_rx_bytes)::bigint   as net_rx_bytes,
		        sum(cold_boot_count)::bigint as cold_boot_count
		   from usage_minutes
		  where account_id = $1
		    and minute >= $2
		    and minute <  $3
		  group by account_id, app_id
		  order by app_id`,
		accountID, monthStart, monthEnd)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		u := Usage{}
		if err := rows.Scan(&u.AccountID, &u.AppID, &u.Month, &u.MBSeconds, &u.CPUUsec, &u.Requests, &u.TXBytes, &u.NetTxBytes, &u.NetRxBytes, &u.ColdBootCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListInvoicesForAccount returns the account's invoices, newest first,
// ordered by (period_end DESC, id DESC) for deterministic pagination.
// The handler clamps limit (default 25, max 100); the SQL uses the same
// $1..$N split as ListDeploymentsForAccount.
//
// Month filtering (when month != nil) applies a half-open UTC range
// [month, month+1mo) to period_end. Both bounds are pre-computed in
// Go in UTC (monthStart / monthEnd), so the SQL compares timestamptz
// to timestamptz on UTC instants — no `date_trunc('month', ...)` on
// either side. The earlier form `date_trunc('month', $2::timestamptz)`
// bucketed in the SESSION timezone, so on non-UTC Postgres sessions
// the half-open boundary leaked (memory:
// pkg-state-usage-monthly-tz-compare). The fix uses bare
// `period_end >= $2` — same shape as the existing UsageByAccount
// minute-range filter — and a session-static TZ test pins it.
//
// Cursor (before) is strict-less on period_end only. The id tie-break
// is implicit in the unique index ordering; rows sharing the same
// period_end may appear at the page boundary if a customer has multiple
// invoices for the same provider-period. Acceptable for the v1
// surface — added this comment so the next reader does not silently
// "fix" the cursor without introducing a compound id cursor.
func (s *PgStore) ListInvoicesForAccount(ctx context.Context, accountID string, month *time.Time, before time.Time, limit int) ([]Invoice, error) {
	if limit <= 0 {
		limit = 25
	}
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case month != nil && before.IsZero():
		monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd := monthStart.AddDate(0, 1, 0)
		rows, err = s.pool.Query(ctx,
			`select id, account_id, provider, provider_invoice_id, number, status,
			        period_start, period_end,
			        subtotal_cents, tax_cents, total_cents, amount_paid_cents,
			        currency, pdf_available, created_at, updated_at
			   from invoices
			  where account_id = $1
			    and period_end >= $2
			    and period_end <  $3
			  order by period_end desc, id desc
			  limit $4`,
			accountID, monthStart, monthEnd, limit)
	case month != nil && !before.IsZero():
		monthStart := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd := monthStart.AddDate(0, 1, 0)
		rows, err = s.pool.Query(ctx,
			`select id, account_id, provider, provider_invoice_id, number, status,
			        period_start, period_end,
			        subtotal_cents, tax_cents, total_cents, amount_paid_cents,
			        currency, pdf_available, created_at, updated_at
			   from invoices
			  where account_id = $1
			    and period_end >= $2
			    and period_end <  $3
			    and period_end < $4
			  order by period_end desc, id desc
			  limit $5`,
			accountID, monthStart, monthEnd, before, limit)
	case month == nil && !before.IsZero():
		rows, err = s.pool.Query(ctx,
			`select id, account_id, provider, provider_invoice_id, number, status,
			        period_start, period_end,
			        subtotal_cents, tax_cents, total_cents, amount_paid_cents,
			        currency, pdf_available, created_at, updated_at
			   from invoices
			  where account_id = $1
			    and period_end < $2
			  order by period_end desc, id desc
			  limit $3`,
			accountID, before, limit)
	default: // month == nil && before.IsZero()
		rows, err = s.pool.Query(ctx,
			`select id, account_id, provider, provider_invoice_id, number, status,
			        period_start, period_end,
			        subtotal_cents, tax_cents, total_cents, amount_paid_cents,
			        currency, pdf_available, created_at, updated_at
			   from invoices
			  where account_id = $1
			  order by period_end desc, id desc
			  limit $2`,
			accountID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(
			&inv.ID, &inv.AccountID, &inv.Provider, &inv.ProviderInvoiceID,
			&inv.Number, &inv.Status,
			&inv.PeriodStart, &inv.PeriodEnd,
			&inv.SubtotalCents, &inv.TaxCents, &inv.TotalCents, &inv.AmountPaidCents,
			&inv.Currency, &inv.PDFAvailable,
			&inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// GetInvoiceByID resolves a single invoice by primary key. Returns
// ErrNotFound when no row matches (the consumption reducer surfaces
// this to the apid handler as 404 CodeNotFound). Hand-written —
// single-row read against the PK index, no sqlc win. The future
// GET /v1/invoices/{id} single-invoice endpoint will reuse this
// primitive.
func (s *PgStore) GetInvoiceByID(ctx context.Context, id string) (Invoice, error) {
	var inv Invoice
	err := s.pool.QueryRow(ctx,
		`select id, account_id, provider, provider_invoice_id, number, status,
		        period_start, period_end,
		        subtotal_cents, tax_cents, total_cents, amount_paid_cents,
		        currency, pdf_available, created_at, updated_at
		   from invoices
		  where id = $1`,
		id).Scan(
		&inv.ID, &inv.AccountID, &inv.Provider, &inv.ProviderInvoiceID,
		&inv.Number, &inv.Status,
		&inv.PeriodStart, &inv.PeriodEnd,
		&inv.SubtotalCents, &inv.TaxCents, &inv.TotalCents, &inv.AmountPaidCents,
		&inv.Currency, &inv.PDFAvailable,
		&inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrNotFound
		}
		return Invoice{}, err
	}
	return inv, nil
}

// UpsertInvoice stores the provider projection used by invoice history. A
// Polar order can arrive as pending and later as paid, so updates replace the
// mutable invoice fields while preserving the original created_at timestamp.
func (s *PgStore) UpsertInvoice(ctx context.Context, inv Invoice) error {
	if inv.Provider == "" || inv.ProviderInvoiceID == "" || inv.AccountID == "" {
		return errors.New("state: invoice account, provider, and provider_invoice_id are required")
	}
	if inv.PeriodStart.IsZero() {
		inv.PeriodStart = time.Now().UTC()
	}
	if inv.PeriodEnd.IsZero() {
		inv.PeriodEnd = inv.PeriodStart
	}
	if inv.Currency == "" {
		inv.Currency = "eur"
	}
	if inv.Status == "" {
		inv.Status = "open"
	}
	_, err := s.pool.Exec(ctx,
		`insert into invoices (
			account_id, provider, provider_invoice_id, number, status,
			period_start, period_end, subtotal_cents, tax_cents, total_cents,
			amount_paid_cents, currency, pdf_available, updated_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		on conflict (account_id, provider, provider_invoice_id) do update set
			number = excluded.number,
			status = excluded.status,
			period_start = excluded.period_start,
			period_end = excluded.period_end,
			subtotal_cents = excluded.subtotal_cents,
			tax_cents = excluded.tax_cents,
			total_cents = excluded.total_cents,
			amount_paid_cents = excluded.amount_paid_cents,
			currency = excluded.currency,
			pdf_available = excluded.pdf_available,
			updated_at = now()`,
		inv.AccountID, inv.Provider, inv.ProviderInvoiceID, inv.Number, inv.Status,
		inv.PeriodStart.UTC(), inv.PeriodEnd.UTC(), inv.SubtotalCents, inv.TaxCents,
		inv.TotalCents, inv.AmountPaidCents, strings.ToLower(inv.Currency), inv.PDFAvailable)
	return err
}

// --- account credits (issue #279) -------------------------------------------

// CreateAccountCredit inserts a new operator-issued credit. The DB
// generates id (UUIDv4) and created_at (default now()) via the table
// defaults; RETURNING surfaces them so the handler can echo the row
// back without a second round-trip. The migration 00049 CHECK
// constraint on cents_remaining >= 0 and reason length is enforced
// at the DB; the handler validates client-side for a friendlier 400.
//
// NOT transactional with the matching credit_ledger row — the ledger
// insert is a separate statement in the handler. If the ledger write
// fails after the credit row lands, the credit is the source of truth
// and the ledger insert is retryable on the next operator action. The
// handler logs a WARN when the ledger write fails. Mirrors the
// documented behaviour of DeleteAPIKeyReturning (audit-loss trade-off,
// store.go DeleteAPIKeyReturning doc).
func (s *PgStore) CreateAccountCredit(ctx context.Context, c AccountCredit) (AccountCredit, error) {
	row := s.pool.QueryRow(ctx,
		`insert into account_credits (account_id, cents_remaining, reason, expires_at)
		 values ($1, $2, $3, $4)
		 returning id, account_id, cents_remaining, reason, created_at, expires_at`,
		c.AccountID, c.CentsRemaining, c.Reason, c.ExpiresAt)
	var out AccountCredit
	if err := row.Scan(
		&out.ID, &out.AccountID, &out.CentsRemaining, &out.Reason,
		&out.CreatedAt, &out.ExpiresAt,
	); err != nil {
		return AccountCredit{}, err
	}
	return out, nil
}

// ListAccountCredits returns the account's credit rows. onlyActive
// filters to (cents_remaining > 0) ∧ (expires_at IS NULL OR expires_at
// > now()), the active set the consumption reducer will use once it
// lands. Order is created_at DESC for deterministic test assertions.
// limit is the caller's responsibility; the SQL uses a single LIMIT
// when the caller has set one (the handler clamps at 100).
func (s *PgStore) ListAccountCredits(ctx context.Context, accountID string, onlyActive bool) ([]AccountCredit, error) {
	now := time.Now().UTC()
	var rows pgx.Rows
	var err error
	if onlyActive {
		rows, err = s.pool.Query(ctx,
			`select id, account_id, cents_remaining, reason, created_at, expires_at
			   from account_credits
			  where account_id = $1
			    and cents_remaining > 0
			    and (expires_at is null or expires_at > $2)
			  order by created_at desc`,
			accountID, now)
	} else {
		rows, err = s.pool.Query(ctx,
			`select id, account_id, cents_remaining, reason, created_at, expires_at
			   from account_credits
			  where account_id = $1
			  order by created_at desc`,
			accountID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountCredit{}
	for rows.Next() {
		var c AccountCredit
		if err := rows.Scan(
			&c.ID, &c.AccountID, &c.CentsRemaining, &c.Reason,
			&c.CreatedAt, &c.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCreditLedgerEntry appends one audit row. The DB generates id
// (UUIDv4) and created_at (default now()) via the table defaults; the
// actor and reason are operator-supplied (the handler pulls actor from
// the authenticated caller account ID). Migration 00049's CHECK
// constraint on delta_cents <> 0 ensures issuance (+N) and consumption
// (-N) are both accepted but a zero-delta row is rejected.
//
// provider_invoice_id (migration 00058) is NULL on issuance rows
// (today's only caller, cmd/apid/handlers_admin_credits.go::issueCredit);
// the consumption reducer (issue #279 PR-C) sets it on consumption
// rows and the partial unique index
// credit_ledger_invoice_credit_idx(provider_invoice_id, credit_id) WHERE
// provider_invoice_id IS NOT NULL is the dedupe story for webhook
// redelivery and admin endpoint replay.
func (s *PgStore) CreateCreditLedgerEntry(ctx context.Context, e CreditLedgerEntry) error {
	_, err := s.pool.Exec(ctx,
		`insert into credit_ledger (account_id, credit_id, delta_cents, reason, actor, provider_invoice_id)
		 values ($1, $2, $3, $4, $5, $6)`,
		e.AccountID, e.CreditID, e.DeltaCents, e.Reason, e.Actor, e.ProviderInvoiceID)
	return err
}

// GetAccountOverageCapCents returns (cents, ok, nil). ok=false means
// the column is NULL (no cap configured). 0 with ok=true means "no
// overage allowed"; >0 is the explicit monthly ceiling. meterd calls
// this once per account per quota tick.
//
// Hand-written against the column added by migration 00049 — NOT
// sqlc-generated, mirroring the ListInvoicesForAccount pattern.
// Single-row read with no parameters other than the account id; sqlc
// would not add observability here.
func (s *PgStore) GetAccountOverageCapCents(ctx context.Context, accountID string) (int64, bool, error) {
	var cents *int64
	if err := s.pool.QueryRow(ctx,
		`select overage_cap_cents from accounts where id = $1`,
		accountID).Scan(&cents); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Account row gone — treat as no cap so meterd does not
			// block on a half-deleted account. Different from
			// ErrNotFound semantics: the caller is meterd, not a
			// user-facing handler.
			return 0, false, nil
		}
		return 0, false, err
	}
	if cents == nil {
		return 0, false, nil
	}
	return *cents, true, nil
}

// UpdateAccountOverageCapCents writes accounts.overage_cap_cents for
// the given account. Pass nil → SQL NULL (no cap). Pass *non-nil →
// the integer cents; 0 is preserved as "no overage allowed" and
// distinguished from NULL by the reader's (cents, ok) return shape.
//
// Hand-written UPDATE (no sqlc) — mirrors the GetAccountOverageCapCents
// pattern at line 7356 and the UpdateAccountPlan sibling at line 302.
// Issue #561's raiseOverageCap endpoint calls this for the customer
// self-service "raise / clear cap" surface. The migration CHECK at
// accounts/00049 pins `overage_cap_cents IS NULL OR overage_cap_cents
// >= 0`; this layer does not re-validate. pgx maps a *int64 nil to
// SQL NULL via its standard type codec; an empty-string or sentinel
// compare is unnecessary.
func (s *PgStore) UpdateAccountOverageCapCents(ctx context.Context, accountID string, cents *int64) error {
	_, err := s.pool.Exec(ctx,
		`update accounts set overage_cap_cents = $2 where id = $1`,
		accountID, cents)
	return err
}

// ListActiveCreditsForConsumption returns the account's active credit
// rows ordered FIFO (created_at ASC) for the consumption reducer
// (issue #279 PR-C). Mirrors the (cents_remaining > 0) ∧ (expires_at
// IS NULL OR expires_at > now()) active-set predicate of
// ListAccountCredits(onlyActive=true) but sorts ASC because the
// reducer drains oldest credit first. Backed by the same partial
// index account_credits_account_active_idx that the issuance
// surface's reducer eventually needs; FOR UPDATE locks the rows so
// the same transaction's conditional UPDATEs cannot race with a
// concurrent operator issuance.
//
// Hand-written (not sqlc) — single-statement read with one parameter
// plus the now() anchor; sqlc would not add observability here.
func (s *PgStore) ListActiveCreditsForConsumption(ctx context.Context, accountID string) ([]AccountCredit, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, cents_remaining, reason, created_at, expires_at
		   from account_credits
		  where account_id = $1
		    and cents_remaining > 0
		    and (expires_at is null or expires_at > now())
		  order by created_at asc
		  for update`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountCredit{}
	for rows.Next() {
		var c AccountCredit
		if err := rows.Scan(
			&c.ID, &c.AccountID, &c.CentsRemaining, &c.Reason,
			&c.CreatedAt, &c.ExpiresAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConsumeAccountCredit performs an atomic FIFO decrement across the
// account's active credits, capped at TargetCents. The whole loop runs
// inside a single transaction so the conditional UPDATE / INSERT pair
// per credit is atomic against a concurrent operator issuance on a
// different credit.
//
// Idempotency is the partial unique index credit_ledger_invoice_credit_idx
// (provider_invoice_id, credit_id) WHERE provider_invoice_id IS NOT NULL
// (migration 00058). The INSERT uses ON CONFLICT DO NOTHING so a second
// call for the same invoice sees zero rows returned for every (invoice,
// credit) pair and reports AlreadyConsumedForInvoice=true.
//
// Concurrent-operator race window: the prior_check reads
// credit_ledger for the invoice's existing consumption rows; without
// a lock, two operators firing the endpoint simultaneously could
// both observe hasPrior=false and both drain. The end state would
// be correct (no double-decrement — the per-pair partial unique index
// catches it), but PerCredit and AlreadyConsumedForInvoice would
// differ between the two callers. Closing the window: at the top of
// the Tx we take a SHARE lock on every existing credit_ledger row
// for this provider_invoice_id. A concurrent INSERT ... ON CONFLICT
// DO NOTHING in another Tx blocks until we commit, so the second
// operator's prior_check reads our committed rows and reports
// AlreadyConsumedForInvoice=true with the SAME ConsumedCents.
//
// Per credit:
//  1. amount = min(credit.CentsRemaining, remaining).
//  2. UPDATE account_credits SET cents_remaining = cents_remaining - $amount
//     WHERE id = $id AND cents_remaining >= $amount RETURNING
//     cents_remaining. Zero rows ⇒ the credit was drained concurrently
//     (issuance took it to 0 or a parallel reducer won) — skip the
//     INSERT and try the next credit.
//  3. INSERT INTO credit_ledger (... provider_invoice_id) ... ON
//     CONFLICT DO NOTHING RETURNING id. Zero rows ⇒ the (invoice,
//     credit) pair was already drained on a prior call — set
//     AlreadyConsumedForInvoice=true and skip.
//  4. remaining -= amount. Break when 0.
//
// Final ConsumedCents = TargetCents - remaining. If the loop ended
// because every (invoice, credit) pair was already drained (no UPDATE
// succeeded AND no INSERT landed), the reducer re-derives ConsumedCents
// from the existing ledger rows so the operator sees the same total
// regardless of which call they inspect.
//
// "Atomic" here means: for each credit, the conditional UPDATE cannot
// return a row that would have driven cents_remaining negative — the
// migration's CHECK (cents_remaining >= 0) is the floor.
//
// Hand-written (not sqlc) — multi-statement transaction with
// dynamic per-credit bounds; sqlc would not add observability here.
func (s *PgStore) ConsumeAccountCredit(ctx context.Context, p ConsumeAccountCreditParams) (ConsumeAccountCreditResult, error) {
	if p.TargetCents == 0 {
		return ConsumeAccountCreditResult{}, nil
	}
	if p.ProviderInvoiceID == "" {
		return ConsumeAccountCreditResult{}, fmt.Errorf("ConsumeAccountCredit: ProviderInvoiceID required (the partial unique index needs a non-null dedupe key)")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ConsumeAccountCreditResult{}, fmt.Errorf("state: consume_credits tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	// Race-safe idempotency: lock + check existing consumption
	// rows for this invoice. See priorLockAndCheck docstring.
	hasPrior, priorCents, err := priorLockAndCheck(ctx, tx, p.ProviderInvoiceID)
	if err != nil {
		return ConsumeAccountCreditResult{}, err
	}
	if hasPrior {
		remSum, err := sumActiveCents(ctx, tx, p.AccountID)
		if err != nil {
			return ConsumeAccountCreditResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ConsumeAccountCreditResult{}, fmt.Errorf("state: consume_credits prior_commit: %w", err)
		}
		return ConsumeAccountCreditResult{
			ConsumedCents:             priorCents,
			RemainingCreditsCents:     remSum,
			AlreadyConsumedForInvoice: true,
		}, nil
	}

	active, err := loadActiveForUpdate(ctx, tx, p.AccountID)
	if err != nil {
		return ConsumeAccountCreditResult{}, err
	}

	res, anyInserted, err := drainActive(ctx, tx, active, p)
	if err != nil {
		return ConsumeAccountCreditResult{}, err
	}
	if !anyInserted && p.TargetCents > 0 {
		// Re-derive ConsumedCents from existing ledger rows so the
		// operator sees the same total across calls. The partial
		// unique index guarantees there is exactly one ledger row
		// per (invoice, credit) pair.
		rederived, derr := rederiveConsumed(ctx, tx, p.ProviderInvoiceID)
		if derr != nil {
			return ConsumeAccountCreditResult{}, derr
		}
		res.ConsumedCents = rederived
	}

	remSum, err := sumActiveCents(ctx, tx, p.AccountID)
	if err != nil {
		return ConsumeAccountCreditResult{}, err
	}
	res.RemainingCreditsCents = remSum

	if err := tx.Commit(ctx); err != nil {
		return ConsumeAccountCreditResult{}, fmt.Errorf("state: consume_credits commit: %w", err)
	}
	return res, nil
}

// priorLockAndCheck takes a SHARE lock on every existing credit_ledger
// row for the provider_invoice_id and reports whether prior
// consumption rows exist. The SHARE lock conflicts with INSERT (which
// takes ROW EXCLUSIVE) so a concurrent operator call blocks here
// until our Tx commits. The second operator's prior_check then reads
// our committed rows and reports AlreadyConsumedForInvoice=true with
// the SAME ConsumedCents. Without the lock, two simultaneous
// operators can both observe hasPrior=false and both proceed to
// drain — the end state is still correct (no double-decrement via
// the per-pair partial unique index), but PerCredit and
// AlreadyConsumedForInvoice differ between the two callers.
//
// The FOR SHARE on a zero-row set is a no-op — Postgres acquires a
// table-level intention lock and proceeds. When another Tx already
// holds the SHARE lock, INSERT ... ON CONFLICT DO NOTHING blocks at
// lock acquisition (before uniqueness evaluation), so the hasPrior
// read below reflects committed state.
//
// Returns (hasPrior, priorCents, err). Caller commits/rolls back.
func priorLockAndCheck(ctx context.Context, tx pgx.Tx, providerInvoiceID string) (bool, int64, error) {
	if _, err := tx.Exec(ctx,
		`select 1
		   from credit_ledger
		  where provider_invoice_id = $1
		  for share`,
		providerInvoiceID); err != nil {
		return false, 0, fmt.Errorf("state: consume_credits prior_lock: %w", err)
	}
	var priorCents int64
	var hasPrior bool
	if err := tx.QueryRow(ctx,
		`select coalesce(sum(-delta_cents), 0), coalesce(bool_or(delta_cents < 0), false)
		   from credit_ledger
		  where provider_invoice_id = $1`,
		providerInvoiceID).Scan(&priorCents, &hasPrior); err != nil {
		return false, 0, fmt.Errorf("state: consume_credits prior_check: %w", err)
	}
	return hasPrior, priorCents, nil
}

// loadActiveForUpdate returns the account's FIFO-locked active
// credits (created_at ASC) inside the Tx. Mirrors
// ListActiveCreditsForConsumption's predicate and sort; the FOR UPDATE
// locks the rows so the conditional UPDATEs in drainActive cannot
// race with a concurrent operator issuance.
func loadActiveForUpdate(ctx context.Context, tx pgx.Tx, accountID string) ([]AccountCredit, error) {
	rows, err := tx.Query(ctx,
		`select id, account_id, cents_remaining, reason, created_at, expires_at
		   from account_credits
		  where account_id = $1
		    and cents_remaining > 0
		    and (expires_at is null or expires_at > now())
		  order by created_at asc
		  for update`,
		accountID)
	if err != nil {
		return nil, fmt.Errorf("state: consume_credits list: %w", err)
	}
	defer rows.Close()
	out := []AccountCredit{}
	for rows.Next() {
		var c AccountCredit
		if err := rows.Scan(
			&c.ID, &c.AccountID, &c.CentsRemaining, &c.Reason,
			&c.CreatedAt, &c.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("state: consume_credits scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: consume_credits list_iter: %w", err)
	}
	return out, nil
}

// drainActive runs the per-credit conditional UPDATE + INSERT ON
// CONFLICT DO NOTHING loop, capped at p.TargetCents. Returns the
// per-credit rows plus the total drained and whether any row was
// successfully inserted (the latter distinguishes a fresh drain from
// the all-already-consumed path that triggers rederiveConsumed).
//
// Per credit:
//  1. amount = min(credit.CentsRemaining, remaining).
//  2. UPDATE … WHERE cents_remaining >= $amount RETURNING …
//     Zero rows ⇒ concurrent drain won; skip and try the next.
//  3. INSERT … ON CONFLICT DO NOTHING RETURNING id.
//     Zero rows ⇒ (invoice, credit) pair was already drained on a
//     prior call; mark AlreadyConsumedForInvoice=true and skip.
//  4. remaining -= amount. Break when 0.
//
// Returns (res, anyInserted, err). Errors abort the loop and surface
// to the caller; AlreadyConsumedForInvoice=true in res is the
// partial-success path that lets the caller trigger rederiveConsumed.
func drainActive(ctx context.Context, tx pgx.Tx, active []AccountCredit, p ConsumeAccountCreditParams) (ConsumeAccountCreditResult, bool, error) {
	res := ConsumeAccountCreditResult{}
	remaining := p.TargetCents
	anyInserted := false
	for i := range active {
		if remaining == 0 {
			break
		}
		c := active[i]
		amount := c.CentsRemaining
		if amount > remaining {
			amount = remaining
		}
		if amount <= 0 {
			continue
		}

		// Step 2: conditional decrement.
		var newBalance int64
		err := tx.QueryRow(ctx,
			`update account_credits
			    set cents_remaining = cents_remaining - $1
			  where id = $2
			    and cents_remaining >= $1
			  returning cents_remaining`,
			amount, c.ID).Scan(&newBalance)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Concurrent drain won this credit — skip and try
				// the next. The transaction stays valid.
				continue
			}
			return res, anyInserted, fmt.Errorf("state: consume_credits update: %w", err)
		}

		// Step 3: ledger insert with ON CONFLICT DO NOTHING.
		//
		// Postgres requires ON CONFLICT inference to use a unique
		// index whose column list AND WHERE clause match the
		// conflict target. The partial unique index
		// credit_ledger_invoice_credit_idx carries `WHERE
		// provider_invoice_id IS NOT NULL` (migration 00058), so
		// the inference clause must repeat it — without the WHERE,
		// Postgres errors with SQLSTATE 42P10 "there is no unique
		// or exclusion constraint matching the ON CONFLICT
		// specification".
		var insertedID string
		err = tx.QueryRow(ctx,
			`insert into credit_ledger
			   (account_id, credit_id, delta_cents, reason, actor, provider_invoice_id)
			 values ($1, $2, $3, $4, $5, $6)
			 on conflict (provider_invoice_id, credit_id)
			   where provider_invoice_id is not null
			   do nothing
			 returning id`,
			p.AccountID, c.ID, -amount, p.Reason, p.Actor, p.ProviderInvoiceID,
		).Scan(&insertedID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT DO NOTHING returned no rows — the
				// (invoice, credit) pair was already drained. Mark
				// the no-op state and skip.
				res.AlreadyConsumedForInvoice = true
				continue
			}
			return res, anyInserted, fmt.Errorf("state: consume_credits ledger: %w", err)
		}
		_ = insertedID // observational; consumed via RETURNING id

		res.PerCredit = append(res.PerCredit, ConsumedCreditRow{
			CreditID:   c.ID,
			DeltaCents: -amount,
			NewBalance: newBalance,
		})
		res.ConsumedCents += amount
		remaining -= amount
		anyInserted = true
	}
	return res, anyInserted, nil
}

// sumActiveCents returns the sum of cents_remaining across the
// account's active (non-zero, non-expired) credits. Used for
// RemainingCreditsCents in the response. Caller-supplied Tx.
func sumActiveCents(ctx context.Context, tx pgx.Tx, accountID string) (int64, error) {
	var remSum int64
	if err := tx.QueryRow(ctx,
		`select coalesce(sum(cents_remaining), 0)
		   from account_credits
		  where account_id = $1
		    and cents_remaining > 0
		    and (expires_at is null or expires_at > now())`,
		accountID).Scan(&remSum); err != nil {
		return 0, fmt.Errorf("state: consume_credits remaining: %w", err)
	}
	return remSum, nil
}

// rederiveConsumed sums the negative-delta ledger rows for the
// invoice (the partial unique index guarantees exactly one row per
// (invoice, credit) pair). Used when drainActive inserted nothing —
// the operator's replay path.
func rederiveConsumed(ctx context.Context, tx pgx.Tx, providerInvoiceID string) (int64, error) {
	var rederived int64
	if err := tx.QueryRow(ctx,
		`select coalesce(sum(-delta_cents), 0)
		   from credit_ledger
		  where provider_invoice_id = $1
		    and delta_cents < 0`,
		providerInvoiceID).Scan(&rederived); err != nil {
		return 0, fmt.Errorf("state: consume_credits rederive: %w", err)
	}
	return rederived, nil
}

// LoadAllOverageCapCents returns every (account_id, cap) tuple in one
// round-trip. meterd's quota tick walks all accounts every minute and
// would otherwise issue N single-row reads; the bulk read keeps the
// per-tick cost at one round-trip. Drops accounts whose overage_cap_cents
// is NULL — the caller treats them as "no cap".
//
// Hand-written for the same reason as GetAccountOverageCapCents; the
// shape is meterd-internal and not on the sqlc public surface.
func (s *PgStore) LoadAllOverageCapCents(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx,
		`select id, overage_cap_cents from accounts where overage_cap_cents is not null`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var cents int64
		if err := rows.Scan(&id, &cents); err != nil {
			return nil, err
		}
		out[id] = cents
	}
	return out, rows.Err()
}

// CurrentMonthOverageCents returns the account's derived overage in
// integer cents for the current UTC month. It removes the account plan's
// included calendar-month allowance before converting the remainder to
// cents. Integer math only — never float on money (CLAUDE.md).
//
// Hand-written: the formula is meterd-internal and not on the read
// surface. The migration on usage_minutes is unchanged; we SELECT
// against the existing table.
func (s *PgStore) CurrentMonthOverageCents(ctx context.Context, accountID string) (int64, error) {
	// Anchor the month lower bound in UTC on the Go side. The previous
	// shape `minute >= date_trunc('month', now())` returned the local
	// month's start in the session timezone (a timestamptz), so on a
	// non-UTC Postgres session the bound was a different UTC instant
	// from the start of the UTC calendar month — usage_minutes rows
	// bucketed into the first UTC hour of the month were skipped.
	// Matches the project convention (memory:
	// pkg-state-usage-monthly-tz-compare) used elsewhere in this file.
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var mbSeconds int64
	var plan api.Plan
	if err := s.pool.QueryRow(ctx,
		`select COALESCE(SUM(u.mb_seconds), 0)::bigint,
		        COALESCE((select a.plan from accounts a where a.id = $1), 'free')
		   from usage_minutes u
		  where u.account_id = $1
		    and u.minute >= $2`,
		accountID, monthStart).Scan(&mbSeconds, &plan); err != nil {
		return 0, err
	}
	return api.OverageCentsForMBSeconds(plan, mbSeconds), nil
}

// UsageByHour returns per-app usage rolled up from the per-minute rows
// in the [start, end) window. The Stripe pusher calls this hourly;
// (start, end) is the [now-1h, now) hour window so the SQL is an
// indexed range scan on usage_minutes.minute. cpu_usec is selected too
// (issue #279 / PR-B) so the per-hour rollup exposes the additive CPU
// accumulation; the Stripe pusher still pushes only mb_seconds-based
// usage (billing stays on plan RAM) but the column is available for
// future per-hour dashboards without re-rolling per-minute rows.
//
// tx_bytes and net_tx_bytes (ADR-046) are summed in the same query
// so the per-hour rollup exposes both contributions. The asymmetry
// the same as cpu_usec: additive on (instance_id, minute) conflict
// so the SUM over the hour window is the full amount. Future
// per-hour egress dashboards feed off this without a re-roll.
func (s *PgStore) UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]Usage, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id,
		        date_trunc('hour', minute AT TIME ZONE 'UTC') as hour,
		        sum(mb_seconds)::bigint      as mb_seconds,
		        sum(cpu_usec)::bigint        as cpu_usec,
		        sum(requests)::bigint        as requests,
		        sum(tx_bytes)::bigint        as tx_bytes,
		        sum(net_tx_bytes)::bigint    as net_tx_bytes,
		        sum(net_rx_bytes)::bigint    as net_rx_bytes,
		        sum(cold_boot_count)::bigint as cold_boot_count
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
		if err := rows.Scan(&u.AccountID, &u.AppID, &hour, &u.MBSeconds, &u.CPUUsec, &u.Requests, &u.TXBytes, &u.NetTxBytes, &u.NetRxBytes, &u.ColdBootCount); err != nil {
			return nil, err
		}
		u.Month = hour
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageWindows returns the account-level, per-UTC-hour billable aggregates
// used by meterd's retry/backfill pass. Keeping the aggregation in Postgres
// means a meterd restart does not lose an hour merely because its tick was
// missed; the provider-side dedupe tables make replays safe.
func (s *PgStore) UsageWindows(ctx context.Context, start, end time.Time) ([]UsageWindow, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id,
		        date_trunc('hour', minute AT TIME ZONE 'UTC') as hour,
		        sum(mb_seconds)::bigint as mb_seconds
		   from usage_minutes
		  where minute >= $1 and minute < $2
		  group by account_id, hour
		 having sum(mb_seconds) > 0
		  order by hour, account_id`,
		start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageWindow
	for rows.Next() {
		var w UsageWindow
		if err := rows.Scan(&w.AccountID, &w.Hour, &w.MBSeconds); err != nil {
			return nil, err
		}
		w.Hour = w.Hour.UTC()
		out = append(out, w)
	}
	return out, rows.Err()
}

// UsageDaily returns the per-(account, app, day) rollup rows that the
// meterd rollup loop (pkg/meter/rollup.go) populated into the
// usage_daily table (ADR-048 §5, migration 00067; tail_seconds
// added by issue #667 / ADR-078 / migration 00151). day is a UTC
// midnight timestamp; only the date portion is used in the predicate
// so a caller that already normalised to midnight does not have to
// truncate again.
//
// Empty result means either: (a) the requested day is in the future
// and no rows exist yet, or (b) the rollup loop has not yet fired
// since the rows were sampled into usage_minutes. Callers should NOT
// fall back to UsageByHour here — the dashboard wants the rollup row
// specifically so the cron cadence is observable.
func (s *PgStore) UsageDaily(ctx context.Context, accountID string, day time.Time) ([]DailyUsage, error) {
	rows, err := s.pool.Query(ctx,
		`select app_id, day, mb_seconds, requests, cpu_usec,
		        tx_bytes, net_tx_bytes, net_rx_bytes,
		        cold_boot_count, builder_seconds, tail_seconds
		   from usage_daily
		  where account_id = $1
		    and day = ($2::timestamptz AT TIME ZONE 'UTC')::date
		  order by app_id`,
		accountID, day.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyUsage
	for rows.Next() {
		u := DailyUsage{AccountID: accountID}
		if err := rows.Scan(&u.AppID, &u.Day, &u.MBSeconds, &u.Requests, &u.CPUUsec, &u.TXBytes, &u.NetTxBytes, &u.NetRxBytes, &u.ColdBootCount, &u.BuilderSeconds, &u.TailSeconds); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageDailyForAccount returns the trailing 30 UTC calendar days of the
// materialised daily usage rollup. The UTC conversion is explicit rather than
// relying on the Postgres session timezone so a customer in a non-UTC session
// never loses the boundary day. ADR-048 / issue #308.
func (s *PgStore) UsageDailyForAccount(ctx context.Context, accountID string) ([]DailyUsage, error) {
	rows, err := s.pool.Query(ctx,
		`select app_id, day, mb_seconds, requests, cpu_usec,
		        tx_bytes, net_tx_bytes, net_rx_bytes,
		        cold_boot_count, builder_seconds, tail_seconds
		   from usage_daily
		  where account_id = $1
		    and day >= ((now() at time zone 'UTC')::date - 29)
		    and day <=  (now() at time zone 'UTC')::date
		  order by day, app_id`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyUsage
	for rows.Next() {
		u := DailyUsage{AccountID: accountID}
		if err := rows.Scan(&u.AppID, &u.Day, &u.MBSeconds, &u.Requests, &u.CPUUsec, &u.TXBytes, &u.NetTxBytes, &u.NetRxBytes, &u.ColdBootCount, &u.BuilderSeconds, &u.TailSeconds); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageSLOForApp returns the customer-facing SLO rollup
// (instance_hours, gb_hours) for one app over the half-open
// UTC range [start, end). Powers GET /v1/apps/{slug}/slo
// (issue #696 / ADR-082).
//
// Defensive cross-account: the SQL filters on BOTH
// `m.app_id = $1` AND `a.account_id = $2`. The handler
// passes the account_id read from the session token
// (cmd/apid/handlers_slo.go:78) so any future caller that
// bypasses loadApp — an operator-side SLO aggregate, an
// internal cron, a refactor that passes a fetched app_id
// from a different account's context — cannot leak the
// other account's instance_hours / gb_hours. The JOIN to
// apps is a schema-anchored existence check (NOT NULL FK
// on usage_minutes.app_id guarantees the row resolves).
//
// Math:
//   - instance_hours = COUNT(*) / 60
//     usage_minutes is one row per (instance_id, minute).
//     Each row represents one minute of "the instance was
//     alive and accounted for in meterd's sampler". So
//     COUNT(*) over the window is instance-minutes, and
//     / 60 converts to instance-hours.
//   - gb_hours = SUM(mb_seconds) / 3600 / 1024
//     mb_seconds is the per-minute ram_mb × 60 snapshot
//     (the actual RAM.billable_seconds counter, per the
//     schema comment on usage_minutes.mb_seconds). Sum
//     across the window is mb-seconds; / 3600 = mb-hours;
//     / 1024 = GB-hours. Matches the meterd pusher's
//     Math.GBHours shape (pkg/meter/pusher.go). The
//     per-instance × per-minute sampling granularity
//     matters here — a Scale customer running 20
//     instances sees the same number as 20 instances × 1
//     minute, not 1 instance × 20 minutes.
//
// Returns (0, 0, nil) when no rows fall in the window
// (cold account, no usage yet, or the window is entirely
// in the future). The handler treats that as "empty
// SLO panel" — the partial-population degraded path
// when the PromQL also failed is the one that surfaces
// "0" with a "degraded:" source.
//
// builder_seconds / builder_kind are intentionally NOT
// counted here — runtime GB-RAM-hour billing is
// unchanged by the SLO surface. The meterd pusher
// (pkg/meter/pusher.go::Math.GBHours) also excludes
// them, so the SLO number and the billed number stay
// consistent.
func (s *PgStore) UsageSLOForApp(ctx context.Context, appID, accountID string, start, end time.Time) (float64, float64, error) {
	var instanceHours, gbHours float64
	err := s.pool.QueryRow(ctx,
		`select coalesce(count(*)::float8 / 60.0, 0),
		        coalesce(sum(mb_seconds)::float8 / 3600.0 / 1024.0, 0)
		   from usage_minutes m
		   join apps a on a.id = m.app_id
		  where m.app_id = $1
		    and a.account_id = $2
		    and m.minute >= $3
		    and m.minute <  $4`,
		appID, accountID, start.UTC(), end.UTC()).Scan(&instanceHours, &gbHours)
	if err != nil {
		return 0, 0, err
	}
	return instanceHours, gbHours, nil
}

// UsageSLOForAccount is the account-wide SLO rollup of
// UsageSLOForApp. Powers GET /v1/account/slo. Same math,
// broader scope. The account_id is in the WHERE clause
// (no handler-side cross-account leak; the pgstore is the
// only place the rollup touches the table).
func (s *PgStore) UsageSLOForAccount(ctx context.Context, accountID string, start, end time.Time) (float64, float64, error) {
	var instanceHours, gbHours float64
	err := s.pool.QueryRow(ctx,
		`select coalesce(count(*)::float8 / 60.0, 0),
		        coalesce(sum(mb_seconds)::float8 / 3600.0 / 1024.0, 0)
		   from usage_minutes m
		   join apps a on a.id = m.app_id
		  where a.account_id = $1
		    and m.minute >= $2
		    and m.minute <  $3`,
		accountID, start.UTC(), end.UTC()).Scan(&instanceHours, &gbHours)
	if err != nil {
		return 0, 0, err
	}
	return instanceHours, gbHours, nil
}

// AppendSnapshotStorage upserts one snapshot_storage_daily row.
// NOT additive merge: the storage rollup is a point-in-time snapshot
// of the current snapshot+layer bytes (pkg/meter/storage.go computes
// the cumulative total for the day, then writes). Re-running for the
// same day overwrites the existing row. ADR-049 §B.3.
func (s *PgStore) AppendSnapshotStorage(ctx context.Context, accountID, appID string, day time.Time, snapshotBytes, layerBytes int64) error {
	_, err := s.pool.Exec(ctx,
		`insert into snapshot_storage_daily
		    (account_id, app_id, day, snapshot_bytes, layer_bytes, computed_at)
		 values ($1, $2, $3::timestamptz, $4, $5, now())
		 on conflict (account_id, app_id, day) do update set
		    snapshot_bytes = excluded.snapshot_bytes,
		    layer_bytes    = excluded.layer_bytes,
		    computed_at    = excluded.computed_at`,
		accountID, appID, day.UTC(), snapshotBytes, layerBytes)
	return err
}

// LatestSnapshotBytes returns mem_bytes + disk_bytes for the app's
// latest non-stale snapshot under the currently-live deployment.
// We join deployments to filter to status='live' (the only state
// that can serve wake — `pending`/`building`/`imaging`/`failed`/
// `superseded` rows must not be billed for storage) and look up
// the most recently created non-stale snapshot row. The
// `snapshots_live_idx` partial index (migration 00071) on
// (deployment_id) WHERE stale=false makes the inner lookup a
// bounded Index Scan instead of a per-app heap scan. Returns
// (0, 0, nil) when the app has no live deployment yet — a cold
// start, not an error. ADR-049 §B.3.
func (s *PgStore) LatestSnapshotBytes(ctx context.Context, appID string) (int64, int64, error) {
	var memBytes, diskBytes int64
	err := s.pool.QueryRow(ctx,
		`select coalesce(s.mem_bytes, 0), coalesce(s.disk_bytes, 0)
		   from snapshots s
		   join deployments d on s.deployment_id = d.id
		  where d.app_id = $1
		    and d.status  = 'live'
		    and s.stale = false
		  order by s.created_at desc
		  limit 1`,
		appID).Scan(&memBytes, &diskBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return memBytes, diskBytes, nil
}

// StorageUsage returns the per-(account, app, day) storage rollup
// rows. day is a UTC midnight timestamp; only the date portion is
// used in the predicate (mirrors UsageDaily's AT TIME ZONE 'UTC'
// conversion). ADR-049 §B.3.
func (s *PgStore) StorageUsage(ctx context.Context, accountID string, day time.Time) ([]StorageUsage, error) {
	rows, err := s.pool.Query(ctx,
		`select app_id, day, snapshot_bytes, layer_bytes
		   from snapshot_storage_daily
		  where account_id = $1
		    and day = ($2::timestamptz AT TIME ZONE 'UTC')::date
		  order by app_id`,
		accountID, day.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StorageUsage
	for rows.Next() {
		u := StorageUsage{AccountID: accountID}
		if err := rows.Scan(&u.AppID, &u.Day, &u.SnapshotBytes, &u.LayerBytes); err != nil {
			return nil, err
		}
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

// HasPaddleOverageMonth is the legacy month-scoped dedupe gate kept on
// the state.Store interface for back-compat with PR #179 callers.
// Migration 00039 replaced the month-keyed PK with a per-window PK
// `(account_id, window_start)`; the legacy pair therefore now keys on
// `window_start` and accepts any hour-aligned timestamp (the
// calendar-month start at 00:00 UTC is itself a valid window). New
// callers (post-#204 meterd pusher) must use ClaimPaddleOverageWindow
// + CompletePaddleOverageWindow — those carry the pending/completed
// claim state machine this legacy pair does not.
func (s *PgStore) HasPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`select exists(select 1 from paddle_overage_dedupe where account_id = $1 and window_start = $2)`,
		accountID, month.UTC()).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// RecordPaddleOverageMonth inserts a 'completed' dedupe row on the
// new window-keyed PK. Same ON CONFLICT DO NOTHING idempotency as
// RecordStripePushHour — preserved for callers that still pass the
// calendar-month start (which is itself a valid windowStart).
func (s *PgStore) RecordPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) error {
	_, err := s.pool.Exec(ctx,
		`insert into paddle_overage_dedupe (account_id, window_start, state) values ($1, $2, 'completed')
		 on conflict (account_id, window_start) do nothing`,
		accountID, month.UTC())
	return err
}

// PaddleOverageDedupeSchema probes information_schema.columns for
// the four migration-00041 columns + counts the per-state rows in
// one round-trip. Read-only; consumed by the B4 pre-flight
// (`GET /v1/admin/billing-paddle-overage/preflight`).
//
// TableExists is derived from a separate to_regclass probe so a
// freshly-installed box without migrations 00034 applied at all
// reports TableExists=false (with the count fields zero) — the
// pre-flight maps the missing-table case to "apply 00034 then
// 00041". A box with 00034 but no 00041 reports TableExists=true
// and the four HasX=false — the pre-flight maps that to "apply
// 00041".
//
// The single-query COUNT(*) FILTER shape (rather than two COUNTs)
// keeps this to one DB round-trip; the four columns come back as
// rows that we pivot into a bool map.
func (s *PgStore) PaddleOverageDedupeSchema(ctx context.Context) (PaddleOverageDedupeSchemaResult, error) {
	var out PaddleOverageDedupeSchemaResult
	var tableName *string
	if err := s.pool.QueryRow(ctx, `select to_regclass('public.paddle_overage_dedupe')::text`).Scan(&tableName); err != nil {
		return out, fmt.Errorf("probe paddle_overage_dedupe table: %w", err)
	}
	if tableName == nil || *tableName == "" {
		return out, nil // TableExists stays false; the pre-flight maps this to the missing-table error.
	}
	out.TableExists = true

	// Pull the four migration-00041 columns in one query. Anything
	// the DB doesn't know about is silently absent — we don't
	// require the column to be NOT NULL or to have a specific
	// default, only that it exists. The state column has a CHECK
	// constraint but its presence is the gate, not its constraint
	// shape — that's a separate concern for the pusher, not the
	// pre-flight.
	rows, err := s.pool.Query(ctx, `
		select column_name
		from information_schema.columns
		where table_schema = 'public'
		  and table_name = 'paddle_overage_dedupe'
		  and column_name in ('window_start', 'state', 'claimed_at', 'claimed_by')
	`)
	if err != nil {
		return out, fmt.Errorf("probe paddle_overage_dedupe columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return out, fmt.Errorf("scan column name: %w", err)
		}
		switch col {
		case "window_start":
			out.HasWindowStart = true
		case "state":
			out.HasState = true
		case "claimed_at":
			out.HasClaimedAt = true
		case "claimed_by":
			out.HasClaimedBy = true
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate paddle_overage_dedupe columns: %w", err)
	}

	// Per-state row counts. The state column is nullable on
	// legacy rows (pre-00041 default 'completed' was backfilled
	// for legacy rows but new INSERTs are constrained); the
	// FILTERs treat NULL as "neither pending nor completed" and
	// exclude it from both totals. That's the right shape — the
	// pre-flight reports what the meterd pusher sees.
	//
	// Guard on out.HasState: with 00034 applied but 00041 not
	// yet, the table exists but `state` does not, and the
	// FILTER would 42703. The handler contract is "partial 00041
	// surfaces the missing column hint", not a raw probe error
	// — skip the count when the column isn't there yet. The
	// schema-qualified FROM also matches the to_regclass probe
	// above so a non-default search_path can't make the two
	// probes disagree.
	if out.HasState {
		if err := s.pool.QueryRow(ctx, `
			select
			  count(*) filter (where state = 'pending'),
			  count(*) filter (where state = 'completed')
			from public.paddle_overage_dedupe
		`).Scan(&out.PendingRows, &out.CompletedRows); err != nil {
			return out, fmt.Errorf("count paddle_overage_dedupe states: %w", err)
		}
	}
	return out, nil
}

// CheckWebhookReplay returns ErrReplay if (provider, delivery_id) has
// a dedupe row received on or after cutoff. The PgStore implementation
// translates a found-row into ErrReplay so callers can branch on a
// single errors.Is(err, state.ErrReplay) check at the ingress layer.
// Backing schema: webhook_deliveries (migration 00149, with provider
// extensions in migration 00587).
func (s *PgStore) ClaimWebhookDelivery(ctx context.Context, provider, deliveryID string, cutoff, expiresAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`insert into webhook_deliveries (provider, delivery_id, expires_at)
		 values ($1, $2, $3)
		 on conflict (provider, delivery_id) do update
		 set received_at = now(), expires_at = excluded.expires_at
		 where webhook_deliveries.received_at < $4`,
		provider, deliveryID, expiresAt.UTC(), cutoff.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseWebhookDelivery removes a claim after business-state application
// fails. The next provider retry can then claim and process the delivery.
func (s *PgStore) ReleaseWebhookDelivery(ctx context.Context, provider, deliveryID string) error {
	_, err := s.pool.Exec(ctx,
		`delete from webhook_deliveries where provider = $1 and delivery_id = $2`,
		provider, deliveryID)
	return err
}

func (s *PgStore) CheckWebhookReplay(ctx context.Context, provider, deliveryID string, cutoff time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`select exists(select 1 from webhook_deliveries
		 where provider = $1 and delivery_id = $2 and received_at >= $3)`,
		provider, deliveryID, cutoff.UTC()).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// RecordWebhookDelivery inserts the dedupe row. ON CONFLICT DO UPDATE
// refreshes expires_at so a delivery that the sweep has not yet
// reaped gets a fresh 5-minute window — prevents the case where a
// redelivery arrives after the original expires_at but before the
// sweep runs. The unique constraint is (provider, delivery_id);
// the provider column is constrained by the
// webhook_deliveries_provider_check CHECK (initial providers in migration
// 00149; Polar/Resend are added by migration 00587).
func (s *PgStore) RecordWebhookDelivery(ctx context.Context, provider, deliveryID string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`insert into webhook_deliveries (provider, delivery_id, expires_at)
		 values ($1, $2, $3)
		 on conflict (provider, delivery_id) do update set expires_at = excluded.expires_at`,
		provider, deliveryID, expiresAt.UTC())
	return err
}

// SweepExpiredWebhookDeliveries is the apid sweep goroutine's bulk
// delete. Returns rows affected (informational). Runs on the
// `pkg/grace.Interval` cadence in cmd/apid/server.go; the partial
// index webhook_deliveries_expires_idx keeps the predicate scan
// O(N expired) rather than O(N total).
func (s *PgStore) SweepExpiredWebhookDeliveries(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`delete from webhook_deliveries where expires_at < $1`,
		now.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClaimPaddleOverageWindow atomically claims the (account_id,
// window_start) row. The shape mirrors ClaimInvocation
// (pgstore.go:1297): an INSERT … ON CONFLICT DO NOTHING creates the
// row in 'completed' default state, then an UPDATE … SET state='pending'
// WHERE state IS NULL OR (state='pending' AND claimed_at < now() - interval
// …) flips it to pending. The RETURNING carries the row state back so
// the caller can distinguish "I claimed it" from "another pod holds it"
// from "row already exists and is completed" (which is a stale
// pre-PR-#204 row that the caller should treat as a fresh re-claim).
//
// Backing schema: paddle_overage_dedupe, primary key (account_id,
// window_start), state column with check constraint
// (state IN ('pending','completed')). Migration 00037 introduced
// the per-window PK and the pending/completed state column.
func (s *PgStore) ClaimPaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, claimedBy string, lease time.Duration) (bool, error) {
	windowStart = windowStart.UTC()
	leaseSeconds := int64(lease.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}

	// Step 1: ensure the row exists. ON CONFLICT DO NOTHING is a no-op
	// when the row already exists — both the "fresh" path (no row)
	// and the "already claimed" path (row exists) leave us with a
	// row we can attempt to UPDATE. The RETURNING is intentionally
	// omitted because the next step is the actual claim.
	if _, err := s.pool.Exec(ctx,
		`insert into paddle_overage_dedupe (account_id, window_start, state, claimed_at, claimed_by)
		 values ($1, $2, 'completed', now(), $3)
		 on conflict (account_id, window_start) do nothing`,
		accountID, windowStart, claimedBy,
	); err != nil {
		return false, fmt.Errorf("paddle dedupe upsert acct=%s window=%s: %w", accountID, windowStart.Format(time.RFC3339), err)
	}

	// Step 2: atomic claim. Three branches map to claimable rows:
	//
	//   * Fresh row from Step 1 above — state='completed', claimed_at
	//     is `now()` (we just set it). The state clause carries this
	//     branch; without it the lease predicate alone would skip
	//     fresh rows because now() < now() - lease is FALSE.
	//   * Stale-pending row from a crashed pod — claimed_at is older
	//     than the lease. The lease predicate carries this branch.
	//   * Reaper-reset row from ReapStalePaddleOverageClaims — state
	//     reset to 'completed', claimed_at=null. The IS NULL clause
	//     carries this branch.
	//
	// A currently-pending row inside the lease is intentionally NOT
	// claimable — only the pod that holds the claim (or a reaped
	// releaser) can flip it.
	var claimed bool
	err := s.pool.QueryRow(ctx,
		`update paddle_overage_dedupe
		   set state = 'pending',
		       claimed_at = now(),
		       claimed_by = $3
		 where account_id = $1
		   and window_start = $2
		   and (claimed_at is null
		        or state = 'completed'
		        or claimed_at < now() - make_interval(secs => $4))
		 returning true`,
		accountID, windowStart, claimedBy, leaseSeconds,
	).Scan(&claimed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Another pod holds a non-stale claim. Skip.
			return false, nil
		}
		return false, fmt.Errorf("paddle dedupe claim acct=%s window=%s: %w", accountID, windowStart.Format(time.RFC3339), err)
	}
	return claimed, nil
}

// CompletePaddleOverageWindow transitions (account_id, window_start)
// from pending to completed after a successful SDK POST. Only the
// pod that holds the claim (state='pending') is allowed to flip; a
// foreign caller (or one whose lease expired and the row was
// reaped+re-claimed) sees 0 rows updated and gets ErrClaimLost so
// the caller can decide how to react. mb_seconds is stamped on the
// row (column added in migration 00280) so ops reconciliation can
// read the integer wire value directly without joining against
// usage_minutes; the Paddle merchant dashboard's line item
// Quantity + CustomData["mb_seconds"] carry the same value at the
// merchant side.
func (s *PgStore) CompletePaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, mbSeconds int64) error {
	windowStart = windowStart.UTC()
	tag, err := s.pool.Exec(ctx,
		`update paddle_overage_dedupe
		   set state = 'completed',
		       pushed_at = now(),
		       pushed_mb_seconds = $3
		 where account_id = $1
		   and window_start = $2
		   and state = 'pending'`,
		accountID, windowStart, mbSeconds,
	)
	if err != nil {
		return fmt.Errorf("paddle dedupe complete acct=%s window=%s: %w", accountID, windowStart.Format(time.RFC3339), err)
	}
	if tag.RowsAffected() == 0 {
		// Row is in 'completed' state (someone else completed — could be
		// a retried POST that landed on the merchant dashboard via
		// Idempotency-Key collapse), OR the row doesn't exist (caller
		// skipped Claim). Either way, the terminal state is correct
		// and we don't want to alert.
		//
		// We do still want to stamp pushed_mb_seconds for ops — re-do
		// the UPDATE without the state filter so the value is
		// materialised even on a re-stamped row. Idempotent re-stamp
		// is OK: the new value is the same integer the previous
		// complete call carried.
		if _, err := s.pool.Exec(ctx,
			`update paddle_overage_dedupe
			   set pushed_at = now(),
			       pushed_mb_seconds = $3
			 where account_id = $1
			   and window_start = $2
			   and state = 'completed'`,
			accountID, windowStart, mbSeconds,
		); err != nil {
			return fmt.Errorf("paddle dedupe complete refresh acct=%s window=%s: %w", accountID, windowStart.Format(time.RFC3339), err)
		}
		return nil
	}
	return nil
}

// ReapStalePaddleOverageClaims resets pending rows whose claimed_at
// is older than olderThan, returning them to the claimable pool.
// Called from meterd boot before the first push tick; safe to call
// again on a tick (idempotent inside the lease window). The RETURNING
// count is informational — the caller logs it but does not branch on
// the value.
func (s *PgStore) ReapStalePaddleOverageClaims(ctx context.Context, olderThan time.Duration) (int, error) {
	olderThanSeconds := int64(olderThan.Seconds())
	if olderThanSeconds < 1 {
		olderThanSeconds = 1
	}
	tag, err := s.pool.Exec(ctx,
		`update paddle_overage_dedupe
		   set state = 'completed',
		       claimed_at = null,
		       claimed_by = null
		 where state = 'pending'
		   and claimed_at < now() - make_interval(secs => $1)`,
		olderThanSeconds,
	)
	if err != nil {
		return 0, fmt.Errorf("paddle dedupe reap olderThan=%s: %w", olderThan, err)
	}
	return int(tag.RowsAffected()), nil
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

// UpsertAppSecret inserts or replaces the (app_id,
// scope='default', key) ciphertext row (ADR-092 PR-A).
// updated_at is bumped on conflict so schedd's "freshest per app" cache
// can re-stage drive1 even if the value didn't change (matters for
// rotation flows that re-seal with the same plaintext).
//
// ADR-089 PR-A: this method preserves the pre-PR-A wire shape (no kid
// stamp) for backward compatibility with existing call sites that
// don't track the sealing identity — webhook secret stores, the
// alert-rule dispatcher, etc. New callers (the per-secret rotate
// handler in PR-B, the rekey.Replayer in PR-A) use
// UpsertAppSecretWithKid which stamps kid alongside the new
// ciphertext. Use UpsertAppSecretInScope for non-default scopes.
func (s *PgStore) UpsertAppSecret(ctx context.Context, accountID, appID, key string, ciphertext []byte) error {
	return s.UpsertAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key, ciphertext)
}

// UpsertAppSecretWithKid is the kid-stamping sibling of
// UpsertAppSecret (ADR-089 PR-A / migration 00166). The kid column
// records which host identity sealed the row so operators can
// answer "what key sealed this row?" without parsing the
// ciphertext blob. Hardcodes scope='default' (PR-A); use
// UpsertAppSecretWithKidInScope for any other scope.
//
// ADR-089 D4: the kid is stamped at every Seal, both the user-
// initiated rotate handler (PR-B) and the rekey.Replayer (PR-A).
// Rows sealed before this PR stay with kid = "" until a subsequent
// Seal (re-key, re-seal, or new PUT) stamps them. The rekey pass
// walks every row and re-seals anything with kid != current, so
// after Replayer.Run completes the entire app_secrets table has
// kid = current.
func (s *PgStore) UpsertAppSecretWithKid(ctx context.Context, accountID, appID, key, kid string, ciphertext []byte) error {
	return s.UpsertAppSecretWithKidInScope(ctx, accountID, appID, DefaultEnvScope, key, kid, ciphertext)
}

// GetAppSecret returns the (account_id, app_id, scope='default',
// key) row including ciphertext, kid, and timestamps. Returns
// ErrNotFound when no row matches. Used by the per-secret rotate
// handler (PR-B) to distinguish first-time set (emits secret.set
// audit kind) from rotation (emits secret.rotated). Use
// GetAppSecretInScope for non-default scopes.
func (s *PgStore) GetAppSecret(ctx context.Context, accountID, appID, key string) (*AppSecret, error) {
	return s.GetAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key)
}

// UpsertAppSecretInScope is the scope-aware sibling of
// UpsertAppSecret (ADR-092 PR-A / migration 00214). Writes-or-
// replaces the (app_id, scope, key) row at the caller-supplied
// scope. Mirrors UpsertAppSecret's ON CONFLICT shape — the PK
// widening to (app_id, scope, key) means the conflict target is
// now a 3-column tuple.
func (s *PgStore) UpsertAppSecretInScope(ctx context.Context, accountID, appID, scope, key string, ciphertext []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_secrets (account_id, app_id, scope, key, ciphertext)
		 values ($1, $2, $3, $4, $5)
		 on conflict (app_id, scope, key) do update
		   set ciphertext = excluded.ciphertext,
		       updated_at = now()`,
		accountID, appID, scope, key, ciphertext)
	return err
}

// UpsertAppSecretWithKidInScope is the kid-stamping scope-aware
// sibling (ADR-092 PR-A). Mirrors UpsertAppSecretInScope but
// stamps kid alongside ciphertext.
func (s *PgStore) UpsertAppSecretWithKidInScope(ctx context.Context, accountID, appID, scope, key, kid string, ciphertext []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid)
		 values ($1, $2, $3, $4, $5, $6)
		 on conflict (app_id, scope, key) do update
		   set ciphertext = excluded.ciphertext,
		       kid = excluded.kid,
		       updated_at = now()`,
		accountID, appID, scope, key, ciphertext, kid)
	return err
}

// UpsertAppSecretWithKidAndValueHashInScope is the value-hash
// scope-aware sibling (ADR-117 env-diff matrix, PR-C). Mirrors
// UpsertAppSecretWithKidInScope but stamps both kid and
// value_hash alongside ciphertext. Used by the PUT + rotate
// paths + the rekey re-seal pass; legacy UpsertAppSecretWithKidInScope
// stays for callers that don't carry the value_hash field
// (the pre-PR-C rekey path, for example).
//
// valueHash is the 16-hex truncated HMAC-SHA256 of the
// PLAINTEXT (NOT the ciphertext — see ADR-117 D1). The handler
// computes it via secretbox.ValueFingerprint BEFORE SealOne so
// the same plaintext byte string feeds both the HMAC and the
// seal. SQL stores it as TEXT (NULL allowed for legacy rows
// pre-00296; the migration CHECK caps the length at 16 hex).
// NULLIF($7, ”) preserves the "empty string = NULL" semantic
// so an unconfigured handler surface as NULL on the column.
func (s *PgStore) UpsertAppSecretWithKidAndValueHashInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_secrets (account_id, app_id, scope, key, ciphertext, kid, value_hash)
		 values ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		 on conflict (app_id, scope, key) do update
		   set ciphertext = excluded.ciphertext,
		       kid = excluded.kid,
		       value_hash = excluded.value_hash,
		       updated_at = now()`,
		accountID, appID, scope, key, ciphertext, kid, valueHash)
	return err
}

// GetAppSecretInScope is the scope-aware sibling of GetAppSecret
// (ADR-092 PR-A). Returns the (account_id, app_id, scope, key) row
// including ciphertext, kid, and timestamps. Returns ErrNotFound
// when no row matches.
func (s *PgStore) GetAppSecretInScope(ctx context.Context, accountID, appID, scope, key string) (*AppSecret, error) {
	var out AppSecret
	err := s.pool.QueryRow(ctx,
		`select account_id, app_id, scope, key, ciphertext, COALESCE(kid, ''), COALESCE(value_hash, ''), created_at, updated_at
		 from app_secrets
		 where account_id = $1 and app_id = $2 and scope = $3 and key = $4`,
		accountID, appID, scope, key).Scan(
		&out.AccountID, &out.AppID, &out.Scope, &out.Key, &out.Ciphertext, &out.Kid, &out.ValueHash, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

// ListAppSecretsForRekey is the global paginated walk consumed by
// pkg/rekey.Replayer.Run (ADR-089 PR-A). Order is
// (account_id ASC, app_id ASC, key ASC) so a cursor based on the
// last visited tuple yields a deterministic continuation across
// daemon restarts.
//
// The cursor is "<account_id>|<app_id>|<key>" from
// RekeyProgress.LastID; an empty cursor starts from the
// beginning. limit is clamped to [1, 200] — values outside this
// range are silently coerced to 50, matching RekeyConfig.BatchSize
// default. Tests use small limits (≤10) to drive deterministic
// walks.
//
// CRASH-SAFETY: the WHERE clause uses COMPOSITE greater-than-or-
// equal. This pairs with pkg/rekey/Replayer.Run's
// per-row cursor advance + per-run seen-set dedupe: every row is
// visited at least once per Run (the >= fence brings back rows
// whose persist failed on a previous Run), but the seen-set
// prevents redundant re-processing within a single Run. A row
// whose persist fails mid-Run is retried in the next Run —
// after `gregale host-age prune-previous` the previous-key
// envelope would otherwise become permanently unreadable on a
// skipped row.
//
// Cross-account: this query intentionally returns rows across
// every account. The Replayer is an operator-driven background
// pass (FAAS_REKEY_ENABLED=true), not a customer-facing API; the
// pgx role already has read access to every account's app_secrets
// rows because vmmd unseals every customer's envelopes at wake
// time. The rekey pass is the same trust perimeter.
func (s *PgStore) ListAppSecretsForRekey(ctx context.Context, limit int, cursor string) ([]AppSecret, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Postgres parameter casts ($1::uuid) are evaluated at plan time, NOT
	// per-row — so an `$1 = '' or $1::uuid` short-circuit is impossible:
	// the planner tries to cast the empty string and 22P02s before the
	// OR runs. Branch the query instead: empty cursor → no WHERE clause;
	// non-empty cursor → composite >= with explicit uuid casts. The
	// composite-uuid text ordering is what gives us a stable, restartable
	// walk — see pkg/rekey/rekey.go for the cursor semantics.
	//
	// ADR-092 PR-A widened the cursor from 3-tuple to 4-tuple by adding
	// `scope`. The 3-tuple form is still accepted for the lazy-fallback
	// window so an in-flight Replayer that persisted a pre-PR LastID
	// continues to work after the rollout — a 3-tuple cursor is treated
	// as scope='default'.
	if cursor == "" {
		rows, err := s.pool.Query(ctx,
			`select account_id, app_id, scope, key, ciphertext, COALESCE(kid, ''), COALESCE(value_hash, ''), created_at, updated_at
			 from app_secrets
			 order by account_id asc, app_id asc, scope asc, key asc
			 limit $1`,
			limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []AppSecret
		for rows.Next() {
			var r AppSecret
			if err := rows.Scan(&r.AccountID, &r.AppID, &r.Scope, &r.Key, &r.Ciphertext, &r.Kid, &r.ValueHash, &r.CreatedAt, &r.UpdatedAt); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	parts := strings.SplitN(cursor, "|", 4)
	var curAcct, curApp, curScope, curKey string
	switch len(parts) {
	case 4:
		curAcct, curApp, curScope, curKey = parts[0], parts[1], parts[2], parts[3]
	case 3:
		// Pre-PR 3-tuple: scope collapses to DefaultEnvScope.
		curAcct, curApp, curKey = parts[0], parts[1], parts[2]
		curScope = DefaultEnvScope
	default:
		return nil, fmt.Errorf("pgstore: malformed rekey cursor %q", cursor)
	}
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, scope, key, ciphertext, COALESCE(kid, ''), COALESCE(value_hash, ''), created_at, updated_at
		 from app_secrets
		 where (account_id, app_id, scope, key) >= ($1::uuid, $2::uuid, $3, $4)
		 order by account_id asc, app_id asc, scope asc, key asc
		 limit $5`,
		curAcct, curApp, curScope, curKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppSecret
	for rows.Next() {
		var r AppSecret
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.Scope, &r.Key, &r.Ciphertext, &r.Kid, &r.ValueHash, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteAppSecret removes the (app_id, scope='default', key) row
// scoped to accountID (ADR-092 PR-A). Returns ErrNotFound when no
// row matches — the handler renders 400 CodeSecretNotFound
// (intentional: the URL resource IS the secret name, by design).
// Use DeleteAppSecretInScope for non-default scopes.
func (s *PgStore) DeleteAppSecret(ctx context.Context, accountID, appID, key string) error {
	return s.DeleteAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key)
}

// DeleteAppSecretInScope is the scope-aware sibling of
// DeleteAppSecret (ADR-092 PR-A). The PK widening to
// (app_id, scope, key) means the WHERE clause gains a `scope = $3`
// predicate.
func (s *PgStore) DeleteAppSecretInScope(ctx context.Context, accountID, appID, scope, key string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_secrets where account_id = $1 and app_id = $2 and scope = $3 and key = $4`,
		accountID, appID, scope, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAppSecretsInScope is the scope-aware sibling of
// ListAppSecrets (ADR-092 PR-A). Returns every (key, ciphertext,
// kid, timestamps) row on the app where scope matches the
// caller-supplied value, scoped to accountID. Order: by scope
// ASC, key ASC for deterministic wake staging.
func (s *PgStore) ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]AppSecret, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, scope, key, ciphertext, coalesce(kid, '') as kid, coalesce(value_hash, '') as value_hash, created_at, updated_at
		 from app_secrets
		 where account_id = $1 and app_id = $2 and scope = $3
		 order by scope asc, key asc`,
		accountID, appID, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppSecret
	for rows.Next() {
		var r AppSecret
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.Scope, &r.Key, &r.Ciphertext, &r.Kid, &r.ValueHash, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAppSecrets returns every secret on the app where scope =
// 'default', scoped to accountID (ADR-092 PR-A delegation). Use
// ListAppSecretsInScope for the scope-aware path; use
// ListAllAppSecrets for the cross-scope enumeration the GET
// ?scope=__all__ handler renders (PR-B).
func (s *PgStore) ListAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error) {
	return s.ListAppSecretsInScope(ctx, accountID, appID, DefaultEnvScope)
}

// ListAllAppSecrets is the cross-scope mirror of ListAppSecrets
// (ADR-092 PR-A). Used by apid's GET
// /v1/apps/{slug}/secrets?scope=__all__ arm (PR-B) to render the
// nested secrets_by_scope response shape. Order: by scope ASC,
// key ASC.
func (s *PgStore) ListAllAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, scope, key, ciphertext, coalesce(kid, '') as kid, coalesce(value_hash, '') as value_hash, created_at, updated_at
		 from app_secrets
		 where account_id = $1 and app_id = $2
		 order by scope asc, key asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppSecret
	for rows.Next() {
		var r AppSecret
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.Scope, &r.Key, &r.Ciphertext, &r.Kid, &r.ValueHash, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAppSecretsForAccount joins apps + app_secrets on account_id
// (issue #393). The cursor is the (app_slug, scope, key) triple —
// after PR-A's PK widening to (app_id, scope, key), the same
// (slug, key) at multiple scopes is no longer unique, so the
// cursor MUST include scope to fence rows that would otherwise
// be dropped or duplicated (e.g. (foo,default,API_KEY) and
// (foo,prod,API_KEY) on a limit=2 page).
//
// The handler emits "<slug>|<scope>|<key>" and the SQL splits it
// back via split_part. Order is (app_slug ASC, scope ASC, key ASC)
// so the cursor walk is monotonic and the row-by-row comparison
// in the WHERE clause is the same sort key. Each row carries the
// app_slug the per-app path doesn't (the URL slug is the path
// parameter there).
//
// Cross-account isolation is the JOIN on apps.account_id = $1 — the
// SQL is the only IDOR guard. Returns nil slice (not error) when
// the account has no secrets.
//
// ADR-092 PR-B: pre-PR-B the cursor was just (slug, key) which
// silently dropped the trailing rows when a customer had the
// same key at multiple scopes. The fix widens the cursor model
// in lockstep with the (slug, scope, key) sort key.
func (s *PgStore) ListAppSecretsForAccount(ctx context.Context, accountID string, limit int, before string) ([]AccountAppSecret, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx,
		`select s.account_id, s.app_id, a.slug, s.key, s.scope, s.ciphertext, coalesce(s.value_hash, '') as value_hash, s.created_at, s.updated_at
		 from app_secrets s
		 join apps a on a.id = s.app_id
		 where s.account_id = $1
		   and ($2 = '' or (a.slug, s.scope, s.key) > (split_part($2, '|', 1), split_part($2, '|', 2), split_part($2, '|', 3)))
		 order by a.slug asc, s.scope asc, s.key asc
		 limit $3`, accountID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountAppSecret
	for rows.Next() {
		var r AccountAppSecret
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.AppSlug, &r.Key, &r.Scope, &r.Ciphertext, &r.ValueHash, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountAppSecrets is the quota helper (ADR-092 PR-A). Used by
// apid's PUT handler (PR-B widens it to scope-aware) to enforce
// Limits.SecretCountMax BEFORE UpsertAppSecretInScope so a
// quota-exceeded request never overwrites an existing
// (app_id, scope, key) row.
//
// ADR-092 limits decision: SecretCountMax is GLOBAL across scopes
// (parallel to ADR-090 D6 on env). A customer with 80 prod +
// 80 staging secrets is 160 total — exceeds Scale cap of 100
// and returns ErrPlanLimitSecrets. This is the right cap
// because customers will assume "100 per scope" without
// reading the docs. PR-A leaves the SQL predicate unchanged
// (no scope filter); the count is across all scopes by design.
func (s *PgStore) CountAppSecrets(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_secrets where account_id = $1 and app_id = $2`,
		accountID, appID).Scan(&n)
	return n, err
}

// --- per-app private-registry Basic Auth (issue #461 / ADR-062) -------------
//
// Mirror of the sealed-secrets shape (lines 6446-6551) keyed by
// (app_id, registry) instead of (app_id, key). The password column is
// the same age-sealed bytea; username is plaintext metadata. apid is
// the sole writer (handlers_registry_auth.go); imaged is the sole
// reader (pkg/imaged/handler.go::buildImageLayer — transient unseal).
//
// Cross-account isolation: every query scopes by (account_id, app_id,
// registry). The (account_id, app_id) ownership predicate is the SQL
// IDOR guard; the FK on (account_id, app_id) is the schema-level
// guarantee.

// UpsertAppRegistryCredential inserts or replaces the
// (app_id, registry) row. username + password_encrypted are replaced
// on conflict; created_at is preserved (the ON CONFLICT clause omits
// it). updated_at is bumped on every call so imaged's MarkUsed check
// sees a fresh mtime on rotation flows.
func (s *PgStore) UpsertAppRegistryCredential(ctx context.Context, accountID, appID, registry, username string, passwordEncrypted []byte) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_registry_credentials (account_id, app_id, registry, username, password_encrypted)
		 values ($1, $2, $3, $4, $5)
		 on conflict (app_id, registry) do update
		   set username = excluded.username,
		       password_encrypted = excluded.password_encrypted,
		       updated_at = now()`,
		accountID, appID, registry, username, passwordEncrypted)
	return err
}

// GetAppRegistryCredential returns the row for (app_id, registry).
// Returns ErrNotFound when no row matches the
// (account_id, app_id, registry) triple — the (account_id, app_id)
// ownership predicate is the SQL IDOR guard. The schema FK is
// defence in depth.
func (s *PgStore) GetAppRegistryCredential(ctx context.Context, accountID, appID, registry string) (AppRegistryCredential, error) {
	var r AppRegistryCredential
	err := s.pool.QueryRow(ctx,
		`select account_id, app_id, registry, username, password_encrypted, created_at, updated_at, last_used_at
		 from app_registry_credentials
		 where account_id = $1 and app_id = $2 and registry = $3`,
		accountID, appID, registry).Scan(
		&r.AccountID, &r.AppID, &r.Registry, &r.Username, &r.PasswordEncrypted,
		&r.CreatedAt, &r.UpdatedAt, &r.LastUsedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppRegistryCredential{}, ErrNotFound
		}
		return AppRegistryCredential{}, err
	}
	return r, nil
}

// ListAppRegistryCredentials returns every (registry, username,
// ciphertext) row on the app. Order by registry ASC for deterministic
// wire output. Returns nil slice (not error) when the app has no
// credentials — the handler renders an empty list.
func (s *PgStore) ListAppRegistryCredentials(ctx context.Context, accountID, appID string) ([]AppRegistryCredential, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, registry, username, password_encrypted, created_at, updated_at, last_used_at
		 from app_registry_credentials
		 where account_id = $1 and app_id = $2
		 order by registry asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppRegistryCredential
	for rows.Next() {
		var r AppRegistryCredential
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.Registry, &r.Username, &r.PasswordEncrypted,
			&r.CreatedAt, &r.UpdatedAt, &r.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteAppRegistryCredential removes the (app_id, registry) row
// scoped to accountID. Returns ErrNotFound when no row matches.
func (s *PgStore) DeleteAppRegistryCredential(ctx context.Context, accountID, appID, registry string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_registry_credentials where account_id = $1 and app_id = $2 and registry = $3`,
		accountID, appID, registry)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAppRegistryCredentials is the quota helper. Used by apid's PUT
// handler to enforce Limits.RegistryCredentialMax BEFORE
// UpsertAppRegistryCredential. Mirrors CountAppSecrets.
func (s *PgStore) CountAppRegistryCredentials(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_registry_credentials where account_id = $1 and app_id = $2`,
		accountID, appID).Scan(&n)
	return n, err
}

// RegistryCredentialQuotaCheck collapses the "count rows on app"
// + "does (app, host) exist" pair into a single CTE query. apid's
// PUT handler calls this instead of running CountAppRegistryCredentials
// + GetAppRegistryCredential back-to-back — one round-trip, same
// shape as AppSecretQuotaCheck (if that ever lands). The CTE form
// is single-pass: the count and the exists check both read from
// the same scan. `(n, false, nil)` is the "host isn't set yet"
// answer.
func (s *PgStore) RegistryCredentialQuotaCheck(ctx context.Context, accountID, appID, registry string) (int, bool, error) {
	var n int
	var exists bool
	err := s.pool.QueryRow(ctx, `
		with counts as (
		    select count(*) as n,
		           bool_or(registry = $3) as exists
		    from app_registry_credentials
		    where account_id = $1 and app_id = $2
		)
		select coalesce(n, 0), coalesce(exists, false)
		from counts`,
		accountID, appID, registry).Scan(&n, &exists)
	return n, exists, err
}

// MarkAppRegistryCredentialUsed updates last_used_at + updated_at to
// now(). Returns ErrNotFound when no row matches the
// (account_id, app_id, registry) triple. Callers MUST treat
// ErrNotFound as non-fatal — the deployment already succeeded, and a
// missing-on-cascade is an expected race with account/app delete.
func (s *PgStore) MarkAppRegistryCredentialUsed(ctx context.Context, accountID, appID, registry string) error {
	tag, err := s.pool.Exec(ctx,
		`update app_registry_credentials
		 set last_used_at = now(), updated_at = now()
		 where account_id = $1 and app_id = $2 and registry = $3`,
		accountID, appID, registry)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- app env vars (issue #395 / ADR-045) -------------------------------------
//
// Mirror of the sealed-secrets shape (lines 4608-4676) minus the
// ciphertext column. Plaintext TEXT values; apid is the sole writer;
// schedd reads via ListAppEnv at wake time and ships the rows on the
// AppSpec wire.

// UpsertAppEnv inserts or replaces the (app_id, scope='default', key)
// env row. updated_at is bumped on conflict so schedd's "freshest per
// app" staging path observes a fresh mtime on every write — same
// posture as the secrets table (rotation flows re-PUT without changing
// the value are still treated as a write).
//
// ADR-090 PR-A: the underlying PK is (app_id, scope, key) post-00203,
// so the flat writers hardcode scope='default' at the SQL boundary.
// Use UpsertAppEnvInScope for any other scope.
func (s *PgStore) UpsertAppEnv(ctx context.Context, accountID, appID, key, value string) error {
	return s.UpsertAppEnvInScope(ctx, accountID, appID, "default", key, value)
}

// DeleteAppEnv removes the (app_id, scope='default', key) row scoped
// to accountID. Returns ErrNotFound when no row matches — handler
// renders 400 CodeEnvVarNotFound.
//
// ADR-090 PR-A: hardcodes scope='default' (see UpsertAppEnv).
func (s *PgStore) DeleteAppEnv(ctx context.Context, accountID, appID, key string) error {
	return s.DeleteAppEnvInScope(ctx, accountID, appID, "default", key)
}

// ListAppEnv returns every (key, value) row on the app where
// scope='default', scoped to accountID. Order: by scope ASC, key ASC
// for deterministic staging (the flat reader sees the same ordering
// as pre-00203 because all its rows share scope='default'). Returns
// nil slice when the app has no env rows.
//
// ADR-090 PR-A: the WHERE clause adds `scope='default'` so the flat
// reader keeps seeing the pre-PR row set. The composite index
// `app_envs_account_app_scope_idx (account_id, app_id, scope)` makes
// the (account_id, app_id, scope) prefix a single index scan.
func (s *PgStore) ListAppEnv(ctx context.Context, accountID, appID string) ([]AppEnv, error) {
	return s.ListAppEnvInScope(ctx, accountID, appID, "default")
}

// CountAppEnv is the quota helper used by apid's PUT handler to enforce
// Limits.EnvVarsMax BEFORE UpsertAppEnv. Counts ALL scope values for
// the app per ADR-090 D6 (EnvVarsMax is per-app, not per-scope).
// Mirrors CountAppSecrets.
//
// ADR-090 PR-A: the WHERE clause drops the scope filter (per-D6
// per-app semantics). PR-B's per-scope quota enforcement (if it
// lands) uses CountAppEnvInScope instead.
func (s *PgStore) CountAppEnv(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_envs where account_id = $1 and app_id = $2`,
		accountID, appID).Scan(&n)
	return n, err
}

// UpsertAppEnvInScope is the scope-aware sibling of UpsertAppEnv.
// Inserts or replaces the (app_id, scope, key) row using the
// caller-supplied scope. The 3-column PK upsert guarantees the on
// conflict targets the right tuple regardless of the scope value.
//
// Reserved for PR-B's `?scope=` API handler and PR-C's
// wake-time scope overlay. PR-A only verifies the surface compiles
// and the flat wrappers delegate correctly.
func (s *PgStore) UpsertAppEnvInScope(ctx context.Context, accountID, appID, scope, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`insert into app_envs (account_id, app_id, scope, key, value)
		 values ($1, $2, $3, $4, $5)
		 on conflict (app_id, scope, key) do update
		   set value = excluded.value,
		       updated_at = now()`,
		accountID, appID, scope, key, value)
	return err
}

// DeleteAppEnvInScope is the scope-aware sibling of DeleteAppEnv.
// Removes the (app_id, scope, key) row scoped to accountID. Returns
// ErrNotFound when no row matches.
func (s *PgStore) DeleteAppEnvInScope(ctx context.Context, accountID, appID, scope, key string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_envs where account_id = $1 and app_id = $2 and scope = $3 and key = $4`,
		accountID, appID, scope, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAppEnvInScope is the scope-aware sibling of ListAppEnv. Returns
// every (key, value) row on the app where scope matches the
// caller-supplied value, scoped to accountID. Order: by scope ASC,
// key ASC for deterministic staging.
func (s *PgStore) ListAppEnvInScope(ctx context.Context, accountID, appID, scope string) ([]AppEnv, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, scope, key, value, created_at, updated_at
		 from app_envs
		 where account_id = $1 and app_id = $2 and scope = $3
		 order by scope asc, key asc`,
		accountID, appID, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppEnv
	for rows.Next() {
		var e AppEnv
		if err := rows.Scan(&e.AccountID, &e.AppID, &e.Scope, &e.Key, &e.Value, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountAppEnvInScope is the scope-aware sibling of CountAppEnv.
// Counts only rows where scope matches the caller-supplied value.
// Reserved for future per-scope caps (ADR-091 follow-up); PR-A does
// not call it.
func (s *PgStore) CountAppEnvInScope(ctx context.Context, accountID, appID, scope string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_envs where account_id = $1 and app_id = $2 and scope = $3`,
		accountID, appID, scope).Scan(&n)
	return n, err
}

// ListAllAppEnv returns every env row on the app across all scopes,
// scoped to accountID. Order: by scope ASC, key ASC. Used by apid's
// GET /v1/apps/{slug}/envs?scope=__all__ arm (ADR-090 PR-B) to
// render the nested `env_by_scope` response shape (D3).
//
// The composite index `app_envs_account_app_scope_idx
// (account_id, app_id, scope)` covers the (account_id, app_id)
// prefix; the row count is bounded by Limits.EnvVarsMax so the scan
// is cheap (8..2000 by plan).
func (s *PgStore) ListAllAppEnv(ctx context.Context, accountID, appID string) ([]AppEnv, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, scope, key, value, created_at, updated_at
		 from app_envs
		 where account_id = $1 and app_id = $2
		 order by scope asc, key asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppEnv
	for rows.Next() {
		var e AppEnv
		if err := rows.Scan(&e.AccountID, &e.AppID, &e.Scope, &e.Key, &e.Value, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- app trusted cosign signers (issue #472 / ADR-054) -----------------------
//
// Per-app allowlist of cosign public keys whose signatures on OCI images
// are accepted at deploy time. Mirrors AWS Lambda's CodeSigningConfig
// (trusted-signing-profiles + signing-job-completion). Default
// apps.require_signed=false means no production behavior changes on the
// merged PR; only apps that opt in via PATCH /v1/apps/{slug} pay the
// verify cost. apid is the only writer; imaged reads the resulting rows
// at buildImageLayer time (and via pg_notify('trusted_signer_changed')).
//
// Quota: enforced at the apid handler via Limits.TrustedSignerCountMax
// (default 16), mirroring the CountAppSecrets/CountAppEnv pattern.

// UpsertAppTrustedSigner inserts or replaces the (app_id, signer_name)
// row. On conflict the public-key blob and added_by_account_id are
// refreshed; added_at stays at the original write so the audit trail
// distinguishes "created" from "rotated".
//
// Returns (addedAt, rotated, err) where rotated=false means this
// was a fresh insert (the audit row emits app.trusted_signer_added)
// and rotated=true means this was an update on an existing row
// (the audit row emits app.trusted_signer_rotated). The addedAt
// returned is the original write timestamp, preserved across
// rotations.
//
// The detection uses PostgreSQL's `(xmax = 0)` idiom: on a fresh
// INSERT, xmax is 0 (no row to lock); on an UPDATE-via-ON CONFLICT
// xmax is the conflicting row's xid. This is exact, race-free, and
// does NOT require a second SELECT (the prior 1-second heuristic
// in cmd/apid/handlers_trusted_signers.go would misclassify
// concurrent rotations within the same second).
func (s *PgStore) UpsertAppTrustedSigner(ctx context.Context, accountID, appID, signerName string, pubKey []byte, addedByAccountID string) (time.Time, bool, error) {
	var addedAt time.Time
	var isNewRow bool
	err := s.pool.QueryRow(ctx,
		`insert into app_trusted_signers (account_id, app_id, signer_name, cosign_public_key, added_by_account_id)
		 values ($1, $2, $3, $4, $5)
		 on conflict (app_id, signer_name) do update
		   set cosign_public_key   = excluded.cosign_public_key,
		       added_by_account_id = excluded.added_by_account_id
		 returning added_at, (xmax = 0) AS is_new`,
		accountID, appID, signerName, pubKey, addedByAccountID).Scan(&addedAt, &isNewRow)
	if err != nil {
		return time.Time{}, false, err
	}
	// isNewRow=true means inserted now; rotated = !isNewRow.
	return addedAt, !isNewRow, nil
}

// DeleteAppTrustedSigner removes the (app_id, signer_name) row scoped to
// accountID. Returns ErrNotFound when no row matches — handler renders
// 404 CodeTrustedSignerNotFound.
func (s *PgStore) DeleteAppTrustedSigner(ctx context.Context, accountID, appID, signerName string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_trusted_signers where account_id = $1 and app_id = $2 and signer_name = $3`,
		accountID, appID, signerName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAppTrustedSigners returns every (signer_name, cosign_public_key)
// row on the app, scoped to accountID. Order: by signer_name ASC for
// deterministic stage (mirrors ListAppEnv). Returns nil slice when the
// app has no trusted signers.
func (s *PgStore) ListAppTrustedSigners(ctx context.Context, accountID, appID string) ([]AppTrustedSigner, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, signer_name, cosign_public_key, added_at, added_by_account_id
		 from app_trusted_signers
		 where account_id = $1 and app_id = $2
		 order by signer_name asc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppTrustedSigner
	for rows.Next() {
		var r AppTrustedSigner
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.SignerName, &r.CosignPublicKey, &r.AddedAt, &r.AddedByAccountID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAppTrustedSignersForApp is the system-side sibling of
// ListAppTrustedSigners: takes only appID, used by the on-disk
// mirror writer (cmd/apid/trusted_publisher_writer.go) which is
// system-side and doesn't have an accountID in scope. Same order
// (signer_name ASC) and shape as the accountID-scoped sibling.
func (s *PgStore) ListAppTrustedSignersForApp(ctx context.Context, appID string) ([]AppTrustedSigner, error) {
	rows, err := s.pool.Query(ctx,
		`select account_id, app_id, signer_name, cosign_public_key, added_at, added_by_account_id
		 from app_trusted_signers
		 where app_id = $1
		 order by signer_name asc`,
		appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppTrustedSigner
	for rows.Next() {
		var r AppTrustedSigner
		if err := rows.Scan(&r.AccountID, &r.AppID, &r.SignerName, &r.CosignPublicKey, &r.AddedAt, &r.AddedByAccountID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountAppTrustedSigners is the quota helper used by apid's PUT handler
// to enforce Limits.TrustedSignerCountMax BEFORE UpsertAppTrustedSigner.
// Mirrors CountAppSecrets.
func (s *PgStore) CountAppTrustedSigners(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from app_trusted_signers where account_id = $1 and app_id = $2`,
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
	err := scanAppInto(&a, row)
	return a, err
}

func scanApps(rows pgx.Rows) ([]App, error) {
	var out []App
	for rows.Next() {
		a := App{}
		if err := scanAppInto(&a, rows); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scanAppInto decodes the apps projection (appsSelectColumns) into a.
// Single source of truth for the 20-column scanApp shape — every
// SELECT/RETURNING that reads an App row uses this helper so adding
// a column only touches the const + the App struct + this function.
func scanAppInto(a *App, row pgx.Row) error {
	var typeStr, statusStr string
	var manifestBytes []byte
	var allowlistText string
	var publicAuthIPAllowlistText string
	var workloadClassStr string
	var scalingPolicyBytes []byte
	// Tier A10 / ADR-088: scratch sink for the overflow_node
	// projection. coalesce(overflow_node::text, '') returns
	// '' for the NULL-preference case, which we promote to
	// App.OverflowNode == nil below.
	var overflowNodeStr string
	// ADR-091 CORS improvements D1: scratch sink for the
	// cors_default_enabled scan target. The schema's NOT
	// NULL DEFAULT false makes the bool value always present
	// at scan time; the *bool field is built by lifting
	// this local below.
	var corsDefaultEnabled bool
	if err := row.Scan(&a.ID, &a.AccountID, &a.Slug, &typeStr, &a.Runtime, &a.RAMMB, &a.IdleTimeoutS,
		&a.MaxConcurrency, &statusStr, &manifestBytes, &a.CreatedAt, &a.MinInstances, &allowlistText,
		&publicAuthIPAllowlistText,
		&a.AutoscaleTargetRPS, &a.AutoscaleTargetCPUPct,
		&a.ProjectID, &a.RootDir, &a.WorkloadName, &workloadClassStr, &a.StartCommand,
		&a.StreamingEnabled, &a.RequireSigned, &scalingPolicyBytes, &a.LastScaleOutAt, &a.LastScaleInAt, &a.NodeID, &a.ReassignedAt, &a.MigratedAt,
		&a.WarmSnapshotEnabled, &a.WarmSnapshotMinRequests, &a.WarmSnapshotMinMs,
		// Issue #475: per-app eviction tier. eviction_priority is
		// NOT NULL DEFAULT 'best_effort' (migration 00135) so the
		// query can scan into a plain string without a SQL NULL
		// helper.
		&a.EvictionPriority,
		// Issue #560: per-app require_authn column.
		&a.RequireAuthn,
		// Issue #477 / ADR-079: per-app public_auth. Both
		// columns land positionally after require_authn. The
		// mode column is NOT NULL DEFAULT 'open' so a plain
		// *string scan is safe; the basic blob is nullable
		// bytea (NULL when mode='open'|'bearer'), scanned
		// into a *[]byte to keep the SQL NULL → Go nil
		// convention explicit.
		&a.PublicAuthMode, &a.PublicAuthBasicSealed,
		// Issue #695 / ADR-080: grand-father marker. Nullable
		// timestamptz scanned into *time.Time (pgx handles the
		// SQL NULL → Go nil conversion natively — same shape
		// as ReassignedAt / MigratedAt above). NOT NULL after
		// migration 00156 backfills; NULL on apps created
		// post-flip (no grandfather needed).
		&a.AuthDefaultFlippedAt,
		// Issue #676 / PR-3: per-app websocket_enabled flag.
		// NOT NULL DEFAULT false (migration 00155); plain bool
		// scan is safe.
		&a.WebSocketEnabled,
		// ADR-093: per-route observability opt-in. NOT NULL
		// DEFAULT false (migration 00212); plain bool scan is
		// safe. Order is positional and must match
		// appsSelectColumns.
		&a.RouteMetricsEnabled,
		// Tier A10 / ADR-088: per-app overflow_node preference.
		// Scanned into a scratch string then conditionally
		// promoted to *string so NULL round-trips as Go-nil —
		// nil = "no preference" (back to A9 fallback). The
		// pgx string → Go nil promotion mirrors how the
		// column-list normalisation handles other nullable
		// string-shaped values like RootDir / WorkloadName
		// (see below).
		&overflowNodeStr,
		// Issue #272 / ADR-094: per-app preview metadata. The
		// column projection wraps preview_of_slug and
		// preview_pr_state in coalesce(..., '') so the scan
		// targets can be plain strings (NULL → '' round-trips
		// through). preview_pr_number is wrapped in
		// coalesce(..., 0) so the scan can use a plain *int
		// target — pgx rejects SQL NULL into *int with
		// "cannot scan NULL into *int" (the strict default
		// for the int4 → Go int mapping). The 0 sentinel is
		// distinguishable from "preview with pr_number=0" via
		// the preview_of_slug discriminator (prod apps have
		// preview_of_slug=NULL → ''). preview_expires_at is
		// nullable timestamptz scanned into *time.Time
		// directly (pgx handles SQL NULL → Go nil natively).
		//
		// ADR-091 CORS improvements D1: per-app default CORS
		// opt-in + allowlist. cors_default_enabled is NOT
		// NULL DEFAULT false in the schema so the scan lands
		// a plain bool; we lift it into *bool below so the
		// wire projection surfaces the legacy-row nil vs.
		// explicit opt-out *false distinction. The three-state
		// collapse (nil = never touched, *false = PATCHed-to-
		// false, *true = opted in) lives on the write path;
		// the read path always returns a non-nil pointer.
		// cors_default_origins is text[] — pgx maps a NULL
		// array to a nil Go slice, which is exactly the
		// "deny all" sentinel the gateway uses (mirrors how
		// EgressAllowlist handles the empty case). No helper
		// normalisation needed.
		&a.PreviewOfSlug, &a.PreviewPrNumber, &a.PreviewPrState, &a.PreviewExpiresAt,
		// Mega-C PR-1 / issue #961 leaf 3: dedupe carrier for
		// the one-click PR comment destroy surface. Nullable
		// timestamptz scanned into *time.Time directly (pgx
		// handles SQL NULL → Go nil natively — same shape as
		// PreviewExpiresAt above).
		&a.PreviewDestroyCommentedAt,
		&corsDefaultEnabled, &a.CORSDefaultOrigins,
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag (apps.maintenance_mode). NOT NULL
		// DEFAULT false (migration 00237); plain bool scan is
		// safe. Order is positional and must match
		// appsSelectColumns above.
		&a.MaintenanceMode,
		// ADR-124: per-app wire-protocol selector. NOT NULL
		// DEFAULT 'http1' (migration 00382); the schema-default
		// 'http1' guarantee means a plain string scan is safe
		// (no coalesce needed). Closed-set
		// apps_app_protocol_chk rejects any value outside
		// {http1, http2, grpc} at write time.
		&a.AppProtocol,
		// ADR-119: per-app static egress IP + audit stamp.
		// static_egress_ip is nullable inet — pgx scans it into
		// *netip.Addr (Go nil when SQL NULL). static_egress_ip_
		// set_at is nullable timestamptz — pgx scans it into
		// *time.Time (Go nil when SQL NULL). Both pointer
		// targets are populated by the Set*/CASE branch on the
		// write side.
		&a.StaticEgressIP, &a.StaticEgressIPSetAt); err != nil {
		return mapErr(err)
	}
	if overflowNodeStr != "" {
		s := overflowNodeStr
		a.OverflowNode = &s
	}
	// Lift hydrated cors_default_enabled into the
	// store-layer pointer field. The schema's NOT
	// NULL DEFAULT false makes the bool value always
	// present at scan time; the *bool shape exists
	// because the wire projection needs the nil vs
	// *false distinction (legacy rows vs explicit
	// opt-out hydrates identically to *false — the
	// three-state lives on the write path only).
	a.CORSDefaultEnabled = &corsDefaultEnabled
	a.Type = AppType(typeStr)
	a.Status = AppStatus(statusStr)
	a.WorkloadClass = WorkloadClass(workloadClassStr)
	if len(manifestBytes) > 0 {
		_ = json.Unmarshal(manifestBytes, &a.Manifest)
	}
	a.EgressAllowlist = cidrTextToPrefixes(allowlistText)
	// ADR-118: per-app ingress IP allowlist. The column projection
	// is public_auth_ip_allowlist::text (mirroring egress_allowlist
	// above) so the cidr array round-trips through a stable string
	// rendering — pgx's native cidr[] decode is fine but the text
	// projection is consistent with the egress column's treatment
	// and keeps the scan path single-shape.
	a.PublicAuthIPAllowlist = cidrTextToPrefixes(publicAuthIPAllowlistText)
	// scaling_policy: an empty jsonb ('{}'::jsonb, the column
	// default) round-trips as a non-nil zero-length slice. The
	// non-empty path is the customer-authored shape from
	// `pkg/state.ScalingPolicy.MarshalJSON`. Empty path = legacy
	// row, project as the zero-value struct + nil pointer so the
	// apid read-back falls through to the min_instances /
	// max_concurrency projection.
	if len(scalingPolicyBytes) > 0 {
		p := &ScalingPolicy{}
		if err := json.Unmarshal(scalingPolicyBytes, p); err == nil {
			a.ScalingPolicy = p
		}
	}
	return nil
}

// appsSelectColumns is the projection every app SELECT/RETURNING
// must use. Listed in the same order as scanAppInto — scanAppInto
// reads columns positionally, so the order is load-bearing. Keep this
// const and the App struct aligned: adding a column touches both.
//
// Column provenance (most-recent first):
//
//	warm_snapshot_enabled, warm_snapshot_min_requests,
//	  warm_snapshot_min_ms  — issue #470 / ADR-055 (two-tier snapshot;
//	    migration 00109 adds the columns, migration 00110 adds the
//	    per-tier unique index on snapshots)
//	node_id, reassigned_at  — issue #533 / ADR-066 (Phase 2 / Gate A
//	    shard key + Tier A4 cross-node rebalance)
//	scaling_policy,
//	  last_scale_out_at,
//	  last_scale_in_at      — issue #462 / ADR-058 PR-A (scaling policy)
//	require_signed          — issue #472 / ADR-054 (cosign enforce)
//	streaming_enabled       — issue #471 PR-A (streaming response)
//	project_id, root_dir,
//	  workload_name,
//	  workload_class,
//	  start_command         — ADR-050 Phase 1 (repo decomposition)
//	require_authn          — issue #560 (per-deployment
//	  authentication opt-in; Cloud Run --no-allow-unauthenticated
//	  analogue). Surfaced on GET /v1/apps/{slug} so dashboards can
//	  render "auth required on / off" alongside the streaming /
//	  require_signed pills.
const appsSelectColumns = `
	id, account_id, slug, type, coalesce(runtime,''), ram_mb, coalesce(idle_timeout_s,0),
	max_concurrency, status, manifest, created_at, min_instances, egress_allowlist::text,
	public_auth_ip_allowlist::text,
	coalesce(autoscale_target_rps, 0), coalesce(autoscale_target_cpu_pct, 0),
	coalesce(project_id::text, ''), coalesce(root_dir, ''), workload_name,
	workload_class, coalesce(start_command, ''), streaming_enabled, require_signed,
	scaling_policy, last_scale_out_at, last_scale_in_at, coalesce(node_id::text, ''),
	reassigned_at, migrated_at,
	warm_snapshot_enabled, warm_snapshot_min_requests, warm_snapshot_min_ms,
	eviction_priority,
	require_authn,
	-- Issue #477 / ADR-079: per-app public_auth
	public_auth_mode, public_auth_basic,
-- Issue #695 / ADR-080: grand-father marker. Set by
	-- migration 00156 on every pre-flip row; reads null on
	-- apps created post-flip.
	auth_default_flipped_at,
	-- Issue #676 / PR-3: per-app raw-bytes Upgrade bridge flag.
	-- Boolean NOT NULL DEFAULT false (migration 00155); apid applies
	-- Plan.WebSocketEnabled() at CreateApp time and gates PATCH
	-- writes through Plan.WebSocketResponseAllowed().
	websocket_enabled,
	-- ADR-093: per-route observability opt-in. Boolean NOT NULL
	-- DEFAULT false (migration 00212); apid applies
	-- Plan.RouteMetricsEnabled() at CreateApp time and gates PATCH
	-- writes through Plan.RouteMetricsResponseAllowed() (Free →
	-- 403 plan_route_metrics_not_allowed).
	route_metrics_enabled,
	-- Tier A10 / ADR-088: per-app overflow_node preference.
	-- Nullable UUID; FK to compute_nodes(id) with ON DELETE SET
	-- NULL cascades the preference to NULL on operator-side
	-- compute_node deletion (migration 00167). The empty-uuid
	-- CHECK is a tripwire against buggy INSERT paths. NULL
	-- coerces to the empty string via coalesce so the pgx
	-- scan sees a string target — the App.OverflowNode field
	-- is *string (nil = no preference = default A9 fallback).
	coalesce(overflow_node::text, ''),
	-- Issue #272 / ADR-094: per-app preview metadata. NULL on
	-- production apps. preview_of_slug carries the parent app's
	-- slug (no FK — parent may be deleted while previews are
	-- still open). preview_pr_state is the closed-set label
	-- enforced by apps_preview_pr_state_chk (migration 00218);
	-- the coalesce() wraps NULL → '' so the pgx scan into a
	-- plain string is safe. preview_pr_number is wrapped in
	-- coalesce(..., 0) so the scan can target *int directly
	-- (pgx rejects SQL NULL into *int with "cannot scan NULL
	-- into *int"; the 0 sentinel is distinguishable from a real
	-- PR-number-0 preview via the preview_of_slug discriminator).
	-- preview_expires_at is nullable timestamptz scanned into
	-- *time.Time directly (pgx handles SQL NULL → Go nil natively).
	coalesce(preview_of_slug, ''), coalesce(preview_pr_number, 0),
	coalesce(preview_pr_state, ''), preview_expires_at,
	-- Mega-C PR-1 / issue #961 leaf 3: dedupe carrier for the
	-- one-click PR comment destroy surface. Nullable
	-- timestamptz; preview_destroy_commented_at IS NULL means
	-- "githubd has never posted a destroy hint comment for this
	-- preview row". pgx scans nullable timestamptz → *time.Time
	-- natively; no coalesce needed.
	preview_destroy_commented_at,
	-- CORS improvements D1: per-app default CORS opt-in + allowlist.
	-- cors_default_enabled is NOT NULL DEFAULT false (migration 00224);
	-- cors_default_origins is a nullable text[]; coalesce to '{}' so the
	-- pgx scan sees a non-nil slice on legacy rows (the gateway treats
	-- len==0 as "deny all" — same contract as EgressAllowlist).
	cors_default_enabled, coalesce(cors_default_origins, '{}'::text[]),
	-- ADR-091 amendment / §4.1.2.0: coarse-gate per-app
	-- maintenance flag. Boolean NOT NULL DEFAULT false
	-- (migration 00237); plain bool scan is safe. Order is
	-- positional and must match scanApp below.
	maintenance_mode,
	-- ADR-124: per-app wire-protocol selector. NOT NULL
	-- DEFAULT 'http1' (migration 00382); closed-set CHECK
	-- apps_app_protocol_chk admits {http1, http2, grpc}.
	-- Text NOT NULL with constant default — plain string
	-- scan is safe (no coalesce needed). Positional last so
	-- future audit-log columns can append safely.
	app_protocol,
	-- ADR-119: per-app static egress IP (customer BYOIP). Nullable
	-- inet (NULL = no pin); family=4 CHECK enforced at the DB
	-- layer. Scanned into *netip.Addr directly — pgx maps SQL
	-- inet to Go's netip.Addr natively for non-NULL rows, and a
	-- SQL NULL maps to a Go nil pointer when the scan target is
	-- a pointer to a value type. The companion timestamp column
	-- (static_egress_ip_set_at) is nullable timestamptz scanned
	-- into *time.Time (pgx handles SQL NULL → Go nil natively —
	-- same shape as ReassignedAt / MigratedAt above).
	static_egress_ip, static_egress_ip_set_at`

// Compile-time anchor: the const is interpolated only inside SQL raw-string
// literals (the 9 SELECT/RETURNING sites), which golangci-lint's `unused`
// checker doesn't trace. This blank reference keeps the const bound to a
// Go-level use so deleting it (or a future SQL rewrite that drops all
// nine callers) trips the linter instead of rotting silently.
var _ = appsSelectColumns

// deploymentSelectColumnsWithRootfs is the canonical SELECT projection
// for a deployment row. Used by every read path that needs the full
// deployment state (CreateDeployment RETURNING, DeploymentByID,
// LatestDeployment, LiveDeployment, LatestSupersededDeployment,
// UpdateDeploymentMinInstances RETURNING, SetDeploymentParked
// RETURNING, SetDeploymentFailed RETURNING, ListAllDeployments,
// ListDeploymentsForAccount, ListDeploymentsByNodeID, etc). The
// order is load-bearing — pgx scans scanDeploymentInto positionally,
// and the scan order matches the SELECT list.
//
// Issue #460 / ADR-053: the trailing 6 columns are the override shape.
// Coalesce rules:
//   - text[] columns: coalesce with ARRAY[]::text[] so the read
//     destination is always a non-nil []string (mirrors the pre-PR
//     convention used for non-override reads).
//   - jsonb columns: NO coalesce — pgx scans into json.RawMessage which
//     can be nil for a NULL column (pre-override rows + a refresh
//     that didn't write env/env_secrets/healthcheck).
//   - int port: coalesce to 0 so the absence sentinel reads as 0
//     (mirrors nullableOverridePort on the write side).
//
// Adding a new column touches: this const + scanDeploymentInto +
// scanDeployments + the INSERT in CreateDeployment. Keep them
// aligned; the gofmt/golangci-lint gate catches the constant binding
// but not column-order drift.
//
// The rootfs triple (rootfs_path, rootfs_key, rootfs_bytes) is
// always included — scanDeploymentInto has a fixed set of positional scan
// destinations and the helper is shared across read paths. Earlier
// versions split this into "with rootfs" vs "without rootfs"
// variants, but the asymmetry caused pgx's "number of field
// descriptions must equal number of destinations" runtime error on
// every LatestDeployment / UpdateDeploymentMinInstances /
// LatestParkedDeploymentForApp call — see PR #697 review pass.
//
// Used by DeploymentByID, LiveDeployment, LatestDeployment,
// LatestSupersededDeployment, UpdateDeploymentMinInstances,
// LatestParkedDeploymentForApp, ListDeploymentsForAccount,
// ListDeploymentsByNodeID, SetDeploymentFailed,
// SetDeploymentParked (RETURNING), CreateDeployment (RETURNING),
// and ListAllDeployments.
const deploymentSelectColumnsWithRootfs = `
	id, app_id, coalesce(build_id::text,''), image_digest, kind,
		coalesce(source_path,''), coalesce(source_root,''), coalesce(source_bytes,0), coalesce(handler,''), coalesce(log_path,''),
	coalesce(rootfs_path,''), coalesce(rootfs_key,''), coalesce(rootfs_bytes,0),
	status, coalesce(error,''), coalesce(error_code,''),
	coalesce(error_hint,''), coalesce(error_why,''), coalesce(error_fix,''),
	error_relevant_logs,
	created_at,
	coalesce(source_url,''), coalesce(commit_sha,''),
	coalesce(override_entrypoint, ARRAY[]::text[]),
	coalesce(override_cmd, ARRAY[]::text[]),
	override_env, override_env_secrets,
	coalesce(override_port, 0), override_healthcheck,
	override_liveness_probe,
	coalesce(sidecars, '[]'::jsonb),
	min_instances,
	scan_result, scan_status, scanned_at,
	secret_findings, secret_scanned_at,
	liveness_restart_count,
	coalesce(parked_reason,''), parked_at,
	traffic_percent,
	scope,
	stage_state,
	coalesce(deployed_by_user_id::text,''), deployed_via, coalesce(host(deployed_from_ip),''), coalesce(pusher_login,''),
	coalesce(reason,''), coalesce(tag,''), coalesce(deployed_by,''), pr_number,
	rollback_on_5xx,
	first_wake_at, first_5xx_window_ends_at, first_5xx_count,
	last_auto_rollback_at, coalesce(last_auto_rollback_reason,''),
	coalesce(canary_preset, 'none'), canary_step, canary_total_steps,
	canary_step_started_at, coalesce(rollout_state, 'pending'),
	rollout_started_at, rollout_completed_at, rollout_aborted_at,
	coalesce(rollout_aborted_reason, ''),
	-- ADR-124 deployment queue controls (migration 00391/00491). priority
	-- is NOT NULL DEFAULT 100 so the coalesce is purely for symmetry
	-- with the rest of the projection (and for the rare pre-PR
	-- backfill window). cancelled_*/cancel_reason are nullable so the
	-- coalesce is the canonical "never cancelled" sentinel. deleted_at
	-- + deleted_by_principal are the soft-delete audit columns.
	coalesce(priority, 100), coalesce(reordered_by_principal, ''), reordered_at,
	cancelled_at, coalesce(cancelled_by_principal, ''), coalesce(cancel_reason, ''),
	deleted_at, coalesce(deleted_by_principal, ''),
	coalesce(workflows, '[]'::jsonb),
	coalesce(full_rootfs_allow_auto, false), full_rootfs_override`

// Compile-time anchors for the deployment column constants. See the
// appsSelectColumns comment above for rationale.
var _ = deploymentSelectColumnsWithRootfs

// deploymentSelectColumnsQualified is the d.alias-prefixed variant of
// deploymentSelectColumnsWithRootfs for SELECTs that JOIN with another
// table (e.g. ListDeploymentsForAccount joins deployments d with apps
// a on a.id = d.app_id). The qualifications resolve the id / app_id
// ambiguity that arises when both tables carry the same column name.
// Column order matches deploymentSelectColumnsWithRootfs exactly so
// the scanDeployments helper stays in lockstep across all read paths.
const deploymentSelectColumnsQualified = `
	d.id, d.app_id, coalesce(d.build_id::text,''), d.image_digest, d.kind,
		coalesce(d.source_path,''), coalesce(d.source_root,''), coalesce(d.source_bytes,0), coalesce(d.handler,''), coalesce(d.log_path,''),
	coalesce(d.rootfs_path,''), coalesce(d.rootfs_key,''), coalesce(d.rootfs_bytes,0),
	d.status, coalesce(d.error,''), coalesce(d.error_code,''),
	coalesce(d.error_hint,''), coalesce(d.error_why,''), coalesce(d.error_fix,''),
	d.error_relevant_logs,
	d.created_at,
	coalesce(d.source_url,''), coalesce(d.commit_sha,''),
	coalesce(d.override_entrypoint, ARRAY[]::text[]),
	coalesce(d.override_cmd, ARRAY[]::text[]),
	d.override_env, d.override_env_secrets,
	coalesce(d.override_port, 0), d.override_healthcheck,
	d.override_liveness_probe,
	coalesce(d.sidecars, '[]'::jsonb),
	d.min_instances,
	d.scan_result, d.scan_status, d.scanned_at,
	d.secret_findings, d.secret_scanned_at,
	d.liveness_restart_count,
	coalesce(d.parked_reason,''), d.parked_at,
	d.traffic_percent,
	d.scope,
	d.stage_state,
	coalesce(d.deployed_by_user_id::text,''), d.deployed_via, coalesce(host(d.deployed_from_ip),''), coalesce(d.pusher_login,''),
	coalesce(d.reason,''), coalesce(d.tag,''), coalesce(d.deployed_by,''), d.pr_number,
	d.rollback_on_5xx,
	d.first_wake_at, d.first_5xx_window_ends_at, d.first_5xx_count,
	d.last_auto_rollback_at, coalesce(d.last_auto_rollback_reason,''),
	coalesce(d.canary_preset, 'none'), d.canary_step, d.canary_total_steps,
	d.canary_step_started_at, coalesce(d.rollout_state, 'pending'),
	d.rollout_started_at, d.rollout_completed_at, d.rollout_aborted_at,
	coalesce(d.rollout_aborted_reason, ''),
	-- ADR-124 deployment queue controls (migration 00391/00491). See the
	-- unqualified-projection counterpart above for the rationale on
	-- coalesce choices.
	coalesce(d.priority, 100), coalesce(d.reordered_by_principal, ''), d.reordered_at,
	d.cancelled_at, coalesce(d.cancelled_by_principal, ''), coalesce(d.cancel_reason, ''),
	d.deleted_at, coalesce(d.deleted_by_principal, ''),
	coalesce(d.workflows, '[]'::jsonb),
	coalesce(d.full_rootfs_allow_auto, false), d.full_rootfs_override`

var _ = deploymentSelectColumnsQualified

// scanDeploymentInto is the single source of truth for the
// deployment-row column scan order shared by scanDeployment,
// scanDeploymentWithRootfs, and scanDeployments. Adding a new
// column means: append the column to the SELECT projection
// constants (deploymentSelectColumnsWithRootfs /
// deploymentSelectColumnsQualified), and append the destination
// to this function — never to just one wrapper. Three previous
// PRs landed the column in one wrapper and missed another, which
// surfaced as pg-shard-2 failures (e.g. issue #554 follow-up
// migration 00157 could have shipped the same shape of bug). The
// helper eliminates that duplication class entirely. The
// optional `rootfsPath` / `rootfsKey` / `rootfsBytes`
// destinations are nil for scanDeployment and scanDeployments
// callers that don't care about the rootfs triple (they ignore
// the three fields anyway), but the columns still come back in
// the SELECT projection so the destination count matches.
func scanDeploymentInto(d *Deployment, row pgx.Row, rootfsPath, rootfsKey *string, rootfsBytes *int64) error {
	var kind, statusStr string
	var scanStatus *string
	var scannedAt *time.Time
	var parkedAt *time.Time
	// Issue #977 / ADR-116: pr_number is scanned as *int so the
	// NULL ("no PR") case reads cleanly. The text columns (reason,
	// tag, deployed_by) are coalesced to '' in the SELECT projection
	// so they read into the plain string fields of the struct; the
	// closed-set vocabulary on `tag` is enforced at the schema layer
	// via deployments_tag_set_chk, not on the scan side.
	var prNumber *int
	// Issue #961 leaf 8 / ADR-118 / Mega-C PR-2: nullable
	// timestamps + a non-nullable counter scanned directly into
	// the struct. first_wake_at and first_5xx_window_ends_at are
	// NULL until the gateway stamps the first customer-visible
	// response; last_auto_rollback_at is NULL until schedd fires
	// the rollback. last_auto_rollback_reason is coalesced to ''
	// in the SELECT projection so it reads into a plain string
	// field (the closed-set is enforced at the schema layer via
	// deployments_last_auto_rollback_reason_check).
	var firstWakeAt, first5xxWindowEndsAt, lastAutoRollbackAt *time.Time
	var canaryStepStartedAt *time.Time
	var rolloutStartedAt, rolloutCompletedAt, rolloutAbortedAt *time.Time
	// Issue #460 / ADR-053: six override columns scanned here so
	// the SELECT projections in DeploymentByID / LatestDeployment /
	// etc. match. The scan order matches the column order in the
	// SELECT list — keep them in lockstep or pgx's positional Scan
	// returns the wrong field into the wrong destination.
	//
	// Issue #464 / ADR-055: scan columns (scan_result jsonb,
	// scan_status text, scanned_at timestamptz) are scanned here
	// too. scan_status is nullable (NULL on pre-PR-#651 rows,
	// before the backfill passes — and NULL inside the window
	// where the deploy ships ahead of the scan) so the destination
	// is *string. scanned_at is also nullable for the same reason;
	// destination is *time.Time. pgx scans a NULL into a nil
	// pointer cleanly.
	//
	// Issue #554 follow-up: parked_reason + parked_at columns
	// (migration 00157). parked_at is *time.Time so the closed-set
	// "never parked" path (NULL parked_at) scans cleanly. The
	// SELECT projection coalesces parked_reason to '' so the
	// "never parked" path (NULL parked_reason) scans into the
	// struct's plain string field — the closed-set is enforced at
	// the schema layer via the deployments_parked_reason_check
	// constraint, not on the scan side. The omitempty on the JSON
	// tag keeps the wire clean when the value is empty.
	if err := row.Scan(&d.ID, &d.AppID, &d.BuildID, &d.ImageDigest, &kind,
		&d.SourcePath, &d.SourceRoot, &d.SourceBytes, &d.Handler, &d.LogPath,
		rootfsPath, rootfsKey, rootfsBytes,
		&statusStr, &d.Error, &d.ErrorCode,
		&d.ErrorHint, &d.ErrorWhy, &d.ErrorFix,
		&d.ErrorRelevantLogs,
		&d.CreatedAt,
		&d.SourceURL, &d.CommitSHA,
		&d.OverrideEntrypoint, &d.OverrideCmd,
		&d.OverrideEnv, &d.OverrideEnvSecrets,
		&d.OverridePort, &d.OverrideHealthcheck,
		&d.OverrideLivenessProbe,
		&d.Sidecars, &d.MinInstances,
		&d.ScanResult, &scanStatus, &scannedAt,
		&d.SecretFindings, &d.SecretScannedAt,
		&d.LivenessRestartCount,
		&d.ParkedReason, &parkedAt, &d.TrafficPercent,
		&d.Scope,
		&d.StageState,
		&d.DeployedByUserID, &d.DeployedVia, &d.DeployedFromIP, &d.PusherLogin,
		// Issue #977 / ADR-116: annotation columns. reason / tag /
		// deployed_by are coalesced to '' in the SELECT projection
		// (nullable text NULL → ''), so the typed-string destinations
		// stay plain string fields. pr_number is NULLIF($N, 0) on
		// the INSERT side and a plain int column on the SELECT side,
		// scanned via the *int local returned as nil for NULL.
		&d.Reason, &d.Tag, &d.DeployedBy, &prNumber,
		&d.RollbackOn5xx,
		&firstWakeAt, &first5xxWindowEndsAt, &d.First5xxCount,
		&lastAutoRollbackAt, &d.LastAutoRollbackReason,
		&d.CanaryPreset, &d.CanaryStep, &d.CanaryTotalSteps,
		&canaryStepStartedAt, &d.RolloutState,
		&rolloutStartedAt, &rolloutCompletedAt, &rolloutAbortedAt,
		&d.RolloutAbortedReason,
		// ADR-124 deployment queue controls (migration 00391/00491). The
		// scan order mirrors the SELECT projection above — see the
		// docblock on deploymentSelectColumnsWithRootfs for the
		// "lockstep or pgx panic" invariant.
		&d.Priority, &d.ReorderedByPrincipal, &d.ReorderedAt,
		&d.CancelledAt, &d.CancelledByPrincipal, &d.CancelReason,
		&d.DeletedAt, &d.DeletedByPrincipal, &d.Workflows,
		&d.FullRootfsAllowAuto, &d.FullRootfsOverride,
	); err != nil {
		return mapErr(err)
	}
	if rootfsPath != nil {
		d.RootfsPath = *rootfsPath
	}
	if rootfsKey != nil {
		d.RootfsKey = *rootfsKey
	}
	if rootfsBytes != nil {
		d.RootfsBytes = *rootfsBytes
	}
	d.Kind = DeploymentKind(kind)
	d.Status = DeploymentStatus(statusStr)
	if scanStatus != nil {
		d.ScanStatus = *scanStatus
	}
	if scannedAt != nil {
		d.ScannedAt = *scannedAt
	}
	d.ParkedAt = parkedAt // nil for "never parked"; non-nil for a stamped park
	if prNumber != nil {
		d.PRNumber = *prNumber
	}
	d.FirstWakeAt = firstWakeAt
	d.First5xxWindowEndsAt = first5xxWindowEndsAt
	d.LastAutoRollbackAt = lastAutoRollbackAt
	d.CanaryStepStartedAt = canaryStepStartedAt
	d.RolloutStartedAt = rolloutStartedAt
	d.RolloutCompletedAt = rolloutCompletedAt
	d.RolloutAbortedAt = rolloutAbortedAt
	return nil
}

func scanDeployment(row pgx.Row) (Deployment, error) {
	d := Deployment{}
	// Use local scratch vars for the rootfs triple so the call
	// passes NON-NIL *string / *int64 destinations to row.Scan.
	// pgx v5 panics with "invalid memory address or nil pointer
	// dereference" when a typed-nil pointer is passed as a Scan
	// destination (the unified 32-column helper unifies this path
	// with scanDeploymentWithRootfs; previously scanDeployment had
	// its own 29-column projection that skipped the rootfs columns
	// entirely). The scratch values are discarded — callers don't
	// see rootfs fields on the Deployment struct.
	var rootfsPath, rootfsKey string
	var rootfsBytes int64
	if err := scanDeploymentInto(&d, row, &rootfsPath, &rootfsKey, &rootfsBytes); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// scanDeploymentWithRootfs is the post-imaged variant that also
// reads the rootfs_path / rootfs_key / rootfs_bytes columns
// stamped by SetDeploymentRootfs. Every reads-everything query
// (used by schedd's prime handshake, M5, and by the engine's Wake
// flow at LiveDeployment) uses this so the snapshot_prime
// consumer sees the layer path AND schedd's wake wire can carry
// the layer key (issue #96 / ADR-025 axis 2 / PR #116). Ordering
// matches the SELECT projections in DeploymentByID, LiveDeployment,
// and SetDeploymentFailed.
func scanDeploymentWithRootfs(row pgx.Row) (Deployment, error) {
	d := Deployment{}
	var rootfsPath, rootfsKey string
	var rootfsBytes int64
	if err := scanDeploymentInto(&d, row, &rootfsPath, &rootfsKey, &rootfsBytes); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

func scanDeployments(rows pgx.Rows) ([]Deployment, error) {
	var out []Deployment
	// Local scratch vars for the rootfs triple — pgx v5 panics on
	// typed-nil Scan destinations. The values are discarded.
	var rootfsPath, rootfsKey string
	var rootfsBytes int64
	for rows.Next() {
		d := Deployment{}
		// Mirrors scanDeployment: keep the column scan destination
		// list aligned with the SELECT projection in
		// deploymentSelectColumnsWithRootfs — adding a new column
		// there without adding it here triggers pgx's "number of
		// field descriptions must equal number of destinations"
		// runtime error on every ListDeploymentsForApp / Account
		// read path. Caught by the pg-shard-2 unit suite
		// (TestPg_CreateDeployment_*). The shared
		// scanDeploymentInto helper makes drift impossible —
		// changing one SELECT projection forces a single helper
		// update.
		if err := scanDeploymentInto(&d, rows, &rootfsPath, &rootfsKey, &rootfsBytes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanBuild(row pgx.Row) (Build, error) {
	b := Build{}
	var kind, statusStr, fc string
	// pgtype.Timestamptz is the canonical nullable timestamptz
	// reader in this file (see test_request_id_tz_row at
	// pgstore.go:12322) — its `.Valid` flag round-trips NULL
	// cleanly across both row.Scan (single-row callers like
	// BuildByID) and rows.Scan (multi-row callers like
	// ListBuildsForAccountPaged). `*time.Time` works for
	// single-row scans when the dst pointer is nil, but pgx
	// v5.10.0 rejects NULL into a nil `*time.Time` when the
	// same scan args are reused across rows.Scan iterations —
	// the running build has started_at set + finished_at NULL,
	// and the second iteration's `finishedAt` is left at the
	// first iteration's post-scan value, which is then
	// incompatible with column NULL. pgtype.Timestamptz avoids
	// the trap by always being a value type with a `.Valid`
	// flag.
	var startedAt, finishedAt, cancelledAt pgtype.Timestamptz
	if err := row.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &startedAt, &finishedAt, &b.EnqueuedAt, &cancelledAt, &b.CancelledByDeploymentCascade); err != nil {
		return Build{}, mapErr(err)
	}
	if startedAt.Valid {
		b.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		b.FinishedAt = finishedAt.Time
	}
	if cancelledAt.Valid {
		t := cancelledAt.Time
		b.CancelledAt = &t
	}
	b.Kind = DeploymentKind(kind)
	b.Status = BuildStatus(statusStr)
	b.FailureClass = FailureClass(fc)
	return b, nil
}

// scanBuildProvenance reads a build_provenance row into the
// BuildProvenance struct (ADR-038). All columns except id, build_id,
// source_sha256, started_at, finished_at are COALESCEd to empty
// string on the read side so the struct's text fields stay
// valid (avoiding a nil-deref if a future migration loosens a
// NOT NULL).
func scanBuildProvenance(row pgx.Row) (BuildProvenance, error) {
	p := BuildProvenance{}
	if err := row.Scan(
		&p.ID, &p.BuildID, &p.BuildkitVer, &p.RailpackVer,
		&p.BaseDigest, &p.SourceSHA256, &p.SourceURL, &p.CommitSHA,
		&p.Plan, &p.RunnerDigest, &p.BuilderNodeID,
		&p.StartedAt, &p.FinishedAt, &p.SBOMStorageKey,
		&p.FrameworkVer,
	); err != nil {
		return BuildProvenance{}, mapErr(err)
	}
	return p, nil
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
// node_id is the 12th column (issue #97 / ADR-025 axis 3) — NOT NULL since
// migrations/00024_compute_nodes but scanned into a string so a future
// regression that re-allows NULL surfaces as an empty string in Go rather
// than a scan error (the SELECT column list pins the contract; a divergence
// from there is a louder failure than a Scan error).
//
// framework_ready_at is the 14th column (PR #470-FU-B, migration 00112).
// Nullable forever — legacy rows never had a vsock signal and Free/Hobby
// plans never opt in. Scanned into a *time.Time so the nil/zero-value
// distinction survives the Scan trip (pgx returns untyped nil for NULL
// TIMESTAMPTZ, which is exactly the marker we want to keep on the struct).
//
// tail_count is the 15th column (issue #667 / ADR-078, migration 00151),
// and mode is the 16th column (issue #1186 / ADR-137).
// NOT NULL DEFAULT 0 — every pre-#667 row reads as 0 (the column
// default fills pre-migration rows), which is the correct "no active
// tails" value schedd's reaper gate (PR 4) decisions are keyed on.
// Scanned into a plain int (no *int) because the column is NOT NULL
// across the entire schema lifetime — a *int would silently mask a
// future regression that re-allows NULL.
func scanInstanceCols(scan func(...any) error) (Instance, error) {
	ins := Instance{}
	var started, lastReq, parked, frameworkReady *time.Time
	// wake_id is the 13th column (migration 00028). It's NOT NULL post-
	// 00028 but scanned into a string so any pre-migration-00028 row that
	// somehow surfaced surfaces as "" rather than a NULL scan error — the
	// SELECT column list is the contract that prevents column-order drift
	// from silently swallowing wake_id into an unrelated field.
	if err := scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
		&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked, &ins.NodeID, &ins.WakeID,
		&frameworkReady, &ins.TailCount, &ins.Mode); err != nil {
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
	if frameworkReady != nil {
		ts := *frameworkReady
		ins.FrameworkReadyAt = &ts
	}
	return ins, nil
}

// scanInstanceColsWithMigration is the 19-column variant of
// scanInstanceCols that also lifts framework_ready_at (PR #543 /
// migration 00120), migrated_from_node_id, migrated_at, and
// lease_token (Tier A5 / migration 00097, ADR-066), and
// tail_count (issue #667 / ADR-078, migration 00151). Used by
// ListLiveInstancesOnNode and ListExpiredMigrations — the rest
// of the codebase reads 15-column instances rows and doesn't
// need the migration lineage. Column order matches the SELECTs
// in those two functions; keep them in lock-step.
// migrated_from_node_id is nullable forever (a fresh instance
// has no migration history), so it scans into a *string
// pointer to preserve the distinction between "fresh" and
// "previously migrated". migrated_at is nullable for the same
// reason. lease_token is also nullable. framework_ready_at is
// nullable on every pre-warm-capture row — for migrating
// instances it is always NULL (the warm-capture path predates
// migration), but the column is part of the row shape so we
// scan it for shape parity. tail_count is NOT NULL DEFAULT 0
// and scans into a plain int.
//
// Single-call scan: pgx rejects a 17-column SELECT with a 13-dest
// scan followed by a 4-dest scan — the row surface is one
// contiguous column stream and each scan call must consume all
// columns in one go. The base 13 fields are duplicated here
// rather than split across two scan calls so the row descriptor
// stays consistent across rows.
func scanInstanceColsWithMigration(scan func(...any) error) (Instance, error) {
	ins := Instance{}
	var started, lastReq, parked, frameworkReady *time.Time
	var migFromStr *string
	var migAtTime *time.Time
	var leaseStr *string
	if err := scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
		&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked, &ins.NodeID, &ins.WakeID,
		&frameworkReady, &migFromStr, &migAtTime, &leaseStr, &ins.TailCount, &ins.Mode); err != nil {
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
	if frameworkReady != nil {
		ts := *frameworkReady
		ins.FrameworkReadyAt = &ts
	}
	ins.MigratedFromNodeID = migFromStr
	ins.MigratedAt = migAtTime
	if leaseStr != nil {
		ins.LeaseToken = *leaseStr
	}
	return ins, nil
}

// scanInstancesWithTerminal is the 17-column variant of scanInstanceCols
// that also lifts terminal_at (PR #74) and node_id (issue #97). Used only
// by ListInstancesInTerminalStatesOlderThan — the rest of the codebase
// reads 14-column instances rows (incl. node_id) and doesn't need
// terminal_at, so threading it into scanInstanceCols would force every
// SELECT to expose it for no reason. node_id is included here so the
// retention sweep's row carries the same node info as a live row — the
// GC delete later (DeleteInstance) doesn't need it, but a future
// per-node retention policy might, and surfacing it now keeps the row
// shape uniform across the read paths. framework_ready_at is the 14th
// column (PR #470-FU-B migration 00112); for the retention sweep it's
// always NULL (terminal rows pre-date the warm-capture path) but the
// column is part of the row shape so we scan it for shape parity.
// tail_count is the 15th column (issue #667 / ADR-078, migration 00151),
// terminal_at is the 16th column, and mode is the 17th column;
// for the retention sweep it's always 0 (terminal rows have no active
// tails) but the column is part of the row shape so we scan it for
// shape parity.
func scanInstancesWithTerminal(rows pgx.Rows) ([]Instance, error) {
	var out []Instance
	for rows.Next() {
		ins := Instance{}
		var started, lastReq, parked, frameworkReady, terminal *time.Time
		// Column order matches ListInstancesInTerminalStatesOlderThan's
		// SELECT (now 17 columns after migration 00028 added wake_id,
		// 00112 added framework_ready_at, 00151 added tail_count,
		// before terminal_at).
		if err := rows.Scan(&ins.ID, &ins.AppID, &ins.DeploymentID, &ins.State, &ins.Netns, &ins.GuestUID,
			&ins.HostIP, &ins.RAMMB, &started, &lastReq, &parked, &ins.NodeID, &ins.WakeID, &frameworkReady, &ins.TailCount, &terminal, &ins.Mode); err != nil {
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
		if frameworkReady != nil {
			ts := *frameworkReady
			ins.FrameworkReadyAt = &ts
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
	// The 9th column is tier (issue #470 / ADR-055). Every query
	// in this file now selects the tier column explicitly; the
	// scan returns "init" if the column is NULL (legacy rows from
	// before migration 00110 applied).
	var tier *string
	if err := row.Scan(&s.ID, &s.DeploymentID, &s.FCVersion, &s.MemBytes, &s.DiskBytes, &s.StorageKey, &s.Stale, &s.CreatedAt, &tier); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	if tier != nil && *tier != "" {
		s.Tier = *tier
	} else {
		s.Tier = SnapshotTierInit
	}
	return s, nil
}

// --- error mapping -----------------------------------------------------------

// ErrConflict is returned when a unique constraint is violated. MemStore
// returns plain errors; PgStore maps pgx's unique-violation SQLSTATE here so
// callers don't need to know about pgerrcode.
var ErrConflict = errors.New("state: conflict")

// ErrNoRollbackTarget is returned by GetDeploymentByIDScopedToSuperseded when
// no row matches the (deployment_id, app_id) pair (missing deployment, or
// deployment belongs to a different app). SAFE-RELEASES-G (issue #976). Maps
// to API stable code rollback_target_not_found (404).
var ErrNoRollbackTarget = errors.New("state: no rollback target")

// ErrRollbackTargetAlreadyLive is returned by GetDeploymentByIDScopedToSuperseded
// when the row exists and belongs to the app but has status != 'superseded'
// (caller asked to rollback to the already-current deployment). Rejected
// explicitly rather than silently no-op'd. Maps to API stable code
// rollback_target_already_live (409).
var ErrRollbackTargetAlreadyLive = errors.New("state: rollback target is already live")

// ErrInvalidArgument is returned when a Store method receives a
// required-empty argument (empty account_id, empty hash, etc).
// Distinct from ErrNotFound so callers can map empty-input bugs to
// 400 (validation) rather than 404 (missing row). Used by the
// MemStore side of the password / OAuth-link primitives introduced
// for issue #165 / ADR-032.
var ErrInvalidArgument = errors.New("state: invalid argument")

// checkViolationMappedToInvalid lists the constraint names whose
// CHECK violations surface as ErrInvalidArgument at the Store
// contract. The list is intentionally narrow — most CHECK violations
// bubble the raw *pgconn.PgError so tripwire tests like
// TestPgStore_InstancesStateCheck_RejectsBogusState,
// TestPgStore_InstancesStateCheck_RejectsInjection, and
// TestPgStore_UpdateApp_SlashZeroRejected can substring-match
// "23514" and a future widening of the CHECK (e.g. to text) is
// visible at the test boundary.
//
// The two entries below map to ErrInvalidArgument because their
// Store-layer callers (PR-B's SetAppWorkloadClass +
// SetProjectScanSource) explicitly validate the input upstream
// — a CHECK hit is a contract violation between the Store and the
// schema, not a transient DB error. Don't add
// `instances_state_check` or `apps_egress_allowlist_cidr` here —
// their raw errors are load-bearing for the tripwires above.
var checkViolationMappedToInvalid = map[string]struct{}{
	"apps_workload_class_chk": {}, // SetAppWorkloadClass (PR-B)
	"scan_source_tier_chk":    {}, // SetProjectScanSource tier enum
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgerrcode.UniqueViolation:
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.ConstraintName)
		case pgerrcode.CheckViolation:
			if pgErr.ConstraintName == "app_has_object_buckets" {
				return ErrConflict
			}
			// CHECK violations surface as ErrInvalidArgument ONLY
			// for the constraints named in
			// checkViolationMappedToInvalid — the rest bubble the
			// raw SQLSTATE so tripwire tests like
			// TestPgStore_InstancesStateCheck_RejectsBogusState can
			// substring-match `23514` and a future widening of
			// `instances.state` to text is visible at the test
			// boundary. The empty-class guard in
			// SetAppWorkloadClass covers Go-side validation; this
			// mapping covers schema-side defence-in-depth for the
			// three named constraints.
			if _, ok := checkViolationMappedToInvalid[pgErr.ConstraintName]; ok {
				return fmt.Errorf("%w: %s", ErrInvalidArgument, pgErr.ConstraintName)
			}
			return err
		}
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

// nullJSONRaw returns nil for an empty json.RawMessage so the DB column
// is NULL rather than the byte string "{}" or "null". Used by the
// CreateDeployment INSERT for the override_*_env / override_healthcheck
// jsonb columns (issue #460 / ADR-053) — a deployment that didn't
// carry an override writes NULL, not an empty object.
//
// json.RawMessage IS []byte, so the non-empty branch is a direct
// return — no conversion needed. The redirection to `any` here
// gates the value through pgx's encode path; the slice type is
// preserved on the wire so pgx sends the raw bytes.
func nullJSONRaw(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// notNullEmptyJSONRaw is the sidecar-shape variant of nullJSONRaw
// (issue #463 / ADR-066 / migration 00095). The `deployments.sidecars`
// column is jsonb NOT NULL DEFAULT '[]'::jsonb, so an explicit NULL
// parameter at INSERT would 23502-fail (DEFAULT only applies to
// columns not mentioned in the column list). This helper converts
// an empty json.RawMessage to the literal `'[]'::jsonb' string so
// the wire value is a valid 0-sidecar payload that satisfies the
// NOT NULL constraint. Non-empty bytes pass through verbatim (pgx
// sends them as raw jsonb bytes).
//
// Why a literal string instead of a byte slice: pgx's jsonb codec
// inspects the value's Go type — a `[]byte` is encoded as raw
// bytes, but for a literal cast we need to drive the value through
// the text protocol as a parameter. The string form is the smallest
// ergonomic shape that both pgx and the Postgres jsonb parser
// unambiguously accept.
func notNullEmptyJSONRaw(b json.RawMessage) any {
	if len(b) == 0 {
		return "[]" // pgx encodes Go string as text → jsonb parser sees `[]`
	}
	return b
}

// nullableOverridePort returns nil when port is 0 (the "absent" sentinel
// for CreateDeploymentOverrides.Port) so the column reads NULL on a
// round-trip; otherwise the int value. Mirrors nullableInt's zero-to-NULL
// rule but keeps the override-intent explicit at the call site so a
// future reader can grep for it.
func nullableOverridePort(p int) any {
	if p == 0 {
		return nil
	}
	return p
}

// logExcerptsJSON encodes a []api.LogExcerpt as the pgx driver value
// for the deployments.error_relevant_logs jsonb column. The empty
// (nil or zero-length) case maps to nil so pgx sends NULL — the
// column was added by migrations/00290 and pre-cluster rows have no
// relevant-logs payload. Non-empty slices are JSON-encoded as a
// compact byte slice so pgx's jsonb codec parses them as a real
// jsonb array (not as text).
//
// Why not notNullEmptyJSONRaw like sidecars: the error_relevant_logs
// column is jsonb with no NOT NULL constraint (migration 00290), so
// an empty slice is correctly represented as NULL. The sidecars
// column has a NOT NULL DEFAULT '[]'::jsonb contract which forces
// the literal '[]' for empty inputs — different column, different
// rule.
func logExcerptsJSON(logs []api.LogExcerpt) any {
	if len(logs) == 0 {
		return nil
	}
	b, err := json.Marshal(logs)
	if err != nil {
		// json.Marshal on a []api.LogExcerpt cannot fail under the
		// fixed schema (ts/level/source/message are all string), so
		// this branch is defensive — return nil so the column writes
		// NULL rather than aborting the deployment-failed path.
		return nil
	}
	return b
}

// cidrPrefixesToArray renders a Go []netip.Prefix as a pgx driver value
// for a cidr[] column. Empty/nil renders as a literal `'{}'` string so
// Postgres casts it to an empty array (matches the column default of
// '{}' from migration 00029). Non-empty renders as a Postgres array
// literal: '{1.2.3.0/24,8.8.8.0/24}'. Bypasses pgx's array codec
// because it doesn't have a clean cidr[] element type by default and
// building the literal here keeps the cidr parse surface on the Go
// side (validation already ran in cmd/apid).
//
// Returns the wire shape pgx expects: a single string that Postgres
// casts to cidr[]. The cast in the surrounding SQL is `$N::cidr[]`.
func cidrPrefixesToArray(prefixes []netip.Prefix) string {
	if len(prefixes) == 0 {
		return "{}"
	}
	parts := make([]string, len(prefixes))
	for i, p := range prefixes {
		parts[i] = p.String()
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// cidrTextToPrefixes parses the text rendering of a Postgres cidr[]
// column back to []netip.Prefix. The rendered shape from pgx is
// '{1.2.3.0/24,8.8.8.0/24}' (with surrounding braces, no spaces between
// entries). Empty: '{}' or '[]' depending on pgx version; both render
// as no entries. We split on top-level commas only (entries are CIDR
// strings, no embedded commas), so a naïve Split is correct.
//
// On any parse failure we return an empty slice and rely on the caller
// to fail loud — none of the consumers silently swallow a malformed
// allowlist because (a) the API layer validates each entry with
// netip.ParsePrefix before insert, and (b) the DB trigger
// (apps_egress_allowlist_cidr, migration 00033) rejects bogus entries
// at the schema. Defensive parse is belt-and-braces, not load-bearing.
func cidrTextToPrefixes(text string) []netip.Prefix {
	text = strings.TrimSpace(text)
	if text == "{}" || text == "" {
		return nil
	}
	// Strip surrounding braces if present.
	if len(text) >= 2 && text[0] == '{' && text[len(text)-1] == '}' {
		text = text[1 : len(text)-1]
	}
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, p := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		out = append(out, prefix)
	}
	return out
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

// DeleteOldEvents (ADR-075) prunes audit-log events whose `at` is
// older than the cutoff. Returns the row count. The
// (subject, at desc) partial index on the events table keeps the
// WHERE a partial range scan; the per-tick cost is bounded by
// (cutoff × recent rows added in that window).
//
// Used by pkg/eventretention's daily loop; the maintenance floor
// is 90 days (SOC 2 CC6.2).
func (s *PgStore) DeleteOldEvents(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from events where at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SetAccountPassword upserts the Argon2id PHC hash for an account.
// ON CONFLICT (account_id) DO UPDATE so a password change / set
// flow replaces the prior hash atomically. The PK on account_id is
// the floor — a racing concurrent SetAccountPassword on the same
// account is linearised at the database; the ON CONFLICT branch
// keeps the second writer's hash instead of dropping it (mirrors
// the MemStore overwrite-under-lock).
//
// phc is the PHC wire format from pkg/auth.Encode. UpdatedAt is
// stamped by `now()` so the "rotate hash on login" PR #2.5
// hardening has a stable reference.
func (s *PgStore) SetAccountPassword(ctx context.Context, accountID, phc string) error {
	if accountID == "" || phc == "" {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx,
		`insert into account_passwords (account_id, hash, updated_at)
		 values ($1, $2, now())
		 on conflict (account_id) do update
		   set hash = excluded.hash,
		       updated_at = excluded.updated_at`,
		accountID, phc)
	return mapErr(err)
}

// AccountPasswordByAccountID returns the stored Argon2id PHC hash
// for an account, or ErrNotFound when no row exists. Used by the
// postLogin handler as the trigger for the anti-enumeration
// Argon2id pad (pkg/auth.DummyPHC).
func (s *PgStore) AccountPasswordByAccountID(ctx context.Context, accountID string) (string, error) {
	var phc string
	err := s.pool.QueryRow(ctx,
		`select hash from account_passwords where account_id = $1`, accountID,
	).Scan(&phc)
	if err != nil {
		return "", mapErr(err)
	}
	return phc, nil
}

// DeleteAccountPassword removes the Argon2id row for an account.
// Idempotent (DELETE with zero affected rows is not an error);
// matches the MemStore's delete-on-missing semantics.
func (s *PgStore) DeleteAccountPassword(ctx context.Context, accountID string) error {
	if accountID == "" {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx,
		`delete from account_passwords where account_id = $1`, accountID)
	return mapErr(err)
}

// UpsertOAuthLink writes the (provider, provider_subject) → account
// row. The composite PK enforces the §11 anti-takeover invariant:
// re-bind by the SAME account updates email/email_verified in place
// (ON CONFLICT ... DO UPDATE with WHERE account_id = excluded.account_id,
// so a different account attempting to claim the same (provider, sub)
// pair triggers the unique-violation → ErrConflict path). The
// account_id equality check inside the WHERE clause is the load-bearing
// bit: without it, an attacker with the same email could overwrite
// the victim's link row. The dashboard refreshes its own email on
// Google account-rename; the WHERE clause keeps that case working.
func (s *PgStore) UpsertOAuthLink(ctx context.Context, accountID, provider, providerSubject, email string, emailVerified bool) error {
	if accountID == "" || provider == "" || providerSubject == "" {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx,
		`insert into oauth_links (provider, provider_subject, account_id, email, email_verified)
		 values ($1, $2, $3, $4, $5)
		 on conflict (provider, provider_subject) do update
		   set email = excluded.email,
		       email_verified = excluded.email_verified
		 where oauth_links.account_id = excluded.account_id`,
		provider, providerSubject, accountID, email, emailVerified)
	return mapErr(err)
}

// OAuthLinkByProviderSubject returns the link for a (provider, sub)
// pair, or ErrNotFound when no row matches. The OAuth callback runs
// this on every handshake; the sub-first lookup is the §11
// anti-takeover closure (the first party to bind a sub owns the row).
func (s *PgStore) OAuthLinkByProviderSubject(ctx context.Context, provider, providerSubject string) (OAuthLink, error) {
	var link OAuthLink
	err := s.pool.QueryRow(ctx,
		`select provider, provider_subject, account_id, email, email_verified, created_at
		   from oauth_links
		  where provider = $1 and provider_subject = $2`,
		provider, providerSubject,
	).Scan(&link.Provider, &link.ProviderSubject, &link.AccountID, &link.Email, &link.EmailVerified, &link.CreatedAt)
	if err != nil {
		return OAuthLink{}, mapErr(err)
	}
	return link, nil
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
	activeBuckets, err := sqlc.New().ObjectBucketCountForAccount(ctx, tx, mustPgUUID(id))
	if err != nil {
		return fmt.Errorf("state: count account object buckets: %w", err)
	}
	if activeBuckets != 0 {
		return ErrConflict
	}
	// Only metadata for confirmed-deleted upstream buckets may be purged.
	// Active-bucket FKs also guard against a concurrent reservation.
	if err := sqlc.New().ObjectBucketPruneTombstones(ctx, tx, mustPgUUID(id)); err != nil {
		return fmt.Errorf("state: prune deleted object bucket metadata: %w", err)
	}

	// Capture email at copy-time for the audit_log row (issue #755 /
	// PR-6). The audit_log table is FK-free; the row outlives the
	// account, so the email must be inlined so a regulator reading
	// a post-deletion row has the human identifier without joining
	// back to a deleted accounts row. Read inside the same tx with
	// the deleted_pending predicate so we get the same race-guard
	// semantics as the parent DELETE: if a restore raced us, we
	// won't see status='deleted_pending' and the email is empty.
	//
	// Empty email is a tolerated outcome for the anonymous test
	// accounts that have no email column populated — the audit
	// row still records kind=account.deleted + actor=grace-sweep.
	var accountEmail string
	err = tx.QueryRow(ctx,
		`select email from accounts where id = $1 and status = 'deleted_pending'`,
		id,
	).Scan(&accountEmail)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("state: read email for %s: %w", id, err)
	}

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
		// app_envs: matches app_secrets posture above — env rows hold
		// account_id directly, so the FK + GDPR cascade handles them.
		// Plaintext text values are erased with the account.
		{"app_envs", `delete from app_envs where account_id = $1`},
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

	// audit_log backfill (issue #755 / PR-6). Writes one row to the
	// FK-free audit_log table so the post-deletion state is preserved
	// as regulator / DPO evidence. Lives inside the same tx so the
	// audit row is atomic with the accounts row delete — a separate
	// tx could commit the audit row before the parent delete, or vice
	// versa, breaking the atomicity invariant.
	//
	// Placement matters: this runs AFTER the `delete from events`
	// step (the events rows are the per-event audit trail that gets
	// erased under GDPR right-to-erasure, spec §17 G6) and BEFORE
	// the parent `delete from accounts` (so the audit row still
	// points at a parent row in the tx's read view — even though the
	// audit_log row is FK-free by spec, ordering the writes keeps the
	// audit_log row's account_id coherent with the parent delete's
	// sentinel check below).
	auditID := uuid.New()
	auditAt := time.Now().UTC()
	auditPayload, mErr := json.Marshal(map[string]string{
		"source": "grace-sweep",
		"email":  accountEmail,
		"actor":  "grace-sweep",
	})
	if mErr != nil {
		return fmt.Errorf("state: marshal audit_log payload for %s: %w", id, mErr)
	}
	// AuditLog.AccountID is *uuid.UUID (not *string) — mirror the
	// migration's UUID column. If the inbound id is unparseable
	// (it should never be — the grace sweep reads it from
	// accounts.id), leave AccountID nil rather than failing the
	// entire tx; the audit_log row still records the deletion.
	var auditAccountID *uuid.UUID
	if parsed, parseErr := uuid.Parse(id); parseErr == nil {
		auditAccountID = &parsed
	}
	auditEntry := AuditLog{
		ID:           auditID,
		Kind:         AuditLogKindAccountDeleted,
		AccountID:    auditAccountID,
		AccountEmail: accountEmail,
		Actor:        "grace-sweep",
		ReceivedAt:   auditAt,
		Data:         auditPayload,
	}
	if err := s.insertAuditLogTx(ctx, tx, auditEntry); err != nil {
		return fmt.Errorf("state: insert audit_log for %s: %w", id, err)
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
		        b.started_at, b.finished_at, b.enqueued_at, b.cancelled_at, b.cancelled_by_deployment_cascade
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
		// pgtype.Timestamptz for nullable timestamps (same
		// pattern as ListBuildsForAccountPaged immediately
		// below and scanBuild at pgstore.go:10962): &b.StartedAt
		// / &b.FinishedAt are *time.Time (Build fields are not
		// pointers) and pgx v5.10.0 rejects NULL into a fresh
		// *time.Time under rows.Scan.
		var startedAt, finishedAt, cancelledAt pgtype.Timestamptz
		if err := rows.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes, &statusStr, &fc, &b.LogPath, &startedAt, &finishedAt, &b.EnqueuedAt, &cancelledAt, &b.CancelledByDeploymentCascade); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			b.StartedAt = startedAt.Time
		}
		if finishedAt.Valid {
			b.FinishedAt = finishedAt.Time
		}
		if cancelledAt.Valid {
			t := cancelledAt.Time
			b.CancelledAt = &t
		}
		b.Kind = DeploymentKind(kind)
		b.Status = BuildStatus(statusStr)
		b.FailureClass = FailureClass(fc)
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBuildsForAccountPaged returns one page of builds across the
// account's deployments, ordered started_at desc nulls last with
// id DESC as the tiebreaker (DEPLOY-PROV-6 follow-up / ADR-091,
// issue #741 close-out, post-review fix).
//
// Keyset pagination: pass the previous response's (started_at, id)
// tuple as (before.Time, beforeID) to page backwards. before.IsZero()
// = first page (beforeID ignored). The id tiebreaker is what makes
// pagination deterministic for queued builds (started_at IS NULL)
// AND for sub-second collisions on started_at — without it, rows
// whose started_at lands in the same wall-clock second as the
// cursor are dropped on the next page (whole-second wire format
// vs. sub-second DB precision), and queued-only pages lose the
// cursor entirely (no non-null started_at to anchor it on).
//
// The query is supported by builds_deployment_started_idx
// (migrations/00197, originally renumbered 166 → 191 → 193 → 195
// → 197 during cross-PR reviews — each renumber was forced by a
// sibling PR's reservation fence landing in the same slot mid-
// rebase; the slot family uses the canonical cross-pr-slot-gate-
// reservation-fence-pattern + drop-on-rebase cleanup; the latest
// cycle is 195 → 197 after PR #819 placed a 195_reserve_slot fence
// + 196 webhook_event_allowlist_cron_fired_manually real migration
// on its branch, so PR #803 jumped to 197, the next free slot beyond
// PR #819's 196). The leading deployment_id column lets the planner's
// nested-loop strategy probe each outer deployment row's builds via
// a bounded range scan instead of fetching + filtering in-memory.
// The DESC NULLS LAST ordering matches the SQL surface so queued
// builds stay at the bottom of every page.
//
// limit is clamped server-side by the handler (1..200).
func (s *PgStore) ListBuildsForAccountPaged(
	ctx context.Context, accountID, statusFilter, appIDFilter string,
	before time.Time, beforeID string, limit int,
) ([]Build, error) {
	var (
		rows pgx.Rows
		err  error
	)
	// Branch order is load-bearing (post-review fix): the
	// "queued-tail cursor" case has `before.IsZero() &&
	// beforeID != ""` — the started_at segment is empty but the
	// id segment anchors the boundary in the NULL zone. If we
	// tested `before.IsZero()` FIRST it would swallow that case
	// (the very first page branch has no keyset predicate, and
	// "no started_at" doesn't mean "no cursor"). So the queued-
	// tail branch is checked before the "first page" branch.
	switch {
	case before.IsZero() && beforeID != "":
		// Queued-tail cursor contract: empty started_at segment
		// in the opaque cursor — caller is asking for rows
		// AFTER a queued tail boundary. The keyset becomes id-
		// only in the NULL zone: "rows with started_at IS NULL
		// AND id < beforeID". All non-NULL rows (started_at
		// set) have already been returned in pages with non-
		// empty started_at cursors; the queued tail is the
		// last zone to walk. ORDER BY id DESC walks the queued
		// tail from newest-id to oldest-id.
		rows, err = s.pool.Query(ctx,
			`select b.id, b.deployment_id, b.kind, b.source_bytes, b.status,
			        coalesce(b.failure_class,''), coalesce(b.log_path,''),
			        b.started_at, b.finished_at, b.enqueued_at, b.cancelled_at, b.cancelled_by_deployment_cascade
			 from builds b
			 join deployments d on d.id = b.deployment_id
			 join apps a on a.id = d.app_id
			 where a.account_id = $1
			   and ($2 = '' or b.status = $2)
			   and ($3 = '' or d.app_id = $3::uuid)
			   and b.started_at is null
			   and b.id < $4
			 order by b.id desc
			 limit $5`, accountID, statusFilter, appIDFilter, beforeID, limit)
	case before.IsZero() && beforeID == "":
		// First page: no keyset predicate, just the ordering +
		// limit. id DESC is the stable tiebreaker that makes
		// page-2's WHERE clause deterministic.
		rows, err = s.pool.Query(ctx,
			`select b.id, b.deployment_id, b.kind, b.source_bytes, b.status,
			        coalesce(b.failure_class,''), coalesce(b.log_path,''),
			        b.started_at, b.finished_at, b.enqueued_at, b.cancelled_at, b.cancelled_by_deployment_cascade
			 from builds b
			 join deployments d on d.id = b.deployment_id
			 join apps a on a.id = d.app_id
			 where a.account_id = $1
			   and ($2 = '' or b.status = $2)
			   and ($3 = '' or d.app_id = $3::uuid)
			 order by b.started_at desc nulls last, b.id desc
			 limit $4`, accountID, statusFilter, appIDFilter, limit)
	default:
		// Keyset (started_at, id) < (before, beforeID) under
		// the DESC NULLS LAST ordering. Naively encoded as a
		// row-value comparison it WOULD be `(b.started_at,
		// b.id) < ($4, $5)` — but PG's row-value `<` is three-
		// valued for tuples with NULL elements: a `(NULL, x) <
		// (T, y)` row returns NULL and is therefore excluded
		// by WHERE, which silently drops queued tails (started_at
		// IS NULL) past page boundaries.
		//
		// The fix is a disjunction that respects all of the
		// ordering cases:
		//   1. earlier started_at          → included
		//   2. equal started_at, smaller id → included
		//   3. NULL started_at             → included (queued
		//      zone falls AFTER every non-NULL row in DESC
		//      NULLS LAST ordering, so any queued row is
		//      strictly less than a non-NULL cursor)
		// Case 3 ignores the id because once we're in the
		// NULL zone, the ordering is by id DESC. The page-2
		// result, in id-DESC order, advances the cursor to
		// the SMALLEST queued id, which then anchors the
		// queued-tail branch (case 1 of the switch above)
		// on subsequent pages.
		rows, err = s.pool.Query(ctx,
			`select b.id, b.deployment_id, b.kind, b.source_bytes, b.status,
			        coalesce(b.failure_class,''), coalesce(b.log_path,''),
			        b.started_at, b.finished_at, b.enqueued_at, b.cancelled_at, b.cancelled_by_deployment_cascade
			 from builds b
			 join deployments d on d.id = b.deployment_id
			 join apps a on a.id = d.app_id
			 where a.account_id = $1
			   and ($2 = '' or b.status = $2)
			   and ($3 = '' or d.app_id = $3::uuid)
			   and (
			       b.started_at < $4
			       or (b.started_at = $4 and b.id < $5)
			       or b.started_at is null
			   )
			 order by b.started_at desc nulls last, b.id desc
			 limit $6`, accountID, statusFilter, appIDFilter, before, beforeID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		b := Build{}
		var kind, statusStr, fc string
		// pgtype.Timestamptz is the canonical nullable timestamp
		// reader here (mirrors scanBuild at pgstore.go:10962):
		// &b.StartedAt / &b.FinishedAt are *time.Time (Build
		// fields are not pointers), and pgx v5.10.0 refuses
		// NULL into a fresh *time.Time under rows.Scan. The
		// pgtype wrapper encodes NULL via .Valid = false so
		// the inner `b.StartedAt = ...` only fires on a real
		// timestamp.
		var startedAt, finishedAt, cancelledAt pgtype.Timestamptz
		if err := rows.Scan(&b.ID, &b.DeploymentID, &kind, &b.SourceBytes,
			&statusStr, &fc, &b.LogPath, &startedAt, &finishedAt, &b.EnqueuedAt, &cancelledAt, &b.CancelledByDeploymentCascade); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			b.StartedAt = startedAt.Time
		}
		if finishedAt.Valid {
			b.FinishedAt = finishedAt.Time
		}
		if cancelledAt.Valid {
			t := cancelledAt.Time
			b.CancelledAt = &t
		}
		b.Kind = DeploymentKind(kind)
		b.Status = BuildStatus(statusStr)
		b.FailureClass = FailureClass(fc)
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListCronsForAccount walks every cron tied to the account's apps.
// Used by the GDPR export bundle. Ordered by created_at desc so the
// newest crons surface first.
func (s *PgStore) ListCronsForAccount(ctx context.Context, accountID string) ([]Cron, error) {
	rows, err := s.pool.Query(ctx,
		`select c.id, c.app_id, c.schedule, c.path, c.enabled, c.created_at
		 from crons c
		 join apps a on a.id = c.app_id
		 where a.account_id = $1
		 order by c.created_at desc`, accountID)
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
			`select account_id, app_id, date_trunc('month', minute AT TIME ZONE 'UTC') as month,
			        sum(mb_seconds)::bigint, sum(requests)::bigint
			 from usage_minutes
			 where account_id = $1
			 group by account_id, app_id, month
			 order by app_id, month`, accountID)
	} else {
		rows, err = s.pool.Query(ctx,
			`select account_id, app_id, date_trunc('month', minute AT TIME ZONE 'UTC') as month,
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
	// Empty request_id round-trips as NULL so the partial unique
	// index (gdpr_requests_request_id_idx) excludes it from the
	// index entirely — a NULL request_id is the "no inbound id"
	// path, distinct from a real id.
	_, err := s.pool.Exec(ctx,
		`insert into gdpr_requests
		   (id, account_id, account_email, action, requested_at, completed_at, request_id)
		 values ($1, $2, $3, $4, $5, $6, nullif($7, ''))`,
		r.ID, r.AccountID, r.AccountEmail, string(r.Action),
		r.RequestedAt.UTC(), nullableTimestamptz(r.CompletedAt), r.RequestID)
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
		`select id, account_id, account_email, action, requested_at, completed_at, request_id
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
			requestID   pgtype.Text // NULL = no inbound X-Request-Id (PR-5.2)
		)
		if err := rows.Scan(&g.ID, &g.AccountID, &g.AccountEmail,
			&g.Action, &g.RequestedAt, &completedAt, &requestID); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			g.CompletedAt = completedAt.Time
		}
		if requestID.Valid {
			g.RequestID = requestID.String
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CountGdprRequestsSince is the rate-limit probe for
// GET /v1/account/export (issue #755 / PR-5.1). Returns how many
// (account_id, action) ledger rows landed at or after since. The
// gdpr_requests_account_action_idx partial index covers the
// (account_id, action, requested_at desc) prefix so this is an
// index-only count even for accounts with long export histories.
// limit=0 callers should pass since=time.Now() and check >1.
func (s *PgStore) CountGdprRequestsSince(ctx context.Context, accountID, action string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*)::int
		   from gdpr_requests
		  where account_id = $1
		    and action = $2
		    and requested_at >= $3`,
		accountID, action, since.UTC()).Scan(&n)
	return n, err
}

// FindGdprRequestByRequestID is the idempotency probe for
// GET /v1/account/export (issue #755 / PR-5.2). Returns the most
// recent ledger row matching (account_id, request_id) when one
// exists, or ErrNotFound when it does not. Backed by the partial
// index gdpr_requests_request_id_idx so the lookup is O(log n)
// even for accounts with millions of legacy rows where most
// request_ids are NULL (NULL rows are excluded by the WHERE
// clause). Returns (zero, ErrNotFound) when no row matches.
func (s *PgStore) FindGdprRequestByRequestID(ctx context.Context, accountID, requestID string) (GdprRequest, error) {
	if requestID == "" {
		return GdprRequest{}, ErrNotFound
	}
	var (
		g           GdprRequest
		completedAt pgtype.Timestamptz
	)
	err := s.pool.QueryRow(ctx,
		`select id, account_id, account_email, action, requested_at, completed_at, request_id
		   from gdpr_requests
		  where account_id = $1
		    and request_id = $2
		  order by requested_at desc
		  limit 1`, accountID, requestID).
		Scan(&g.ID, &g.AccountID, &g.AccountEmail,
			&g.Action, &g.RequestedAt, &completedAt, &g.RequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GdprRequest{}, ErrNotFound
		}
		return GdprRequest{}, err
	}
	if completedAt.Valid {
		g.CompletedAt = completedAt.Time
	}
	return g, nil
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

// nullableTimestamptzPtr is the *time.Time sibling of
// nullableTimestamptz. A nil pointer produces pgtype.Timestamptz{Valid:false}
// so the column round-trips as NULL. Used by IAM-5's
// CreateAPIKeyWithExpiry and the SetAccountKeyGraceWindow path
// where the input is naturally a pointer (the zero value of
// *time.Time is nil, the zero value of time.Time is "0001-01-01"
// which is NOT a NULL).
func nullableTimestamptzPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// scanAPIKeyRow runs a query and scans the result into a *APIKey.
// Used by multi-statement transactions (RotateAPIKey) where the
// helper needs to participate in a tx rather than the pool. The
// column projection is the same eleven-column shape scanAPIKey
// expects; both helpers share the post-scan logic via a small
// inline pass-through that the caller doesn't have to repeat.
func scanAPIKeyRow(ctx context.Context, tx pgx.Tx, sql string, args ...any) (APIKey, error) {
	row := tx.QueryRow(ctx, sql, args...)
	k, err := scanAPIKey(row)
	if err != nil {
		return APIKey{}, err
	}
	return k, nil
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

// --- IAM-3 sessions (ADR-039, issue #187 + #244 merged) ---------------------
//
// One row per dashboard login. The cookie envelope carries the row's
// uuid as `sid`; every authenticated dashboard request re-validates
// the row before the handler runs. Revocation is `revoked_at != nil`;
// LastSeenAt may update post-revocation (operational signal only).
//
// IDOR protection lives at the SQL `account_id = $2` predicate — a
// cross-account DELETE returns 0 rows (handler maps false → 404).
// The MemStore mirrors this with an in-memory AccountID equality
// check (memstore.go, same section header).

const sessionSelectCols = `
	coalesce(host(issued_ip), '') as issued_ip,
	coalesce(issued_ua, '') as issued_ua,
	coalesce(binding_hash, '') as binding_hash`

func (s *PgStore) CreateSession(ctx context.Context, id, accountID, issuedIP, issuedUA string) (Session, error) {
	row := s.pool.QueryRow(ctx, `
		insert into sessions (id, account_id, issued_ip, issued_ua)
		values ($1, $2, nullif($3, '')::inet, nullif($4, ''))
		returning id, account_id, issued_at, last_seen_at, revoked_at, `+sessionSelectCols,
		id, accountID, issuedIP, issuedUA)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, mapErr(err)
	}
	return sess, nil
}

// CreateSessionWithBinding is the IAM-hardening-mega-PR
// (logical change 5) variant. The bindingHash parameter is the
// HMAC-SHA256 fingerprint of (ip, ua_family) — same value the
// cookie envelope stamps. Empty string → NULL column.
func (s *PgStore) CreateSessionWithBinding(ctx context.Context, id, accountID, issuedIP, issuedUA, bindingHash string) (Session, error) {
	row := s.pool.QueryRow(ctx, `
		insert into sessions (id, account_id, issued_ip, issued_ua, binding_hash)
		values ($1, $2, nullif($3, '')::inet, nullif($4, ''), nullif($5, ''))
		returning id, account_id, issued_at, last_seen_at, revoked_at, `+sessionSelectCols,
		id, accountID, issuedIP, issuedUA, bindingHash)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, mapErr(err)
	}
	return sess, nil
}

func (s *PgStore) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.pool.QueryRow(ctx,
		`select id, account_id, issued_at, last_seen_at, revoked_at, `+sessionSelectCols+`
		   from sessions where id = $1`, id)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, mapErr(err)
	}
	return sess, nil
}

// RevokeSession atomically stamps revoked_at iff (a) the row exists,
// (b) it belongs to accountID, and (c) it is not already revoked.
// Returns true on real write, false on no-op. The account_id check
// is the SQL IDOR guard; pgx.RowsAffected()==0 covers all three
// no-op cases (gone / wrong-account / already-revoked) without
// leaking existence.
func (s *PgStore) RevokeSession(ctx context.Context, id, accountID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`update sessions set revoked_at = coalesce(revoked_at, now())
		   where id = $1 and account_id = $2 and revoked_at is null`,
		id, accountID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UpdateSessionBinding re-stamps sessions.binding_hash on a live
// row. The IAM-hardening-mega-PR (logical change 5) reissue path
// (cmd/apid/handlers_mfa.go reissueSessionCookieWithStepUp) calls
// this so the row's fingerprint tracks the cookie envelope's
// fingerprint across /confirm + /verify + /recover + /disable.
//
// IDOR: the (id, account_id) predicate is the same shape as
// RevokeSession — a cross-account update returns 0 rows which
// surfaces as ErrNotFound to the handler. Revoked rows are
// excluded: a session that the operator (or the auto-revoke
// branch in middleware.go) already revoked must not be revived
// by a stale reissue from a stolen cookie that won the race.
//
// Empty bindingHash → NULL column (the unix-socket / CLI-auth
// path has no meaningful fingerprint).
func (s *PgStore) UpdateSessionBinding(ctx context.Context, id, accountID, bindingHash string) error {
	tag, err := s.pool.Exec(ctx,
		`update sessions set binding_hash = nullif($3, '')
		   where id = $1 and account_id = $2 and revoked_at is null`,
		id, accountID, bindingHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListSessions(ctx context.Context, accountID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, issued_at, last_seen_at, revoked_at, `+sessionSelectCols+`
		   from sessions where account_id = $1 and revoked_at is null
		   order by issued_at desc`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeAllSessions revokes every active row for accountID except
// the supplied sid (the calling session). Returns the count via
// pgx.RowsAffected.
func (s *PgStore) RevokeAllSessions(ctx context.Context, accountID, exceptID string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`update sessions set revoked_at = now()
		   where account_id = $1 and id <> $2 and revoked_at is null`,
		accountID, exceptID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// TouchSessionLastSeen stamps last_seen_at = now(). Best-effort,
// fire-and-forget. Allowed on revoked rows. Missing rows are
// silent no-ops — a race between GetSession (missing) and Touch
// is benign.
func (s *PgStore) TouchSessionLastSeen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`update sessions set last_seen_at = now() where id = $1`, id)
	return err
}

// --- Organizations (ADR-061 / IAM-6, PR 2) -------------------------------
//
// PR 2 is the additive-schema milestone: schema lands here, apid stays
// unchanged. Every method here uses inline SQL on s.pool for simple CRUD
// (handles ErrNotFound / ErrConflict via pgerrcode); the tx-heavy
// ConsumeOrgInvitation / RemoveOrgMember last-owner paths open their own
// pgx.Tx and follow the BeginTx precedent at line 226 / 709 / 1269.

// CreateOrg inserts a new org row. Slug collision returns ErrConflict;
// the partial unique on personal_owner_account_id WHERE personal_org is
// also caught here and surfaced as ErrConflict. Returns ErrConflict on
// any 23505 in this method.
func (s *PgStore) CreateOrg(ctx context.Context, o Org) (Org, error) {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.Plan == "" {
		o.Plan = api.PlanFree
	}
	if o.Status == "" {
		o.Status = OrgStatusActive
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = o.CreatedAt
	}
	var personalOwner *string
	if o.PersonalOwnerAccountID != nil {
		po := *o.PersonalOwnerAccountID
		personalOwner = &po
	}
	tag, err := s.pool.Exec(ctx, `
		insert into orgs (
			id, slug, name, personal_org, personal_owner_account_id,
			plan, status, provider_customer_id, stripe_subscription_item,
			deleted_pending, created_at, updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, o.ID, o.Slug, o.Name, o.Personal, personalOwner,
		string(o.Plan), string(o.Status),
		nullIfEmpty(o.ProviderCustomerID), nullIfEmpty(o.StripeSubscriptionItem),
		o.DeletedPending, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return Org{}, ErrConflict
		}
		return Org{}, fmt.Errorf("state: create org: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Org{}, fmt.Errorf("state: create org rows=%d", tag.RowsAffected())
	}
	return o, nil
}

// nullIfEmpty maps "" → NULL for nullable text columns.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// OrgByID is the canonical primary-key lookup.
func (s *PgStore) OrgByID(ctx context.Context, id string) (Org, error) {
	row := s.pool.QueryRow(ctx, `
		select id, slug, name, personal_org, personal_owner_account_id,
		       plan, status, provider_customer_id, stripe_subscription_item,
		       deleted_pending, created_at, updated_at
		  from orgs where id = $1
	`, id)
	return scanOrg(row)
}

// OrgBySlug case-folds the slug (matches orgs_slug_uniq).
func (s *PgStore) OrgBySlug(ctx context.Context, slug string) (Org, error) {
	row := s.pool.QueryRow(ctx, `
		select id, slug, name, personal_org, personal_owner_account_id,
		       plan, status, provider_customer_id, stripe_subscription_item,
		       deleted_pending, created_at, updated_at
		  from orgs where lower(slug) = lower($1)
	`, slug)
	return scanOrg(row)
}

// OrgByPersonalAccount returns the unique personal-org row.
func (s *PgStore) OrgByPersonalAccount(ctx context.Context, accountID string) (Org, error) {
	row := s.pool.QueryRow(ctx, `
		select id, slug, name, personal_org, personal_owner_account_id,
		       plan, status, provider_customer_id, stripe_subscription_item,
		       deleted_pending, created_at, updated_at
		  from orgs
		 where personal_org = true and personal_owner_account_id = $1
	`, accountID)
	return scanOrg(row)
}

// ListOrgsForAccount JOINs the active memberships for an account.
func (s *PgStore) ListOrgsForAccount(ctx context.Context, accountID string) ([]Org, error) {
	rows, err := s.pool.Query(ctx, `
		select distinct o.id, o.slug, o.name, o.personal_org, o.personal_owner_account_id,
		       o.plan, o.status, o.provider_customer_id, o.stripe_subscription_item,
		       o.deleted_pending, o.created_at, o.updated_at
		  from orgs o
		  join org_memberships m on m.org_id = o.id
		 where m.account_id = $1 and m.removed_at is null
		 order by o.slug
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list orgs for account: %w", err)
	}
	defer rows.Close()
	out := make([]Org, 0)
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOrgPlan / UpdateOrgName / UpdateOrgStatus / SoftDeleteOrg are
// mirror updates. Each stamps `updated_at = now()` so the wire shape's
// UpdatedAt is monotonic per row.
func (s *PgStore) UpdateOrgPlan(ctx context.Context, id string, plan api.Plan) error {
	tag, err := s.pool.Exec(ctx, `update orgs set plan = $2, updated_at = now() where id = $1`, id, string(plan))
	if err != nil {
		return fmt.Errorf("state: update org plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateOrgName is the name half of the PATCH /v1/orgs/{slug} contract
// (PR 5). The SQL CHECK on orgs.name rejects empty strings and names
// >256 bytes with sqlstate 23514 — the handler trims + bounds before
// reaching here so the surface is always 200/404. Returns ErrNotFound
// when no row matches.
func (s *PgStore) UpdateOrgName(ctx context.Context, id, name string) error {
	tag, err := s.pool.Exec(ctx, `update orgs set name = $2, updated_at = now() where id = $1`, id, name)
	if err != nil {
		return fmt.Errorf("state: update org name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) UpdateOrgStatus(ctx context.Context, id string, status OrgStatus) error {
	tag, err := s.pool.Exec(ctx, `update orgs set status = $2, updated_at = now() where id = $1`, id, string(status))
	if err != nil {
		return fmt.Errorf("state: update org status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) SoftDeleteOrg(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		update orgs
		   set status = 'deleted_pending', deleted_pending = true, updated_at = now()
		 where id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("state: soft delete org: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanOrg reads the canonical column tuple into Org. Used by every OrgBy*
// reader and ListOrgsForAccount.
func scanOrg(r rowScanner) (Org, error) {
	var o Org
	var plan, status string
	var personalOwner *string
	var providerCustomer, stripeSub *string
	if err := r.Scan(
		&o.ID, &o.Slug, &o.Name, &o.Personal, &personalOwner,
		&plan, &status, &providerCustomer, &stripeSub,
		&o.DeletedPending, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Org{}, ErrNotFound
		}
		return Org{}, fmt.Errorf("state: scan org: %w", err)
	}
	if personalOwner != nil {
		po := *personalOwner
		o.PersonalOwnerAccountID = &po
	}
	if providerCustomer != nil {
		o.ProviderCustomerID = *providerCustomer
	}
	if stripeSub != nil {
		o.StripeSubscriptionItem = *stripeSub
	}
	o.Plan = api.Plan(plan)
	o.Status = OrgStatus(status)
	return o, nil
}

// AddOrgMember inserts a membership row. Returns ErrConflict on duplicate
// PK, ErrOrgLastOwner when adding a second active owner would trip the
// partial unique, ErrNotFound when the org row is missing.
//
// IAM-6 / ADR-061 PR 7 note: this method does NOT enforce
// Plan.OrgMembersMax. The cap is only enforced inside
// ConsumeOrgInvitation's tx (the load-bearing gate). The
// initial-owner seed at org creation bypasses the cap by design —
// the brand-new org has active=0, so the cap is non-binding — and
// every subsequent membership insert flows through the consume
// path which holds the lock + the count check. The earlier
// comment claiming "cmd/apid's enforceMemberCap gates the handler
// path" is stale; that helper is intentionally unwired
// (cmd/apid/org_handler_helpers.go) and the future direct-add
// route (PR-11 follow-up) is what would call it.
func (s *PgStore) AddOrgMember(ctx context.Context, orgID, accountID string, role OrgRole, invitedBy *string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: add org member tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var orgExists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from orgs where id = $1)`, orgID).Scan(&orgExists); err != nil {
		return fmt.Errorf("state: add org member org probe: %w", err)
	}
	if !orgExists {
		return ErrNotFound
	}

	// Defence-in-depth note (IAM-6 / ADR-061 PR 2):
	// AddOrgMember does NOT enforce the OrgMembersMax cap here. The
	// cap is enforced at two layers instead:
	//   1. Wire helper `cmd/apid::enforceMemberCap` (handler prelude)
	//   2. Store `consumeOrgInvitation` (the only consumer path that
	//      inserts a membership from outside the org)
	// Both run before the insert lands. A direct `AddOrgMember` call
	// (e.g. the owner-seed path in `cmd/apid::createSharedOrg` and the
	// owner-takeover path in `transferOrgOwnership`) is internal — it
	// always adds exactly one row at a time, with a pre-checked role
	// from the caller, so a third cap layer would only add a redundant
	// SQL count + the test-fixture friction of pre-promoting Free orgs
	// to Hobby. The single-owner partial unique index (`org_memberships_one_owner_idx`)
	// is the authoritative "one owner per non-personal org" guard.

	var inv *string
	if invitedBy != nil {
		ib := *invitedBy
		inv = &ib
	}
	tag, err := tx.Exec(ctx, `
		insert into org_memberships (org_id, account_id, role, invited_by_account_id)
		values ($1, $2, $3, $4)
	`, orgID, accountID, string(role), inv)
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation:
			// Partial unique org_memberships_one_owner_idx trips as 23505
			// (and PK collision is also 23505). Disambiguate by role:
			// adding an owner that's not the first → ErrOrgLastOwner,
			// otherwise the caller already had a row → ErrConflict.
			if role == OrgRoleOwner {
				return ErrOrgLastOwner
			}
			return ErrConflict
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.CheckViolation:
			return fmt.Errorf("state: add org member role check: %w", err)
		}
		return fmt.Errorf("state: add org member insert: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("state: add org member rows=%d", tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: add org member commit: %w", err)
	}
	return nil
}

// RemoveOrgMember stamps removed_at = now() and rejects removing the
// only active owner. ErrOrgLastOwner when the row IS the only active
// owner; ErrNotFound when the row is missing.
func (s *PgStore) RemoveOrgMember(ctx context.Context, orgID, accountID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: remove org member tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var role string
	var removedAt *time.Time
	row := tx.QueryRow(ctx, `
		select role, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, accountID)
	if err := row.Scan(&role, &removedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: remove org member probe: %w", err)
	}
	if role == string(OrgRoleOwner) && removedAt == nil {
		return ErrOrgLastOwner
	}
	if removedAt != nil {
		// already removed; idempotent no-op
		return nil
	}
	tag, err := tx.Exec(ctx, `
		update org_memberships set removed_at = now()
		 where org_id = $1 and account_id = $2
	`, orgID, accountID)
	if err != nil {
		return fmt.Errorf("state: remove org member update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: remove org member commit: %w", err)
	}
	return nil
}

// UpdateOrgMemberRole updates the role and rejects demoting the only
// active owner.
func (s *PgStore) UpdateOrgMemberRole(ctx context.Context, orgID, accountID string, role OrgRole) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: update org member role tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	var currentRole string
	var removedAt *time.Time
	row := tx.QueryRow(ctx, `
		select role, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, accountID)
	if err := row.Scan(&currentRole, &removedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: update org member role probe: %w", err)
	}
	if currentRole == string(OrgRoleOwner) && role != OrgRoleOwner && removedAt == nil {
		return ErrOrgLastOwner
	}
	tag, err := tx.Exec(ctx, `
		update org_memberships set role = $3
		 where org_id = $1 and account_id = $2
	`, orgID, accountID, string(role))
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation:
			return ErrOrgLastOwner
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.CheckViolation:
			return fmt.Errorf("state: update org member role check: %w", err)
		}
		return fmt.Errorf("state: update org member role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: update org member role commit: %w", err)
	}
	return nil
}

// TransferOrgOwnership atomically promotes toAccountID to owner and
// demotes fromAccountID to admin under one tx (PR 5; ADR-061). The
// exactly-one-owner invariant is enforced by the partial unique
// org_memberships_one_owner_idx (migrations/00099); the tripwire is
// sqlstate 23505 if either side of the swap would briefly leave two
// active owners.
//
// Reads both rows FOR UPDATE before the writes so a concurrent
// RemoveOrgMember / UpdateOrgMemberRole cannot race the swap (the
// row locks held during the tx) — pgx serialises both reads at the
// pgx.TxOptions{} default (ReadCommitted) which matches AddOrgMember
// and RemoveOrgMember's existing isolation level (see store.go:230-232).
//
// Sentinel mapping:
//   - either side missing → ErrNotFound
//   - fromAccountID is not the active owner → ErrOrgLastOwner
//     (the partial unique is the tripwire; defensively also probe
//     role explicitly so the error surfaces cleanly on the rare
//     path where the partial unique is not the failing constraint)
//   - to-account is removed → ErrNotFound (a removed invitee
//     cannot become owner)
//   - both rows present + invariants hold → swap succeeds
func (s *PgStore) TransferOrgOwnership(ctx context.Context, orgID, fromAccountID, toAccountID string) error {
	if fromAccountID == toAccountID {
		// No-op would silently skip the swap if the caller is
		// already the only owner. Refuse explicitly so the handler
		// surface is consistent (ErrOrgLastOwner mirrors the
		// self-transfer-is-illegal invariant; PR 5 front-loads the
		// check so we don't issue a no-op write).
		return ErrOrgLastOwner
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: transfer org ownership tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	// (1) Probe fromAccountID: must be the active owner right now.
	var fromRole string
	var fromRemoved *time.Time
	row := tx.QueryRow(ctx, `
		select role, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, fromAccountID)
	if err := row.Scan(&fromRole, &fromRemoved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: transfer ownership from probe: %w", err)
	}
	if fromRole != string(OrgRoleOwner) || fromRemoved != nil {
		return ErrOrgLastOwner
	}

	// (2) Probe toAccountID: must be an active, non-owner member.
	// Promoting a viewer/admin to owner is the swap; the active
	// membership is what makes the swap legitimate. Reject a
	// already-owner path with ErrOrgLastOwner so the partial unique
	// tripwire is bypassed upstream (cleaner wire-shape error).
	var toRole string
	var toRemoved *time.Time
	row = tx.QueryRow(ctx, `
		select role, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
		   for update
	`, orgID, toAccountID)
	if err := row.Scan(&toRole, &toRemoved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: transfer ownership to probe: %w", err)
	}
	if toRemoved != nil {
		return ErrNotFound
	}
	if toRole == string(OrgRoleOwner) {
		return ErrOrgLastOwner
	}

	// (3) Demote fromAccountID to admin first. Order matters: if
	// the demote succeeded and the promote raced with another
	// transfer, the partial unique org_memberships_one_owner_idx
	// would 23505 on the second owner. The reverse order (promote
	// first) would briefly leave two active owners, which is the
	// exact invariant the partial unique is meant to prevent.
	// Demote-first means a 23505 on the promote step surfaces as
	// ErrOrgLastOwner and the tx rolls back cleanly.
	if _, err := tx.Exec(ctx, `
		update org_memberships
		   set role = $3
		 where org_id = $1 and account_id = $2
	`, orgID, fromAccountID, string(OrgRoleAdmin)); err != nil {
		return fmt.Errorf("state: transfer ownership demote: %w", err)
	}
	// (4) Promote toAccountID to owner.
	if _, err := tx.Exec(ctx, `
		update org_memberships
		   set role = $3
		 where org_id = $1 and account_id = $2
	`, orgID, toAccountID, string(OrgRoleOwner)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return ErrOrgLastOwner
		}
		return fmt.Errorf("state: transfer ownership promote: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: transfer ownership commit: %w", err)
	}
	return nil
}

// ListOrgMembers returns every (active + removed) membership row.
func (s *PgStore) ListOrgMembers(ctx context.Context, orgID string) ([]OrgMembership, error) {
	rows, err := s.pool.Query(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships
		 where org_id = $1
		 order by joined_at
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("state: list org members: %w", err)
	}
	defer rows.Close()
	out := make([]OrgMembership, 0)
	for rows.Next() {
		m, err := scanOrgMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountActiveOrgMembers returns the number of memberships with
// removed_at IS NULL for the given org. The filter lives at the SQL
// layer so the count does not scan every row into Go; the partial
// unique index `org_memberships_account_idx WHERE removed_at IS NULL`
// (migration 00099_orgs_memberships_invitations.sql) keeps the scan
// cheap on the typical Hobby/Pro/Scale team-size org.
func (s *PgStore) CountActiveOrgMembers(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from org_memberships
		 where org_id = $1 and removed_at is null
	`, orgID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("state: count active org members: %w", err)
	}
	return n, nil
}

// OrgMemberByAccount returns the single (org, account) row.
func (s *PgStore) OrgMemberByAccount(ctx context.Context, orgID, accountID string) (OrgMembership, error) {
	row := s.pool.QueryRow(ctx, `
		select org_id, account_id, role, invited_by_account_id, joined_at, removed_at
		  from org_memberships
		 where org_id = $1 and account_id = $2
	`, orgID, accountID)
	return scanOrgMembership(row)
}

// scanOrgMembership reads one membership row.
func scanOrgMembership(r rowScanner) (OrgMembership, error) {
	var m OrgMembership
	var role string
	var invitedBy *string
	if err := r.Scan(&m.OrgID, &m.AccountID, &role, &invitedBy, &m.JoinedAt, &m.RemovedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrgMembership{}, ErrNotFound
		}
		return OrgMembership{}, fmt.Errorf("state: scan org membership: %w", err)
	}
	m.Role = OrgRole(role)
	if invitedBy != nil {
		ib := *invitedBy
		m.InvitedByAccountID = &ib
	}
	return m, nil
}

// CreateOrgInvitation inserts a new pending invitation. The unique
// on token_hash catches the (astronomically unlikely) duplicate.
func (s *PgStore) CreateOrgInvitation(ctx context.Context, inv OrgInvitation) (OrgInvitation, error) {
	if inv.ID == "" {
		inv.ID = uuid.NewString()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	var invitedBy *string
	if inv.InvitedByAccountID != nil {
		ib := *inv.InvitedByAccountID
		invitedBy = &ib
	}
	tag, err := s.pool.Exec(ctx, `
		insert into org_invitations (
			id, org_id, email, role, token_hash, invited_by_account_id,
			expires_at, created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8)
	`, inv.ID, inv.OrgID, inv.Email, string(inv.Role), inv.TokenHash,
		invitedBy, inv.ExpiresAt, inv.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return OrgInvitation{}, ErrConflict
		}
		return OrgInvitation{}, fmt.Errorf("state: create org invitation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return OrgInvitation{}, fmt.Errorf("state: create org invitation rows=%d", tag.RowsAffected())
	}
	return inv, nil
}

// OrgInvitationByTokenHash is the consume / revoke lookup.
func (s *PgStore) OrgInvitationByTokenHash(ctx context.Context, hash []byte) (OrgInvitation, error) {
	row := s.pool.QueryRow(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations
		 where token_hash = $1
	`, hash)
	return scanOrgInvitation(row)
}

// RevokeOrgInvitation stamps revoked_at on a still-pending row.
func (s *PgStore) RevokeOrgInvitation(ctx context.Context, hash []byte, _ string) error {
	tag, err := s.pool.Exec(ctx, `
		update org_invitations set revoked_at = now()
		 where token_hash = $1
		   and consumed_at is null
		   and revoked_at is null
	`, hash)
	if err != nil {
		return fmt.Errorf("state: revoke org invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrOrgInvitationInvalid
	}
	return nil
}

// ListOrgInvitationsForOrg returns every invitation row (any state).
func (s *PgStore) ListOrgInvitationsForOrg(ctx context.Context, orgID string) ([]OrgInvitation, error) {
	rows, err := s.pool.Query(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations
		 where org_id = $1
		 order by created_at desc
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("state: list org invitations: %w", err)
	}
	defer rows.Close()
	out := make([]OrgInvitation, 0)
	for rows.Next() {
		inv, err := scanOrgInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ListOrgInvitationsForOrgPage is the cursor-paginated variant of
// ListOrgInvitationsForOrg (PR-8 acceptance; PR-9 cursor upgrade).
//
// PR-9: the v1 cursor was the bare invitation id (UUID). The SQL
// predicate `id::text < $cursor` against an ORDER BY (created_at
// DESC, id DESC) is unsound under random UUIDs — two rows can have
// an inverted id::text comparison relative to the actual created_at
// ordering (the v1 limitation is documented at the memstore mirror).
// PR-9 replaces the v1 cursor with a compound (created_at, id)
// key encoded by pkg/cursor. The SQL filter switches to a tuple
// predicate on the (created_at, id::text) row, partitioned by
// the decoded cursor. When `before` is empty (first page) the
// cursor parameters bind as SQL NULL and the predicate short-circuits.
//
// limit is clamped to [1, 100]; out-of-range resolves to 25. The
// before="" case is the first page (no filter). No JOIN: invitations
// are org-scoped directly (org_id NOT NULL FK) so the org_id
// predicate is the only filter the SQL needs.
func (s *PgStore) ListOrgInvitationsForOrgPage(ctx context.Context, orgID string, limit int, before string) ([]OrgInvitation, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	// PR-9: parse the compound cursor. Empty is the first-page
	// sentinel; malformed input returns state.ErrInvalidCursor so
	// the handler can surface a 400 validation_failed (the v1
	// cursor silently returned 0 rows on malformed input, which
	// made broken clients silently fall behind — DO NOT regress).
	var cursorTS *time.Time
	var cursorID string
	if before != "" {
		k, err := cursor.Decode(before)
		if err != nil {
			// Return the sentinel plus the cursor decode error so
			// errors.Is(err, ErrInvalidCursor) works for the
			// handler's 400-mapping while the log keeps the
			// cursor-package body. errors.Join is the canonical
			// multi-error wrap (Go 1.20+).
			return nil, errors.Join(ErrInvalidCursor, err)
		}
		cursorTS = &k.CreatedAt
		cursorID = k.ID
	}
	rows, err := s.pool.Query(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations
		 where org_id = $1
		   and ($3::timestamptz is null or (created_at, id::text) < ($3::timestamptz, $4))
		 order by created_at desc, id::text desc
		 limit $2
	`, orgID, limit, cursorTS, cursorID)
	if err != nil {
		return nil, fmt.Errorf("state: list org invitations paged: %w", err)
	}
	defer rows.Close()
	out := make([]OrgInvitation, 0, limit)
	for rows.Next() {
		inv, err := scanOrgInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// CountPendingOrgInvitations returns the number of invitation rows
// with consumed_at IS NULL AND revoked_at IS NULL AND expires_at >
// now() for the given org. The filter lives at the SQL layer; the
// `now` is computed server-side via `now() at time zone 'utc'` so
// the SQL matches the in-Go `time.Now()` semantics used elsewhere in
// the org surface.
func (s *PgStore) CountPendingOrgInvitations(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		select count(*) from org_invitations
		 where org_id = $1
		   and consumed_at is null
		   and revoked_at is null
		   and (expires_at is null or expires_at > now() at time zone 'utc')
	`, orgID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("state: count pending org invitations: %w", err)
	}
	return n, nil
}

// ExpireOrgInvitations is the cleanup tick — stamps revoked_at on every
// pending + past-expires_at row. Returns the count.
func (s *PgStore) ExpireOrgInvitations(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		update org_invitations set revoked_at = $1
		 where consumed_at is null
		   and revoked_at is null
		   and expires_at < $1
	`, now)
	if err != nil {
		return 0, fmt.Errorf("state: expire org invitations: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanOrgInvitation reads one invitation row. email is cast back to text
// from the citext column so callers see a normalised string.
func scanOrgInvitation(r rowScanner) (OrgInvitation, error) {
	var inv OrgInvitation
	var role, email string
	var invitedBy *string
	var consumedAt, revokedAt *time.Time
	var accepting *string
	if err := r.Scan(
		&inv.ID, &inv.OrgID, &email, &role, &inv.TokenHash, &invitedBy,
		&inv.ExpiresAt, &consumedAt, &revokedAt, &accepting, &inv.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrgInvitation{}, ErrNotFound
		}
		return OrgInvitation{}, fmt.Errorf("state: scan org invitation: %w", err)
	}
	inv.Email = email
	inv.Role = OrgRole(role)
	if consumedAt != nil {
		t := *consumedAt
		inv.ConsumedAt = &t
	}
	if revokedAt != nil {
		t := *revokedAt
		inv.RevokedAt = &t
	}
	if invitedBy != nil {
		ib := *invitedBy
		inv.InvitedByAccountID = &ib
	}
	if accepting != nil {
		a := *accepting
		inv.AcceptingAccountID = &a
	}
	return inv, nil
}

// ConsumeOrgInvitation is the tx-heavy PR 5 acceptance path. Per
// ADR-061 §Migration strategy every step runs under one tx with the
// invitation row locked FOR UPDATE. Returns ErrOrgMemberCapExceeded on
// the plan cap, ErrOrgInvitationInvalid / ErrOrgInvitationExpired on
// state failures, ErrOrgAlreadyMember on the membership PK collision.
func (s *PgStore) ConsumeOrgInvitation(ctx context.Context, hash []byte, accepting Account) (OrgMembership, OrgInvitation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck

	// (1) Lock + read the invitation.
	row := tx.QueryRow(ctx, `
		select id, org_id, email::text, role, token_hash, invited_by_account_id,
		       expires_at, consumed_at, revoked_at, accepting_account_id, created_at
		  from org_invitations
		 where token_hash = $1
		   for update
	`, hash)
	inv, err := scanOrgInvitation(row)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, err
	}

	// (2) State validations.
	now := time.Now().UTC()
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationInvalid
	}
	if inv.ExpiresAt.Before(now) {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationExpired
	}
	if !strings.EqualFold(inv.Email, accepting.Email) {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationInvalid
	}

	// (3) Cap check. orgs.plan drives the limit; read inside the same tx.
	//
	// PR 7 cutover note: when plan / billing moves from accounts onto
	// orgs, this step must run inside an explicit outer transaction with
	// `SELECT … FROM orgs WHERE id = $1 FOR NO KEY UPDATE` to prevent a
	// concurrent UpdateOrgPlan from racing a parallel accept. PR 2 uses
	// the implicit FOR UPDATE on the invitation row plus the org-row
	// implicit MVCC snapshot read, which is sufficient for the personal-org
	// backfill path where plan changes are still serialized through the
	// accounts table. The cutover PR will widen the lock surface here.
	var planSlug string
	if err := tx.QueryRow(ctx, `select plan from orgs where id = $1`, inv.OrgID).Scan(&planSlug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrgMembership{}, OrgInvitation{}, ErrNotFound
		}
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation plan: %w", err)
	}
	var activeMembers int
	if err := tx.QueryRow(ctx, `
		select count(*) from org_memberships
		 where org_id = $1 and removed_at is null
	`, inv.OrgID).Scan(&activeMembers); err != nil {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation count: %w", err)
	}
	limits, _ := api.LimitsFor(api.Plan(planSlug))
	limit := limits.OrgMembersMax
	if limit > 0 && activeMembers >= limit {
		return OrgMembership{}, OrgInvitation{}, ErrOrgMemberCapExceeded
	}

	// (4) Already-member check (race guard).
	var existing string
	err = tx.QueryRow(ctx, `
		select account_id from org_memberships
		 where org_id = $1 and account_id = $2 and removed_at is null
	`, inv.OrgID, accepting.ID).Scan(&existing)
	switch {
	case err == nil:
		return OrgMembership{}, OrgInvitation{}, ErrOrgAlreadyMember
	case errors.Is(err, pgx.ErrNoRows):
		// expected
	default:
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation existing: %w", err)
	}

	// (5) Insert membership.
	var inviter *string
	if inv.InvitedByAccountID != nil {
		ib := *inv.InvitedByAccountID
		inviter = &ib
	}
	tag, err := tx.Exec(ctx, `
		insert into org_memberships (org_id, account_id, role, invited_by_account_id)
		values ($1, $2, $3, $4)
	`, inv.OrgID, accepting.ID, string(inv.Role), inviter)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return OrgMembership{}, OrgInvitation{}, ErrOrgAlreadyMember
		}
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation insert: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation rows=%d", tag.RowsAffected())
	}

	// (6) Stamp invitation.
	tag, err = tx.Exec(ctx, `
		update org_invitations
		   set consumed_at = $2, accepting_account_id = $3
		 where id = $1
	`, inv.ID, now, accepting.ID)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation stamp: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation stamp rows=%d", tag.RowsAffected())
	}

	// (7) Commit.
	if err := tx.Commit(ctx); err != nil {
		return OrgMembership{}, OrgInvitation{}, fmt.Errorf("state: consume org invitation commit: %w", err)
	}

	// Return the inserted membership + stamped invitation (post-commit values).
	acceptingID := accepting.ID
	inv.AcceptingAccountID = &acceptingID
	consumedAt := now
	inv.ConsumedAt = &consumedAt
	mem := OrgMembership{
		OrgID:              inv.OrgID,
		AccountID:          accepting.ID,
		Role:               inv.Role,
		InvitedByAccountID: inv.InvitedByAccountID,
		JoinedAt:           now,
		RemovedAt:          nil,
	}
	return mem, inv, nil
}

// rowScanner is the minimal Scan(dest ...any) error interface both
// pgx.Row (single-row scan) and pgx.Rows (multi-row scan) satisfy.
// Centralising the field list here means a future Session-struct
// column addition only edits one site.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(s rowScanner) (Session, error) {
	var sess Session
	var lastSeen, revoked *time.Time
	if err := s.Scan(&sess.ID, &sess.AccountID, &sess.IssuedAt, &lastSeen, &revoked, &sess.IssuedIP, &sess.IssuedUA, &sess.BindingHash); err != nil {
		return Session{}, err
	}
	sess.LastSeenAt = lastSeen
	sess.RevokedAt = revoked
	return sess, nil
}

// InsertAuditLog (issue #755 / PR-6) writes one row to the FK-free
// audit_log table (migrations/00163_audit_log.sql). The table is
// append-only by spec — there is no companion Update / Delete method,
// and the production role grants only INSERT (not UPDATE / DELETE).
//
// The pool-based path here is the standalone entry point used by any
// future emitter that needs to record an audit row outside of an
// account-deletion tx. The DeleteAccount-time insert goes through
// insertAuditLogTx (private) so the audit row rides the same tx as
// the accounts row delete — atomicity is the load-bearing property
// of the post-deletion evidence story.
//
// Raw SQL (not sqlc-generated) to match the local convention in
// AppendEvent / ListEvents and in the rest of DeleteAccount's
// children-walk. Bypassing the sqlc-check CI gate keeps the PR
// small and avoids regenerating pkg/state/sqlc/*.go.
func (s *PgStore) InsertAuditLog(ctx context.Context, entry AuditLog) error {
	_, err := s.pool.Exec(ctx,
		`insert into audit_log
		    (id, kind, account_id, account_email, actor, received_at, data)
		 values ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		entry.ID,
		entry.Kind,
		entry.AccountID,
		entry.AccountEmail,
		entry.Actor,
		entry.ReceivedAt,
		[]byte(entry.Data),
	)
	return err
}

// AppendDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
// inserts one row of the deployment_audit table
// (migrations/00332_deployment_audit.sql). Mirrors InsertAuditLog
// but writes the per-deployment shape (BIGINT IDENTITY PK, NOT NULL
// deployment_id with NO FK — see migration commentary for the FK-free
// rationale).
//
// Returns the id Postgres assigned via RETURNING id. The caller uses
// this for the deployment-timeline endpoint's cursor (the id DESC
// tiebreaker keeps the result stable across at-ties).
func (s *PgStore) AppendDeploymentAudit(ctx context.Context, entry DeploymentAudit) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`insert into deployment_audit
		    (deployment_id, account_id, kind, actor, at, data, alert_rule_id)
		 values ($1, $2, $3, $4, coalesce($5, now()), $6::jsonb, $7)
		 returning id`,
		entry.DeploymentID,
		entry.AccountID,
		string(entry.Kind),
		entry.Actor,
		// Pass zero time as NULL so the column default now() takes
		// effect; a caller-supplied non-zero time is honored
		// verbatim (used by the 90-day backfill in migration
		// 00333, which preserves the events.at timestamp).
		nullTime(entry.At),
		[]byte(entry.Data),
		// SAFE-RELEASES-OBS PR-D: nil for non-rule-triggered rows;
		// ActionDispatcher + evaluator stamp this for the 5
		// rule-touching audit kinds. NULL on insert is fine — the
		// partial index excludes null rows.
		entry.AlertRuleID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("state: append deployment_audit: %w", err)
	}
	return id, nil
}

// ListDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
// returns deployment_audit rows for one deployment, ordered
// (at DESC, id DESC). The deployment_audit_deployment_idx
// ((deployment_id, at DESC), migration 00326) backs the query so
// the timeline endpoint stays sub-millisecond at one-box scale.
//
// limit > 0 caps the page; <= 0 means "no row cap" (the caller is
// responsible for bounding via the 90-day retention floor or the
// customer-facing handler cap).
func (s *PgStore) ListDeploymentAudit(ctx context.Context, deploymentID string, limit int) ([]DeploymentAudit, error) {
	const cap = 1000
	if limit <= 0 || limit > cap {
		limit = cap
	}
	rows, err := s.pool.Query(ctx,
		`select id, deployment_id, account_id, kind, actor, at, data, alert_rule_id
		   from deployment_audit
		  where deployment_id = $1::uuid
		  order by at desc, id desc
		  limit $2`,
		deploymentID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list deployment_audit: %w", err)
	}
	defer rows.Close()
	var out []DeploymentAudit
	for rows.Next() {
		var (
			d         DeploymentAudit
			dataRaw   []byte
			alertRule *uuid.UUID
		)
		if err := rows.Scan(&d.ID, &d.DeploymentID, &d.AccountID, &d.Kind, &d.Actor, &d.At, &dataRaw, &alertRule); err != nil {
			return nil, fmt.Errorf("state: scan deployment_audit row: %w", err)
		}
		d.AlertRuleID = alertRule
		d.Data = json.RawMessage(dataRaw)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list deployment_audit rows: %w", err)
	}
	return out, nil
}

// ListDeploymentAuditByAlertRule (SAFE-RELEASES-OBS PR-D) returns
// deployment_audit rows whose alert_rule_id matches, newest first.
// Backs the /dashboard/alerts/{id} reverse-lookup query. The
// partial index deployment_audit_alert_rule_idx (migrations/20260905000000002)
// keeps the lookup sub-millisecond; only rows with non-null
// alert_rule_id participate so the index stays tiny.
func (s *PgStore) ListDeploymentAuditByAlertRule(ctx context.Context, alertRuleID string, limit int) ([]DeploymentAudit, error) {
	const cap = 500
	if limit <= 0 || limit > cap {
		limit = cap
	}
	rows, err := s.pool.Query(ctx,
		`select id, deployment_id, account_id, kind, actor, at, data, alert_rule_id
		   from deployment_audit
		  where alert_rule_id = $1::uuid
		  order by at desc, id desc
		  limit $2`,
		alertRuleID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list deployment_audit by alert_rule: %w", err)
	}
	defer rows.Close()
	var out []DeploymentAudit
	for rows.Next() {
		var (
			d         DeploymentAudit
			dataRaw   []byte
			alertRule *uuid.UUID
		)
		if err := rows.Scan(&d.ID, &d.DeploymentID, &d.AccountID, &d.Kind, &d.Actor, &d.At, &dataRaw, &alertRule); err != nil {
			return nil, fmt.Errorf("state: scan deployment_audit row by alert_rule: %w", err)
		}
		d.AlertRuleID = alertRule
		d.Data = json.RawMessage(dataRaw)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list deployment_audit by alert_rule rows: %w", err)
	}
	return out, nil
}

// nullTime is a tiny helper that returns a *time.Time pointing at the
// zero value as SQL NULL — used by AppendDeploymentAudit so a caller
// who omits At gets the column default now() rather than the Go zero
// time (which Postgres would reject as out-of-range for timestamptz).
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// insertAuditLogTx is the tx-bound variant of InsertAuditLog. Called
// from inside (*PgStore).DeleteAccount so the audit_log row lands in
// the same tx as the accounts row delete. The pool-based InsertAuditLog
// is NOT used on the DeleteAccount path because a separate-tx insert
// would race the accounts delete (the audit row could be committed
// before the parent delete, or vice versa, breaking the atomicity a
// regulator relies on).
func (s *PgStore) insertAuditLogTx(ctx context.Context, tx pgx.Tx, entry AuditLog) error {
	_, err := tx.Exec(ctx,
		`insert into audit_log
		    (id, kind, account_id, account_email, actor, received_at, data)
		 values ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		entry.ID,
		entry.Kind,
		entry.AccountID,
		entry.AccountEmail,
		entry.Actor,
		entry.ReceivedAt,
		[]byte(entry.Data),
	)
	return err
}

// ListAuditLog (issue #755 / PR-6) is the dashboard read path for the
// audit_log table. Translates the AuditLogFilter struct into a single
// WHERE clause; no string-built queries (matches the repo convention
// enforced by the sqlc-check CI gate).
//
// The ORDER BY matches the audit_log_received_at_idx so the index is
// honored on the dashboard-default sort. The id DESC tiebreaker keeps
// the result stable when two rows share a received_at (rare on
// nanosecond-resolution inserts, but the secondary sort costs nothing).
//
// Filter semantics, end-to-end:
//
//   - AccountID nil + IncludeAnonymous false: customer-scoped shape
//     ("show me this account's rows, never anonymous") — used by
//     GET /v1/audit-log after the handler pins AccountID to the
//     calling account's ID.
//   - AccountID nil + IncludeAnonymous true: operator cross-account
//     view with anonymous rows surfaced — used by
//     GET /v1/audit-log/all when ?include_anonymous=true.
//   - AccountID set + IncludeAnonymous false: operator cross-account
//     view restricted to one account — used by
//     GET /v1/audit-log/all when ?account_id=<uuid>.
//
// Limit is bounded by the handler to the over-read constant; a zero
// value here falls back to a sane default to prevent unbounded scans.
func (s *PgStore) ListAuditLog(ctx context.Context, filter AuditLogFilter) ([]AuditLog, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	// Translate the zero-time Since into a NULL lower bound so the
	// query can use a single uniform prepared statement shape.
	var sinceParam interface{}
	if !filter.Since.IsZero() {
		sinceParam = filter.Since
	} else {
		sinceParam = nil
	}

	// OperatorOnly takes precedence over KindPrefix at the SQL
	// layer: it forces kind like 'operator.action.%'. The
	// handler layer enforces mutual exclusivity (returns 400
	// when both are set); the SQL is defensive — a stray
	// KindPrefix with OperatorOnly=true would be ignored.
	kindPrefix := filter.KindPrefix
	if filter.OperatorOnly {
		kindPrefix = "operator.action."
	}

	// Build the actor_email + target_account_id params. Both are
	// optional; the SQL uses ($N::text is null or ...) so an
	// empty pointer is a no-op.
	var actorEmailParam, targetAccountIDParam interface{}
	if filter.ActorEmail != nil {
		actorEmailParam = *filter.ActorEmail
	}
	if filter.TargetAccountID != nil {
		targetAccountIDParam = *filter.TargetAccountID
	}

	rows, err := s.pool.Query(ctx,
		`select id, kind, account_id, account_email, actor, received_at, data
		   from audit_log
		  where ($1::uuid is null or account_id = $1::uuid)
		    and ($2 = '' or kind like $2 || '%')
		    and ($3::timestamptz is null or received_at >= $3::timestamptz)
		    and ($4::bool or account_id is not null)
		    and ($5::text is null or account_email = $5::text)
		    and ($6::text is null or data @> jsonb_build_object('target_account_id', $6::text))
		  order by received_at desc, id desc
		  limit $7`,
		filter.AccountID,
		kindPrefix,
		sinceParam,
		filter.IncludeAnonymous,
		actorEmailParam,
		targetAccountIDParam,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditLog
	for rows.Next() {
		var a AuditLog
		var rawData []byte
		if err := rows.Scan(
			&a.ID,
			&a.Kind,
			&a.AccountID,
			&a.AccountEmail,
			&a.Actor,
			&a.ReceivedAt,
			&rawData,
		); err != nil {
			return nil, err
		}
		if len(rawData) > 0 {
			a.Data = json.RawMessage(rawData)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// TrafficAnomalyAggregate is the pgstore-side hand-rolled mirror of
// the sqlc query TrafficAnomalyAggregate (queries.sql). The pgstore
// adapter still hand-rolls SQL per ADR-017 / M5 (TODO(M5.1) is to
// replace this with a call into the generated package — the sqlc
// query is the canonical source). See ADR-091 §3.6 / PR #2 for
// the anomaly model; the handler bounds since / baseline / limit
// per pkg/api/limits.go::ObsAdminWindowMaxHours + ObsAdminAnomaly*
// before calling this method.
func (s *PgStore) TrafficAnomalyAggregate(ctx context.Context, arg sqlc.TrafficAnomalyAggregateParams) ([]sqlc.TrafficAnomalyAggregateRow, error) {
	if !arg.Minute.Valid || !arg.Minute_2.Valid || arg.Column3 <= 0 {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate: invalid params (since=%v baseline=%v limit=%d)", arg.Minute, arg.Minute_2, arg.Column3)
	}
	rows, err := s.pool.Query(ctx, `
		with baseline as (
		    select account_id,
		           app_id,
		           extract(hour from usage_minutes.minute) as hour_of_day,
		           avg(mb_seconds)::float8 as mean_mb_seconds,
		           coalesce(stddev_pop(mb_seconds), 0)::float8 as stddev_mb_seconds,
		           count(*)::int as sample_count
		    from usage_minutes
		    where usage_minutes.minute >= $2
		      and usage_minutes.minute <  $1
		      and mb_seconds > 0
		    group by account_id, app_id, extract(hour from usage_minutes.minute)
		),
		current_pool as (
		    select account_id,
		           app_id,
		           minute,
		           sum(mb_seconds)::float8 as current_mb_seconds
		    from usage_minutes
		    where minute >= $1
		      and mb_seconds > 0
		    group by account_id, app_id, minute
		),
		scored as (
		    select c.account_id,
		           c.app_id,
		           c.minute,
		           c.current_mb_seconds,
		           b.mean_mb_seconds,
		           b.stddev_mb_seconds,
		           b.sample_count,
		           case
		               when b.sample_count < 3 then null
		               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
		                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
		               when b.stddev_mb_seconds >= 1.0
		                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
		                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
		               else null
		           end as z_score,
		           case
		               when b.stddev_mb_seconds < 1.0 then 'raw_z'
		               else 'hour_of_day'
		           end as reason
		    from current_pool c
		    join baseline b
		      on c.account_id = b.account_id
		     and c.app_id = b.app_id
		     and extract(hour from c.minute) = b.hour_of_day
		)
		select account_id,
		       app_id,
		       minute,
		       current_mb_seconds,
		       mean_mb_seconds,
		       stddev_mb_seconds,
		       sample_count,
		       z_score,
		       reason
		from scored
		where z_score is not null
		order by z_score desc
		limit $3
	`, arg.Minute.Time.UTC(), arg.Minute_2.Time.UTC(), arg.Column3)
	if err != nil {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate: %w", err)
	}
	defer rows.Close()
	out := []sqlc.TrafficAnomalyAggregateRow{}
	for rows.Next() {
		var r sqlc.TrafficAnomalyAggregateRow
		var zScore *float64
		if err := rows.Scan(
			&r.AccountID,
			&r.AppID,
			&r.Minute,
			&r.CurrentMbSeconds,
			&r.MeanMbSeconds,
			&r.StddevMbSeconds,
			&r.SampleCount,
			&zScore,
			&r.Reason,
		); err != nil {
			return nil, fmt.Errorf("state: traffic_anomaly_aggregate scan: %w", err)
		}
		r.ZScore = zScore
		out = append(out, r)
	}
	return out, rows.Err()
}

// TrafficAnomalyAggregateByNode is the pgstore wrapper around
// the sqlc-generated TrafficAnomalyAggregateByNode query
// (PR #4 / ADR-092 §3.4 amendment). The raw SQL mirrors the
// sqlc-emitted string verbatim so the generated code can stay
// the source of truth for the column shapes. Same scoring
// formula as TrafficAnomalyAggregate; one extra GROUP BY key
// (node_id) threads the result through instances →
// compute_nodes. The handler resolves node_id → node_name via
// ListComputeNodes before returning the wire shape.
func (s *PgStore) TrafficAnomalyAggregateByNode(ctx context.Context, arg sqlc.TrafficAnomalyAggregateByNodeParams) ([]sqlc.TrafficAnomalyAggregateByNodeRow, error) {
	if !arg.Minute.Valid || !arg.Minute_2.Valid || arg.Column3 <= 0 {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate_by_node: invalid params (since=%v baseline=%v limit=%d)", arg.Minute, arg.Minute_2, arg.Column3)
	}
	rows, err := s.pool.Query(ctx, `
		with baseline as (
		    select um.account_id,
		           um.app_id,
		           n.id as node_id,
		           extract(hour from um.minute) as hour_of_day,
		           avg(um.mb_seconds)::float8 as mean_mb_seconds,
		           coalesce(stddev_pop(um.mb_seconds), 0)::float8 as stddev_mb_seconds,
		           count(*)::int as sample_count
		    from usage_minutes um
		    join instances i on i.id = um.instance_id
		    join compute_nodes n on n.id = i.node_id
		    where um.minute >= $2
		      and um.minute <  $1
		      and um.mb_seconds > 0
		    group by um.account_id, um.app_id, n.id, extract(hour from um.minute)
		),
		current_pool as (
		    select um.account_id,
		           um.app_id,
		           n.id as node_id,
		           um.minute,
		           sum(um.mb_seconds)::float8 as current_mb_seconds
		    from usage_minutes um
		    join instances i on i.id = um.instance_id
		    join compute_nodes n on n.id = i.node_id
		    where um.minute >= $1
		      and um.mb_seconds > 0
		    group by um.account_id, um.app_id, n.id, um.minute
		),
		scored as (
		    select c.account_id,
		           c.app_id,
		           c.node_id,
		           c.minute,
		           c.current_mb_seconds,
		           b.mean_mb_seconds,
		           b.stddev_mb_seconds,
		           b.sample_count,
		           case
		               when b.sample_count < 3 then null
		               when b.stddev_mb_seconds < 1.0 and c.current_mb_seconds >= 5.0 * b.mean_mb_seconds
		                   and b.mean_mb_seconds > 0 then (c.current_mb_seconds - b.mean_mb_seconds) / 5.0
		               when b.stddev_mb_seconds >= 1.0
		                   and c.current_mb_seconds >= b.mean_mb_seconds + 3.0 * b.stddev_mb_seconds then
		                   (c.current_mb_seconds - b.mean_mb_seconds) / b.stddev_mb_seconds
		               else null
		           end as z_score,
		           case
		               when b.stddev_mb_seconds < 1.0 then 'raw_z'
		               else 'hour_of_day'
		           end as reason
		    from current_pool c
		    join baseline b
		      on c.account_id = b.account_id
		     and c.app_id    = b.app_id
		     and c.node_id   = b.node_id
		     and extract(hour from c.minute) = b.hour_of_day
		)
		select account_id,
		       app_id,
		       node_id,
		       minute,
		       current_mb_seconds,
		       mean_mb_seconds,
		       stddev_mb_seconds,
		       sample_count,
		       z_score,
		       reason
		from scored
		where z_score is not null
		order by z_score desc
		limit $3::int8
	`, arg.Minute.Time.UTC(), arg.Minute_2.Time.UTC(), arg.Column3)
	if err != nil {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate_by_node: %w", err)
	}
	defer rows.Close()
	out := []sqlc.TrafficAnomalyAggregateByNodeRow{}
	for rows.Next() {
		var r sqlc.TrafficAnomalyAggregateByNodeRow
		var zScore *float64
		if err := rows.Scan(
			&r.AccountID,
			&r.AppID,
			&r.NodeID,
			&r.Minute,
			&r.CurrentMbSeconds,
			&r.MeanMbSeconds,
			&r.StddevMbSeconds,
			&r.SampleCount,
			&zScore,
			&r.Reason,
		); err != nil {
			return nil, fmt.Errorf("state: traffic_anomaly_aggregate_by_node scan: %w", err)
		}
		r.ZScore = zScore
		out = append(out, r)
	}
	return out, rows.Err()
}

// PerAccountRateLimitAggregate is the pgstore-side hand-rolled
// mirror of the sqlc query PerAccountRateLimitAggregate
// (queries.sql). See ADR-091 §3.5 / PR #2 for the model. The
// handler bounds since / limit per pkg/api/limits.go before
// calling this method.
func (s *PgStore) PerAccountRateLimitAggregate(ctx context.Context, arg sqlc.PerAccountRateLimitAggregateParams) ([]sqlc.PerAccountRateLimitAggregateRow, error) {
	if !arg.At.Valid || arg.Column2 <= 0 {
		return nil, fmt.Errorf("state: per_account_rate_limit_aggregate: invalid params (since=%v limit=%d)", arg.At, arg.Column2)
	}
	rows, err := s.pool.Query(ctx, `
		select coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid) as account_id,
		       count(*)::int as hits,
		       max(at) as last_event_at
		from events
		where kind = 'auth.rate_limited'
		  and at >= $1
		group by coalesce(subject, '00000000-0000-0000-0000-000000000000'::uuid)
		order by hits desc, last_event_at desc
		limit $2
	`, arg.At.Time.UTC(), arg.Column2)
	if err != nil {
		return nil, fmt.Errorf("state: per_account_rate_limit_aggregate: %w", err)
	}
	defer rows.Close()
	out := []sqlc.PerAccountRateLimitAggregateRow{}
	for rows.Next() {
		var r sqlc.PerAccountRateLimitAggregateRow
		var lastAt time.Time
		if err := rows.Scan(&r.AccountID, &r.Hits, &lastAt); err != nil {
			return nil, fmt.Errorf("state: per_account_rate_limit_aggregate scan: %w", err)
		}
		if !lastAt.IsZero() {
			r.LastEventAt = lastAt
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertGithubWebhookSecret installs or rotates the per-tenant
// GitHub App webhook secret for the given installation_id. PR-D /
// ADR-012 §7 amendment: replaces the platform-wide
// FAAS_GITHUB_WEBHOOK_SECRET with a row-per-install lookup so a
// leaked tenant secret can rotate without coordinating every
// GitHub install. upgradedAt + upgradedBy form a §11 audit trail
// for the rotation.
//
// The secret is stored as raw bytes (bytea) — NOT hex-encoded
// text — because the daemon-side verifier at
// pkg/githubd/webhook.go::VerifyPushSignature reads the wire body
// raw. A hex-decode on every webhook would be wasted CPU at the
// gatewayd-internal proxy hot path.
//
// On conflict the existing row's secret_value + audit fields are
// overwritten; the test at
// migrations/00208_github_webhook_secrets_test.go pins the
// round-trip.
func (s *PgStore) UpsertGithubWebhookSecret(ctx context.Context, installationID int64, secret []byte, upgradedBy string) (time.Time, string, error) {
	if installationID == 0 {
		return time.Time{}, "", fmt.Errorf("state: UpsertGithubWebhookSecret: installation_id must be non-zero")
	}
	if len(secret) == 0 {
		return time.Time{}, "", fmt.Errorf("state: UpsertGithubWebhookSecret: secret must be non-empty (use a 32-byte random value per GitHub's recommendation)")
	}
	if upgradedBy == "" {
		upgradedBy = "platform"
	}
	var upgradedAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO github_webhook_secrets (installation_id, secret_value, upgraded_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (installation_id) DO UPDATE
		SET secret_value = EXCLUDED.secret_value,
		    upgraded_at  = now(),
		    upgraded_by  = EXCLUDED.upgraded_by
		RETURNING upgraded_at, upgraded_by
	`, installationID, secret, upgradedBy).Scan(&upgradedAt, &upgradedBy)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("state: UpsertGithubWebhookSecret: %w", err)
	}
	return upgradedAt, upgradedBy, nil
}

// GetGithubWebhookSecret returns the per-tenant secret for the
// given installation_id. Returns ErrNotFound when no row exists
// (the daemon-side resolver treats this as fail-closed — the
// webhook is rejected rather than falling back to the platform-
// wide FAAS_GITHUB_WEBHOOK_SECRET, per PR-D's adoption posture).
//
// The bytea is returned as a raw byte slice; the caller
// (pkg/githubd/webhook_secret.go::PGWebhookSecretResolver)
// passes the bytes directly to VerifyPushSignature.
func (s *PgStore) GetGithubWebhookSecret(ctx context.Context, installationID int64) ([]byte, error) {
	if installationID == 0 {
		return nil, ErrNotFound
	}
	var secret []byte
	err := s.pool.QueryRow(ctx,
		`SELECT secret_value FROM github_webhook_secrets WHERE installation_id = $1`,
		installationID,
	).Scan(&secret)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("state: GetGithubWebhookSecret: %w", err)
	}
	return secret, nil
}

// --- ADR-096 customer-facing automatic error grouping ---
//
// The sqlc-generated methods (pkg/state/sqlc/queries.sql.go) own
// the canonical SQL text. PgStore delegates to them via a
// per-call sqlc.New() Queries instance + s.pool as the DBTX —
// the pool's pgx connection handles satisfy the sqlc.DBTX
// interface (pgxpool.Pool.Exec / Query / QueryRow match the
// generated signature). The conversion from sqlc row types
// (pgtype.UUID, pgtype.Timestamptz) to the domain types
// (uuid.UUID, time.Time) happens here so callers (handlers in
// PR-B + the nightly purge cron) don't have to thread pgtype
// values through their own code. This matches the precedent
// for TrafficAnomalyAggregate / PerAccountRateLimitAggregate
// which the Store interface exposes at sqlc types directly.

// appErrorsQueries is a per-call helper that returns a fresh
// Queries instance. The Queries struct carries no state (see
// pkg/state/sqlc/db.go) so there's no need to cache it on
// PgStore. Allocating per call is cheap.
func (s *PgStore) appErrorsQueries() *sqlc.Queries { return sqlc.New() }

// uuidFromPgtype converts a sqlc/pgtype.UUID to google/uuid.UUID.
// Returns uuid.Nil if the input is invalid (the migration's id
// columns are NOT NULL uuid, so this is a defensive guard).
func uuidFromPgtype(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return uuid.UUID(id.Bytes)
}

// timeFromPgtype converts a sqlc/pgtype.Timestamptz to time.Time.
// Returns time.Time{} if the input is invalid.
func timeFromPgtype(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// pgtypeFromUUID wraps google/uuid.UUID into pgtype.UUID with
// Valid=true. Used by the writer methods to map domain types
// to sqlc params.
func pgtypeFromUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// pgtypeFromTime wraps time.Time into pgtype.Timestamptz.
func pgtypeFromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

// oidcExchangedTokenFromRow maps a sqlc row to the state-side
// OIDCExchangedToken. JTI is coalesced in the SQL so we never
// surface SQL NULL as the empty string differently from a real
// empty jti.
func oidcExchangedTokenFromRow(row sqlc.GetOIDCExchangedTokenByHashRow) *OIDCExchangedToken {
	return &OIDCExchangedToken{
		ID:        uuidFromPgtype(row.ID).String(),
		AccountID: uuidFromPgtype(row.AccountID).String(),
		TokenHash: row.TokenHash,
		ExpiresAt: timeFromPgtype(row.ExpiresAt),
		IssuerURL: row.IssuerUrl,
		Subject:   row.Subject,
		Audience:  row.Audience,
		JTI:       row.Jti,
		CreatedAt: timeFromPgtype(row.CreatedAt),
	}
}

// trustPolicyFromRow is the shared row→struct mapper for the three
// list / get / upsert shapes. They all expose the same column set
// (account_id, issuer_url, jwks_url, audience, subject_pattern,
// algorithms, required_claims, created_at, updated_at,
// audit_login) — the only differences are row-typed vs the slice
// element type, which the caller bridges.
func trustPolicyFromRow(
	accountID pgtype.UUID,
	issuerURL, jwksURL string,
	audience []string,
	subjectPattern string,
	algorithms []string,
	requiredClaims []byte,
	createdAt, updatedAt pgtype.Timestamptz,
	auditLogin string,
) *OIDCTrustPolicy {
	claims := map[string]string{}
	if len(requiredClaims) > 0 {
		// Tolerate malformed JSON by leaving claims empty rather
		// than failing the lookup — the row came from the
		// dashboard's refine form, which always writes valid
		// JSON, so a parse failure indicates disk corruption,
		// not bad input.
		_ = json.Unmarshal(requiredClaims, &claims)
	}
	return &OIDCTrustPolicy{
		AccountID:      uuidFromPgtype(accountID).String(),
		IssuerURL:      issuerURL,
		JWKSURL:        jwksURL,
		Audience:       audience,
		SubjectPattern: subjectPattern,
		Algorithms:     algorithms,
		RequiredClaims: claims,
		CreatedAt:      timeFromPgtype(createdAt),
		UpdatedAt:      timeFromPgtype(updatedAt),
		AuditLogin:     auditLogin,
	}
}

// IncrementAppError is the dedupe-merge INSERT called by the
// apid gRPC server-side handler. The handler wraps N calls in
// a single pgx transaction; this method runs ONE call at a
// time — the caller is responsible for the tx (via
// pgxpool.Pool's BeginTx in the grpc handler).
func (s *PgStore) IncrementAppError(ctx context.Context, arg sqlc.IncrementAppErrorParams) (bool, error) {
	return s.appErrorsQueries().IncrementAppError(ctx, s.pool, arg)
}

// InsertAppErrorRequest writes one drill-down row per request.
// Paired with IncrementAppError on the same gRPC stream batch.
func (s *PgStore) InsertAppErrorRequest(ctx context.Context, arg sqlc.InsertAppErrorRequestParams) error {
	return s.appErrorsQueries().InsertAppErrorRequest(ctx, s.pool, arg)
}

// ListAppErrorGroups converts sqlc.ListAppErrorGroupsRow to
// the domain AppErrorGroup at the boundary so handlers don't
// touch pgtype.
func (s *PgStore) ListAppErrorGroups(ctx context.Context, arg sqlc.ListAppErrorGroupsParams) ([]AppErrorGroup, error) {
	rows, err := s.appErrorsQueries().ListAppErrorGroups(ctx, s.pool, arg)
	if err != nil {
		return nil, err
	}
	out := make([]AppErrorGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, AppErrorGroup{
			ID:            uuidFromPgtype(r.ID),
			Fingerprint:   r.Fingerprint,
			ErrorClass:    r.ErrorClass,
			Route:         r.Route,
			HTTPStatus:    r.HttpStatus,
			Count:         r.Count,
			RequestCount:  r.RequestCount,
			FirstSeenAt:   timeFromPgtype(r.FirstSeenAt),
			LastSeenAt:    timeFromPgtype(r.LastSeenAt),
			SampleMessage: r.SampleMessage,
		})
	}
	return out, nil
}

// ListAppErrorRequests converts sqlc.ListAppErrorRequestsRow to
// the domain AppErrorRequestRow.
func (s *PgStore) ListAppErrorRequests(ctx context.Context, arg sqlc.ListAppErrorRequestsParams) ([]AppErrorRequestRow, error) {
	rows, err := s.appErrorsQueries().ListAppErrorRequests(ctx, s.pool, arg)
	if err != nil {
		return nil, err
	}
	out := make([]AppErrorRequestRow, 0, len(rows))
	for _, r := range rows {
		var depID *uuid.UUID
		if r.DeploymentID.Valid {
			d := uuidFromPgtype(r.DeploymentID)
			depID = &d
		}
		out = append(out, AppErrorRequestRow{
			ID:            uuidFromPgtype(r.ID),
			RequestID:     uuidFromPgtype(r.RequestID),
			ReceivedAt:    timeFromPgtype(r.ReceivedAt),
			Route:         r.Route,
			HTTPStatus:    r.HttpStatus,
			ErrorClass:    r.ErrorClass,
			SampleMessage: r.SampleMessage,
			DeploymentID:  depID,
		})
	}
	return out, nil
}

// GetAppErrorSample returns the single oldest request row for
// one fingerprint, with headers_sample + redactions populated
// for the wire-side "we redacted X / Y / Z" badge.
func (s *PgStore) GetAppErrorSample(ctx context.Context, arg sqlc.GetAppErrorSampleParams) (AppErrorSampleRow, error) {
	row, err := s.appErrorsQueries().GetAppErrorSample(ctx, s.pool, arg)
	if err != nil {
		return AppErrorSampleRow{}, err
	}
	var depID *uuid.UUID
	if row.DeploymentID.Valid {
		d := uuidFromPgtype(row.DeploymentID)
		depID = &d
	}
	headers := []byte(nil)
	if row.HeadersSample != nil {
		headers = row.HeadersSample
	}
	return AppErrorSampleRow{
		AppErrorRequestRow: AppErrorRequestRow{
			ID:            uuidFromPgtype(row.ID),
			RequestID:     uuidFromPgtype(row.RequestID),
			ReceivedAt:    timeFromPgtype(row.ReceivedAt),
			Route:         row.Route,
			HTTPStatus:    row.HttpStatus,
			ErrorClass:    row.ErrorClass,
			SampleMessage: row.SampleMessage,
			DeploymentID:  depID,
		},
		HeadersSample: headers,
		Redactions:    row.Redactions,
	}, nil
}

// ListAppErrorFingerprintsForPurge is the read-side of the
// nightly retention purge (cmd/apid/app_errors_purge.go).
func (s *PgStore) ListAppErrorFingerprintsForPurge(ctx context.Context, arg sqlc.ListAppErrorFingerprintsForPurgeParams) ([]uuid.UUID, error) {
	rows, err := s.appErrorsQueries().ListAppErrorFingerprintsForPurge(ctx, s.pool, arg)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, uuidFromPgtype(r))
	}
	return out, nil
}

// DeleteAppErrorsByIDs removes app_errors rows by ID array.
// The sqlc-generated method takes []pgtype.UUID; we convert
// here.
func (s *PgStore) DeleteAppErrorsByIDs(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	pgIDs := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIDs[i] = pgtypeFromUUID(id)
	}
	return s.appErrorsQueries().DeleteAppErrorsByIDs(ctx, s.pool, pgIDs)
}

// DeleteAppErrorRequestsByIDs removes app_error_requests rows
// by ID array.
func (s *PgStore) DeleteAppErrorRequestsByIDs(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	pgIDs := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIDs[i] = pgtypeFromUUID(id)
	}
	return s.appErrorsQueries().DeleteAppErrorRequestsByIDs(ctx, s.pool, pgIDs)
}

// DeleteAppErrorRequestsOlderThan removes ALL app_error_requests
// rows for one account older than cutoff. Used by the nightly
// retention purge to age out orphaned request rows.
func (s *PgStore) DeleteAppErrorRequestsOlderThan(ctx context.Context, accountID uuid.UUID, cutoff time.Time) error {
	return s.appErrorsQueries().DeleteAppErrorRequestsOlderThan(ctx, s.pool, sqlc.DeleteAppErrorRequestsOlderThanParams{
		AccountID:  pgtypeFromUUID(accountID),
		ReceivedAt: pgtypeFromTime(cutoff),
	})
}

// ----------------------------------------------------------------------------
// ADR-127 production debugger (§Decision 1) — pgstore wrappers for the
// request_telemetry query surface. Reuses the same sqlc.Queries accessor
// as the app_errors block above (no per-feature helper needed; the
// queries live in the same sqlc package). Same tx-handling contract:
// caller wraps N calls in a pgx transaction (the apid gRPC receiver
// uses pgxpool.BeginTx per stream batch).
// ----------------------------------------------------------------------------

// InsertRequestTelemetry writes one row per gateway-served request.
// Called by the apid gRPC IncrementRequestTelemetry handler.
func (s *PgStore) InsertRequestTelemetry(ctx context.Context, arg sqlc.InsertRequestTelemetryParams) error {
	return s.appErrorsQueries().InsertRequestTelemetry(ctx, s.pool, arg)
}

// UpdateSpansSummary enriches an existing request_telemetry row with
// the OTel spans summary payload (ADR-127 PR-D). Called by the
// apid gRPC WriteSpansSummary handler (Stage 4). summary is the
// raw JSON bytes; the caller has already validated json.Valid. The
// 24h WHERE clause bounds the index seek to the partial index
// request_telemetry_trace_idx; rows outside the window are skipped
// (no error — last-writer-wins).
//
// PR-D code-review #1: accountID is the third binding. The SQL
// predicate `and account_id = $3::uuid` makes cross-customer
// overwrite impossible even if a buggy upstream caller forwards
// the wrong trace_id. The pgtype.UUID Valid flag is set
// unconditionally — pgx binds NULL when Valid is false, and a NULL
// account_id can never match the NOT NULL column on
// request_telemetry.
//
// sqlc names the third positional arg `Column3` because the
// predicate is `account_id = $3::uuid` with no `RETURNING`
// or named-arg convention. Mirror what `make sqlc-generate`
// produces so the drift gate stays green.
func (s *PgStore) UpdateSpansSummary(ctx context.Context, traceID string, accountID uuid.UUID, summary []byte) error {
	return s.appErrorsQueries().UpdateSpansSummary(ctx, s.pool, sqlc.UpdateSpansSummaryParams{
		TraceID: pgtype.Text{String: traceID, Valid: traceID != ""},
		Column2: summary,
		Column3: pgtype.UUID{Bytes: accountID, Valid: true},
	})
}

// ListRequestTelemetryByApp backs GET /v1/apps/{slug}/debug/requests.
// The returned rows are the sqlc.Row struct; handlers convert to the
// domain DebugTelemetryRow at the boundary.
func (s *PgStore) ListRequestTelemetryByApp(ctx context.Context, arg sqlc.ListRequestTelemetryByAppParams) ([]sqlc.ListRequestTelemetryByAppRow, error) {
	return s.appErrorsQueries().ListRequestTelemetryByApp(ctx, s.pool, arg)
}

// GetRequestTelemetryByAppAndID backs the direct customer debugger
// drill-down. The sqlc query filters by app_id before matching the
// request id, preserving the app's tenant boundary in the database.
func (s *PgStore) GetRequestTelemetryByAppAndID(ctx context.Context, arg sqlc.GetRequestTelemetryByAppAndIDParams) (sqlc.GetRequestTelemetryByAppAndIDRow, error) {
	return s.appErrorsQueries().GetRequestTelemetryByAppAndID(ctx, s.pool, arg)
}

// RequestTelemetryByDeployment backs the per-deployment drilldown
// and the regression detector (PR-B cron).
func (s *PgStore) RequestTelemetryByDeployment(ctx context.Context, arg sqlc.RequestTelemetryByDeploymentParams) ([]sqlc.RequestTelemetryByDeploymentRow, error) {
	return s.appErrorsQueries().RequestTelemetryByDeployment(ctx, s.pool, arg)
}

// RequestTelemetryBaselineP95ByRoute backs the regression detector's
// per-route p95 baseline lookup.
func (s *PgStore) RequestTelemetryBaselineP95ByRoute(ctx context.Context, arg sqlc.RequestTelemetryBaselineP95ByRouteParams) ([]sqlc.RequestTelemetryBaselineP95ByRouteRow, error) {
	return s.appErrorsQueries().RequestTelemetryBaselineP95ByRoute(ctx, s.pool, arg)
}

// RequestTelemetryAnalyticsSummary backs the customer-facing aggregated
// request analytics summary. The SQL weights collapsed telemetry rows by
// their count column and is bounded by the caller's retention window.
func (s *PgStore) RequestTelemetryAnalyticsSummary(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsSummaryParams) (sqlc.RequestTelemetryAnalyticsSummaryRow, error) {
	return s.appErrorsQueries().RequestTelemetryAnalyticsSummary(ctx, s.pool, arg)
}

// RequestTelemetryAnalyticsByRoute backs the bounded top-route portion of
// the customer-facing request analytics response.
func (s *PgStore) RequestTelemetryAnalyticsByRoute(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsByRouteParams) ([]sqlc.RequestTelemetryAnalyticsByRouteRow, error) {
	return s.appErrorsQueries().RequestTelemetryAnalyticsByRoute(ctx, s.pool, arg)
}

// --- ADR-127 PR-B — regression observation persistence + dashboard reads ---

// UpsertRegressionObservation writes (or refreshes) the regression
// observation row for (app_id, deployment_id, route). PRIMARY KEY upsert
// keeps the table bounded — at most one row per (deployment, route),
// not one row per cron tick. Mirrors UpsertDoctorObservation's
// primary-key upsert shape.
func (s *PgStore) UpsertRegressionObservation(ctx context.Context, arg sqlc.UpsertRegressionObservationParams) error {
	return s.appErrorsQueries().UpsertRegressionObservation(ctx, s.pool, arg)
}

// ListActiveRegressionsByApp backs GET /v1/apps/{slug}/debug/regressions
// and the dashboard regression banner. since is an interval (e.g.
// pgtype.Interval{Microseconds: 3600 * 1e6} for "1 hour"); the
// regression cron and the dashboard handler use a wider window than
// the per-request GET endpoint so a regression that fired during a
// 30-minute deployment diff still surfaces.
func (s *PgStore) ListActiveRegressionsByApp(ctx context.Context, arg sqlc.ListActiveRegressionsByAppParams) ([]sqlc.ListActiveRegressionsByAppRow, error) {
	return s.appErrorsQueries().ListActiveRegressionsByApp(ctx, s.pool, arg)
}

// ListDeploymentsForCompare backs the dashboard compare panel. Returns
// distinct deployment_ids that have shipped traffic in the window
// with first_seen / last_seen / row_count metadata.
func (s *PgStore) ListDeploymentsForCompare(ctx context.Context, arg sqlc.ListDeploymentsForCompareParams) ([]sqlc.ListDeploymentsForCompareRow, error) {
	return s.appErrorsQueries().ListDeploymentsForCompare(ctx, s.pool, arg)
}

// ListAppsWithRecentTelemetry is the regression cron's discovery
// loop seam. Returns distinct app_ids that have shipped at least one
// request in the window so the cron doesn't have to walk the full
// `apps` table on every tick.
func (s *PgStore) ListAppsWithRecentTelemetry(ctx context.Context, arg pgtype.Interval) ([]pgtype.UUID, error) {
	return s.appErrorsQueries().ListAppsWithRecentTelemetry(ctx, s.pool, arg)
}

// ----------------------------------------------------------------------------
// ADR-098 connection-aware execution (§9.A). pgstore wrappers for the
// sqlc-generated data_upstreams + data_upstream_probes query surface
// (queries.sql ADR-098 block). Per-call helper mirrors
// appErrorsQueries() at pgstore.go:14924. The typed DataUpstream /
// DataUpstreamProbe structs (types.go ADR-098 block) are the handler
// boundary; pgtype-flavored rows stay inside this adapter.
//
// PR-A: thin wrappers over sqlc. The call sites are PR-B (apid
// env-classifier), PR-C (meterd probe loop), and PR-D (schedd wake-
// side read). Until PR-B lands, no production code path calls these —
// the surface compiles so the Store interface extension doesn't break
// the rest of the package.
// ----------------------------------------------------------------------------

// dataUpstreamsQueries is the per-call helper for the ADR-098 §9.A
// typed query surface. Mirrors appErrorsQueries() at pgstore.go:14924
// — no state, no caching, allocated per call.
func (s *PgStore) dataUpstreamsQueries() *sqlc.Queries { return sqlc.New() }

// InsertDataUpstream writes one data_upstreams row via the dedupe-
// merge ON CONFLICT tripwire on data_upstreams_dedupe_uniq. PR-B's
// apid env-classifier (cmd/apid/extract.go) calls this on every
// observed (app_id, scope, kind, host, port) tuple. The returned
// uuid.UUID is the row's id (the same caller-supplied id on insert;
// the existing row's id on conflict).
func (s *PgStore) InsertDataUpstream(ctx context.Context, arg sqlc.InsertDataUpstreamParams) (uuid.UUID, error) {
	id, err := s.dataUpstreamsQueries().InsertDataUpstream(ctx, s.pool, arg)
	if err != nil {
		return uuid.Nil, err
	}
	return uuidFromPgtype(id), nil
}

// ListDataUpstreamsByApp converts sqlc.ListDataUpstreamsByAppRow to
// the typed DataUpstream at the boundary so handlers don't touch
// pgtype. Cursor-paginated via (created_at, id); the handler MUST
// pre-clamp limit to api.DataUpstreamsListMaxLimit (PR-B).
func (s *PgStore) ListDataUpstreamsByApp(ctx context.Context, arg sqlc.ListDataUpstreamsByAppParams) ([]DataUpstream, error) {
	rows, err := s.dataUpstreamsQueries().ListDataUpstreamsByApp(ctx, s.pool, arg)
	if err != nil {
		return nil, err
	}
	out := make([]DataUpstream, 0, len(rows))
	for _, r := range rows {
		var rtt *int
		if r.LastRttMs.Valid {
			v := int(r.LastRttMs.Int32)
			rtt = &v
		}
		var probedAt *time.Time
		if r.LastProbedAt.Valid {
			t := timeFromPgtype(r.LastProbedAt)
			probedAt = &t
		}
		out = append(out, DataUpstream{
			ID:        uuidFromPgtype(r.ID),
			AccountID: uuidFromPgtype(r.AccountID),
			AppID:     uuidFromPgtype(r.AppID),
			Source:    DataUpstreamSource(r.Source),
			Scope:     r.Scope,
			// DeploymentScope widens the dedupe key in ADR-098
			// amendment (issue #954). Read-through; the column is
			// NOT NULL DEFAULT 'default' on the SQL side so the
			// empty-string default stamp is what backfills land as.
			DeploymentScope:  r.DeploymentScope,
			Kind:             DataUpstreamKind(r.Kind),
			Host:             r.Host,
			Port:             int(r.Port),
			HostRedactedHash: r.HostRedactedHash,
			DeclaredRegion:   r.DeclaredRegion,
			LastRTTMs:        rtt,
			LastProbedAt:     probedAt,
			LastSeenAt:       timeFromPgtype(r.LastSeenAt),
			CreatedAt:        timeFromPgtype(r.CreatedAt),
		})
	}
	return out, nil
}

// GetDataUpstreamByID is the single-row read for the dashboard's
// "edit upstream" pane (PR-B).
func (s *PgStore) GetDataUpstreamByID(ctx context.Context, id uuid.UUID) (DataUpstream, error) {
	row, err := s.dataUpstreamsQueries().GetDataUpstreamByID(ctx, s.pool, pgtypeFromUUID(id))
	if err != nil {
		return DataUpstream{}, err
	}
	var rtt *int
	if row.LastRttMs.Valid {
		v := int(row.LastRttMs.Int32)
		rtt = &v
	}
	var probedAt *time.Time
	if row.LastProbedAt.Valid {
		t := timeFromPgtype(row.LastProbedAt)
		probedAt = &t
	}
	return DataUpstream{
		ID:        uuidFromPgtype(row.ID),
		AccountID: uuidFromPgtype(row.AccountID),
		AppID:     uuidFromPgtype(row.AppID),
		Source:    DataUpstreamSource(row.Source),
		Scope:     row.Scope,
		// DeploymentScope widens the dedupe key in ADR-098
		// amendment (issue #954). Single-row read so the
		// DELETE-handler audit site can round-trip the value
		// into the data_upstream.deleted payload.
		DeploymentScope:  row.DeploymentScope,
		Kind:             DataUpstreamKind(row.Kind),
		Host:             row.Host,
		Port:             int(row.Port),
		HostRedactedHash: row.HostRedactedHash,
		DeclaredRegion:   row.DeclaredRegion,
		LastRTTMs:        rtt,
		LastProbedAt:     probedAt,
		LastSeenAt:       timeFromPgtype(row.LastSeenAt),
		CreatedAt:        timeFromPgtype(row.CreatedAt),
	}, nil
}

// DeleteDataUpstreamByID removes one data_upstreams row by ID.
// PR-B wires DELETE /v1/apps/{slug}/upstreams/{id} to this method.
// FK CASCADE on account_id / app_id handles the GDPR path; the
// handler is the only direct DELETE caller.
func (s *PgStore) DeleteDataUpstreamByID(ctx context.Context, id uuid.UUID) error {
	return s.dataUpstreamsQueries().DeleteDataUpstreamByID(ctx, s.pool, pgtypeFromUUID(id))
}

// InsertDataUpstreamProbe writes one probe sample. meterd's probe
// loop calls this every 30s per (host_redacted_hash, region).
// Partitioning on sampled_at gives the hot-write path; the partition
// creator (PR-C) drops old partitions wholesale.
func (s *PgStore) InsertDataUpstreamProbe(ctx context.Context, arg sqlc.InsertDataUpstreamProbeParams) error {
	return s.dataUpstreamsQueries().InsertDataUpstreamProbe(ctx, s.pool, arg)
}

// ListDataUpstreamProbesByHostRegion is schedd's wake-side read path.
// Returns the N most recent samples for one (host_redacted_hash,
// region) pair within a time window. Partition pruning on sampled_at
// drops everything outside the window.
func (s *PgStore) ListDataUpstreamProbesByHostRegion(ctx context.Context, arg sqlc.ListDataUpstreamProbesByHostRegionParams) ([]DataUpstreamProbe, error) {
	rows, err := s.dataUpstreamsQueries().ListDataUpstreamProbesByHostRegion(ctx, s.pool, arg)
	if err != nil {
		return nil, err
	}
	out := make([]DataUpstreamProbe, 0, len(rows))
	for _, r := range rows {
		var rtt *int
		if r.RttMs.Valid {
			v := int(r.RttMs.Int32)
			rtt = &v
		}
		var errClass *string
		if r.ErrorClass.Valid {
			s := r.ErrorClass.String
			errClass = &s
		}
		out = append(out, DataUpstreamProbe{
			ID:               uuidFromPgtype(r.ID),
			HostRedactedHash: r.HostRedactedHash,
			Region:           r.Region,
			Kind:             DataUpstreamKind(r.Kind),
			SampledAt:        timeFromPgtype(r.SampledAt),
			RTTMs:            rtt,
			OK:               r.Ok,
			ErrorClass:       errClass,
			ProbeNode:        r.ProbeNode,
		})
	}
	return out, nil
}

// PruneDataUpstreamProbesOlderThan is the retention purge. meterd
// calls this hourly with cutoff = now() - 30 days. The partition
// pruning on sampled_at makes the partial-partition tail O(affected
// partitions); PR-C's partition creator handles whole-partition drops.
func (s *PgStore) PruneDataUpstreamProbesOlderThan(ctx context.Context, cutoff time.Time) error {
	return s.dataUpstreamsQueries().PruneDataUpstreamProbesOlderThan(ctx, s.pool, pgtypeFromTime(cutoff))
}

// Issue #757 / ADR-0NN — Trigger primitive (event-source mappings).
// The store methods below mirror the cron CreateCronIfUnderQuota /
// CronByID / UpdateCron / DeleteCron / ListCronsForApp shape so the
// apid handler can stay symmetric. The schedd-side methods
// (ClaimTriggerRecords / Mark* / InsertTriggerDeadLetter) live on
// the same struct and are called by pkg/sched/dispatch_triggers.go
// (commit #14).
//
// Quota enforcement: CreateTriggerIfUnderQuota opens a tx, locks the
// parent apps row FOR UPDATE, counts existing triggers for app +
// account under the same lock, and inserts under that lock. The
// pattern is byte-for-byte the cron CreateCronIfUnderQuota pattern
// at lines 5431-5511 — same TOCTOU defence, same QuotaError type
// shape, same per-app + per-account split.

// CreateTriggerIfUnderQuota creates a non-cron trigger (kafka / nats
// / redis_streams / sqs_compat / queue) under the apps-row FOR
// UPDATE lock. Returns *TriggerQuotaError when the per-app or
// per-account cap is reached; ErrNotFound when the app row is gone
// or already deleted. The cron kind routes through the existing
// CreateCronIfUnderQuota path because cron needs the crons row + the
// schedule+path cron-specific schema.
func (s *PgStore) CreateTriggerIfUnderQuota(ctx context.Context, appID, kind, slug string, enabled bool, config []byte, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes int32, brokerPoisonStrategy string, limits api.Limits) (sqlc.Trigger, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// 1. Lock the parent apps row (apps_pkey serves the lock search).
	var locked int
	err = tx.QueryRow(ctx,
		`select 1 from apps where id = $1 and status <> 'deleted' for update`, appID,
	).Scan(&locked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Trigger{}, ErrNotFound
		}
		return sqlc.Trigger{}, fmt.Errorf("state: lock app %s: %w", appID, err)
	}

	// 2. Per-app count, authoritative under the lock.
	var appCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from triggers where app_id = $1`, appID,
	).Scan(&appCount); err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: count triggers for app %s: %w", appID, err)
	}
	if appCount >= limits.TriggerLimitPerApp {
		return sqlc.Trigger{}, &TriggerQuotaError{
			Scope:    TriggerQuotaScopeApp,
			Limit:    limits.TriggerLimitPerApp,
			Observed: appCount,
		}
	}

	// 3. Per-account count under the same tx. account_id is read off
	//    the apps row we just locked (no second round-trip).
	var accountID pgtype.UUID
	if err := tx.QueryRow(ctx,
		`select account_id from apps where id = $1`, appID,
	).Scan(&accountID); err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: read account_id for app %s: %w", appID, err)
	}
	var accountCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from triggers t
		 join apps a on a.id = t.app_id
		 where a.account_id = $1 and a.status <> 'deleted'`,
		accountID,
	).Scan(&accountCount); err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: count triggers for account %s: %w", accountID, err)
	}
	if accountCount >= limits.TriggerLimitPerAccount {
		return sqlc.Trigger{}, &TriggerQuotaError{
			Scope:    TriggerQuotaScopeAccount,
			Limit:    limits.TriggerLimitPerAccount,
			Observed: accountCount,
		}
	}

	// 4. Insert under the same lock. cron_id + source are NULL for
	//    the five non-cron kinds; the SQL CHECK + table-level
	//    constraint enforces that the cron kind has cron_id set and
	//    non-cron kinds have it NULL. We default both to NULL here;
	//    the apid handler routes cron-kind creations through
	//    CreateCron (the existing path) and this method is for the
	//    five non-cron kinds only. payload_max_bytes (migration
	//    00274) defaults to 6291456 (6 MiB) at the DB layer; we
	//    surface it as a parameter so the apid handler can
	//    override per-trigger without a follow-up migration.
	//    broker_poison_strategy (migration 00275) carries the
	//    audit-#10 poison-record handling flag; "" → DB default
	//    'commit' so callers that don't yet know about the
	//    strategy land the previous behaviour byte-for-byte.
	bps := brokerPoisonStrategy
	if bps == "" {
		bps = "commit"
	}
	row := tx.QueryRow(ctx,
		`insert into triggers (account_id, app_id, kind, slug, enabled, config,
		                       batch_size_max, batch_window_ms, max_attempts,
		                       cron_id, source, payload_max_bytes,
		                       broker_poison_strategy)
		 values ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13)
		 returning id, account_id, app_id, kind, slug, enabled, config,
		           batch_size_max, batch_window_ms, max_attempts,
		           cron_id, source, payload_max_bytes, broker_poison_strategy,
		           created_at, updated_at`,
		accountID, appID, kind, slug, enabled, config,
		batchSizeMax, batchWindowMs, maxAttempts,
		pgtype.UUID{}, pgtype.Text{}, payloadMaxBytes, bps)
	t := sqlc.Trigger{}
	if err := row.Scan(
		&t.ID, &t.AccountID, &t.AppID, &t.Kind, &t.Slug, &t.Enabled,
		&t.Config, &t.BatchSizeMax, &t.BatchWindowMs, &t.MaxAttempts,
		&t.CronID, &t.Source, &t.PayloadMaxBytes, &t.BrokerPoisonStrategy,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return sqlc.Trigger{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: commit create trigger: %w", err)
	}
	return t, nil
}

// TriggerByID returns the trigger with the given ID. Returns
// ErrNotFound when the row is gone.
func (s *PgStore) TriggerByID(ctx context.Context, id string) (sqlc.Trigger, error) {
	row, err := s.triggerQueries().TriggerByID(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return sqlc.Trigger{}, err
	}
	return triggerRowToTrigger(row), nil
}

// UpdateTrigger patches the mutable fields (enabled, config,
// batch_size_max, batch_window_ms, max_attempts,
// broker_poison_strategy, filter_criteria). The kind + slug +
// cron_id + source fields are immutable after creation; the apid
// handler rejects PATCHes that touch them with
// trigger_immutable_field. The cron_id linkage is set at creation
// only (kind='cron' is created via the legacy CreateCron path).
//
// We bypass the sqlc.UpdateTrigger generated stub because sqlc
// generated the coalesce() UPDATE with non-nullable parameter
// types (Enabled bool, Column3 []byte, etc.) which collapses the
// "absent" / "explicit" distinction the apid handler needs (the
// cron UpdateCron precedent handles this same coalesce-via-pool
// pattern at line 5523+). Bypassing sqlc here keeps the PATCH
// semantics correct at the cost of losing auto-generated type
// safety — net-positive because the alternative would force the
// handler to send "current values" for unset fields and break the
// JSON `omitempty` round-trip.
//
// filter_criteria is REVIEW-FIX MED-1 (issue #757 closure PR
// #993): it was added to the sqlc.Trigger struct in commit 6 of
// the mega-PR but omitted from this inline UPDATE — meaning a
// PATCH that flipped filter_criteria was silently dropped on the
// floor. The pointer is nullable: nil = "leave unchanged",
// non-nil []byte = "replace the JSONB column" (json.RawMessage
// shape mirrors the FilterCriteria wire DTO; nil-element means
// "clear filter to no-op").
func (s *PgStore) UpdateTrigger(ctx context.Context, id string, enabled *bool, config []byte, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes *int32, brokerPoisonStrategy *string, filterCriteria *[]byte) (sqlc.Trigger, error) {
	var enabledArg, configArg, batchSizeArg, batchWindowArg, maxAttemptsArg, payloadMaxArg, brokerPoisonArg, filterCriteriaArg any
	if enabled != nil {
		enabledArg = *enabled
	}
	if config != nil {
		configArg = config
	}
	if batchSizeMax != nil {
		batchSizeArg = *batchSizeMax
	}
	if batchWindowMs != nil {
		batchWindowArg = *batchWindowMs
	}
	if maxAttempts != nil {
		maxAttemptsArg = *maxAttempts
	}
	if payloadMaxBytes != nil {
		payloadMaxArg = *payloadMaxBytes
	}
	if brokerPoisonStrategy != nil {
		brokerPoisonArg = *brokerPoisonStrategy
	}
	if filterCriteria != nil {
		filterCriteriaArg = filterCriteria
	}
	row := s.pool.QueryRow(ctx,
		`update triggers set
		   enabled = coalesce($2, enabled),
		   config = coalesce($3::jsonb, config),
		   batch_size_max = coalesce($4, batch_size_max),
		   batch_window_ms = coalesce($5, batch_window_ms),
		   max_attempts = coalesce($6, max_attempts),
		   payload_max_bytes = coalesce($7, payload_max_bytes),
		   broker_poison_strategy = coalesce($8, broker_poison_strategy),
		   filter_criteria = coalesce($9::jsonb, filter_criteria)
		 where id = $1
		 returning id, account_id, app_id, kind, slug, enabled, config,
		           batch_size_max, batch_window_ms, max_attempts,
		           cron_id, source, payload_max_bytes, broker_poison_strategy,
		           filter_criteria,
		           created_at, updated_at`,
		id, enabledArg, configArg, batchSizeArg, batchWindowArg, maxAttemptsArg, payloadMaxArg, brokerPoisonArg, filterCriteriaArg)
	t := sqlc.Trigger{}
	if err := row.Scan(
		&t.ID, &t.AccountID, &t.AppID, &t.Kind, &t.Slug, &t.Enabled,
		&t.Config, &t.BatchSizeMax, &t.BatchWindowMs, &t.MaxAttempts,
		&t.CronID, &t.Source, &t.PayloadMaxBytes, &t.BrokerPoisonStrategy,
		&t.FilterCriteria,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return sqlc.Trigger{}, mapErr(err)
	}
	return t, nil
}

// DeleteTrigger removes a trigger + cascades to trigger_records +
// trigger_dead_letter via the ON DELETE CASCADE FKs. The appID
// argument is the authz guard — the apid handler must pass the
// app_id it loaded; the WHERE id=$1 AND app_id=$2 clause refuses to
// delete a trigger that doesn't belong to the requested app
// (cross-app tenant bypass defence).
func (s *PgStore) DeleteTrigger(ctx context.Context, id, appID string) error {
	return s.triggerQueries().DeleteTrigger(ctx, s.pool, sqlc.DeleteTriggerParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID)})
}

// ListTriggersForApp is the dashboard read-back (GET /v1/triggers).
func (s *PgStore) ListTriggersForApp(ctx context.Context, appID string) ([]sqlc.Trigger, error) {
	rows, err := s.triggerQueries().ListTriggersForApp(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.Trigger, len(rows))
	for i, r := range rows {
		out[i] = triggerListRowToTrigger(r)
	}
	return out, nil
}

// ListEnabledTriggers is the schedd-side read on each 1-second
// cadence. Returns the full enabled-triggers set; the dispatch
// tick filters by kind to pick the per-kind poller.
func (s *PgStore) ListEnabledTriggers(ctx context.Context) ([]sqlc.Trigger, error) {
	rows, err := s.triggerQueries().ListEnabledTriggers(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.Trigger, len(rows))
	for i, r := range rows {
		out[i] = triggerEnabledRowToTrigger(r)
	}
	return out, nil
}

// ClaimTriggerRecords is the schedd-side pull from the per-trigger
// pending/retry queue. FOR UPDATE SKIP LOCKED (set in queries.sql)
// lets concurrent schedd replicas each claim disjoint row sets —
// ADR-099 PR-C precedent for claim_job_tasks.
func (s *PgStore) ClaimTriggerRecords(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error) {
	rows, err := s.triggerQueries().ClaimTriggerRecords(ctx, s.pool, sqlc.ClaimTriggerRecordsParams{TriggerID: mustPgUUID(triggerID), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.TriggerRecord, len(rows))
	for i, r := range rows {
		out[i] = claimTriggerRecordRowToTriggerRecord(r)
	}
	return out, nil
}

// InsertTriggerRecord persists a single broker-delivered record
// into the trigger_records FSM queue. Returns the persisted (or
// existing-on-conflict) trigger_records.id so the dispatcher can
// hold a stable row identity across the per-record FSM transitions.
//
// Review finding #1 (PR #910): without this insert, the dispatch
// tick is structurally dead — ClaimTriggerRecords returns 0 rows
// because nothing ever writes to trigger_records. This is the
// seam every per-broker poller calls inside Poll() so a broker
// message becomes a row BEFORE the dispatch tick can decide what
// to do with it.
//
// ON CONFLICT (trigger_id, item_identifier) DO NOTHING (set in
// queries.sql) mirrors the broker-side dedupe guarantee (kafka
// per-partition offset, NATS stream sequence, Redis entry-id, SQS
// receipt-handle, in-platform invocation_id). A re-poll after a
// partial commit + Ack timeout therefore never inserts a duplicate
// row; the existing row's id is returned via the no-rows path
// (ON CONFLICT suppresses RETURNING, so we read-back the id with a
// second SELECT only when the INSERT returns zero rows).
func (s *PgStore) InsertTriggerRecord(ctx context.Context, triggerID, itemIdentifier string, payload, headers, metadata []byte) (string, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	if headers == nil {
		headers = []byte("{}")
	}
	if metadata == nil {
		metadata = []byte("{}")
	}
	id, err := s.triggerQueries().InsertTriggerRecord(ctx, s.pool,
		sqlc.InsertTriggerRecordParams{
			TriggerID:      mustPgUUID(triggerID),
			ItemIdentifier: itemIdentifier,
			Column3:        payload,
			Column4:        headers,
			Column5:        metadata,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT path — the row already existed (a
			// previous Poll inserted and the dispatch tick has
			// not yet Ack'd). Read back the existing id so the
			// dispatcher can attribute FSM transitions to one
			// canonical row identity.
			var existing string
			if err := s.pool.QueryRow(ctx,
				`select id::text from trigger_records
				 where trigger_id = $1 and item_identifier = $2`,
				mustPgUUID(triggerID), itemIdentifier,
			).Scan(&existing); err != nil {
				return "", fmt.Errorf("state: insert trigger_record conflict read-back: %w", err)
			}
			return existing, nil
		}
		return "", fmt.Errorf("state: insert trigger_record: %w", err)
	}
	return pgUUIDString(id), nil
}

// MarkTriggerRecordSucceeded transitions a claimed record to the
// succeeded state. Called from the dispatch tick after the runner
// envelope returns 2xx with no ReportBatchItemFailures entry for
// this item_identifier.
func (s *PgStore) MarkTriggerRecordSucceeded(ctx context.Context, id string) error {
	return s.triggerQueries().MarkTriggerRecordSucceeded(ctx, s.pool, mustPgUUID(id))
}

// MarkTriggerRecordRetry schedules a retry with exponential
// backoff. The dispatch tick calls this with attempts < max_attempts.
func (s *PgStore) MarkTriggerRecordRetry(ctx context.Context, id, lastError string, nextFireAt time.Time) error {
	return s.triggerQueries().MarkTriggerRecordRetry(ctx, s.pool, sqlc.MarkTriggerRecordRetryParams{
		ID:         mustPgUUID(id),
		LastError:  pgtype.Text{String: lastError, Valid: lastError != ""},
		NextFireAt: pgtypeFromTime(nextFireAt),
	})
}

// MarkTriggerRecordDeadLetter transitions a claimed record to the
// dead_letter state. Called when the runner envelope returns a
// batchItemFailures entry AND attempts >= max_attempts OR the
// record carries a poison_record signature (malformed response
// JSON, missing item_identifier, etc.).
func (s *PgStore) MarkTriggerRecordDeadLetter(ctx context.Context, id, lastError string) error {
	return s.triggerQueries().MarkTriggerRecordDeadLetter(ctx, s.pool, sqlc.MarkTriggerRecordDeadLetterParams{
		ID:        mustPgUUID(id),
		LastError: pgtype.Text{String: lastError, Valid: lastError != ""},
	})
}

// InsertTriggerDeadLetter writes the closed-vocab failure-routing
// row that pairs with a dead-lettered record. detail carries any
// per-reason payload (broker error text, payload size that tripped
// the 6MB cap, etc.) for the dashboard read-back.
//
// Audit round 2 finding #1 (PR #910): the recordID parameter MUST
// be the trigger_records.id UUID, not a broker-side handle. The
// trigger_dead_letter.record_id column is a UUID FK into
// trigger_records.id; passing a kafka offset / NATS seq / SQS
// receipt handle / Redis entry-id / queue invocation_id trips
// SQLSTATE 23503, the dead_letter row is silently dropped, and
// MarkTriggerRecordDeadLetter updates 0 rows. Callers must look
// up the UUID via TriggerRecordIDByItemIdentifier before invoking
// this method.
func (s *PgStore) InsertTriggerDeadLetter(ctx context.Context, recordID, triggerID, reason, routedTo string, detail []byte) error {
	var detailArg any = []byte("{}")
	if detail != nil {
		detailArg = detail
	}
	_, err := s.pool.Exec(ctx,
		`insert into trigger_dead_letter (record_id, trigger_id, reason, routed_to, detail)
		 values ($1, $2, $3, $4, $5::jsonb)`,
		recordID, triggerID, reason, routedTo, detailArg)
	return err
}

// ListTriggerDeadLetter reads the per-trigger DLQ rows for the
// dashboard + GET /v1/triggers/{id}/metrics?include_dlq=true.
func (s *PgStore) ListTriggerDeadLetter(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerDeadLetter, error) {
	return s.triggerQueries().ListTriggerDeadLetter(ctx, s.pool, sqlc.ListTriggerDeadLetterParams{TriggerID: mustPgUUID(triggerID), Limit: limit})
}

// TriggerRecordIDByItemIdentifier resolves a broker-side handle
// (kafka offset, NATS seq, SQS receipt handle, Redis entry-id,
// queue invocation_id) to the durable trigger_records.id UUID
// the dead_letter FK expects.
//
// Audit round 2 finding #1 (PR #910): the dispatcher needs this
// bridge because the broker side speaks the per-broker handle
// namespace (a string) while the trigger_records table uses a
// global UUID identity and the trigger_dead_letter.record_id FK
// is on the UUID column. Without this lookup every rate-limit
// denial tripped SQLSTATE 23503.
//
// Returns ("", nil) — empty string + nil error — when no row
// matches. That case fires when the rate-limit gate denies a
// record BEFORE InsertTriggerRecord has had a chance to persist
// it (the lookup misses on the very first tick after a fresh
// broker poll). Callers MUST treat the empty string as "skip the
// dead_letter insert; the record will be retried on the next
// dispatch tick". pgx.ErrNoRows is collapsed to a plain
// ("", nil) for caller convenience.
//
// Also collapses malformed triggerID strings to ("", nil) via
// mustPgUUID — a parse failure means the row simply doesn't
// exist (the zero UUID never matches anything).
func (s *PgStore) TriggerRecordIDByItemIdentifier(ctx context.Context, triggerID, itemIdentifier string) (string, error) {
	id, err := s.triggerQueries().TriggerRecordIDByItemIdentifier(ctx, s.pool,
		sqlc.TriggerRecordIDByItemIdentifierParams{
			TriggerID:      mustPgUUID(triggerID),
			ItemIdentifier: itemIdentifier,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if !id.Valid {
		return "", nil
	}
	return id.String(), nil
}

// ListTriggerRecordsForTrigger reads the records for a trigger in
// dispatch-time order. Used by GET /v1/triggers/{id}/records.
func (s *PgStore) ListTriggerRecordsForTrigger(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error) {
	rows, err := s.triggerQueries().ListTriggerRecordsForTrigger(ctx, s.pool, sqlc.ListTriggerRecordsForTriggerParams{TriggerID: mustPgUUID(triggerID), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.TriggerRecord, len(rows))
	for i, r := range rows {
		out[i] = listTriggerRecordRowToTriggerRecord(r)
	}
	return out, nil
}

// triggerQueries returns a fresh sqlc.Queries for the trigger table.
// Pattern after appErrorsQueries (line 15120) and
// dataUpstreamsQueries (line 15324): sqlc.Queries carries no state,
// so a per-call allocation is cheap and avoids a cache invalidation
// hazard if PgStore ever pools across multiple DB connections in a
// future scale-out.
func (s *PgStore) triggerQueries() *sqlc.Queries { return sqlc.New() }

// triggerRowToTrigger converts a sqlc-generated TriggerByIDRow back to
// the model type. sqlc v1.31 emits a dedicated Row struct per query
// even when the column set matches the underlying model; the
// conversion is mechanical and cheap (struct copy). Pre-v1.31 deduped
// Row → Model automatically.
func triggerRowToTrigger(r sqlc.TriggerByIDRow) sqlc.Trigger {
	return sqlc.Trigger{
		ID:                   r.ID,
		AccountID:            r.AccountID,
		AppID:                r.AppID,
		Kind:                 r.Kind,
		Slug:                 r.Slug,
		Enabled:              r.Enabled,
		Config:               r.Config,
		BatchSizeMax:         r.BatchSizeMax,
		BatchWindowMs:        r.BatchWindowMs,
		MaxAttempts:          r.MaxAttempts,
		CronID:               r.CronID,
		Source:               r.Source,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		PayloadMaxBytes:      r.PayloadMaxBytes,
		BrokerPoisonStrategy: r.BrokerPoisonStrategy,
		FilterCriteria:       r.FilterCriteria,
	}
}

// triggerListRowToTrigger mirrors triggerRowToTrigger for the
// ListTriggersForApp query. Field set is identical (the queries.sql
// comments explicitly call this out); the helper exists because sqlc
// emits one Row struct per :many query.
func triggerListRowToTrigger(r sqlc.ListTriggersForAppRow) sqlc.Trigger {
	return sqlc.Trigger{
		ID:                   r.ID,
		AccountID:            r.AccountID,
		AppID:                r.AppID,
		Kind:                 r.Kind,
		Slug:                 r.Slug,
		Enabled:              r.Enabled,
		Config:               r.Config,
		BatchSizeMax:         r.BatchSizeMax,
		BatchWindowMs:        r.BatchWindowMs,
		MaxAttempts:          r.MaxAttempts,
		CronID:               r.CronID,
		Source:               r.Source,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		PayloadMaxBytes:      r.PayloadMaxBytes,
		BrokerPoisonStrategy: r.BrokerPoisonStrategy,
		FilterCriteria:       r.FilterCriteria,
	}
}

// triggerEnabledRowToTrigger mirrors triggerRowToTrigger for the
// ListEnabledTriggers query.
func triggerEnabledRowToTrigger(r sqlc.ListEnabledTriggersRow) sqlc.Trigger {
	return sqlc.Trigger{
		ID:                   r.ID,
		AccountID:            r.AccountID,
		AppID:                r.AppID,
		Kind:                 r.Kind,
		Slug:                 r.Slug,
		Enabled:              r.Enabled,
		Config:               r.Config,
		BatchSizeMax:         r.BatchSizeMax,
		BatchWindowMs:        r.BatchWindowMs,
		MaxAttempts:          r.MaxAttempts,
		CronID:               r.CronID,
		Source:               r.Source,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		PayloadMaxBytes:      r.PayloadMaxBytes,
		BrokerPoisonStrategy: r.BrokerPoisonStrategy,
		FilterCriteria:       r.FilterCriteria,
	}
}

// claimTriggerRecordRowToTriggerRecord converts a ClaimTriggerRecordsRow
// back to the model type. Same sqlc v1.31 dedicated-Row pattern as the
// trigger row helpers above.
func claimTriggerRecordRowToTriggerRecord(r sqlc.ClaimTriggerRecordsRow) sqlc.TriggerRecord {
	return sqlc.TriggerRecord{
		ID:               r.ID,
		TriggerID:        r.TriggerID,
		ItemIdentifier:   r.ItemIdentifier,
		Payload:          r.Payload,
		Headers:          r.Headers,
		Metadata:         r.Metadata,
		State:            r.State,
		Attempts:         r.Attempts,
		NextFireAt:       r.NextFireAt,
		ReceivedAt:       r.ReceivedAt,
		LastError:        r.LastError,
		LastDispatchedAt: r.LastDispatchedAt,
	}
}

// listTriggerRecordRowToTriggerRecord converts a ListTriggerRecordsForTriggerRow
// back to the model type.
func listTriggerRecordRowToTriggerRecord(r sqlc.ListTriggerRecordsForTriggerRow) sqlc.TriggerRecord {
	return sqlc.TriggerRecord{
		ID:               r.ID,
		TriggerID:        r.TriggerID,
		ItemIdentifier:   r.ItemIdentifier,
		Payload:          r.Payload,
		Headers:          r.Headers,
		Metadata:         r.Metadata,
		State:            r.State,
		Attempts:         r.Attempts,
		NextFireAt:       r.NextFireAt,
		ReceivedAt:       r.ReceivedAt,
		LastError:        r.LastError,
		LastDispatchedAt: r.LastDispatchedAt,
	}
}

// parsePgUUID decodes a hyphenated hex string UUID into pgtype.UUID.
// Used at the seam between the Store interface (string-typed) and
// the typed sqlc.Params structs (pgtype.UUID) for every trigger
// store method.
func parsePgUUID(s string) (pgtype.UUID, error) {
	uid, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("state: invalid uuid %q: %w", s, err)
	}
	var p pgtype.UUID
	copy(p.Bytes[:], uid[:])
	p.Valid = true
	return p, nil
}

// mustPgUUID is the same as parsePgUUID but elides the error — used
// when the caller has already validated the input upstream (the
// apid handler's parseTriggerID rejects malformed UUIDs at the HTTP
// boundary). On a malformed input here the row simply doesn't exist
// (we bind the zero uuid, which never matches), so callers see a
// natural "not found" rather than a 500.
func mustPgUUID(s string) pgtype.UUID {
	p, err := parsePgUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return p
}

// pgUUIDString encodes a pgtype.UUID as the canonical hyphenated
// hex string form. Used at the seam between sqlc-generated types
// (pgtype.UUID) and the Store interface's string-typed ids (the
// apid handlers + sched dispatch path take strings throughout).
// Returns the empty string for an invalid pgtype.UUID so callers
// can branch on the empty-string sentinel rather than panicking.
func pgUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		id.Bytes[0:4],
		id.Bytes[4:6],
		id.Bytes[6:8],
		id.Bytes[8:10],
		id.Bytes[10:16],
	)
}

// RetryTriggerRecordByOperator (issue #757 / ADR-0NN, commit #6)
// resets a record's state to 'pending' with attempts=0, last_error
// cleared, and next_fire_at=NOW(). Distinct from
// MarkTriggerRecordRetry (which the dispatcher uses with
// exp-backoff): the operator verb has no exp-backoff and no
// last_error carry-over — the operator is signalling "re-drive this
// record from clean". Returns state.ErrNotFound when the row does
// not exist so the handler can emit a 404.
func (s *PgStore) RetryTriggerRecordByOperator(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update trigger_records
		   set state = 'pending',
		       attempts = 0,
		       last_error = null,
		       next_fire_at = now()
		 where id = $1`,
		id)
	if err != nil {
		return fmt.Errorf("state: retry trigger_record %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RetryQueueDeadLetter (ADR-134 PR-C) is the queue counterpart to
// RetryTriggerRecordByOperator: resets an invocations row in
// state='dead_letter' back to 'pending' with attempts=0,
// last_error cleared, due_at=NOW(), outcome cleared. Stamps
// last_replayed_at = NOW() so the dashboard can render the
// replay history. Scoped to (id, app_id's account) so a customer
// cannot replay a row they do not own — the caller passes the
// accountID resolved from the URL slug.
//
// Returns ErrNotFound when the row is missing, not in
// state='dead_letter', or owned by a different account. The
// dashboard's "Replay" button reads 404 as "no longer
// replayable".
//
// Idempotent: a redelivered replay that finds the row already
// back in 'pending' returns ErrNotFound (the WHERE filters on
// state='dead_letter'). The dashboard renders this as
// "already replayed".
func (s *PgStore) RetryQueueDeadLetter(ctx context.Context, accountID, invocationID string) (Invocation, error) {
	row := s.pool.QueryRow(ctx, `
		update invocations
		   set state = 'pending',
		       attempts = 0,
		       last_error = null,
		       outcome = null,
		       due_at = now(),
		       lease_expires_at = null,
		       instance_id = null,
		       last_replayed_at = now(),
		       completed_at = null
		 where id = $1
		   and account_id = $2
		   and state = 'dead_letter'
		 returning `+invocationSelectCols, invocationID, accountID)
	inv, err := scanInvocation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invocation{}, ErrNotFound
		}
		return Invocation{}, fmt.Errorf("state: retry queue dead_letter %s: %w", invocationID, err)
	}
	return inv, nil
}

// DropTriggerRecordByOperator (issue #757 / ADR-0NN, commit #6)
// deletes the record outright — an operator verb for "this row
// should not be retried". Distinct from the dead_letter transition
// (which preserves history); this verb preserves no DLQ row. The
// record's parent trigger is untouched; this is a record-level
// operation only.
func (s *PgStore) DropTriggerRecordByOperator(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from trigger_records where id = $1`,
		id)
	if err != nil {
		return fmt.Errorf("state: drop trigger_record %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AppUpstreamProbeScore is the JOIN-collapsed per-upstream
// probe summary. One row per (data_upstreams.id, region) with
// the freshest probe's RTT. Old name: aggregate per
// (host_redacted_hash, region) — the new per-app-scoped
// reader returns one row per inferred upstream + region.
//
// The schedd chooser (PR-D) reads this in a single round-trip
// on the wake path. The previous N+1 design (1 ListAllAppDataUpstreams
// + N ListDataUpstreamProbesByHostRegion) cost N+1 PG round-trips
// per wake; for a Scale plan app with DataPlacementHintsPerApp=50,
// that's 51 round-trips on the wake goroutine under appMu —
// far past the < 1 ms wake-budget claim. The JOIN collapses
// the read to a single round-trip.
type AppUpstreamProbeScore struct {
	HostRedactedHash string
	Region           string
	Kind             DataUpstreamKind
	Port             int
	RTTMs            *int
	OK               bool
}

// ListAppUpstreamProbeScores (ADR-098 PR-D) returns the freshest
// probe per (data_upstreams.id, region) for the given app, scoped
// to a single deployment. ADR-098 amendment (issue #954) widens
// the dedupe key to (app_id, scope, deployment_scope, ...); the
// chooser must therefore scope its probe scan to the deployment
// the wake targets — a staging deployment should bias on staging
// probes, not production.
//
// SQL shape (hand-rolled, not sqlc — the JOIN with DISTINCT ON
// is a single statement and adding it via sqlc would require a
// schema-bound regeneration):
//
//	SELECT DISTINCT ON (u.id, p.region)
//	       u.host_redacted_hash, p.region, u.kind, u.port,
//	       p.rtt_ms, p.ok
//	FROM data_upstreams u
//	LEFT JOIN LATERAL (
//	  SELECT region, rtt_ms, ok
//	  FROM data_upstream_probes
//	  WHERE host_redacted_hash = u.host_redacted_hash
//	  ORDER BY sampled_at DESC
//	  LIMIT 1
//	) p ON true
//	WHERE u.account_id = $1 AND u.app_id = $2
//	  AND u.deployment_scope = $3
//	  AND u.declared_region IS NOT NULL
//
// The LATERAL subquery keeps the index-driven lookup hot
// (data_upstream_probes partitioned by sampled_at, the
// (host_redacted_hash, sampled_at) index is the probe-loop's
// hot path). One round-trip per wake.
//
// deploymentScope is required (no fallback defaulting here —
// the caller threads dep.ID from engine.go; pkg/sched/engine.go
// applies defaultDeploymentScope="default" for the cold-path
// branch where dep is nil).
func (s *PgStore) ListAppUpstreamProbeScores(ctx context.Context, accountID, appID, deploymentScope string) ([]AppUpstreamProbeScore, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (u.id, p.region)
		       u.host_redacted_hash, p.region, u.kind, u.port,
		       p.rtt_ms, p.ok
		FROM data_upstreams u
		LEFT JOIN LATERAL (
			SELECT region, rtt_ms, ok
			FROM data_upstream_probes
			WHERE host_redacted_hash = u.host_redacted_hash
			ORDER BY sampled_at DESC
			LIMIT 1
		) p ON true
		WHERE u.account_id = $1 AND u.app_id = $2
		  AND u.deployment_scope = $3
		  AND u.declared_region IS NOT NULL
	`, accountID, appID, deploymentScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppUpstreamProbeScore
	for rows.Next() {
		var s AppUpstreamProbeScore
		var kind string
		var rtt *int32
		if err := rows.Scan(&s.HostRedactedHash, &s.Region, &kind, &s.Port, &rtt, &s.OK); err != nil {
			return nil, err
		}
		if rtt != nil {
			v := int(*rtt)
			s.RTTMs = &v
		}
		s.Kind = DataUpstreamKind(kind)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListAllAppDataUpstreams returns every data_upstreams row on the
// app across all scopes, scoped to accountID. Used by apid's
// GET /v1/apps/{slug}/upstreams?scope=__all__ arm to render the
// full list without a cursor. The row count is bounded by
// DataPlacementHintsPerApp (0/3/10/50 by plan per ADR-098 §D5)
// so the scan is cheap.
func (s *PgStore) ListAllAppDataUpstreams(ctx context.Context, accountID, appID string) ([]DataUpstream, error) {
	rows, err := s.pool.Query(ctx,
		`select id, account_id, app_id, source, scope, deployment_scope, kind, host, port,
		        host_redacted_hash, coalesce(declared_region, ''),
		        last_rtt_ms, last_probed_at, last_seen_at, created_at
		 from data_upstreams
		 where account_id = $1 and app_id = $2
		 order by created_at desc, id desc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataUpstream
	for rows.Next() {
		var (
			id, accountIDpg, appIDpg                                                     pgtype.UUID
			source, scope, deploymentScope, kind, host, hostRedactedHash, declaredRegion string
			port                                                                         int32
			lastRTT                                                                      pgtype.Int4
			lastProbedAt                                                                 pgtype.Timestamptz
			lastSeenAt, createdAt                                                        pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &accountIDpg, &appIDpg, &source, &scope, &deploymentScope, &kind, &host, &port,
			&hostRedactedHash, &declaredRegion,
			&lastRTT, &lastProbedAt, &lastSeenAt, &createdAt); err != nil {
			return nil, err
		}
		var rttPtr *int
		if lastRTT.Valid {
			v := int(lastRTT.Int32)
			rttPtr = &v
		}
		var probedPtr *time.Time
		if lastProbedAt.Valid {
			t := timeFromPgtype(lastProbedAt)
			probedPtr = &t
		}
		out = append(out, DataUpstream{
			ID:        uuidFromPgtype(id),
			AccountID: uuidFromPgtype(accountIDpg),
			AppID:     uuidFromPgtype(appIDpg),
			Source:    DataUpstreamSource(source),
			Scope:     scope,
			// DeploymentScope (ADR-098 amendment issue #954) — the
			// ?scope=__all__ arm of listUpstreams must surface the
			// same deployment overlay the per-page arm does;
			// otherwise the staging-vs-prod view regresses here.
			DeploymentScope:  deploymentScope,
			Kind:             DataUpstreamKind(kind),
			Host:             host,
			Port:             int(port),
			HostRedactedHash: hostRedactedHash,
			DeclaredRegion:   declaredRegion,
			LastRTTMs:        rttPtr,
			LastProbedAt:     probedPtr,
			LastSeenAt:       timeFromPgtype(lastSeenAt),
			CreatedAt:        timeFromPgtype(createdAt),
		})
	}
	return out, rows.Err()
}

// CountDataUpstreamsByApp is the per-plan quota helper. Counts
// ALL scope values for the app per ADR-098 §D5
// (DataPlacementHintsPerApp is per-app, not per-scope). Mirrors
// CountAppEnv's posture.
func (s *PgStore) CountDataUpstreamsByApp(ctx context.Context, accountID, appID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`select count(*) from data_upstreams where account_id = $1 and app_id = $2`,
		accountID, appID).Scan(&n)
	return n, err
}

// ListDistinctUpstreamHostHashes walks data_upstreams and
// returns the deduplicated set of
// (host_redacted_hash, kind, port, host) tuples. Used by the
// meterd probe loop (PR-C).
//
// The plaintext host is included in the row so the probe can
// resolve the dial address. §11 invariant scope: the host
// NEVER reaches the Prom metric labels, the audit emit, the
// pg_notify payload, or any log line. The probe carries the
// plaintext in-process ONLY for the duration of the dial,
// then drops it. The customer-facing list/get endpoints
// return host_redacted_hash + host_last4 — NEVER plaintext.
func (s *PgStore) ListDistinctUpstreamHostHashes(ctx context.Context) ([]DataUpstreamTarget, error) {
	rows, err := s.pool.Query(ctx,
		`select host_redacted_hash, kind, port, host
		 from data_upstreams
		 group by host_redacted_hash, kind, port, host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataUpstreamTarget
	for rows.Next() {
		var t DataUpstreamTarget
		var kind string
		if err := rows.Scan(&t.HostRedactedHash, &kind, &t.Port, &t.Host); err != nil {
			return nil, err
		}
		t.Kind = DataUpstreamKind(kind)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Consumer keys (ADR-120 / issue #975 item #5).
//
// Tenancy-at-the-boundary: every read pins (account_id, ...) in the
// WHERE; cross-tenant probes collapse to ErrNotFound. The pg path is
// the floor — memstore matches its strictness (see
// memstore_consumer_keys.go for the symmetric contract).
//
// We deliberately write the SQL by hand (not via sqlc) for v1:
// consumer_keys is small (Scale cap 1000/app × ~few accounts) and the
// five methods are pure CRUD. When the surface grows (consumer_id
// joins in #6/#7/#8), this becomes a sqlc concern; for now the
// explicit SQL keeps the IDOR-safe predicates visible in one place.

// scanConsumerKeyRow scans a single row selected with the standard
// consumer_keys column order (see consumerKeySelectCols). Tolerant
// of NULL on the optional timestamp columns: ExpiresAt / LastUsedAt
// / RevokedAt come back as pgtype.Timestamptz{} when the row has
// them unset, and timeFromPgtype returns nil for the zero value.
func scanConsumerKeyRow(row pgx.Row) (ConsumerKey, error) {
	var k ConsumerKey
	var scopes []string
	var expiresAt, lastUsedAt, revokedAt *time.Time
	if err := row.Scan(
		&k.ID,
		&k.AccountID,
		&k.AppID,
		&k.Name,
		&k.Prefix,
		&k.Hash,
		&scopes,
		&k.CreatedAt,
		&expiresAt,
		&lastUsedAt,
		&revokedAt,
	); err != nil {
		return ConsumerKey{}, err
	}
	k.Scopes = scopes
	k.ExpiresAt = expiresAt
	k.LastUsedAt = lastUsedAt
	k.RevokedAt = revokedAt
	return k, nil
}

// consumerKeySelectCols is the column list every read uses. Keeping
// the order stable means scanConsumerKeyRow above is the single scan
// helper, not five copies.
const consumerKeySelectCols = `id, account_id, app_id, name, prefix, hashed_secret, scopes, created_at, expires_at, last_used_at, revoked_at`

func (s *PgStore) CreateConsumerKey(ctx context.Context, accountID, appID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) (ConsumerKey, error) {
	if accountID == "" || appID == "" {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKey: empty account_id or app_id")
	}
	// pgx requires a non-nil []byte for NOT NULL bytea; nil maps to
	// SQL NULL and the migration's CHECK would reject. The caller
	// always supplies a 32-byte SHA-256 hash; this is a defence.
	if len(hash) != 32 {
		return ConsumerKey{}, fmt.Errorf("pgstore: CreateConsumerKey: hash must be 32 bytes, got %d", len(hash))
	}
	if len(scopes) == 0 {
		return ConsumerKey{}, errors.New("pgstore: CreateConsumerKey: scopes cannot be empty (closed-set CHECK in 00329)")
	}
	row := s.pool.QueryRow(ctx,
		`insert into consumer_keys
		   (account_id, app_id, name, prefix, hashed_secret, scopes, expires_at)
		 values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
		 returning `+consumerKeySelectCols,
		accountID, appID, name, prefix, hash, scopes, expiresAt)
	k, err := scanConsumerKeyRow(row)
	if err != nil {
		// 23505 = unique_violation; the (account_id, app_id, name)
		// UNIQUE tripped. Mirrors the alert_deliveries / CreateAccount
		// pattern — surface as ErrConflict so callers can use
		// errors.Is(err, state.ErrConflict) independent of the pg path.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ConsumerKey{}, ErrConflict
		}
		return ConsumerKey{}, err
	}
	return k, nil
}

func (s *PgStore) GetConsumerKeyByID(ctx context.Context, accountID, keyID string) (ConsumerKey, error) {
	if accountID == "" || keyID == "" {
		return ConsumerKey{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+consumerKeySelectCols+`
		   from consumer_keys
		  where id = $1::uuid and account_id = $2::uuid`,
		keyID, accountID)
	k, err := scanConsumerKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumerKey{}, ErrNotFound
	}
	return k, err
}

func (s *PgStore) ListConsumerKeysForApp(ctx context.Context, accountID, appID string) ([]ConsumerKey, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx,
		`select `+consumerKeySelectCols+`
		   from consumer_keys
		  where account_id = $1::uuid and app_id = $2::uuid
		  order by created_at desc`,
		accountID, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConsumerKey
	for rows.Next() {
		k, err := scanConsumerKeyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeConsumerKey stamps revoked_at = now(). Idempotent: a second
// call returns the same (already-revoked) row without error. We
// don't filter on revoked_at IS NULL because the audit-trail
// semantics demand that revoke-after-revoke still succeeds (the
// response body carries the original revoke timestamp).
func (s *PgStore) RevokeConsumerKey(ctx context.Context, accountID, keyID string) (ConsumerKey, error) {
	if accountID == "" || keyID == "" {
		return ConsumerKey{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`update consumer_keys
		    set revoked_at = coalesce(revoked_at, now())
		  where id = $1::uuid and account_id = $2::uuid
		  returning `+consumerKeySelectCols,
		keyID, accountID)
	k, err := scanConsumerKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumerKey{}, ErrNotFound
	}
	return k, err
}

// TouchConsumerKeyLastUsed stamps last_used_at = now(). Called
// fire-and-forget from the gateway middleware; we don't fail the
// request if the touch fails (best-effort observability).
func (s *PgStore) TouchConsumerKeyLastUsed(ctx context.Context, keyID string) error {
	if keyID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`update consumer_keys
		    set last_used_at = now()
		  where id = $1::uuid
		    and revoked_at is null`,
		keyID)
	return err
}

// ConsumerKeyByAppAndPrefix is the gateway-side hot-path lookup.
// (app_id, prefix) is the unique hot-path key — see the
// consumer_keys_app_prefix_idx UNIQUE index in 00329 (the UNIQUE
// is load-bearing: a non-UNIQUE index would let a birthday-bound
// ~0.78% collision at Scale cap of 1000 keys/app silently collapse
// the lookup to "first row found by the planner" and the constant-
// time hash compare would run against the wrong key's bytes).
//
// CONTRACT: this method returns the row regardless of revoked_at
// or expires_at. The caller (gatewayd-internal middleware in PR #5-C)
// MUST call Active(now) on the returned row before the hash compare.
// This is intentional — the read path stays passive so the audit
// trail (revoked_at TIMESTAMPTZ + last_used_at) survives gateway
// reads; the caller-side Active() is the single source of truth for
// "is this key still usable right now". TouchConsumerKeyLastUsed
// stays filtered on revoked_at IS NULL (no observability point in
// stamping a revoked row's last_used_at).
func (s *PgStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerKey, error) {
	if accountID == "" || appID == "" || prefix == "" {
		return ConsumerKey{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`select `+consumerKeySelectCols+`
		   from consumer_keys
		  where app_id = $1::uuid
		    and account_id = $2::uuid
		    and prefix = $3`,
		appID, accountID, prefix)
	k, err := scanConsumerKeyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumerKey{}, ErrNotFound
	}
	return k, err
}

// ----------------------------------------------------------------------------
// mirror_rules + mirror_invocation_results (issue #72 / ADR-125)
//
// Storage shape mirrors the edge_rules section above: a single
// mirrorRuleSelectCols constant + scanMirrorRuleCols helper is the
// column order contract. Every SELECT against mirror_rules binds
// against this list, so a future column add lands in one commit.
//
// CreateMirrorRuleIfUnderQuota uses the same FOR UPDATE row lock on
// apps that CreateEdgeRuleIfUnderQuota uses — the cheap-tier
// guardrail that keeps a burst of N parallel inserts from racing
// past the per-app cap by N-1. The mirror rule count is on the
// apps row (not on a per-deployment row), so the picker cache
// refresh path keyed on app_id (pkg/gateway/pgbackend.go) can
// read the active rule set in a single OAuth-throttled query.
//
// Cross-app + cross-deployment validation is enforced at the SQL
// CHECK level (migrations/00348) AND inside CreateMirrorRuleIfUnderQuota
// so the handler can return a precise 422 / 409 instead of a
// generic 23505 FK violation.
// ----------------------------------------------------------------------------

const mirrorRuleSelectCols = `id, account_id, app_id, source_deployment_id,
       mirror_deployment_id, percent, enabled, include_body, redact_headers,
       created_at, updated_at`

const mirrorResultSelectCols = `id, mirror_rule_id, account_id, app_id,
       source_deployment_id, mirror_deployment_id, instance_id, source_instance_id,
       status_code, source_status_code, latency_ms, source_latency_ms,
       body_hash, source_body_hash, schema_hash, source_schema_hash,
       status_diff, schema_diff, body_diff, crashed, request_id, completed_at`

// scanMirrorRule reads a single mirror_rule row. ErrNotFound on
// no-rows; mapErr handles raw errors (e.g. constraint violations).
func (s *PgStore) scanMirrorRule(row pgx.Row) (MirrorRule, error) {
	r, err := scanMirrorRuleCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MirrorRule{}, ErrNotFound
		}
		return MirrorRule{}, err
	}
	return r, nil
}

// scanMirrorRules walks the Rows iterator. Caller owns Close().
func (s *PgStore) scanMirrorRules(rows pgx.Rows) ([]MirrorRule, error) {
	var out []MirrorRule
	for rows.Next() {
		r, err := scanMirrorRuleCols(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanMirrorRuleCols is the single source of column order for
// mirror_rules. SELECT clauses above cite this exact list.
func scanMirrorRuleCols(scan func(...any) error) (MirrorRule, error) {
	var (
		r             MirrorRule
		redactHeaders []string
	)
	if err := scan(
		&r.ID, &r.AccountID, &r.AppID, &r.SourceDeploymentID,
		&r.MirrorDeploymentID, &r.Percent, &r.Enabled, &r.IncludeBody,
		&redactHeaders, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return MirrorRule{}, err
	}
	if redactHeaders == nil {
		redactHeaders = []string{}
	}
	r.RedactHeaders = redactHeaders
	return r, nil
}

// scanMirrorResult reads a single mirror_invocation_results row.
func scanMirrorResult(scan func(...any) error) (MirrorInvocationResult, error) {
	var r MirrorInvocationResult
	var statusCode, sourceStatusCode, latencyMs, sourceLatencyMs *int
	// instance_id / source_instance_id are nullable text columns
	// (NULL when the mirror wake failed — the customer-facing POST
	// returned a valid source response but the mirror VM never
	// came up). Scan into *string helpers and dereference so pgx
	// can return NULL cleanly; scanning into the non-pointer field
	// crashes pgx v5 with "cannot scan NULL into *string" on the
	// first failed-wake row, dropping the entire result set and
	// 500-ing the customer-facing summary endpoint.
	var instanceID, sourceInstanceID *string
	if err := scan(
		&r.ID, &r.MirrorRuleID, &r.AccountID, &r.AppID,
		&r.SourceDeploymentID, &r.MirrorDeploymentID, &instanceID, &sourceInstanceID,
		&statusCode, &sourceStatusCode, &latencyMs, &sourceLatencyMs,
		&r.BodyHash, &r.SourceBodyHash, &r.SchemaHash, &r.SourceSchemaHash,
		&r.StatusDiff, &r.SchemaDiff, &r.BodyDiff, &r.Crashed, &r.RequestID, &r.CompletedAt,
	); err != nil {
		return MirrorInvocationResult{}, err
	}
	if instanceID != nil {
		r.InstanceID = *instanceID
	}
	if sourceInstanceID != nil {
		r.SourceInstanceID = *sourceInstanceID
	}
	if statusCode != nil {
		r.StatusCode = *statusCode
	}
	if sourceStatusCode != nil {
		r.SourceStatusCode = *sourceStatusCode
	}
	if latencyMs != nil {
		r.LatencyMs = *latencyMs
	}
	if sourceLatencyMs != nil {
		r.SourceLatencyMs = *sourceLatencyMs
	}
	return r, nil
}

// CreateMirrorRuleIfUnderQuota — see Store interface. Returns:
//   - (MirrorRule{}, *QuotaError) when Limits.MirrorTargetsPerApp trips
//   - (MirrorRule{}, ErrMirrorDeploymentNotLive) when source or
//     mirror deployment is missing / not live / wrong app
//   - (MirrorRule{}, ErrInvalidMirrorPercent) on out-of-range
//   - (MirrorRule{}, ErrMirrorSourceTargetSame) when source == mirror
//   - (MirrorRule{}, ErrMirrorCrossAppMismatch) when the source
//     deployment's app_id differs from in.AppID (cross-app mirrors
//     are ADR-125 §follow-on 4, not first-class)
//
// The FOR UPDATE row lock on apps is the TOCTOU defence: concurrent
// inserts serialise on the apps row before reading the count, so a
// burst of N parallel inserts can't race past the cap by N-1.
func (s *PgStore) CreateMirrorRuleIfUnderQuota(ctx context.Context, in CreateMirrorRuleParams, limits api.Limits) (MirrorRule, error) {
	if in.Percent < 0 || in.Percent > 100 {
		return MirrorRule{}, ErrInvalidMirrorPercent
	}
	if in.SourceDeploymentID == in.MirrorDeploymentID {
		return MirrorRule{}, ErrMirrorSourceTargetSame
	}
	redactHeaders := in.RedactHeaders
	if redactHeaders == nil {
		redactHeaders = []string{}
	}
	if len(redactHeaders) > 32 {
		return MirrorRule{}, fmt.Errorf("state: mirror_rules redact_headers has %d entries (cap 32)", len(redactHeaders))
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MirrorRule{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// Lock the apps row so concurrent inserts serialise.
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from apps where id = $1::uuid and status <> 'deleted' for update`, in.AppID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MirrorRule{}, ErrNotFound
		}
		return MirrorRule{}, fmt.Errorf("state: lock app %s: %w", in.AppID, err)
	}

	// Per-app count gate (Limits.MirrorTargetsPerApp; Free 0 /
	// Hobby 0 / Pro 1 / Scale 3). The plan gate (Free/Hobby lock)
	// is the handler's job — Plan.MirrorRuleAllowed() returns false
	// at the http boundary so we never see a Free/Hobby request here.
	var appCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from mirror_rules where app_id = $1::uuid`, in.AppID,
	).Scan(&appCount); err != nil {
		return MirrorRule{}, fmt.Errorf("state: count mirror_rules for app %s: %w", in.AppID, err)
	}
	if appCount >= limits.MirrorTargetsPerApp {
		return MirrorRule{}, &QuotaError{
			Kind:     QuotaErrorKindMirror,
			Limit:    limits.MirrorTargetsPerApp,
			Observed: appCount,
		}
	}

	// Validate source + mirror deployments. Both must be live
	// (operators mirror against live rows, same as traffic split)
	// AND belong to the same app (a single mirror_rule is
	// app-scoped; cross-app is ADR-125 §follow-on 4).
	for _, depID := range []string{in.SourceDeploymentID, in.MirrorDeploymentID} {
		var appID string
		var status string
		if err := tx.QueryRow(ctx,
			`select app_id, status from deployments where id = $1::uuid`, depID,
		).Scan(&appID, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return MirrorRule{}, ErrMirrorDeploymentNotLive
			}
			return MirrorRule{}, fmt.Errorf("state: read deployment %s: %w", depID, err)
		}
		if appID != in.AppID {
			return MirrorRule{}, ErrMirrorCrossAppMismatch
		}
		if status != "live" {
			return MirrorRule{}, ErrMirrorDeploymentNotLive
		}
	}

	row := tx.QueryRow(ctx, `
		insert into mirror_rules (
			account_id, app_id, source_deployment_id, mirror_deployment_id,
			percent, enabled, include_body, redact_headers
		) values (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid,
			$5, $6, $7, $8
		)
		returning `+mirrorRuleSelectCols,
		in.AccountID, in.AppID, in.SourceDeploymentID, in.MirrorDeploymentID,
		in.Percent, in.Enabled, in.IncludeBody, redactHeaders,
	)
	r, err := s.scanMirrorRule(row)
	if err != nil {
		return MirrorRule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MirrorRule{}, fmt.Errorf("state: commit create mirror rule: %w", err)
	}
	return r, nil
}

// ListMirrorRules returns every rule for the app, ordered by
// created_at ASC. The gateway picker reads from this in the
// deployment_changed pg_notify refresh path; MemStore's in-memory
// mirror makes it testable without a DB.
func (s *PgStore) ListMirrorRules(ctx context.Context, appID string) ([]MirrorRule, error) {
	rows, err := s.pool.Query(ctx, `
		select `+mirrorRuleSelectCols+`
		from mirror_rules
		where app_id = $1::uuid
		order by created_at asc`,
		appID)
	if err != nil {
		return nil, fmt.Errorf("state: list mirror_rules for app %s: %w", appID, err)
	}
	defer rows.Close() //nolint:errcheck // short-lived iterator
	return s.scanMirrorRules(rows)
}

// GetMirrorRuleByID returns a single rule by id. IDOR safety is the
// caller's responsibility (apid's loadApp + AccountID check); this
// method scopes the read by id alone.
func (s *PgStore) GetMirrorRuleByID(ctx context.Context, id string) (MirrorRule, error) {
	row := s.pool.QueryRow(ctx, `
		select `+mirrorRuleSelectCols+`
		from mirror_rules
		where id = $1::uuid`,
		id)
	return s.scanMirrorRule(row)
}

// UpdateMirrorRule applies a partial update via MirrorRulePatch.
// Pointer fields let the caller distinguish "absent" from "zero"
// (Percent=0 disables the rule without removing it). Same FOR
// UPDATE discipline as CreateMirrorRuleIfUnderQuota so concurrent
// writers serialise on the same apps row.
func (s *PgStore) UpdateMirrorRule(ctx context.Context, id string, patch MirrorRulePatch) (MirrorRule, error) {
	// Validate Percent range + RedactHeaders cap before opening the tx.
	if patch.Percent != nil && (*patch.Percent < 0 || *patch.Percent > 100) {
		return MirrorRule{}, ErrInvalidMirrorPercent
	}
	if patch.RedactHeaders != nil && len(*patch.RedactHeaders) > 32 {
		return MirrorRule{}, fmt.Errorf("state: mirror_rules redact_headers has %d entries (cap 32)", len(*patch.RedactHeaders))
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return MirrorRule{}, fmt.Errorf("state: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// Lock the rule row + its parent app. The apps-lock is what
	// serialises concurrent CreateMirrorRuleIfUnderQuota against
	// this same rule (both take the apps FOR UPDATE lock).
	var appID string
	if err := tx.QueryRow(ctx,
		`select app_id from mirror_rules where id = $1::uuid for update`, id,
	).Scan(&appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MirrorRule{}, ErrNotFound
		}
		return MirrorRule{}, fmt.Errorf("state: lock mirror_rule %s: %w", id, err)
	}
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from apps where id = $1::uuid and status <> 'deleted' for update`, appID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MirrorRule{}, ErrNotFound
		}
		return MirrorRule{}, fmt.Errorf("state: lock app %s: %w", appID, err)
	}

	// Build the SET clause from the patch. Pointer fields that are
	// nil are skipped via COALESCE($n, col) — this is the standard
	// pattern for partial-update SQL and keeps the call site free
	// of dynamic SQL string-build.
	var (
		setPercent       *int
		setEnabled       *bool
		setIncludeBody   *bool
		setRedactHeaders *[]string
	)
	if patch.Percent != nil {
		p := *patch.Percent
		setPercent = &p
	}
	if patch.Enabled != nil {
		b := *patch.Enabled
		setEnabled = &b
	}
	if patch.IncludeBody != nil {
		b := *patch.IncludeBody
		setIncludeBody = &b
	}
	if patch.RedactHeaders != nil {
		headers := *patch.RedactHeaders
		if headers == nil {
			headers = []string{}
		}
		setRedactHeaders = &headers
	}
	row := tx.QueryRow(ctx, `
		update mirror_rules
		set percent        = coalesce($2::int,        percent),
		    enabled        = coalesce($3::boolean,    enabled),
		    include_body   = coalesce($4::boolean,    include_body),
		    redact_headers = coalesce($5::text[],     redact_headers),
		    updated_at     = now()
		where id = $1::uuid
		returning `+mirrorRuleSelectCols,
		id, setPercent, setEnabled, setIncludeBody, setRedactHeaders,
	)
	r, err := s.scanMirrorRule(row)
	if err != nil {
		return MirrorRule{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MirrorRule{}, fmt.Errorf("state: commit update mirror rule: %w", err)
	}
	return r, nil
}

// DeleteMirrorRule removes a rule. ON DELETE CASCADE on mirror_rules
// (migrations/00350) cascades to mirror_invocation_results, so a
// single DELETE clears the customer's history along with the rule.
func (s *PgStore) DeleteMirrorRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from mirror_rules where id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("state: delete mirror_rule %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertMirrorResult appends one row to the mirror ledger. Best-
// effort: the caller logs the error but doesn't roll back the
// customer-facing response. Status / latency / body / schema fields
// are NULL when the mirror wake failed (the customer-facing POST
// returned a valid source response, but the mirror never came up).
//
// NULL contract for the bytea columns (issue #72 / ADR-125): body_hash
// and source_body_hash are NULL when the rule has include_body=false
// (the safe-by-default posture — sensitive bodies are explicitly
// opted in via per-rule). pgx v5 encodes a typed nil `[]byte(nil)` as
// SQL NULL, but a coerced `[]byte{}` (empty non-nil) as a 0-length
// bytea. We MUST preserve the typed-nil distinction so downstream
// tooling (e.g. body_diff skip, schema-evolution audit) can use
// the column's NULL state to honour include_body=false. Coercing
// nil → []byte{} collapses "body intentionally not hashed" with
// "body present but empty" — a silent contract violation.
func (s *PgStore) InsertMirrorResult(ctx context.Context, r MirrorInvocationResult) error {
	bodyHash := r.BodyHash // typed-nil preserved on insert
	srcBodyHash := r.SourceBodyHash
	schemaHash := r.SchemaHash
	srcSchemaHash := r.SourceSchemaHash
	// Status/latency are nullable because the mirror wake can fail
	// (StatusCode=0 → NULL), hence the pointer dance. Body/hash
	// columns are the only other nullable fields.
	var (
		instanceID    *string
		srcInstanceID *string
		statusCode    *int
		srcStatusCode *int
		latencyMs     *int
		srcLatencyMs  *int
	)
	if r.InstanceID != "" {
		v := r.InstanceID
		instanceID = &v
	}
	if r.SourceInstanceID != "" {
		v := r.SourceInstanceID
		srcInstanceID = &v
	}
	if r.StatusCode != 0 {
		v := r.StatusCode
		statusCode = &v
	}
	if r.SourceStatusCode != 0 {
		v := r.SourceStatusCode
		srcStatusCode = &v
	}
	if r.LatencyMs != 0 {
		v := r.LatencyMs
		latencyMs = &v
	}
	if r.SourceLatencyMs != 0 {
		v := r.SourceLatencyMs
		srcLatencyMs = &v
	}
	_, err := s.pool.Exec(ctx, `
		insert into mirror_invocation_results (
			mirror_rule_id, account_id, app_id,
			source_deployment_id, mirror_deployment_id,
			instance_id, source_instance_id,
			status_code, source_status_code, latency_ms, source_latency_ms,
			body_hash, source_body_hash, schema_hash, source_schema_hash,
			status_diff, schema_diff, body_diff, crashed, request_id, completed_at
		) values (
			$1::uuid, $2::uuid, $3::uuid,
			$4::uuid, $5::uuid,
			$6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21
		)`,
		r.MirrorRuleID, r.AccountID, r.AppID,
		r.SourceDeploymentID, r.MirrorDeploymentID,
		instanceID, srcInstanceID,
		statusCode, srcStatusCode, latencyMs, srcLatencyMs,
		bodyHash, srcBodyHash, schemaHash, srcSchemaHash,
		r.StatusDiff, r.SchemaDiff, r.BodyDiff, r.Crashed, r.RequestID, r.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("state: insert mirror_invocation_result: %w", err)
	}
	return nil
}

// ListMirrorResults returns up to `limit` rows for a rule with
// completed_at >= `since`, ordered DESC. limit <= 0 means "no
// cap" (matches the same contract ListDeploymentsForApp uses).
func (s *PgStore) ListMirrorResults(ctx context.Context, ruleID string, since time.Time, limit int) ([]MirrorInvocationResult, error) {
	rows, err := s.pool.Query(ctx, `
		select `+mirrorResultSelectCols+`
		from mirror_invocation_results
		where mirror_rule_id = $1::uuid
		  and completed_at >= $2
		order by completed_at desc
		limit $3`,
		ruleID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list mirror_invocation_results for rule %s: %w", ruleID, err)
	}
	defer rows.Close() //nolint:errcheck // short-lived iterator
	var out []MirrorInvocationResult
	for rows.Next() {
		r, err := scanMirrorResult(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MirrorSummary aggregates the rows in the same window via SQL
// aggregates (COUNT/SUM/AVG/p99_cont) — never by client-side
// iteration. The latency diff is signed (mirror_ms - source_ms,
// positive = mirror is slower).
func (s *PgStore) MirrorSummary(ctx context.Context, ruleID string, since time.Time) (MirrorSummary, error) {
	var s2 MirrorSummary
	var meanLatencyDiff *float64
	var p99LatencyDiff *float64
	if err := s.pool.QueryRow(ctx, `
		select
			count(*),
			coalesce(sum(case when status_diff then 1 else 0 end), 0),
			coalesce(sum(case when schema_diff then 1 else 0 end), 0),
			coalesce(sum(case when body_diff   then 1 else 0 end), 0),
			coalesce(sum(case when crashed     then 1 else 0 end), 0),
			avg(case when latency_ms is not null and source_latency_ms is not null
			         then (latency_ms - source_latency_ms)::double precision
			         else null end),
			percentile_cont(0.99) within group (order by case
				when latency_ms is not null and source_latency_ms is not null
				then (latency_ms - source_latency_ms)::double precision
				else null end)
		from mirror_invocation_results
		where mirror_rule_id = $1::uuid
		  and completed_at >= $2`,
		ruleID, since,
	).Scan(&s2.TotalInvocations, &s2.StatusDiffCount, &s2.SchemaDiffCount, &s2.BodyDiffCount, &s2.CrashCount, &meanLatencyDiff, &p99LatencyDiff); err != nil {
		return MirrorSummary{}, fmt.Errorf("state: mirror summary for rule %s: %w", ruleID, err)
	}
	if meanLatencyDiff != nil {
		s2.MeanLatencyDiffMs = int(*meanLatencyDiff)
	}
	if p99LatencyDiff != nil {
		s2.P99LatencyDiffMs = int(*p99LatencyDiff)
	}
	return s2, nil
}

// ProvisionedStaticEgressIPExists (ADR-119 redesign) is the
// apid-side gate. Returns true iff the (account_id, customer_ip)
// tuple is in the operator-provisioned set. The vmmd bundle
// reload writes the table from the operator's TOML on SIGHUP;
// the apid PUT path reads it here. A false return is the
// "not provisioned" surface the customer sees as 404 Not
// Found (api.ErrStaticEgressIPNotProvisioned).
//
// Implementation note: the lookup is a single-row PK read
// against the `(account_id, customer_ip)` composite index
// declared by migration 00337. Sub-millisecond under realistic
// load.
func (s *PgStore) ProvisionedStaticEgressIPExists(ctx context.Context, accountID string, ip netip.Addr) (bool, error) {
	if accountID == "" || !ip.Is4() {
		return false, nil
	}
	var found bool
	row := s.pool.QueryRow(ctx,
		`select exists(
		   select 1
		     from provisioned_static_egress_ips
		    where account_id = $1::uuid
		      and customer_ip = $2::inet
		)`,
		accountID, ip.String())
	if err := row.Scan(&found); err != nil {
		return false, fmt.Errorf("state: ProvisionedStaticEgressIPExists: %w", err)
	}
	return found, nil
}

// ReplaceProvisionedStaticEgressIPs (ADR-119 redesign) is the
// vmmd-side write that mirrors the operator's TOML into the
// Postgres gate table. The watcher calls this on every SIGHUP
// (and once at startup). The store clears the table for the
// given account_id, then inserts the new set inside a single
// transaction — the visible-state invariant is "either the
// prior set OR the new set, never a partial mix". Empty
// `ips` removes all rows for the account (the "revoke
// provisioning" path).
//
// The DELETE + INSERT pair runs in one transaction so a
// concurrent apid PUT either sees the prior set or the new
// set, not a partial empty+insert gap. The `customer_ip` v4
// CHECK on the table (migration 00337) rejects non-IPv4 inputs
// at the database boundary; the caller-side deny-set gate is
// `api.ValidateStaticEgressIP` (defence in depth).
func (s *PgStore) ReplaceProvisionedStaticEgressIPs(ctx context.Context, accountID string, ips []netip.Addr) error {
	if accountID == "" {
		return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: empty account_id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`delete from provisioned_static_egress_ips where account_id = $1::uuid`,
		accountID); err != nil {
		return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: delete: %w", err)
	}
	for _, ip := range ips {
		if !ip.Is4() {
			return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: rejecting non-v4 %s", ip)
		}
		if _, err := tx.Exec(ctx,
			`insert into provisioned_static_egress_ips (account_id, customer_ip)
			  values ($1::uuid, $2::inet)`,
			accountID, ip.String()); err != nil {
			return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: insert %s: %w", ip, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: ReplaceProvisionedStaticEgressIPs: commit: %w", err)
	}
	return nil
}

// RecordMailSuppression writes a row for a hard-bounce or complaint
// (issue #246 acceptance item 7). Maps the domain MailSuppressionInput
// to the sqlc-generated query parameters; pgtype.UUID / pgtype.Timestamptz
// wrap the nullable account_id and expires_at so a nil pointer round-trips
// to NULL. Returns inserted=true on a fresh row, false on a replay that
// hit the (source, provider_event_id) unique index — the bounce handler
// reads the bool to decide whether to advance dunning (fresh) or skip
// (replay). A SQLSTATE 23505 from a parallel writer is mapped to
// ErrConflict per the pr-1000 convention.
func (s *PgStore) RecordMailSuppression(ctx context.Context, in MailSuppressionInput) (bool, error) {
	if in.Email == "" {
		return false, fmt.Errorf("state: RecordMailSuppression: email required")
	}
	if in.ProviderEventID == "" {
		return false, fmt.Errorf("state: RecordMailSuppression: provider_event_id required")
	}
	if in.Reason == "" {
		return false, fmt.Errorf("state: RecordMailSuppression: reason required (one of hard_bounce / complaint / manual)")
	}
	if in.Source == "" {
		return false, fmt.Errorf("state: RecordMailSuppression: source required (one of resend / postmark / operator)")
	}
	var accountID pgtype.UUID
	if in.AccountID != nil {
		if err := accountID.Scan(*in.AccountID); err != nil {
			return false, fmt.Errorf("state: RecordMailSuppression: account_id: %w", err)
		}
	}
	var expiresAt pgtype.Timestamptz
	if in.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *in.ExpiresAt, Valid: true}
	}
	q := sqlc.New()
	inserted, err := q.RecordMailSuppression(ctx, s.pool, sqlc.RecordMailSuppressionParams{
		AccountID:       accountID,
		Email:           in.Email,
		Reason:          string(in.Reason),
		Source:          string(in.Source),
		ProviderEventID: in.ProviderEventID,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		// Two writers can race on the unique index; the loser sees
		// SQLSTATE 23505. Surface it as ErrConflict per pr-1000 so
		// a caller that wants strict failure on duplicate event id
		// can detect it without parsing pgx's wrapped PgError.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return false, ErrConflict
		}
		return false, fmt.Errorf("state: RecordMailSuppression: %w", err)
	}
	return inserted, nil
}

// IsMailSuppressed reports whether any active suppression matches
// the address (issue #246 acceptance item 7). "Active" means
// expires_at IS NULL OR expires_at > now(); the partial index
// keeps expired rows out of the lookup so a row that fell out of
// TTL does not block future mail to that address.
func (s *PgStore) IsMailSuppressed(ctx context.Context, email string) (bool, error) {
	if email == "" {
		return false, fmt.Errorf("state: IsMailSuppressed: email required")
	}
	q := sqlc.New()
	return q.IsMailSuppressed(ctx, s.pool, email)
}

// ----------------------------------------------------------------------------
// ADR-134 PR-B: per-account async concurrency cap (account_async_quota)
// + invocations retention reaper.
//
// The counter-table pattern is deliberate: see migrations/00541 for the
// rationale. The cap is enforced atomically with the claim transition so
// the drain cannot exceed the plan's MaxAsyncInvocationsPerAccount even
// under concurrent claim. Decrement happens in the same transaction as
// every terminal transition (Complete / Fail / Cancel) so the counter
// converges even on crash-recovery via the requeue-expired path (which
// itself decrements).
// ----------------------------------------------------------------------------

// EnsureAccountAsyncQuota upserts the account's cap row. Called by apid
// at plan-change time and by the drain on first sight of a new
// account_id. ON CONFLICT DOES overwrite max_inflight when the row
// already exists — this is the operator/plan-change intent; the
// per-claim lazy-insert path uses a different helper
// (upsertAccountAsyncQuotaTx) that is DO NOTHING on conflict so a
// transient lookup failure in the drain cannot poison the existing
// row's cap.
//
// Returns the resulting (max_inflight, current_inflight) pair. Returns
// (0, 0) on conflict when the row already had max_inflight=0 (e.g. a
// Free account whose cap is 0 because MaxAsyncInvocationsPerAccount
// was set to 0 by an operator); the caller must treat that as
// "claim not allowed".
func (s *PgStore) EnsureAccountAsyncQuota(ctx context.Context, accountID string, maxInflight int) (int, int, error) {
	row := s.pool.QueryRow(ctx, `
		insert into account_async_quota (account_id, max_inflight)
		values ($1, $2)
		on conflict (account_id) do update
		  set max_inflight = excluded.max_inflight,
		      updated_at = now()
		returning max_inflight, current_inflight`,
		accountID, maxInflight)
	var gotMax, gotCur int
	if err := row.Scan(&gotMax, &gotCur); err != nil {
		return 0, 0, fmt.Errorf("state: account_async_quota upsert: %w", err)
	}
	return gotMax, gotCur, nil
}

// GetAccountAsyncQuota returns the cap row for an account. Returns
// ErrNotFound when no row exists (caller should call EnsureAccountAsyncQuota
// first or use ClaimInvocationWithCap which lazy-inserts).
func (s *PgStore) GetAccountAsyncQuota(ctx context.Context, accountID string) (int, int, error) {
	row := s.pool.QueryRow(ctx,
		`select max_inflight, current_inflight from account_async_quota where account_id = $1`,
		accountID)
	var max, cur int
	if err := row.Scan(&max, &cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, fmt.Errorf("state: account_async_quota get: %w", err)
	}
	return max, cur, nil
}

// ClaimInvocationWithCap is the cap-aware variant of ClaimInvocation
// (ADR-134 PR-B). Wraps the pending→dispatching transition + lease
// stamp + attempts bump AND the per-account counter increment in one
// transaction so the cap check cannot race the state transition.
//
// Returns:
//   - the claimed Invocation on success
//   - ErrNotFound when the row is missing or not in 'pending'
//   - ErrQuotaExceeded when the account has reached its
//     MaxAsyncInvocationsPerAccount cap (the UPDATE returns 0 rows).
//
// When the cap row is missing, this method lazily inserts it with
// maxInflight from the caller so apid does not need a provisioning
// step. Lazy-insert is safe: ON CONFLICT preserves current_inflight
// for accounts that already have a counter row.
//
// Decrement is the caller's responsibility — see
// DecrementAccountAsyncInflight, which CompleteInvocation /
// FailInvocation / CancelInvocation call.
func (s *PgStore) ClaimInvocationWithCap(ctx context.Context, id, instanceID string, leaseSeconds, maxInflight int) (Invocation, error) {
	leaseText := strconv.Itoa(leaseSeconds) + " seconds"
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Invocation{}, fmt.Errorf("state: invocations claim cap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read account_id off the row, then upsert the cap row with
	// maxInflight from the caller. The upsert is a no-op for
	// accounts that already have a counter row.
	var accountID string
	if err := tx.QueryRow(ctx,
		`select account_id from invocations where id = $1`, id).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invocation{}, ErrNotFound
		}
		return Invocation{}, fmt.Errorf("state: invocations claim cap lookup: %w", err)
	}
	if _, _, err := s.upsertAccountAsyncQuotaTx(ctx, tx, accountID, maxInflight); err != nil {
		return Invocation{}, err
	}

	// CAS the counter: cap check + increment in one UPDATE. 0 rows
	// returned means cap hit. The CHECK (current_inflight >= 0)
	// keeps the counter non-negative under concurrent decrement.
	var newInflight int
	err = tx.QueryRow(ctx, `
		update account_async_quota
		   set current_inflight = current_inflight + 1,
		       updated_at = now()
		 where account_id = $1
		   and current_inflight < max_inflight
		returning current_inflight`, accountID).Scan(&newInflight)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either the cap is 0 or the cap is full. Either way,
		// the claim is rejected. The deferred rollback cleans up.
		return Invocation{}, ErrQuotaExceeded
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("state: account_async_quota claim: %w", err)
	}

	// Atomic state transition + lease stamp + attempts bump.
	row := tx.QueryRow(ctx, `
		update invocations
		   set state = 'dispatching',
		       lease_expires_at = now() + $3::interval,
		       instance_id = coalesce(nullif($2, ''), instance_id),
		       received_at = now(),
		       attempts = attempts + 1
		 where id = $1 and state = 'pending'
		 returning `+invocationSelectCols, id, instanceID, leaseText)
	inv, err := scanInvocation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invocation{}, ErrNotFound
		}
		return Invocation{}, fmt.Errorf("state: invocations claim cap update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, fmt.Errorf("state: invocations claim cap commit: %w", err)
	}
	return inv, nil
}

// upsertAccountAsyncQuotaTx is the tx-bound variant of
// EnsureAccountAsyncQuota. Used by ClaimInvocationWithCap so the cap
// row and the counter increment live in the same transaction.
//
// PR-B fixup (code-review #1185 finding #2): on conflict this is
// DO NOTHING — max_inflight is preserved on the existing row. The
// lazy-insert is a convenience for missing rows only; if the row
// exists, the existing cap is authoritative and the caller's
// maxInflight (which could be 0 from a transient lookup failure in
// the drain) must NOT poison it. The CAS step below uses the
// RETURNING values, so the existing max_inflight flows through.
func (s *PgStore) upsertAccountAsyncQuotaTx(ctx context.Context, tx pgx.Tx, accountID string, maxInflight int) (int, int, error) {
	row := tx.QueryRow(ctx, `
		insert into account_async_quota (account_id, max_inflight)
		values ($1, $2)
		on conflict (account_id) do update
		  set updated_at = now()
		returning max_inflight, current_inflight`,
		accountID, maxInflight)
	var gotMax, gotCur int
	if err := row.Scan(&gotMax, &gotCur); err != nil {
		return 0, 0, fmt.Errorf("state: account_async_quota upsert tx: %w", err)
	}
	return gotMax, gotCur, nil
}

// DecrementAccountAsyncInflight drops current_inflight by 1. Idempotent:
// running below zero is clamped at zero via greatest(). Used by
// CompleteInvocation / FailInvocation / CancelInvocation on their
// terminal-state branches and by RequeueExpiredInvocations on the
// dispatching→pending transition. Returns nil on missing-cap-row — a
// tolerated condition (the increment never happened on a row that
// bypassed ClaimInvocationWithCap).
func (s *PgStore) DecrementAccountAsyncInflight(ctx context.Context, accountID string) error {
	tag, err := s.pool.Exec(ctx, `
		update account_async_quota
		   set current_inflight = greatest(current_inflight - 1, 0),
		       updated_at = now()
		 where account_id = $1`, accountID)
	if err != nil {
		return fmt.Errorf("state: account_async_quota decrement: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Cap row missing — the increment never happened (e.g.
		// drain bypassed ClaimInvocationWithCap). Tolerate; not
		// an error.
		return nil
	}
	return nil
}

// ListExpiredInvocationsForReaper returns up to `limit` invocation
// IDs whose retention horizon (result_retention_until) is in the past.
// Used by pkg/sched/retention_invocations.go.
//
// PR-B fixup (code-review #1185 finding #3): the WHERE clause restricts
// to terminal states only (migration 00550_invocations_async_fields.sql
// documents this contract). Without the filter, the reaper would
// DELET Epending/dispatching rows whose customer-supplied
// result_retention_until happens to be in the past.
func (s *PgStore) ListExpiredInvocationsForReaper(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		select id
		  from invocations
		 where result_retention_until is not null
		   AND result_retention_until <= $1
		   AND state in ('completed', 'failed', 'dead_letter', 'cancelled')
		 order by result_retention_until
		 limit $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("state: invocations reaper list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("state: invocations reaper scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteInvocationsByIDs removes the given rows. Caller passes the IDs
// from ListExpiredInvocationsForReaper. Returns the number deleted.
func (s *PgStore) DeleteInvocationsByIDs(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `delete from invocations where id = any($1::uuid[])`, ids)
	if err != nil {
		return 0, fmt.Errorf("state: invocations reaper delete: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RecentInvocations (SAFE-RELEASES-OBS PR-E, issue #976 / ADR-122)
// returns invocation rows for appID whose created_at >= since,
// ordered (created_at DESC, id DESC), capped at limit.
func (s *PgStore) RecentInvocations(ctx context.Context, appID string, since time.Time, limit int) ([]Invocation, error) {
	if appID == "" {
		return nil, fmt.Errorf("state: RecentInvocations: empty app_id")
	}
	const cap = 10_000
	if limit <= 0 || limit > cap {
		limit = cap
	}
	rows, err := s.pool.Query(ctx,
		`select `+invocationSelectCols+`
		   from invocations
		  where app_id = $1::uuid
		    and created_at >= $2
		  order by created_at desc, id desc
		  limit $3`,
		appID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("state: RecentInvocations: %w", err)
	}
	defer rows.Close()
	return scanInvocations(rows)
}

// RecentErrorRate (SAFE-RELEASES-OBS PR-E) returns the fraction of
// failed invocations (state IN ('failed','dead_letter')) in the
// [since, now] window for appID. Returns 0.0 when there are no rows.
func (s *PgStore) RecentErrorRate(ctx context.Context, appID string, since time.Time) (float64, error) {
	if appID == "" {
		return 0, fmt.Errorf("state: RecentErrorRate: empty app_id")
	}
	var total, failed int
	err := s.pool.QueryRow(ctx,
		`select count(*),
		        count(*) filter (where state in ('failed','dead_letter'))
		   from invocations
		  where app_id = $1::uuid
		    and created_at >= $2`,
		appID, since).Scan(&total, &failed)
	if err != nil {
		return 0, fmt.Errorf("state: RecentErrorRate: %w", err)
	}
	if total == 0 {
		return 0.0, nil
	}
	return float64(failed) / float64(total), nil
}

// RecentP95LatencyMs (SAFE-RELEASES-OBS PR-E) returns the
// 95th-percentile completion latency in milliseconds over the
// [since, now] window for appID. Returns 0.0 when there is no data.
func (s *PgStore) RecentP95LatencyMs(ctx context.Context, appID string, since time.Time) (float64, error) {
	if appID == "" {
		return 0, fmt.Errorf("state: RecentP95LatencyMs: empty app_id")
	}
	var p95 *float64
	err := s.pool.QueryRow(ctx,
		`select coalesce(
		           percentile_cont(0.95) within group (order by extract(epoch from (completed_at - created_at)) * 1000.0),
		           0.0)
		   from invocations
		  where app_id = $1::uuid
		    and state = 'completed'
		    and completed_at is not null
		    and created_at >= $2`,
		appID, since).Scan(&p95)
	if err != nil {
		return 0, fmt.Errorf("state: RecentP95LatencyMs: %w", err)
	}
	if p95 == nil {
		return 0.0, nil
	}
	return *p95, nil
}

// ListDeadlineBreachedInvocations returns up to `limit` invocation IDs
// still in (pending|dispatching) whose deadline_at is in the past.
// Used by pkg/sched/retention_invocations.go's deadline-breach branch.
func (s *PgStore) ListDeadlineBreachedInvocations(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		select id
		  from invocations
		 where state in ('pending', 'dispatching')
		   AND deadline_at is not null
		   AND deadline_at <= $1
		 order by deadline_at
		 limit $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("state: invocations deadline list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("state: invocations deadline scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ForceDeadlineBreachedInvocations transitions the listed invocations
// to dead_letter with outcome='deadline'. Decrements the per-account
// counter for each one so the cap reflects the abandoned work.
func (s *PgStore) ForceDeadlineBreachedInvocations(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: invocations deadline begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Capture account_ids before the UPDATE so we can decrement
	// the counter for each one. The cap row should already exist
	// (the increment path created it), but tolerant decrement.
	rows, err := tx.Query(ctx,
		`select account_id from invocations where id = any($1::uuid[])`, ids)
	if err != nil {
		return 0, fmt.Errorf("state: invocations deadline lookup: %w", err)
	}
	var accounts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return 0, fmt.Errorf("state: invocations deadline scan account: %w", err)
		}
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tag, err := tx.Exec(ctx, `
		update invocations
		   set state = 'dead_letter',
		       outcome = 'deadline',
		       last_error = 'deadline_at breached',
		       completed_at = now(),
		       received_at = coalesce(received_at, now())
		 where id = any($1::uuid[])
		   and state in ('pending', 'dispatching')`, ids)
	if err != nil {
		return 0, fmt.Errorf("state: invocations deadline force: %w", err)
	}

	for _, a := range accounts {
		if _, err := tx.Exec(ctx, `
			update account_async_quota
			   set current_inflight = greatest(current_inflight - 1, 0),
			       updated_at = now()
			 where account_id = $1`, a); err != nil {
			return 0, fmt.Errorf("state: invocations deadline decrement: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: invocations deadline commit: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ----------------------------------------------------------------------------
// ADR-134 PR-E: trigger_records retention reaper.
//
// The reaper (pkg/sched/retention_triggers.go) DELETEs terminal
// trigger_records whose result_retention_until is in the past.
// Pattern mirrors ListExpiredInvocationsForReaper — list IDs in
// a SELECT (no SKIP LOCKED; trigger_records are not claimed by
// any worker today), DELETE in a single batch.
// ----------------------------------------------------------------------------

// ListExpiredTriggerRecordsForReaper returns up to `limit`
// trigger_records IDs whose result_retention_until is in the
// past. Used by pkg/sched/retention_triggers.go.
//
// PR-E fixup (code-review #1185 finding #3): the WHERE clause
// restricts to terminal states only ('succeeded', 'dead_letter').
// Without the filter, the reaper would DELETE a `claimed`
// trigger_records row mid-batch dispatch and the in-flight
// outcome write would fail with ErrNotFound.
func (s *PgStore) ListExpiredTriggerRecordsForReaper(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		select id
		  from trigger_records
		 where result_retention_until is not null
		   AND result_retention_until <= $1
		   AND state in ('succeeded', 'dead_letter')
		 order by result_retention_until
		 limit $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("state: trigger_records reaper list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("state: trigger_records reaper scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteTriggerRecordsByIDs removes the given rows. Returns the
// count deleted.
func (s *PgStore) DeleteTriggerRecordsByIDs(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `delete from trigger_records where id = any($1::uuid[])`, ids)
	if err != nil {
		return 0, fmt.Errorf("state: trigger_records reaper delete: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
