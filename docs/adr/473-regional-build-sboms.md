# ADR-473: Regional build SBOM publication and reads

Status: Accepted; internal rollout pending native acceptance
Date: 2026-10-02
Supersedes the unprefixed SBOM key for regional apps in ADR-038.

## Context

ADR-460 lets a peer worker materialize a source build from regional storage.
Its CycloneDX SBOM still used the legacy global store, and the API accepted
any relative provenance key. Placement therefore did not protect this build
metadata. A failed rematerialization could also clear an existing SBOM reference.

## Decision

Imaged derives the SBOM key from the build ID and the app's immutable compute
region: `regions/<region>/sboms/<build-id>.cdx.json`. Empty placement retains
`sboms/<build-id>.cdx.json`. All layer and full-rootfs assembly paths share this
key derivation. Rootfs publication requires an explicit route for a regional
key and uses the existing storage backend; SBOM failures remain observational
and do not fail an otherwise valid deployment.

Imaged stamps provenance only after publication succeeds. Empty scan/upload
results preserve any existing reference in both state implementations, including
provenance upserts and build completion. PostgreSQL and MemStore reject new
regional references with the wrong region, build ID, namespace or path alias.
Database triggers also protect direct writes and prevent rebinding a published
regional SBOM's build or app identity. Historical unchanged references survive
unrelated provenance updates; migration replay does not move or delete bytes.

Apid follows Build → Deployment → App → Account ownership before reading. The
persisted reference must exactly equal that build's canonical key in its app's
region. Reads require the explicit route before touching storage or a cache.
The legacy filesystem fallback accepts only empty placement. Missing routes,
objects, scans, or malformed references return 503; another account sees 404.
The API and SDK response shape remains raw CycloneDX JSON.

App deletion already inventories SBOM provenance alongside runtime artifacts.
It retains the complete regional key and uses the configured artifact router
for deletion, preserving other regions' objects. This change keeps that lifecycle
contract and tests its projection. It does not add a separate SBOM retention loop;
publication without a provenance reference can still leave an orphan until an
artifact-specific orphan collector is implemented.

## Compatibility and rollout

The additive migration installs guards, with replay-safe function and trigger
replacement. Downgrade refuses while regional SBOM references exist. Drain
regional materializations before deploying the migration, imaged and apid together.
Configure the app's route on each publisher, API reader and deletion worker.
Legacy apps keep their keys. Regional apps with historical global SBOM references
return unavailable until their SBOM is republished; there is no global-store
fallback or automatic claim that old bytes were moved into a region.

The guarded `TestNativeRegionalBuildExportAcceptance` also requires Syft on the
materializing peer and verifies the regional provenance key and authenticated API
retrieval after the producer is disabled. Run it on the dedicated native Linux
x86_64/KVM fleet, followed by leakcheck on each worker. Portable tests and Linux
cross-compilation do not qualify the native lifecycle. Admission remains internal
and default-off until the operational gates are green.

## Remaining work

Regional runtime log archive publication and history reads, persistent services,
ingress/global identity and control-plane independence remain open. PostgreSQL
control-plane metadata remains shared; regional artifact routing alone does not
promise end-to-end data residency.
