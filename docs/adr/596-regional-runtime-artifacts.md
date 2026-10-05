# ADR-596: Regional runtime artifacts

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

ADR-591 and ADR-594 constrain app and managed-job execution to an immutable
region. Their runtime artifacts still used the shared legacy store. Placement
alone cannot provide regional artifact delivery or keep an artifact fetch from
implicitly using another region's store.

## Decision

New app layers, sidecar layers, materialized job images, and snapshot captures
use `regions/<compute_region>/<legacy_key>` when the workload selects a region.
Empty placement preserves legacy keys. Imaged persists the complete layer/image
reference, and schedd passes it through to vmmd. Memory, vmstate, captured drive,
and ext4 signatures retain the same namespace. Regional routing strips the
namespace only at the selected storage driver, preserving its OCI/GCS key format,
compression, immutable-capture protocol, and two-drive rootfs architecture.

`FAAS_STORAGE_REGIONS_FILE` optionally selects a strict JSON routing file with a
`regions` array. Each entry names a canonical region and one remote OCI or GCS
store. OCI entries carry an HTTP(S) registry origin, optional repository prefix,
timeout, and paired environment-variable names for credentials. GCS uses a bucket
and the existing ADC authentication. Unknown fields, duplicate regions, invalid
settings, missing named credentials, and duplicate storage namespaces fail
startup. Regional routes do not inherit local-prefix routing or the legacy
GCS-to-OCI migration fallback. Operators provision this file consistently on
artifact consumers and publishers before deploying regional workloads.

The existing backend remains responsible for unprefixed artifacts, platform
bases, kernels, source uploads, and build metadata. It preserves current local
and shared-fleet deployments. Already published references remain authoritative:
enabling routing does not move their data or reinterpret an old unprefixed key.
An absent regional route is an explicit error, including when no routing file
is configured; unknown regional keys never enter the legacy backend. A selected
store's absence or outage never triggers a lookup in another regional store.

One cache retains the current host-wide disk budget and uses complete regional
keys. Routing validation wraps the cache, so an old cache entry cannot bypass a
removed route, existence probe, local-path lookup, or prefetch locator. Sparse
copying and restore working-set families recognize regional snapshot namespaces.

Lifecycle inventory explicitly enumerates an artifact class across stores while
retaining its complete keys. Ordinary `List` preserves prefix semantics and
uses authoritative stores rather than the cache subset. Incomplete enumeration
fails the sweep instead of authorizing destructive cleanup from partial results.
Schedd's drift scan includes the regional namespace in object identity, so a
similarly named object in another store cannot satisfy a missing regional row.
Imaged's SQLC-backed GC projection now carries the committed rootfs key instead
of reconstructing a legacy layer name. OCI index reconciliation receives historical
deployment IDs only for the legacy store; new regional writes register their
repositories and empty regional indexes are initialized separately.

## Validation and rollout

Portable checks cover regional publication, OCI round trips and compression,
namespace/cache separation, missing-route and outage behavior, removal of cached
routes, signature key pairing, snapshot companion keys, prime RPCs, drift scans,
and app/job cleanup. PostgreSQL tests cover GC and deletion inventory with
committed regional references. Legacy storage and runtime suites remain required.

This increment routes tenant runtime artifacts. It does not claim regional data
residency for source builds, logs, metadata, volumes, databases, platform assets,
or an independently operated control plane. Operators must verify the physical
location and access policy of each configured store; the application validates
connection namespaces, not provider geography. Retain the internal/default-off
regional-compute gate. Native Linux x86_64/KVM regional acceptance, affected
`test-metal` coverage, and `leakcheck` remain mandatory before rollout is qualified.
