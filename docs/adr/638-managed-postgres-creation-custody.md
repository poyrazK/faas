# ADR-638: Managed PostgreSQL creation custody before data verification

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Persist an immutable, private creation acknowledgement before
  waiting for restore lineage or snapshot retention metadata. Creation custody
  records the exact provider ID, independently reported source and creation
  time, logical owner, backend fingerprint, account, requested point and target
  generation. It grants cleanup authority only; existing verified restore and
  retained snapshot receipts continue to own readiness and publication.
- **Why:** Live Neon accepted restore branches and manual snapshots, then
  returned conflicting or incomplete correctness metadata. Returning an error
  discarded the accepted ID. Discovery-based cleanup required the same missing
  proof, so individual resources could not be compensated safely.
- **Consequences:** Restore acknowledgement writes require the current database
  lease, checked using the server clock after locking the row. Snapshot writes
  can only append custody to an already dispatched, matching durable clone
  intent. They do not grant a worker lease, advance clone state, release writers
  or claim retention. Contradictory receipts and duplicate physical ownership
  are refused. The requested point in custody is an intent fence, never an
  observation of restored data. Ordinary customer restores also revalidate
  observed lineage while becoming ready, matching clone correctness checks.
- **Rejected alternatives:** Treat a successful create response as readiness;
  replace missing capture timestamps with request values; relax exact PITR
  lineage; delete a shared source project to remove an ambiguous target; or
  authorize cleanup from a deterministic name alone.

Recovery with custody reads the exact acknowledged ID and never issues another
create. Cleanup independently verifies its operation owner, exact source,
creation time, project and resource type before mutation. A successful delete
response or completed response operation alone cannot retire custody: the
adapter must independently observe physical absence. Before deletion, it
checkpoints that the exact resource was independently visible (and a restore
branch was ready with no pending state). An early 404 before this checkpoint
remains unavailable; a retry after a lost delete acknowledgement uses the
persisted checkpoint to confirm absence. Snapshot compensation
can finish from the creation journal without manufacturing a retained snapshot
receipt. Provisioning gates do not block cleanup. Accounting intent and the
physical target identity survive database deletion; cleanup does not declare
zero historical usage or settled provider charges.
The clone schema registry treats custody as account identity, never copied
configuration. Final account erasure removes receipts only after their database
or snapshot lifecycle has confirmed deletion; unresolved custody retains its
restrictive foreign keys.

Legacy resources and lost POST acknowledgements without creation custody keep
full-proof discovery. A missing name cannot establish absence of an uncertain
creation. The journal does not close the unavoidable crash window between an
upstream commit and receiving its acknowledgement. Missing or contradictory
metadata retains source holds and retryable compensation; it never authorizes
publication or deletion of the source.

This change does not qualify Neon PITR, snapshot capture/retention, native copies
or settled usage. Independent timestamp/LSN evidence, restored data and login
isolation, and a completed provider usage window remain live qualification
requirements. Production provisioning stays disabled until those gates pass.
