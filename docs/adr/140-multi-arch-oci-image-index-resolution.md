# ADR-140 · Multi-arch OCI image-index resolution

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task C.1 (multi-arch image-index resolution). M-3 of the five-Mega-PR plan.

## Context

Today the consumer-side OCI pull path **rejects every manifest list /
image index** at three layers:

1. `pkg/oci/manifest.go:90-98` — `PullManifest` rejects
   `application/vnd.oci.image.index.v1+json` and
   `application/vnd.docker.distribution.manifest.list.v2+json` with
   `ErrImageManifestInvalid`. The error message says "is a manifest
   list/index, not an image manifest; digest-pinned to a single-arch
   image is required".
2. `pkg/oci/registry.go:342-355` — `fetchManifestWithAuth` (the
   transport-level caller) enforces the same rejection.
3. `pkg/oci/errors.go:44-47` — `ErrImageManifestInvalid` is the single
   sentinel behind both call sites.

The `manifestAccept` header at `pkg/oci/registry.go:107-112` already
**advertises** both index media types in the `Accept` header — the
registry returns the index body, we just refuse to parse it.

This blocks every public registry image that ships as an index:
`gcr.io/distroless/static-debian12`, `alpine:latest`, `node:22`,
`python:3.12-slim`, the lot. Even operators who pinned
`FAAS_BUILDER_BASE_REF` to a digest end up blocked if the digest is
itself an image-index digest (today's check at `cmd/imaged/main.go:631`
allows that — `isDigestPinned` is a regex, not a content-shape check).

The platform's build fleet (`pkg/imaged/base.go:23-31`) hard-codes
single-arch refs at `ghcr.io/poyrazk/{runner-node22,…,base-minimal,…}`
which happen to be digest-pinned per-arch — so the platform's own
base-image pull has never tripped this. The constraint is only
exercised by **customer** image refs.

ADR-114 ("builder-base multi-arch pipeline") proves the publisher
side can do multi-arch: builder-base is published for both
`linux/amd64` and `linux/arm64` to GHCR via
`.github/workflows/images.yml`. The consumer side is the gap.

M-3 closes the consumer gap so every public registry image can be
deployed; M-4 workstream E revisits per-arch snapshot re-keying when
the arm64 fleet ships.

## Decision

### 1. Replace the three-layer rejection with a host-arch-aware platform walk

When `PullManifest` / `PullManifestWithAuth` resolves to an
`application/vnd.oci.image.index.v1+json` or
`application/vnd.docker.distribution.manifest.list.v2+json` body, the
puller **walks** `Manifests[]` and selects the descriptor whose
`Platform.OS="linux"` AND `Platform.Architecture=<host-arch>`. The
selected descriptor's `Digest` becomes the new manifest ref; the
same `getManifest` is recursively invoked with that ref. The walk is
**bounded to two hops** — a manifest list whose selected entry is
itself a list is rejected as `ErrImageManifestInvalid` (defensive; real
indexes are one level deep).

```go
// pkg/oci/manifest.go (commit 3)
type Platform struct {
    OS           string `json:"os"`
    Architecture string `json:"architecture"`
    Variant      string `json:"variant,omitempty"`
}

type IndexEntry struct {
    MediaType string   `json:"mediaType"`
    Digest    string   `json:"digest"`
    Size      int64    `json:"size"`
    Platform  Platform `json:"platform"`
}

type PlatformMatcher func(Platform) bool

func DefaultPlatformMatcher(goarch string) PlatformMatcher {
    return func(p Platform) bool {
        return p.OS == "linux" && p.Architecture == goarch && p.Variant == ""
    }
}

type Index struct {
    SchemaVersion int          `json:"schemaVersion"`
    MediaType     string       `json:"mediaType"`
    Manifests     []IndexEntry `json:"manifests"`
}
```

The matcher lives on `RegistryClient` as a constructor-injected field
— the `Puller` / `AuthPuller` / `ManifestPuller` / `AuthManifestPuller`
interfaces gain NO new methods. Interface seams stable; test doubles
carry on.

### 2. Host arch from `runtime.GOARCH` with `FAAS_BUILDER_ARCH` operator override

Build-time `runtime.GOARCH` is the default value. On multi-box hosts
(the `isNamedHost` flag, sibling to `FAAS_BUILDER_BASE_REF`'s gate at
`cmd/imaged/main.go:631`), the operator MUST set `FAAS_BUILDER_ARCH`
explicitly — `amd64` or `arm64`, no bare tags. On single-box dev
hosts the `runtime.GOARCH` default is acceptable.

The gate shape is duplicated from `builderBaseRefFromEnv`:

```go
// cmd/imaged/main.go (commit 3)
func builderArchFromEnv(isNamedHost bool) (string, error) {
    raw := os.Getenv("FAAS_BUILDER_ARCH")
    if raw == "" {
        if isNamedHost {
            return "", errors.New("imaged: FAAS_BUILDER_ARCH required on multi-box hosts")
        }
        return runtime.GOARCH, nil
    }
    switch raw {
    case "amd64", "arm64":
        return raw, nil
    default:
        return "", fmt.Errorf("imaged: FAAS_BUILDER_ARCH must be amd64 or arm64, got %q", raw)
    }
}
```

`FAAS_BUILDER_BASE_REF` digest pinning (ADR-021 D4) is unaffected —
the two envs are independent. The arm64 fleet bring-up is M-4; M-3
ships the matcher code path so when M-4 lights up `FAAS_BUILDER_ARCH=arm64`
on real boxes, every consumer call site already works.

### 3. `ErrImageManifestInvalid` semantics tighten

The existing error comment at `pkg/oci/errors.go:44-47` lists "is a
manifest list" as one of the failure modes. After M-3 the top-level
index rejection is gone; the `ErrImageManifestInvalid` clause now
applies only when:

- the manifest body cannot be parsed (malformed JSON),
- the matched descriptor's digest fails to fetch (registry 404),
- the walk exceeds depth 2 (defensive),
- `runtime.GOARCH` returns an unknown arch on a host where the operator
  has not set `FAAS_BUILDER_ARCH`,
- the manifest body is single-platform but its `mediaType` is
  neither OCI nor Docker (genuine malformed manifest).

The error message gains a "did you set `FAAS_BUILDER_ARCH`?" hint
where applicable, since unknown-arch is the most likely operator error.

### 4. Digest-pinned pulls skip the walk

Today-equivalent: a per-arch digest pull bypasses the index walk
entirely (the digest IS the single-platform manifest, no walk needed).
`PullDigest` already does the resolve-ref-to-digest hop; the multi-arch
walk runs inside `getManifest`, which is the shared path. Digest-pinned
tests in `pkg/oci/manifest_test.go::TestPullManifest_DigestPinnedSkipsWalk`
pin this boundary.

## Consequences

### Positive

- Every public-registry image (`alpine:latest`,
  `gcr.io/distroless/static-debian12`, `node:22`, …) can be pulled
  by the consumer-side without operator intervention.
- The publisher-side multi-arch capability (ADR-114) becomes
  symmetric — both sides of the registry contract now speak multi-arch.
- `Puller` interface seams are unchanged; existing test doubles and
  consumers don't reflow.
- `FAAS_BUILDER_ARCH` is one constant + one helper, sibling to
  `FAAS_BUILDER_BASE_REF`; the operator surface is uniform.

### Negative

- **Operator surface grows by one env.** Operators running multi-box
  fleets must now set both `FAAS_BUILDER_BASE_REF` (digest-pinned) and
  `FAAS_BUILDER_ARCH` (amd64/arm64). Documented in
  `docs/operator/sealed-env.md` (M-3 follow-up).
- **arm64 fleet is M-4.** The matcher code path is shipped in M-3, but
  without the arm64 fleet there's no end-to-end CI exercise beyond the
  unit tests + Lima arm64 runner. The Lima arm64 nested-virt runner is
  the only place `FAAS_BUILDER_ARCH=arm64` is exercised end-to-end in
  M-3; the metal acceptance gate runs on amd64 today.
- **Per-arch snapshot keying deferred to M-4.** Today's snapshot key
  is per-deploymentID with no arch axis
  (`schema.sql:1999` `snapshots.fc_version`). Multi-arch deployments
  share a snapshot key across arch bumps; on M-4 fleet bring-up, an
  arch-aware GC may evict a working snapshot. M-3 mitigation: the
  matcher's arch is recorded in the deployment metadata so M-4 can
  rebuild the snapshot index without re-walking the registry.

### Neutral

- `manifestAccept` already advertised both media types; no header
  change.
- Existing test fixtures that pinned the "rejects manifest list"
  behaviour need to be updated to pin the new "walks to per-arch
  manifest" behaviour. Same test files, semantically inverted cases.
- `DefaultPuller` (`pkg/oci/puller.go:172-200`, the offline test
  default) keeps its echo-only behaviour; multi-arch is exercised by
  `httptest` servers in `manifest_test.go`.

## Rejected alternatives

- **Defaulting to amd64 when `runtime.GOARCH` returns an unknown
  arch.** Rejected — silent fallback is exactly the bug class M-1 +
  M-2 spent commits fighting. Fail-loud with `ErrImageManifestInvalid`
  + hint pointing at `FAAS_BUILDER_ARCH`.
- **Per-customer platform override (`faas deploy --platform linux/arm64`).**
  Rejected — operator-level axis only on M-3; per-app axis is M-4
  workstream F (Compose import subset). Adding a customer-controlled
  axis in M-3 would require admission logic + per-deployment
  metadata + snapshot re-keying, which is more surface than M-3 needs.
- **Manifest-list resolution via `go-containerregistry` / `crane`.**
  Rejected — `pkg/oci/registry.go` is hand-rolled registry v2 (HTTP +
  Bearer-token dance). Adopting `go-containerregistry` is a wider
  refactor that touches every consumer; deferred to a future ADR if
  the hand-rolled path becomes unmaintainable. The 2-hop walk is ~80
  LOC; the dependency would cost more than it saves.
- **Always pre-fetching all per-arch manifests at pull time.** Rejected
  — wastes bandwidth; today the platform only ever boots one arch per
  box. The walk is on-demand.
- **Allowing `linux/arm64` as a non-`linux/amd64` default on amd64
  fleet.** Rejected — `runtime.GOARCH` is the truthful source; the
  fleet operator MUST opt in via `FAAS_BUILDER_ARCH` to ship arm64.

## Cross-references

- **Forced by Mega-PR #3 (M-3) of issue #1186**:
  - `pkg/oci/manifest.go` (commit 3) — `Platform`, `IndexEntry`,
    `Index`, `PlatformMatcher`, `DefaultPlatformMatcher`; walk in
    `PullManifest`; 2-hop bound
  - `pkg/oci/registry.go` (commit 3) — drop `isImageManifest` reject;
    `matcher` field on `RegistryClient` constructor
  - `pkg/oci/errors.go` (commit 3) — update `ErrImageManifestInvalid`
    comment (multi-arch clause now refers to depth>2 only)
  - `cmd/imaged/main.go` (commit 3) — `FAAS_BUILDER_ARCH` env +
    `builderArchFromEnv` helper; threaded into `RegistryClient`
    constructor
  - `pkg/oci/manifest_test.go`, `cmd/imaged/main_test.go` (commit 3) —
    6 new tests

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-021 (digest pinning, PR #35): `FAAS_BUILDER_BASE_REF` digest
    pinning stays; M-3 adds `FAAS_BUILDER_ARCH` as a sibling, not a
    replacement. The two gates are independent.
  - ADR-114 (builder-base multi-arch pipeline, publisher side):
    M-3 makes the consumer side symmetric.
  - ADR-005 (cold boot must always work): unaffected. The walk only
    changes which manifest is selected; cold-boot paths downstream
    are identical.
  - ADR-009 (identical inner network world 10.0.0.2/30): unaffected.
    Snapshot reuse invariants are preserved by per-deploymentID keys.

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-3 of the five-Mega-PR plan. This ADR
    closes sub-task C.1 (multi-arch).
  - **#600** (digest pinning) — informational; M-3 doesn't add digest
    pinning enforcement but lays the wire field.
  - **PR #1190 (M-1)** — extended `oci.ParseConfig` to handle the
    nested-OCI envelope. M-3's walk uses the same `oci.ParseConfig`
    for the per-arch manifest body; no parser change.
  - **PR #1202 (M-2)** — `instances.mode` widening lets the engine
    stamp per-arch metadata; not directly consumed by M-3 but the
    field exists for M-4's per-arch snapshot index.

- **Spec sections**:
  - §4.4 (OCI image ingestion) — the section this ADR amends.
  - §11 (security hardening) — egress denylist still applies
    (RFC1918, link-local, metadata, CGN, SMTP); the walk doesn't
    change which registries are reachable.
  - §14 (delivery plan) — M-3 ships as part of M8.
  - §3 ADR template — followed verbatim (this file).

- **Tests pinning this ADR**:
  - `pkg/oci/manifest_test.go::TestPullManifest_MultiArchIndexResolvesLinuxAMD64`
  - `pkg/oci/manifest_test.go::TestPullManifest_MultiArchIndexResolvesLinuxARM64`
  - `pkg/oci/manifest_test.go::TestPullManifest_NestedIndexRejects`
  - `pkg/oci/manifest_test.go::TestPullManifest_DigestPinnedSkipsWalk`
  - `cmd/imaged/main_test.go::TestBuilderArch_MultiBoxRequiresEnv`
  - `cmd/imaged/main_test.go::TestBuilderArch_SingleBoxDefaultsToGOARCH`
