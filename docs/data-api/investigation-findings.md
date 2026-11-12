# Data API investigation backlog

Investigated checkout: `2616a7170a80a08bb3b143535ec31b9939aaa5bb`
(2026-11-12). These findings are open; this investigation adds documentation
and manual reproductions only. Stable IDs let later fixes retain the original
evidence and record their resolution. Locations refer to the investigated
checkout.

P1 means prioritize correctness or recovery work. P2 means a bounded correctness
gap, operational improvement, or scalability work. An enhancement or a suspected
performance bottleneck is not a confirmed production defect.

## Finding index

| ID | Priority | Evidence | Finding | Status |
| --- | --- | --- | --- | --- |
| DAPI001 | P1 | Reproduced | Terminal authorization errors retain live-query rows | Open |
| DAPI002 | P1 | Reproduced | Lost upload-start responses hide the generated recovery ID | Open |
| DAPI003 | P1 | Worker reproduction + SQL review | Repeatedly failing oldest jobs can delay the queue | Open |
| DAPI004 | P2 | Documented operational gap | Storage garbage collection and job-result retention require owner implementation | Open |
| DAPI005 | P2 | Code review; load testing needed | Shared-budget admission serializes unrelated subjects on the app row | Open |
| DAPI006 | P2 | Reproduced; documented limitation | Lossless decoding cannot preserve JSON numeric types on write-back | Open |
| DAPI007 | P1 | Reproduced | Plain CSV import silently rounds numeric values before approval | Open |
| DAPI008 | P1 | Failure injection + gate review | Failed access-safety checks admit requests using indefinitely stale metadata | Open |
| DAPI009 | P2 | Reproduced | Transaction-visibility waits ignore pre-cancellation and retain abort listeners | Open |
| DAPI010 | P2 | Reproduced | Late credential lookups can replace a fresh subscription after reconnect | Open |
| DAPI011 | P2 | Reproduced + server expiry review | Temporary credential failures can leave live reads stale indefinitely | Open |
| DAPI012 | P2 | Reproduced with a supported serving snapshot | Cross-schema RPC results produce dangling OpenAPI references | Open |
| DAPI013 | P2 | Reproduced | OpenAPI conversion treats field names as schema keywords | Open |
| DAPI014 | P2 | Normalizer/codec reproduced; database integration pending | Arrays of PostgreSQL domains decode differently from generated types | Open |
| DAPI015 | P1 | Reproduced through the HTTP gateway | Requests pass the old RLS gate before a successful restore safety refresh | Open |

Recommended first fixes: DAPI015, DAPI008, DAPI001, DAPI002, and DAPI007. DAPI003
should follow with recovery-aware queue scheduling. Treat DAPI005 as a benchmark-led
performance task, DAPI004/DAPI006 as operational/codec enhancements, and validate
DAPI014 against PostgreSQL before choosing its wire-contract change.

## DAPI001 Terminal authorization errors retain live-query rows

`sdk/data/src/live/client.ts:466` calls `finish(frame)` for a closed error.
`finish` at line 485 spreads the existing state and changes only `status` and
`error`; rows, keys, columns, snapshot, the protocol result, and parsed row cache
remain. React identity-scope changes clear the previous subscription, but an
authorization failure does not require such a scope change.

Evidence: deliver one `live.rows` frame containing a private row, followed by
`live.error` with `code: 'forbidden'` and `closed: true`. `getSnapshot()` reports
`status: 'closed'` and still returns the private row. This concerns cached UI
data; the server has closed the subscription.

Remediation direction: clear observable rows, keys, snapshots, and internal
caches on authorization-related terminal errors. Decide explicitly whether
non-authorization closures should retain stale results. Cover the hook and SDK.

## DAPI002 Lost upload-start responses hide the generated recovery ID

`sdk/data/src/storage-uploads.ts:49` generates a session UUID inside
`startUpload`. `uploadResumable` at line 64 does not expose it until the request
returns successfully and `onSession` runs at line 66. If creation commits but
the response is lost, callers have no ID for `getUpload` or reconciliation.

Evidence: inject a network failure after recording each start request payload.
Two calls to `uploadResumable` create two different UUIDs; the `onSession`
callback runs zero times. Repeating this can consume the 16 active-session
allowance until abort or expiry. Low-level `startUpload({ id })` allows an
explicit ID, but the convenience method's documented flow does not.

Remediation direction: generate and persist the ID before the first request;
support retrying/reconciling that exact ID after uncertain creation. Include the
ID in recoverable errors and distinguish starting a supplied ID from resuming
an existing session.

## DAPI003 Repeatedly failing oldest jobs can delay the queue

`cmd/gregale/templates/data-api-teams/workers/data-jobs.mjs:37` rethrows network
and HTTP 5xx errors, ending the entire worker invocation. Migration
`0013_background_data_jobs.sql:70` claims eligible work in creation order,
including expired running jobs. There is no attempt counter, next-attempt
schedule, backoff, or quarantine state.

Evidence: three simulated scheduled invocations after lease expiry, each
claiming the same oldest job and receiving HTTP 500 during advancement, each
stop before a subsequent claim. SQL review establishes that the oldest expired
job becomes eligible first again. This can starve newer jobs when the recurring
failure is specific to that job rather than a general database outage.

Remediation direction: reconcile saved progress and receipts before retrying
uncertain work; add bounded retry scheduling and attempt diagnostics. Let other
eligible jobs proceed while preserving the existing no-assumed-rejection rule
for ambiguous commits.

## DAPI004 Storage garbage collection and result retention require owner implementation

`docs/data-api/workflows.md:64` explicitly requires owner cleanup of expired
chunks, abandoned objects, and replaced versions. Line 128 requires a retention
policy for completed job history. `storage.mjs:255` performs best-effort deletion
without a durable cleanup queue; `storage-sessions.mjs:124` removes chunks after
completion but has no automated collection of expired sessions or old object
versions. Completed export results remain in the customer database.

Evidence: code and documented behavior, rather than an unexpected contract
violation. Repeated uncertainty, abandoned sessions, and replacements can retain
unused bytes; repeated exports retain snapshots without an aggregate retention
policy.

Remediation direction: provide owner-controlled cleanup with dry-run reports,
grace periods, reference verification, retryable deletion, receipt retention, and
result expiration. Never infer safe deletion from an inconclusive RLS read or
delete chunks belonging to a live session/composition.

## DAPI005 Shared-budget admission serializes unrelated subjects on the app row

`pkg/state/dataapibudget/store.go:39` starts a transaction and locks the app row
using `queries.sql:1` before reading database time, pruning, and consuming a
budget key. Every subject/tenant for the same app contends for that row; denied
admissions take the same path. The lock also shares the application's intent
row with unrelated application operations.

Evidence: lock-path review. Existing cardinality bounds and pruning are present;
this is not a claim of unbounded budget storage. No throughput or latency limit
has been measured in this investigation.

Remediation direction: benchmark contention under concurrent subjects and
replicas. Keep strict atomic per-key spending, app ownership checks, deletion
semantics, and the 4,096-key cap while moving capacity bookkeeping off the hot
app row or avoiding the global lock for existing keys.

## DAPI006 Lossless decoding cannot preserve numeric JSON types on write-back

`sdk/data/src/numeric.ts:5` converts each numeric token into a plain string.
`docs/data-api/workflows.md:87` documents that this does not preserve numeric
versus string token types for writes.

Evidence: decoding `{"numeric":9007199254740993,"text":"9007199254740993"}`
produces identical strings for both fields. Serializing that object writes both
as JSON strings. Precision is retained, but token identity cannot be recovered.

Remediation direction: consider an opt-in tagged decimal representation and
matching serializer, including nested JSON, arrays, REST, RPC, and live reads.
Preserve the existing string codec for compatibility; do not guess which
numeric-looking strings should become numbers.

## DAPI007 Plain CSV import silently rounds numeric values before approval

`cmd/gregale/templates/data-api/csv-import.mjs:28` validates numeric syntax and
immediately calls `Number(text)`. The plain-mode JSON fallback at line 35 uses
`JSON.parse` before validating nested JSON. Both lose original numeric lexemes.
`validateRecord` only enforces safe integers when the descriptor declares an
integer; ordinary decimal and JSON numbers can pass after rounding/underflow.

Evidence: previewing a numeric CSV cell `0.10000000000000001` in the default
plain mode returns `ok: true` with `{"amount":0.1}`. The same cell in JSON mode
is rejected with `precision_loss` by the existing `decodeJsonCell` helper.
The approved import payload has already changed before any database request.

Remediation direction: preserve numeric lexemes until exactness is checked in
plain mode too, including numbers nested in JSON cells. Reject unrepresentable
values with `precision_loss`, or implement an explicit exact-number write path;
do not silently round. Cover underflow, long decimals, and large JSON integers.

## DAPI008 Failed access-safety checks admit requests using stale metadata

`cmd/gregale/templates/data-api/access.mjs:265` catches catalog-read failures
without propagating a failed result, and line 266 advances `checkedAt` even on
failure. `ready` at line 282 then resolves `true` after a failed restore check.
Periodic checks keep previously cached `rowSecurity` and public-allowlist safety
flags indefinitely if every reread fails.

Evidence: seed `api.notes` as row-secured, inject a failing access reader, call
`restored()`, and await `ready(100)`: readiness returns true. A later failed
refresh still leaves `rowSecurityDenial` returning null for that relation.
The existing test at `test/access.test.mjs:452` also expects failed restore
checks to report ready, but starts from an already unsafe cached state and
does not cover stale-safe metadata.

Impact requires both a safety-changing migration and a failed safety refresh.
The comment assumes that a failed catalog connection also prevents PostgREST
from reaching the database. That does not cover, for example, catalog-query
failure or new-connection exhaustion while the PostgREST pool retains working
connections. Disabled RLS could then remain accepted beyond the intended
recheck interval. The reproduction injects this partial failure; it does not
claim that a production data leak occurred.

Remediation direction: track the last successful safety check separately from
attempts. Require a successful restore check and define a bounded stale-state
policy for regular requests, with an explicit unavailable response when the
safety decision cannot be refreshed. Preserve observation of healthy routes
without treating an unknown authorization guard as a successful check.

## DAPI009 Transaction-visibility waits mishandle cancellation and cleanup

`sdk/data/src/live/client.ts:431` registers an abort listener in `awaitTxId`
without checking whether the signal is already aborted. The anonymous listener
at line 438 is never removed when a wait resolves from a snapshot or rejects
because its query closes. Reusing a signal retains each waiter closure after
the work has finished.

Evidence: after an initial snapshot `1:2:`, call `awaitTxId(99)` with a signal
aborted before the call: the promise remains pending. Separately wait for
transaction 5 with a live signal, deliver progress `1:10:`, and await success;
Node's `getEventListeners(signal, 'abort')` still returns one listener.

Remediation direction: reject pre-aborted pending waits immediately, use a named
abort handler, and remove it on every resolve/reject/close path. Test independent
waiters sharing a signal and transaction progress racing cancellation.

## DAPI010 Late credentials can replace fresh subscriptions after reconnect

`sdk/data/src/live/client.ts:422` awaits a credential, then checks only whether
the query is finished and whether some socket is open. It does not bind the
lookup to the connection/attachment generation that started it. A lookup begun
on a dead socket can therefore publish on a replacement socket.

Evidence: hold the first token lookup pending, close its socket, reconnect, and
return `fresh-token` from the second lookup. Resolve the old lookup afterwards.
The replacement socket receives two `live.subscribe` frames for the same ID,
first with `fresh-token` and then with `old-token`.
`pkg/livequery/liveproxy/proxy.go:229` and `liveserver/hub.go:429` replace an
existing subscription when its ID is reused, so this can undo the fresh
subscription; an expired old credential can instead close it with a refusal.

Remediation direction: attach an immutable generation/socket identifier to
credential work, invalidate it on disconnect/re-attach, and discard stale
completions. Apply the same ordering discipline to concurrent renewal work.

## DAPI011 Temporary credential failures can leave live reads stale indefinitely

`sdk/data/src/live/client.ts:503` converts token-provider rejection to undefined.
The scheduled renewal at line 523 returns without scheduling another attempt
when it has no credential. A `seal_expired` frame triggers one further attempt,
but `pkg/livequery/liveserver/hub.go:332` emits that frame only on the transition
to stale, not repeatedly.

Evidence: let initial subscription succeed, reject the early renewal lookup,
then reject the lookup triggered by `seal_expired`. Restore the token provider
and advance a fake clock another two minutes. There are still only three token
calls, zero remaining timers, and the subscription remains `stale` while its
socket stays connected.

Remediation direction: schedule bounded credential-refresh retries with backoff
and cancellation tied to the current attachment. Stop retries when a query
closes, and keep terminal authorization failures distinct from transient
provider/network errors. Exercise recovery without forcing a socket reconnect.

## DAPI012 Cross-schema RPC results produce dangling OpenAPI references

`cmd/gregale/templates/data-api/types.mjs:421` supports an approved RPC returning
rows from a table in another exposed schema. `enrichOpenAPI` emits table
definitions only for the selected profile at line 494, but `contractSchema` at
line 461 still emits a reference to the result table's schema. Including all
composite definitions does not include the missing table-row definition.

Evidence: a serving snapshot with schemas `api` and `other`, table
`other.notes(id)`, and `api.list_other()` returning
`Database["other"]["Tables"]["notes"]['Row'][]` produces the response reference
`#/components/schemas/other.notes.Row` in the 3.1 document while no such schema
exists. The reproduction uses the supported normalized shape, not a deployed
PostgREST instance. The Swagger document has the same missing target.

Remediation direction: emit the reachable row/composite definition closure for
approved result and argument references, while restricting HTTP paths to the
selected profile. Validate that every internal reference resolves in both
Swagger 2 and OpenAPI 3.1 outputs, including cross-schema RPC return types.

## DAPI013 OpenAPI conversion treats field names as schema keywords

`cmd/gregale/templates/data-api/openapi31.mjs:5` recursively processes every
object with the same schema-keyword rules, including `properties` dictionaries
and literal defaults/examples. At line 11 a dictionary field named `$ref` is
sent to `rewriteRef` even when its value is a field schema object. At line 10 a
field named `x-nullable` is removed as if it were the nullable extension.

Evidence: converting a Row schema containing the legal field name `$ref` throws
`TypeError` when `.replace` is called on its schema object. A field named
`x-nullable` is silently missing from the converted Row properties. These names
can occur in quoted PostgreSQL columns or JSON RPC argument objects.

Remediation direction: traverse schema keywords, maps of named schemas, and
literal values using separate contexts. Rewrite genuine reference strings and
nullable extensions without changing data/field names. Add equivalent checks
for parameter maps, example objects, and schema defaults.

## DAPI014 Arrays of PostgreSQL domains disagree with generated types

`cmd/gregale/templates/data-api/types.mjs:351` resolves domain base types during
normalization, including an array's domain element at line 357. The wire column
mapping in `pkg/livequery/compile.go:158` unwraps a top-level domain but does not
describe an array element's domain base. An array of domain `cents` is therefore
named `_cents`; `sdk/data/src/live/client.ts:189` then asks the element decoder
to parse `cents`, which falls through to the raw string at line 200.

Evidence: normalize an `int4` domain `api.cents` and its array column: the
generated column type is `(number | null)[]`. Decode `{1,2}` with the corresponding
wire type `_cents`: the live values are `["1","2"]`, unlike REST JSON's numbers.
This was reproduced at the normalizer/codec boundary. PostgreSQL integration
was not run because the local test database was unavailable; verify the real
column descriptor and end-to-end behavior before changing the protocol.

Remediation direction: carry normalized array-element/base-type metadata or
normalize the selected wire expression to a documented built-in representation.
Cover arrays of numeric, boolean, JSON, and enum domains in REST/live parity
tests; preserve null elements, multidimensional arrays, and lossless mode.

## DAPI015 Requests pass the old RLS gate before restore safety refresh completes

`cmd/gregale/templates/data-api/server.mjs:225` runs `rowSecurityDenial` before
waiting for `config.lifecycle.ready` at line 262. The lifecycle wait includes
the restore access-safety refresh (`server.mjs:523`), which can change the cached
RLS flags from safe to unsafe. That request's guard is never reevaluated before
SQL proxying at line 303. Anonymous target safety is likewise selected at line
142 before this wait. This issue occurs even when the safety refresh succeeds;
it is independent of failed-check handling in DAPI008.

Evidence: start the gateway with `api.notes` cached as row-secured and hold the
restore safety read pending. Send an authenticated GET and wait for it to reach
the lifecycle readiness wait. Complete the safety read successfully with
`api.notes` now marked unprotected. The waiting request reaches the stub SQL
upstream and returns HTTP 200, although calling the current RLS guard then
returns `403 rls_required`. The retained reproduction uses real loopback HTTP
servers and a stub SQL upstream, not a customer database.

Impact: requests admitted while a restored instance still holds its pre-park
metadata can read an RLS-disabled relation that the successful refresh would
deny. A later request observes the new denial; the requests already past the
old guard can still be dispatched. Coordinated refresh or database permissions
may independently block a particular deployment, but this race exists in the
normal uncoordinated runtime path.

Remediation direction: complete required restore safety refresh before making
row-security/public-target decisions, or reevaluate those decisions immediately
after the wait and before dispatch. Cover user and anonymous paths with a
delayed safe-to-unsafe refresh and assert zero upstream requests. Preserve the
bounded readiness/deadline and unrelated route behavior.

## Reproduction and validation

The initial investigation ran 14 focused runtime/worker tests successfully;
the expanded access/OpenAPI/storage/budget/worker selection passed all 47 tests.
The full Data SDK suite also passed all 107 tests, including its TypeScript
build, declaration checks, and inference checks. The retained manual evidence
ran successfully for all 13 executable finding IDs.
The retained [manual reproduction script](investigation-repros.mjs) records
DAPI001–DAPI003 and DAPI006–DAPI015 using fake transports/clocks, local loopback
servers, supported snapshot fixtures, and the current SDK source transpiled by
TypeScript.
DAPI004/DAPI005 are code/documentation findings without a runtime reproduction.
Performance claims have not been load-tested. No hosted deployment or customer
data was changed.

From the repository root, with Node 24+ and the existing SDK/runtime development
dependencies installed, run:

```sh
node docs/data-api/investigation-repros.mjs
```

To reuse dependencies already installed in another checkout, set
`GREGALE_DATA_API_REVIEW_DEPS` to that checkout's absolute path. This environment
variable selects development dependencies only; the sources under investigation
always come from this checkout. The script creates and removes its own temporary
compilation directory and makes no external network/database calls.

The assertions deliberately record the investigated defects. They are manual
evidence, not an acceptance gate that should keep passing after fixes. When
resolving an ID, add a regression test asserting the corrected behavior, record
the resolution here, and retire or update the corresponding evidence assertion.
The two fixed `data_api_access_recheck_unavailable` log events are expected from
the injected catalog failures in DAPI008.
