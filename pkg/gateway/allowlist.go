// On-demand HTTP-01 allowlist (spec §11, §7). CertMagic's
// OnDemand.DecisionFunc asks "may I mint a cert for this hostname?"; we
// answer by looking up either:
//   - a customer-verified custom_domains row (legacy path), OR
//   - a preview-host apps row in state='open' (issue #272 / ADR-095 PR-B)
//
// Why this lives in pkg/gateway (not pkg/state): the allowlist is part of the
// edge's TLS seam. pkg/state holds the rows; pkg/gateway decides what to do
// with them. The query is identical to the one pgRouter.ResolveHost uses for
// routing (cmd/gatewayd-internal/backend.go), so we cannot serve one hostname from
// routing and a different one from the allowlist — they share the Store.
//
// Caching: none today. The custom_domains table is small (one per customer
// domain, ~10⁴ at fleet scale), the query is index-keyed, and certmagic
// serializes on-demand mints per hostname via an in-process mutex. Add a
// short TTL cache here if the table grows past that.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// OnDemandLookup is the function signature NewPGAllowlist consumes for the
// custom-domain path. It is a function (not an interface on a Store) so
// callers don't have to declare an adapter type that bridges the
// (state.CustomDomain) → (any) return-type mismatch. Production passes a
// closure that calls state.PgStore.DomainByName and wraps state.ErrNotFound
// as gateway.ErrNotFound; tests inject fakes directly. The function returns
// any because the result is type-asserted on the Verified() method below —
// pkg/gateway stays free of pkg/state.
type OnDemandLookup func(ctx context.Context, domain string) (any, error)

// OnDemandSurfaceLookup is the tenant-surface half of the allowlist
// (PR-D commit 4 / ADR-100 amendment). The closure is expected
// to load the tenant_surface that claims `host` (via
// state.PgStore.TenantSurfaceByHostname) and return a struct
// satisfying the tenantSurfaceVerified interface below. A
// nil surfaceLookup disables the branch — useful for tests
// and for staging paths that don't mint per-surface certs.
//
// Mirrors OnDemandLookup's shape: returns (any, error).
// Callers MUST surface ErrNotFound from the lookup closure
// when no row exists; other errors fail closed at Warn level.
type OnDemandSurfaceLookup func(ctx context.Context, host string) (any, error)

// tenantSurfaceVerified is the shape NewPGAllowlist needs
// from the surface-lookup result. The concrete state.TenantHostname
// satisfies it (state.TenantHostname.Verified() returns
// !VerifiedAt.IsZero()); tests use a local fake. Mirrors the
// verified interface for custom domains.
//
// Why TenantHostname and not TenantSurface: the lookup path
// (state.PgStore.GetTenantHostnameByName) returns the
// hostname row, not the parent surface. The hostname row's
// Verified() is the load-bearing predicate — the parent
// surface's Status is filtered upstream by the store's
// SELECT (soft-deleted surfaces never surface). PR-D code
// review Candidate 4's "natural caller returns TenantSurface"
// claim is wrong: the natural caller returns TenantHostname,
// which DOES satisfy this interface.
type tenantSurfaceVerified interface {
	Verified() bool
}

// OnDemandPreviewLookup is the preview-host half of the allowlist. It is
// invoked with (PR number, parent-slug) extracted from the hostname by
// PreviewScopeFromHost; the closure is expected to load the preview apps row
// (slug `pr-{N}-{parent-slug}`) and return it for the
// PreviewOpen() assertion. nil previewLookup means the preview branch is
// disabled — useful for unit tests and for staging paths that don't mint
// preview certs.
//
// Mirrors OnDemandLookup's shape: returns (any, error). Callers MUST surface
// ErrNotFound from the lookup closure when no row exists; other errors
// fail closed at Warn level.
type OnDemandPreviewLookup func(ctx context.Context, prNumber int, parentSlug string) (any, error)

// OnDemandDeploymentLookup is the deployment-preview half of the
// allowlist (issue #976 / ADR-122 / SAFE-RELEASES-C). It is
// invoked with (deployment ordinal, app slug) extracted from the
// hostname by DeploymentScopeFromHost; the closure is expected to
// load the deployment row and return it for the
// DeploymentPreviewActive() assertion (a deployment is
// "previewable" when its row exists and has not been superseded
// or removed). nil deploymentLookup means the deployment-preview
// branch is disabled — useful for unit tests and for staging
// paths that don't mint deployment-preview certs.
//
// Mirrors OnDemandPreviewLookup's shape: returns (any, error).
// Callers MUST surface ErrNotFound from the lookup closure when
// the row is missing; other errors fail closed at Warn level.
type OnDemandDeploymentLookup func(ctx context.Context, ordinal int, slug string) (any, error)

// deploymentPreviewActive is the shape NewPGAllowlist needs from
// the deployment-preview lookup result. The concrete state.Deployment
// satisfies it via a small adapter method added alongside this
// change; tests use a local fake. Mirrors the verified interface
// for custom domains and the previewOpen interface for PR
// previews — pkg/gateway stays free of pkg/state.
type deploymentPreviewActive interface {
	DeploymentPreviewActive() bool
}

// verified is the shape NewPGAllowlist needs from the custom-domain lookup
// result. The concrete state.CustomDomain satisfies it; tests use
// fakeCustomDomain. Keeping this as a local interface (rather than
// importing pkg/state) means pkg/gateway stays free of pgx.
type verified interface {
	Verified() bool
}

// previewOpen is the shape NewPGAllowlist needs from the preview lookup
// result. The concrete state.App satisfies it; tests use fakePreviewApp.
// Mirrors the verified interface — pkg/gateway stays free of pkg/state.
type previewOpen interface {
	PreviewOpen() bool
}

// ErrNotFound is the sentinel NewPGAllowlist recognizes as "this hostname is
// not in the custom_domains table" so it can return false without logging at
// Warn level (the steady-state denial path; logging here would flood the
// gatewayd-internal log on every scan of an unowned hostname). Callers MUST surface
// this sentinel from their lookup closure when the row is missing — wrapping
// state.ErrNotFound (or any other concrete store sentinel) is the production
// path in cmd/gatewayd-internal/. The same sentinel applies to the preview
// branch: a missing row is the steady-state denial path, not a warning.
var ErrNotFound = errors.New("gateway: domain not found in allowlist")

// NewPGAllowlist returns an OnDemandAllowlist backed by the Postgres store.
// The store MUST be the same instance pgRouter uses for routing so the two
// can't drift (a hostname that routes must be allowlisted, and vice versa).
// The slog logger is used to record denied on-demand requests — those are
// the loud signal that someone is poking the edge for a hostname we don't
// own.
//
// appsSuffix is the leading-dot suffix of the platform zone
// (".gregale.dev"). The custom-domain path ignores it; the preview
// path uses it to peel pr-{N}.{parent-slug}.{suffix} via
// PreviewScopeFromHost. Empty appsSuffix disables the preview branch.
//
// surfaceLookup is the tenant-surface branch (PR-D commit 4):
// when set, the closure is consulted AFTER the custom-domain
// branch fails ErrNotFound and BEFORE the preview branch.
// The returned struct's Verified() must return true (the
// hostname row must have a non-zero VerifiedAt) for the
// allowlist to admit the host. nil surfaceLookup disables
// the branch — useful for staging paths that don't mint
// per-surface certs.
//
// NewPGAllowlist never panics on a nil lookup: nil is treated as
// deny-all, which is the safe fail-closed default for an unconfigured edge.
//
// deploymentLookup + deploySuffix (issue #976 / ADR-122 /
// SAFE-RELEASES-C) extend the allowlist with a deployment-preview
// branch. Hostnames whose shape matches
// `deploy-{N}.{slug}.{deploySuffix}` are peeled by
// DeploymentScopeFromHost, the closure loads the deployment row,
// and the row's DeploymentPreviewActive() must return true for the
// allowlist to admit the host. nil deploymentLookup or empty
// deploySuffix disables the branch entirely (existing single-daemon
// gateways that don't mint deployment-preview certs).
func NewPGAllowlist(
	customLookup OnDemandLookup,
	previewLookup OnDemandPreviewLookup,
	surfaceLookup OnDemandSurfaceLookup,
	deploymentLookup OnDemandDeploymentLookup,
	appsSuffix string,
	deploySuffix string,
	log *slog.Logger,
) OnDemandAllowlist {
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context, host string) (bool, error) {
		// Custom-domain path (legacy). On ErrNotFound we fall through to
		// the tenant-surface path; on any other error we fail closed.
		if customLookup != nil {
			dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			raw, err := customLookup(dbCtx, host)
			cancel()
			if err == nil {
				v, ok := raw.(verified)
				if !ok {
					log.Warn("gateway: allowlist lookup returned non-verified type; failing closed",
						"host", host)
					return false, nil
				}
				if !v.Verified() {
					log.Info("gateway: on-demand denied: domain exists but TXT challenge unverified",
						"host", host)
					return false, nil
				}
				return true, nil
			}
			if !errors.Is(err, ErrNotFound) {
				log.Warn("gateway: allowlist lookup failed; failing closed",
					"host", host, "err", err)
				return false, nil
			}
		}

		// Tenant-surface path (PR-D commit 4 / ADR-100 amendment).
		// Mirrors the custom-domain branch shape: ErrNotFound
		// falls through to the preview path; other errors fail
		// closed. The row's Verified() must return true — the
		// caller has published the _faas-verify TXT record and
		// the dns_poller has flipped verified_at. Soft-deleted
		// parent surfaces are filtered out by the store so the
		// row's Verified() is meaningful.
		if surfaceLookup != nil {
			dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			raw, err := surfaceLookup(dbCtx, host)
			cancel()
			if err == nil {
				v, ok := raw.(tenantSurfaceVerified)
				if !ok {
					log.Warn("gateway: surface allowlist lookup returned non-verified type; failing closed",
						"host", host)
					return false, nil
				}
				if !v.Verified() {
					log.Info("gateway: on-demand denied: surface hostname exists but TXT challenge unverified",
						"host", host)
					return false, nil
				}
				return true, nil
			}
			if !errors.Is(err, ErrNotFound) {
				log.Warn("gateway: surface allowlist lookup failed; failing closed",
					"host", host, "err", err)
				return false, nil
			}
		}

		// Deployment-preview path (issue #976 / ADR-122 / SAFE-RELEASES-C).
		// Only fires for hostnames whose shape matches
		// deploy-{N}.{slug}.{deploySuffix} — anything else (custom
		// domains, surface hostnames, prod, malformed scans) is refused.
		// deploymentLookup==nil OR empty deploySuffix disables the branch
		// entirely (e.g. tests + staging paths that don't mint
		// deployment-preview certs). Sits BEFORE the PR-preview branch
		// because the deployment-preview suffix (".gregale.dev") does
		// not collide with the PR-preview suffix (".apps.gregale.dev"),
		// but the parsers fail closed on the wrong-suffix input so
		// the order is purely a code-readability choice.
		if deploymentLookup == nil || deploySuffix == "" {
			// fall through to the PR-preview branch below
		} else {
			ord, slug, ok := DeploymentScopeFromHost(deploySuffix, host)
			if !ok {
				// Quiet denial: this is the steady-state path for any
				// hostname that isn't a deployment-preview shape.
				// Logging here would flood on every scan of an
				// unowned hostname. The PR-preview branch below
				// handles its own shape; anything that reaches the
				// bottom of the function is denied.
			} else {
				dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				raw, err := deploymentLookup(dbCtx, ord, slug)
				cancel()
				if err != nil {
					if errors.Is(err, ErrNotFound) {
						// Quiet: missing deployment row is the
						// steady-state denial path. Fall through
						// to the PR-preview branch — the host might
						// be a PR preview, in which case the
						// allowlist still admits it.
					} else {
						log.Warn("gateway: deployment allowlist lookup failed; failing closed",
							"host", host, "ordinal", ord, "slug", slug, "err", err)
						return false, nil
					}
				} else {
					v, ok := raw.(deploymentPreviewActive)
					if !ok || !v.DeploymentPreviewActive() {
						log.Info("gateway: on-demand denied: deployment exists but is not preview-active",
							"host", host, "ordinal", ord, "slug", slug)
						return false, nil
					}
					return true, nil
				}
			}
		}

		// Preview-host path (PR-B). Only fires for hostnames whose shape
		// matches pr-{N}.{slug}.{appsSuffix} — anything else (custom
		// domains, prod, malformed scans) is refused. previewLookup==nil
		// disables the branch entirely (e.g. tests).
		if previewLookup == nil || appsSuffix == "" {
			return false, nil
		}
		n, slug, ok := PreviewScopeFromHost(appsSuffix, host)
		if !ok {
			// Quiet denial: this is the steady-state path for any
			// hostname that isn't a preview shape. Logging here would
			// flood on every scan of an unowned hostname.
			return false, nil
		}
		dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		raw, err := previewLookup(dbCtx, n, slug)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// Quiet: missing preview row is the steady-state path.
				return false, nil
			}
			log.Warn("gateway: preview allowlist lookup failed; failing closed",
				"host", host, "pr_number", n, "parent_slug", slug, "err", err)
			return false, nil
		}
		v, ok := raw.(previewOpen)
		if !ok || !v.PreviewOpen() {
			log.Info("gateway: on-demand denied: preview exists but state is not 'open'",
				"host", host, "pr_number", n, "parent_slug", slug)
			return false, nil
		}
		return true, nil
	}
}

// StaticAllowlist returns an OnDemandAllowlist that allows exactly the given
// hostnames. Used by tests and by the staging path where CertMagic's
// staging-CA allows a fixed hostname.
func StaticAllowlist(hosts ...string) OnDemandAllowlist {
	set := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		set[h] = struct{}{}
	}
	return func(_ context.Context, host string) (bool, error) {
		_, ok := set[host]
		return ok, nil
	}
}

// TenantHostnameLookup is the narrow read seam the production
// surface-lookup closure needs. The interface returns (any,
// error) so pkg/gateway stays free of pkg/state (the wildcard
// TLS path lives in gatewayd-public, pkg/gateway cannot import
// the state package). The concrete value inside the any is
// state.TenantHostname, which satisfies tenantSurfaceVerified
// via its Verified() method.
//
// The single method name tracks state.PgStore.GetTenantHostnameByName
// so production can pass the store directly through this
// adapter, and tests can build a fake without a struct.
type TenantHostnameLookup interface {
	GetTenantHostnameByName(ctx context.Context, hostname string) (any, error)
}

// NewSurfaceLookupByHostname is the production OnDemandSurfaceLookup
// factory. It closes over a TenantHostnameLookup (the read
// seam) and returns a closure that matches the OnDemandSurfaceLookup
// signature. The closure maps ErrNotFound to itself so the
// tenant-surface branch's "missing row" path is the
// steady-state denial rather than a Warn-logged failure.
//
// The factory MUST be constructed at the gatewayd-public
// startup path (the wildcard TLS consumer that consults the
// OnDemandHTTP01Allowlist) — the per-host engine in
// gatewayd-internal doesn't drive this branch (it uses
// cfg.ObtainCertSync directly, not certmagic's on-demand
// TLS handshake code path).
func NewSurfaceLookupByHostname(loader TenantHostnameLookup) OnDemandSurfaceLookup {
	if loader == nil {
		return nil
	}
	return func(ctx context.Context, host string) (any, error) {
		row, err := loader.GetTenantHostnameByName(ctx, host)
		if err != nil {
			return nil, err
		}
		return row, nil
	}
}

// CountingAllowlist wraps an inner allowlist and records how many times it
// returned true/false. Used by tests to assert the CertMagic wire consults
// the callback (instead of bypassing it via the wildcard cert cache).
//
// Counter access goes through atomic.Int64 so tests can read it concurrently
// with the callback (CertMagic invokes the decision func on its own goroutine).
type CountingAllowlist struct {
	Inner OnDemandAllowlist
	Allow atomic.Int64
	Deny  atomic.Int64
	mu    sync.Mutex
	seen  []string
}

// NewCountingAllowlist wraps inner. If inner is nil, StaticAllowlist() (deny-all)
// is used so a nil-wrapped counter still records calls.
func NewCountingAllowlist(inner OnDemandAllowlist) *CountingAllowlist {
	if inner == nil {
		inner = StaticAllowlist()
	}
	return &CountingAllowlist{Inner: inner}
}

// allow returns true iff inner does, and increments the matching counter.
// The signature matches OnDemandAllowlist via AsFunc; using a method keeps
// the inner field unexported-safe.
func (c *CountingAllowlist) allow(ctx context.Context, host string) (bool, error) {
	ok, err := c.Inner(ctx, host)
	c.mu.Lock()
	c.seen = append(c.seen, host)
	c.mu.Unlock()
	if err != nil {
		// Lookup failures count as denials; we surface the error to the
		// caller unchanged so the DecisionFunc adapter can decide whether
		// to log it. The counter still increments so tests can assert the
		// wire actually called back.
		c.Deny.Add(1)
		return false, err
	}
	if ok {
		c.Allow.Add(1)
	} else {
		c.Deny.Add(1)
	}
	return ok, nil
}

// AsFunc returns the OnDemandAllowlist function view of this counter. Use
// this when handing the counter to certmagic.Config.OnDemand.DecisionFunc
// adapters — certmagic's signature is func(ctx, name) error, and
// OnDemandAllowlist is func(ctx, host) (bool, error); this method bridges
// the two on the predicate side.
func (c *CountingAllowlist) AsFunc() OnDemandAllowlist {
	return c.allow
}

// Seen returns a copy of the hostnames the callback was invoked with, in
// call order. Tests use this to assert certmagic reached the callback.
func (c *CountingAllowlist) Seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.seen))
	copy(out, c.seen)
	return out
}
