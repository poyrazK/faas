package oci

import (
	"fmt"
	"strings"
)

// Image references. We accept the standard Docker/OCI reference grammar,
// normalised to the canonical form the registry v2 API needs:
//
//	[registry[:port]/]repository[:tag][@sha256:digest]
//
// Per gap G1 (spec §17) v1 pulls from public registries only, digest-pinned
// where possible. A bare name defaults to Docker Hub (docker.io) with the
// implicit `library/` namespace, matching `docker pull` semantics.
const (
	defaultRegistry = "docker.io"
	// dockerAPIHost is the actual v2 API endpoint for Docker Hub — the
	// canonical name docker.io is not the host you talk to.
	dockerAPIHost = "registry-1.docker.io"
	defaultTag    = "latest"
	digestAlgo    = "sha256:"
	digestHexLen  = 64
)

// Reference is a parsed image reference.
type Reference struct {
	Registry   string // canonical registry name, e.g. "docker.io", "ghcr.io"
	Repository string // e.g. "library/nginx", "org/app"
	Tag        string // "" when a digest is given without a tag
	Digest     string // "sha256:<64 hex>", or "" for a tag-only reference
}

// ParseReference parses a Docker/OCI image reference into its canonical parts.
// It defaults the registry to Docker Hub and, there, the `library/` namespace
// for single-segment repositories; a reference with neither tag nor digest
// defaults to `:latest`.
func ParseReference(s string) (Reference, error) {
	if strings.TrimSpace(s) == "" {
		return Reference{}, fmt.Errorf("oci: empty image reference")
	}

	remainder := s
	var digest string
	if i := strings.LastIndex(remainder, "@"); i >= 0 {
		digest = remainder[i+1:]
		remainder = remainder[:i]
		if err := validateDigest(digest); err != nil {
			return Reference{}, err
		}
	}

	// Split the registry off the first path segment. A first segment is a
	// registry only if it looks like a host (has a '.' or ':' port, or is
	// localhost); otherwise it's part of the repository on Docker Hub.
	registry := defaultRegistry
	name := remainder
	if i := strings.IndexByte(remainder, '/'); i >= 0 {
		if first := remainder[:i]; strings.ContainsAny(first, ".:") || first == "localhost" {
			registry = first
			name = remainder[i+1:]
		}
	}

	// A ':' after the last '/' introduces the tag (a ':' before it is a
	// registry port, already split above).
	tag := ""
	if colon := strings.LastIndexByte(name, ':'); colon > strings.LastIndexByte(name, '/') {
		tag = name[colon+1:]
		name = name[:colon]
	}
	if name == "" {
		return Reference{}, fmt.Errorf("oci: reference %q has no repository", s)
	}
	if registry == defaultRegistry && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	if tag == "" && digest == "" {
		tag = defaultTag
	}
	return Reference{Registry: registry, Repository: name, Tag: tag, Digest: digest}, nil
}

// APIHost returns the host the registry v2 API is served from. Docker Hub's
// canonical name (docker.io) differs from its API host (registry-1.docker.io).
func (r Reference) APIHost() string {
	if r.Registry == defaultRegistry {
		return dockerAPIHost
	}
	return r.Registry
}

// ManifestRef is the digest if pinned, else the tag — the {reference} path
// segment in `/v2/{repo}/manifests/{reference}`.
func (r Reference) ManifestRef() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

// String renders the canonical reference (registry/repo[:tag][@digest]).
func (r Reference) String() string {
	var b strings.Builder
	b.WriteString(r.Registry)
	b.WriteByte('/')
	b.WriteString(r.Repository)
	if r.Tag != "" {
		b.WriteByte(':')
		b.WriteString(r.Tag)
	}
	if r.Digest != "" {
		b.WriteByte('@')
		b.WriteString(r.Digest)
	}
	return b.String()
}

// validateDigest enforces the sha256:<64 lowercase hex> form we support.
func validateDigest(d string) error {
	hex, ok := strings.CutPrefix(d, digestAlgo)
	if !ok {
		return fmt.Errorf("oci: unsupported digest %q (want sha256:...)", d)
	}
	if len(hex) != digestHexLen {
		return fmt.Errorf("oci: digest %q has %d hex chars, want %d", d, len(hex), digestHexLen)
	}
	for i := 0; i < len(hex); i++ {
		if !isLowerHex(hex[i]) {
			return fmt.Errorf("oci: digest %q has non-hex char %q", d, hex[i])
		}
	}
	return nil
}

// isLowerHex reports whether c is a lowercase hex digit (0-9, a-f).
func isLowerHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
