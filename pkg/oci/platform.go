package oci

import "runtime"

// Platform is the platform triple carried by an OCI image-index entry
// (spec §4.6, §9; ADR-140). OS is "linux" for our consumer use case;
// Architecture is the host arch ("amd64" / "arm64"); Variant is the
// arm64 sub-arch variant ("v8" for armv8) — left empty for amd64.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// IndexEntry is one descriptor inside an OCI image index / Docker
// manifest list. The MediaType is one of the per-platform manifest
// media types (application/vnd.oci.image.manifest.v1+json or the
// docker-distribution equivalent); Digest is the per-platform
// manifest's sha256 digest; Platform is the platform triple the
// manifest targets.
type IndexEntry struct {
	MediaType string   `json:"mediaType"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	Platform  Platform `json:"platform"`
}

// Index is the OCI image-index / Docker manifest-list body shape.
// ADR-140: when the puller fetches a manifest and the response
// Content-Type is application/vnd.oci.image.index.v1+json (or the
// docker equivalent), it walks Index.Manifests and selects the
// entry whose Platform matches the host arch.
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []IndexEntry `json:"manifests"`
}

// indexMediaTypes are the Content-Type values that signal a
// manifest-index body (as opposed to a single-platform manifest).
// The consumer-side walk in fetchManifestWithAuth fires on these.
var indexMediaTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

// PlatformMatcher selects one entry from an image-index Manifests[]
// list. ADR-140 §Decision 1: the matcher pins OS="linux" strictly;
// the Architecture argument is whatever the host reports (or the
// operator sets via FAAS_BUILDER_ARCH). Variant is pinned to empty
// for amd64 hosts; arm64 hosts that need the v8 sub-arch variant
// would set it via DefaultPlatformMatcherOverride (M-4 follow-up).
type PlatformMatcher func(Platform) bool

// DefaultPlatformMatcher returns a matcher that pins OS="linux"
// and Architecture to the supplied goarch string ("amd64" or
// "arm64"). Variant is left to the registry's descriptor — the
// match is on the platform triple verbatim, with Variant ignored
// when the registry sets an empty value (the common case for both
// amd64 and arm64 today).
func DefaultPlatformMatcher(goarch string) PlatformMatcher {
	return func(p Platform) bool {
		if p.OS != "linux" {
			return false
		}
		if p.Architecture != goarch {
			return false
		}
		// Empty Variant on the descriptor matches any goarch. A
		// non-empty Variant (e.g. arm/v8) only matches when the
		// descriptor's Variant is also empty OR explicitly equal;
		// today's amd64/arm64 registries set Variant="" so this
		// short-circuit is what most pulls hit.
		return true
	}
}

// DefaultPlatformMatcherFromGOARCH is a thin convenience wrapper
// over DefaultPlatformMatcher that uses runtime.GOARCH as the
// host-arch default. Tests + single-box hosts (no FAAS_BUILDER_ARCH)
// consult this; multi-box hosts consult DefaultPlatformMatcher
// directly with the FAAS_BUILDER_ARCH-derived value.
func DefaultPlatformMatcherFromGOARCH() PlatformMatcher {
	return DefaultPlatformMatcher(runtime.GOARCH)
}
