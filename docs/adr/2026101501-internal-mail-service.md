# ADR-1737: Internal mail service with independent activation and delivery gates

- Status: accepted for an internal experiment; customer availability is disabled.
- Date: 2026-10-15
- Builds on: ADR-115 and ADR-136 (platform mail transport and suppression).

Durable deployment-failure capture and notices are specified in
[ADR-20261017155842](20261017155842-mail-deployment-failure-notices.md). They remain
internal behind their independent default-off gate.

Gregale needs to evaluate running its own transactional mail delivery. This
change introduces an internal service rather than a customer email entitlement.
Existing Resend/Postmark selections remain operator-controlled.

## Decision

`FAAS_INTERNAL_MAIL_RECOVERY_ENABLED=0` separately gates private database
restore recovery. The database cannot detect its own rewind: each restore must
supply a fresh canonical `FAAS_INTERNAL_MAIL_RECOVERY_ID` outside the snapshot,
with all maild instances stopped. Startup commits an audited namespace hold
before encryption readiness, the listener or workers. The same ID across normal
restarts preserves the current hold/release; an older known ID cannot replace a
newer generation. This does not automatically detect an unannounced restore.

Admission and SMTP custody take a shared namespace recovery fence (871739)
before encryption, address, producer, domain or recipient locks. Starting a
recovery or changing its review state takes exclusive. New admission and
producer authentication/feed acknowledgements stop while held. Existing SMTP
custody, settlement and authenticated reports continue. Every pending restored
recipient whose saved recovery ID differs remains quarantined, including after
a global release and with management off. Claims, final custody and expiry
check that fence; pruning pauses while held and unknown pending mail retains
its body. No bulk operation rewrites recipient custody or assumes non-acceptance.

The private bounded preview contains IDs and custody metadata only. Per-recipient
review requires the current recovery ID/revision, expected status, positive
relay acceptance or non-acceptance evidence, and an audited saved request UUID.
Active leases and terminal states cannot be overwritten; known relay ownership
cannot be changed to retry. Review clears only that recipient's generation
quarantine, preserves timing/attempts/suppression and all independent holds, and
commits its event and audit together. Duplicate requests replay their original
result without changing newer state.

Audited global release verifies all required encryption readers under the key
fence and requires explicit reviews of restored suppression/delivery policy,
producer credentials/revocations, event consumers, relay custody and retained
quarantine. These operator assertions must use independently retained evidence;
a restored database cannot reconstruct post-backup changes or missing messages.
Unreviewed recipients remain blocked. Rollback refuses any used recovery state
or audit. New label-free aggregate metrics are private and default-off. The
offline PostgreSQL snapshot-rewind drill uses a fake relay and does not qualify
live delivery or production backup infrastructure.

`FAAS_INTERNAL_MAIL_SUPPRESSION_CONTROLS_ENABLED` separately gates private
per-address inspection/history, audited blocking and reviewed release. Existing
active suppressions remain the dispatch projection; durable state and history
retain revisions after release. Namespace-wide mailbox matching follows the
existing case-insensitive policy, without domain patterns or alias expansion.
Complaints remain protected across messages and cannot be released or weakened
by manual adds or later bounces. Eligible manual/hard-bounce releases require a
current revision, a saved request ID and explicit reviewed evidence.

Every distinct adverse observation advances the address revision and retains
only trusted adapter provenance, recipient ID and a namespace/source-bound
event digest. Provider provenance is assigned after DKIM authentication, Postfix
provenance by the private bridge, and legacy JSON reports remain service reports
regardless of their event prefix. Exact report replay neither creates history
nor reblocks a reviewed address. Operator mutation, active projection, history
and audit commit atomically; replay cannot overwrite a later block.

Per-address advisory locks serialize feedback and operator changes against
admission and SMTP custody. Admission takes addresses in sorted order and saves
each recipient's last blocking revision, with a blocked admission retaining zero.
After release, recipients predating that block or admitted during it remain
ineligible for new SMTP custody and settle as suppressed when processed. Only
future admissions inherit the released revision. No operation revives terminal
mail, modifies established SMTP custody, refunds quotas, or clears other holds.
Feedback takes its address lock before reputation/recipient locks, matching the
SMTP order and preventing cross-feature deadlocks.

Normal feedback/history tracking and saved recipient fences persist with
management off. Legacy backfill conservatively preserves known complaints even
where older projection reasons were downgraded. Rollback refuses managed
history, release audit or saved recipient admission revisions. Private aggregate
metrics contain no addresses or source/producer/domain identifiers. This adds
no public endpoint, automatic activation or live delivery qualification.

`FAAS_INTERNAL_MAIL_ENCRYPTION_ROTATION_ENABLED` separately gates private key
inspection, audited encryption key activation, bounded re-encryption and logical
retirement. The existing dedicated age identity remains the legacy reader and
verifier. An optional private, bounded keyring stages additional X25519 identities
through systemd credentials. Staging alone does not select a writer. PostgreSQL
pins the namespace's legacy identity and active key under revision checks; key
identifiers derive from public recipients and contain no private material.

New envelopes authenticate their format version, key identifier, namespace and
resource. New HMAC fingerprints use a separate versioned domain and explicit
verifier identifier. Legacy payloads and 32-byte verifiers remain readable.
Replay selects the saved verifier independently of the active encryption key.
Re-encryption authenticates old content and HMACs before changing retained bytes
and their verifier; it never changes message ownership, DKIM selection, events,
timing, delivery leases or SMTP custody. Pruned fingerprints cannot be migrated
without their original content, so their old identities remain required.

A shared namespace advisory fence precedes every ciphertext/fingerprint writer,
including automatic retention and DKIM selection/retirement. Activation, bounded
migration and retirement hold it exclusively. Stale writers cannot reintroduce
an old key after activation or retirement, including with management disabled.
Each bounded migration and its idempotent operator audit commit atomically;
failed verification, cancellation or a disabled gate rolls the entire pass back.
Permanent retirement refuses active keys or any retained ciphertext/verifier
dependency. Retirement records cannot be undone by restoring configuration.

Startup checks all required read identities. Workers without the selected key
stop before claiming work; a missing-key render race queues the claim without
spending a delivery attempt. Runtime rollback retains the keyring, durable
selection, new formats and writer fences while removing management access.
Schema rollback refuses selected keys, versioned data or encryption audit.
Logical retirement never deletes key files or backup/WAL material; operators
retain identities needed by older snapshots. Metadata-only operator controls
and aggregate metrics add no public route, automatic key change or SMTP action.

`FAAS_INTERNAL_MAIL_PRODUCER_LIFECYCLE_ENABLED` separately gates staged producer
credential activation and audited rotation, revocation, suspension and reviewed
resume. Private enrollment may contain a primary `token` and one optional
`replacement_token`; all configured credentials must be distinct across callers
and service/report/operator/feedback roles. Staging alone never activates the
replacement. Operator status and requests use producer-bound SHA-256 credential
identifiers. Bearer values remain exclusively in private configuration and
selected callers' existing secret delivery paths.

Rotation uses an explicit saved request ID, expected revision and the staged
replacement's identifier. It atomically selects the replacement and saves an
irreversible deadline for the old credential, with a default one-hour overlap
and a maximum of 24 hours; explicit zero permits immediate cutover. Deadlines
cannot be extended, revoked/retired identifiers cannot be recycled, and audit
replay returns the original result without changing newer state or requiring
old secrets. After updating callers, operators promote the selected secret to
the primary configuration field and remove the old one. Stable producer IDs,
message ownership, idempotency, daily budgets and event-consumer progress remain
independent of credentials.

Saved revocations, retirement deadlines and producer suspensions remain enforced
with management disabled. Suspension blocks producer access, admission and new
SMTP custody; credential revocation blocks only that credential's access and
owner mutations. Existing queued mail remains dispatchable after revocation,
while existing SMTP custody, settlement, feedback and expiry survive suspension.
Reviewed resume never restores revoked credentials or clears other holds.

Producer access advisory locks serialize mutations against admission,
event-consumer writes and SMTP handoff, including the first durable state row.
HTTP authentication is rechecked inside owner transactions and after later lock
waits, so revocation or overlap expiry cannot commit a new message, create a
consumer or advance its checkpoint after authorization is lost. Operator writes
and their audit share a transaction and revision checks. Metadata-only private
CLI inspection and aggregate metrics add no public routes or automatic callers.
Database rollback refuses retained access state or lifecycle audit. Runtime
rollback keeps these binaries and tables, promotes the selected credential to
the primary field, and disables only the management gate.

`FAAS_INTERNAL_MAIL_PRODUCER_SCHEDULING_ENABLED` separately gates fair producer
claims and rolling minute submission limits. Claims serialize a durable
least-recently-served turn per namespace and producer while retaining the
existing eligibility filters and FIFO order within each producer. A producer
with a large backlog cannot exhaust all turns while other producers are ready.
Legacy service-token messages share their own producer bucket.

Each producer defaults to ten submission starts in any rolling 60 seconds;
private enrollment may specify `dispatch_per_minute` in `1..100`. A producer
row lock fences capacity across sender domains and workers. Reservation,
recipient-domain pacing and the transition to SMTP custody commit atomically,
using the database clock after lock waits. Rejected handoffs roll back both
slots; rate races return to the queue without charging delivery attempts or
failure events. Committed SMTP failures and uncertain attempts retain their
slots. Historical reservation rows are pruned during the next successful
handoff for that producer, retaining at most the configured maximum per bucket.

The read-only private `mailctl scheduler status` reports per-producer queue age,
usage, capacity and retry time. Aggregate metrics have no producer labels.
Disabling the gate restores FIFO and bypasses limits while preserving durable
turns and recent reservations; re-enabling honors those reservations. Other
holds, timing, expiry, suppression and custody fences remain in force. This
adds no automatic activation, public routes or live delivery qualification.

`FAAS_INTERNAL_MAIL_EVENT_FEED_ENABLED` separately gates durable delivery event
feeds for named internal producers on the existing Unix API. A bounded worker
publishes committed source observations into immutable per-producer streams,
allocating positions under namespace serialization in the projection transaction.
Late source commits remain discoverable; source timestamps/IDs and nontransactional
sequence allocation are not consumer high-water marks. Stable feed UUIDs exclude
raw report identities, and payloads contain only message/recipient/domain IDs,
kind and observation/publication times, without addresses or diagnostics.

Each producer may register eight named consumers. Pull saves an immutable batch
and scoped receipt; acknowledgment advances its durable checkpoint only after
the consumer's application effects succeed. Receipts support response-loss replay
without changing newer progress. Processing is at least once and requires durable
application deduplication by event ID. Audited operator rewind permits only owned
cursors at or behind the acknowledged checkpoint, increments a generation and
fences stale acknowledgments. It cannot resend email, skip forward or undo effects.
Publication and access stop when disabled while metadata survives for resumption
and bounded backfill. Aggregate lag monitoring has no identity labels. The feature
adds no public listener, automatic caller activation or live delivery qualification.

`FAAS_INTERNAL_MAIL_REPUTATION_ENABLED` adds default-off, internal sender
protection. DKIM-authenticated provider feedback alone contributes adverse
evidence; distinct recipient deliveries form rolling 24-hour activity windows
per producer and sending domain. The sample floor is 20, with at least three
hard bounces at 5% or two complaints at 0.1%. Late feedback joins both numerator
and denominator, repeated recipient observations never refresh timestamps, and
complaints supersede bounces. Worker evaluation covers rates crossing thresholds
as traffic ages and sample floors change. Feedback and durable trigger snapshots
commit atomically with holds under the existing namespace submission fence.

Reputation holds persist until an audited, revision-checked operator review
releases each scope. A release acknowledges prior evidence; only new adverse
evidence can cause another hold, and request replay cannot clear a newer one.
Holds remain separate from operator controls and relay health, refund racing
claims/pacing slots and preserve expiry, suppression and unknown SMTP custody.
Private CLI inspection includes per-scope windows and transition evidence;
aggregate monitoring uses only closed labels. Runtime rollback bypasses this
feature while retaining evidence. No production activation or live qualification
is introduced; database rollback refuses retained reputation state.

`maild` owns only `internal_mail_*` tables. Its HTTP API is served on the
mode-0660 Unix socket `/run/faas/maild.sock`, with an additional dedicated bearer
credential. This private HTTP protocol is an explicit exception to the usual
gRPC protocol for inter-daemon calls: it supports operator inspection with curl
and a small mail transport without extending the customer OpenAPI contract.
It does not bypass apid ownership of customer-intent tables. No public edge,
customer route, SDK command, customer plan allowance, or billing is added.

Two additional exact default-off gates provide signing-key maintenance and
recurring domain authorization: `FAAS_INTERNAL_MAIL_DKIM_ROTATION_ENABLED` and
`FAAS_INTERNAL_MAIL_DOMAIN_REVERIFY_ENABLED`. DKIM operations use the existing
private operator credential and atomic audit. Keys move through staged, active,
retiring and retired states; observed publication precedes activation by at
least one hour. Old selectors remain required for at least seven days and while
unresolved tracked or legacy submissions retain relay custody. Retirement also
requires an operator's queue evidence and never edits DNS itself.

The selected signer is mirrored atomically in the domain row for delivery;
new key ciphertext binds namespace, domain and key ID. Submission serializes
with signer changes and records the actual selector before SMTP. Periodic DNS
checks persist public authorization observations, distinguish unavailable DNS
from confirmed mismatches, and fence stale results by selector, observation
timestamp and DNS revision. Fresh proof for the configured source IP is required
within 24 hours when recurring verification is enabled. Two mismatches separated
by five minutes pause new work; successful proof recovers it. Pauses persist
when the gate is disabled, and disabled domain status is never overridden.
The existing maild worker group owns the bounded hourly checks, with separate
aggregate monitoring and internal warnings. No additional listener, daemon,
customer API or production activation is added.

Private operator tooling uses `mailctl` and an exact, default-off gate,
`FAAS_INTERNAL_MAIL_OPERATIONS_ENABLED=1`, in addition to the master gate. A
distinct operator credential permits bounded metadata inspection, read-only
DNS/suppression review, uncertain reconciliation and separately gated retention.
It grants no submission or trusted-report access. Successful operator writes
and `internal_mail_operations` audit records share a transaction; namespace-bound
request IDs deduplicate retries after ambiguous responses. Reconciliation
requires an explicit operator assertion with an evidence reference and cannot
override an active lease or terminal recipient state. The actor field is an
assertion under a shared credential, not a customer identity. No new listener,
daemon, customer command, SDK or production activation is introduced.

`FAAS_INTERNAL_MAIL_DELIVERY_CONTROLS_ENABLED` separately gates audited operator
pause/resume, drain status and queued cancellation. Namespace and domain holds
persist in maild-owned state and remain enforced when operator access is off.
Mutations require the observed scope revision and the existing evidence-bearing
audit; request replay returns the original result without reapplying old intent.
Submission takes shared namespace/domain transaction locks before locking the
domain and marking its recipient submitting. Control changes take the matching
exclusive scope lock, fencing even the first hold insertion. Candidate selection
and the pre-render check also honor holds; admission continues under its quotas.

Drain saves a hold and waits only for local claims and attempts, with uncertain
custody blocking completion. Submitted Postfix work remains separately visible;
timeouts never resume the scope or alter the MTA queue. Cancellation locks
recipients and changes only queued, deferred or claimed work to terminal
`cancelled`, clearing leases and atomically recording events and the audit.
Mixed-custody results identify protected recipients. Worker settlement cannot
revive cancelled work; trusted reports and reconciliation cannot requeue it.
Cancelled payloads become eligible only after the existing quiet period, while
replay metadata remains. Rollback refuses durable holds or operator evidence.
No public route, listener, production activation or live qualification is added.

`FAAS_INTERNAL_MAIL_PRODUCERS_ENABLED` separately gates stable internal producer
identities on the same Unix socket. Each private enrollment has a distinct token,
exact approved From mailboxes and daily/pending recipient budgets. Producer
credentials authorize only own submission, metadata, event history and usage.
The explicit `internal-producer` platform transport reads only its scoped
credential and fails closed when disabled. Legacy full-service callers retain
their selected transport and privileges.

Messages persist producer ID and the approved sender mailbox. Idempotency uses
namespace, producer ID and request key, retaining the empty scope for legacy
messages. Credential rotation preserves that identity; IDs cannot safely be
reassigned to unrelated callers. The namespace admission lock serializes producer
budgets with the existing global safeguards and durable outbox writes. Ownership
predicates precede metadata reads. Payload cleanup retains ownership and replay
evidence; rollback refuses to discard producer-owned records.

Claim selection and first-handoff SQL require an enrolled producer/sender pair,
and the worker rechecks access before rendering and SMTP. Disabling producer
access or withdrawing a sender prevents new queued handoffs, while existing age
limits, delivery holds and Postfix custody remain in force. Configuration is
operator-managed and applied through daemon/caller restarts. Namespace aggregate
monitoring gains no producer or address labels. No customer service, public API,
new listener, VM egress change or live enrollment is introduced.

`FAAS_INTERNAL_MAIL_SCHEDULING_ENABLED` separately gates absolute `send_at` and
`expires_at` admission, timed dispatch and metadata-only expiry maintenance.
Schedules are bounded to 24 hours ahead, followed by at most the normal 24-hour
queue-age window. UTC microsecond canonicalization preserves legacy requests
and binds timing to encrypted payloads and HMAC replay evidence. Admission checks
new windows after the existing scoped key lookup, so identical retries remain
valid after their original time passes. Timing changes require a new intent.

Scheduled work consumes admission/pending budgets immediately. Claim selection
holds future or disabled timed work without rendering or charging attempts.
First handoff locks the domain and recipient before reading current database
wall time; SMTP additionally bounds its session and checks before body transfer.
Expired safe-to-retry recipients become terminal `expired` with fenced events.
Expiry maintenance can run with SMTP off and never overwrites relay or unknown
custody. A DATA ambiguity remains uncertain after the deadline; Postfix retains
accepted work. Persisted deadlines are enforced even when new timing access is
disabled. Retention preserves timestamps and replay, and rollback refuses timing
or expiry evidence.

The explicit `mail.SendTimed` helper requires a supported internal capability,
the scheduling gate and a caller-stable message ID. Unsupported transports or
decorators fail closed. Existing notification callers keep ordinary Send unless
separately opted in. Metadata exposes timing without ciphertext, and aggregate
pending-age monitoring starts at the due time to avoid false backlog alerts.
No new listener, daemon, public API, customer entitlement or activation is added.

`FAAS_INTERNAL_MAIL_DOMAIN_PACING_ENABLED` separately gates durable submission
spacing by namespace and exact recipient domain. A private bounded interval
defaults to five seconds. Candidate selection skips cooling domains; submission
locks and reserves the shared row in the same transaction as custody transfer,
using the current database clock after waits. Producer and sending-domain
boundaries cannot multiply a recipient-domain slot. Idle time banks no burst.
Racing claims return to the queue at the saved cooldown without spending attempts
or adding failures. Failed custody transitions roll back their slot; committed
attempts retain it even after rejection or ambiguous transport outcomes.

Pacing preserves future scheduling, expiry, replay, budgets, suppression and
delivery controls. It creates no new listener or public policy API. Aggregate
monitoring contains no recipient-domain labels. Disabling pacing bypasses pacing
waits while retaining rows for restart/re-enable recovery, and rollback refuses
persisted cooldown metadata. The optional unit and examples default the gate off.
Postfix remains responsible for its own existing queue and remote SMTP pacing;
maild spaces new submission reservations rather than final remote deliveries.

`FAAS_INTERNAL_MAIL_RELAY_BACKPRESSURE_ENABLED` separately gates automatic relay
protection. The existing report/metrics helper submits bounded whole-relay queue
aggregates and a greeting-only numeric-loopback SMTP probe using its telemetry
credential on the protected Unix socket. No raw queue, message or SMTP transcript
is stored. Missing, stale or failed evidence and depth/age limits hold new
handoffs. Resumption requires two minutes of fresh healthy observations; replay
cannot refresh sample age or accumulate healthy time across a stale gap.

Health/recovery and closed-reason transition events are maild-owned durable state,
separate from operator holds. Namespace transaction locks serialize observations
with first handoffs and manual namespace controls. Current-clock SQL fences
freshness and known queue age after other lock waits; rejection refunds a racing
claim and rolls back a new pacing slot. Maintenance preserves expiry and unknown
custody. Automatic recovery never changes operator holds, cancellations or
Postfix queues. Private CLI inspection and fixed-label metrics expose status;
runtime rollback bypasses only automatic protection while retaining evidence.
All activation gates default off, and live relay qualification remains separate.

`FAAS_INTERNAL_MAIL_ENABLED=1` is required for the daemon, private routes,
message admission, and platform transport. Disabled startup reads no mail
credentials and opens no DB or listener. A second exact opt-in,
`FAAS_INTERNAL_MAIL_DELIVERY_ENABLED=1`, enables SMTP submission. Platform
notifications additionally require explicit `FAAS_MAIL_TRANSPORT=internal`.
No migration, unit generation, or core deployment activates it. The systemd
unit is in `OptionalRegistry`; the Ansible role is included only under
`faas_internal_mail_managed`, with service activation defaulting to false.

A third exact gate, `FAAS_INTERNAL_MAIL_REPORTS_ENABLED=1`, enables automatic
Postfix reporting, independently of new SMTP delivery. maild requests RFC 3461
DSNs with a recipient-bound envelope ID; Postfix keeps the RFC 3464 reports in
its durable queue and delivers them through the unprivileged `mail-report` pipe
helper. The helper uses a separate telemetry token on gated Unix HTTP routes
for report ingestion and aggregate monitoring.
It acknowledges only after commit and returns temporary failure while disabled
or unavailable. Byte-stable hashed event IDs deduplicate Postfix retry after
lost responses. Parsed reports must match the original recipient and envelope
ID; raw notifications and provider diagnostics are discarded. Late observations
cannot regress bounces or complaints. No file-tail cursor or second local spool
is introduced. Optional helper installation does not activate Postfix routing.

A fourth default-off gate, `FAAS_INTERNAL_MAIL_FEEDBACK_ENABLED=1`, admits late
provider DSNs and RFC 5965 abuse reports on a separate source-scoped Unix route.
Each explicitly enrolled source has its own credential, exact DKIM domains,
report sender mailboxes, recipient domains and allowed formats. The credential
does not authenticate Internet evidence: maild separately verifies an aligned
provider DKIM signature covering the complete body and critical outer headers.
Unsigned, redacted, multi-recipient or uncorrelated reports are rejected.
Returned original Message-ID and recipient headers must match both the stored
sending domain and destination within the namespace. No incoming SMTP listener,
public webhook or provider mailbox poller is added by this adapter.

The `mail-feedback` helper forwards original bytes and acknowledges only after
commit; the operator-managed source collector retains durable custody and
failed inputs for retry or review. A source plus signed provider Message-ID
identifies the durable event even when retry adds unsigned Received headers.
Contradictory reuse fails closed. Existing report transactions preserve terminal
state precedence, suppression and submission fencing without another migration.
The feedback path cannot attest acceptance or request a resend. Reports may
drain while delivery is paused; raw evidence and provider diagnostics never
enter state, metrics or logs. Source enrollment and connecting live feedback
feeds remain operator work alongside external qualification.

A fifth default-off gate, `FAAS_INTERNAL_MAIL_RETENTION_ENABLED=1`, enables
payload retention. Its independent preview-only default must be explicitly
disabled before any content removal. The default quiet period is seven days
after every recipient reaches an allowed terminal state, with batches of 25
and an internal maximum of 100. Queued, active, relay-pending, uncertain and
missing-recipient evidence protects the whole payload. The private maintenance
route uses the full service credential and defaults to preview for each request.
An independent, bounded maild worker performs one pass at startup and hourly.

New messages store an HMAC of the canonical request, bound to namespace,
resource ID and the dedicated age identity. Cleanup decrypts and verifies
each candidate, backfills legacy verifiers, and atomically clears only the
encrypted payload while recording a prune timestamp. Message IDs, idempotency
keys, recipients, events, suppressions and domain keys remain. Identical retries
therefore retain their original identity after cleanup, and late feedback keeps
its correlation and replay evidence. A database reader cannot use the verifier
as a public hash to guess low-entropy message content.

Cleanup locks candidates and recipients, skips busy work, rechecks eligibility
and the gate, and rolls the entire batch back on verification or transaction
failure. Previews neither decrypt payloads nor mutate rows. Durable inventory
gauges and process-local pass health have an internal warning. No production
cleanup is activated by migration, unit generation or deployment defaults.
Migration rollback refuses to discard verifiers after payload removal; runtime
rollback disables further cleanup while preserving the upgraded replay path.
Payload removal is logical retention, with separate WAL/replica/backup policies.

The internal pilot additionally requires an exact approved-recipient allowlist
at admission and dispatch. An empty list denies admission and cannot enable
delivery. A configurable namespace budget defaults to ten recipient submissions
per UTC day, bounded by the internal maximum of 100 and enforced under the
existing admission lock. Only explicitly chosen platform callers opt in.

Monitoring reads durable namespace state instead of process-local counters.
The separate optional `mail-metrics` timer combines a private aggregate view
with the dedicated relay's read-only JSON queue inventory and atomically
publishes a node_exporter textfile. It runs as `faas:postdrop` with
`NoNewPrivileges`, retaining the Postfix queue-access group without changing
maild's identity or adding another listener. Raw inventory, addresses and mail
content are excluded from telemetry. Failures replace healthy samples with
explicit failure gauges; stale collection and stuck work have internal alerts.
The timer defaults off, requires the existing telemetry gates, and removes its
aggregate file when disabled through the role.

Messages and per-recipient outbox rows commit in one PostgreSQL transaction.
Admission locks the internal namespace to enforce idempotency and conservative
daily/pending safeguards. These are internal limits, not customer-plan quotas.
Message payloads and DKIM keys use dedicated age-encrypted resource-bound
envelopes. Domain verification checks ownership, the generated DKIM key, one
direct-IP SPF policy, and DMARC. A maintained signing library signs DKIM;
Postfix performs persistent SMTP delivery.

Claims have lease-token fences. A crash before submission may safely reclaim a
claim. A crash or lost SMTP response after submission begins becomes uncertain
and is not automatically resent. Authenticated reports support audited
reconciliation: accepted/bounced/complained observations, relay deferral without
resubmission, and retry only after confirming non-acceptance. SMTP acceptance
never attests inbox placement or exactly-once delivery.

The SMTP target is operator-selected numeric loopback only. Tenant egress
policy is unchanged. A relay's stable source IP, reverse DNS, permitted outbound
port 25, durable spool, and qualified local DSN routing are operator
prerequisites. The local bridge covers the relay's own observations; the private
provider adapter requires separately qualified inbound collectors and feedback
loop enrollment to supply original signed late bounces and complaints.
External delivery qualification and customer rollout are separate
work; flags stay off until an internal pilot is explicitly enabled.

## Validation

The environment-clone schema inventory classifies internal mail domains,
signing/encryption key state, producer access and delivery controls as platform
configuration. Delivery queues, leases, observations, billing notice receipts,
suppression/reputation history and recovery state remain operational; login
tokens and account lifecycle revisions remain account identity. None become
customer environment configuration or cloneable delivery authority. Registering
these passive schema additions keeps existing clone checks working with mail
disabled, without exposing internal mail resources in customer clone errors.
A migrated PostgreSQL handler test pins that default-off boundary.

Tests exercise disabled startup and routes, service credentials, DNS proof,
encrypted resource boundaries, DKIM verification, concurrent durable
idempotency, namespace isolation, quota admission, lease recovery, suppressions,
and positive, negative and ambiguous SMTP handoffs. Migration replay and sqlc
projection fidelity are checked against disposable PostgreSQL state.

An opt-in disposable Postfix fixture checks real DSN generation, pipe delivery,
relay retry, and queued reports surviving queue-manager restart. It has no
external network while running and establishes no external deliverability.

The existing `TestIssuedMigrationDeclarationsRespectTargetSchema` failure was
caused by listing an already unqualified issued migration among the sources
requiring public-declaration rewriting. Removing that stale adaptation entry
preserves the issued SQL bytes; a regression test pins preservation for public,
isolated and quoted schema names. The corrected source checks, fresh PostgreSQL
migration apply-and-walk, and schema-scoped guard tests pass. The unrelated full
migration suite previously reached its ten-minute timeout; selected replay and
schema-isolation evidence does not assert that every historical test ran.

Pilot tests cover concurrent daily budgets, allowlist withdrawal before SMTP,
durable metrics across store recreation, namespace isolation, default-off
monitoring, atomic failure publication, and closed private metric labels.
Prometheus fixtures cover all seven new internal alerts, disabled operation
and a healthy low-volume pilot. The real Postfix fixture also checks the queue
inventory with the optional helper's unprivileged identity and NoNewPrivileges.

Feedback tests use locally signed provider fixtures and synthetic DNS to verify
source and signature validation, exact mailbox and domain scope, strict MIME
correlation and parser bounds. PostgreSQL tests exercise concurrent replay,
lost-response recovery, namespace isolation, late reports after MX acceptance,
complaint precedence, temporary failures and submission fencing. All gates
remain off by default, and disabled feedback reads no source credentials/input
or network. These tests establish no live provider enrollment or deliverability.

Retention tests cover default-off configuration and credential scope, bounded
read-only previews, every protected recipient state, mixed recipients, fresh
observations and missing evidence. PostgreSQL tests verify idempotency across
pruning and store recreation, legacy backfill, late feedback and suppression,
concurrent batches, busy locks, eligibility rechecks, corruption, key mismatch,
cancellation and gate revocation. The additive migration replays, permits an
unused rollback and refuses rollback once content has been removed. Metrics and
Prometheus fixtures cover disabled, failed and stale retention operation.

Delivery-control tests cover first-handoff serialization, persistent nested holds,
stale revisions, replay after a newer resume and audit rollback. Race-enabled
workers verify cancellation before SMTP and protection during SMTP. Other cases
cover mixed-custody results, unchanged daily budgets, cancelled-message replay
after pruning, drain lease expiry and unknown custody, bounded CLI waits and
private aggregate metrics. Schema replay and rollback guards use disposable
PostgreSQL schemas. Default-off units and generated artifacts are checked.

Producer tests cover credential separation, own-message/event reads, exact sender
scope, concurrent idempotency and atomic budgets under namespace caps. They also
cover token rotation, isolated namespaces, cancelled replay after pruning,
withdrawn sender pairs, flag revocation during render, legacy-entrypoint fencing,
admission rollback, default-off configuration and the Unix-only platform transport.
The ownership migration is replayable and refuses an unsafe rollback.

Timing tests cover bounded admission, canonical replay after expiry/pruning,
restart before due, quota preservation, gate rollback, future-age monitoring and
metadata integrity. Race-enabled tests cover expiry during rendering, lock waits
and stale worker settlement. Loopback SMTP peers test expiry during greeting,
envelope and DATA readiness, post-DATA ambiguity and positive acceptance. Native
expiry preserves submitted/uncertain custody and avoids failure-rate inflation.
Migration replay and legacy identity preservation use disposable PostgreSQL state.

Domain-pacing tests use concurrent workers with different sending domains to
verify one shared recipient-domain reservation and attempt-free queue waits.
They cover restart and runtime rollback, real cooldown expiry, independent
domains/namespaces, scheduled work, message/lease/authorization expiry across row-lock waits,
reservation rollback, producer budgets, holds, cancellation and ambiguous SMTP
reconciliation. Monitoring tests verify default-off reads and failure privacy;
the additive migration is replayed with legacy identities and guarded rollback.

Relay backpressure tests cover default-off credentials and IO, whole-relay
inventory, greeting-only probes and cancellation, strict telemetry role/payload
validation, replay/reordering and fresh continuous recovery. PostgreSQL tests
exercise boot holds, restart, namespace isolation, durable transition pagination,
gate rollback, expiry with SMTP disabled, concurrent sample retries, holds during
rendering and stale/aging evidence after pacing lock waits. In-flight uncertainty,
late reports, operator holds, queued cancellation and budget accounting retain
their fences. Migration replay/rollback guards and failure-private metrics use
synthetic data and local fixtures; no live deliverability is asserted.

Reputation tests cover minimum samples, exact rate boundaries, authenticated
feedback deduplication under concurrent retries, late reports, complaint precedence
and worker reevaluation without new reports. PostgreSQL cases cover both scope
fences, restart, reviewed release/replay, fresh evidence reopening, rolling-window
aging, namespace isolation, release authorization, atomic gate rollback, queued
cancellation and pacing rollback during rendering. Default-off CLI/metrics checks
verify no credential/state reads; additive schema replay and guarded rollback
use disposable local state. No external email, DNS mutation or deployment occurs.

Event-feed tests cover late and uncommitted source/projection transactions,
concurrent bounded publication, contiguous durable ordering, restart, immutable
pending batches and acknowledgment replay after newer progress. They also cover
consumer budgets, producer/namespace boundaries, token rotation and withdrawal,
generation-fenced audited rewinds, default-off IO and atomic gate revocation.
Local Unix HTTP tests drop responses after commits and fail consumer handlers to
exercise at-least-once recovery. Native SQL lifecycle events and authenticated
feedback enter the feed without decryption; schema replay/rollback guards and
failure-private lag metrics use disposable fixtures only.
