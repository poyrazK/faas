# ADR-381: Bounded inline output artifacts for stateless executions

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** Return explicitly selected files as bounded inline terminal output.
- **Why:** Agents need patches and reports while Runs remain disposable.
- **Consequences:** Receipts, transport, CLI and SDKs carry files within the existing output budget; guest images must be rebuilt and validated.
- **Rejected alternatives:** Persistent workspaces and artifact buckets introduce a separate storage lifecycle and change the stateless workload economics.

## Context

Disposable Runs already accept source bundles and return JSON and logs. Agents
also need generated patches, datasets, and reports. Retaining a VM filesystem
would change Gregale's stateless workload model and residency economics.

## Decision

- Callers may declare up to eight unique `output_files`, each a normalized
  relative POSIX path of at most 256 bytes. Limits live in `pkg/api/limits.go`.
- Both interpreters expose `context.output_dir`, a fresh guest scratch
  directory separate from staged source/input. Collection selects only declared
  files after successful interpreter exit; descriptor-relative opens reject
  symlinks in every path component, directories, and special files. Collection
  failure discards all artifacts and fails the execution with `artifact_invalid`.
- Terminal receipts gain optional `artifacts`: name, raw size, SHA-256, and
  base64 content. Result and logs plus the compact JSON artifacts array must
  fit the existing admitted output budget. Base64 expansion and metadata count;
  the guest, protocol, scheduler, state store, and database enforce the bound.
  Usage accounting includes the encoded artifacts once.
- Artifacts travel through vsock and gRPC, never guest network access or host
  directory mounts. No disk is retained. The scheduler destroys the microVM
  before committing the receipt. Cancellation, timeout, and teardown failure
  retain their existing terminalization fences.
- Compact serialized artifact bytes live in the existing execution row and
  share its account authorization and receipt lifecycle. This is an extension
  of bounded terminal output, with no separate artifact bucket, storage quota,
  or guest workspace. Retention follows execution receipts; this change does
  not establish a new independent retention duration.
- Export requests use guest protocol version 2; legacy requests retain version
  1. Old vmmd/guest binaries reject v2 before caller dispatch. Successful results
  must exactly match the requested selection, so an old transport cannot
  silently discard exports. Publish rebuilt guest images/sanitized snapshots
  before making exports available; rollback may make exports unavailable but
  must never acknowledge a partial export as success.
- The CLI saves files only into an explicitly selected local destination, checks
  checksums, confines writes through `os.Root`, and refuses overwrite. SDK
  decoding helpers verify size/checksum and perform no filesystem writes.

## Verification and rollout

Unit tests exercise both real interpreters, subsequent fresh execution,
cleanup, symlinks, special files, encoded budget boundaries, sealed selections,
transport, receipt persistence, usage accounting, and CLI/SDK integrity.
Apply the migration before new API/scheduler binaries. Rebuild guest images
and regenerate runtime snapshots under the new guest digest. Native x86_64
Linux KVM restore/execute/destroy acceptance and `make leakcheck` remain release
gates; a local interpreter test is not proof of VM isolation.
