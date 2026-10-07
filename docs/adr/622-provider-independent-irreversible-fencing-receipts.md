# ADR-622: Provider-independent irreversible fencing receipts

Status: accepted · 2026-10-07

## Context

ADR-615 preserves historical public startup withdrawals until their irreversible
local admission fence reports known zero activity. An unreachable process cannot
produce that observation. ADR-619–621 provide selected topology, host, service
and activation audits, but those observations cannot prevent future execution.
A vanished process, inactive service, persistent mask or network block cannot
clear a historical withdrawal. Define durable signed evidence before choosing
an external enforcement provider.

## Decision

Add private `pkg/runtimefence` and `RuntimeUpgradeExternalFenceStore` PostgreSQL
seams. There is no production signer, provider client, daemon/CLI/customer API,
host mutation, network mutation or automatic issuer enrollment. Existing private
flags remain default off; public `execution_available=false` stays unchanged.

Support exactly one assertion, `irreversible_host_epoch_termination_v1`. A
reviewed external authority must permanently terminate the **entire execution
epoch** of the identified host boot: every process and namespace, service/socket
activation, pending and existing connections, and every mechanism that could
resume its process memory or execution snapshots. It must never allow that epoch
to execute again. A new host boot is a separate epoch and needs separately
reviewed startup membership. Stopping, powering off, freezing, masking units or
temporarily isolating a network is insufficient. A leased/reversible fence has
no supported receipt contract; the permanent receipt has no expiration or
unfencing operation. This contract deliberately excludes network-only adapters.

Private administrative review pins an authority UUID to one canonical Ed25519
public key. Reject malformed, noncanonical and small-order keys using the
existing module graph's `filippo.io/edwards25519` v1.2.0 point validation and the
standard library signature verifier. Keys cannot change or be deleted, nor can
one key be enrolled under another UUID. Revocation is one-way, database timed
after acquiring the authority lock. Key rotation requires a new authority ID
and key; it cannot silently change existing intent attribution.

Persist a review UUID and randomly generated challenge for one exact immutable
withdrawal, authority, current internal/public roster revisions, host machine
and boot IDs, external resource ID and selected scope SHA-256. Derive slot,
startup session and configuration digest from the locked withdrawal, never
from an external claim. The complete fixed-order JSON intent includes these
fields and its database-created microsecond timestamp; signers bind its SHA-256.
The caller-reviewed host/resource/scope binding is an administrative assertion.
A digest does not prove complete host inventory or authenticated startup-to-host
attribution; production qualification must establish those facts independently.

The fixed-order JSON envelope contains a version-1 claim and unpadded base64url
Ed25519 signature. The signed message is the UTF-8 bytes of
`gregale.runtime-upgrade.external-fence.v1` followed by a single NUL byte and the
exact `encoding/json.Marshal(Claim)` bytes. Claim field order is: `version`,
`contract`, `authority_id`, `receipt_id`, `intent_id`, `intent_sha256`, `challenge`,
`enforced_at_micros`, `issued_at_micros`. Envelope order is `claim`, `signature`.
Integers are signed decimal microseconds since the Unix epoch. The encoding is
this explicit contract, not a general-purpose JSON canonicalization standard.
Decoding and exact re-encoding equality reject duplicate/unknown/missing fields,
reordering, alternate escapes/casing, whitespace and trailing values. Require
canonical nonzero UUIDs and lowercase digests. Freeze key/envelope bytes and
copy receipt bytes returned to callers.

For a new receipt, lock the internal head, public head and exact withdrawal,
then share-lock its authority. Require both current heads equal the immutable
review and the issuer remain unrevoked. Sample database time **after every lock
wait**, rederive the intent and verify its pinned key and challenge. Enforcement
must occur at or after review creation, issuance at or after enforcement, and
issuance no later than the database observation and no more than 60 seconds old.
Bounds are centralized in `pkg/api/limits.go`: envelope ≤16 KiB, resource ID
≤256 ASCII characters, first-acceptance age ≤60 seconds. Store the exact signed
bytes, their SHA-256, receipt UUID and enforcement/issuance/database-observation
timestamps. No local activity version or local fence UUID is invented.

Exact accepted-byte retries for the same intent/withdrawal return the original
receipt and timestamp even after head changes, issuer revocation or elapsed
time. This is retrieval of previously accepted permanent proof, not a new
authorization. Different bytes or intent conflict. An exact intent-review retry
likewise returns its original challenge/time without granting a fresh receipt;
a new review after a head change needs a new UUID and current head pins.

Migration tables and triggers make authority history, intents and receipts
immutable except one-way revocation. Local and external receipt writers both
lock the withdrawal `FOR UPDATE`, preventing the two evidence families from
coexisting even through direct SQL. A local repair refuses a withdrawal with
external proof before invoking any admission-fence callback. Triggers recheck current heads, unrevoked
authority, exact withdrawal, time order, byte hash and actual post-wait database
clock. PostgreSQL does **not** verify Ed25519: the trusted Gregale store verifies
it before insert. Database writers/administrators remain trusted, as for existing
gateway facts; direct SQL is not an untrusted receipt ingestion surface.

Private coverage and the 64 unresolved-withdrawal bound exclude accepted
external receipts as well as local seals. Historical withdrawals remain forever
and still prevent old-session reenrollment. Resolving historical ingress does
not supply current-generation health/liveness/activity, VM quiescence, routing
cutover, runtime acceptance or artifact retirement authority. Clone schema
inventory classifies authorities/intents as platform configuration and receipts
as operational history; none becomes customer-cloned configuration or data.

## Consequences and limits

Signatures authenticate a pinned authority's assertion. They cannot prove its
claim is true, its key is protected, its provider operation is irreversible, or
its machine/boot/resource mapping is correct. No production authority is trusted
or connected in this slice. An unavailable issuer, invalid receipt or incomplete
review leaves the withdrawal pending. Selecting a provider-independent contract
preserves deployment freedom while setting a concrete bar for future adapters.

The next slice must qualify a real authority: authenticated startup/host-epoch
attribution, provider operation semantics covering every execution/resume path,
key custody and transport, enforcement completion before signing, replay-safe
receipt delivery, irreversible failure behavior and restart/recovery acceptance.
Do not expose generic signing of operator-supplied booleans as an adapter.

## Validation and adversarial review

Portable cryptographic tests use real generated Ed25519 keys and exercise every
intent binding, unsupported temporary contracts, time boundaries, malformed
keys, wrong keys/domain, noncanonical signatures/envelopes and frozen bytes.
Real migrated PostgreSQL tests cover immutable issuer/review history, revocation,
authentic but retargeted claims, exact durable retries, both head changes,
concurrent retries, post-lock clocks, committed revocation while waiting,
mutually exclusive local/external receipts, direct database guards, pending
capacity and permanent non-reenrollment. Migrated clone inventory must include
all new tables. Normal/race checks, SQLC regeneration, Linux amd64 compilation,
focused Linux lint and repository policy/migration gates are required locally.

Local validation passed 71 top-level tests in normal mode and the same 71 with
the race detector, with no failures or skips. These include 38 tests against
real migrated PostgreSQL and 15 external-fence store tests. Linux amd64 test
binary compilation passed for the verifier, state store and ingress packages;
focused Linux lint including tests reported zero issues. SQLC regeneration,
runbook SQL, text encoding, shell quoting and ADR number gates passed. A
read-only check of all 129 open pull requests found no migration-version
collision. No production issuer, host, daemon or customer traffic was changed.

Adversarial pass: reject powered-off snapshots, lease expiry, network-only
isolation, locally fabricated activity versions, stale keys/heads/challenges,
cross-host/boot/session claims, mutable keys/bytes, duplicate JSON keys, proof
age sampled before waits, refreshed retry timestamps and competing local seals.
Permanent history survives revocation because changing accepted evidence into
temporary evidence would violate its contract. That permanence makes honest
authority qualification essential before any production enrollment.

These local checks do not qualify enforcement, customer traffic or a production
provider. Native Caddy/DNS/systemd acceptance, Linux amd64 KVM `test-metal` and
final `leakcheck` remain prerequisites for larger runtime-upgrade enablement.

Primary references:

- [RFC 8032: Ed25519](https://www.rfc-editor.org/rfc/rfc8032)
- [Go Ed25519 verification](https://pkg.go.dev/crypto/ed25519)
- [Edwards25519 point encoding/cofactor validation](https://pkg.go.dev/filippo.io/edwards25519)
- [PostgreSQL row locks](https://www.postgresql.org/docs/16/explicit-locking.html#LOCKING-ROWS)
- [PostgreSQL actual clock versus transaction time](https://www.postgresql.org/docs/16/functions-datetime.html#FUNCTIONS-DATETIME-CURRENT)
- [PostgreSQL built-in SHA-256](https://www.postgresql.org/docs/16/functions-binarystring.html)
