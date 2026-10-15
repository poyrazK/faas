# Transactional application event outbox

The Go SDK can stage an application event in the same PostgreSQL transaction as
business writes, then publish it after commit. This closes the failure window
between committing business state and calling Gregale. It uses application-owned
storage and the existing application producer-key endpoint; it does not replace
the platform's internal routing outbox.

## Install and stage

Install `faas.EventOutboxSchema` explicitly in the application's business database.
The SDK accepts `database/sql` transactions and does not import a PostgreSQL driver.
No helper installs, migrates, or prunes tables. Restrict table access and protect
backups: unfinished and blocked rows contain original business event data.

```go
func recordOrder(ctx context.Context, db *sql.DB, accountID, appID, slug, orderID string) error {
    tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
    if err != nil { return err }
    defer tx.Rollback()

    // Perform the application's business writes through tx here.
    data, err := json.Marshal(map[string]string{"order_id": orderID})
    if err != nil { return err }
    _, err = faas.SaveEventOutbox(ctx, tx, accountID, appID, slug,
        faas.AppPublishEventRequest{
            Key: "order-created-" + orderID,
            Type: "order.created",
            Data: data,
        }, 7 * 24 * time.Hour)
    if err != nil { return err }
    return tx.Commit()
}
```

Use a new, stable key for each logical business event, scoped to the canonical
account/application UUIDs. Do not use a key previously published outside this
outbox. Retries of the same business operation must use the original key and
request. The helper sets `expected_app_id` to the saved application UUID. A
nonmatching supplied guard is rejected.

The saved marshaled request and slug are immutable through `SaveEventOutbox`.
Repeated identical saves return the retained row, including terminal state,
without resetting the retry window or reopening publication. Different request
bytes or slug return `ErrEventOutboxConflict`; the local comparison is stricter
than the server's normalized JSON comparison. For example, changing object key
order or an explicit occurrence time may conflict. Preserve explicit times on
transaction retries. Omitting time leaves the first ingress time to Gregale.
The complete saved request must fit the existing 1 MiB publication limit.

Roll back the entire business transaction on any staging error. The helper does
not commit, roll back, or send HTTP. A failed commit response may still mean the
transaction committed: reconcile business state and the retained outbox row
before redoing effects. Use database uniqueness/business idempotency for those
retries; an outbox row alone does not deduplicate arbitrary business writes.

## Recover publication

Run recovery in an application-owned worker, using credentials for the saved
account and the existing publication authorization requirements:

```go
status, err := faas.RecoverNextEventOutbox(ctx, db, client, accountID, appID,
    faas.EventOutboxRecoveryOptions{
        ClaimDuration: 30 * time.Second,
        RequestTimeout: 10 * time.Second,
        Backoff: 5 * time.Second,
    })
// ErrEventOutboxEmpty: nothing due; wait before polling again.
// ErrEventOutboxUnconfirmed: uncertainty was saved; retry when due.
// ErrEventOutboxBlocked: inspect status.LastCode and reconcile before intervention.
// Other errors can leave a lease or a remotely accepted publication outstanding.
_ = status
_ = err
```

Each call claims at most one due row with `FOR UPDATE SKIP LOCKED`, commits the
claim, and sends HTTP outside the transaction. Concurrent workers recover
independent rows. A crashed worker's claim becomes eligible after lease expiry.
The claim token and database-clock expiry fence settlement; a stale worker cannot
overwrite a newer claim. Attempts count recovery claims, not transport requests.
A lost claim or failed settlement can follow successful remote acceptance.
`ErrEventOutboxCommitUnknown` means the claim transaction may have committed;
wait for its lease to expire rather than assuming no claim exists.

Defaults are shown above. Claim duration supports one second–five minutes in
whole milliseconds. Request timeout must be positive (at least one millisecond)
and shorter than the claim duration. Backoff supports one millisecond–one hour in
whole milliseconds and is capped at the original retry deadline. The HTTP context
is additionally bounded by the remaining database lease/deadline, accounting for
time spent reading the lease. Transports must honor context cancellation.

Successful responses must match the saved application, account, derived source,
and derived event ID and contain an acceptance time. Confirmed rows become
`published`, retain the receipt's acceptance timestamp, and clear the payload.
Transport/server failures or malformed success responses become `unconfirmed`.
HTTP 4xx other than 408/429 become `blocked`, including identity/content conflicts
and authorization failures requiring operator action. The SDK returns stable
local errors and codes, without server/driver diagnostic text that might contain
payload data. Cancellation can prevent settlement; lease expiry allows recovery.

`GetEventOutboxStatus` reads one key's retained local metadata and returns
`ErrNotFound` for an absent row. It exposes no payload or claim token; producer
keys can themselves be sensitive business identifiers. A `published` status means
Gregale acknowledged durable acceptance, not that every consumer completed.

## Retention, identity and ordering

A retry window defaults to seven days and accepts one second–seven days, measured
from the database time when the row was first staged, not commit or first send.
It never extends on saves, claims or retries. Expired work becomes `blocked` with
`retry_window_expired` when recovery next visits it, without publishing again.
Blocked work requires explicit operator review; this version has no automatic
reset or replacement-key API.

Gregale's producer-key deduplication lasts only while the original acceptance
receipt remains retained. Normal settled receipts become eligible for pruning
after 30 days. The shorter fixed outbox window bounds retries for fresh keys
under that policy; it is not an indefinite exactly-once guarantee. Never extend
the deadline, manually prune acceptance evidence early, recycle a historical key,
or replace an uncertain key simply to bypass a conflict. After the window expires,
use retained publication verification/status and business evidence to decide what
happened. Unavailable evidence is not proof that publication failed.

Use a server containing the `expected_app_id` guard shipped with this feature.
Publication and read-only content verification reject a mismatching resolved app
with 409 before admitting/comparing an event. This prevents a deleted/recreated
slug from silently moving an intent into a new UUID namespace. App rename can
cause 404 and block recovery; operators must reconcile the current slug while
preserving the original UUID, key, payload and deadline. Deploying only the SDK
against a server without this contract is unsupported.

Retain local published identities for the application's idempotency horizon;
pruning them permits staging that key again. Retain unresolved/blocked rows until
reconciliation and apply an explicit payload/backup retention policy. No helper
prunes them automatically. This application table is not part of the platform
clone registry or platform database migrations.

Concurrent publication and retries can reorder events, including events from
successive business transactions. There is no global, per-app, or per-consumer
ordering guarantee. Existing recipient retry, replay and dead-letter behavior
remains independent; consumers must deduplicate their own effects. Account/app
arguments scope SQL but are not a database access-control boundary: use trusted
worker configuration and appropriate database permissions.
