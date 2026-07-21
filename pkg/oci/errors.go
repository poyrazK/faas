// Package oci — error sentinels for the puller-side failure modes.
//
// ADR-021 (image digest enforcement hardening, G1): pkg/api.SentinelToCode
// lifts these three sentinels to the RFC 7807 codes
// CodeImageNotFound (422), CodeImageEgressDenied (403), and
// CodeImageManifestInvalid (422). imaged writes the resulting code
// into deployments.error_code at the markDeployFailed helper, so a
// customer / dashboard can branch on a stable string rather than
// parsing the free-text deployments.error.
//
// Sentinels are wrapped at the failure site via fmt.Errorf("%w: …",
// sentinel, …) so errors.Is matches both the bare sentinel and any
// %w-wrapped form. IsImageTerminal is the cheap pre-check.
//
// ErrEgressDenied (the original egress denylist sentinel) is preserved
// for backwards compat with pkg/oci consumers that already check it
// via errors.Is; ADRs above added ErrImageEgressDenied as the new
// single-source-of-truth sentinel that pkg/api.SentinelToCode consults.
// IsImageTerminal treats both as terminal.
package oci

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrImageNotFound is wrapped into the error returned from
// RegistryClient.PullDigest when the registry HTTP returns 404 on
// the manifest blob. Distinct from a parse failure (which yields
// ErrImageManifestInvalid) and from the upstream-hop dial-failure
// (which yields ErrImageEgressDenied via the egress denylist).
var ErrImageNotFound = errors.New("oci: image not found")

// ErrImageEgressDenied is wrapped into the error returned from
// EgressDialContext when the resolved IP is in the egress denylist
// (RFC1918, link-local, IMDS 169.254/16, ULA, multicast — see
// pkg/oci/egress.go). Security-class signal: surfaced as the 403
// code ImageEgressDenied, distinct from the 422 validation codes
// so customers can tell the policy block apart from a 404.
var ErrImageEgressDenied = errors.New("oci: egress denied by policy")

// ErrImageManifestInvalid is wrapped into the error returned from
// PullManifest when the body is a manifest-list (multi-arch), fails
// schema validation, or otherwise cannot be reduced to a single-
// platform Manifest. The two-drive build path requires a flat
// per-platform manifest to compute the above-base layer list
// (see pkg/imaged/handler.go::aboveBaseLayers).
var ErrImageManifestInvalid = errors.New("oci: manifest invalid")

// (Legacy ErrEgressDenied is declared in pkg/oci/egress.go to keep its
// original home; IsImageTerminal below consults it directly.)

// IsImageTerminal reports whether err is one of the three puller-side
// sentinels above. Use it as a cheap pre-check before falling
// through to errors.As / LiftSentinel.
//
// IsImageTerminal also accepts the legacy ErrEgressDenied sentinel
// so older pkg/oci call sites that wrap only that one still report
// as terminal — ADR-021 closes the gap by adding ErrImageEgressDenied
// as the canonical sentinel, but existing wrapping sites continue
// to behave correctly.
func IsImageTerminal(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrImageNotFound) ||
		errors.Is(err, ErrImageEgressDenied) ||
		errors.Is(err, ErrImageManifestInvalid) ||
		errors.Is(err, ErrEgressDenied)
}

// SentinelToCode maps a pkg/oci puller-side sentinel error to its
// stable RFC 7807 code from pkg/api (ADR-021). Returns ("", false)
// when err doesn't wrap any of the three sentinels — callers fall
// through to their generic failure path.
//
// This is the single source of truth for the sentinel → code
// mapping; imaged's buildImageLayer handler and any future pkg/oci
// consumer consult this rather than matching strings. The mapping
// lives in pkg/oci (the package that owns the sentinels) and reads
// from pkg/api (the package that owns the codes) — the dependency
// direction pkg/oci → pkg/api is already established and load-bearing.
func SentinelToCode(err error) (code string, ok bool) {
	if err == nil {
		return "", false
	}
	switch {
	case errors.Is(err, ErrImageNotFound):
		return api.CodeImageNotFound, true
	case errors.Is(err, ErrImageEgressDenied):
		return api.CodeImageEgressDenied, true
	case errors.Is(err, ErrImageManifestInvalid):
		return api.CodeImageManifestInvalid, true
	}
	return "", false
}
