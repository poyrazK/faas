// Package authz (errors.go) — sentinels for the orgz half of pkg/authz.
// The sentinel set is intentionally small: only the membership lookup
// gate (IsOrgMember, ADR-120) and the active-org resolver
// (LoadOrgWithResolver, ADR-061 PR 4) return package-level errors that
// callers want to distinguish via errors.Is. Handler-level error
// mapping (5xx / 4xx / audit reasons) happens in pkg/gateway, NOT
// here — pkg/authz is the policy oracle, not the HTTP translator.
//
// New code MUST introduce a sentinel here instead of returning a
// fmt.Errorf ad-hoc: future PRs (e.g. "deny delete-org if last
// active member") will share the IsMembership chain. Without a
// sentinel, those PRs would need to string-match the error body,
// which is exactly the contract bug ADR-119 round-2 surfaced
// ("fenced substring-match reason drift"). See
// cmd/schedd/internal_svc_minter.go:6 for the ADR-119 precedent.
//
// Why a separate file: pkg/authz had no errors.go before ADR-120
// because every earlier API either returned a typed *Problem (apid
// layer) or returned (bool, *Problem) for handler-layer errors.
// IsOrgMember is the first package-level function that returns
// `(bool, error)` for callers outside the HTTP layer — the synth
// gate (pkg/gateway/synth_members_only.go) and the public-auth gate
// (pkg/gateway/handler.go::applyIngressMembersOnly) both need an
// errors.Is-recoverable sentinel so they can pivot on DB-error
// (fail-closed = 401 + audit 'lookup_error') vs API-shape error
// (would be a wiring bug, surfaces as 500).
package authz

import "errors"

// ErrMembershipLookup is the sentinel returned by IsOrgMember when the
// Postgres membership SELECT itself fails (connection / timeout /
// constraint violation). The fail-closed posture (ADR-119 round-2
// at cmd/schedd/internal_svc_minter.go:6) treats every lookup error
// as "not a member" at the gate layer — the gate returns 401 (or 403
// on the public-ingress surface, mirroring the
// non-member-vs-no-cookie distinction) AND emits an audit row with
// `reason="lookup_error"` so the operator can spot the DB outage
// through the audit pipeline rather than through the customer-facing
// 401 alone.
//
// CodeQL's `go/error-strings` and Go vet's printf checker both stay
// clean: the message is a static sentinel — no format verbs, no
// dynamic data leaking into the error body (which would break the
// audit-redaction invariant the 401 / 403 responses honour).
//
// Tested in pkg/authz/is_org_member_test.go::TestIsOrgMember_DBError.
// Wrapped errors are detectable via errors.Is(err, ErrMembershipLookup)
// at every layer that needs to pivot.
var ErrMembershipLookup = errors.New("authz: org membership lookup failed")

// IsMembership reports whether err (or anything in its wrap chain)
// is the package-level membership-lookup sentinel. Callers that want
// a fail-closed gate (the public-auth gate at
// pkg/gateway/handler.go::applyIngressMembersOnly) use this in
// combination with `Is(err, ErrMembershipLookup)` → treat as 401
// rather than 500; the sentinel is the only path that surfaces a
// controlled, audit-emitted 401 across both the HTTP and the synth
// (cron-fired wakes) ingress surfaces.
func IsMembership(err error) bool {
	return errors.Is(err, ErrMembershipLookup)
}
