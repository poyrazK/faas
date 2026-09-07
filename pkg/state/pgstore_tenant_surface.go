// pgstore_tenant_surface.go — PgStore implementations for the tenant
// surfaces Store interface (ADR-100 / issue #879). The two
// quota-enforcing methods mirror CreateEdgeRuleIfUnderQuota (pgstore.go:5951):
// BeginTx → FOR UPDATE on the parent row → count → insert → commit.
// The remaining methods are plain pool queries using `mapErr` for
// the established ErrNotFound / ErrConflict / ErrInvalidArgument
// shapes. Hand-written SQL matches the pgstore convention
// (ADR-017 / sqlc TODO is unchanged).
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// tenantSurfaceCols is the canonical SELECT column list for a
// tenant_surfaces row. Matches the order of scanTenantSurface.
// Bare columns are fine for INSERT...RETURNING and single-table
// queries (where the FROM alias is also `s`); the qualified
// variant tenantSurfaceColsQ prefixes every column with `s.` so
// JOINs against tenant_hostnames (which also has id + created_at)
// don't trip SQLSTATE 42702 (ambiguous column reference).
const tenantSurfaceCols = `id, account_id, app_id, name, cert_kind, status,
	cert_state, coalesce(cert_not_after, 'epoch'::timestamptz),
	coalesce(cert_last_error, ''), created_at, updated_at`

// tenantSurfaceColsQ — JOIN-safe form of tenantSurfaceCols.
const tenantSurfaceColsQ = `s.id, s.account_id, s.app_id, s.name, s.cert_kind, s.status,
	s.cert_state, coalesce(s.cert_not_after, 'epoch'::timestamptz),
	coalesce(s.cert_last_error, ''), s.created_at, s.updated_at`

// tenantHostnameCols — single column order used everywhere a hostname
// row scans. The zero-value sentinel for verified_at / last_check_at
// is 'epoch' (mirrors custom_domains.verified_at treatment at
// pgstore.go:5236); the Go-side .Verified() / .LastCheckAt.IsZero()
// accessor compensates.
const tenantHostnameCols = `id, surface_id, hostname, challenge_token,
	coalesce(verified_at, 'epoch'::timestamptz),
	coalesce(last_check_at, 'epoch'::timestamptz),
	coalesce(last_error, '')`

// scanTenantSurface is the single row helper for tenant_surfaces.
// Defense-in-depth: the closed CHECK constraint on cert_kind / status
// is the SQL-side gate, but a row that bypassed the CHECK (manual fix,
// replication drift) would otherwise silently produce a typed value
// (`CertKind("unknown")`) the issuer would fail-closed at — masking
// the drift. We fail-loud here so the validate-replica path is
// observable.
func scanTenantSurface(row pgx.Row) (TenantSurface, error) {
	var s TenantSurface
	var certKindRaw, statusRaw, certStateRaw string
	if err := row.Scan(
		&s.ID, &s.AccountID, &s.AppID, &s.Name,
		&certKindRaw, &statusRaw, &certStateRaw,
		&s.CertNotAfter, &s.CertLastError,
		&s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return TenantSurface{}, mapErr(err)
	}
	s.CertKind = CertKind(certKindRaw)
	s.Status = SurfaceStatus(statusRaw)
	s.CertState = CertState(certStateRaw)
	if !s.CertKind.Valid() {
		return TenantSurface{}, fmt.Errorf("state: tenant_surfaces row %s has invalid cert_kind %q", s.ID, s.CertKind)
	}
	if !s.Status.Valid() {
		return TenantSurface{}, fmt.Errorf("state: tenant_surfaces row %s has invalid status %q", s.ID, s.Status)
	}
	if !s.CertState.Valid() {
		return TenantSurface{}, fmt.Errorf("state: tenant_surfaces row %s has invalid cert_state %q", s.ID, s.CertState)
	}
	return s, nil
}

// scanTenantSurfaces is the multi-row helper.
func scanTenantSurfaces(rows pgx.Rows) ([]TenantSurface, error) {
	var out []TenantSurface
	for rows.Next() {
		s, err := scanTenantSurface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanTenantHostname — single row.
func scanTenantHostname(row pgx.Row) (TenantHostname, error) {
	var h TenantHostname
	if err := row.Scan(
		&h.ID, &h.SurfaceID, &h.Hostname, &h.ChallengeToken,
		&h.VerifiedAt, &h.LastCheckAt, &h.LastError,
	); err != nil {
		return TenantHostname{}, mapErr(err)
	}
	return h, nil
}

// scanTenantHostnames — multi-row.
func scanTenantHostnames(rows pgx.Rows) ([]TenantHostname, error) {
	var out []TenantHostname
	for rows.Next() {
		h, err := scanTenantHostname(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateTenantSurfaceIfUnderQuota inserts a surface iff the account
// is under limits.TenantSurfacesPerAccount and the plan allows
// surfaces at all (limits.TenantSurfacesAllowed). Returns:
//   - (TenantSurface{}, *TenantSurfaceQuotaError) when limit trips
//   - (TenantSurface{}, ErrTenantSurfacesNotAllowed) when the plan
//     gate is off (Free today)
//   - (TenantSurface{}, ErrNotFound) when the parent account row is
//     missing or soft-deleted
//
// TOCTOU defence: BeginTx + FOR UPDATE on the accounts row
// serialises concurrent inserts before the count.
func (s *PgStore) CreateTenantSurfaceIfUnderQuota(ctx context.Context, in CreateTenantSurfaceParams, limits api.Limits) (TenantSurface, error) {
	if !limits.TenantSurfacesAllowed {
		return TenantSurface{}, ErrTenantSurfacesNotAllowed
	}
	if in.CertKind == "" {
		in.CertKind = CertKindPerHostSAN
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantSurface{}, fmt.Errorf("state: begin create tenant surface tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	// Lock the parent account row so concurrent inserts serialise.
	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from accounts where id = $1 and status <> 'deleted' for update`,
		in.AccountID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TenantSurface{}, ErrNotFound
		}
		return TenantSurface{}, fmt.Errorf("state: lock account %s: %w", in.AccountID, err)
	}

	var observed int
	if err := tx.QueryRow(ctx,
		`select count(*) from tenant_surfaces
		 where account_id = $1 and status <> 'deleted'`,
		in.AccountID,
	).Scan(&observed); err != nil {
		return TenantSurface{}, fmt.Errorf("state: count tenant_surfaces for account %s: %w", in.AccountID, err)
	}
	if observed >= limits.TenantSurfacesPerAccount {
		return TenantSurface{}, &TenantSurfaceQuotaError{
			Limit:    limits.TenantSurfacesPerAccount,
			Observed: observed,
		}
	}

	row := tx.QueryRow(ctx,
		`insert into tenant_surfaces (account_id, app_id, name, cert_kind)
		 values ($1, $2, $3, $4)
		 returning `+tenantSurfaceCols,
		in.AccountID, in.AppID, in.Name, string(in.CertKind),
	)
	surf, err := scanTenantSurface(row)
	if err != nil {
		// pgerrcode.UniqueViolation on tenant_surfaces_account_name_uniq
		// surfaces as ErrConflict via mapErr; CHECK violations on
		// tenant_surfaces_app_or_not_chk / cert_kind_chk flow through
		// unchanged (the apid validates the inputs before calling,
		// and tripwire tests want the raw SQLSTATE for visibility).
		return TenantSurface{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantSurface{}, fmt.Errorf("state: commit create tenant surface: %w", err)
	}
	return surf, nil
}

// GetTenantSurfaceByID — primary-key lookup.
func (s *PgStore) GetTenantSurfaceByID(ctx context.Context, id string) (TenantSurface, error) {
	row := s.pool.QueryRow(ctx,
		`select `+tenantSurfaceColsQ+` from tenant_surfaces s where s.id = $1`, id)
	return scanTenantSurface(row)
}

// GetTenantSurfaceByName — account-scoped name lookup. Includes
// soft-deleted rows so audit / cert_history flows can find them.
func (s *PgStore) GetTenantSurfaceByName(ctx context.Context, accountID, name string) (TenantSurface, error) {
	row := s.pool.QueryRow(ctx,
		`select `+tenantSurfaceColsQ+` from tenant_surfaces s
		 where s.account_id = $1 and s.name = $2`,
		accountID, name)
	return scanTenantSurface(row)
}

// ListTenantSurfacesForAccount — operator + apid listings; excludes
// soft-deleted surfaces (they live in audit trails).
func (s *PgStore) ListTenantSurfacesForAccount(ctx context.Context, accountID string) ([]TenantSurface, error) {
	rows, err := s.pool.Query(ctx,
		`select `+tenantSurfaceColsQ+` from tenant_surfaces s
		 where s.account_id = $1 and s.status <> 'deleted'
		 order by s.created_at, s.id`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantSurfaces(rows)
}

// ListTenantSurfacesForApp — pgRouter.ResolveHost hot path
// reverse-lookup from an app id.
func (s *PgStore) ListTenantSurfacesForApp(ctx context.Context, appID string) ([]TenantSurface, error) {
	rows, err := s.pool.Query(ctx,
		`select `+tenantSurfaceColsQ+` from tenant_surfaces s
		 where s.app_id = $1 and s.status <> 'deleted'
		 order by s.created_at, s.id`,
		appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantSurfaces(rows)
}

// CountTenantSurfacesForAccount — quota + dashboard hot path.
func (s *PgStore) CountTenantSurfacesForAccount(ctx context.Context, accountID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from tenant_surfaces
		 where account_id = $1 and status <> 'deleted'`,
		accountID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count tenant_surfaces for account %s: %w", accountID, err)
	}
	return n, nil
}

// UpdateTenantSurfaceStatus — apid sets status (pending/active/suspended/deleted).
// Returns ErrNotFound if the row is gone.
func (s *PgStore) UpdateTenantSurfaceStatus(ctx context.Context, id string, status SurfaceStatus) error {
	tag, err := s.pool.Exec(ctx,
		`update tenant_surfaces set status = $1, updated_at = now() where id = $2`,
		string(status), id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTenantSurfaceCert — the cert engine's only writer to cert_*
// columns. The trigger on tenant_surfaces emits tenant_surface_changed
// regardless of which column we touch; the cert-remint goroutine
// coalesces.
//
// PR-D code review (PR #959 candidate 5): the wrapper previously
// called UpdateTenantSurfaceCert on every transition, which
// bumped updated_at and fired the tenant_surface_changed
// trigger — creating a self-recursive notify storm: each
// pg_notify → wrapper re-entry → another flip → another notify
// (3 notifies per surface, M replicas amplifying). The fix is
// to write only when the row's cert_state does NOT match the
// new value via the WHERE clause — when the predicate doesn't
// match, the UPDATE is a no-op and the trigger doesn't fire.
//
// The renewer tick path (TouchTenantSurfaceForRenewal) is
// unaffected: that method updates updated_at directly via
// the dedicated UPDATE and the trigger fires (one notify per
// due surface, the load-bearing renewer kick).
func (s *PgStore) UpdateTenantSurfaceCert(ctx context.Context, in UpdateSurfaceCertParams) error {
	var notAfter any
	if !in.NotAfter.IsZero() {
		notAfter = in.NotAfter
	}
	tag, err := s.pool.Exec(ctx,
		`update tenant_surfaces
		    set cert_state = $1,
		        cert_not_after = $2,
		        cert_last_error = $3,
		        updated_at = now()
		  where id = $4
		    and cert_state is distinct from $1`,
		string(in.CertState), notAfter, in.LastError, in.SurfaceID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		// Two reasons for 0 rows: (a) the surface id doesn't
		// exist (the wrapper already checked via GetTenantSurfaceByID
		// so this is rare), or (b) the row's cert_state already
		// matches the new value (the load-bearing no-op path
		// that suppresses the re-entry notify storm). We
		// disambiguate via a follow-up existence check so the
		// wrapper can still distinguish missing-row from
		// idempotent flip.
		var exists bool
		err := s.pool.QueryRow(ctx,
			`select exists (select 1 from tenant_surfaces where id = $1)`, in.SurfaceID).Scan(&exists)
		if err != nil {
			return mapErr(err)
		}
		if !exists {
			return ErrNotFound
		}
		return nil
	}
	return nil
}

// DeleteTenantSurface — soft delete: flips status to 'deleted' so the
// audit / cert_history paths keep referencing the row.
func (s *PgStore) DeleteTenantSurface(ctx context.Context, id string) error {
	return s.UpdateTenantSurfaceStatus(ctx, id, SurfaceStatusDeleted)
}

// ListTenantSurfacesNearingExpiry — renewer hot-path (PR-D
// commit 3). Returns active surfaces whose cert_not_after
// < cutoff AND cert_state='issued'. Reads the partial index
// tenant_surfaces_cert_expiry_idx so the query stays bounded
// regardless of fleet size. The renewer goroutine polls every
// CertRenewTickSeconds (api.CertRenewTickSeconds).
//
// cutoff is in UTC; the cert_not_after column is timestamptz so
// the comparison is timezone-correct.
//
// PR-D code review (PR #959 candidate 6): the v1 shape issued
// an unbounded UPDATE per due row when a CA outage landed
// N>1000 surfaces in the renewal window. The renewer now
// bounds each tick to a hard limit (api.CertRenewTickBatchLimit,
// 1k) and uses (cert_not_after, id) as a stable composite
// cursor for next-tick continuation. The keyset cursor avoids
// the OFFSET-counter trap that would let new rows being
// touched mid-batch silently skip pages.
func (s *PgStore) ListTenantSurfacesNearingExpiry(ctx context.Context, cutoff time.Time, limit int, afterCertNotAfter time.Time, afterID string) ([]TenantSurface, error) {
	if limit <= 0 {
		limit = 1
	}
	var rows pgx.Rows
	var err error
	if afterID == "" {
		// First page: no cursor.
		rows, err = s.pool.Query(ctx,
			`select `+tenantSurfaceColsQ+` from tenant_surfaces s
			  where s.status = 'active'
			    and s.cert_state = 'issued'
			    and s.cert_not_after < $1
			  order by s.cert_not_after asc, s.id asc
			  limit $2`,
			cutoff, limit)
	} else {
		// Keyset pagination: (cert_not_after, id) > (cursor, cursorid)
		// in the order-by sense. The row constructor compares
		// lexicographically against the cursor.
		rows, err = s.pool.Query(ctx,
			`select `+tenantSurfaceColsQ+` from tenant_surfaces s
			  where s.status = 'active'
			    and s.cert_state = 'issued'
			    and s.cert_not_after < $1
			    and (s.cert_not_after, s.id) > ($2, $3)
			  order by s.cert_not_after asc, s.id asc
			  limit $4`,
			cutoff, afterCertNotAfter, afterID, limit)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return scanTenantSurfaces(rows)
}

// TouchTenantSurfaceForRenewal bumps updated_at on the surface
// row so the tenant_surface_changed notify trigger fires. The
// pg_notify subscriber routes the bare surface UUID back through
// CertIssuer.RequestCertForSurface which re-runs the full
// state machine. The renewer doesn't need its own write path
// to the cert columns — it rides the existing pipeline so the
// in-flight state machine (issued → pending → issued) stays
// the source of truth.
func (s *PgStore) TouchTenantSurfaceForRenewal(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`update tenant_surfaces set updated_at = now() where id = $1`,
		id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TenantSurfaceByHostname — pgRouter.ResolveHost hot path; joins
// tenant_hostnames to tenant_surfaces via the UQ on hostname alone.
// Returns ErrNotFound when no surface claims the hostname (the
// caller then consults custom_domains via DomainByName).
func (s *PgStore) TenantSurfaceByHostname(ctx context.Context, hostname string) (TenantSurface, error) {
	row := s.pool.QueryRow(ctx,
		`select `+tenantSurfaceColsQ+` from tenant_surfaces s
		    join tenant_hostnames h on h.surface_id = s.id
		  where h.hostname = $1
		    and s.status <> 'deleted'`,
		hostname)
	return scanTenantSurface(row)
}

// CreateTenantHostnameIfUnderQuota — locks the parent tenant_surfaces
// row before counting hostnames. Distinct from CreateTenantSurfaceIfUnderQuota:
//   - the parent lock is on tenant_surfaces (not accounts)
//   - the per-hostname UQ on tenant_hostnames.hostname surfaces as
//     ErrConflict (CodeTenantHostnameAlreadyClaimed in PR-C)
//
// Returns:
//   - (TenantHostname{}, *TenantHostnameQuotaError) when per-surface cap trips
//   - (TenantHostname{}, ErrNotFound) when the parent surface is missing
//   - (TenantHostname{}, ErrConflict) on UQ violation
func (s *PgStore) CreateTenantHostnameIfUnderQuota(ctx context.Context, in CreateTenantHostnameParams, limits api.Limits) (TenantHostname, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantHostname{}, fmt.Errorf("state: begin create tenant hostname tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit

	var locked int
	if err := tx.QueryRow(ctx,
		`select 1 from tenant_surfaces where id = $1 and status <> 'deleted' for update`,
		in.SurfaceID,
	).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TenantHostname{}, ErrNotFound
		}
		return TenantHostname{}, fmt.Errorf("state: lock tenant_surface %s: %w", in.SurfaceID, err)
	}

	var observed int
	// Per-surface cap counts VERIFIED hostnames only. The
	// limits.go:449-450 doc explicitly says "verified hostnames one
	// surface may hold"; counting unverified would lock a customer
	// out of retrying verification once they hit the cap with
	// pending TXT records (the dns_poller can't flip them to
	// verified so the customer is stuck — the capacity-aware
	// behaviour is the inverse: more unverified = more headroom,
	// not less).
	if err := tx.QueryRow(ctx,
		`select count(*) from tenant_hostnames where surface_id = $1 and verified_at is not null`,
		in.SurfaceID,
	).Scan(&observed); err != nil {
		return TenantHostname{}, fmt.Errorf("state: count verified tenant_hostnames for surface %s: %w", in.SurfaceID, err)
	}
	if observed >= limits.TenantHostnamesPerSurface {
		return TenantHostname{}, &TenantHostnameQuotaError{
			Limit:     limits.TenantHostnamesPerSurface,
			Observed:  observed,
			SurfaceID: in.SurfaceID,
		}
	}

	row := tx.QueryRow(ctx,
		`insert into tenant_hostnames (surface_id, hostname, challenge_token)
		 values ($1, $2, $3)
		 returning `+tenantHostnameCols,
		in.SurfaceID, in.Hostname, in.ChallengeToken,
	)
	host, err := scanTenantHostname(row)
	if err != nil {
		return TenantHostname{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantHostname{}, fmt.Errorf("state: commit create tenant hostname: %w", err)
	}
	return host, nil
}

// ListTenantHostnamesForSurface — full enumeration (including
// unverified) for the apid response shape.
func (s *PgStore) ListTenantHostnamesForSurface(ctx context.Context, surfaceID string) ([]TenantHostname, error) {
	rows, err := s.pool.Query(ctx,
		`select `+tenantHostnameCols+` from tenant_hostnames
		 where surface_id = $1
		 order by hostname`,
		surfaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantHostnames(rows)
}

// ListTenantSurfaceHostnames returns every hostname reserved by a non-deleted
// tenant surface. Tenant hostnames are globally unique, so F4 wildcard
// reservation checks this account-independent set.
func (s *PgStore) ListTenantSurfaceHostnames(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		select h.hostname
		  from tenant_hostnames h
		  join tenant_surfaces s on s.id = h.surface_id
		 where s.status <> 'deleted'
		 order by h.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var hostname string
		if err := rows.Scan(&hostname); err != nil {
			return nil, err
		}
		out = append(out, hostname)
	}
	return out, rows.Err()
}

// ListVerifiedTenantHostnamesForSurface — SAN assembly hot path.
// Sort-by-hostname is deterministic so re-mints produce identical
// (primary, sans) tuples.
func (s *PgStore) ListVerifiedTenantHostnamesForSurface(ctx context.Context, surfaceID string) ([]TenantHostname, error) {
	rows, err := s.pool.Query(ctx,
		`select `+tenantHostnameCols+` from tenant_hostnames
		 where surface_id = $1 and verified_at is not null
		 order by hostname`,
		surfaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantHostnames(rows)
}

// CountTenantHostnamesForSurface — quota hot path; cousin of ListVerified*.
func (s *PgStore) CountTenantHostnamesForSurface(ctx context.Context, surfaceID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`select count(*) from tenant_hostnames where surface_id = $1`,
		surfaceID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("state: count tenant_hostnames for surface %s: %w", surfaceID, err)
	}
	return n, nil
}

// MarkTenantHostnameVerified — sets verified_at = now() and clears
// last_error. Idempotent: a second call with last_error stays
// unchanged.
func (s *PgStore) MarkTenantHostnameVerified(ctx context.Context, hostname string) error {
	tag, err := s.pool.Exec(ctx,
		`update tenant_hostnames
		    set verified_at = now(),
		        last_check_at = now(),
		        last_error = ''
		  where hostname = $1`,
		hostname)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkTenantHostnameCheckFailed — the dns_poller path. Preserves
// verified_at (a previously-verified hostname stays verified across
// a transient DNS resolution failure); only updates last_check_at +
// last_error.
func (s *PgStore) MarkTenantHostnameCheckFailed(ctx context.Context, hostname, reason string) error {
	tag, err := s.pool.Exec(ctx,
		`update tenant_hostnames
		    set last_check_at = now(),
		        last_error = $1
		  where hostname = $2`,
		reason, hostname)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPendingTenantHostnames — dns_poller queue fetch. Returns the
// `limit` oldest unverified rows whose last_check_at is older than
// `olderThan` (the poller sleeps batch+1 interval seconds between
// passes, so a row re-enters the queue roughly every batch-time).
// Rows with last_check_at IS NULL are always eligible (fresh inserts).
func (s *PgStore) ListPendingTenantHostnames(ctx context.Context, olderThan time.Time, limit int) ([]TenantHostname, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`select `+tenantHostnameCols+` from tenant_hostnames
		 where verified_at is null
		   and (last_check_at is null or last_check_at < $1)
		 order by coalesce(last_check_at, 'epoch'::timestamptz), created_at
		 limit $2`,
		olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTenantHostnames(rows)
}

// DeleteTenantHostname — apid path; cascades nothing (hostnames have
// no children).
func (s *PgStore) DeleteTenantHostname(ctx context.Context, hostname string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from tenant_hostnames where hostname = $1`, hostname)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetTenantHostnameByName — pgRouter.ResolveHost's tenant-surface
// branch uses this to fail closed on hostname.Verified() == false.
// Looked up by hostname alone (the citext column normalises the
// caller-side lowercase); parent surface is reachable via
// TenantSurfaceByHostname if the caller wants the row joined. The
// lookup filters out deleted parent surfaces (status='deleted') so
// a deleted surface never leaks a routable hostname — the soft
// delete flips status, not the hostname row.
func (s *PgStore) GetTenantHostnameByName(ctx context.Context, hostname string) (TenantHostname, error) {
	row := s.pool.QueryRow(ctx,
		`select `+tenantHostnameCols+` from tenant_hostnames h
		    join tenant_surfaces s on s.id = h.surface_id
		  where h.hostname = $1
		    and s.status <> 'deleted'`,
		hostname)
	return scanTenantHostname(row)
}

// Compile-time guard. Add the tenant_surfaces CHECK constraints to
// mapErr's named-mapping so apid-validate-time errors surface as
// ErrInvalidArgument instead of bubbling the raw SQLSTATE. The
// tripwire tests at pgstore_test.go (snapshot of the original
// named-mapping) stay green because we ADD entries, never remove.
func init() {
	checkViolationMappedToInvalid["tenant_surfaces_app_or_not_chk"] = struct{}{}
	checkViolationMappedToInvalid["tenant_surfaces_cert_kind_chk"] = struct{}{}
	checkViolationMappedToInvalid["tenant_surfaces_status_chk"] = struct{}{}
	checkViolationMappedToInvalid["tenant_surfaces_cert_state_chk"] = struct{}{}
	checkViolationMappedToInvalid["tenant_hostnames_hostname_len_chk"] = struct{}{}
}
