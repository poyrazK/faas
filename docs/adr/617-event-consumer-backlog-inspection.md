# ADR-617: Event consumer backlog inspection

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Maintain an indexed metadata projection of waiting captured
  application recipients and expose account-scoped discovery through the API,
  Go client and CLI.
- **Why:** ADR-614 capacity waits have neither an invocation nor a terminal
  routing failure. Existing delivery listings cannot discover them; receipt
  inspection requires a known source/id, and scheduler metrics lack consumer
  identity.

## Projection and ownership

`event_routing_backlog` has one fixed-size metadata row per currently pending
or processing captured application recipient. It contains receipt/subscription
identity, original account/app, acceptance time, routing mode/state, cumulative
attempt/deferral counters, recorded capacity scope, next attempt and lease.
It copies no envelope, filter, policy, payload or error text. Receipt/snapshot
admission limits already bound customer recipient cardinality; projection and
indexes are outside ADR-615's logical JSON budget. Reserved platform events
retain their existing exemption. Non-application destinations are excluded.

The outbox snapshot/checkpoint and, after adoption, normalized recipient rows
remain authoritative. Database triggers refresh the projection in the writer's
transaction, including publication, legacy claims/checkpoints/backoff, recipient
adoption/claims/completion, operator replay and settlement. Terminal rows are
removed immediately; receipt deletion cascades the projection. An unchanged
metadata tuple avoids an unnecessary rewrite. Progress-only updates refresh
only changed subscription keys. Other root changes refresh that receipt's keys
in deterministic subscription order. The source view/function always restricts
the root by its primary key; there is no periodic global JSON scan.

Recipient claim projection reads the root without acquiring a parent lock.
It must not reverse the existing parent-first completion/replay lock order.
Legacy claims have no normalized child writers. Adoption and replay are atomic;
fallback checkpoint values cover adoption before the child inserts commit.
An independent processing recipient has no active capacity scope, even when its
prior checkpoint recorded a wait. Legacy processing has only a shared receipt
lease: recipient state remains pending until an outcome exists.

Read queries use only the indexed projection and scalar outbox/app metadata.
Every query restricts the authenticated account. App labels join on both app
and account, so removing/transferring a target never reveals its new owner's
name or makes an old receipt visible to the new owner. Historical app identity
remains part of the original account's accepted receipt.

## Discovery contract

`GET /v1/events/backlog` uses the existing read scopes, account authentication
and MFA rules, and `Cache-Control: no-store`. Filters are owned `app` slug,
captured `subscription_id`, current `state`, recorded `capacity_scope`, and
`min_age_seconds` since acceptance. Application target deletion, subscription
editing and disabling do not change captured membership. Handler execution
queues and settled routing belong to the existing delivery/receipt surfaces.
Entries include receipt and, when the target is still owned, routing history
links. There is no payload in this response.

Recipient pages use ascending `(accepted_at, outbox_id, subscription_id)`;
consumer summaries use ascending `(app_id, subscription_id)` with an independent
cursor. Counts and oldest age cover all matching waiting recipients for each
consumer, rather than only the recipient page. Both pages default to 100 and
cap at 200. `pkg/api/limits.go` also bounds cursor/filter size, minimum age
input to one year, and the read deadline to five seconds. Deadline exhaustion
returns 503 `event_backlog_read_timeout`, never an empty success. Filtering
can narrow aggregate work; the deadline bounds expensive account summaries.

All pages from a cursor preserve `window_at`, anchoring acceptance and minimum
age cutoffs. `observed_at` and membership/counts are live per request. One
repeatable-read, read-only transaction makes the three database queries
consistent within a response. Cursors are versioned, opaque JSON bound to
account, resolved app, filters and page kind. Both cursor kinds must share a
window when supplied together; limits may change. Authorization is enforced
by SQL, independently of cursor contents. Cursors are not signed audit evidence.

Recovery removes entries and may remove the cursor row itself; continuation
still uses its immutable tuple. Newly accepted work after the anchored window
is excluded. Replays of older receipts can re-enter before a consumed cursor;
restart discovery to see them. This is a live inspection view, not a frozen
cross-request export. Oldest-first listing supplies no delivery FIFO guarantee.
Age is total time since acceptance, not duration of the current capacity wait.
Recorded capacity scope takes precedence for pending rows; a live whole-event
lease is described as `receipt_processing`, without claiming a particular
recipient is executing.

`coverage=captured_application_recipients` explicitly excludes older receipts
without snapshots. `unattributed_receipts` counts those unresolved receipts
across the account with only the acceptance/age window applied. It remains
account-wide even when app/subscription/state/capacity filters are selected.
Unknown legacy recipient identities are never invented. MemStore mirrors the
same contract under its mutex for portable qualification.

## Rollout and qualification

Apply the additive migration before deploying the API. It backfills waiting
metadata from retained snapshots/checkpoints and adopted rows in the migration
transaction. This may require a maintenance window proportional to existing
recipient count; test it on a representative restored database. Old routing
binaries continue updating existing tables and are covered by the triggers.
No routing mode, adoption switch, lease, retry budget, retention, deduplication
or invocation behavior changes. Upgrade CLI/client to use `GetEventBacklog`
and `gregale events backlog`; there is no deployment in this change.

Qualification exercises consumer saturation, independent healthy sibling
progress, twenty durable capacity deferrals, discovery without event identity,
recovery removal, terminal settlement, selective replay, both routing modes,
account/filter isolation, independent pagination with removed cursor rows,
counts larger than the recipient page, transferred target metadata, null
snapshot coverage, migration backfill and receipt deletion. API tests check
scope enforcement, cursor/filter validation, receipt/history links, timeout
errors and no-store; Go client/CLI tests verify encoded filters and continuations.

The adversarial pass considered stale routing workers, a transferred/deleted
target, old writers, adoption between inspection reads, deleted cursor rows,
replays behind a cursor, new events during pagination, mismatched cursor windows,
large account aggregation, and root/child lock order. Projection changes commit
or roll back with their authoritative writers; the API never claims or replays
work. Existing claim fencing and atomic routing tests remain regression gates.
Tenant predicates protect every result independently of opaque cursors.
