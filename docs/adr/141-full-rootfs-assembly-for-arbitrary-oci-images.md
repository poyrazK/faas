# ADR-141 · Full-rootfs assembly for arbitrary OCI images

- **Status:** proposed
- **Date:** 2026-08-30
- **Deciders:** Gregale platform team
- **Forks:** [Epic #1186 — make Gregale a first-class OCI container platform](#1186), sub-task C.2 (full-rootfs assembly for arbitrary OCI images). M-3 of the five-Mega-PR plan.

## Context

The two-drive rootfs scheme (spec §4.6) is the load-bearing economics
behind Gregale's 130 MB/sandbox disk budget: `drive0` is a shared,
content-addressed, read-only **base rootfs** (one of
`ghcr.io/poyrazk/{runner-node22,runner-python312,base-minimal,…}`);
`drive1` is a per-app writable **app layer** ext4 carrying only the
OCI layers **above the base** plus the platform's `app.json` manifest.
guest-init assembles them with overlayfs at boot.

The constraint that makes this work is `oci.LayersAboveBase`
(`pkg/oci/image.go:108-129`):

> Given an app image built FROM one of our base images, return the
> diff_ids that sit ABOVE the base. Errors when the app's diff_ids
> are not a strict prefix extension of the base's.

A customer image built `FROM gcr.io/distroless/static-debian12` or
`FROM alpine` fails this prefix check — its `diff_ids` have nothing
in common with our base's. The handler surfaces the failure as
`fmt.Errorf("oci: app diff_ids are not a prefix extension of base diff_ids …")`
and the deployment is rejected.

This blocks the entire distroless + scratch ecosystem:

- `gcr.io/distroless/static-debian12` — Google ships rootless containers
  with `USER nonroot` (uid 65532). M-3 wires this end-to-end.
- `alpine:latest` — most common small base for hand-built images.
- `FROM scratch` — Go binaries, Rust binaries, statically-linked ELF.
- `node:22-slim`, `python:3.12-slim` — public Docker Hub indexes.

The platform's `pkg/rootfs/build.go::Builder.Build` currently exposes
a single path that always assumes `LayersAboveBase` succeeds. There
is no escape hatch.

ADR-136 §Forced follow-ups explicitly deferred this to M-3:

> Cross-drive whiteouts (overlayfs char-device to hide base-dir
> victims). Rejected for M-1 — addresses the `layer.go:21-24` TODO
> but the underlying two-drive scheme is unchanged. M-3 problem;
> M-3 lands full-rootfs assembly which subsumes the issue.

M-3 ships the full-rootfs path while **preserving** two-drive for
`FROM runner-*` customers (the load-bearing 130 MB economics). The
two paths share storage keys and overlay semantics; the dispatch is
a typed-sentinel gate at `imaged.buildImageLayer` (commit 6).

## Decision

### 1. New `BuildInput.FullRootfs bool` field on `pkg/rootfs/build.go::BuildInput`

When `false` (default), today's two-drive path runs unchanged. When
`true`, the builder **skips the base pull entirely** and assembles
drive1 from **ALL layers** in the app image (bottom-to-top),
producing a self-contained ext4 sized for the full image (no shared-
base compression, ~30-50 MB for `alpine:latest` vs ~3 MB for the
above-base delta of a runner-* customer).

```go
// pkg/rootfs/build.go (commit 5)
func (b *Builder) BuildFullRootfs(ctx context.Context, in FullRootfsInput) (BuildResult, error)

type FullRootfsInput struct {
    Layers          []io.Reader
    Manifest        api.AppManifest
    GuestInitPath   string
    Plan            api.Plan
    Storage         storage.StorageBackend
    StorageKey      string
    SBOMRun         func(context.Context) ([]byte, error)
    SBOMStorageKey  string
}
```

Internally `BuildFullRootfs` reuses the package-private helpers
already extracted by the two-drive path: `applyLayers(staging, …)`,
`InjectManifest(staging, manifest)`, `InjectGuestInit(staging, …)`,
and `mkfs(staging, plan, storage, key)`. The function signature
differs from `Build` only in the input struct shape (`FullRootfsInput`
vs `BuildInput`); the mkfs + Storage.Put pipeline is byte-identical.

### 2. Tri-state dispatch in `imaged.buildImageLayer` (commit 6)

Today `LayersAboveBase` returning an error fails the deployment.
After M-3 the dispatch is **tri-state**, recorded on
`state.Deployment`:

| Field | Type | Default |
|---|---|---|
| `FullRootfsAllowAuto` | `bool` | `true` on Hobby+; `false` on Free |
| `FullRootfsOverride` | `*bool` | `nil` (honor auto) |

Dispatch table at `pkg/imaged/handler.go::buildImageLayer` (the
de-facto six-row matrix; rows collapse cleanly when `LayersAboveBase`
succeeds):

| Plan | Override | AllowAuto | `LayersAboveBase` outcome | Result |
|---|---|---|---|---|
| Hobby+ | nil | true | succeeds | two-drive (today) |
| Hobby+ | nil | true | fails | **full-rootfs (auto)** |
| Hobby+ | &false | true | fails | today failure (force-off) |
| Hobby+ | &true | true | fails | full-rootfs (force-on) |
| Free | nil | false | fails | today failure (Free no auto) |
| Free | &true | false | fails | full-rootfs (Free explicit) |
| Free | &false | false | fails | today failure |

The auto path on Hobby+ is the load-bearing UX improvement: a customer
who `faas deploy`s `alpine:latest` gets a working deployment without
knowing about full-rootfs. Free plan customers must opt in explicitly
via `--full-rootfs` because the storage economics (40 MB vs 3 MB)
don't fit the Free tier's storage envelope.

### 3. Sentinel gate via `oci.ErrLayersNotAboveBase` (commit 4)

`pkg/oci/image.go:117` `LayersAboveBase` returns the new typed
sentinel `ErrLayersNotAboveBase` instead of a plain `fmt.Errorf`. The
dispatch in `buildImageLayer` uses `errors.Is(err, oci.ErrLayersNotAboveBase)`
— never a string match — so future error wrapping (logging, telemetry)
doesn't silently bypass the gate.

`IsImageTerminal` widens to include the new sentinel; `SentinelToCode`
maps it to `api.CodeImageManifestInvalid` (still 422 — same shape as
a malformed manifest; full-rootfs is the auto-recovery path, not a
4xx the customer must manually fix unless they chose `&false`).

### 4. Storage keys and snapshot sharing

`BuildInput.Storage` and `StorageKey` stay **shared** with the
two-drive path. Full-rootfs images publish under the same
per-deploymentID key shape, just sized for the full image. The shared-
base key (drive0) is NOT consulted on the full-rootfs path; cold
boot attaches the single self-contained drive as drive0+vda (the
guest sees drive0 as root and drive1 as a no-op).

Snapshot keying is per-deploymentID (today, `snapshots.fc_version`
at `schema.sql:1999`). The full-rootfs snapshot is bigger but lives
under the same key. M-4 will revisit when arm64 fleet lands; M-3
mitigation: the matcher's arch is recorded in the deployment
metadata so M-4 can rebuild the snapshot index without re-walking
the registry.

### 5. Sized budget

Full-rootfs ext4 = `LayerUncompressedBytes × 1.3` (vs. two-drive's
`AboveBase × 1.3`). For `alpine:latest` (5 layers, ~30 MB
uncompressed) the produced drive is ~40 MB vs two-drive's ~3 MB app
layer. Snapshot cache still works (the bigger drive simply yields a
bigger snapshot blob).

The per-plan cap `MaxFullRootfsLayerBytes` (commit 9) bounds the
deployment size: Hobby 256 MB / Pro 1 GB / Scale 4 GB. Builds
exceeding the cap are rejected with `ErrImageTooLarge` and a clear
message pointing at the cap + plan tier + docs URL.

## Consequences

### Positive

- **Every public-registry image** can be deployed. distroless,
  alpine, scratch, slim, you-name-it. The blocker moves from
  `LayersAboveBase` to consumer quota.
- **Auto-fallback on Hobby+** delivers a working deployment without
  the customer needing to know about full-rootfs. They just
  `faas deploy alpine:latest` and it works.
- **Free plan opt-in path** keeps the storage economics honest: a
  Free customer can't accidentally consume 40 MB of base storage
  without explicit `--full-rootfs`.
- **Per-deployment override** (`&true` / `&false`) gives a customer
  the choice to pin today's behaviour or force full-rootfs even on
  Free, without depending on platform defaults.
- **Two-drive is preserved verbatim** for `FROM runner-*` customers.
  The 130 MB/sandbox economics still hold; no silent regression on
  the load-bearing customer base.
- **Storage keys shape unchanged** — existing Storage backends,
  SBOM flows, and GC logic carry on.

### Negative

- **Storage cost grows proportionally to image uncompressed size.**
  Hobby/Pro/Scale customers see the cost surface via `MaxFullRootfsLayerBytes`.
  Documented in financial-model addendum (M-3 follow-up).
- **Auto-fallback on Hobby+ may surprise customers** who expected
  today-equivalent failure. Mitigated by `FullRootfsOverride=&false`
  opt-out per deployment.
- **Per-arch snapshot keying deferred** to M-4. Today's snapshot GC
  is per-deploymentID with no arch axis; arm64 fleet will need an
  arch-aware GC to avoid evicting working snapshots. Not in M-3.
- **Cross-drive whiteouts (the `layer.go:21-24` TODO) become moot**
  for the arbitrary-image path but **stay unresolved** for two-drive.
  M-4 workstream E.

### Neutral

- `Builder.Build` (the existing two-drive entrypoint) keeps its
  signature; M-3 only adds `BuildFullRootfs` as a sibling. Existing
  callers (the builderd output path) don't reflow.
- `LayersAboveBase`'s return value widens from `error` to
  `errors.Is(err, oci.ErrLayersNotAboveBase)` — every existing call
  site works (wrapping preserves `errors.Is`).

## Rejected alternatives

- **Silent flattening of two-drive for arbitrary images.** Rejected —
  every two-drive customer would silently lose snapshot economics.
  ADR-141 §Forced follow-ups calls out: if the prefix check passes,
  two-drive is mandatory.
- **Always-explicit `--full-rootfs` opt-in (no auto-fallback).**
  Rejected — adds friction; customers retrying after the
  `LayersAboveBase` failure have to remember the flag. Auto-fallback
  on Hobby+ is the better DX; the `&false` override preserves
  today's behaviour for customers who want it.
- **Auto-fallback on ALL plans including Free.** Rejected — Free
  plan's storage envelope (10 MB cap) doesn't fit full-rootfs
  economics (40 MB+ for `alpine`). Free customers must opt in
  explicitly; the cap is enforced even on explicit opt-in via
  `MaxFullRootfsLayerBytes[Free]=0`.
- **Two-drive with a "best-effort above-base" prefix scan.** Rejected
  — silently produces broken overlays when the prefix is partial.
  The strict-prefix check is what makes the 130 MB economics safe.
- **Per-image runtime override (set `FullRootfs` via OCI label).**
  Rejected — OCI labels aren't authenticated; an attacker-controlled
  label could pin the cheaper two-drive path on a malicious image
  that wasn't built FROM our base. The deployment-level override
  is authenticated via the customer's Gregale session.
- **Renaming the field `UseTwoDrive` (positive form) instead of
  `FullRootfs` (negative form).** Rejected — `FullRootfs` matches the
  M-3 PR description and the docs/financial/M3 addendum; renaming
  would force a wider wire change for negligible gain.

## Cross-references

- **Forced by Mega-PR #3 (M-3) of issue #1186**:
  - `pkg/oci/errors.go` (commit 4) — new typed `ErrLayersNotAboveBase`
    sentinel + `SentinelToCode` mapping
  - `pkg/oci/image.go:108-129` (commit 4) — `LayersAboveBase` returns
    `ErrLayersNotAboveBase` via `fmt.Errorf("%w: …")`
  - `pkg/imaged/handler.go::buildImageLayer` (commit 6) — tri-state
    dispatch table; six-row matrix above
  - `pkg/rootfs/build.go::BuildFullRootfs` (commit 5) — sibling to
    `Builder.Build`; reuses the same mkfs + Storage.Put pipeline
  - `pkg/state/types.go::Deployment` (commit 6) — additive widening
    with `FullRootfsAllowAuto bool` + `FullRootfsOverride *bool`
  - `pkg/api/limits.go` (commit 9) — `FullRootfsAllowedPlans`,
    `MaxFullRootfsLayerBytes`, `PlanMeetsFullRootfs`,
    default `FullRootfsAllowAuto` per plan

- **Loading constraints (existing ADRs this PR must not violate)**:
  - ADR-005 (cold boot must always work): the full-rootfs path
    cold-boots via `mkfs.ext4 -d` from a staging dir, no different
    from the two-drive path. Snapshots are still the cache.
  - §4.6 two-drive load-bearing (CLAUDE.md): preserved verbatim
    for `FROM runner-*` customers. The dispatch is a typed-sentinel
    gate — never a silent flattening.
  - ADR-019 (jailer uid 20000-29999): unaffected. Each instance gets
    its own uid regardless of full-rootfs vs two-drive.
  - ADR-053 (deploy overrides for OCI image deploys): M-3 lays the
    `FullRootfsOverride *bool` wire field; the operator override
    (`faas deploy --user postgres`) lands in M-4.
  - ADR-009 (identical inner network world 10.0.0.2/30): unaffected.
    Full-rootfs uses the same guest-init overlayfs assembly as
    two-drive (drive0 read-only root, drive1 the merged ext4).

- **Issue / PR relationships**:
  - **#1186** (parent epic) — M-3 of the five-Mega-PR plan. This ADR
    closes sub-task C.2 (full-rootfs assembly).
  - **#460** (ADR-053 PR-A persistence): `FullRootfsOverride *bool`
    adds to the operator-override surface; ADR-053's `apps.metadata`
    pattern carries the value forward.
  - **PR #1190 (M-1)**: `oci.LayersAboveBase` strict-prefix check
    is M-1's invariant; M-3 widens the failure path with a typed
    sentinel + auto-fallback.
  - **PR #1202 (M-2)**: `instances.mode` widening lets the engine
    stamp `mode='worker'` / `mode='service'` for non-request
    workloads. Full-rootfs is orthogonal — a full-rootfs `alpine`
    image can still be `request` mode today; the mode axis and the
    full-rootfs axis are independent.

- **Spec sections**:
  - §4.6 (two-drive rootfs) — the load-bearing constraint this ADR
    preserves verbatim.
  - §4.4 (OCI image ingestion) — the section this ADR amends.
  - §6.2 (invariants) — preserved verbatim. Full-rootfs images
    satisfy invariants 1-5 by construction (the assembled rootfs
    carries whatever the image declared).
  - §11 (security hardening) — egress denylist, jailer uid, cgroup
    scope unchanged.
  - §14 (delivery plan) — M-3 ships as part of M8.

- **Tests pinning this ADR**:
  - `pkg/oci/image_test.go::TestLayersAboveBase_NotPrefix_ReturnsTypedSentinel`
    (commit 4) — assert `errors.Is(err, oci.ErrLayersNotAboveBase)`
  - `pkg/oci/errors_test.go::TestSentinelToCode_LayersNotAboveBase`
    (commit 4) — pin the code mapping
  - `pkg/imaged/handler_image_build_test.go::TestBuildFullRootfsLayer_HappyPath`
    (commit 6) — synthetic scratch image → full-rootfs ext4 published
  - `pkg/imaged/handler_image_build_test.go::TestBuildFullRootfsLayer_DispatchesOnNotAboveBase`
    (commit 6) — matrix-driven over the six-row dispatch table
  - `pkg/imaged/handler_image_build_test.go::TestBuildFullRootfsLayer_StorageKeyShape`
    (commit 6) — assert storage key matches the two-drive shape
  - `pkg/api/limits_test.go::TestFullRootfsAllowedPlans_PlanMembership`
    (commit 9) — plan membership pinning
  - `pkg/api/limits_test.go::TestMaxFullRootfsLayerBytes_PerPlan`
    (commit 9) — per-plan byte cap pinning
  - `pkg/imaged/full_rootfs_metal_test.go::TestMetalFullRootfs_DistrolessStaticDebian12`
    (commit 10) — `gcr.io/distroless/static-debian12:latest`
    cold-boots, `whoami` reports `nonroot`
  - `pkg/imaged/full_rootfs_metal_test.go::TestMetalFullRootfs_AlpineLatest`
    (commit 10) — `alpine:latest` cold-boots, `cat /etc/os-release`
    reports alpine
  - `pkg/imaged/full_rootfs_metal_test.go::TestMetalFullRootfs_TwoDriveCustomerUnaffected`
    (commit 10) — `FROM runner-*` keeps two-drive (load-bearing
    invariant)
