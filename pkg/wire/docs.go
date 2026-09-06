// Wire-level constants for the documentation host and platform
// contact host embedded in customer-facing URLs (RFC 7807
// Problem.DocsURL on the apid REST surface,
// errdetails.ErrorInfo.Metadata["docs_url"] on the gRPC envelope,
// the CLI's synthesized docs row when Problem.DocsURL is empty, and
// the outbound User-Agent header on OCI registry traffic).
//
// These hosts are part of the customer-visible contract — they
// travel into error responses and into every manifest a customer's
// app produces — so the rename discipline matches pkg/wire/wake.go:
// every site reads the constant, no site inlines the literal. The
// tripwire TestLintTripwire_NoLiteralDocsDomainEverywhere in
// cmd/gregale/lint_tripwires_test.go forbids DOMAIN-shaped literals
// outside this package; rotating the host deliberately means editing
// these two constants plus the test pin at
// pkg/grpcerr/grpcerr_test.go (the test pins the literal emitted
// shape so a forgotten host rotation trips CI).
//
// Issue: https://github.com/onebox-faas/faas/issues/420
package wire

const (
	// DocsHost is the LEGACY documentation host. It is NOT a
	// deployed site: DNS resolves (Cloudflare) but every path
	// answers 404, and the deploy/ansible/roles/docs-tls/ runbook
	// this comment used to cite was never written.
	//
	// Do not introduce new uses. Documentation lives at
	// DocsBaseURL (https://gregale.dev/docs). Anything that still
	// composes against DocsHost is repaired at the emission
	// boundary by api.NormalizeDocsURL, which WithDocs() applies —
	// so a stale link degrades to the docs index rather than a
	// dead host. The constant is retained only so that normalizer
	// can recognise and rewrite the legacy form.
	//
	// Remaining composers (all normalized on the way out, tracked
	// for a follow-up sweep):
	//   - pkg/vmmdgrpc/{proto.go, server.go, migration_handlers.go}
	//     (~30 sites, internal daemon-to-daemon /vmmd#* anchors)
	//   - cmd/gregalectl/{main.go, output.go} (operator CLI)
	DocsHost = "docs.gregale.dev"

	// DocsBaseURL is where the documentation actually lives. The
	// site is a SPA that answers HTTP 200 on every path and
	// renders its 404 client-side, so only the slugs in
	// api.NormalizeDocsURL's allowlist resolve to real content —
	// a link checker cannot tell the difference.
	DocsBaseURL = "https://" + PlatformHost + "/docs"

	// DashboardBillingURL is where a customer resolves a past-due
	// balance. The 402 "Account suspended" path points here rather
	// than at documentation: a suspended customer needs the page
	// that takes payment, not a page that explains billing.
	DashboardBillingURL = "https://" + PlatformHost + "/dashboard/billing"

	// PlatformHost is the platform contact host used in outbound
	// User-Agent headers (OCI registry traffic — ghcr.io, ECR,
	// Docker Hub, etc.). Distinct from DocsHost because the
	// contact host is the platform's home, not its documentation:
	// a registry operator tailing access logs sees the platform
	// contact on every request, and pointing that at a docs site
	// is confusing.
	//
	// Used by:
	//   - pkg/oci/registry.go (RegistryClient User-Agent)
	//   - pkg/storage/oci.go (OCIRegistryStorageBackend User-Agent)
	PlatformHost = "gregale.dev"

	// DeployWildcardSuffix is the leading-dot wildcard suffix the
	// per-deployment preview URL surface mints certs under
	// (issue #976 / ADR-122 / SAFE-RELEASES-C). Pairs with the
	// parser at pkg/gateway/preview_parser.go::DeploymentScopeFromHost
	// — keeping the suffix as a single constant prevents the cert
	// allowlist and the apid URL-emission path from drifting
	// apart. Empty value disables the deployment-preview branch
	// entirely (the allowlist's deploymentLookup branch, and the
	// C.2 wire endpoint's Host field).
	//
	// Distinct from AppsWildcardSuffix because the two audiences
	// don't overlap: PR previews live under *.apps.gregale.dev
	// (GitHub-Checks-driven ephemeral); deployment previews live
	// under *.gregale.dev (customer-facing, shareable with a
	// teammate). The cert issuer would mint one wildcard cert
	// per suffix, so the constant is per-suffix.
	DeployWildcardSuffix = ".gregale.dev"
	// DeployPreviewURIScheme is the URI scheme stamped on the
	// GET /v1/deployments/{id}/url response's URL field
	// (SAFE-RELEASES-C.2). "https" is the only valid production
	// value (certmagic's OnDemand HTTP-01 challenge is
	// HTTPS-driven); http is only for a non-TLS staging
	// environment where the challenge is also disabled.
	DeployPreviewURIScheme = "https"
)
