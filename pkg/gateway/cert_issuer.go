// pkg/gateway/cert_issuer.go — the cert-remint seam for ADR-100
// (issue #879). The interface lives here so cmd/gatewayd-internal
// can wire a *PGBackend (which is what the notify subscriber calls
// per the invalidator contract) without pulling in a cert-mint
// implementation. The production implementation
// (pkg/gateway/cert_issuer_letsencrypt.go) lands in PR-A commit 7.
//
// The interface is intentionally narrow: a single entry point
// that re-mints a cert for a surface. The engine handles all the
// state lookup (surface row + verified hostnames), the SAN
// assembly (sort-by-hostname for determinism), and the store
// writeback (UpdateTenantSurfaceCert) — the issuer itself is a
// thin shell over the underlying CA / on-disk store.
package gateway

import "context"

// CertIssuer is the gatewayd seam for the cert-remint goroutine.
// A nil implementation short-circuits the pg_notify subscriber
// (PGBackend.RequestCertForSurface) so PR-A's commit-6 wiring
// compiles before the PR-A commit-7 issuer lands. The production
// implementation is wired by cmd/gatewayd-internal/run.go.
type CertIssuer interface {
	// RequestCertForSurface re-mints the cert for the given
	// surface and writes the result back via
	// state.Store.UpdateTenantSurfaceCert. Returns an error
	// when:
	//   - the surface is missing / soft-deleted
	//   - the surface has zero verified hostnames (fail-closed
	//     — issuing against an empty SAN set is meaningless)
	//   - the underlying CA call fails (rate limit, network,
	//     DNS, challenge timeout)
	//   - the on-disk write fails
	// Errors are NOT terminal: the next tenant_surface_changed
	// notification (or the next apid write that bumps the
	// surface) re-tries. The notify subscriber logs-and-
	// swallows so a transient CA outage can't block the edge.
	RequestCertForSurface(ctx context.Context, surfaceID string) error
}

// WildcardCertIssuer is an optional extension implemented by engines that can
// mint a customer-owned wildcard certificate with DNS-01. It is separate from
// CertIssuer so existing surface-only test doubles and adapters remain valid.
type WildcardCertIssuer interface {
	RequestCertForWildcardDomain(ctx context.Context, domain string) error
}
