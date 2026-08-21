// Package authz (is_org_member.go) — IsOrgMember: the single
// org-membership predicate the public-auth 'members_only' gate
// (ADR-120) and the SynthServer cron-mirror gate
// (pkg/gateway/synth_members_only.go) call. Decoupled from the
// per-request active-org resolver LoadOrgWithResolver on purpose —
// the resolver stamps principal.Membership based on the
// X-Active-Org / ?org= header that the customer themselves chose,
// whereas IsOrgMember decides whether the cookie's account is an
// ACTIVE MEMBER of the LOOKED-UP apps.org_id. A request can ask the
// gate about org-A membership while the resolver is still
// considering org-B the active one; the gate must NOT consult those
// headers, otherwise an attacker who learns an org-id can forge
// X-Active-Org=<target-org> to pass the gate for a target org they
// are not a member of. See plan §"X-Active-Org header interference".
//
// Semantics:
//   - Active member (removed_at IS NULL): returns (true, nil).
//   - Removed member (removed_at IS NOT NULL): returns (false, nil).
//   - No row (account never joined this org): returns (false, nil).
//   - DB-level error (timeout / connection / SQL constraint):
//     returns (false, ErrMembershipLookup). Fail-closed posture
//     mirrors ADR-119 round-2 at cmd/schedd/internal_svc_minter.go:6 —
//     the gate treats every lookup failure as "not a member" +
//     surfaces a controlled 401/403 + audit 'reason=lookup_error'.
//     Returning the sentinel (not nil) lets the gate emit the
//     controlled response rather than a Go-panic-on-nil 500.
//   - Empty orgID or accountID: returns (false, nil). The gate
//     layer should never call this with an empty value, but the
//     defensive short-circuit keeps a code-path bug from
//     accidentally returning (true, nil) via an empty-key
//     EXISTS-true prank.
//
// The SELECT uses `SELECT EXISTS(...)`, NOT `SELECT 1 FROM ... LIMIT
// 1` — `SELECT 1` with no LIMIT emits zero or more rows; the
// Scan(&bool) of a 0-row result is `pgx.ErrNoRows`, which we then
// would have to translate to (false, nil). `SELECT EXISTS` always
// returns exactly one row with a bool (the implicit type
// resolution), so Scan(&ok) is total — no ErrNoRows branch needed.
// That's the same shape as the auth/account EXISTS lookups
// pkg/auth/middleware uses for the live session row (see
// middleware.go:LookupSession — wraps the same SELECT EXISTS
// pattern).
//
// Indexing: the membership hot path is the PK `(org_id, account_id)`
// (migration 00099 line 117). The PK gives a single-btree-leaf
// equality lookup on the join key, so this query is O(log n) per
// request and does NOT depend on the `org_memberships_account_idx`
// (account_id) or `org_memberships_one_owner_idx` (org_id WHERE
// role='owner') partial indexes. The PK lookup is also the right
// cardinality probe for the hot path: a Hobby customer with 5 apps
// in org-A and 3 apps in org-B sees ~5 SQL lookups per request that
// the per-app PublicAuthBlock('members_only') gate fires for. The
// PK cardinality is 1, so the lookup is bounded even on the
// Scale-tier-org with the OrgMembersMax cap (per
// docs/iam-6-ownership-inventory.md: 100 active members on Scale).
//
// Test surface:
//   - pkg/authz/is_org_member_test.go::TestIsOrgMember_ActiveMember
//   - pkg/authz/is_org_member_test.go::TestIsOrgMember_RemovedMember
//   - pkg/authz/is_org_member_test.go::TestIsOrgMember_NoRow
//   - pkg/authz/is_org_member_test.go::TestIsOrgMember_DBError
//   - pkg/authz/is_org_member_test.go::TestIsOrgMember_EmptyInputs
package authz

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OrgMemberChecker is the narrow predicate interface the public-auth
// gate (pkg/gateway/handler.go::applyIngressMembersOnly) depends on.
// pkg/gateway does NOT import pgxpool directly — pkg/authz.IsOrgMember
// is the prod implementation, exposed via the WithMembersOnlyChecker
// constructor that wraps it. Unit tests in pkg/gateway pass a fake
// that returns canned (bool, error) pairs without touching a DB —
// same shape as how WithInternalSvcVerifier decouples the gate from
// the cmd-side Ed25519 verifier constructor at
// cmd/gatewayd-internal/internal_svc_verifier.go.
//
// The boolean is "is an active member of the org the app belongs
// to" — NOT "is the active-org header set to this org". Decoupling
// from LoadOrgWithResolver's header-driven resolver is the
// point of this interface (see file header).
type OrgMemberChecker interface {
	// IsOrgMember reports whether accountID is an active member
	// (removed_at IS NULL) of orgID. Returns (false,
	// ErrMembershipLookup) on DB-level failure to keep the
	// gate's fail-closed posture explicit at the type level.
	IsOrgMember(ctx context.Context, orgID, accountID string) (bool, error)
}

// poolOrgMemberChecker is the prod OrgMemberChecker
// implementation that wraps the package-level IsOrgMember helper
// (so tests can exercise IsOrgMember directly without a Handler).
// The constructor PoolOrgMemberChecker returns an interface value
// so pkg/gateway never sees pgxpool.Pool — keeps the gate
// composition root-free (cmd/gatewayd-internal wires the concrete
// pool).
type poolOrgMemberChecker struct {
	pool *pgxpool.Pool
}

// PoolOrgMemberChecker constructs the production OrgMemberChecker
// implementation backed by a pgxpool.Pool. cmd/gatewayd-internal/
// run.go's WithMembersOnlyChecker takes the resulting interface
// value (pkg/gateway never imports pgxpool itself, same shape as
// the JWT verifier / edge-rule resolver wiring).
//
// A nil pool panics on first call rather than wiring silently —
// matches the cmd-side pattern where a misconfigured prod binary
// fails fast at startup instead of serving every request as
// "not a member" forever.
func PoolOrgMemberChecker(pool *pgxpool.Pool) OrgMemberChecker {
	return &poolOrgMemberChecker{pool: pool}
}

// IsOrgMember is the underlying pgxpool-backed implementation of
// the OrgMemberChecker interface. Lives here (not behind an
// interface method value) so pkg/authz/is_org_member_test.go can
// call it with a directly-supplied pgxpool.Pool — the table-driven
// tests pin both the happy-path AND the DB-error path
// (TestIsOrgMember_DBError uses a closed-pool fixture) without
// the OrchMemberChecker indirection.
//
// The query is `SELECT EXISTS(SELECT 1 FROM org_memberships
// WHERE org_id = $1 AND account_id = $2 AND removed_at IS
// NULL)`. Active member → true; every other case (no row,
// removed row, DB error) → false. PK = (org_id, account_id) so
// the equality lookup hits a single leaf and `removed_at IS
// NULL` filters in-place; no seq scan even on cold caches.
func (c *poolOrgMemberChecker) IsOrgMember(ctx context.Context, orgID, accountID string) (bool, error) {
	return IsOrgMember(ctx, c.pool, orgID, accountID)
}

// IsOrgMember is the package-level helper that the OrgMemberChecker
// interface delegates to. Pulled out as a free function so pkg/authz
// tests can call IsOrgMember(ctx, testPool, …) directly without
// constructing an OrgMemberChecker value first — keeps the test
// fixture count small and matches the test style of the surrounding
// pgstore_*.go test files (which take a pool in their setup helper).
//
// `pool` may not be nil; passing nil is a programming error and
// surfaces as a panic via pgx's first call. The poolOrgMemberChecker
// constructor pins the prod-side nil-check; the free function relies
// on the caller for that contract.
func IsOrgMember(ctx context.Context, pool *pgxpool.Pool, orgID, accountID string) (bool, error) {
	// Defensive short-circuit: empty inputs return (false, nil)
	// (NOT ErrMembershipLookup) because the gate failure mode
	// is "user has no usable identity" rather than "DB is
	// unhealthy". The no-cookie path at the gate fires the same
	// 401 the empty-input case here emulates — keeping the
	// ErrMembershipLookup sentinel reserved for genuine DB-level
	// failures (which the operator's audit pipeline must see as
	// distinct from a benign no-cookie).
	if orgID == "" || accountID == "" {
		return false, nil
	}
	var ok bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM org_memberships WHERE org_id = $1 AND account_id = $2 AND removed_at IS NULL)`,
		orgID, accountID,
	).Scan(&ok)
	if err != nil {
		// Every pgx error path (timeout / connection-lost /
		// server-closed / schema-violation) collapses to
		// ErrMembershipLookup so the gate sees ONE controlled
		// 401/403 + ONE controlled audit kind ('lookup_error')
		// rather than a per-error fan-out. The error message
		// retains the pgx detail (operator's log keeps the
		// diagnostic; the audit payload does NOT — it carries
		// `actor_account_id` + `app_id` + `reason` only).
		// errorlint: %w for both verbs would be ambiguous
		// (errors.Is on a double-wrapped same sentinel still
		// walks fine, but the canonical pattern is to wrap
		// once and let the inner err.Error() surface via the
		// chain — see errorlint rule rationale).
		return false, errors.Join(ErrMembershipLookup, err)
	}
	return ok, nil
}
