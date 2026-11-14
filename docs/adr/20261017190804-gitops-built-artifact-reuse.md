# ADR-20261017190804: Reuse signed build artifacts across GitOps environments

- **Status:** accepted (implementation preview; native acceptance pending)
- **Date:** 2026-10-17
- **Related:** ADR-435, ADR-1155, ADR-20261017190802, ADR-20261017190803

## Context

A source descriptor at a new manifest commit causes a rebuild. Promotion must
select the tested application-layer bytes, preserve target configuration, and
keep publisher trust, retention and native qualification independent.

## Decision

1. Add `build_artifact_id` and `build_artifact_hash` to source, Dockerfile and
   function contracts. They select an immutable `SourceBuildRootfs` producer,
   not a source archive, OCI image, snapshot or release approval. Both fields
   are mandatory together. Recipe, directory and function runner must match the
   original build. The target manifest commit remains distinct from the
   original code commit recorded in the private materialization receipt.
2. Initially require the same account, project and mapped app across distinct
   environments, with matching provider/repository provenance. This preserves
   app-specific signer trust. Cross-app transfer, sidecars and scheduled-Job reuse are
   rejected explicitly; they need separate trust and scheduled-artifact adapters.
3. Preparation validates the retained origin, current selected producer, exact
   input hash, current publisher proof and base lineage. It creates a held
   target candidate referencing the same layer path/key/bytes. No new build,
   source archive download or registry materialization notification is issued.
   Target variables, secret aliases, runtime, binding identities and policy
   remain target inputs. No source snapshot, scan or qualification is copied.
4. Approval pins the layer in the existing GC retention fence, including
   protected-branch polling approvals. Current manifest pins are replaced on
   reapproval and removed on detach. A candidate pins origin metadata through
   its retained lifetime, including failure and rollback history. Foreign keys,
   origin mutation guards and borrower inventories prevent cleanup from
   silently removing its provenance. Pin publication and GC serialize on the
   existing storage-key retention row. A deleting/deleted key cannot be reused.
   After manifest detachment, explicit app retirement remains possible. Candidate
   pins survive the tombstone and are released only by retired same-app owner
   erasure; foreign borrowers continue to block provenance deletion.
5. Runtime artifact composition keeps the original producer and original
   publisher claim scope, while binding the composed identity to the target
   deployment/environment. SQL native projections resolve the explicit borrower
   and validate its exact receipt before authenticating origin lineage. Go
   still verifies current signatures in the same transaction. Revocation,
   replacement, changed lineage and missing target qualification remain fences.
6. Extend ADR-20261017190803's read-only promotion preview/export with producer ID/hash,
   original build/commit and byte digest. Require the source's current serving
   release evidence, explicit matching app mappings and compatible recipes.
   The review hash binds the selected artifact. Preview/export never pins or
   approves anything; normal Git approval and target qualification remain
   mandatory. PostgreSQL takes a repeatable-read transaction with read locks
   for source publisher validation; it performs no persistent writes.

## Consequences and validation boundary

The two-drive base/application-layer layout and builder microVM boundary remain
unchanged. Reuse saves build latency without transferring signing or execution
claims between deployments. References may become temporarily unusable after a
publisher or base change and require fresh origin evidence.

Portable signed byte fixtures and PostgreSQL regressions validate identities,
configuration isolation, no-build preparation, revocation and retention fences.
They do not establish valid native ext4 mounting, full VM lifecycle or leak
acceptance. Exclusive physical x86_64 Linux KVM acceptance remains pending; no
production qualification/enforcement is enabled by this change.

## Amendment (2026-11-05): fresh source imaging preserves reviewed inputs

A fresh source candidate keeps its reviewed archive hash, build ID, commit,
directory and provider provenance after imaging. Its initially empty
`image_digest` may then contain the canonical SHA-256 OCI manifest digest pinned
by imaged. This is derived build metadata, not a replacement source descriptor
or registry-image authority. Malformed digests and changes to reviewed source
identity remain invalid; the producer, signature and qualification checks remain
independent requirements.

The PostgreSQL candidate guard permits that first digest pin and updates to
derived `port` and `image_healthcheck_required` facts before publication. A
missing profile may acquire the usual `v1`/`unknown` header. It still rejects
digest replacement, other profile changes, source-envelope changes and writes
to borrowed artifacts. The append-only imaging metadata migration preserves
the existing activation and immutable deployment-input checks.

Source app layers apply explicit reviewed runtime fields after OCI inference,
deployment overrides and app lifecycle fields, as registry images and functions
do. The default `PORT` environment value is seeded from that final contract;
explicit image, deployment or app environment values retain precedence.
