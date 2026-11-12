# Data API investigation backlog

Investigated checkout: `2616a7170a80a08bb3b143535ec31b9939aaa5bb`
(2026-11-12). The original observations below are retained as historical
evidence. All 30 findings now have implementations and regression coverage on
`codex/data-api-investigation-fixes`, based on main `06fa38e06`. Stable IDs are
unchanged. Original line locations refer to the investigated checkout; the
resolution table describes current behavior.

The third review also compared these Data API source paths with upstream main
`10034cea1` (2026-11-12); the reviewed implementation paths were unchanged.
The fourth review checked main `f58166604` with the same result for the runtime,
usage-report, and starter migration paths examined in that pass.

P1 means prioritize correctness or recovery work. P2 means a bounded correctness
gap, operational improvement, or scalability work. An enhancement or a suspected
performance bottleneck is not a confirmed production defect.

## Finding index

| ID | Priority | Evidence | Finding | Status |
| --- | --- | --- | --- | --- |
| DAPI001 | P1 | Reproduced | Terminal authorization errors retain live-query rows | Fixed |
| DAPI002 | P1 | Reproduced | Lost upload-start responses hide the generated recovery ID | Fixed |
| DAPI003 | P1 | Worker reproduction + SQL review | Repeatedly failing oldest jobs can delay the queue | Fixed |
| DAPI004 | P2 | Documented operational gap | Storage garbage collection and job-result retention require owner implementation | Fixed |
| DAPI005 | P2 | Code review; load testing needed | Shared-budget admission serializes unrelated subjects on the app row | Fixed |
| DAPI006 | P2 | Reproduced; documented limitation | Lossless decoding cannot preserve JSON numeric types on write-back | Fixed |
| DAPI007 | P1 | Reproduced | Plain CSV import silently rounds numeric values before approval | Fixed |
| DAPI008 | P1 | Failure injection + gate review | Failed access-safety checks admit requests using indefinitely stale metadata | Fixed |
| DAPI009 | P2 | Reproduced | Transaction-visibility waits ignore pre-cancellation and retain abort listeners | Fixed |
| DAPI010 | P2 | Reproduced | Late credential lookups can replace a fresh subscription after reconnect | Fixed |
| DAPI011 | P2 | Reproduced + server expiry review | Temporary credential failures can leave live reads stale indefinitely | Fixed |
| DAPI012 | P2 | Reproduced with a supported serving snapshot | Cross-schema RPC results produce dangling OpenAPI references | Fixed |
| DAPI013 | P2 | Reproduced | OpenAPI conversion treats field names as schema keywords | Fixed |
| DAPI014 | P2 | Real PostgreSQL descriptor and SDK codec reproduced | Arrays of PostgreSQL domains decode differently from generated types | Fixed |
| DAPI015 | P1 | Reproduced through the HTTP gateway | Requests pass the old RLS gate before a successful restore safety refresh | Fixed |
| DAPI016 | P1 | Reproduced in generated Go and Python clients | Keyset pagination removes the caller's existing OR filters | Fixed |
| DAPI017 | P1 | Reproduced with loopback HTTP redirects | Generated clients follow redirects with credentials or resubmit mutations | Fixed |
| DAPI018 | P2 | Reproduced by rendering and compiling | Generated type-name allocation emits duplicate declarations | Fixed |
| DAPI019 | P2 | Reproduced; client expressiveness gap | Typed Go insert and RPC inputs cannot distinguish omitted values from SQL NULL | Fixed |
| DAPI020 | P2 | Reproduced in rendered Go code | Legal field names break Go struct tags or change JSON keys | Fixed |
| DAPI021 | P2 | Reproduced in generated Go and Python clients | Relation names are appended to URLs without path encoding | Fixed |
| DAPI022 | P2 | Reproduced in generated Python client | Generated table accessors are shadowed by client configuration attributes | Fixed |
| DAPI023 | P2 | Loader reproduction + write-path review | Local types output can overwrite a migration source file | Fixed |
| DAPI024 | P1 | Reproduced in authorization replay | Replay assertions and captures silently round large numeric IDs | Fixed |
| DAPI025 | P2 | Reproduced with controlled timers/transport | Initial live-access publication is not retried after identity lookup failure | Fixed |
| DAPI026 | P2 | Reproduced with delayed loopback HTTP | Health invalidation reuses an old in-flight readiness probe | Fixed |
| DAPI027 | P1 | Reproduced on PostgreSQL 16 | Keys-only change events include non-key covering-index columns | Fixed |
| DAPI028 | P1 | Reproduced on PostgreSQL 16 | Renaming a primary-key column makes change hooks emit a null key | Fixed |
| DAPI029 | P2 | Reproduced on PostgreSQL 16 | Migration transaction statements can commit schema without ledger entries | Fixed |
| DAPI030 | P2 | Reproduced in an isolated child process | A named-pipe migration file blocks file loading indefinitely | Fixed |

Recommended first fixes: DAPI015, DAPI008, DAPI001, DAPI002, and DAPI007. DAPI003
should follow with recovery-aware queue scheduling. Treat DAPI005 as a benchmark-led
performance task and DAPI004/DAPI006 as operational/codec enhancements. DAPI014
now has a real PostgreSQL reproduction; extend it to REST/live integration when
choosing its wire-contract change.
Include DAPI016/DAPI017/DAPI024 in the first correctness/security batch. Each ID
is independent; fixes should retain the original evidence and add a regression
assertion for the intended behavior.
Prioritize DAPI027/DAPI028 with the change-hook correctness work; neither is a
general event-retention or global-commit-order complaint.

## Resolutions

Implemented 2026-11-12. Setup and compatibility details are in
[workflows](workflows.md) and [ADR-20261112210000](../adr/20261112210000-data-api-investigation-hardening.md).

| ID | Current behavior | Regression coverage |
| --- | --- | --- |
| DAPI001 | Terminal closure clears observable state, protocol rows, parsed caches and initial-row references. | [Test](../../sdk/data/test/live.test.mjs) |
| DAPI002 | Persist an upload ID before dispatch; ambiguous creation exposes UploadSessionStartError.sessionId and supports explicit idempotent creation. | [Test](../../sdk/data/test/storage.test.mjs) |
| DAPI003 | Migrations 0014/0015 add delayed claims, saved attempt diagnostics, eight-attempt quarantine, authorized resume/cancel and safe history retirement. Ambiguous progress is reconciled. | [Test](../../tests/data-api/data-workflows.test.mjs) |
| DAPI004 | Owner maintenance provides registered-key inventory, shared-bucket ownership checks, dry-run reports, delete fences/leases, retries, retention and retired-ID protection. | [Test](../../tests/data-api/data-maintenance.test.mjs) |
| DAPI005 | Existing keys use shared ownership protection and per-counter locks. Only registration takes the capacity lock and prunes; strict limits and cardinality bounds remain. | [Test](../../pkg/state/dataapibudget/store_test.go) |
| DAPI006 | Opt-in exact types and immutable ExactNumber values preserve number/string identity through REST, RPC, live reads, cursor continuations and serialization. Legacy modes remain available. | [Test](../../sdk/data/test/numeric.test.mjs) |
| DAPI007 | Plain numeric and nested JSON CSV cells use the precision-checking decoder before producing any import payload. | [Test](../../tests/data-api/csv-import.test.mjs) |
| DAPI008 | Only successful safety reads renew a bounded lease; expired or restored leases fail closed and retry after a bounded interval. | [Test](../../cmd/gregale/templates/data-api/test/access.test.mjs) |
| DAPI009 | Visibility waits honor pre-aborted signals and remove listeners on resolve, abort and terminal closure. | [Test](../../sdk/data/test/live.test.mjs) |
| DAPI010 | Attachment generations and credential attempt ordering discard results from old sockets or superseded renewals. | [Test](../../sdk/data/test/live.test.mjs) |
| DAPI011 | Credential failures schedule cancellable exponential retries; expired seals are not reused as a failed-refresh fallback. | [Test](../../sdk/data/test/live.test.mjs) |
| DAPI012 | Cross-profile row definitions are included while endpoint paths remain restricted to the selected schema. | [Test](../../cmd/gregale/templates/data-api/test/openapi.test.mjs) |
| DAPI013 | Schema conversion traverses schema-valued keywords and dictionaries; property names and literal JSON remain data. | [Test](../../cmd/gregale/templates/data-api/test/openapi.test.mjs) |
| DAPI014 | Live result metadata recursively unwraps domains and their array elements to the base wire codec. | [Test](../../pkg/livequery/result_columns_internal_test.go) |
| DAPI015 | The gateway rechecks user and anonymous authorization after readiness and a successful safety refresh. | [Test](../../cmd/gregale/templates/data-api/test/access.test.mjs) |
| DAPI016 | Generated pagination preserves caller OR clauses and conjoins the cursor, while replacing ordering and removing offset. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI017 | Generated transports reject redirects, validate the API origin before reading credentials, and require HTTPS outside loopback. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI018 | Type families, enum constants and runtime symbols share deterministic collision allocation. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI019 | Go insert/update/RPC records support SetNull with field validation and conflicting-value checks, preserving omission/default behavior. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI020 | Generated custom JSON methods map exact database field names independently of Go struct-tag syntax. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI021 | Generated relation/RPC path segments are encoded exactly once; slash names fail explicitly. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI022 | Python table accessors reserve all instance configuration names and transport methods. | [Test](../../pkg/dataapi/clientgen/hardening_test.go) |
| DAPI023 | Generated output rejects migration-tree destinations, canonical symlink paths, and hardlink aliases before Docker or writes. | [Test](../../tests/data-api/dev-project.test.mjs) |
| DAPI024 | Saved requests, editor inputs and response captures reject unrepresentable numeric literals before comparison or capture. Exact replay IDs can use text projections. | [Test](../../tests/data-api/dev-requests.test.mjs) |
| DAPI025 | Access publication and initial cap totals remain retryable when identity lookup fails before an in-flight report exists. | [Test](../../cmd/gregale/templates/data-api/test/usage.test.mjs) |
| DAPI026 | Health invalidation cancels probes, invalidates pool observations and excludes pre-invalidation refresh results and snapshots. | [Test](../../cmd/gregale/templates/data-api/test/health.test.mjs) |
| DAPI027 | Primary-key metadata excludes INCLUDE attributes; old incompatible trigger arguments fail closed until the owner re-enables the hook. | [Test](../../tests/data-api/catalog-regressions.test.mjs) |
| DAPI028 | Capture validates the configured parent relation and current key/exclusion metadata; rename drift raises actionable SQLSTATE 55000 until re-enabled. | [Test](../../tests/data-api/catalog-regressions.test.mjs) |
| DAPI029 | Preflight rejects top-level transaction control before any migration content runs, while respecting SQL strings/comments/function bodies. | [Test](../../tests/data-api/catalog-regressions.test.mjs) |
| DAPI030 | Migration files and watch reads use bounded regular-file reads; migration loading refuses symlinks/FIFOs, checks cancellation and hashes original bytes. | [Test](../../cmd/gregale/templates/data-api-starter/test/migration.test.mjs) |

The independent-subject budget benchmark measured approximately 1.49 ms per
admission (4 workers, 16-connection test pool, disposable local PostgreSQL 16).
This is a local regression measurement, not a production capacity claim. The
concurrency test also holds the capacity lock and a shared app-row lock while
an existing subject proceeds. Atomic limit and key-cap checks remain covered.

Full CLI integration verification also caught stale customer command examples,
a missing normal-run synopsis for `data-api sync`, and the maintenance tool's
implicit optional schema and standalone hook-worker settings. Command references and help are
aligned with the current tree, and the template declares the schema knob as
optional. Existing CLI documentation/help and fresh-template doctor contract
tests cover these integration corrections.

The sections below preserve the original observations and remediation ideas.
They do not describe the fixed implementation.

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
This was initially reproduced at the normalizer/codec boundary. The fourth pass
also created a real PostgreSQL 16 `api.cents AS integer` domain and an array
column. The actual query field descriptor resolves to `_cents` using the live
server's catalog query. PostgreSQL `to_jsonb(amounts)` yields `[1,2]`, while the
current SDK decoder yields `["1","2"]`. This verifies the database descriptor
and codec mismatch; a full hosted realtime session was not run.

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

## DAPI016 Keyset pagination removes existing OR filters

`pkg/dataapi/clientgen/golang.go:487` removes both `order` and `or` from the
builder before applying cursor predicates. The generated Python client does
the same at `python.go:365`. The documentation says to keep the same filters
across pages, but an existing application `Or(...)` / `or_(...)` disappears,
including on the first page with no cursor.

Evidence: generate both clients, build a notes query with
`title.eq.private,title.eq.team`, and apply a page ordered by unique `id`.
The first-page request contains no `or` parameter. The next-page request
contains only the cursor's `id` predicate; the original title predicate is
still absent. The Go reproduction inspects real loopback HTTP requests; the
Python reproduction inspects the resulting builder parameters.

Impact: pagination broadens the requested result set and can undermine
application-owned scope filters. RLS remains an independent protection, but
it does not restore the omitted filter. The same Table type also exposes writes,
so callers must not treat pagination as preserving their mutation predicates.

Remediation direction: preserve caller filters and conjoin the cursor predicate
with them; replace only pagination-owned ordering/limits. Cover multiple OR
groups, AND/OR combinations, backward traversal, and the no-cursor first page
in both generated languages and compare with the TypeScript behavior.

## DAPI017 Generated clients follow credential-bearing redirects

`pkg/dataapi/clientgen/golang.go:741` creates a default `http.Client` without a
redirect policy; the configured client's redirect behavior is also unchanged.
`python.go:464` uses `urllib.request.urlopen`, whose default redirect handler
forwards the request's Authorization header. Neither generated transport pins
the response to the originally configured API origin. The TypeScript SDK uses
`redirect: 'error'` instead.

Evidence: point the generated Python client at a loopback endpoint that redirects
from `127.0.0.1` to `localhost` on another server. The second server receives
`Authorization: Bearer fake-token`. In the generated Go client, a 307 redirect
to another port on the same hostname forwards its bearer token and resubmits
the POST body: both source and target observe a POST. The source stub records a
simulated write before returning the redirect. All credentials were synthetic.

Impact: Python application JWTs/server keys can leave the configured host;
Go's default hostname-based header policy does not isolate origins by port and
does not prevent a redirected mutation from executing again. This contradicts
the generated runtime's stated no-automatic-write-replay behavior when the
original endpoint has already applied the operation.

Remediation direction: reject redirects by default and validate configured URLs
consistently with the TypeScript SDK. Any explicitly supported redirect mode
must not forward credentials across origins or replay an uncertain mutation.
Test 301/302/303/307/308, hostname/port changes, and HTTPS downgrade behavior.

## DAPI018 Generated name allocation emits duplicate declarations

`pkg/dataapi/clientgen/model.go:122` detects that `base + Row/Insert/Update`
collides with an existing type, but calls `types.take(base)` even when the bare
base has never been reserved. That can return the unchanged stem, after which
line 128 reserves the same colliding generated names. The runtime's full set of
type/global names is also not reserved at line 100.

Evidence: a valid contract with enum `api.notes_row` and table `api.notes`
successfully renders Go code containing both `type NotesRow string` and
`type NotesRow struct`. Compiling the generated package fails with
`NotesRow redeclared in this block`. Both are legal, distinct PostgreSQL
objects; the renderer reports success. The shared allocator also governs
Python's public generated type names.

Remediation direction: allocate and reserve the actual emitted declarations,
including runtime symbols, enum constants, composite/argument types, and all
table suffixes. Retry until the whole generated name family is free. Exercise
enum/table collisions, names that normalize to the same identifier, and runtime
names such as `CursorOrder` in generated compilation/import checks.

## DAPI019 Typed Go inputs cannot express explicit null separately from omission

`pkg/dataapi/clientgen/golang.go:127` represents an optional nullable insert
field as a pointer with `omitempty`; nil means omission and a non-nil pointer
can only encode a value. Optional RPC arguments at line 161 have the same
limitation. Required scalar RPC inputs do not gain the nullable argument form
that the TypeScript generator permits. Update types have `SetNull`, but insert
types and RPC argument structs have no corresponding mechanism.

Evidence: a generated optional nullable `title` field is `*string` with
`json:"title,omitempty"`. Marshaling `NotesInsert{Id: "1", Title: nil}` produces
`{"id":"1"}`, not a null assignment; no pointer value represents JSON null.
The optional `set_note(title)` argument has the same generated pointer/tag.
This is an expressiveness gap, not a claim that nil should stop selecting the
documented database default.

Impact: a nullable column with a non-null default cannot explicitly be inserted
as NULL through the typed struct, and an optional RPC cannot distinguish
explicit NULL from invoking its SQL default. The raw/untyped relation API can
work around the insert limitation, losing the generated input type. Generated
RPC methods still require their concrete argument structs; a custom HTTP call
or another client is needed for that workaround.

Remediation direction: retain omission semantics while adding explicit
unset/null/value input representation or narrowly scoped null setters for
inserts and RPCs. Verify wire bodies against columns/arguments with non-null
defaults and preserve generated update behavior.

## DAPI020 Legal field names break Go tags or change JSON keys

`pkg/dataapi/clientgen/golang.go:81` embeds the original field name in a Go
`encoding/json` struct tag. JSON tag syntax gives special meaning to commas and
`-`, and a backtick in the original name terminates the raw Go string used for
the tag. The generator accepts these names from valid PostgreSQL contracts.

Evidence: generated input fields for columns `a,b` and `-`, set to `comma` and
`dash`, serialize as `{"a":"comma"}`: the comma changes the key and the dash
field disappears. A legal column named `tick` followed by a backtick and `mark`
makes `RenderGo` fail Go parsing. Row decoding has the corresponding tag/key
problem. The manually generated schema is accepted by `dataapi.Parse`.

Remediation direction: encode original JSON names through custom marshaling
where standard tags cannot represent them, or reject unsupported names with a
clear generation diagnostic before emitting a misleading client. Include row,
insert, update, composite, and RPC argument fields in round-trip checks.

## DAPI021 Relation names are not URL path-encoded

`pkg/dataapi/clientgen/golang.go:510` and the write/GetInto methods concatenate
`t.name` into `/rest/v1/`; the generated Python table methods at
`python.go:378` do likewise. Their shared transports then parse the result as
a URL. A quoted PostgreSQL identifier containing URL syntax is therefore
interpreted as URL syntax rather than one relation path segment.

Evidence: render clients for legal table `api."odd?name"` and call its generated
accessor. Go and Python both send `/rest/v1/odd?name`, targeting relation `odd`
with query `name`, instead of `/rest/v1/odd%3Fname`. The probes inspect real
loopback HTTP requests. The runtime can identify the correctly encoded table
name, so this is a generated-client request-construction mismatch.

Remediation direction: path-encode relation names exactly once using each
language's URL utilities while keeping the structural `/rest/v1/` separators.
Cover spaces, question marks, fragments, percent signs, and non-ASCII names for
reads and writes. Keep unsupported slash/RPC routing cases explicit.

## DAPI022 Python table accessors are shadowed by configuration attributes

`pkg/dataapi/clientgen/python.go:155` reserves `rpc` and `table` when naming
generated accessors, but not the instance attributes assigned at line 419:
`base_url`, `access_token`, `server_key`, `subject`, `anonymous`, and `timeout`.
An ordinary relation with one of these names generates a method that is hidden
by the instance's string/callback/bool/number value.

Evidence: generate a contract containing `api.base_url`, construct `DataClient`,
and inspect `base_url`: the class has a generated callable accessor, but the
instance has a non-callable URL string at the same name. `client.base_url()`
therefore raises instead of constructing its typed table query. The untyped
`client.table("base_url")` remains available.

Remediation direction: reserve all public/runtime instance and class names when
allocating generated accessors, with deterministic suffixes and documentation.
Test collisions with every constructor attribute as well as inherited/runtime
methods, while retaining the original database name in the request.

## DAPI023 Local types output can overwrite a migration source

`cmd/gregale/templates/data-api/dev-project.mjs:70` checks type-output aliases
against configuration, seed, request collections, and several baseline/report
inputs, but not migration files. The nearby behavior-output guard at line 78
explicitly excludes the migrations tree. Types writing in `dev.mjs:493` and
the watch path at line 608 uses `atomicFile`, which replaces an existing regular
file after generating the client types.

Evidence: create a minimal valid development project whose `output` is
`migrations/sql/0001_notes.sql`. `loadDevProject` accepts it and returns that SQL
source as the types destination. Write-path review shows it is subsequently
passed to the normal atomic replacement routine. This probe does not run Docker
or overwrite a real project; it verifies the accepted conflicting path using
temporary fixtures.

Impact: a mistyped or inherited development configuration can replace an
applied migration's source with generated TypeScript, and the migration watcher
then reports a changed migration after the original file has been lost.

Remediation direction: reject types-output paths within the migrations tree
and aliases/hardlinks to migration sources before starting the local database
or writing any output. Apply the same input/output separation to other source
assets and preserve legitimate custom types destinations outside inputs.

## DAPI024 Replay assertions and captures round large numeric IDs

`cmd/gregale/templates/data-api/dev-requests.mjs:57` reads expected JSON values
with `JSON.parse`; `request-replay.mjs:62` does the same to actual responses.
`matchesJSON` then compares already-rounded numbers, and `captureValue` stores
the rounded number for later URL/body substitution. Exact matching and captured
resource identity are no longer exact for int8/high-precision numeric values.

Evidence: a request collection expecting JSON ID `9007199254740993` passes
against a response containing different ID `9007199254740992`. The replay
reports `passed: true` and captures `9007199254740992` for subsequent requests.
The probe uses a synthetic response; no mutation is sent. The expected-value
parse follows the saved-collection path's actual decoding behavior.

Impact: authorization/integrity replay can report success for the wrong row,
and capture-driven follow-up requests can select a neighboring resource.
SDK lossless mode and the CSV exact-number helpers do not cover this tooling
path. This is separate from DAPI007's CSV conversion and DAPI006's write codec.

Remediation direction: preserve numeric lexemes and token kinds in collection
expectations, response comparisons, captures, and substitutions, or reject
unrepresentable numeric values explicitly before running requests. Do not
convert numeric-looking strings heuristically. Cover int8 IDs, decimal values,
exponents, nested objects, and capture-to-path/body round trips.

## DAPI025 Initial access publication is not retried after identity lookup failure

`cmd/gregale/templates/data-api/usage.mjs:187` schedules the initial report when
access settings must be published. If the identity request at line 145 fails
before `inflight` is created, the `finally` block at line 175 reschedules only
when an in-flight report or request counts exist. It does not include
`accessDue()` in that decision. A quiet process then has no retry scheduled.

Evidence: create usage reporting with an access configuration, let the initial
scheduled identity lookup fail, and then restore the fake identity service.
There is one identity call, no access publication, and zero retry timers. An
explicit `report()` succeeds afterwards, proving that the remaining problem
is scheduling rather than continuing service failure.

Impact: reporting-based live configuration can remain missing or outdated until
ordinary HTTP traffic or another explicit flush/report triggers publication.
A client contacting the separate realtime service does not itself provide that
ordinary Data API HTTP traffic. Caps' initial totals lookup has the same
pre-report retry gap, although a later HTTP request also triggers recovery.

Remediation direction: keep a bounded retry scheduled while initial access or
cap-total synchronization is still due, including failures before report IDs
are assigned. Preserve the permanent-production-refusal behavior and avoid a
busy loop or duplicate usage deltas.

## DAPI026 Health invalidation reuses an old in-flight readiness probe

`cmd/gregale/templates/data-api/health.mjs:119` clears only the combined cached
check set. `probe` at line 35 retains its per-path pending promise, and the
refresh reader at line 79 similarly retains its pending read. A fresh check set
started after invalidation can therefore consume the pre-invalidation result.
This conflicts with the comment that both running and completed checks are
forgotten when the engine stops, starts, reloads, or restores.

Evidence: start `/ready` against a loopback admin server and hold its response.
Invalidate health and start another snapshot; then complete the old response
with HTTP 200 while new requests would receive 503. Both snapshots report ready,
and only one `/ready` request occurred. Invalidating again after that work
finishes produces a new probe and correctly reports unready.

Impact: a health answer for the previous engine/connection generation can be
reused and cached as if it described the new one. The reproduction covers the
health component with delayed HTTP, not a native VM snapshot. It does not claim
that the separate request lifecycle gate is bypassed.

Remediation direction: associate pending probe/refresh/pool work with an engine
generation, invalidate or cancel that work on lifecycle changes, and prevent
late completions from populating the new generation's readiness cache. Preserve
bounded concurrent probing within each generation.

## DAPI027 Keys-only change events include non-key covering-index columns

`cmd/gregale/templates/data-api/change-hooks.sql:62` constructs `key_columns`
from every attribute in the primary index's `indkey`. PostgreSQL primary keys
can have `INCLUDE` columns after the actual key attributes. The query does not
restrict positions to `pg_index.indnkeyatts`, so those non-key values enter
`row_key` and are treated as primary-key columns by the exclusion check.

Evidence: on PostgreSQL 16 create a table with
`PRIMARY KEY(id) INCLUDE(included_secret)`, enable hooks with `payload => 'keys'`,
and insert a row. The emitted key is
`{"id":1,"included_secret":"not-a-primary-key"}` although only `id` belongs to
the key constraint. Trying to exclude `included_secret` is rejected with
`22023`, claiming that primary-key columns cannot be excluded.

Impact: keys-only delivery includes unexpected application values and mutable
non-key values become part of the apparent event identity. Sensitive covering
columns cannot be excluded through the documented hook options.

Remediation direction: use only the actual ordered key attributes, validate
the extracted key against the primary-key constraint, and keep INCLUDE columns
eligible for ordinary payload exclusion. Cover composite keys and covering
columns whose values change while the row's identity remains the same.

## DAPI028 Renaming a primary-key column makes hooks emit a null key

`cmd/gregale/templates/data-api/change-hooks.sql:77` copies key column names into
trigger arguments at enable time. Capture reads those frozen names at line 158
and extracts their values from current row JSON at line 186. PostgreSQL column
renames update the table/key metadata but do not rewrite those text arguments.
A missing key field becomes JSON null without any configuration error.

Evidence: enable keys-only hooks on table `api.renamed(id PRIMARY KEY, body)`,
rename `id` to `item_id`, and insert primary key 7. The write succeeds and its
outbox event contains `{"id":null}` instead of `{"item_id":7}`. This was run on
PostgreSQL 16. A fresh hook enable reads the new metadata, but normal DDL does
not automatically perform that refresh.

Impact: consumers cannot identify or reconcile the affected row from the
event's documented key. Other primary-key definition changes can similarly
leave the frozen names inconsistent. Source table/schema names are also frozen
for parent-partition identity; their rename semantics should be made explicit
when fixing metadata refresh.

Remediation direction: validate or refresh hook metadata when key definitions
change, or stop publication with an actionable configuration error until hooks
are re-enabled. Do not silently publish a null/incomplete primary key. Include
rename, compound-key replacement, and partition-parent cases in SQL tests.

## DAPI029 Migration transaction statements can commit schema without ledger entries

`cmd/gregale/templates/data-api-starter/migrations/migrate.mjs:29` starts the
transaction that holds the advisory lock and migration ledger. Each SQL file
is then sent unchanged at line 52. An explicit `COMMIT` or `ROLLBACK` in a file
can end that outer transaction before the runner records the migration. The
catch block's later rollback cannot undo an already committed schema change.

Evidence: an ordinary failing file `CREATE TABLE ...; SELECT 1/0;` leaves no
table, as expected. Change the file to
`CREATE TABLE ...; COMMIT; SELECT 1/0;`: `migrate()` still rejects with `22012`,
but the table remains and `gregale_migrations.applied` contains zero entries.
The experiment used only a uniquely named database on disposable PostgreSQL 16.

Impact requires a migration file containing transaction-control statements,
for example SQL copied from a script that manages its own transactions. Such
a file can also release the runner's advisory lock early. This is not a claim
that ordinary transaction-free migration files lose their rollback behavior.

Remediation direction: explicitly define and enforce the supported SQL contract
for runner-managed transactions. Reject conflicting transaction control before
executing any migration content, with statement-aware validation, a stable
diagnostic, and tests preserving schema/ledger atomicity. Account for comments
and function/procedure bodies. Do not infer rollback merely from the final
request error after a transaction has already committed.

## DAPI030 A named-pipe migration file blocks file loading indefinitely

`cmd/gregale/templates/data-api-starter/migrations/migrate.mjs:15` passes every
matching directory entry directly to `readFile`. It does not check file kind,
use nonblocking/no-follow open flags, impose a byte limit, or accept an abort
signal. A FIFO with a valid numbered SQL filename therefore blocks before any
SQL or ledger logic runs. Other local inputs use explicit regular-file checks.

Evidence: a regular `0001_example.sql` loads successfully. In an isolated child
process, load a directory containing only FIFO `0001_blocked.sql` with no writer:
the loader stays pending. A timer records that state and the parent must
terminate the timeboxed child. Temporary fixture files are then removed.

Impact: an accidental non-regular migration entry can stall local development
or release startup and prevent normal cleanup/cancellation from advancing.
Symlink targets and unbounded files are also not qualified by this reader;
the retained reproduction demonstrates the FIFO case specifically.

Remediation direction: open migration inputs without following links or
blocking on special files, require bounded regular files, and propagate the
workflow's cancellation/deadline into reading. Keep the content checksum over
exact accepted bytes and avoid changing valid migration ordering semantics.

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

The third pass ran the existing `pkg/dataapi/clientgen` tests successfully,
including rendering/type checks and generated Go/Python calls against fake
HTTP APIs. The additional [code-generation/tooling reproduction script](investigation-codegen-repros.py)
ran successfully for all nine new IDs, DAPI016–DAPI024. It calls the real
renderers, compiles generated code, checks synthetic serialization, starts
loopback redirect fixtures, and exercises the actual local-project and replay
parsers. DAPI023 covers path validation and subsequent write-path inspection;
it does not run the full Docker-backed development workflow.

The fourth pass added [runtime/resource reproductions](investigation-runtime-repros.mjs)
for DAPI025/DAPI026/DAPI030 and [isolated PostgreSQL reproductions](investigation-postgres-repros.mjs)
for DAPI027–DAPI029 plus the real descriptor/codec verification for DAPI014.
All six new finding reproductions ran successfully. The existing usage, health,
relay, engine, migration, and real change-hook SQL selection passed all 63 tests,
with zero skips. PostgreSQL 16 ran in a dedicated disposable local container;
the scripts removed their own temporary databases and files. No shared/customer
database or deployed application was changed.

For current acceptance, use the maintained regression suites:

```sh
make data-api-check
make data-api-acceptance
make data-api-packaging-check
go test ./pkg/dataapi/clientgen ./pkg/state/dataapibudget ./pkg/livequery
```

PostgreSQL acceptance requires the documented disposable database and pinned
PostgREST environment. The new catalog and owner-maintenance regressions are
part of that acceptance target. The benchmark is
`go test ./pkg/state/dataapibudget -bench BenchmarkSharedBudgetIndependentSubjects`.

The four `investigation-*-repros` scripts are historical negative assertions,
retained for the original investigation commits (`1e1aaca86`, `b7547f7ea`, and
`060551217`). They assert the old defects and are not acceptance gates for this
branch. Run them only from the corresponding historical checkout when auditing
the original evidence. No hosted application or customer database was changed.
