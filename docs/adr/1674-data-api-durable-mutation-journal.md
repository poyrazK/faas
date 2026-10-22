# ADR-1674: Durable client mutation recovery

Status: Accepted

## Context

V2 transactions retain original success receipts independently of business state
(ADR-1673). Their client keeps an immutable prepared operation but leaves storage
to the application. Reloads or crashes can lose the key/payload/context needed
for recovery. Multiple browser tabs can also overwrite local pending state or
dispatch the same initial action without coordination.

## Decision

Add an opt-in Data SDK mutation journal with an application-selected storage
adapter and lossless application parser. Each stable application slot stores a
versioned canonical JSON record containing a random revision, state and complete
original prepared operation. Bind it to the API URL, optional schema fingerprint,
contract, tenant, actor and original UUIDv7. No token, header, fetch callback or
SDK client is supplied to storage. Application parsers must project/reject fields
outside the intended prepared operation.

Use three states: prepared, uncertain and confirmed. Preparing inserts only into
an empty slot. Dispatch atomically changes prepared to uncertain and awaits the
storage commit acknowledgement before calling the explicit request callback.
Only one tab can claim that revision. Lost/failed storage acknowledgements issue
no request; a committed uncertain marker remains available for recovery. Request
exceptions or transport failures keep the original operation. There is no lease
that expires into automatic replay, no automatic key replacement and no volatile
fallback. Existing direct transaction methods remain available separately.

Loading sends no request. A new instance validates bounded plain JSON, record
version, exact context, key and the application parser before issuing a fresh
immutable handle. Parsers must return the same canonical JSON: restore cannot
silently normalize inputs, drop fields, upgrade versions or generate another key.
Reject asynchronous/lossy parsers and malformed or unknown records without edits.
Canonicalization preserves array order, rejects nonfinite numbers, cycles,
accessors, custom prototypes and non-JSON values, and never calls toJSON.

Recovery explicitly invokes an application receipt-read callback. Use the existing
SDK exact receipt classifier and full application matcher/type guard. Missing,
conflicting, failed, malformed or expired results keep the record untouched.
Expiry is determined by PostgreSQL, not the client clock. A confirmed original
receipt can atomically mark the local record confirmed. If that marker cannot
be saved or another tab has advanced the revision, return the validated receipt
with a separate persistence outcome; never hide proven server success behind a
local storage failure. Confirmation is historical, not current business state.
Only confirmed handles can explicitly remove local records. Removing them does
not delete server receipts. Unknown outcomes have no automatic pruning path.

Storage compareExchange compares exact prior serialized bytes and commits its
replacement/removal atomically across all users. Revision nonces prevent stale
handles from deleting a newly prepared operation in a reused slot. SDK handles
are scoped to the journal instance and must be reloaded after restart. Custom
adapters own their atomicity, durability and aggregate budget guarantees; a
localStorage read/write pair is not a valid implementation.

Provide a dedicated IndexedDB adapter with lazy opening, strict read/write
transaction durability and one object store. It serializes compare-and-swap and
aggregate count/byte accounting inside the same transaction across tabs. It
rejects unavailable/blocked/closed/version-changed storage, quota failures and
unsupported strict durability. It never evicts another operation to make room.
The database name is explicitly selected by the application. As with IndexedDB,
origin storage must be retained; clearing/evicting it or losing the device removes
the local copy. This is local recovery storage, not server authorization or a
backup against origin/device loss.

Central defaults/hard bounds are 100 records, 8 MiB stored values, 1 MiB per
serialized record, 256 UTF-8 bytes per slot/database name and JSON depth 64. Mirror
these in SDK constants under a parity check. IndexedDB callers may lower record/
byte budgets. Per-record parsing/serialization bounds apply to custom stores too.
Storage errors expose fixed codes without original exceptions or payloads.

Extend only the v2 transaction generator with a strict restore parser and
persistentTransactionClient. It composes prepare/load, explicit first dispatch,
completed-response reconciliation, explicit read-only recovery and confirmed
removal with the existing typed RPCs and request deadline helper. Its projection
rejects extra credentials/fields and preserves canonical SKU/quantity pairs.
Token providers must remain scoped to the captured identity. V1 templates and
all existing SQL RPCs/migrations remain unchanged. Applications choose a stable
slot and explicitly adopt storage; no default browser persistence is introduced.

## Consequences

Applications can retain the exact operation through reloads and coordinate its
first dispatch across tabs. They must choose storage and a stable slot, retain
the original identity/contract, and handle unresolved outcomes and local marker
failures explicitly. Restoring pending work does not authorize a new write.
Prepared payloads are persisted intentionally, so applications choose appropriate
origin, retention and payload handling. No generic SQL, background worker,
automatic polling, cross-request transaction or new server endpoint is added.

## Validation

SDK unit/inference tests cover immutable round trips, single-claim races, failed
and lost commit acknowledgements, callback exceptions, receipt outcomes, local
marker failures, stale removal, context mismatches, corruption, lossy/async
parsers and bounded safe JSON. Native acceptance compiles actual CLI output and
uses two Chromium tabs with real IndexedDB and PostgreSQL/PostgREST: a committed
response is lost, the page reloads, the same operation is restored with no request,
and explicit receipt recovery confirms it. It also verifies cross-tab CAS,
stale deletion, storage unavailability, count/byte quotas, corruption, changed
context and retained expiry uncertainty. Legacy transaction and CLI checks remain
regression coverage; limits have an SDK/Go parity gate.
