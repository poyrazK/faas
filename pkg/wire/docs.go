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
	// DocsHost is the canonical documentation host embedded in
	// every Problem.DocsURL / ErrorInfo.Metadata["docs_url"] the
	// platform emits. The TLS cert for this host must be renewed
	// in lock-step with any rotation; see deploy/ansible/roles/
	// docs-tls/ for the operator-side runbook.
	//
	// Used by:
	//   - pkg/vmmdgrpc/{proto.go, server.go} (gRPC problem envelope)
	//   - pkg/auth/middleware/middleware.go (apid REST 402 Detail)
	//   - cmd/gregale/output.go (CLI error footer)
	//
	// The pkg/grpcerr/grpcerr_test.go round-trip assertions pin the
	// literal https://docs.gregale.dev/... form (not the constant)
	// so a future host rotation that forgets to update the test
	// trips CI.
	DocsHost = "docs.gregale.dev"

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
