# ADR-20261017155842: Durable internal deployment-failure notices

## Context

The deployment-failure subscriber reads a transactional PostgreSQL notification,
claims the app's hourly cooldown and sends inline. LISTEN has no replay during a
disconnect. A send error after the claim consumes that slot. Several existing
daemons persist failures, so capturing only one apid call site misses events.
Mail outages must leave deployment outcomes intact.

## Decision

Add exact, independent, default-off
`FAAS_INTERNAL_MAIL_DEPLOYMENT_FAILED_OUTBOX_ENABLED=0` in apid, maild and private
reconciliation. Existing master, scoped-producer, scheduling and producer-outbox
gates and internal-producer transport remain prerequisites. Flag-off behavior
retains the legacy subscriber. A selected subscriber never falls back inline,
including incomplete late configuration. No customer email API, listener, SDK,
provider credential or deployment activation is added.

A passive trigger stamps the original database failure time, a monotonic
deployment revision, recipient account and account/app/success revisions on
transitions into failed, including inserts. It copies no address, errors or
credentials. Account and app guards independently advance lifecycle revisions
for address/status/deletion and app owner/org/project/slug/status changes.
Explicit revision edits advance rather than rewind them. These metadata guards
remain installed while mail issuance is off, like existing auth/billing fences.

Deployment lifecycle writes serialize through a separate app-and-scope fence.
Only entering live advances its success revision; later supersession, deletion
or another failure cannot erase that success. Success in another scope leaves
the notice eligible. Insert/reassignment takes the ordinary parent FK key-share
before the fence. No trigger encrypts, sends mail, claims a cooldown or depends
on mail queue capacity.

Only enabled apid enrolls a discovery boundary per namespace and producer.
The first enrollment uses database time and excludes historical failures.
Same-generation restarts reuse the boundary. Polling discovers current
unobserved failures within the original 24-hour TTL. A pause leaves eligible
work resumable within that TTL. Recovery holds prevent enrollment/capture.
After an explicit generation change and hold release, the boundary advances to
the new generation: restored unobserved history cannot become freshly issued
mail. Events before that boundary require investigation and are not backfilled.

Each bounded pass handles at most 25 candidates. Under namespace admission,
account, app and scope locks, apid verifies source identity/lifecycle revisions
and atomically saves an encrypted intent, an observation receipt and the hourly
app cooldown. Capture failure rolls back mail work while the already committed
deployment remains failed and retryable. Durable cooldown skips never reappear
after the hour elapses. Observation and intent uniqueness prevent duplicates
across replicas, restarts and distinct producer configurations.

Recipient selection retains deployer-or-app-owner semantics. A named deployer
who differs from the app account must still have active membership in the app's
organization. Capture and delivery share that membership row when needed.
Unrelated or removed actors receive no notice. Account deletion/status/address
changes and app identity changes invalidate earlier failure snapshots.

The encrypted payload freezes failure time, app slug, deployment ID, bounded
redacted diagnostics and authenticated dashboard link. Whole diagnostic fields
over the source bound are omitted before loading; fields over the output bound
are omitted after redaction. Build logs and attachments are excluded. Dedicated
current/previous apid identities encrypt these snapshots; maild reads metadata
only. Associated data binds intent, account, namespace, producer, sender,
recipient, source IDs/scope/revisions, failure time, template/version, expiry,
key and recovery generation. The V1 renderer, `deployment-failed:<intent UUID>`
key and original expiry stay fixed across retries and lost acknowledgements.

Dispatch locks account and intent, then app, optional membership and scope
fence. The app cooldown capturer uses NO KEY UPDATE, compatible with deployment
FK key-shares. Maild holds the same metadata locks through its durable SMTP-start
commit. Neither reader locks deployment rows, avoiding a fence/row inversion.
Changes or a later same-scope success that commit first cancel queued/admitted
mail. After submission start, SMTP custody cannot be recalled or automatically
replayed. Existing suppression, budgets, lease/retry, expiry, recovery, terminal
erasure and audited private reconciliation controls apply.

## Consequences

Lifecycle stamping adds database work, and transitions may briefly wait on a
bounded mail review. Capture may omit superseded failures or ones that expire
during an extended outage; it does not invent a later failure time. The existing
app cooldown continues to suppress other failures during the hour.

Failures within a saved cooldown window, including a legacy inline claim made
while issuance was rolled back, remain skipped after that hour elapses.

Account erasure cascades intents and observations; app deletion cascades scope
fences. Terminal settlement erases recipient/ciphertext. Opaque reconciliation
audit survives erasure. Down refuses used intent, observation or audit history.
Units and examples stay off. Tests use disposable PostgreSQL, local Unix-socket
maild and fake SMTP; live qualification remains separate work.
