# ADR-1549: Reuse signed builds for unchanged isolated GitOps contexts

Status: Accepted for implementation; native acceptance remains pending.

## Decision

A reviewed Git source may opt into `reuse_unchanged_build: true` and name a
repository-relative `build_context`. The default context is the whole repository.
The context must contain `source.directory`, the selected Dockerfile and every
shared package, lockfile or other local input needed by the build. Opting in
accepts reuse of the previous dependency resolution for unchanged inputs;
network dependency freshness is not inferred from a cache hit. Disable reuse to
request a new dependency resolution.

After exact provider/repository/commit and manifest verification, apid creates a
bounded canonical archive containing only the context. Repository-relative paths
remain intact; provider wrappers, timestamps and ownership are normalized, and
entries sorted. These are the actual bytes sent to the ephemeral builder VM,
not a selective hash over a larger accessible repository. Source code cannot
read an excluded manifest or sibling directory. This first format supports
regular files and directories; links and special entries inside the selected
context are rejected. The entire input and output are bounded by existing
archive/expanded-size/entry limits. No source paths are extracted on the host.

The existing builder cache is partitioned for this path by account, mapped app,
provider/repository identity, complete reviewed source recipe and frozen build
intent, in addition to context digest, source root, Dockerfile, framework, plan,
runtime-base reference, staged builder/toolchain identity and target platform.
Legacy entries and entries without origin metadata cannot satisfy this path.
An explicit `build_artifact_id` continues to take precedence and retains the
original context recipe for promotion/rollback compatibility.

A hit requires an undeleted same-app origin that is live or still preparing its
signed converted layer. Terminal history is deliberately a miss in this first
slice. The origin's source digest, frozen recipe and build provenance must match.
The origin's selected source/base producers and export signer are checked using
current state trust readers. The leased cached OCI bytes must match the signed
export's digest and byte count, even if their local integrity sidecar was changed.
A missing, stale, revoked, incompatible or corrupt origin causes a normal build.
The cache lease protects OCI bytes against eviction; this path never borrows or
pins the origin's converted rootfs. A completed fresh build can replace a stale
entry, including its origin, without changing outstanding hard-link leases.

Every approved commit still creates its own held candidate/build identity and
records the current commit in provenance. Reused OCI bytes are published under a
new current target signature, converted for that target, and must pass its own
qualification, config, activation and serving fences. No snapshot, scan approval,
qualification receipt, scheduled occurrence or serving evidence is reused.
`gitops explain`, status and the dashboard show the recorded cache decision;
build logs distinguish trust invalidation from an ordinary recipe miss.

## Example

```yaml
workloads:
  api:
    app: shop-api
    source:
      kind: source
      directory: workspace/apps/api
      reuse_unchanged_build: true
      build_context: workspace
```

Keep shared libraries and lockfiles under `workspace`. A manifest under
`environments/` can change target variables without changing the isolated build
input. A change anywhere under `workspace`, including shared inputs or file
modes, produces different delivered bytes. If the manifest itself is inside the
context, its changes conservatively cause a miss too.

## Limits and validation

This is a node-local optimization, not a distributed artifact index. Cache
availability, source retention, current trust and matching builder identity are
required; misses preserve the normal build path. It adds no database columns,
new execution authority, privileged native dispatch, production enforcement,
release, tag or deployment operation. Physical exclusive x86_64 KVM/full-VM/leak
acceptance is still required before claiming native qualification.
