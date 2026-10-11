# ADR-933 · Durable entity outbox relay and transport acceptance

- **Status:** implemented locally; automated and native/live-provider verification pending
- **Date:** 2026-10-09
- **Decision:** Reserve the entity's FIFO head in its fenced manifest, accept it into the existing webhook delivery ledger using a permanent stable-ID receipt, then remove it through a fenced immutable-snapshot publication. Discover work through disposable time-ordered hints and independent bounded entity reconciliation. Keep the relay and guest outbox disabled by default.
- **Why:** An entity commit and its outgoing intent already share one publication (ADR-903). A worker can crash after enqueueing but before removing that intent. Neither a new random delivery ID nor retention-limited delivery history proves whether that message was previously accepted.
- **Consequences:** Entity state, pending intents and relay retry reservations remain authoritative in object storage. SQL records transport acceptance and delivery metadata only. Receiver delivery stays at least once. Guest protocol v1 still rejects outgoing intents; this change does not qualify or launch customer messaging.
- **Rejected alternatives:** Repeating public `POST /outbox` duplicates deliveries. A unique delivery ID alone fails after terminal-history pruning. An acceptance receipt without an atomic delivery insert can lose work. Dispatching straight from LIST or an observation bypasses ownership. Per-message retry metadata in a full snapshot can make an already full queue impossible to drain. A second HTTP sender would duplicate existing destination, signing and receiver-recovery controls.

## Reservation, acceptance and acknowledgement

Only the first pending message is eligible. `ReserveOutbox` checks authenticated
committed state under a current entity claim, then CAS-publishes its message ID,
fresh attempt token, attempt count and next retry time in the manifest. The
reservation is bounded metadata outside the snapshot's payload budget. A
definitely rejected CAS consumes nothing; a committed reservation with a lost
acknowledgement or subsequent crash consumes an attempt. Uncertain reservation
writes never authorize transport acceptance. Ownership and the attempt token are
checked again before entering the acceptance callback.

The apid worker checks current app/account admission and the explicit app
allowlist before acquiring work and again before acceptance under ownership.
It resolves the immutable environment identity and any selected platform tenant
through the existing invocation admission path. Deleted/recreated environments,
suspended tenants, account deletion/suspension/abuse holds, unavailable workload
classes and plan downgrades cannot attach old work to a new scope. Established
admission holds do not consume attempts; a change during an already reserved
attempt may consume that attempt.

`AcceptEntityOutboxDelivery` atomically inserts a fingerprint receipt and one
delivery whose ID is the committed message ID. On first acceptance it locks and
checks an enabled registered **app** webhook owned by that exact app/account.
It also locks and rechecks account/app status, deletion, abuse holds, plan and
request-workload admission with the insert, covering changes after apid's reads.
Account or tenant webhook scopes and arbitrary URLs are not destinations.
The existing schedd dispatcher owns signing, network restrictions, receiver
backoff, attempt history, dead letters and customer delivery replay. Entity
relay attempts do not run guest code or allocate an entity VM.

The delivery event is the intent's event type. Its JSON body contains `entity`,
`message_id`, `state_version`, `ordinal` and the original JSON payload as `data`.
The dispatcher uses the stable delivery ID in its existing receiver headers.
Recipients must deduplicate that identity before non-idempotent effects.

The acceptance fingerprint binds account, app, webhook, event and the entire
delivery body. Reusing an ID with different content fails closed. An exact repeat
does not reset or recreate a delivery, including after terminal-history pruning
or webhook deletion. Receipts contain no customer payload or signing secret,
have no delivery/webhook FK or TTL, and remain until the owning account/app is
physically deleted. Automatic receipt compaction is deliberately absent: a
future collector needs proof that no pending intent or delayed relay can repeat
acceptance. Their SQL footprint remains an operational cost to qualify.

Only confirmed acceptance permits `AcknowledgeOutbox`. It uploads a smaller
snapshot and publishes through the current claim, attempt token, snapshot root,
storage generation and manifest CAS. It preserves the business version, data,
alarm reservation and receipt journal, and updates snapshot-byte accounting
without incrementing receipt count. Removal remains possible after a byte cap
is lowered. A concurrent business commit or cleanup barrier rejects the obsolete
publication; ordinary business transitions preserve the relay reservation.

## Discovery and recovery

After a successful commit, reservation, acknowledgement or operator retry,
the engine attempts an immutable time-ordered head hint. Hints carry scope and
message identity, never payloads. They are not ownership, quota or delivery
authority. Failed hints cannot revoke a successful authoritative publication.
`ScanIndexedDueOutbox` validates hints against the authenticated committed head
and current retry reservation, pruning obsolete entries. An independent rotating
`ScanDueOutbox` reads eight entity prefixes per page and repairs missing hints,
including work written before the relay was enabled. Corrupt entities fail
closed individually without pinning the discovery cursor. Listings remain
advisory and a restart can begin both scans from empty cursors.

The default-disabled `FAAS_DURABLE_ENTITY_OUTBOX_ENABLED=1` requires the existing
invocation preview, deduplicating transport support and private delimiter/flat
listing plus probe deletion at startup. Polls run every five seconds, with
bounded reads/pages and a 25-second budget for each entity acceptance attempt.
Each sweep visits an entity at most once across both scans. A successful head
acknowledgement makes the next head discoverable on a later sweep. FIFO here
orders **ledger acceptance**, not receiver completion; the existing dispatcher
can deliver several accepted rows concurrently.

The relay reserves at most five attempts, with 30-second exponential backoff
capped at five minutes. Ownership expiry still controls takeover after a worker
crash and can delay recovery beyond the retry deadline. No short-lease renewal
or wake-latency guarantee is introduced. An exhausted head remains in the
snapshot as a private dead letter and blocks later messages in that entity;
other entities continue. `InspectOutbox` exposes pending count, head ID, attempts,
retry deadline and exhaustion without payloads or tokens. Operator-only
`RetryOutbox` requires the exact exhausted head and a current claim. It resets
the budget while preserving message identity, so a prior acceptance still
deduplicates. A crash after the last reservation/acceptance may require this
operator retry to resolve the final uncertain handoff.
If accepted delivery history was already pruned, this retry resolves only the
entity acknowledgement; sending a new notification requires a new business intent.

Metrics add bounded `outbox` operation outcomes, `outbox_index` uploaded bytes
and a histogram of pending counts in exact-entity observations. Those repeated
samples are not an authoritative fleet backlog. Logs omit provider/SQL errors,
identities, payloads and credentials.

## Upgrade and verification

All manifest writers use **schema 6**; readers accept schemas 1–6 and the existing
snapshot schemas 1–3. Older writers cannot preserve the reservation, so stop all
older entity callers, alarms and maintenance workers before any new writer.
Apply migration `20261010010028804_entity_outbox_acceptance.sql` before enabling
the relay. Keep bucket lifecycle deletion disabled. Downgrade requires disabling
workers, an explicit object-storage migration and preservation of acceptance
receipts while any replayable pending work exists; dropping the receipt table
alone is unsafe. Guest protocol v1 remains unchanged. The separately gated v2/SDK
follow-on is recorded in [ADR-934](934-durable-entity-guest-outbox-protocol.md);
qualification is still required before enabling either preview gate.

Added tests cover reservation uncertainty, lost acceptance/acknowledgements,
restart, takeover and cleanup barriers, replay after business-state changes, alarm/receipt/storage
preservation, exhausted-head operator recovery, missing/forged hints, bounded
pagination, corrupt reservations, current scope/destination admission,
concurrent acceptance and retention-safe deduplication in memory and PostgreSQL.
They have not been run in this cloud workspace, per the user's testing handoff.
Source generation, formatting and diff inspection do not establish runtime
acceptance. The testing agent must run automated checks and then qualify native
microVM/real-GCS crash, partition, latency and cost behavior before preview
enablement. No feature flag, live bucket or deployment was changed here.

Testing-agent handoff: run the durableentity/objectstorage, state (with PostgreSQL),
apid and operator-harness suites, plus repository lint and sqlc drift checks.
Use the repository's pinned Go toolchain and existing CI command/budget split;
these source changes have no current test-pass or coverage evidence.
