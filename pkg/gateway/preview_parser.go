// preview_parser.go — pure parser for preview-host hostnames
// (issue #272 / ADR-095 PR-B). Extracted from
// cmd/gatewayd-internal/backend.go::pgRouter.previewScopeFromHost so the
// on-demand cert allowlist (pkg/gateway/allowlist.go) and the routing
// layer share a single source of truth — a hostname that the router
// accepts must be acceptable to the allowlist (and vice versa), and the
// two paths cannot drift if they call into the same function.
//
// Pure: no I/O, no globals, no logger. Lives in pkg/gateway (not
// cmd/gatewayd-internal) so pkg/gateway stays free of pkg/state and the
// router code can stay close to the rest of the edge TLS plumbing.
package gateway

import (
	"strconv"
	"strings"
)

// PreviewScopeFromHost peels a preview-hostname shape
// `pr-{N}-{parent-slug}.<zone>` into (PR number, parent slug).
// appsSuffix is the leading-dot form (".gregale.dev"); empty
// suffix refuses everything. The function returns ok=false for any
// deviation from the locked shape — prod hosts, uppercase, leading
// zeros, embedded dots in the parent slug, non-numeric PR numbers,
// missing parent slug.
//
// Match is strict: case-sensitive `pr-` prefix; ASCII digits for the
// PR number (no leading zero — GitHub PR numbers are never 0-padded,
// and a scan that produced `pr-0...` is either malformed or hostile);
// parent slug limited to the existing slug charset `[a-z0-9-]` with
// no embedded dot. Any deviation returns ok=false; the caller falls
// back to the prod-shape resolution (which itself fails closed at
// the allowlist for non-prod shapes).
//
// Caller responsibility: this function does NOT consult the apps
// table. The lookup half (resolving (n, slug) → preview app row →
// state check) is the responsibility of OnDemandPreviewLookup in
// pkg/gateway/allowlist.go.
func PreviewScopeFromHost(appsSuffix, host string) (number int, slug string, ok bool) {
	if appsSuffix == "" {
		return 0, "", false
	}
	label, ok := strings.CutSuffix(host, appsSuffix)
	if !ok || label == "" {
		return 0, "", false
	}
	if !strings.HasPrefix(label, "pr-") {
		return 0, "", false
	}
	tail := label[3:]
	dash := strings.IndexByte(tail, '-')
	if dash <= 0 || dash == len(tail)-1 {
		return 0, "", false
	}
	digits := tail[:dash]
	if digits[0] == '0' && len(digits) > 1 {
		return 0, "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, "", false
		}
	}
	rest := tail[dash+1:]
	// No inner dots: the parent slug must not contain a separator (the
	// platform slug charset already excludes dots; this guard rejects
	// pathological scans like `pr-42-foo.bar.gregale.dev` whose label is
	// "pr-42-foo.bar" and would otherwise split as slug="foo").
	if strings.Contains(rest, ".") {
		return 0, "", false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < 'a' || c > 'z' {
			if c < '0' || c > '9' {
				if c != '-' {
					return 0, "", false
				}
			}
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, rest, true
}

// DeploymentScopeFromHost peels a deployment-preview hostname shape
// `deploy-{N}.{slug}.{deploySuffix}` into (deployment ordinal, slug).
// deploySuffix is the leading-dot form (".gregale.dev"); empty suffix
// refuses everything. The function returns ok=false for any deviation
// from the locked shape — prod hosts, uppercase, leading zeros,
// embedded dots in the slug, non-numeric ordinal, missing slug.
//
// Why this is a sibling of PreviewScopeFromHost and NOT a
// generalization: the two shapes serve different audiences. PR previews
// (ADR-095) are a GitHub-Checks-driven ephemeral surface that the
// customer never sees; deployment previews (ADR-122 §C) are a
// customer-facing "share this URL with a teammate" surface that is
// stamped on the deployment row at INSERT time. Conflating the two
// would force the cert issuer to mint for both branches under one
// allowlist decision; keeping them separate means a PR-preview cert
// can never accidentally serve traffic for a deployment preview (or
// vice versa) and the route table stays closed-set.
//
// Match is strict: case-sensitive `deploy-` prefix (NOT `pr-` or
// `deployment-` — the closed-set shape is the single source of truth);
// ASCII digits for the ordinal (no leading zero — same rationale as
// PR previews); slug limited to the existing slug charset
// `[a-z0-9-]` with no embedded dot. Any deviation returns ok=false;
// the caller falls back to the prod-shape resolution (which itself
// fails closed at the allowlist for non-prod shapes).
//
// Caller responsibility: this function does NOT consult the
// deployments or apps table. The lookup half (resolving (n, slug) →
// deployment row → status check) is the responsibility of
// OnDemandDeploymentLookup in pkg/gateway/allowlist.go.
func DeploymentScopeFromHost(deploySuffix, host string) (ordinal int, slug string, ok bool) {
	if deploySuffix == "" {
		return 0, "", false
	}
	label, ok := strings.CutSuffix(host, deploySuffix)
	if !ok || label == "" {
		return 0, "", false
	}
	if !strings.HasPrefix(label, "deploy-") {
		return 0, "", false
	}
	tail := label[7:]
	// No inner dots: the slug must not contain a separator (the
	// platform slug charset already excludes dots; this guard
	// rejects pathological scans like `deploy-42.foo.gregale.dev`
	// whose label is "deploy-42.foo" and would otherwise split as
	// slug="42.foo").
	if strings.Contains(tail, ".") {
		return 0, "", false
	}
	// Slug comes first, ordinal comes after — opposite of the PR
	// preview shape (which is pr-{N}-{slug}). The deployment
	// preview shape is {slug}.deploy-{N} only as a documentation
	// typo; the locked shape is deploy-{N}.{slug}, same as PR
	// preview. Cut on the FIRST '-' after the digits so a slug
	// containing '-' is honored verbatim.
	dash := strings.IndexByte(tail, '-')
	if dash <= 0 || dash == len(tail)-1 {
		return 0, "", false
	}
	digits := tail[:dash]
	if digits[0] == '0' && len(digits) > 1 {
		return 0, "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, "", false
		}
	}
	rest := tail[dash+1:]
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c < 'a' || c > 'z' {
			if c < '0' || c > '9' {
				if c != '-' {
					return 0, "", false
				}
			}
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, rest, true
}

// BuildDeploymentPreviewURL is the writer counterpart to
// DeploymentScopeFromHost: it stamps the deployment-preview URL
// shape `deploy-{N}.{slug}{deploySuffix}` from the (ordinal,
// slug) pair the apid read-path resolves. Mirrors the round-trip
// that the cert allowlist issues — every URL it returns MUST
// re-peel through DeploymentScopeFromHost to (ordinal, slug) by
// design, so the URL grammar cannot drift from the parser.
//
// Caller responsibility:
//   - deploySuffix is the leading-dot form (".gregale.dev").
//     Empty suffix is the "no deployment-preview zone on this
//     platform" signal; the call returns empty so the caller
//     can distinguish "preview disabled" from "preview live"
//     on the wire (the handler emits Host="" in that case).
//   - ordinal <= 0 or slug == "" is malformed; the call returns
//     empty so the caller can surface a 422 instead of minting
//     a URL the allowlist will refuse to admit.
//
// Why the helper lives in pkg/gateway (not pkg/api or cmd/apid):
// the allowlist (pkg/gateway/allowlist.go) and the URL stamper must
// agree on the URL grammar. Co-locating the writer with the parser
// at pkg/gateway/preview_parser.go closes the round-trip inside one
// file and prevents the cert-issuance path and the apid URL-emission
// path from drifting apart.
//
// Issue #976 / ADR-122 / SAFE-RELEASES-C.
func BuildDeploymentPreviewURL(deploySuffix string, ordinal int, slug string) string {
	if deploySuffix == "" {
		return ""
	}
	if ordinal <= 0 || slug == "" {
		return ""
	}
	return "deploy-" + strconv.Itoa(ordinal) + "." + slug + deploySuffix
}
