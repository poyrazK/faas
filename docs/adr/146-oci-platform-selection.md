# ADR-146 · OCI platform selection for the x86 compute fleet

- **Status:** accepted
- **Date:** 2026-09-05
- **Scope:** `pkg/oci`, `pkg/imaged`, `gregale doctor --image`
- **Related:** ADR-021 (image errors), ADR-054 (signatures), ADR-062 (registry credentials), ADR-136 (container compatibility)
- **Supersedes:** the registry client's restriction to platform-specific image manifests

## Context

Common image tags identify OCI indexes or Docker manifest lists, including images
built with buildx that contain attestation descriptors. Requiring customers to
find the amd64 child digest manually prevents these references from working even
though the existing x86 compute fleet can execute their Linux/amd64 image.

Digest resolution also serves signature verification. Replacing an index digest
with its child globally would change the signature subject. Reading a mutable tag
again after verification could select content that was not verified.

## Decision

The registry client shares one resolver between preflight and deployment. It reads
the requested object once, hashes its content, verifies any supplied digest pin,
and returns both the immutable source reference and executable image reference.
For an index, it selects a unique Linux/amd64 child, verifies the descriptor's SHA256
digest and size, verifies the config digest, and checks its declared platform.
Single-image inputs also undergo content and platform validation in this resolver.
Pinned manifest reads and pinned `PullDigest` calls verify the response body.

The production target is Linux/amd64 independently of the CLI host. Empty variants
and `v1` are accepted; higher amd64 variants and additional requirements in platform
descriptors are rejected. Unknown media types, missing platform descriptors, and
incompatible platforms are skipped. Duplicate identical descriptors are accepted;
distinct compatible image digests are an ambiguity error with child-pinning advice.

Only direct image children are supported. The [OCI image-index specification](https://github.com/opencontainers/image-spec/blob/main/image-index.md)
recommends nested-index support and first-match selection. We deliberately defer
recursive traversal and reject ambiguous selection because this fleet has one
baseline target and no CPU-feature scheduling. There is no new traversal quota,
architecture selector, scheduler capability, ARM execution, or emulation.

`PullDigest` continues to identify the original object, including an index.
Imaged resolves first, verifies required signatures against the immutable source,
and uses the selected child reference for subsequent app config and manifest reads
in both build paths. Layer descriptors therefore come from that child. Offline
pullers without the additive `ImageResolver` interface keep their existing behavior.
The registry's existing authentication and egress-policy machinery handles resolution.

Preflight reports the input, source and selected references. Deployment preserves
the original `ImageDigest` field and records source and child references in structured
imaged logs; this change adds no database columns or deployment API fields.

## Consequences and validation

Customers can use multi-platform tags or index pins directly on the x86 fleet.
The two-drive layout, above-base layer calculation, snapshot behavior, and existing
runtime validation remain in place. Platform selection does not waive base-image
compatibility or establish that a container starts successfully.

Tests cover OCI/Docker indexes, descriptor order, attestations, missing and ambiguous
platforms, duplicate descriptors, unsupported variants/nesting, content tampering,
authentication on metadata requests, and no layer downloads during inspection.
Imaged tests exercise child-reference propagation through the full build path and
source-index signature verification, including rejecting a signature over a different
digest. Existing single-image, preflight, signing and imaging tests remain applicable.
