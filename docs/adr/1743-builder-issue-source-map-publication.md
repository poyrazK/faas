# ADR-1743: Publish final Node build source maps with build completion

Date: 2026-10-17

Status: Accepted

Extends: ADR-145 (build-completion handoff), ADR-1456 (deployment artifacts),
ADR-1742 (prebuilt CLI publication)

## Decision

A source-root `gregale.issue-source-maps.json` opts production Railpack Node
builds into automatic publication, including connected-repository and selected
workspace builds. Version 1 names one canonical final-image directory under
`/app`. Builderd reads the frozen uploaded archive before customer commands run;
the selected source root and transport wrapper follow framework detection.
Malformed, duplicate, oversized and nonregular configuration fails closed.
Other frameworks and watch-mode builds reject the opt-in.

Pass the reviewed configuration and archive digest in additive, optional builder
manifest fields. For opted-in builds, one BuildKit solve exports both the existing
OCI image and its final filesystem to a second VM-local directory. Customer build
commands still run exclusively in the builder microVM. Discover maps after
BuildKit stops; never read intermediate source/build directories as final output.
The raw filesystem is outside `/build/out` and never exported to the host.

Discover `.js`, `.mjs` and `.cjs` bundles deterministically; skip symlinks,
`node_modules` and `.git`. Use the last complete source-map annotation, or a
neighboring `.map` for hidden maps. Only relative local map paths are supported;
there is no remote fetch, inline map, indexed map or source-code execution.
Rebase map-local relative roots against the bundle, then use the existing
bounded flat-map compiler to strip source contents/extensions. Empty discovery
is an error. All bounds live in `pkg/api/limits.go`: 4,096 visited entries,
16 MiB per bundle, 64 MiB aggregate bundles, existing map/count/decoded-entry
bounds, 4 KiB config and a bounded handoff/cache envelope.

The private sanitized sidecar binds build ID, deployment ID, source digest and
the exact OCI image SHA-256 to the publication request. Builderd verifies all
bindings and the selected directory before completion. Missing sidecars from an
older guest fail the build instead of accepting incomplete symbolication coverage.
The artifact cache separates opt-in recipes and retains sanitized maps plus
their canonical digest and OCI digest. Missing, malformed, oversized, linked or
mismatched metadata causes a cache miss for opted-in builds. Cache hits publish
against the new deployment's identity, never the producing deployment's ID.

An optional PostgreSQL build-completion method publishes source maps in the
same transaction as the running-claim fence, artifact metadata, provenance and
build success. Lock the app before the deployment/build; share the existing
artifact ownership, immutable digest and ready-set quota checks. Builderd writes
only operational build evidence, not customer deployment intent. Imaging cannot
see a succeeded build before its maps commit. Existing imaging/readiness/traffic
policy remains authoritative; no new direct daemon calls or promotion bypass.
No migration, public HTTP schema, SDK generation or additional credentials are
needed. The memory store does not advertise this PostgreSQL-only capability.

Short transaction interruptions retry the same completion with the same export,
without rebuilding or creating another deployment. After an uncertain successful
commit, the original claim is fenced; the durable image-work poll recovers lost
notifications. Replaying a completed claim cannot restore subsequently retired
maps. Quota/conflict failures leave both build success and image handoff uncommitted
and builderd marks the claimed deployment failed with the publication error.

## Consequences and limits

Publication is a prerequisite for opted-in build success, and therefore for
imaging, readiness and production promotion. Startup uses the existing deployment
capture opt-in; this feature does not enable exporters or grouping automatically.
The bundle SHA verifies the exact final build result, not the semantic accuracy of
a customer-authored map. Artifacts remain subject to explicit retirement and the
existing 32-ready-deployment cap. Do not combine automatic final-build collection
with CLI prebuilt publication for the same deployment: immutable-manifest conflicts
are preserved.

The second exporter uses additional ephemeral guest disk and I/O within the
existing builder VM limits; it is opt-in. It does not change VM creation, stop,
snapshot or export lifecycle. Builderd and the builder guest must both include
this feature before enabling it. Deployment to older builderd versions would
ignore the new source file, so operators must finish that rollout first.

Recovery covers transaction interruption and committed-image handoff. As in the
existing build pipeline, a builderd process death before completion commits does
not resume a stopped VM's export; the running-build reaper remains authoritative.
Durable resumable precommit export recovery is separate work. Exhausted transaction
retries retain the existing running-claim/error/reaper behavior; no success or
promotion is advertised.

## Validation

Portable tests cover final-byte hashing, deterministic discovery, hidden/annotated
maps and rebasing, sanitization, path/link/FIFO refusal, bounds and empty output;
guest sidecar binding/private permissions and one-solve exporter construction;
frozen archive/workspace configuration, wrong/stale/missing handoffs, cache recipe
separation and malformed cache metadata. Real PostgreSQL tests cover atomic
success, wrong ownership, stale/canceled claims, immutable conflicts, capacity,
deferred commit failure, retry without another VM/deployment, cached publication
to a new deployment and lost-notification recovery. Dedicated native builder
acceptance is required before production rollout of the dual-export path.

### Local evidence — 2026-10-17

Go 1.26.9 verification passed 37 top-level Issues cases, 240 builder cases and
448 guest cases, including three real-PostgreSQL acceptance suites. The database
suites exercise seven completion/rollback fences, ordinary completion without
maps, a failed publication transaction followed by recovery using one VM result,
cache publication to another deployment, a visible terminal conflict and lost
notification recovery. All artifact/configuration/discovery tests passed.

The guest run used the fixtures' normal `022` file-creation mask and excluded
`TestDiscoverSidecarDevicesCarriesWorkloadNames` and
`TestDiscoverSidecarDevicesReadsOptimizedDriveRoster`: both assume a virtio-blk
base and fail unchanged against the parent code on this pmem-root host. Eleven
existing guest skips and two builder host-fixture skips remain. Earlier signal
startup timing failures passed in isolation and in the final guest rerun. No
repository tests, assertions, CI settings or production permission behavior were
changed to accommodate this environment.

Uncapped package lint passed for Issues, guest-init and builderd. Production
state changes passed diff-scoped lint; the complete production-only state lint
reports two unchanged helpers used only by their tests
(`graphActivationEvidence` and `githubActionsRepositoryFromSubject`). Public
spec/DTO parity, documentation links, ADR uniqueness and the metal-tagged builder
transport tests passed. The static guest-init binary and metal-enabled builderd
daemon both linked successfully.

The native builder acceptance gate compiles with the new generated-map case,
but its existing opt-in guard skips execution here; `/dev/kvm` is unavailable.
Real builder-VM dual-export, image conversion, boot and leak qualification remain
required on the dedicated native host before rollout. Nothing was deployed.
