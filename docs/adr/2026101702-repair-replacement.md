# ADR-2026101702: Linked replacement repair plans

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Decision: Replace an exact saved repair plan with one new immutable, digest-bound child. Reclassify only the parent's candidates, preserve all earlier exclusions, and atomically fence the parent against further writes.
- Scope: The existing PostgreSQL field-update contract, standalone executor, hosted Operations, CLI and review screens. No VM lifecycle or control-plane SQL schema changes.

## Candidate selection and lineage

Read-only validation remains advisory. Replacement is an explicit mutation with
a caller-reserved child UUID and expected parent digest. The new document binds
the parent ID/digest, previous candidate and exclusion counts, retained and
removed candidate counts, and each item's previous status. The same source
operation, enrollment, schema fingerprint, row incarnations, original versions
and inverse field patches remain authoritative. Newly stale candidates become
conflicts or unavailable records. Previously excluded items are copied without
reading their live rows and cannot become candidates, including across chains.

Replacement requires retained payload and the original qualified schema. It may
bind a new operator-configured check policy and receives its own fixed expiry,
digest, rehearsal and approval. No approval, successful rehearsal, failed check
report or receipt transfers from the parent. Eligibility remains mechanical;
constraints and the declared business checks still run in rehearsal and apply.
Existing row, payload, diagnostic and timeout bounds apply.

## Application database atomicity

The owner upgrade adds a nullable unique replacement pointer referencing another
saved plan. Its update is separate from the frozen document, digest, expiry and
approval history. Constructors do not install the upgrade. Parent/child identity
is checked before any source read or mutation; same-ID replay returns the saved
child, even after expiry, policy changes, application or payload compaction.
Another child UUID conflicts.

The replacement transaction takes the same parent row lock as apply and holds
it through commit. A previously committed receipt rejects replacement. An
in-flight apply must finish before replacement can observe its outcome; timeout
does not assume rollback. Otherwise insertion of the child and the parent fence
commit together. Later approval, evidence publication, check-failure publication
and apply of the parent reject the fence. A replacement failure rolls back both
the child and fence. Replacing a child follows the same protocol.

Validation reports `plan_replaced` without reading candidate rows. Review
metadata stays out of the execution header for compatibility with existing
strict guest readers; the snapshot retains the complete digest-bound document.

## Hosted reservation and recovery

The management action requires the normal PostgreSQL management permission,
MFA, same-origin check, expected parent digest and initial operation sequence.
The Idempotency-Key is the replacement UUID. A CAS transition reserves this
UUID in a `replacing` parent before contacting its exact configured executor
binding. Active rehearsal, applying, unknown-outcome and successful operations
cannot enter replacement. There is no timeout-based claim release.

A lost executor response leaves the reservation durable. Only the same parent,
digest and child UUID can resume; a replay may use its original sequence. The
source replays its frozen child rather than refreshing selection. Child
admission and the parent's `replaced` transition commit in one control-plane
transaction using existing SQLC queries and normal progress/cancellation events.
The child stores parent identity, and the parent's original approval remains in
history. Its terminal status cannot authorize further execution.

The admitted parent already binds the source's owner, scope and deployment.
Replacement admission uses that retained identity, so expiration of the original
source Operation cannot prevent recovery. A reservation on an already terminal
parent keeps its terminal status until completion; ordinary child admission
quotas still apply. Pending parents and their events are held against normal
retention, including after the previous result window expires. An expired child
recovered after response loss is admitted as failed, preserving both identities
without refreshing its expiry or granting write authority. Pending replacements
appear in operator health with same-UUID recovery guidance and existing timeout
alerts. Operator removal of an account/app remains its separate owner action.

Application payload retention keeps its existing conservative rule: an approved
parent without an apply receipt remains pinned after replacement. This decision
does not introduce an automatic purge or release based on the replacement fence.
[ADR-2026101704](2026101704-repair-superseded-retention.md) subsequently permits
separately reviewed compaction after verifying the saved replacement and waiting
the retention minimum. Replacement itself still performs no purge.

## Clients and acceptance

`data-repair replace` requires the expected digest, saved new repair-operation
UUID and `--yes`. Both review screens display the UUID before submission, keep
it in the page URL for recovery, and render parent identity, candidate counts and
previous item statuses. Tokens and row data are not persisted in browser storage.

Portable acceptance covers the 460-to-459 narrowing with 41 exclusions, exact
parent history, unchanged earlier exclusions, concurrent/idempotent replacement,
apply serialization through commit, fresh approval, further-drift rollback,
policy/schema changes, authenticated APIs, lost executor response recovery,
retention and operator health. The CLI journey rehearses and applies the new
named policy in the real isolated PostgreSQL runner, then verifies the receipt.
