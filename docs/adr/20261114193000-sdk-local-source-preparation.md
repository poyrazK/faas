# ADR-20261114193000: Local source preparation in the Node and Python SDKs

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261114181406](20261114181406-sdk-resumable-source-uploads.md)

## Context

Resumable source uploads consume an existing archive. Applications and agents
still need a CLI or hand-written packing logic to prepare that archive. Selection
drift can omit Dockerfile build inputs or include installed dependencies and
credentials. A changing tree can invalidate a supposedly reviewed source bundle.

## Decision

Add standalone `inspectSource`/`prepareSource` and async Python
`inspect_source`/`prepare_source`, exported from public and preview packages.
Keep existing client interfaces unchanged. Choose the local context directory and
relative application source root explicitly; retain the context's workspace files
and return source-root metadata for upload deploy options. Detect Dockerfile
markers at the selected application root, with an explicit override for manifest
build configurations. Manifest/workspace discovery and generated function build
markers remain CLI features.

Return immutable typed plans with entries, byte sizes, normalized modes, content
hashes, included/excluded reasons, ignore-rule provenance and blocking issues.
Use the CLI's ordered ignore grammar, mandatory VCS exclusions, Go module
vendoring exception and Dockerfile build output rules. Directory pruning follows
the CLI; a negated child cannot re-enter an excluded parent. Strict dotenv checks
use the provider and entropy rules, blocking findings rather than rewriting the
customer's source. An explicit off option is available. Never include secret
values in findings. The server remains the validation boundary.

Collect bounded metadata and stream file data in 64 KiB chunks. Normalize tar
ownership, permissions, ordering and timestamps, support PAX paths, and gzip at a
fixed compression level. Repeatability applies within a runtime; cross-runtime
gzip identity is not a contract. Resolve the context and in-tree links, reject
escaping/dangling/cyclic links and irregular files, and verify identity, metadata,
containment and content hashes before publication. Record directories and ignore
files too, detecting selection changes. Write a private temporary archive outside
the context and atomically replace the output only after verification. The final
rename is the publication point; no power-loss durability guarantee is added.

Provide per-call deadlines, cancellation, progress/review callbacks and caller
limits on expanded/compressed bytes and entries. Entry and expanded-byte maxima
follow `pkg/api/limits.go`, not plan quotas. Bound traversal to four times the
entry budget, nesting to 64, ignore contents to 1 MiB and dotenv lines to 64 KiB.
Retire canceled callbacks and read workers without permitting late publication.

## Verification

Both SDKs consume shared worktree and secret fixtures. A stdlib-only Go harness
extracts and compiles the actual CLI declarations against those fixtures. Test
independent tar readers, repeated bytes, permissions, links, source/rule changes,
limits, deadlines, cancellation and concurrent preparation. Verify public and
preview exports from packed npm/wheel installations with strict type checks.
Preserve handwritten Python modules and exports through the generator.

## Consequences

Developers can review and package source without a CLI dependency, then compose
the existing checkpointed upload, explicit commit and resource waiters. Source
preparation stays local and makes no deployment decisions or network calls.
