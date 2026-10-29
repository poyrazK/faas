# Internal mail pilot

This is an operator-only experiment. The API has no TCP listener, customer
routes, customer credentials, public SDK surface, billing or plan allowance.
Existing platform mail keeps its selected transport until separately opted in.

## Gates and configuration

| Gate | Default | Effect |
| --- | --- | --- |
| `FAAS_INTERNAL_MAIL_ENABLED` | off | Exact `1` starts the private API and permits admission. Otherwise maild exits before secrets, DB or socket access. |
| `FAAS_INTERNAL_MAIL_DELIVERY_ENABLED` | off | Exact `1`, together with the master gate, allows SMTP submission. |
| `FAAS_INTERNAL_MAIL_REPORTS_ENABLED` | off | Exact `1`, together with the master gate, requests Postfix DSNs and enables the private Postfix report route. The pipe helper checks both flags independently. |
| `FAAS_INTERNAL_MAIL_FEEDBACK_ENABLED` | off | Exact `1`, together with the master gate, enables source-scoped private ingestion of DKIM-authenticated provider DSNs and abuse reports. Independent of delivery and local Postfix reporting. |
| `FAAS_INTERNAL_MAIL_RETENTION_ENABLED` | off | Exact `1`, together with the master gate, enables bounded payload retention previews and the private maintenance route. Removal additionally requires preview-only mode to be explicitly disabled. |
| `FAAS_INTERNAL_MAIL_OPERATIONS_ENABLED` | off | Exact `1`, together with the master gate, enables the private operator routes and `mailctl`. A distinct operator token is required. |
| `FAAS_INTERNAL_MAIL_PRODUCERS_ENABLED` | off | Exact `1`, together with the master gate, enables scoped internal producer routes and permits dispatch for enrolled producer/sender pairs. Disabled access holds producer-owned handoffs. |
| `FAAS_INTERNAL_MAIL_PRODUCER_LIFECYCLE_ENABLED` | off | Exact `1` enables staged replacement credentials and audited private rotation, revocation and producer suspension/resume. Requires enrolled producers in maild; operator commands also require operations. Saved expirations, revocations and suspensions remain enforced with management off. |
| `FAAS_INTERNAL_MAIL_PRODUCER_SCHEDULING_ENABLED` | off | Exact `1`, together with the master gate, enables fair producer turns and atomic rolling minute submission limits. SMTP still requires its own delivery gate; private status also requires operations. |
| `FAAS_INTERNAL_MAIL_SCHEDULING_ENABLED` | off | Exact `1`, together with the master gate, enables `send_at`/`expires_at` admission, timed dispatch and metadata-only expiry maintenance. Disabling holds timed handoffs; persisted deadlines remain enforced. |
| `FAAS_INTERNAL_MAIL_DOMAIN_PACING_ENABLED` | off | Exact `1`, together with master and delivery gates, paces new submission reservations by namespace and exact recipient domain. The default interval is 5 seconds; pacing does not enable SMTP. |
| `FAAS_INTERNAL_MAIL_RELAY_BACKPRESSURE_ENABLED` | off | Exact `1` enables trusted relay health reports and automatic holds for missing, stale or unhealthy evidence. Requires the reports collector/credential; SMTP still needs its independent delivery gate. |
| `FAAS_INTERNAL_MAIL_DELIVERY_CONTROLS_ENABLED` | off | Exact `1`, together with master and operations gates, enables audited pause/resume, local drain and queued cancellation. Existing holds and cancellations remain enforced when access is disabled. |
| `FAAS_INTERNAL_MAIL_DKIM_ROTATION_ENABLED` | off | Exact `1` enables staged signing-key operations; operator mutations also require the operations gate and operator token. |
| `FAAS_INTERNAL_MAIL_DOMAIN_REVERIFY_ENABLED` | off | Exact `1` enables recurring authorization checks and requires fresh proof for new admission and dispatch. Manual checks also require operator access. |
| `FAAS_MAIL_TRANSPORT=internal` | unselected | Explicitly switches an individual platform caller; requires the master flag, service token, socket and `FAAS_MAIL_FROM`. |
| `FAAS_MAIL_TRANSPORT=internal-producer` | unselected | Explicitly selects a scoped internal producer; requires master/producer gates, a stable producer ID, its separate token, socket and an approved `FAAS_MAIL_FROM`. |
| `faas_internal_mail_managed` | false | Includes the optional Ansible role. |
| `faas_internal_mail_enabled` | false | Starts the optional systemd unit. It does not replace the runtime gates. |
| `faas_internal_mail_reports_managed` | false | Installs the pipe wrapper and an explicit operator-provided environment; does not configure or reload Postfix. |
| `faas_internal_mail_metrics_enabled` | false | Starts the optional aggregate telemetry timer; requires the private service, reports environment and node_exporter. |

Start from [`deploy/internal-mail.env.example`](../../deploy/internal-mail.env.example).
Keep all runtime gates at `0` while preparing the relay. Supply a random
service token of at least 32 bytes through the private EnvironmentFile, never
command-line arguments, a repository file, or a customer secret. Use a dedicated
age identity with owner-read-only permissions. Back up that identity securely;
without it neither stored mail bodies nor domain signing keys can be recovered.

The generated unit loads `/etc/faas/secrets/internal-mail/mail.age` through
systemd credentials and reads `/etc/faas/secrets/internal-mail/maild.env`.
The optional role accepts controller-local `faas_internal_mail_env_src` and
`faas_internal_mail_age_identity_src`, copies them with restrictive permissions,
and starts nothing by default. Do not proxy the socket through Caddy or expose
it through the public gateway. Requests require the separate service token;
ordinary Gregale customer API keys have no access.

Apply the internal mail migrations through the normal apid migration owner before
starting maild. maild checks that its schema is readable and never migrates the
shared database itself. Namespace defaults to `platform` and is configured by
the operator, never by a request. Every state read/write and suppression is
scoped to that namespace.

## Restrict the internal pilot

`FAAS_INTERNAL_MAIL_ALLOWED_RECIPIENTS` is an exact comma-separated list of
approved bare mailbox addresses, with at most 25 entries. Empty denies message
admission and cannot enable SMTP dispatch. Wildcards, domain-only entries and
display names are rejected. The allowlist is checked at admission and when
queued work is dispatched; withdrawing an address stops its remaining
maild-owned work. Already accepted Postfix work retains relay ownership.
Restart maild after changing the list. An idempotent retry for a withdrawn
address is refused; the original message remains inspectable by ID.

`FAAS_INTERNAL_MAIL_DAILY_RECIPIENT_LIMIT` defaults to 10 submissions per UTC
day and may be set from 1 through the internal maximum of 100. The namespace
admission lock enforces this budget under concurrent requests; an idempotent
repeat of an allowed message consumes no additional budget. These are pilot
controls, not customer plans. Opt only one chosen Gregale notification caller
into a private internal transport for the initial pilot. Use the scoped producer
transport below when enrolling additional notification callers.

## Prepare Gregale's SMTP relay

Run a maintained Postfix installation with persistent spool storage. The
examples in [`deploy/internal-mail`](../../deploy/internal-mail) accept
submission only on `127.0.0.1:2525` and deliver directly to recipient MX servers.
Install them only on the intended relay host after reviewing its existing mail
configuration. Replace the documentation-only source IP with the real stable
sending IP, use the same value in `FAAS_INTERNAL_MAIL_SENDING_IP`, and configure
matching forward/reverse DNS and permitted outbound TCP 25. Postfix owns final
SMTP retries after accepting a submission; maild must not resubmit while
Postfix still owns it. Tenant SMTP restrictions remain unchanged.

The relay must preserve DKIM-signed message bytes. maild signs each recipient's
message before handoff. Its envelope sender is
`bounce+<recipient UUID>@<verified domain>` and its stable Message-ID contains
that recipient UUID. The report bridge below consumes Postfix's own delivery
notifications through a local pipe. The examples remain outbound-only and do
not install a public inbound mail service. The service reports `submitted`
until the relay supplies further observations; local acceptance alone does
not establish destination acceptance.

## Install the Postfix report bridge

Build and deploy `mail-report` alongside `maild`, with mode `0755` and traversable
parent directories so the `faas` pipe user can execute it. Install the root-owned pipe
wrapper with `faas_internal_mail_reports_managed=true` and an operator-owned
`faas_internal_mail_report_env_src`. Start from
[`report-bridge.env.example`](../../deploy/internal-mail/report-bridge.env.example).
The role puts that file at `/etc/faas/mail-reports/bridge.env` with mode `0440`,
owner `root:faas`, inside a mode-0750 directory. It contains only the two gates,
socket path and a separate random `FAAS_INTERNAL_MAIL_REPORT_TOKEN` of at least
32 bytes. This token must differ from `FAAS_INTERNAL_MAIL_TOKEN`; also supply
it to maild's private environment. It authorizes Postfix ingestion and aggregate
monitoring, and cannot send mail, manage domains, or request an uncertain retry.
The wrapper sources POSIX shell assignments, so keep this file operator-owned.

Merge the example `gregale-report` pipe service into `master.cf` and add the
transport map in
[`postfix-report-transports.example`](../../deploy/internal-mail/postfix-report-transports.example).
Use an anchored, escaped pattern for each verified sending domain. Merge it
with existing transport maps rather than replacing unrelated routes. Preserve
`null_sender=<>`, `user=faas:faas`, and the one-recipient delivery limit. The
wrapper runs as `faas`, can reach the protected Unix socket, and does not need
database credentials or the encryption identity. Review the resulting
configuration with `postfix check` before the operator reloads Postfix.

When the reports gate is on, maild requires the relay to advertise DSN and
submits `RET=HDRS`, a recipient-bound `ENVID`, and
`NOTIFY=SUCCESS,FAILURE,DELAY`. A relay lacking DSN support is deferred before
submission. The example's `smtp_discard_ehlo_keywords` makes Postfix report the
direct MX handoff itself instead of delegating success reports to remote
servers. `delay_warning_time=1h` enables relay delay notifications.

Postfix queues each notification durably and pipes it to `mail-report`, which
posts the MIME report to authenticated `POST /internal/v1/mail/reports/postfix`
over the Unix socket. The endpoint is absent unless both master and reports
gates are on. It validates the null envelope sender, bounce UUID, matching DSN
envelope ID, exactly one recipient and its stored destination address, MIME
structure, action and enhanced status. Only the action and a bounded status
such as `dsn_5.1.1` are retained; mail bodies, returned headers and provider
diagnostics are not stored or logged by the bridge.

The helper exits successfully only after the database commits. Disabled gates,
unavailable maild, malformed reports, authentication errors and submission races
return `EX_TEMPFAIL` with a generic `4.3.0` diagnostic. Postfix retains and retries
the DSN. Repeating the same queued bytes uses the same hashed event ID, including
after a lost HTTP response. Late reports append evidence without overwriting
a bounce or complaint. Relay deferrals never enqueue another mail submission.
Queue expiry with a final temporary (`4.x.x`) status ends that delivery without
permanently suppressing the mailbox; hard bounces and complaints suppress it.

Enable reporting on maild and in the helper before enabling delivery for an
approved internal pilot. Reporting can remain enabled with delivery disabled
to drain existing observations. Inspect `postqueue -p` and the relay's normal
logs for stuck `gregale-report` work. Retention is bounded by
`bounce_queue_lifetime` (one day in the example); repair or preserve stuck
reports before they expire. Never discard queued evidence as a recovery step.

This bridge covers local relay handoff, relay deferral, immediate destination
rejection and relay queue failures. Bounces sent later by remote providers
need a separately qualified inbound route. Complaints require genuine feedback
loop enrollment. The private provider adapter below can ingest their signed
original reports; the local bridge does not infer either from delivery DSNs.
It trusts local Postfix observations; do not forward unauthenticated Internet
notifications into this privileged route.

For reproducible local qualification, run
`bash scripts/test-internal-mail-postfix.sh` with Docker available. It builds a
disposable Postfix fixture, then runs it with external networking disabled to
check acceptance, rejection, delays, pipe failure and queue-manager restart.
The regular package tests separately exercise PostgreSQL commit/replay,
submission races, credential scope, parser limits and default-off gates.

## Enroll private provider feedback

`mail-feedback` forwards an operator-managed collector's original RFC 3464 DSN
or RFC 5965 abuse report to `POST /internal/v1/mail/feedback/<source>` on the
existing Unix socket. It installs no listener, inbound SMTP route, mailbox
poller or public webhook. The collector owns durable custody of the input and
retains it until the helper exits zero. Qualifying the real provider feed and
its original signed report format is separate from implementing this adapter.

Enable the master and feedback gates explicitly in both maild and the
collector's private environment. New SMTP delivery may remain disabled while
feedback drains. maild reads `FAAS_INTERNAL_MAIL_FEEDBACK_SOURCES` only when the
feedback gate is on. Set this variable to a JSON array in the existing private
`maild.env`, using single quotes around the complete JSON value for systemd's
EnvironmentFile syntax. An enrollment object has this shape:

```json
{
  "id": "provider-fbl",
  "token": "REPLACE_WITH_A_DISTINCT_RANDOM_TOKEN",
  "dkim_domains": ["feedback.provider.example"],
  "report_from": ["reports@feedback.provider.example"],
  "recipient_domains": ["recipient.example"],
  "formats": ["dsn", "arf"]
}
```

Enroll only actual provider-operated reporting identities. A source token must
have at least 32 bytes and differ from the service, telemetry and all other
source tokens. Each credential can access only its own feedback route; it
cannot send mail, manage domains, read metrics or submit manual observations
and retries. Service and telemetry credentials cannot use feedback routes.
Enrollment accepts at most eight sources, 16 exact domains or sender mailboxes
per list, and 32 KiB of JSON. IDs have at most 32 lowercase namespace characters.
Wildcard domains and sender mailboxes are rejected. Restart maild after changing
enrollment; withdrawing a source stops further ingestion without clearing its
existing events or suppressions.

The collector credential identifies the adapter, not the report's author.
maild independently verifies the original outer DKIM signature with bounded DNS
lookups. An exact enrolled signing domain must align with the report's From
domain, and that From mailbox must appear in `report_from`. The valid signature
must cover From, Message-ID, MIME-Version, Content-Type and the whole body.
Caller-supplied Authentication-Results, reported Source-IP, display names and
envelope UUIDs do not authenticate a provider. Unapproved signers cause no DNS
lookups. Unsigned reports, incomplete signatures, redacted correlation and
unrecognized report formats require operator review; they are never converted
to suppressions. Do not forward/re-sign or alter the original MIME bytes.

Each report is bounded to 1 MiB, with a 64 KiB machine-readable section and
bounded returned headers. Exactly one recipient and three report parts are
required. The returned original headers must include Gregale's recipient UUID
in both Message-ID and X-Gregale-Mail-Recipient-ID, the original To address and
a From address in the sending domain. The UUID, sending domain and destination
must match the namespace's stored delivery before any event commits.

For a DSN, Final-Recipient must match the returned To; Original-Recipient and
Original-Envelope-ID must agree when present. Only failure and delay actions
are admitted here. For an abuse report, Feedback-Type must be `abuse`, Version
must be `1`, and User-Agent, Original-Mail-From and Original-Rcpt-To are required.
The latter two must match Gregale's recipient-bound bounce address and original
destination. Other feedback types, multi-recipient reports and anonymous or
redacted reports are unsupported by this strict pilot adapter.

Start the collector's private environment from
[`feedback-collector.env.example`](../../deploy/internal-mail/feedback-collector.env.example),
setting only its own source ID and credential. The operator-managed collector
loads `/etc/faas/mail-feedback/collector.env` through its private EnvironmentFile.
Keep the file root-owned, mode `0440`, readable only by the collector's service
group, inside a mode-`0750` directory. With that environment loaded, invoke the
helper with the original file on standard input:

```sh
/opt/faas/current/bin/mail-feedback < original-report.eml
```

The helper acknowledges only HTTP 200 after database commit. Every other result
returns `EX_TEMPFAIL` (75) and a fixed `4.3.0` diagnostic without report content
or credentials. Preserve failed inputs for retry or review: HTTP 503 indicates
unavailable verification/dependencies, 409 can indicate an active submitting
lease or contradictory event identity, and 400/422 indicates malformed,
uncorrelated or untrusted evidence. Removing the feedback gate also retains
input for later handling. Do not point Internet DSNs at the local Postfix route.

Accepted reports use a stable event identity derived from the enrolled source
and signed outer Message-ID. Retries, including those with extra Received
headers, produce one durable observation across restarts. Contradictory reuse
is rejected. Events retain only source identity, recipient linkage, kind and
bounded status code; raw reports and provider diagnostics are discarded.
Hard bounces and abuse complaints reuse namespace suppression. Final queue
expiry with a `4.x.x` status does not permanently suppress a mailbox. Delays
retain relay ownership, complaints cannot regress to bounced/accepted, and no
feedback observation requests another SMTP submission. Existing complaint,
bounce and suppression monitoring includes these durable outcomes.

## Preview and apply payload retention

Retention removes encrypted message content after a quiet period while keeping
message IDs, per-recipient states, event history, namespace suppressions and
idempotency records. It does not delete domains or DKIM keys. These are internal
operating controls, not customer retention terms. Apply the additive
`20261015151448279_internal_mail_payload_retention.sql` migration through apid
before deploying the upgraded maild, including when cleanup remains disabled.
maild readiness checks for the new columns and never applies migrations itself.

All retention work is gated by `FAAS_INTERNAL_MAIL_RETENTION_ENABLED=1` and the
master flag. An unset or malformed gate disables the worker's data access and
hides the maintenance route. The following settings are read only with that
gate enabled, from the existing private `maild.env`:

| Setting | Default | Accepted values |
| --- | --- | --- |
| `FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN` | `1` | Unset or exact `1` previews only; exact `0` permits content removal. Other values reject startup. |
| `FAAS_INTERNAL_MAIL_RETENTION_DAYS` | `7` | Integer `1..365`, the quiet period after every recipient has settled. |
| `FAAS_INTERNAL_MAIL_RETENTION_BATCH_SIZE` | `25` | Integer `1..100`, the maximum messages considered per pass. |

A message must be older than the configured period and have at least one
recipient. Every recipient must have remained in `accepted`, `bounced`,
`complained`, `suppressed` or `failed` for the full quiet period. Any `queued`,
`claimed`, `submitting`, `submitted`, `deferred` or `uncertain` recipient protects
the entire payload, including expired leases awaiting normal reconciliation.
Fresh terminal state updates restart the quiet period before pruning. A late report
after pruning still updates delivery history; it does not recreate content.

The retention worker runs one batch at startup and then hourly, independently
of SMTP dispatch. Each pass has a 15-second deadline and bounded rollback time.
Previews read aggregate sizes without decrypting content or changing rows.
Actual cleanup locks candidates and recipients, skips busy records, and checks
eligibility again before updating. It decrypts and verifies each payload before
removal. A bad identity, corrupt payload, fingerprint mismatch, cancellation or
revoked gate rolls back the entire batch. Repair the cause before retrying;
do not bypass verification or discard related evidence.

`POST /internal/v1/mail/retention` accepts the full private service credential
and a JSON body such as `{"dry_run":true}`. Telemetry, feedback and customer
credentials cannot use it. An omitted or null `dry_run` always previews, even
when automatic cleanup is configured to remove content. Explicit false requests
one cleanup batch and returns `409 retention_dry_run_required` while the daemon
is configured for previews only. Requests cannot change the window or batch
size. Responses contain only aggregate eligible/pruned counts and logical
encrypted byte sizes; a full batch can leave more work for subsequent passes.

New admissions retain a resource- and namespace-bound HMAC of the canonical
request. An actual cleanup pass computes this verifier for legacy messages
before clearing their ciphertext, atomically storing `payload_pruned_at`.
Identical idempotent retries still return the original message; contradictory
retries still conflict and never create another send. The verifier is keyed
with the dedicated age identity so a database reader cannot guess short codes
from a public body hash. Preserve that identity for replay verification even
after payload pruning. Message inspection includes `payload_pruned_at` when
content has expired; fingerprints and payloads stay absent from API responses.
The separately gated encryption keyring described below preserves these legacy
verifiers during rotation and refuses retirement while they are still needed.

Monitoring exposes `maild_mail_retention_enabled`, preview mode, durable retained
and pruned message counts, retained/eligible logical bytes and eligible backlog
age. Last-attempt, last-success and last-pass result gauges describe the current
process and reset on restart; durable inventory does not. Failed statistics
omit inventory samples instead of retaining a healthy zero. The internal
`InternalMailRetentionFailed` warning covers failures, unavailable statistics
and a worker without a completed pass for over two hours. Check configuration,
database health and key availability, then use a preview before resuming removal.

For rollback, disable the retention flag and restart maild while keeping the
upgraded replay handling and retention columns. This stops further cleanup;
previously removed content is not recoverable from its HMAC. Migration rollback
refuses to drop the verifier columns after any payload has been pruned. Logical
content removal does not promise immediate disk reclamation or erasure from
PostgreSQL WAL, replicas or backups; their retention is managed separately.

## Enroll scoped internal notification producers

Apply
[`20261016095545574_internal_mail_producers.sql`](../../migrations/20261016095545574_internal_mail_producers.sql)
through the migration owner before deploying the updated maild. Readiness
requires its ownership columns even when producers are disabled. Producer
routes remain on the existing protected Unix socket, with no customer access.
Update every maild worker for the namespace before enabling producers; older
binaries do not enforce producer ownership or sender withdrawal during dispatch.

In maild's private EnvironmentFile, `FAAS_INTERNAL_MAIL_PRODUCERS` enrolls up to
16 callers. This JSON shape uses placeholders; supply actual approved values
only in the private environment. Keep tokens out of repository files, command
arguments, logs and tickets:

```json
[
  {
    "id": "<stable-producer-id>",
    "token": "<private-token>",
    "from": ["<approved-bare-mailbox>"],
    "daily_recipient_limit": 10,
    "pending_recipient_limit": 100,
    "dispatch_per_minute": 10
  }
]
```

IDs use `[a-z][a-z0-9_-]{0,31}`. Each token has at least 32 random bytes without
whitespace and differs from all service, report, operator, feedback and other
producer tokens. Each caller has `1..16` exact canonical bare From mailboxes;
wildcards and domain-wide grants are rejected. Requests may supply a display
name, but their mailbox must match an approved entry. Domain ownership, DKIM,
SPF/DMARC and the recipient pilot allowlist still apply.

Omitted budgets default to ten recipient admissions per UTC day and 100 pending
recipients. Explicit budgets must be positive and at most 100 daily and 1,000
pending. The configured namespace daily limit and pending safeguard also apply
across all producers and legacy callers. Admission serializes these checks with
message creation. Cancelling does not refund daily usage; submitted relay-owned
work leaves the pending budget.

Enable `FAAS_INTERNAL_MAIL_ENABLED=1` and
`FAAS_INTERNAL_MAIL_PRODUCERS_ENABLED=1` separately in maild and each selected
caller's private environment. An enabled maild rejects empty or invalid producer
enrollment. Start the caller's configuration from
[`producer.env.example`](../../deploy/internal-mail/producer.env.example), select
`FAAS_MAIL_TRANSPORT=internal-producer`, and supply its ID, scoped token and
approved `FAAS_MAIL_FROM`. The transport fails closed when disabled or
misconfigured and never falls back to the full service credential or another
transport. Use the caller's existing private EnvironmentFile. The optional role
does not distribute or enable callers.

The producer credential permits only these routes for its own ID:

| Method | Private route | Result |
| --- | --- | --- |
| POST | `/internal/v1/mail/producers/{producer}/messages` | Enqueue with a caller-stable `Idempotency-Key`. |
| GET | `/internal/v1/mail/producers/{producer}/messages/{id}` | Own message metadata and recipient status. |
| GET | `/internal/v1/mail/producers/{producer}/messages/{id}/events` | Own paginated event history (`limit`, `after`). |
| GET | `/internal/v1/mail/producers/{producer}/usage` | Own durable daily/pending usage and configured budgets. |

It grants no domain management, reports, maintenance, operations or access to
other callers' messages. Inspections fetch no bodies, ciphertext, fingerprints,
idempotency keys or lease tokens. Usage is an observation, not a quota reservation;
namespace limits can still reject the next admission. Existing `mailctl` message
inspection includes producer ID and sender mailbox. Monitoring retains namespace
aggregates without producer/address labels.

Idempotency is scoped by namespace, producer ID and request key. Different
producers and legacy callers can independently use the same key. Identical
retries preserve the original message after restart, cancellation or payload
pruning. Rotate a token while retaining its producer ID to retain ownership and
replay history. After a coordinated private configuration update and restart,
the old token loses access. Never reuse an ID for a different caller; changing
IDs creates a new replay scope and can admit a duplicate.

Workers check the enrolled producer/sender pair at claim selection and before
SMTP handoff. Disabling the producer gate, removing the ID or withdrawing a
sender stops new handoffs for its queued work while other callers continue.
Normal queue age limits still apply. A handoff already started may finish, and
Postfix retains accepted work; reports and reconciliation keep their independent
gates. Use the audited delivery controls to drain or cancel mail when needed.

The migration refuses rollback once any producer-owned message exists. Keep the
upgraded schema and a binary that understands ownership; runtime rollback removes
producer access while preserving replay and queued state. The legacy `internal`
transport remains an explicit full-service option for existing trusted callers.
This enrollment adds no production activation or external delivery qualification.

## Schedule delivery and expire stale notifications

Apply
[`20261016103033444_internal_mail_scheduling.sql`](../../migrations/20261016103033444_internal_mail_scheduling.sql)
through the migration owner before deploying the updated maild. Readiness
requires its timing columns even with scheduling disabled. Update every maild
worker for a namespace before enabling timing; older binaries do not enforce
these fields. The optional unit and example environments keep
`FAAS_INTERNAL_MAIL_SCHEDULING_ENABLED=0`.

With master and scheduling gates explicitly enabled, both private message
submission routes accept optional absolute RFC 3339 timestamps:

| Field | Behavior |
| --- | --- |
| `send_at` | Earliest maild handoff time; a new schedule must be in the future and within 24 hours. Omit for immediate eligibility. |
| `expires_at` | Deadline for a new maild handoff; must be later than admission or `send_at`, and within the following 24 hours. Omit to retain the normal queue-age bound. |

The existing 24-hour queue-age window starts at `send_at` for scheduled mail,
and at admission for immediate mail. Omitting both fields preserves legacy
behavior and the original fingerprint format. Timestamps are normalized to UTC
microseconds before encryption and replay verification. Delivery is polled;
`send_at` is an earliest eligible time, not an exact-time or inbox-arrival promise.
Keep daemon and database UTC clocks synchronized.

Messages reserve their existing namespace/producer daily admission and pending
budgets when enqueued, including future schedules. These are admission budgets,
not delivery-day reservations. Scheduling preserves all pilot recipient,
producer/sender, domain authorization, suppression, attempt and delivery-hold
checks. The signer is selected at dispatch. Scheduled payloads are composed at
admission and are not regenerated at the scheduled time.

Persist the original absolute timestamps with the caller's idempotency key.
Equivalent offsets and sub-microsecond representations canonicalize to the same
request. Repeating an identical request after its schedule or expiry passes
returns the original message, including after restart or payload pruning. A new
key with past timing is rejected. Changing or removing a timestamp under an
existing key returns conflict; a retry cannot extend a deadline. There is no
in-place rescheduling operation. Use audited cancellation and a new intended
message when replacing a schedule.

Platform callers can explicitly use the capability checked helper with a sender
from `mail.SenderFromEnv`, after enabling scheduling in their own private
environment as well as maild's:

```go
sendAt := time.Now().UTC().Add(10 * time.Minute)
expiresAt := sendAt.Add(5 * time.Minute)
// Persist these times and msg.MessageID before the first attempt.
err := mail.SendTimed(ctx, sender, msg, internalmail.MessageTiming{
    SendAt: &sendAt, ExpiresAt: &expiresAt,
})
```

Timed sends require a caller-stable `MessageID`. Unsupported transports and
decorators return an error instead of ignoring timing. Directly constructed
internal senders require an explicit `WithScheduling` gate callback; factory
senders carry the environment gate automatically. Ordinary `Send` is unchanged.
The helper adds no caller opt-in, public SDK or production configuration.

Future mail remains queued without consuming attempts or decrypting payloads.
The worker checks timing before rendering and submission. Database handoff checks
use current wall time after domain/recipient lock waits. The SMTP transport
checks expiry before connecting and before writing DATA, and bounds the session
by the deadline. Modified timing metadata that disagrees with the encrypted
request fails closed.

Elapsed deadlines turn maild-owned queued, deferred and claimed recipients into
terminal `expired` with an event and cleared lease. An expiry pass also runs while
the scheduling gate is on and SMTP delivery is off; it changes only timed mail.
Existing delivery passes enforce saved deadlines even if scheduling access is
disabled. If both passes are disabled, state observation waits until a pass runs;
the saved deadline still prevents a later handoff. Disabling scheduling holds
timed work while legacy untimed mail continues under its own gates.

Expiry does not recall mail already submitted to Postfix or resolve unknown
custody. Submitting, submitted and uncertain work keeps its existing fences and
report/reconciliation path. A lost response after DATA stays uncertain even after
expiry. Confirmed non-acceptance can still be reconciled to the queue, where the
original deadline is enforced before another attempt. Late acceptance, bounce
and complaint reports retain their original correlation. Expiry is not a promise
that downstream delivery finishes before the deadline.

Producer and operator metadata include both timestamps. `mailctl messages list
--status expired`, event inspection and delivery status expose expiry without
payload access. Future queued mail can still be cancelled through the audited
controls. Terminal expired mail cannot be retried or cancelled into another
state. Its fresh observation starts the retention quiet period; daily admission
usage is retained and pending capacity is released.

Aggregate `maild_mail_scheduling_scheduled_waiting` and
`maild_mail_scheduling_expiring_pending` gauges have enabled/read-success gauges
and no address or message labels. Pending-age monitoring starts at the scheduled
time, so intentional future work does not trigger the overdue queue alert.
`expired` recipient/event counts remain separate from delivery failure outcomes.

Migration rollback refuses persisted timing or expiry evidence. Keep the upgraded
schema and an ownership/timing-aware binary when disabling the feature. Runtime
rollback holds new timed handoffs while preserving timestamps, replay and custody.

## Pace delivery by recipient domain

Apply
[`20261016110548882_internal_mail_domain_pacing.sql`](../../migrations/20261016110548882_internal_mail_domain_pacing.sql)
through the migration owner before deploying the updated maild. Readiness checks
the new table even with pacing disabled. Upgrade every worker in a namespace
before enabling the feature; old binaries do not reserve domain slots.

`FAAS_INTERNAL_MAIL_DOMAIN_PACING_ENABLED=0` is the default in the optional unit
and example environment. With an explicit `1`, the existing master and delivery
gates still control all SMTP activity. Configure
`FAAS_INTERNAL_MAIL_DOMAIN_PACING_INTERVAL_SECONDS` only in maild's private
environment: the default is 5 seconds and the accepted range is 1–300 whole
seconds. Restart workers with a consistent policy. No caller, producer or message
can select its own pacing policy.

One shared cooldown covers all senders and producers targeting the same exact,
lowercase recipient domain within a namespace. Different domains and namespaces
have independent slots; subdomains are separate domains. Candidate selection skips
cooling domains so other due mail can progress. Future schedules reserve no slot.
Known blocked work is left queued without decryption or a charged attempt.

Immediately before starting submission, maild locks the recipient-domain row and
uses the current database clock to reserve the next interval. Reservation and
recipient transition to `submitting` commit together. Concurrent workers,
including workers using different sending domains, cannot reserve the same slot.
A failed handoff check, including expired authorization after a lock wait, rolls
back the reservation. A race that loses the slot
returns its recipient to `queued` with code `recipient_domain_paced`, records the
durable next eligible time, refunds the claim attempt and adds no failure event.
The operator's existing message inspection exposes that code and next attempt
time. Expiry still makes blocked mail terminal before any later handoff.

The limit spaces submission reservations. Idle time does not accumulate burst
credits, and restart does not reset the last cooldown. Slots are conservatively
retained after any committed handoff, including definitive failure, connection
failure or an uncertain SMTP outcome. Confirmed non-acceptance does not refund a
slot. Existing retry backoff, pilot allowlists, suppression, producer access,
delivery holds, cancellation and budget accounting remain enforced. Pacing does
not extend message expiry, change relay custody or resolve uncertain outcomes.

This controls new maild submissions to the local relay. Postfix owns its existing
spool and final SMTP retry behavior; maild pacing is not a remote-MX rate or a
maximum concurrent-connection setting. Local scheduling, worker availability and
relay latency can make actual delivery slower than the configured interval.

Aggregate gauges under `maild_mail_domain_pacing_` expose `enabled`, `stats_up`,
`cooling_domains` and `waiting_recipients`. Waiting inventory counts due queued
or deferred work behind a cooldown, including racing claims requeued by pacing.
Disabled monitoring reads no pacing state. No domain, mailbox, producer or message
identity is used as a metric label.

For runtime rollback, set the pacing flag to `0` and restart maild. This bypasses
pacing waits, including already recorded `recipient_domain_paced` delays, while
normal retry backoff, scheduling, expiry and delivery holds still apply. Cooldown
metadata is retained; re-enabling pacing honors any outstanding reservation.
Changing the interval also preserves the already reserved cooldown. Migration
rollback refuses to discard persisted pacing rows; keep the upgraded schema.

## Protect delivery with relay backpressure

Apply
[`20261016112746813_internal_mail_relay_backpressure.sql`](../../migrations/20261016112746813_internal_mail_relay_backpressure.sql)
through the migration owner before deploying maild. Readiness requires its health
and transition tables even with the feature disabled. Upgrade every worker, the
`mail-metrics` binary and its optional systemd unit before enabling the feature.
No migration, example environment or unit activates delivery or backpressure.

The exact default-off flag is
`FAAS_INTERNAL_MAIL_RELAY_BACKPRESSURE_ENABLED=0`. An internal pilot must opt in
separately in maild's private environment and in
`/etc/faas/mail-reports/bridge.env`. The existing master/reports flags and separate
telemetry credential are required in both environments. maild rejects a startup
configuration that enables backpressure without reports. The helper's fixed
numeric loopback `FAAS_INTERNAL_MAIL_SMTP_ADDR` must identify the same dedicated
Postfix relay as maild's target. No message or HTTP caller selects a relay.

The existing optional 15-second metrics timer collects a fresh, bounded
`postqueue -j` inventory and probes the relay greeting, EHLO/HELO and QUIT.
The probe sends no envelope or DATA. When backpressure is disabled, there is no
new probe or health publication. The optional unit permits IPv4/IPv6 for the
probe and confines IP access to localhost; it remains unprivileged and retains
its existing Postfix queue-access group. An old AF_UNIX-only unit cannot perform
the probe and will hold delivery until upgraded. Collection/probe deadlines
and the existing queue-output bound prevent an unbounded helper pass.

The helper reports only sample ID/time, inventory/probe success, total queued
messages and oldest queued-message age. Totals cover the whole relay, including
unrelated mail and queued reports, so a shared backlog cannot hide overload.
Raw queue data, addresses, SMTP replies, credentials and mail bodies are never
published. Use one explicitly qualified relay/collector for each maild namespace;
the credential and socket select the namespace. The private health route grants
no submission, management or operator access to the telemetry credential.

Initial internal limits are centralized in `pkg/api/internal_mail_limits.go`:

| Condition | Hold behavior |
| --- | --- |
| No sample, failed inventory, or failed SMTP probe | Hold new submissions. |
| 100 or more queued messages | Hold for queue depth. |
| Oldest queued message reaches 10 minutes | Hold for queue age. Known age continues advancing between samples. |
| Sample or server receive time is 90 seconds old | Hold for stale evidence and reset recovery. |
| Healthy observations spanning two minutes, with no stale gap | Resume automatic protection's hold. |

Both database and collector clocks must stay synchronized; observations more
than five seconds in the future are rejected. The sample timestamp is captured
before collection, so slow collection does not make old evidence fresh. Reports
must increase the observed timestamp. An identical replay of the current sample
does not refresh its receive time or recovery window. Changes under the current
sample's ID, reordered older samples, missing fields, unknown JSON fields and oversized inputs are
rejected. A delayed replay after a newer sample has committed returns conflict;
the collector takes a new local observation on its next pass.

Health moves through `paused`, `recovering` and `healthy`. Fresh healthy samples
are required to finish recovery; elapsed wall time alone never resumes delivery.
State, recovery timestamps and reason changes survive restart. The worker records
stale evidence durably even while SMTP delivery is disabled. Held delivery passes
continue expiry/lease maintenance, leave queued mail encrypted and do not charge
attempts. With backpressure enabled, metadata-only timed expiry also continues
when SMTP and new scheduling access are disabled.

Automatic state is separate from operator delivery controls. A healthy relay
cannot clear a namespace/domain pause, resume a drain or revive cancellation.
Namespace transaction locks serialize health changes with first handoffs and
operator namespace controls, including the first health row insertion. Handoff
SQL rechecks current sample freshness and queue age after other lock waits.
A racing claim returns to `queued` with code `relay_backpressure`, refunds its
claim attempt and adds no delivery failure. A rejected handoff also rolls back
any new recipient-domain pacing reservation.

Already committed or in-flight submissions keep their existing custody. A relay
health failure never flushes, deletes or resubmits Postfix work, and never resolves
an uncertain outcome. Late reports, audited reconciliation, cancellation and
payload retention keep their existing gates and state fences. Admission budgets
remain charged when messages are enqueued. This protects new maild handoffs;
remote delivery and persistent SMTP retries remain Postfix's responsibility.

For private inspection, also enable the existing operator gates and the new
backpressure flag in the CLI environment, then run:

```sh
mailctl relay status
mailctl relay events --limit 25
```

These commands are read only. Status reports the effective hold and freshness;
the bounded event history records closed state/reason transitions under durable
revisions. Ordinary `mailctl delivery status` continues to describe operator
holds. Aggregate `maild_mail_relay_` metrics expose enabled/read success, hold,
freshness, sample age and fresh queue counts/age. `hold_reason` uses a fixed set
of reasons; no identity or address labels are added. Failed reads omit healthy
samples, and failed health publication marks the textfile collection unsuccessful.

Runtime rollback sets the backpressure flag to `0` in maild and the collector and
restarts those services. This bypasses automatic protection while retaining its
state and events; scheduling, expiry, pacing, suppression and manual holds remain
enforced. Re-enabling requires fresh healthy observations and honors persisted
recovery only when the prior sample remains fresh. Migration rollback refuses
to discard any persisted health or transition evidence; retain the upgraded schema.

## Consume durable delivery events

Apply
[`20261016123727973_internal_mail_event_feed.sql`](../../migrations/20261016123727973_internal_mail_event_feed.sql)
through the migration owner before deploying the updated maild. Readiness checks
the stream, projection, consumer and receipt tables even with the feature off.
Upgrade every worker before enabling it. No example, migration or unit activates
mail delivery or event feeds.

The exact default-off gate is `FAAS_INTERNAL_MAIL_EVENT_FEED_ENABLED=0`.
maild requires the existing master and producer gates and valid producer
enrollments when this flag is enabled. Each consumer uses only its existing
producer credential on the protected Unix API. Operator inspection/replay also
requires the existing operations gate and separate operator credential. SMTP,
reports, feedback, retention and reputation keep their independent gates; event
publication never decrypts a message or sends mail. Live delivery qualification
remains a separate prerequisite for SMTP use.

An independent bounded worker publishes committed delivery observations once
per second, at most 100 per transaction. The first enabled pass begins backfilling
retained events for named producers; unscoped service-token submissions remain
available through their existing metadata routes. Publication does not depend on
whether a historical producer is still enrolled. Producer withdrawal denies
consumer access while preserving its history and checkpoints.

Each producer has an ordered stream. Publication serializes by namespace and
allocates positions in the same transaction as the projection. A source event
that commits late receives a later published position, even if its timestamp or
ID precedes events already consumed. Consumers never use source timestamps,
UUID ordering or a nontransactional sequence as a high-water mark. Raw source
events remain authoritative; a failed publication transaction can retry without
losing a source event or spending a stream position. Replicas deduplicate each
source observation and retain stable feed event UUIDs across restart and replay.

The feed includes `queued`, `submitted`, `accepted`, `bounced`, `complained`,
`deferred`, `uncertain`, `failed`, `suppressed`, `expired`, `cancelled` and `retry`
observations. SQL-driven expiry, cancellation and lease recovery use the same
source journal. An event is an observation, not a current-state snapshot:
late reports can follow earlier acceptance, and acceptance still means SMTP
acceptance rather than inbox placement. Fetch the producer-owned message view
when a current recipient state is needed.

Events contain only their feed ID, a per-producer sequence encoded as a decimal
JSON string, message/recipient/domain IDs, kind, original observation time and
publication time. They exclude addresses, subjects, bodies, credentials, signing
material, provider diagnostics and raw source event IDs. The latter can originate
in trusted report inputs and are deliberately not exposed as consumer identifiers.

Consumer names are stable lower-case slugs of at most 32 bytes. At most eight
consumers may be registered per producer. The first pull registers a consumer at
the beginning of retained history, subject to that atomic budget. Each name has
one durable checkpoint and at most one outstanding batch. Different consumer
names process independently; replicas sharing a name can receive the same batch
and must coordinate/deduplicate their application effects.

Producer routes use the existing producer token:

| Method | Private route suffix under `/internal/v1/mail/producers/{producer}` | Body/result |
| --- | --- | --- |
| POST | `/event-consumers/{consumer}/pull` | `{"limit":25}` or `{}` returns a batch, generation, saved cursor, next cursor and receipt. |
| POST | `/event-consumers/{consumer}/ack` | `{"receipt":"UUID","generation":1}` acknowledges exactly that saved batch. |
| GET | `/event-consumers/{consumer}` | Returns the saved checkpoint, pending receipt, generation, backlog and timing metadata. |

Pulls accept 1..100 events, defaulting to 25. An outstanding batch is immutable:
repeated pulls return its original events and receipt even if a caller changes
the requested limit or newer events are published. There is no batch lease that
silently advances progress. An empty poll returns no receipt and preserves the
cursor; empty polls never acknowledge anything. Status reads do not register a
consumer or change progress.

Commit application effects with deduplication by feed event ID, then acknowledge
the receipt and generation. Acknowledgment advances only to the saved end of
that batch; a caller cannot supply a forward cursor or select a subset. Lost pull
responses replay the pending batch. Lost acknowledgment responses can replay the
same receipt without changing a newer checkpoint or pending batch. Receipt
history is retained within its namespace, producer, consumer and generation.
This provides at-least-once processing when the consumer acknowledges after its
effects commit. It cannot make effects in another service exactly once without
that service's own durable deduplication.

The internal Go helper is `internalmail.NewEventFeedClient(socket, producerID,
producerToken, enabled)`. Its callback should check the caller's master, producer
and event-feed gates before configuration/credential loading. It exposes `Pull`,
`Ack`, `Status` and `ConsumeOnce`. `ConsumeOnce` invokes the application's handler
for each event and acknowledges only after all calls succeed. Handler failure,
cancellation, flag revocation or a lost response leaves retry/recovery to the
caller; already completed effects may repeat and need the same deduplication.
The client uses Unix sockets only, ignores HTTP proxies, follows no redirects,
bounds response size and performs no automatic request retries. Do not run an
unbounded tight poll loop; use bounded consumer passes and backoff on failures.

For inspection, opt in only in the private operator environment and run:

```sh
mailctl events consumers --producer auth --limit 25
mailctl events status --producer auth --consumer delivery-effects
```

Lists use consumer-name cursors. Producer credentials cannot operate on other
producers or use operator routes; service, report and feedback tokens cannot
consume feeds. Operator views remain available for withdrawn producer metadata
under their separately enabled gates.

An operator can rewind a consumer to an owned feed cursor or the beginning.
Rewind requires a saved request UUID, audit evidence and the observed generation:

```sh
mailctl events replay --producer auth --consumer delivery-effects \
  --from-start --expected-generation "$GENERATION" \
  --request-id "$REQUEST_ID" --actor operator.name \
  --reason consumer_recovery --evidence ticket:mail-replay
```

Use `--after "$EVENT_ID"` instead of `--from-start` to resume after a particular
owned feed event. Forward skips are rejected: the target must be at or behind
the saved acknowledged checkpoint. Rewinding to that same checkpoint can replace
an outstanding batch without dropping its events. Rewind increments generation,
clears the pending receipt and atomically records the operation. Acknowledgments
from a prior generation conflict, including old acknowledgment retries. Request
replay returns the original operation result and cannot reapply an old rewind
after newer processing/recovery. Changed arguments and stale generations conflict.
Application deduplication remains necessary for effects already applied when a
generation changes; rewinding does not undo them or resend email.

Private `maild_mail_event_feed_` gauges expose the gate, read success, unpublished
source backlog, published events, registered consumers, pending batches, total
unacknowledged events, maximum consumer backlog and oldest unacknowledged age
since publication. Independent consumers intentionally count the same event more
than once in the total backlog. There are no producer, consumer, domain, address
or message labels. A disabled monitor reads no feed state; failed reads omit
inventory and expose `stats_up=0`. Use private consumer status for individual lag.

Runtime rollback sets the feed flag to `0` and restarts maild/callers. It stops
publication and consumer access while retaining projections, checkpoints, pending
batches, receipts and audits. Existing delivery, feedback and custody handling
continues under its own gates. Reenabling resumes saved consumers and backfills
events committed during the disabled interval. Payload retention remains
independent: feed metadata does not require message bodies or signing keys.
There is no feed/receipt pruning in this internal pilot. Schema rollback refuses
to discard any stream, projection, registered consumer, receipt or replay audit.

## Protect sender reputation with reviewed holds

Apply
[`20261016120104359_internal_mail_reputation.sql`](../../migrations/20261016120104359_internal_mail_reputation.sql)
through the migration owner and upgrade every maild worker before opting in.
Readiness checks the new tables even with protection disabled. No example, unit
or migration enables the service. The exact default-off gate is
`FAAS_INTERNAL_MAIL_REPUTATION_ENABLED=0` in maild's private environment and the
private operator environment. Protection requires the existing master and
authenticated provider feedback gates and valid source enrollments. Operator
inspection/release additionally requires the operations gate and its separate
credential. No new credential, public route, listener or provider enrollment is
introduced. Live relay/provider qualification remains a separate prerequisite.

Protection evaluates each stable producer ID and each sending domain independently
within maild's namespace. Legacy service-token submissions belong to the empty
producer scope, selected with `--legacy`. IDs survive credential rotation and
producer withdrawal. Holds fence new SMTP handoffs and allow bounded admission
to continue under the existing daily/pending budgets.

The fixed pilot policy lives in `pkg/api/internal_mail_limits.go`:

| Condition within the rolling 24-hour window | Required evidence |
| --- | --- |
| Hard bounce rate at least 5% | At least 20 distinct recipient deliveries and at least 3 hard bounces. |
| Complaint rate at least 0.1% | At least 20 distinct recipient deliveries and at least 2 complaints. |

Each recipient delivery counts once, including safe retries of that delivery.
The denominator is the union of recipients whose **first durable relay-custody
evidence** was recorded within the window and recipients whose first authenticated
adverse feedback was received within the window. Custody evidence comprises
submitted, uncertain, accepted, bounced or complained events; enqueue/claim,
local failures, suppression, cancellation and expiry do not count. The union
keeps late feedback in both numerator and denominator, including when its first
handoff is older than 24 hours. These are conservative activity-window rates,
not an inbox-placement metric or a guarantee that every provider reports outcomes.
The left boundary is exclusive and the right boundary inclusive, using database
time sampled after lock waits. The worker reevaluates unreviewed evidence as
traffic ages or the sample floor is reached, including while SMTP is disabled.

Only the existing DKIM-authenticated, enrolled provider path can record adverse
evidence: permanent `dsn_5.*` failures and ARF abuse reports. Generic service JSON
reports and local Postfix DSNs keep their normal delivery/suppression behavior
but cannot impersonate provider authentication by choosing an event ID. Deferred
reports and final queue expiry with a temporary `dsn_4.*` status never count as
hard bounces. First bounce/complaint receipt times never move on replay, even
under a different provider report identity. A complaint supersedes a bounce for
that recipient; it never adds a second sample. Raw feedback, message bodies,
provider diagnostics and credentials are not retained by reputation protection.

Feedback, its normal recipient/suppression updates, both scope holds and their
trigger snapshots commit atomically before acknowledgment. The namespace lock
serializes holds and releases with the start of every new SMTP handoff, including
the first hold insertion. A claim racing a hold returns to `queued` with code
`reputation_held`, refunds its attempt and rolls back any pacing reservation.
Held candidate selection leaves payloads encrypted; expiry and lease recovery
continue. Committed/in-flight SMTP work retains its submitted or uncertain
custody, and the feature never deletes, retries or flushes Postfix work. Late
feedback, cancellation, retention and operator reconciliation retain their own
gates and fences. Automatic holds are separate from manual pauses/drains, relay
backpressure, DNS authorization and mailbox suppression.

Use the existing private CLI environment to inspect the current rolling window,
the immutable trigger snapshot and transition history:

Rates in JSON are fractions (`0.05` means 5%); counts remain the policy evidence.

```sh
mailctl reputation status --producer auth
mailctl reputation status --domain-id "$DOMAIN_ID"
mailctl reputation status --legacy
mailctl reputation holds --limit 25
mailctl reputation events --limit 25
```

Hold cursors use `scope:key`; event cursors use UUIDs and follow durable database
sequence order. Lists are bounded and namespace-specific. There is no automatic
recovery when a rate drops or its samples age out. Review the source, recipient
selection and evidence, correct the cause, then release each affected scope
explicitly using its current revision. Release asserts review under the existing
shared operator credential; `actor` is an operator assertion. For example:

```sh
mailctl reputation release --producer auth \
  --expected-revision "$REVISION" --confirmed-reviewed \
  --request-id "$REQUEST_ID" --actor operator.name \
  --reason sender_reviewed --evidence ticket:mail-review
```

Save a fresh request UUID before the mutation; repeat it with identical arguments
after a lost response. Changed arguments conflict, and an old replay returns its
original result without clearing a newer hold. A stale revision is rejected.
Release acknowledges all evidence received before its namespace fence and may
be authorized even while the observed rate remains high. Existing evidence and
ordinary polling cannot undo that review; a new adverse recipient observation
or a first complaint upgrading a prior bounce can trip the policy again. Releasing
a producer leaves its domain's hold intact and vice versa. Manual holds and
suppressions require their own independent handling.

Private `maild_mail_reputation_` metrics expose the gate, read success, held-scope
counts by fixed `scope`/`reason`, and distinct adverse recipients by fixed `kind`.
Use `mailctl` for individual rates and evidence: no producer, domain, address,
message or event IDs appear as metric labels. A disabled monitor performs no
reputation reads; failed reads omit inventory and expose `stats_up=0`.

Runtime rollback sets the reputation flag to `0` and restarts maild. This bypasses
automatic reputation enforcement while preserving its ledger, holds, transitions
and review watermarks. It keeps existing manual, relay, timing, authorization,
producer and suppression controls in force. Feedback processed while protection
is disabled does not populate the new ledger, and replaying those reports after
reenabling does not backfill authentication evidence. Reenable only with that
coverage gap understood; saved holds immediately apply again. Schema rollback
refuses any retained feedback, hold, transition or reputation operator evidence.

## Operate through the private CLI

Build `mailctl` with the other mail binaries. Apply
[`20261015154854536_internal_mail_operations_audit.sql`](../../migrations/20261015154854536_internal_mail_operations_audit.sql)
through apid before deploying the updated maild, even with operations disabled.
Readiness now checks for the audit table. Neither the CLI nor maild migrates it.

Operations remain disabled until both `FAAS_INTERNAL_MAIL_ENABLED=1` and
`FAAS_INTERNAL_MAIL_OPERATIONS_ENABLED=1` are present in maild's private
environment and the CLI's environment. Configure a random
`FAAS_INTERNAL_MAIL_OPERATIONS_TOKEN` of at least 32 bytes, distinct from the
service token, report token and every enrolled feedback token. The operator
credential grants only the `/internal/v1/mail/operations/` routes; it cannot
send messages, submit trusted reports, activate domains or access the service
metrics route. Other credentials cannot use the operator routes.

Prepare `/etc/faas/mail-operations/operator.env` from
[`operator.env.example`](../../deploy/internal-mail/operator.env.example),
with a root-owned `0750` directory and a root:faas `0640` file. Provision only
the master and operations gates, any separately authorized feature gates, socket
path and operator token there. Run locally as an approved
operator with Unix socket access. Keep the token in the private environment;
the CLI accepts no credential argument and has no remote HTTP mode. It follows
no redirects, uses no HTTP proxy, and retries no request automatically. The
Ansible role does not provision operator access or enable this gate.

With that operator-owned environment loaded, these commands emit JSON:

```sh
mailctl messages list --status uncertain --limit 25
mailctl messages show --id "$MESSAGE_ID"
mailctl messages events --id "$MESSAGE_ID" --limit 25
mailctl domains list
mailctl domains show --id "$DOMAIN_ID"
mailctl domains check --id "$DOMAIN_ID"
mailctl suppressions list
mailctl audit list
mailctl retention preview
```

Lists default to 25 entries and permit `1..100`. Pass the returned `next` to
`--after` with the same filters. Ordering is ascending resource ID, or address
for suppressions, rather than time. Each page is a current observation, not a
snapshot of concurrent arrivals. Message inspection includes recipient status,
attempt count, next attempt time and lease expiry, plus whether content was
pruned. Bodies, signing keys, fingerprints, idempotency keys and lease tokens
are never fetched by the inspection queries. Event history is paginated so an
operator can inspect beyond the original service route's event cap.

`domains check` observes the ownership, DKIM, SPF and DMARC records using the
same verification rules as admission. It reports matched, missing/mismatched
or lookup-failed per record and the stored domain status. It never updates
that status. DNS readiness does not establish inbox placement or complete live
delivery qualification. Suppression review is read-only; there is no bulk
release or resend command.

Reconcile only after reviewing the recipient's stable Message-ID/envelope
correlation against the durable relay queue and logs. Save a fresh UUID as the
request ID before each intended mutation. For confirmed relay custody:

```sh
mailctl uncertain resolve --id "$RECIPIENT_ID" --outcome submitted \
  --confirmed-relay-accepted --request-id "$REQUEST_ID" \
  --actor "$OPERATOR" --reason relay_custody_confirmed --evidence "$EVIDENCE_REF"
```

This changes `uncertain` to `submitted` without an SMTP submission. It does not
claim destination acceptance or inbox delivery. If evidence establishes the
relay did **not** accept the original attempt, explicitly use
`--outcome retry --confirmed-not-accepted` instead. An active submission lease
cannot be overridden; an expired submitting lease may first be recovered to
uncertain in the same transaction. Every other current state returns conflict.
If acceptance remains unknown, keep it uncertain. The tool cannot prove facts
about an external relay. A retry retains attempt/age limits and all existing
domain, suppression, allowlist and dispatch gates.

For one bounded cleanup batch after a preview:

```sh
mailctl retention apply --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason retention_reviewed --evidence "$EVIDENCE_REF"
```

Both retention commands require the separate retention gate. Applying also
requires `FAAS_INTERNAL_MAIL_RETENTION_DRY_RUN=0` in maild's environment. The
operator cannot override the configured quiet period, batch size or protected
states. Enabling cleanup mode also permits the existing hourly worker to run;
it is not a manual-only mode.

Each successful operator mutation and its audit entry commit atomically.
Records retain the asserted actor, reason, evidence reference, target and
result. Actor is supplied by the operator under the shared credential, not
authenticated by customer login. Reasons are short `[a-z][a-z0-9_]{0,63}` slugs;
evidence must be a bounded reference containing only letters, digits and
`:/._-`, starting with a letter or digit. Never include bodies, credentials or
raw logs. After a lost response, inspect `audit list` and repeat the **same**
request ID and arguments: it returns the original result without another
transition or cleanup batch, including after restart. Contradictory reuse
returns conflict. A new request ID authorizes a new operation.

Disable the operations gate and restart maild to remove operator access while
preserving evidence. The migration refuses rollback after any audit is recorded.
Audits cover these operator mutations; automatic worker passes retain their
existing aggregate monitoring. Live delivery qualification remains separate.

## Pause, drain and cancel queued delivery

Apply
[`20261016085426393_internal_mail_delivery_controls.sql`](../../migrations/20261016085426393_internal_mail_delivery_controls.sql)
through the migration owner before deploying the updated maild. Readiness
requires the new table even when controls are disabled. New commands require
`FAAS_INTERNAL_MAIL_DELIVERY_CONTROLS_ENABLED=1`, the master and operations gates,
and the private operator credential in both maild and the CLI environment.
The example environments and optional unit keep every gate at `0`.

Choose exactly one scope: `--namespace` means maild's configured namespace;
`--domain-id` selects one domain within it. A namespace hold also blocks domains
whose own mode is running. Each scope starts at revision `0` with mode `running`.
Inspect it before a mutation, then use that revision to protect against a
concurrent operator change:

```sh
mailctl delivery status --namespace
mailctl delivery status --domain-id "$DOMAIN_ID"
mailctl delivery pause --namespace --expected-revision "$REVISION" \
  --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason maintenance --evidence "$EVIDENCE_REF"
```

Pause prevents new maild SMTP handoffs. Admission continues under its existing
quotas, so queued work may accumulate. Attempt and age limits still apply while
held. The worker checks the saved holds before
decryption and again under a database fence before marking a submission started.
Claims predating a hold return to the queue without charging an unsubmitted
attempt. Submissions that already crossed that fence may finish. Reports,
feedback, retention and operator inspection keep their independent gates.

Holds survive daemon restarts and disabling the controls or operations gate.
Removing access never resumes delivery. Resuming requires a fresh revision and
all existing dispatch, domain, allowlist and suppression checks still apply:

```sh
mailctl delivery resume --namespace --expected-revision "$REVISION" \
  --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason maintenance_complete --evidence "$EVIDENCE_REF"
```

Save a new request ID for each intended mutation. Repeating the identical
request after a lost response returns its original audit result. It does not
reapply an old pause or resume over a later change; inspect current status after
a replay. A stale revision or contradictory reuse returns conflict.

For maintenance that needs local activity to finish, use a drain:

```sh
mailctl delivery drain --domain-id "$DOMAIN_ID" --expected-revision "$REVISION" \
  --timeout 30s --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason relay_maintenance --evidence "$EVIDENCE_REF"
```

This saves a `draining` hold and waits for active claims and local SMTP attempts.
Queued mail stays queued. Expired claims cannot cross their lease fence; expired
submitting leases count as uncertain. Unknown submission custody blocks drain
completion until trusted reports or evidence-based reconciliation resolve it.
`relay_owned` reports already-submitted recipients separately: a local drain
does not wait for downstream Postfix delivery and never alters its queue.
It also does not establish the queue evidence required to retire a DKIM key.

The wait defaults to 30 seconds, with a five-minute maximum. JSON includes the
committed operation, latest status and `complete`. Timeout, interruption,
uncertain custody or a concurrent scope change returns a nonzero exit status.
The command never automatically resumes a saved hold. Inspect the current scope
before deciding whether to continue waiting, reconcile or resume it.

Cancel individual recipients or all eligible recipients in one bounded message:

```sh
mailctl recipients cancel --id "$RECIPIENT_ID" \
  --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason notification_withdrawn --evidence "$EVIDENCE_REF"
mailctl messages cancel --id "$MESSAGE_ID" \
  --request-id "$REQUEST_ID" --actor "$OPERATOR" \
  --reason notification_withdrawn --evidence "$EVIDENCE_REF"
```

Each example represents a separate mutation and needs its own saved request ID.
Only `queued`, `deferred` and `claimed` recipients can become terminal
`cancelled`; cancellation clears the lease so a racing worker cannot submit or
revive them. Already-cancelled recipients are reported separately. Submitting,
submitted, uncertain and other terminal recipients are protected. Mixed messages
commit eligible cancellations and list protected recipients; `complete=false`
and a nonzero CLI exit signal this partial result. No SMTP recall is implied.

Cancellation, recipient events and the audit commit together without reading
bodies or signing keys. Idempotent send retries retain the original cancelled
state, including after payload pruning. Cancellation does not refund daily
admission usage. Its new observation timestamp starts the retention quiet period;
the entire message must otherwise satisfy the existing cleanup policy.

Aggregate `maild_mail_controls_paused_scopes` and
`maild_mail_controls_draining_scopes` expose saved hold counts without identifiers.
Existing backlog alerts may reflect an intentional hold; inspect status before
resuming it. Metrics collection has its own enabled and read-success gauges.

The migration refuses rollback while a hold exists or any new control or
cancellation audit/event is present. Keep a binary that understands persisted
holds and `cancelled` state; disabling the feature is the runtime rollback.
Never remove evidence or reset recipient states to bypass the rollback guard.

## Rotate DKIM keys and maintain domain authorization

Apply [`20261015173147723_internal_mail_domain_lifecycle.sql`](../../migrations/20261015173147723_internal_mail_domain_lifecycle.sql)
through apid before deploying the upgraded maild, including when both new gates
remain disabled. Readiness checks for the key-history table, authorization
columns and recipient signer tracking. The migration preserves the original
signer as a legacy active key without changing its ciphertext or DNS selector.
Existing domains start with unchecked recurring authorization. No DNS record,
live signing key or deployment is changed by installing this migration.

### Stage, verify and activate a new signer

Set the master, operations and `FAAS_INTERNAL_MAIL_DKIM_ROTATION_ENABLED` gates
to exact `1` in the private daemon and operator environments when intentionally
starting this workflow. Use the existing separate operator credential. These
commands are local Unix-socket operations, with the same audit fields and saved
request-ID requirements as other `mailctl` mutations:

```sh
mailctl domains keys --id "$DOMAIN_ID" --limit 25
mailctl domains stage-key --id "$DOMAIN_ID" --request-id "$STAGE_REQUEST_ID" \
  --actor "$OPERATOR" --reason dkim_rotation_planned --evidence "$EVIDENCE_REF"
```

Staging generates a new RSA-2048 key and unique selector. The private key is
encrypted under the dedicated age identity and bound to its namespace, domain
and key ID. The result contains the public TXT record to publish. The current
signer continues serving queued work. Only one staged key is allowed per
domain, with at most four active, staged or retiring keys in total. Key history
and retired public records remain available through paginated inspection.

Publish the returned TXT record through the normal operator-owned DNS process,
then verify it:

```sh
mailctl domains verify-key --id "$DOMAIN_ID" --key-id "$KEY_ID" \
  --request-id "$VERIFY_REQUEST_ID" --actor "$OPERATOR" \
  --reason dkim_record_published --evidence "$EVIDENCE_REF"
```

The observation is durably audited, including a DNS mismatch or unavailable
lookup. Its JSON includes `check_result`, `first_verified_at` and
`activate_after`. Allow at least one hour after the first successful publication
observation before activation. This minimum observation window does not prove
every external resolver has refreshed its cache; allow longer where existing
DNS TTLs or operational requirements demand it. Use a **new** request ID for a
new DNS observation. Reusing an ID returns its original result without looking
up DNS again. The CLI exits nonzero for a negative observation while still
printing the committed audit result.

```sh
mailctl domains activate-key --id "$DOMAIN_ID" --key-id "$KEY_ID" \
  --request-id "$ACTIVATE_REQUEST_ID" --actor "$OPERATOR" \
  --reason dkim_propagation_reviewed --evidence "$EVIDENCE_REF"
```

Activation checks the ownership proof, source-IP SPF, DMARC, new DKIM record,
current DKIM record and every retiring selector that still needs publication.
It then atomically selects the new key, retains the previous key as retiring,
records fresh authorization and commits its audit. The submission boundary
locks the same domain row and compares the selector used to render the MIME.
An old render cannot begin a new submission after activation. An attempt that
already began retains its signer attribution and may complete normally; its
existing ciphertext, stable Message-ID and relay custody are preserved.

An early activation returns `409 dkim_propagation_wait`. If the propagation
window has elapsed but live DNS checks fail, the attempt instead records an
audited staged-key result with a negative `check_result`; it does not change
the active signer, and the CLI exits nonzero. Repair DNS and verify again with
a new request ID. A confirmed mismatch resets the publication observation
window. A transient lookup failure preserves the prior first observation,
but verification must succeed again before activation.

### Retain and retire old selectors

Keep old DNS TXT records published for at least seven days after deactivation.
Retirement is blocked while any recipient is `submitting`, `submitted` or
`uncertain` under that selector. Legacy unresolved submissions with no recorded
selector conservatively block retirement for the whole sending domain. A lost
relay response must be reconciled using the existing evidence-based workflow;
do not mark an uncertain attempt complete merely to retire a key.

After the overlap window and a separate review of relay custody:

```sh
mailctl domains retire-key --id "$DOMAIN_ID" --key-id "$OLD_KEY_ID" \
  --confirmed-queue-drained --request-id "$RETIRE_REQUEST_ID" \
  --actor "$OPERATOR" --reason old_signer_queue_drained --evidence "$EVIDENCE_REF"
```

This clears the retired key's encrypted private material while retaining public
DNS instructions, history and audit evidence. It does not edit DNS or claim
that every downstream system has finished using the signature. Operators may
keep old public records longer. The current active key cannot be retired. The
same command can discard an unused staged key immediately, with explicit
audit/evidence fields, to unblock a replacement stage. No activation or resend
is implied by retiring or discarding a key.

### Recheck domain authorization

The separate `FAAS_INTERNAL_MAIL_DOMAIN_REVERIFY_ENABLED=1` gate enables a
bounded worker independently of delivery, reports, rotation and operator
access. It considers ten due verified domains per pass, runs at startup and
every minute, and normally rechecks each domain hourly. Each domain has a
ten-second DNS budget; a pass has a thirty-second budget. Delayed/cancelled
passes leave remaining work for subsequent passes. Checks use public metadata
without decrypting bodies or signing keys.

While this gate is enabled, new admission, claims and submission require a
successful authorization check within the preceding 24 hours for the current
configured sending IP. Enabling it initially holds unchecked legacy domains
until their first successful check. DNS records use the same direct-IP SPF
and DKIM/DMARC rules as initial verification, including retained selectors.
Multiple DKIM/DMARC records or duplicate tags do not establish readiness.

Two record mismatches or NXDOMAIN observations, separated by at least five
minutes with no intervening success, persist an authorization pause. Repeating
an observation quickly cannot confirm it twice. A timeout, SERVFAIL or other
lookup failure is recorded as unavailable, without advancing the confirmed
failure count. It also cannot refresh the successful proof; eventually stale
authorization stops new work. A successful full check clears the pause.
Disabled domains are never re-enabled by this worker.

Paused or stale domains are skipped before recipient claims, preserving queued
attempt budgets. Existing overall queue-age and attempt caps still apply.
The submission boundary checks authorization again to fence changes after a
claim. Previously submitted mail remains with the relay and still accepts
delivery/feedback observations. Checks and selector changes use revision and
observation fences, so an older DNS result cannot overwrite a new signer,
retirement decision, disabled status or newer proof.

For an immediate, audited observation:

```sh
mailctl domains reverify --id "$DOMAIN_ID" --request-id "$CHECK_REQUEST_ID" \
  --actor "$OPERATOR" --reason dns_authorization_reviewed --evidence "$EVIDENCE_REF"
mailctl domains show --id "$DOMAIN_ID"
```

Both daemon and operator environments must opt into re-verification for this
command. The result includes authorization state, check result, check/success
timestamps, verified sending IP and confirmed failure count. It records a
negative observation without claiming domain activation; the CLI exits nonzero
for a negative result. Periodic observations persist the same state without
creating operator audit entries. Freshness and confirmed failures survive
restarts. Keep routine `domains check` available for read-only troubleshooting.

`InternalMailDKIMRotationFailed` warns about failed staged-key observations,
stages older than 24 hours, or unavailable lifecycle statistics.
`InternalMailDomainAuthorizationFailed` warns about paused/stale authorization,
unavailable DNS, failed statistics or a failed/stale periodic worker. All metrics
are namespace aggregates without selectors, domains, addresses or key material.
Inspect `domains show`, `domains keys`, and `audit list` locally to diagnose a
warning. Neither monitoring nor the worker publishes DNS or performs live
delivery qualification.

Disabling the rotation gate hides its operations while preserving the selected
signer and old records. Disabling recurring checks stops its DNS work and
freshness requirement, but a confirmed persisted pause still blocks admission
and dispatch until a successful check with re-verification enabled. Keep the
lifecycle-aware binary after staging/activation; an older binary cannot decode
the new signing-key resource binding. Schema rollback refuses to discard staged
keys, authorization evidence or lifecycle audit records. Do not erase those
records to force a downgrade.

## Pilot monitoring

`maild` exports durable aggregate mail gauges, including
`maild_mail_feedback_enabled`, on its existing authenticated
`/metrics` socket endpoint. The telemetry token can read only the mail-specific
`GET /internal/v1/mail/monitoring` view. That view requires both the master and
reports gates, exposes no message IDs, domains, addresses or diagnostics, and
does not inspect Postfix's protected queue.

Build and deploy `mail-metrics` alongside the other mail binaries. Setting
`faas_internal_mail_metrics_enabled=true` installs and starts a 15-second
one-shot timer after the private service and telemetry environment are ready.
It requires the existing `node_exporter` role and the relay's `postdrop` group.
The helper runs as `faas:postdrop`, with supplementary `faas node_exporter`
groups, `NoNewPrivileges=yes`, no capabilities and no TCP socket access. This
allows the maintained read-only `/usr/sbin/postqueue -j` inventory command to
work without changing maild's `faas:faas` identity or privileges. No queue
flush, hold, delete or retry command is invoked by monitoring.

The helper combines the private view with local relay queue counts and
atomically publishes `/var/lib/node_exporter/textfile_collector/internal-mail.prom`.
The existing node_exporter scrape picks it up. Only aggregate metrics are
written. A failed private collection replaces old healthy samples with
`maild_mail_monitoring_up 0` and the attempt timestamp. Database and Postfix
inventory failures have independent `stats_up` gauges and omit affected queue
samples. Turning monitoring off through the role stops its timer and removes
the aggregate file; encrypted mail and queued reports remain intact.

The `maild_mail_*` gauges cover current recipient states, the oldest maild-owned
work, time waiting for final relay reports, namespace suppressions, recent
events, and distinct current terminal outcomes recorded in the last 24 hours.
These counts come from durable rows and survive daemon restarts. Event and
outcome windows are gauges; do not apply `rate()` to them. Database metrics
are scoped to the configured namespace; Postfix gauges cover all Gregale-shaped
envelopes in the dedicated relay instance. Raw queue output is size-bounded,
discarded after aggregation, and never enters logs or labels.

The existing Prometheus rule file includes seven internal warning alerts:

| Alert | Trigger | Action |
| --- | --- | --- |
| `InternalMailMonitoringUnavailable` | Failed collection or samples older than two minutes, sustained two minutes. | Check the optional timer, telemetry environment and private socket. |
| `InternalMailStatisticsUnavailable` | Database or Postfix inventory unavailable for two minutes. | Check maild readiness, Postfix and the helper's group permissions. |
| `InternalMailQueueStalled` | Maild work older than five minutes or relay delivery older than 15 minutes, sustained five minutes. | Inspect queue ownership, relay/DNS connectivity and recipient status. |
| `InternalMailReportsStuck` | A report queued for over two minutes or a submission without a final report for over two hours, sustained two minutes. | Repair the private report pipe before Postfix's bounded retention expires. |
| `InternalMailUncertainSubmission` | An uncertain recipient remains for two minutes. | Reconcile spool/log evidence before considering an audited retry. |
| `InternalMailBounceRateHigh` | More than 20% bounced among at least five accepted/bounced current outcomes in 24 hours, sustained five minutes. | Pause dispatch and inspect destination failures before expanding the pilot. |
| `InternalMailComplaint` | A genuine recorded complaint, sustained one minute. | Pause the pilot and review the notification and recipient approval. |
| `InternalMailRetentionFailed` | Enabled retention has a failed/stale pass or unavailable statistics, sustained two minutes. | Keep previews enabled, repair database/key access or corruption, and inspect a fresh preview before applying cleanup. |

With the optional timer disabled, the mail metric series are absent and these
rules remain inactive. Verify the deployed timer and a fresh successful sample
before relying on their silence. For a runtime rollback, stop the monitoring
timer and remove its aggregate file through the role after disabling sending.

## Internal API

All endpoints below require `Authorization: Bearer <service token>` over the
Unix socket. JSON mutations require `Content-Type: application/json`. Inspect
readiness and metrics with authenticated `GET /readyz` and `GET /metrics` on the
same socket. Message status endpoints and worker logs exclude mail bodies,
credentials and DKIM private keys.

1. `POST /internal/v1/mail/domains` with `{"name":"example.com"}` registers a
   domain and returns required TXT records. Publish ownership proof, the
   generated DKIM key, SPF authorizing the configured IP, and DMARC. Merge the
   IP mechanism into existing SPF; do not create a second SPF record. Split long
   DKIM values into character strings within one TXT record as required by DNS.
2. `POST /internal/v1/mail/domains/<id>/verify` checks the records. Sending is
   rejected until verification succeeds. `GET /internal/v1/mail/domains/<id>`
   returns current status and DNS setup records.
3. `POST /internal/v1/mail/messages` accepts
   `{"from":"notify@example.com","to":["person@example.net"],"subject":"Hello","text":"Hello"}`.
   Supply a stable `Idempotency-Key` per logical message. Optional `html` and
   validated additional `headers` are supported. A `202` means the message and
   recipient outbox are durably committed. Reusing a key with another payload
   returns `409`; repeating the same payload returns the original message.
4. `GET /internal/v1/mail/messages/<id>` reads each recipient's state.
   `GET /internal/v1/mail/messages/<id>/events` reads bounded event history.
   `submitted` means local relay acceptance; `accepted` means an authenticated
   report says the destination SMTP server accepted it. Neither proves inbox
   placement.
5. `POST /internal/v1/mail/domains/<id>/disable` prevents new messages and
   stops queued mail under that domain. Already accepted relay work must be
   managed in Postfix. Re-enablement is not exposed in this pilot API.

The safeguards in `pkg/api/internal_mail_limits.go` cap configurable admission
at 100 recipient submissions per UTC day per namespace (pilot default 10), at
most 10 recipients per message, and
256 KiB combined text/HTML. Idempotent repeats do not consume the allowance.
Queued work expires after 24 hours and has at most eight claim attempts. These
values make no customer pricing or capacity commitment.

## Reports, uncertainty and rollback

`POST /internal/v1/mail/reports` accepts an authenticated relay observation:

```json
{
  "event_id": "stable-relay-event-id",
  "recipient_id": "recipient-uuid-from-the-message-status",
  "kind": "accepted",
  "code": "smtp_250"
}
```

Kinds are `accepted`, `bounced`, `complained` and `deferred`. Stable event IDs
deduplicate redelivery and reject contradictory reuse. Hard bounces and complaints
suppress that recipient within the namespace. A relay `deferred` report appends
evidence while retaining relay ownership; it never creates another submission.
Integrate genuine relay/DSN/feedback-loop observations. There is no public
provider webhook.

A lost DATA response or expired submitting lease is `uncertain`. Search the
relay's spool/logs using the stable recipient UUID/Message-ID before acting.
Confirmed delivery can be reported as `accepted`. Only after confirming that
the relay did not accept the message may an operator submit an audited report
with kind `retry`, code `confirmed_not_accepted`, and a fresh stable event ID.
That returns the recipient to the bounded queue. Do not guess non-acceptance
or claim exactly-once SMTP delivery.

For rollback, set the delivery flag to `0` and restart maild to stop new
submissions. Flags are process environment settings; SIGHUP only reloads the
logger. Already accepted Postfix work continues unless the operator separately
holds its queue. Restore the previous platform mail transport before disabling
the master flag on its callers. Set the master flag to `0` and stop/disable the
optional unit to remove the private API. Retain encrypted state and the
dedicated identity for inspection; never drop state to disable the feature.

Before enabling an internal pilot, qualify the actual source IP, DNS, Postfix
persistence/restart behavior, local reports, any inbound routes, and delivery to the intended
mailbox providers. Automated tests use a local SMTP peer and disposable
PostgreSQL; they do not establish external deliverability.


## Fair producer scheduling and short-window limits

`FAAS_INTERNAL_MAIL_PRODUCER_SCHEDULING_ENABLED=0` is the default in the optional
unit and private environment examples. Exact `1` with the master gate enables
this scheduler in maild; the independent delivery gate still controls SMTP.
Named callers also need the existing producer enrollment and gate. Without
named producers, the scheduler can protect the legacy service-token bucket.

Eligible producers take turns using a durable least-recently-served counter.
The counter and claim commit together under namespace serialization, so worker
concurrency and restarts preserve the rotation. Each producer retains FIFO
order among its eligible recipients. Future schedules, retries, recipient-domain
cooldowns, sender withdrawal, authorization, manual holds, relay health and
reputation holds retain their existing eligibility rules. Expiry and uncertain
submission recovery continue even if every producer is rate limited. Claiming
a recipient consumes a turn but does not consume sending capacity.

The optional producer enrollment field `dispatch_per_minute` defaults to `10`
and accepts whole numbers in `1..100`. Legacy callers share a separate fixed
limit of ten. Each slot represents one recipient submission start in a rolling
60-second window, across all of a producer's sender and recipient domains.
These limits supplement the existing UTC daily admission and pending budgets;
they do not increase either allowance. Unused minutes accumulate no credits.
A higher limit can permit a corresponding burst within the same minute; this
is independent of exact recipient-domain pacing.

Before SMTP custody, a transaction locks the producer bucket, reads the current
database clock and reserves capacity. That reservation commits with the
recipient-domain pacing slot and recipient transition to `submitting`. If a
competing worker fills the window, the losing claim returns to `queued` with
`producer_throttled`, a refunded delivery attempt and no failure event. Its
pacing reservation rolls back. Holds or deadlines rejecting the same handoff
also roll back both reservations. Once custody starts, SMTP failures, uncertain
results and reconciliation do not refund capacity. Postfix delivery and retries
remain relay-owned and do not create new maild submission reservations.

With master, operations and producer scheduling gates explicitly enabled in
the private operator environment, inspect without credentials in arguments:

```sh
mailctl scheduler status
```

The existing Unix-only operator API exposes
`GET /internal/v1/mail/operations/producer-scheduler`. It reports at most the
sixteen enrolled producers and legacy bucket (`producer_id: ""`), each with
its configured limit, rolling `in_window` usage, `remaining` capacity,
`rate_limited`, `retry_at` when limited, `due_recipients` and
`oldest_due_seconds`. Retry time reflects current committed reservations and
policy; other workers may reserve newly released capacity first. Queue age
starts at the scheduled due time or original admission. Due counts include
mail held by other controls, so capacity alone does not establish eligibility.
Output contains no addresses, message payloads, credentials or encryption keys.
This read requires the distinct operator credential and never changes state.

The existing private telemetry registries include the aggregate
`maild_mail_producer_scheduler_` gauges: `enabled`, `stats_up`,
`limited_producers`, `reservations`, `due_recipients`,
`rate_limited_recipients` and `oldest_due_seconds`. No producer or address labels
are emitted. When disabled only `enabled=0` is exposed; no statistics are read.
When enabled, a failed read sets `stats_up=0` and suppresses queue aggregates.
The report credential accesses these only through the existing gated private
monitoring endpoint; no new listener or collector activation is introduced.

Runtime rollback sets only this new gate to `0`: claims resume FIFO, new
handoffs bypass producer limits, and durable turns and reservations remain.
Handoffs started while off create no producer reservation. Re-enabling honors
reservations still within the window and resumes fair turns. Persistent manual
holds, expiry, domain pacing, feedback safeguards and relay custody retain
their existing behavior. Historical reservations are pruned per producer during
successful gated handoffs, keeping at most 100 rows per producer bucket.

Apply the additive migration before running the new binary, including when
this gate remains off. Test with isolated fixtures, then use the separately
owned live qualification process before considering any activation. Database
rollback refuses to erase nonempty turn or reservation state; restore an older
binary and keep the additive tables for a reversible runtime rollback.


## Rotate producer credentials and suspend internal callers

The new gate is `FAAS_INTERNAL_MAIL_PRODUCER_LIFECYCLE_ENABLED=0` in the optional
unit and private maild/operator examples. Exact `1` with the master gate enables
lifecycle management; maild also requires enrolled producers and the producer
gate. The private operator environment additionally needs the operations gate
and its distinct credential. Callers keep their existing scoped transport and
secret delivery path. This feature does not select callers or enable SMTP.

A producer may stage one optional `replacement_token` alongside its current
`token` in maild's private `FAAS_INTERNAL_MAIL_PRODUCERS` enrollment. Generate a
fresh independent random token of at least 32 bytes without whitespace; it must
differ from every configured primary, replacement, service, report, operator
and feedback credential. Keep it in the existing private EnvironmentFile and
caller secret delivery mechanism, never in CLI arguments, repository files,
logs or tickets. Restart maild to load configuration; SIGHUP reloads logging.
Staging alone grants the replacement no access.

Inspect using the private operator environment:

```sh
mailctl producers status --producer auth
```

Status includes the durable revision and suspension, the selected
`active_credential_id`, configured `primary_credential_id` and
`replacement_credential_id`, and metadata for those credentials. Each credential
has an opaque 64-character identifier, `configured`, `active`, `usable`, and any
saved `retires_at`/`revoked_at`. Up to three distinct credentials appear in
ordinary status; a mutation result can also include its selected historical
credential. Identifiers are producer-bound one-way references and cannot be
used as bearer tokens. Status contains no secrets, addresses or message bodies.
A selected credential absent from current configuration has `usable=false`.

Persist a fresh request UUID and record the revision and replacement identifier
from status. The following variables denote those nonsecret saved values:

```sh
mailctl producers rotate --producer auth \
  --replacement-id "${REPLACEMENT_CREDENTIAL_ID}" --overlap 1h \
  --expected-revision "${PRODUCER_REVISION}" \
  --request-id "${ROTATION_REQUEST_ID}" --actor operator \
  --reason credential_rotation --evidence ticket:mail-rotation
```

The CLI defaults to one hour of overlap. Whole-second durations in `0..24h` are
accepted; explicit `--overlap 0s` retires the old key immediately. The API requires
an explicit integer `overlap_seconds`. Activation verifies that the requested
identifier is the exact staged replacement. The expected revision identifies
the saved selected credential; initial activation uses the configured primary.
It atomically selects the replacement and saves
the old key's deadline using current database time. Existing callers keep using
the old key during the window while new callers can use the replacement. Each
credential remains limited to its own producer routes and existing permissions.

Update each selected caller's existing `FAAS_INTERNAL_MAIL_PRODUCER_TOKEN`
through its private secret delivery path. Once all callers use the replacement,
promote that same secret to the enrollment's `token` field, remove
`replacement_token` and the old secret, and restart maild. No second activation
is needed for this configuration promotion. The stable producer ID, sender
permissions, queued ownership, idempotency keys, budgets, scheduling turns and
event-consumer batches/checkpoints are preserved. A new credential can retry
an existing message or acknowledge an existing receipt without creating new
ownership or resetting progress.

After activation, keep the selected primary aligned with durable state before
staging another planned replacement. Recovery can use a fresh staged replacement
even when the previous secret is absent: the saved selected identifier and
expected revision retire that credential without its bearer value. An unselected
configuration primary grants no access. Saved deadlines never move later, including after
configuration changes or restarts. A known retired or revoked identifier cannot
be activated again. Repeating the same saved request and arguments returns its
original audit/result; it cannot extend overlap, select a later staged secret
or undo a newer suspension/revocation. Changed arguments conflict. Fetch current
status and use a new UUID/revision for a genuinely new operation.

For immediate credential revocation, select its identifier from status or saved
audit, save a new request UUID, and use the current producer revision:

```sh
mailctl producers revoke --producer auth \
  --credential-id "${CREDENTIAL_ID}" --expected-revision "${PRODUCER_REVISION}" \
  --request-id "${REVOCATION_REQUEST_ID}" --actor operator \
  --reason credential_revocation --evidence ticket:credential-review
```

Revocation is permanent for that identifier, including a staged replacement.
It stops that credential's producer access and owner writes. It does not cancel
or hold previously admitted mail: use producer suspension when dispatch must
also stop. A fresh staged replacement can recover access even when the previous
primary was revoked. Retired credentials removed from private configuration
remain revocable by their saved identifiers. Other producers and namespaces
retain their own access.

Suspend and resume without deleting producer enrollment:

```sh
mailctl producers suspend --producer auth \
  --expected-revision "${PRODUCER_REVISION}" \
  --request-id "${SUSPENSION_REQUEST_ID}" --actor operator \
  --reason producer_review --evidence ticket:producer-review

mailctl producers resume --producer auth --confirmed-reviewed \
  --expected-revision "${PRODUCER_REVISION}" \
  --request-id "${RESUME_REQUEST_ID}" --actor operator \
  --reason review_complete --evidence ticket:producer-review
```

Refresh the revision before each command. Suspension blocks producer API access,
new admission, consumer creation/acknowledgment and new SMTP custody, including
already claimed recipients. Racing claims return to the queue without charging
a delivery attempt or failure event. It preserves queued messages, payloads,
leases already transferred to SMTP, uncertain/submitted outcomes and durable
consumer progress. Expiry, publication, reports and settlement continue under
their existing gates. Already-started handoffs retain custody and may complete;
this control does not hold or delete the Postfix queue. Reviewed resume releases
only the producer suspension and never revives revoked credentials or clears
namespace/domain, timing, reputation, relay or pacing restrictions.

Owner transactions share a producer access fence; lifecycle mutations acquire
its exclusive counterpart, including first insertion. A revocation or suspension
waits for earlier owner transactions to finish, and later writes recheck access
inside their transaction. Credential deadlines are rechecked after later lock
waits: expired admission, idempotency replay, pull and acknowledgment roll back
without consuming budgets or changing receipt/checkpoint state. Stale revisions
return HTTP 409. Invalid/revoked/expired producer credentials return 401; a
suspended producer returns 403. Backend failures fail closed before mutation.

Private operator routes are `GET
/internal/v1/mail/operations/producer-access/{producer}` and `POST
/internal/v1/mail/operations/producer-access/{rotate|revoke|suspend|resume}`.
POST bodies contain `producer_id`, `expected_revision` and the existing audit
fields. Rotation/revocation select `credential_id`; rotation also supplies
`overlap_seconds`; resume requires `confirmed_reviewed: true`. No route accepts
a bearer secret as mutation data. Inspect ordinary `mailctl audit list` for
saved lifecycle results. These routes return 404 before reading credentials or
input when the lifecycle gate is off.

Private telemetry adds `maild_mail_producer_access_` gauges: `enabled`,
`stats_up`, `suspended_producers`, `revoked_credentials`, `retiring_credentials`
and `expired_credentials`. They contain no producer, credential or address
labels. Disabled management emits only `enabled=0` and reads no aggregates;
failed enabled reads emit `stats_up=0` without inventory values. Existing report
and Unix-socket gates still control telemetry access.

For runtime rollback, first promote the selected active credential to the
primary configuration field so callers can keep using it without replacement
support. Set only the new lifecycle gate to `0` and restart with these binaries.
Management routes and replacement authentication stop; saved retirement
boundaries, revocations and suspensions remain enforced. Retain the additive
tables and audit. Apply migrations before the new binary even with management
off; database rollback refuses retained lifecycle state. Older binaries do not
enforce these fences and are unsuitable while protected producers are enabled.
No live delivery qualification or activation is part of this implementation.

## Rotate stored-mail encryption keys

This is an internal operator capability. It is separate from DKIM selector and
producer credential rotation, and does not publish DNS records, change a caller,
open a listener or send email. The master, operations and
`FAAS_INTERNAL_MAIL_ENCRYPTION_ROTATION_ENABLED` flags must all be exact `1` in
maild and the private operator environment to use its commands. The new flag is
`0` in the optional unit and every example. Nothing rotates automatically.

With no keyring configured, maild loads the existing dedicated identity and
continues the legacy envelope/HMAC format until the first audited activation.
`mailctl encryption status` shows its nonsecret key ID, revision `0`, and stored
dependencies. Each ID is the SHA-256 digest of a fixed mail-specific prefix and
the public age recipient. Key IDs are safe to select in requests; private age
strings, paths, ciphertext, bodies and fingerprints never appear in these
operator responses or audits. The operator token cannot submit messages.

Stage a fresh raw X25519 age identity through the existing private-key
provisioning workflow. Files must contain only the raw `AGE-SECRET-KEY-...`
identity string and use mode `0400`, or the supported systemd credential mode
`0440`. Use identities dedicated to this mail namespace. Do not reuse host or
fleet application-secret identities, include a private key in an environment
variable, or pass one as a mailctl argument.

Set `FAAS_INTERNAL_MAIL_KEYRING_PATH` in the private maild environment or a reviewed
unit drop-in. It names a bounded JSON file, also `0400` or a systemd credential
with mode `0440`. Its optional `legacy_key_id` pins the original identity; when
omitted, the existing `FAAS_INTERNAL_MAIL_AGE_IDENTITY_PATH` is loaded and included
in the ring automatically. `identity_paths` contains additional absolute, clean
paths, with no duplicates. A bootstrap file can contain:

```json
{
  "identity_paths": [
    "/run/credentials/faas-maild.service/faas_internal_mail_age_next"
  ]
}
```

For example, a reviewed drop-in can add these credentials while retaining the
unit's existing legacy credential:

```ini
[Service]
LoadCredential=faas_internal_mail_keyring:/etc/faas/secrets/internal-mail/keyring.json
LoadCredential=faas_internal_mail_age_next:/etc/faas/secrets/internal-mail/mail-next.age
Environment=FAAS_INTERNAL_MAIL_KEYRING_PATH=%d/faas_internal_mail_keyring
```

Keep the credential directory in the JSON consistent with the actual service
unit name. The file is at most 32 KiB and the loaded ring is at most eight keys,
including an automatically included legacy identity. Unknown JSON fields,
trailing JSON, loose permissions, duplicate identities, relative key paths and
invalid legacy IDs fail startup. No key file is generated or overwritten by
maild. Restart the staged maild before activation so every intended reader has
the next identity. Keep delivery at its separately approved setting.

Inspect `mailctl encryption status`, save the next key ID and current revision,
and prepare a new saved audit request ID. Activate the staged identity:

```bash
mailctl encryption activate --key-id "$next_key_id" --expected-revision 0 \
  --request-id "$activate_request_id" --actor oncall --reason encryption_roll \
  --evidence ticket:mail-key-roll
```

Activation pins both legacy and active IDs, increments the revision, and changes
this process's write selection. New payloads, newly generated DKIM private keys
and replay HMACs use the selected identity with explicit version/key headers.
The encrypted envelope also authenticates that header, namespace and resource.
A new HMAC uses a separate versioned domain. Replay verification always selects
the saved fingerprint's identity, including after payload cleanup; changing the
active writer cannot change what an existing idempotency key means.

Retained content is migrated only through explicit bounded passes:

```bash
mailctl encryption preview --batch-size 10
mailctl encryption rewrap --key-id "$next_key_id" --expected-revision 1 \
  --batch-size 10 --request-id "$rewrap_request_id" --actor oncall \
  --reason encryption_roll --evidence ticket:mail-key-roll
```

Preview reads no private key material and does not decrypt or write data. It
reports only aggregate candidate counts for one batch. Apply verifies old
envelopes and existing HMACs, then re-encrypts payloads and all retained DKIM key
copies, including active, staged and retiring signers. It backfills a missing
legacy fingerprint or migrates a retained payload's fingerprint to the selected
verifier. Pruned payloads are never reconstructed. The size defaults to ten and
is restricted to `1..25`; each request has the ordinary 15-second API deadline.
The namespace key fence can briefly delay encrypted writers while a pass runs.

Use a fresh saved request ID for each next successful pass. A full batch can
leave more work; continue deliberate passes and inspect until a pass reports
zero rewrapped blobs. Re-encryption does not change the selection revision.
Read current status if another operator has activated or retired a key. After a
lost response, replay the same request ID and identical arguments to recover its
saved result. Replaying an older activation never restores an older selection.
A changed request under the same ID conflicts. Each applied batch and audit
record share one transaction; corrupt content, a missing verifier, cancellation,
flag disablement or another failure leaves no partial migration or saved result.

Migration preserves recipient states, attempts, leases, event-consumer progress,
message creation/scheduling/expiry, producer ownership, DKIM selectors/public
keys and DNS revisions. Mail in `submitting`, `submitted` or `uncertain` custody
is never retried or settled by a key operation. Existing feedback correlation
continues to work. A worker missing the active key stops before claiming work;
if activation races rendering, a missing-key claim returns to `queued` without
spending a delivery attempt. Repair its configured identities and restart it.

Inspect dependencies again before logical retirement:

```bash
mailctl encryption status
mailctl encryption retire --key-id "$previous_key_id" --expected-revision 1 \
  --request-id "$retire_request_id" --actor oncall --reason encryption_retire \
  --evidence ticket:mail-key-roll
```

Retirement refuses the active identity and any identity still required by a
retained payload, any stored DKIM key copy, or any replay fingerprint. In
particular, a pruned message's HMAC cannot be migrated without its original
content. `pruned_fingerprints` makes that permanent dependency visible. Keep
that verifier identity; there is no metadata-deletion or fingerprint-reset
escape hatch in this pilot. Retirement increments the revision and permanently
blocks later activation of that ID, even if its private file is restored.

The fence is shared by admission, domain/key creation, automatic and operator
retention, and DKIM selection/retirement. Activation, migration and retirement
hold it exclusively. An owner already holding the fence finishes first; a stale
writer arriving after selection changes is rejected rather than committing an
old envelope or verifier. These checks remain enforced with management off.
All reference checks and key selections are scoped to the configured namespace.

Retirement does not erase key files, process memory, backups or WAL. Older
snapshots still need their old identities; retain keys for the entire backup
and restore policy and review other namespaces before any physical disposal.
Only remove an identity from the live ring once its live dependencies are zero
and retirement has succeeded. Preserve `legacy_key_id` from status in the JSON
when omitting the original private identity. Keep all other required identity
paths. The stock unit still has a mandatory legacy `LoadCredential`; removing
that source file also requires a reviewed complete credential override and
consistent provisioning. No command performs that removal automatically.

Runtime rollback disables the new management flag while keeping these binaries,
keyring and additive tables. An explicitly configured ring is still loaded for
decryption, replay and the persisted write selection. Startup refuses missing
required readers, a changed pinned legacy ID, or retired keys with dependencies.
Do not restore an older binary that lacks versioned formats or writer fences.
Schema rollback refuses selected/retired key state, versioned data or encryption
audit; it cannot recover deleted payloads or private keys.

The existing private metrics surfaces expose `maild_mail_encryption_enabled`,
`stats_up`, `missing_required_keys`, `old_payloads`, `old_signing_keys`,
`old_fingerprints` and `pruned_old_fingerprints` with that prefix. They contain no
key, namespace, producer, address or domain labels. Management off emits only
`enabled=0` without inventory reads. Read failure emits `stats_up=0` and omits
inventory rather than reporting misleading zeros. Live delivery qualification
remains a separate operational task.

## Review and manage recipient suppressions

The private controls require the master, operations and
`FAAS_INTERNAL_MAIL_SUPPRESSION_CONTROLS_ENABLED` flags to be exact `1` in maild
and the private operator environment. The new flag defaults to `0` in the
optional unit and both examples. Existing `mailctl suppressions list` remains
read-only under the operations gate and shows only currently active blocks.
Normal bounce/complaint processing and previously saved blocks continue with
suppression management off. No customer route, listener, caller enrollment,
DNS change or SMTP action is introduced by enabling management.

Apply the additive
[`20261016174046483_internal_mail_suppression_controls.sql`](../../migrations/20261016174046483_internal_mail_suppression_controls.sql)
through the existing migration owner before starting the new binary. It retains
current blocks and backfills legacy history. A known historical complaint stays
protected even if an older writer replaced its projection reason with a bounce.
Readiness requires the new state, history and recipient revision schema.

Commands select one exact bare mailbox. Matching follows the existing
namespace-wide case-insensitive policy across producers. Display names, domain
patterns, wildcards and whitespace are rejected. Plus-addresses remain separate
mailboxes; no provider-specific alias or dot rules are inferred.

```bash
mailctl suppressions show --address "$MAILBOX"
mailctl suppressions history --address "$MAILBOX" --limit 25
```

An unseen address has revision `0` and `blocked=false`. Status reports the
current revision, last blocking revision, strongest block reason and latest
transition evidence. History is ordered by per-address revision. Pages default
to 25 entries, permit `1..100`, and return `next` for `--after`. Cursors apply only
to the same selected address; they are positive decimal revisions. Unknown or
duplicate query parameters, malformed cursors and unbounded pages are rejected.
An address released earlier retains its state and history.

Add a manual block using the observed revision and a new saved audit request ID:

```bash
mailctl suppressions add --address "$MAILBOX" --expected-revision "$REVISION" \
  --request-id "$REQUEST_ID" --actor oncall --reason mailbox_block \
  --evidence ticket:mailbox-review
```

A successful add advances the revision and records a `manual_add` transition.
It blocks new SMTP custody across the namespace, including unsent mail already
queued for that address. Adding again with a new request/revision renews that
blocking revision. This is not cancellation of a relay submission: existing
`submitting`, `submitted` and `uncertain` recipients retain their custody and
continue to accept settlement and late reports.

Only active manual or hard-bounce blocks are eligible for reviewed release:

```bash
mailctl suppressions release --address "$MAILBOX" \
  --expected-revision "$REVISION" --confirmed-reviewed \
  --request-id "$REQUEST_ID" --actor oncall --reason mailbox_reviewed \
  --evidence ticket:mailbox-review
```

Review the address and source evidence before asserting `--confirmed-reviewed`.
The actor is an assertion under the existing shared operator credential; the
request does not authenticate a separate human account. Evidence is a reference,
not a message body, raw report or credential. Missing review, stale revision or
a changed request under a reused request ID is rejected. Release increments the
revision, removes only the active block projection and retains its history.
Complaint suppressions cannot be released, including legacy or service-report
complaints. A later bounce on another message cannot downgrade them. Manual add
also refuses a complaint block, preserving its reason and evidence history.

Release permits future admissions. It never requeues a terminal recipient,
resets idempotency or authorizes a previously blocked queued recipient. Each
recipient retains the last blocking revision seen at admission; admissions
while blocked retain zero. An address's later block makes older queued
recipients permanently ineligible for new SMTP custody. Releasing that block
does not erase the blocking revision. Those recipients settle as `suppressed`
when the worker processes them, even if the block and release occurred entirely
between worker polls. Other delivery holds can delay that processing; the
operator command does not directly rewrite recipient statuses.

A new message admitted after release inherits the current blocking revision and
can proceed through the remaining sender, allowlist, timing, pacing, budget,
reputation and relay gates. Daily/pending accounting retains its existing rules;
release grants no quota refund. Replaying a previously submitted message's
idempotency key returns that original message and its original recipient fences.

Every distinct hard-bounce or complaint report updates the block revision, even
if the address is already blocked. Review races with new feedback therefore
conflict on revision. If release commits first, a subsequent fresh adverse
report reblocks the address. The same committed report replay creates neither a
new revision nor a new block after reviewed release. Temporary deferrals and
temporary queue-expiry DSNs retain their existing non-suppressing behavior.

History evidence records `legacy`, `operator`, `service_report`, `postfix` or
`provider` as its source kind. Provider source IDs come only from the enrolled,
DKIM-authenticated ingestion path; Postfix provenance comes only from the
private report adapter. An event ID beginning with `feedback:` does not grant
provider provenance to a JSON service report. Report evidence contains a
recipient ID and namespace/source-bound event digest, with no raw report
identity, diagnostics, MIME, message payload, signing key or bearer token.
The strongest reason can remain `complained` while the latest transition is a
later `hard_bounce`; history retains the original complaint observation.

Operator changes use the same saved request/audit contract as other mailctl
writes. Repeat identical arguments and the same saved request UUID after a lost
response to recover the original result. A replay cannot clear a newer block,
advance history again or change current state. Active projection, revision,
history and operator audit commit together; cancellation or flag disablement
before commit rolls all of them back. Keep new request IDs for new decisions.

Per-address locks order feedback, admission and new SMTP custody. Admission
locks distinct mailboxes in sorted order. A block waits for an existing handoff
transaction; once it commits, a later handoff cannot pass that block. Feedback
takes the address lock before its reputation/recipient locks, so it cannot
race a reviewed release or deadlock by reversing the SMTP lock order. Saved
fences remain enforced by the worker and database handoff even with management
disabled. Disabling the gate hides new inspection/history/mutation routes before
reading credentials or bodies; it does not bypass a saved block.

Runtime rollback keeps these binaries and additive tables while disabling the
management flag. Do not use an older writer that cannot preserve complaint
priority, feedback revisions or recipient admission fences. Schema rollback
refuses new history, reviewed-release audit or saved recipient revisions; an
unused legacy-only backfill can still be rolled back without deleting active
blocks. Restore state/history, active projections and recipient metadata from a
consistent database backup rather than deleting a tombstone or editing epochs.

The private metrics surfaces add `maild_mail_suppression_controls_` gauges:
`enabled`, `stats_up`, `manual_blocks`, `bounce_blocks`, `complaint_blocks` and
`released_addresses`. There are no mailbox, namespace, source, producer or domain
labels. Management off emits only `enabled=0` without reading inventory. Failed
reads emit `stats_up=0` and omit inventory, instead of suggesting zero blocks.
Live delivery qualification remains a separate operational task.


## Database restore recovery

`FAAS_INTERNAL_MAIL_RECOVERY_ENABLED=0` is the default in the optional unit and
both private environment examples. Exact `1` with the master flag enables this
workflow. New operator routes also require the operations gate and its distinct
credential; no service, producer, report or feedback credential grants recovery
access. `mailctl` has no database or encryption-key access.

A restored backup may show a queued message even though Postfix already accepted
it after that backup. Expired submitting leases alone cannot detect this case.
**PostgreSQL cannot reliably detect its own rewind.** Before each restore,
operators must supply a fresh canonical UUID as `FAAS_INTERNAL_MAIL_RECOVERY_ID`
in the private maild EnvironmentFile, outside the restored database. Never derive
it from a database row, reuse the ID from the backup, or assume that merely
enabling the management flag detects a restore. With recovery enabled, a missing
or malformed ID denies startup. With recovery disabled, that input is not read;
previously persisted holds and recipient quarantine remain enforced.

Use a new ID for every restore. Retain the same ID across ordinary process
restarts and while using recovery controls afterward. Repeating the current ID
never reholds a released run. An older known ID cannot supersede the current
one. Restoring an earlier snapshot also restores its history, so external
freshness remains an operator responsibility.

### Restore with delivery held

1. Stop **all** maild processes and opted-in producers before restoring. Keep the
   maild delivery flag `0`. Preserve Postfix spool, queue IDs, stable recipient
   UUID/Message-ID evidence, relay logs and feedback outside the database being
   restored. Hold the Postfix queue separately when required: a maild hold does
   not retract work already owned by Postfix. Do not run an older maild binary
   that lacks recovery-generation checks against this state.
2. Restore a consistent whole mail database snapshot using the platform
   [PostgreSQL backup procedure](../runbooks/PostgresBackup.md). Include encrypted
   payloads, fingerprints, domains/DKIM state, producer credential history,
   suppressions and their revisions, all delivery holds, idempotency metadata,
   feed events/receipts/cursors, audits, and recovery tables/recipient generations.
   Do not independently mix snapshots of those tables. Retain the backup's
   required encryption identities, including readers for pruned fingerprints;
   never discard an older identity because the live writer changed afterward.
3. Supply a **fresh external restore UUID** in
   `/etc/faas/secrets/internal-mail/maild.env`, set its recovery flag to `1`, and
   keep delivery `0`. Enable the master and private operations gates only under
   the existing internal procedure. Start maild. Startup commits an audited hold
   before encryption readiness, listener or worker activity. Missing encryption
   readers still deny startup; the configured hold is already durable. This
   step establishes recovery even if delivery was accidentally configured `1`.
4. Load the existing private operator environment with master, operations and
   recovery gates enabled. Inspect:

   ```sh
   mailctl recovery status
   mailctl recovery preview --limit 25
   # Use the returned next UUID for each following page:
   mailctl recovery preview --limit 25 --after RECIPIENT_UUID
   ```

   Status reports the current recovery ID/revision, held state, count of
   quarantined pending recipients and active SMTP leases. Preview returns only
   recipient/message IDs, status and lease deadline. Its default page size is
   25, maximum 100; cursors are ascending recipient UUIDs. Reports can change
   status during inspection, so a page is an observation rather than a frozen
   snapshot. Newly admitted mail is blocked while held, including idempotent
   POST repeats. Producer metadata authentication and feed pull/ACK stop too.

### Reconcile individual recipients

Every old pending recipient starts quarantined by its saved recovery generation.
Global release never updates those generations. Unknown outcomes stay blocked
indefinitely; absent relay logs, an empty current spool, or a timeout does **not**
prove non-acceptance. Use positive independently retained evidence for the exact
recipient UUID/Message-ID and inspect the message/events through existing private
commands. Keep raw transcripts, bodies, headers, addresses and secrets out of
`--evidence`; give only an incident or artifact reference.

For confirmed durable relay acceptance, record custody without submitting again:

```sh
mailctl recovery review --id RECIPIENT_UUID --recovery-id RESTORE_UUID \
  --expected-revision 1 --expected-status queued --outcome submitted \
  --confirmed-relay-accepted \
  --request-id SAVED_REVIEW_UUID --actor oncall --reason restore_relay_evidence \
  --evidence incident:mail-restore-1
```

Only after positive evidence confirms that the relay did not accept the message:

```sh
mailctl recovery review --id RECIPIENT_UUID --recovery-id RESTORE_UUID \
  --expected-revision 1 --expected-status queued --outcome retry \
  --confirmed-not-accepted \
  --request-id SAVED_REVIEW_UUID --actor oncall --reason restore_no_acceptance \
  --evidence incident:mail-restore-1
```

Use the actual current ID, revision and status from inspection. A changed recovery
revision or recipient status refuses a stale decision. Reviews support queued,
deferred, expired-lease claimed/submitting, uncertain and submitted recipients;
submitted cannot become retry. Active claim/submission leases must first settle
or expire. Terminal mail cannot be reopened. Retry preserves attempt counts,
original schedule/deadline, idempotency, suppressions and every other hold. An
expired or exhausted message will still expire instead of sending. The retry
operation schedules no earlier than its existing retry time. Submitted records
relay custody only; it does not prove destination acceptance or inbox placement.
Normal authenticated reports can then supply later evidence.

Each review atomically changes just that recipient's recovery generation and
custody, appends a safe event, and saves its audit record. Save a distinct request
UUID **before** every operation. After a lost response, repeat identical arguments
with the same UUID: the original result is returned even after a later report or
restore. Reusing it with different arguments conflicts. Replaying old operations
cannot reopen a newer hold.

A recovery hold leaves already committed SMTP custody intact. Original workers
can settle, and report/feedback collectors can continue adding real observations.
A hold waits for earlier admission/handoff transactions; it does not cancel a
handoff that committed first. Restores themselves therefore require all original
processes stopped. Expiry does not turn quarantined pending recipients into
terminal mail, and payload pruning pauses while the namespace is held. After
release, unreviewed pending recipients keep their body and attempt budget.

### Review restored policy and release

Compare restored state with independently retained post-backup evidence before
release. In particular:

- Restore suppression additions/complaints, reviewed releases and recipient
  blocking revisions consistently. Reinstate any block lost by the backup with
  the existing audited suppression controls. Releasing recovery never releases
  a complaint or any other suppression.
- Review producer tokens/enrollment, rotations, overlap deadlines, revocations
  and suspensions. A backup can predate a revocation; replace compromised or
  uncertain credentials using existing lifecycle controls and provisioning.
  A database snapshot alone cannot prove a credential was never revoked later.
- Review namespace/domain delivery controls, reputation holds, selected DKIM
  keys and DNS authorization, pacing/dispatch budgets, and relay health.
- Reconcile consumer checkpoints and pending receipts against downstream state.
  Event feeds remain at least once; keep client deduplication and use existing
  audited consumer replay only for reviewed recovery. This workflow does not
  reconstruct events or idempotency records for messages absent from the backup.
  Do not blindly replay producer requests whose original admission was lost.
- Review the relay's persistent spool/evidence and all recipients left quarantined.
  Keep unknown outcomes blocked; preserving the hold is also a valid choice.

Release requires all five explicit assertions, the current recovery revision and
an audit reference. The server also checks that every required payload, DKIM and
fingerprint encryption reader is configured and not retired under the encryption
fence; this checks availability, not the authenticity of the backup as a whole.

```sh
mailctl recovery release --recovery-id RESTORE_UUID --expected-revision 1 \
  --confirmed-policy --confirmed-access --confirmed-consumers \
  --confirmed-relay --confirmed-quarantine \
  --request-id SAVED_RELEASE_UUID --actor oncall --reason restore_review_complete \
  --evidence incident:mail-restore-1
```

The hold changes to released and the revision increments. New messages may be
admitted; new SMTP custody still requires the independent delivery flag and all
existing controls. **Unreviewed old recipients remain quarantined**, including
when recovery management is disabled afterward. They can be reviewed later with
the new revision. No counters, producer revocations, controls, suppression,
consumer cursor, schedule or relay health gate are reset by release. The five
assertions are operator-reviewed evidence, not automatic reconstruction of data
lost after a backup.

New private `maild_mail_recovery_` gauges are `enabled`, `stats_up`, `held`,
`quarantined`, and `active_submissions`. They have no labels, IDs or addresses.
With the management flag off only `enabled=0` is emitted and no recovery
inventory is read. A failed inventory emits `stats_up=0` without stale counts.
Do not disable management merely to silence a hold. Migration rollback refuses
any used recovery state, saved recipient generation or recovery audit.

### Repeatable offline drill

Against the existing disposable PostgreSQL fixture, run:

```sh
go test -race ./pkg/internalmail -run '^TestPGRecovery' -count=1
```

The drill snapshots recipient custody, lets a fake relay accept a message, rewinds
PostgreSQL recipient state/events, restarts its store, supplies a fresh restore
ID, reviews positive evidence, releases and verifies that only confirmed retry
and new mail submit. It also checks hold/quarantine after flag rollback, active
SMTP custody, late reports, admission concurrency, required keys, private access,
namespace isolation and protected migration rollback. It opens no external SMTP
connection and changes no DNS or deployed flags. This is a deterministic mail
state-rewind drill; it does not replace restoring and verifying the platform's
actual basebackup/WAL, independent secret backups or Postfix spool. Live delivery
qualification remains a separate task.


## Durable platform notification handoff

`FAAS_INTERNAL_MAIL_PRODUCER_OUTBOX_ENABLED=0` is the default in both the apid
unit and optional maild unit, and in the private producer/operator examples.
The base producer outbox covers **self-service account-deletion pending
notices**. Email verification has its own additional opt-in below. Other existing
notification flows keep their current behavior.
It adds no customer email API or entitlement, public inspection route, new
listener, or automatic activation.

There are two transactional boundaries: apid commits the business change and
notification intent together; maild separately commits its message and recipient
outbox. apid owns all producer-intent writes. maild reads only bounded, namespace-
scoped metadata through the existing private operations socket. Admission to
maild means durable queue acceptance, not SMTP delivery or inbox placement.

### Internal opt-in and rollback

To opt an independently qualified internal caller into this path, apid needs the
new exact `1` flag, the existing master, producers and scheduling gates, and
`FAAS_MAIL_TRANSPORT=internal-producer`. Configure the existing private Unix
socket, scoped producer ID/token and approved From address through apid's normal
secret delivery. The optional namespace defaults to `platform`; it must match
maild. Existing service/report/operator tokens cannot replace that producer
credential. An enabled outbox with missing prerequisites or schema denies apid
startup. It never switches a saved intent to another provider.

maild needs its master and new outbox flag for generation-bound admission.
maild and `mailctl` also use those gates for inspection; operations additionally requires the existing operations flag and
separate operator credential. These read-only controls can remain available
while apid's worker flag is off. They require the new additive migration but do
not enable producer admission or SMTP. No example or generated unit enables any
of these gates.

With the new flag off, apid reads no outbox credentials/configuration, starts no
outbox work, and uses its existing account-deletion mail path. Disabling an
already configured worker stops further handoffs and preserves pending intent.
Do not rely on changing the master/producer/scheduling flags alone to select a
fallback: a requested outbox whose prerequisites disappear refuses that atomic
operation. A feature disable at the sender's preflight check retains queued
intent and refunds that claim attempt. Stop or restart apid as appropriate for
its process environment; SIGHUP does not reload feature flags.

### Atomic intent and bounded delivery

When enabled, the deletion timestamp and notification intent share one PostgreSQL
transaction. An insert, quota, cancellation or late gate failure rolls back both,
so the public deletion call cannot succeed after a failed intent save. Repeated
requests retain one intent for the same account/template/deletion timestamp and
never restart its grace window. Turning the feature on does not backfill old
pending deletions. Existing lifecycle audit and notifications continue, and the
inline email is skipped when the durable intent committed.

Each deletion intent pins the exact producer, namespace, normalized From, recipient,
delete timestamp, 30-day restore deadline, template version `1`, and absolute
24-hour notification expiry. The versioned text renderer is frozen for retries;
future wording changes require a new template version. Deletion intent stores no
rendered body, authentication link/token or transport credential. The recipient
snapshot is private database metadata, like existing recipient rows; terminal
handoff states clear it. Account erasure cascades all of this account's producer
outbox rows, including recipient snapshots and metadata.

Pending intent is capped at 1,000 per namespace across producers. A namespace
admission fence serializes the bound with the business transaction. The worker
handles one intent per pass, polls every five seconds, and has a 20-second pass
budget and one-minute fenced claim lease. Transient or ambiguous maild failures
retry with exponential delays from 30 seconds to 30 minutes, up to 32 claims or
the original 24-hour expiry. Maintenance handles at most 100 exhausted/expired
rows per pass and does not steal a live lease. Permanent rejection is retained
as failed metadata. Limits live in `pkg/api/internal_mail_limits.go`; these are
internal safeguards, not customer capacity commitments.

The idempotency key is `account-delete:<intent UUID>` and is unchanged across
process restarts, token rotation and lost acknowledgements. A retry uses the same
recipient, text, sender and absolute expiry; it does not regenerate a payload
from current account fields. maild therefore returns its original admission after
a response loss. apid marks `admitted` only after a successful response; an
acknowledgement or local commit loss leaves the same key available for retry.
There is no claim of exactly-once SMTP delivery.

Before handoff, the worker checks that the account still has the same deletion
request and email address, and checks the existing platform mail suppression
store. Restored, rearmed or changed-address accounts cancel an old notice;
suppressed recipients settle as suppressed. The account lock spans the bounded
HTTP handoff, so a restore cannot commit between that review and admission. A
restore after admission cannot retract a message already owned by maild or the
relay. maild's existing allowlists, quotas, suppressions, scheduling/deadline,
producer suspension, DNS, pacing and custody controls still apply.

Changing the configured producer, namespace or From never moves existing work
or silently changes its payload: incompatible intent stays pending under its
original configuration and eventually expires. Stage/rotate credentials using
the existing producer lifecycle while keeping the same producer identity and
sender. Preserve this configuration with the database backup.

### Recovery and private inspection

Producer intent also saves maild's current recovery generation. apid refuses new
atomic deletion admission during a saved recovery hold. Scheduling takes its
admission/account locks before the recovery fence; dispatch retains no recovery
advisory lock across HTTP. A private transport header carries the saved generation,
and maild validates it inside its own admission transaction. A recovery that
starts and releases between producer preflight and HTTP admission still rejects
the stale request. A remote outbox gate rollback is retryable without discarding
saved intent. A restored producer intent from an older
generation cannot be handed to maild even after global recovery release. Preserve
and review those rows with the database/relay evidence; never reset generations,
leases, attempt counts or idempotency keys to make them send. This workflow
has no bulk replay, force-retry or generation-reset operation. Old held intents
can expire without sending. Stop producers during a database restore as described
in the database recovery procedure above; this does not reconstruct messages or
post-backup decisions absent from the restored database.

Read-only operations use the existing operator environment and Unix socket:

```sh
mailctl outbox status
mailctl outbox list --status queued --limit 25
mailctl outbox list --status failed --limit 25
mailctl outbox list --limit 25 --after INTENT_UUID
mailctl outbox show --id INTENT_UUID
```

States are `queued`, `claimed`, `admitted`, `failed`, `expired`, `cancelled`, and
`suppressed`. The default page size is 25, maximum 100, ordered by intent UUID.
Inspection returns intent/account IDs, producer, template/version, state, attempt
count, revision, safe reason code, deadlines, claim deadline and `recovery_held`. It returns
no recipient, sender header, body, credential or raw transport/database error.
A queued old generation with `recovery_held=true` remains blocked; pending work
with a changed producer/From also remains unsent. `failed` is visible retained
state, not a reason to blindly create a replacement notification. Check maild
and relay evidence before any further recovery decision.

Private label-free `maild_mail_producer_outbox_` metrics expose `enabled`,
`stats_up`, each of the seven state counts, and `recovery_held`. With the new flag
off, only `enabled=0` is emitted and no inventory is read. A failed inventory
emits `stats_up=0` and omits counts. The migration refuses rollback while any
intent/history exists. Disabling a feature is not a reason to drop its table.

### Reconcile a lost producer acknowledgement

`FAAS_INTERNAL_MAIL_OUTBOX_RECONCILIATION_ENABLED=0` is a separate exact opt-in,
default-off in the apid/maild units and private examples. maild and `mailctl`
require it together with the master, operations and producer-outbox gates for:

```sh
mailctl outbox admission --id INTENT_UUID
```

This is a bounded, read-only lookup on the existing private operator socket,
using the distinct operations token. It derives the original idempotency key
from the saved intent UUID and matches the namespace and saved producer. A
`confirmed` receipt also requires the saved sender and exact absolute deadline,
no changed schedule, one recipient, and admission after intent creation. Output
contains the intent revision, current/saved recovery IDs, safe blockers and,
when confirmed, the maild message UUID and admission time. It contains no body,
recipient, From header, replay key, fingerprint or credential. Admission proves
maild committed its queue entry; it does not prove SMTP or destination delivery.

`missing` or `mismatch` never authorizes a new send. In particular, a missing
record after a database restore cannot establish that the relay did not accept
the original mail. An active claim, global recovery hold, changed recovery
generation, cancelled/suppressed intent or already acknowledged intent blocks
acknowledgement. Recovery quarantine remains visible even for terminal intents.

apid owns the write, through its **local operator mode**, using an explicit
`DATABASE_URL` (or `FAAS_DATABASE_URL`) for the same apid owner database and
matching `FAAS_INTERNAL_MAIL_NAMESPACE` (default `platform`). Run it under the
private apid database authority with the master, operations, producer-outbox and
reconciliation flags enabled in that command's reviewed environment. It opens no
listener, loads no maild keys/producer credentials and applies no migrations.
Its single-connection operation has the existing 15-second private API budget.
mailctl remains a Unix-only client without database access.

Save a fresh request UUID and the exact inspection values before applying:

```sh
apid mail-outbox reconcile --id INTENT_UUID --message-id MAILD_MESSAGE_UUID \
  --expected-revision 3 --recovery-id CURRENT_RECOVERY_UUID \
  --request-id SAVED_OPERATION_UUID --actor operator --reason lost_acknowledgement \
  --evidence ticket:mail-handoff

apid mail-outbox audit --request-id SAVED_OPERATION_UUID
```

Before the first restore, the recovery UUID is
`00000000-0000-0000-0000-000000000000`; afterward use the current inspected ID.
Actor is an operator assertion under local database authority; evidence is a
reference, never message contents, a credential or a relay transcript.

The write locks account then intent before taking the shared recovery fence,
and re-reads the authoritative admission inside that same transaction. A
caller-supplied message UUID is only an assertion to check. A changed revision,
generation or receipt conflicts. It records `admitted` and an audit atomically,
clearing any remaining recipient snapshot and expired claim. An expired claim
can settle only after its row lock is available; a stale worker then loses its
lease. Queued, failed and expired intents with confirmed admissions can also be
acknowledged. Original attempts, retry time, expiry and recovery generation stay
unchanged, and no maild message, recipient status or SMTP custody is changed.
In particular, recording an admission after expiry cannot extend its deadline.

Each worker mutation increments the saved intent revision even while this
management feature is disabled. Repeating the same operation UUID and identical
arguments returns its original audit after a lost response; changed arguments
conflict. A different UUID cannot acknowledge an already settled intent. Audit
records retain only opaque intent/message IDs, revisions, previous state,
recovery ID and operator assertions. Account erasure removes the intent and its
recipient metadata; the opaque audit remains available without account data.
Rollback refuses to discard any reconciliation audit, including after account
erasure. Disabling the management gate stops lookup/acknowledgement/audit access
and preserves history. It never enables delivery or releases a saved hold.

### Durable email-verification handoff

`FAAS_INTERNAL_MAIL_VERIFICATION_OUTBOX_ENABLED=0` is a separate exact opt-in,
default-off in the apid/maild units and private examples. apid requires the base
producer outbox, master, producer, scheduling and scoped `internal-producer`
prerequisites. maild independently requires the new flag for the private
`X-Gregale-Mail-Outbox-Template: email_verification` header and validates the
saved recovery-generation header inside its admission transaction. Both gates
are rechecked after waits and before commit. Roll out the migration and compatible
binaries before enabling a gate. This adds no listener, customer email API,
SDK surface or automatic activation. Live delivery qualification stays separate.

With the new flag off, verification uses its existing inline delivery path and
configuration reads no verification origin, identity, token or ciphertext. With
it enabled, existing signup/resend call sites atomically commit an address-bound
token hash and encrypted notification intent. Signup has already committed; a
failed notification save preserves the existing public response and logs a safe
failure, leaving neither an orphan token nor an inline fallback. A repeated user
request deliberately mints a fresh link, as before. Worker retries reuse the
original token, frozen version-1 renderer, absolute 24-hour expiry and
`verify-email:<intent UUID>` key.

Configure `FAAS_INTERNAL_MAIL_VERIFICATION_ORIGIN` through apid's
`/etc/faas/sealed.env` as the trusted HTTPS origin of the verification API,
for example `https://api.example.test`. Userinfo, paths beyond `/`, queries and
fragments are rejected. Request Host and forwarding headers never select a
durable credential destination. A changed configured origin holds old intent
under its original configuration until expiry.

The raw 32-byte token is sealed with age under a **dedicated apid identity**.
Only its hash and address snapshot enter the authentication table. The producer
outbox stores encrypted credential bytes and immutable routing/rendering inputs,
without a rendered body or complete plaintext credential URL. The authenticated
seal binds namespace, producer, intent, account, sender, recipient, recovery
generation, key ID, origin, token hash, template/version and expiry. Copying a
ciphertext or changing these inputs fails closed. Terminal settlement, bounded
expiry maintenance, cancellation, suppression and reconciliation erase the
recipient snapshot and encrypted credential fields. Account erasure cascades
token and intent rows.

apid's optional LoadCredentials are:

| Input | Private source | apid selector |
| --- | --- | --- |
| Current writer/reader | `/etc/faas/secrets/internal-mail/producer-verification.age` | `FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH=%d/faas_mail_verification_identity` |
| Optional previous reader | `/etc/faas/secrets/internal-mail/producer-verification.age.previous` | `FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH=%d/faas_mail_verification_identity_previous` |

These identities are independent of maild's encryption keys and are delivered
only to apid. maild and mailctl receive neither private verification key.
Workers sharing a producer use the same writer/readers. Files must be regular,
private to their owner, bounded in size and contain one X25519 identity. Enabled
startup refuses a missing current key, invalid origin, unavailable schema or
missing reader for this producer's unexpired pending intent. Errors never expose
key contents.

There is no key-management command in this phase. For a coordinated change,
retain the old identity as previous reader while the new identity becomes writer.
Workers claim only payloads for keys they can read; an older running worker
leaves a newer writer's work for a compatible worker. Retain the preceding
reader for at least 24 hours plus the one-minute claim lease after the last old
writer stops, and preserve required readers with backups. Never replace an
unreadable intent with a new message to recover an unknown handoff.

Before handoff, apid locks account, intent, then live token. It checks the
original address and expiry, active/unverified account, unconsumed token,
matching address binding and existing platform suppression. Those locks span
the bounded HTTP admission. Token consumption now takes account before token
too, avoiding inversion with dispatch or account erasure. Consumption before
handoff cancels the pending credential; consumption after handoff waits for that
boundary. A new bound token cannot verify a replacement address even after maild
accepts its email. The binding survives disabling this flag. Existing legacy
hash-only tokens retain their previous semantics and single-use/expiry behavior.
Existing allowlists, budgets, holds, recovery quarantine and SMTP custody rules
remain authoritative. Accepted mail cannot be retracted by producer cancellation.

Existing private outbox inspection exposes template/state metadata without raw
credentials, hashes, links or ciphertext. Admission lookup derives the saved
verification key prefix. Local `apid mail-outbox reconcile` and audit replay for
verification intent also require the new gate; acknowledgement erases remaining
ciphertext without resending. The opaque audit records template and survives
account erasure. Rollback refuses verification intent, address-bound tokens or
verification audit history.

Disabling the new flag stops durable issuance, verification claims and decryption.
Base producer maintenance can still expire old intent and erase credentials while
the base worker runs. Disabling the base worker preserves rows until it resumes;
saved token deadlines stay enforced. Never downgrade token-consumption code to a
version that ignores saved address bindings while bound tokens remain.

### Offline verification

```sh
go test -race ./pkg/apid/mailoutbox -count=1
make test-apid RACE=1 RUN='^(TestProducerOutbox.*|TestDeleteAccount_.*|TestRestoreAccount_.*|TestScheduleDeletion_.*)$'
```

The integration test uses disposable PostgreSQL, the real protected Unix HTTP
handler and a fake SMTP transport. It drops a response **after** maild commits
admission, restarts the producer, retries and verifies one maild message and one
SMTP handoff. Other tests cover atomic rollback, simultaneous requests, worker
restart, stale leases, concurrent workers, expiry, suppression, account restore,
rearming, configuration changes, recovery quarantine, erasure and private access.
The lost-response test also exhausts a second producer intent, inspects its real
maild receipt and acknowledges it through apid without creating another message
or SMTP handoff. Reconciliation tests cover audited replay across restart, stale
revisions, concurrent operators, late flag/generation changes, mismatch/missing
receipts, active claims and rollback when the audit cannot commit.
Verification tests add atomic token/intent rollback, unchanged public signup and
default-off behavior, canonical origin selection, encrypted restart/replay,
consumption/handoff races in both orders, lifecycle cancellation, ciphertext
binding/erasure, key overlap, address binding after handoff and independent late
template-gate revocation. The real private HTTP test loses an accepted
verification response, restarts and proves one original maild message and one
fake SMTP handoff.
They change no deployed flags or DNS and perform no external delivery. Live
qualification remains separate.
## Durable password-reset handoff

`FAAS_INTERNAL_MAIL_PASSWORD_RESET_OUTBOX_ENABLED=0` separately gates internal
password-reset issuance/handoff in apid and admission in maild. It defaults off in
both units and all private examples. Existing inline forgot-password behavior
continues with this flag off. This adds no customer email capability or listener.

Apply
[`20261017073748949_platform_mail_password_reset_outbox.sql`](../../migrations/20261017073748949_platform_mail_password_reset_outbox.sql)
through the migration owner before starting the new binary. The base outbox's
readiness now checks its reset dependencies. The decision is recorded in
[ADR-20261017073748](../adr/20261017073748-mail-password-reset-outbox.md).

An opted-in apid needs the existing master, producer, scheduling and producer
outbox gates and the scoped `internal-producer` transport. Set the separate
`FAAS_INTERNAL_MAIL_PASSWORD_RESET_ORIGIN` in apid's `/etc/faas/sealed.env` to the
trusted canonical HTTPS dashboard origin. No path, query, fragment or credentials
are allowed. The saved link uses `/auth/reset`; request Host and forwarding
headers cannot choose its destination. Verification and reset origins/gates are
independent.

Reset encryption reuses the dedicated apid credential readers already selected
by `FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and its optional previous path.
These historical names also serve reset handoff. No apid key is delivered to
maild or mailctl. Keep the existing current/previous reader overlap procedure;
enabled startup refuses missing readers for its pending reset payloads. Workers
skip origins and keys they cannot read. Retain readers for the longest pending
credential lifetime plus its handoff lease; verification's 24-hour lifetime is
longer than reset's 15 minutes.

Token hash and encrypted intent commit together. A retry preserves the original
link, body, producer, origin, absolute 15-minute expiry and
`password-reset:<intent UUID>` admission key. No plaintext link or token is saved
in PostgreSQL. Queue failure keeps the public forgot-password response identical,
without inline fallback or orphan credentials. Existing shared pending caps,
platform/maild suppressions and recovery generation quarantine remain enforced.

The reset token is a separate family and cannot sign in through a magic link.
It is bound to the active verified account, current address and password revision.
Password set/change/delete, account/address change, expiry or prior use stops a
pending handoff. Password mutation and token consumption are atomic; concurrent
sibling links have at most one winner. The authenticated account/password write
paths advance the revision even with mail flags off. Binding and single-use
checks also remain enforced after delivery or an issuance/delivery flag rollback,
without reading any mail encryption keys. Reset credentials also retain their
namespace and recovery generation. Redemption checks that generation under the
shared recovery fence; a restored token cannot revive after a new recovery
generation is installed, even after global release or flag rollback. Legacy
inline tokens retain their
existing shared-family compatibility.

Use the existing private outbox inspection commands for metadata and safe result
codes. Reset admission reconciliation requires the reset gate in addition to
the existing reconciliation gates, including exact audit replay. It acknowledges
an existing maild receipt and never resends or extends a credential's deadline.
Maild admission does not prove final delivery. Once admitted, a notice can arrive
after account changes; its stale credential still fails redemption.

Expired token hashes/address bindings are deleted by bounded apid outbox
maintenance independently of the child flag. Account erasure cascades both token
and intent. The migration's Down refuses reset intents, tokens or audit history.
An expired or stale link needs a fresh user request; no force replay is added.
Keep live qualification, DNS, operational enrollment and flag activation in the
separate qualification workflow.

## Durable magic-link sign-in handoff

`FAAS_INTERNAL_MAIL_MAGIC_LINK_OUTBOX_ENABLED=0` independently gates internal
magic-link issuance/handoff in apid and template admission in maild. It defaults
off in both units and all private examples. The existing signup/sign-in endpoint
and uniform response remain unchanged. This adds no customer mail capability or
public listener. Live qualification remains a separate workflow.

Apply
[`20261017082032672_platform_mail_magic_link_outbox.sql`](../../migrations/20261017082032672_platform_mail_magic_link_outbox.sql)
through the migration owner before starting the new binary. Base producer outbox
readiness requires its credential table. See
[ADR-20261017082032](../adr/20261017082032-mail-magic-link-outbox.md).

The enabled producer needs the existing master, producer, scheduling and outbox
gates and scoped `internal-producer` transport. Configure the separate
`FAAS_INTERNAL_MAIL_MAGIC_LINK_ORIGIN` in apid's `/etc/faas/sealed.env` as the
trusted canonical HTTPS dashboard origin. No credentials, query or fragment are
allowed, and no path beyond an optional trailing slash. The frozen link path is
`/auth/verify`. Request Host and forwarding headers never choose this destination.
Verification, reset and magic-link origins and child gates are independent.

Magic-link payloads reuse apid's dedicated current/previous credential readers
selected by the historical `FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH` names. No apid key is
delivered to maild or mailctl. Enabled startup requires readers for all its live
pending magic-link payloads. Keep readers across all enabled credential families;
verification's 24-hour lifetime exceeds reset/sign-in's 15 minutes. Workers skip
unknown reader keys or origins and never rewrite pending links to a new origin.

The sign-in hash and encrypted intent commit together. Retries keep the original
token, body, producer, origin, absolute 15-minute expiry and
`magic-link:<intent UUID>` admission key. No plaintext link or token is stored in
PostgreSQL. Enabled save failure never falls back inline or leaves an orphan
hash. Existing pending caps, suppressions, recovery generation quarantine,
bounded attempts and terminal secret erasure continue to apply.

New magic-link credentials are sign-in-only and cannot reset a password. They
bind the account, current address, exact email-verification state and password
revision. Existing sign-in policy permits active and past-due accounts without
an abuse hold; suspended, deleted or held accounts cannot use durable links. Password set/delete, address or verification change, account
deactivation, expiry or use stops stale links at handoff and redemption. For an
unverified account, consuming the link also atomically revokes pre-verification
passwords, sessions and API keys before marking the bound address verified. A
failed cleanup leaves the token unused and rolls back the whole claim; expiry
is checked again after cleanup waits. Verified accounts retain their credentials.
Sign-in continues through the existing dashboard session/cookie/audit path and
current MFA requirements. No API key or completed MFA proof is issued by a link.

Saved credential checks remain enforced after rolling issuance/handoff flags
off, without encryption keys or configured origins. Namespace recovery holds or
generation mismatches reject old tokens even after global release. Already-issued
legacy inline links retain their existing shared-family compatibility. Maild
admission is separate from final delivery: an admitted email may arrive after an
account change, while its stale link still fails to authenticate.

Use existing private outbox metadata inspection and admission reconciliation.
Magic-link reconciliation and exact audit replay require the magic-link child
gate in addition to the existing operator/reconciliation gates. They acknowledge
an existing receipt, never send mail or extend a deadline. Inspection omits
addresses, token hashes, ciphertext, origins and encryption identities.

Bounded apid maintenance purges expired token hashes/address bindings even with
the child flag off. Account erasure cascades credentials/intents. Migration Down
refuses any magic-link token, intent or audit history. An expired or stale link
requires a fresh user request; no forced replay or deadline extension is added.

## Durable MFA recovery alerts

`FAAS_INTERNAL_MAIL_MFA_RECOVERY_ALERT_OUTBOX_ENABLED=0` independently gates
internal recovery-alert issuance/handoff in apid and admission in maild. Both
units and all private examples default off. This moves the existing alert for
`POST /v1/account/mfa/recover` into a durable transaction. It adds no public mail
API or listener. Live qualification remains in the separate qualification work.

Apply
[`20261017084607182_platform_mail_mfa_recovery_alert_outbox.sql`](../../migrations/20261017084607182_platform_mail_mfa_recovery_alert_outbox.sql)
through the migration owner before starting the new binary. Base producer outbox
readiness checks the new event columns and existing MFA account dependencies.
The decision is recorded in
[ADR-20261017084607](../adr/20261017084607-mail-mfa-recovery-alert-outbox.md).

The opted-in producer requires the existing master, producer, scheduling and
producer-outbox gates, scoped `internal-producer` transport and dedicated apid
current/previous credential readers. Their historical names remain
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. This alert contains no
authentication link and requires no origin. No apid key is supplied to maild or
mailctl. Enabled startup requires readers for its live pending alert payloads;
keep reader overlap across every enabled family. These alerts expire after 24
hours, the same maximum pending lifetime as email verification.

Code consumption and encrypted intent commit together. The encrypted event
contains only its database timestamp and remaining-code count. Recovery codes,
their HMAC hashes, HMAC keys and TOTP secrets never enter notification intent or
email. The account's existing recovery-code array remains the authentication
source. A last-code refusal, mismatch, already-used code, late disabled gate or
failed intent save creates no event and consumes no code. The final-code rule is
rechecked under the account lock, so concurrent recovery cannot burn the last
backup. Queue saturation or recovery hold refuses the paired operation before
consumption; the user retains the code for a retry.

Maild downtime does not fail a recovery: the saved alert is retried separately.
There is no inline fallback for failed enabled saves. Existing CSRF, MFA session
clearance, enrollment, session-ID reuse and step-up rules stay in the existing
handler. If session reissue fails after consumption commits, the code-use alert
remains queued. This flag covers recovery alerts; the existing MFA disable and
email-assisted disable flows keep their own behavior.

Retries retain the original event time, remaining count, V1 body, producer,
recipient, `mfa-recovery-alert:<intent UUID>` admission key and absolute 24-hour
deadline. Later password changes, MFA disable or fresh recovery codes do not
change the historical alert. Address changes or account deletion stop pending
delivery; billing suspension or a later abuse hold does not suppress this
historical security notification. Existing platform/maild suppressions, producer
budgets and recovery-generation quarantine still apply. A global recovery release
never rewrites an old intent's generation or authorizes replay.

Use the existing private outbox metadata inspection and receipt reconciliation.
Inspection omits the recipient, event time/count, ciphertext and reader identity.
Admission reconciliation and exact audit replay require this alert's own child
gate as well as the existing operator/reconciliation gates. Reconciliation only
acknowledges an existing receipt and never sends an email or extends its deadline.
Maild admission does not prove final delivery.

Terminal settlement, admission reconciliation and bounded expiry maintenance
erase event ciphertext, reader key and recipient. Disabling the child flag pauses
its pending delivery and restores inline issuance for later recoveries; base
maintenance can still expire and erase old payloads. Account erasure cascades
intent while opaque reconciliation audit survives. Migration Down refuses alert
intents or audit history. No forced replay or deadline extension is provided.

## Durable MFA disable-email requests

`FAAS_INTERNAL_MAIL_MFA_DISABLE_OUTBOX_ENABLED=0` independently gates durable
issuance/handoff for the existing authenticated MFA disable-email request and
private maild admission/reconciliation. Units and private examples keep it off.
Live delivery qualification remains separate; this adds no customer mail API.

Apply
[`20261017091735652_platform_mail_mfa_disable_outbox.sql`](../../migrations/20261017091735652_platform_mail_mfa_disable_outbox.sql)
before the new binary. The decision and locking rules are recorded in
[ADR-20261017091735](../adr/20261017091735-mail-mfa-disable-outbox.md).
The enabled producer requires master, producer, scheduling and producer-outbox
gates, scoped `internal-producer` transport, dedicated apid credential readers,
and a canonical HTTPS `FAAS_INTERNAL_MAIL_MFA_DISABLE_ORIGIN`. Request Host and
forwarding headers never choose the confirmation destination. The fixed V1 path
is `/auth/reset/mfa`.

Opening the link renders a small authenticated confirmation page behind the same
child gate. GET and email prefetch cannot consume the token or disable MFA. Only
the explicit button obtains the existing action-bound CSRF token and sends the
authenticated confirmation POST. The page uses no-store, no-referrer and CSP
nonces, with no external resources or token in browser history after rendering.
Pending MFA sessions can open this recovery page. Turning the child or base gate
off hides the new page; the existing confirmation POST retains saved credential
checks for clients after rollback.

The existing historical `FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH` identify the dedicated
apid current/previous readers. Keep overlap for every enabled pending family;
enabled startup rejects a missing reader for live disable-email payloads.
Maild and mailctl receive no apid encryption identities. Changed origins pause
claims for old saved links rather than rewriting them.

Credential hash and encrypted intent commit together. The raw 32-byte token
never enters inline request, sign-in, reset or verification tables. No MFA secret,
recovery code or recovery HMAC enters mail intent. A failed save, full queue,
recovery hold or late disabled gate leaves no orphan new token and preserves
any older request. Maild downtime is retried after the successful request.
Enabled save failure does not fall back to another transport.

All times come from the database. Delivery expires at request +24 hours;
confirmation is allowed from request +24 hours until request +48 hours. The V1
email prints both confirmation and expiration times. Retries preserve the same
token/link, requested time, cooldown, credential expiry, mail deadline, body and
`mfa-disable:<intent UUID>` admission key. A deliberate new user request
supersedes older durable and inline requests and starts a new cooldown. Inline
reissuance after rollback also invalidates pending durable links.

Confirmation requires the same authenticated account, current address, password
revision, MFA revision/enrollment and recovery generation. Secret replacement,
enrollment, clear and actual MFA policy changes advance a persistent revision;
restoring prior secret bytes does not restore an old request. Recovery-code burns
retain the factor and do not reset a pending request. Before the cooldown, the
existing 425 problem and Retry-After leave the token unused. At confirmation,
factor clear and single-use token consumption commit together; concurrent
confirmation has one winner. Failure leaves both unchanged. Explicit MFA policy
is retained, and confirmation grants no session or step-up proof.

Saved credential checks remain active when issuance/delivery flags are off and
need no mail key, host MFA identity or origin. Keep the schema and original
bindings during rollback. Only unknown hashes in the legacy sealed-token format can enter the legacy
inline reader; unknown raw 32-byte tokens stay invalid after pruning without host
keys; recognized expired, consumed, foreign-account or stale tokens
never fall back. The old inline format keeps its existing behavior.

Changed account/address/password/MFA state cancels pending link delivery.
Suppressions, producer budgets, recovery holds and generation quarantine remain
in force. Global recovery release does not authorize old restored links. Private
metadata inspection omits recipients, raw token, ciphertext and reader identity.
Receipt reconciliation and exact audit replay require this own child gate as
well as operator/reconciliation gates; acknowledgement creates no mail and
changes no cooldown or expiry. Admission is not final delivery evidence.

Terminal settlement and bounded expiry maintenance erase recipient and sealed
credential fields. Base maintenance can expire mail and purge expired token
bindings while the child gate is off. Hard account deletion cascades both;
opaque reconciliation audit survives. Migration Down refuses any disable-token,
intent or audit history. No resend override or cooldown bypass is provided.

## Durable MFA-disabled security alerts

`FAAS_INTERNAL_MAIL_MFA_DISABLED_ALERT_OUTBOX_ENABLED=0` independently gates
internal removal alerts in apid, maild and private reconciliation. Units and
examples keep it off. It adds no customer email API or public listener.
Apply
[`20261017094937523_platform_mail_mfa_disabled_alert_outbox.sql`](../../migrations/20261017094937523_platform_mail_mfa_disabled_alert_outbox.sql)
before enabling the new capability; enabled startup checks the migrated alert
constraint. See
[ADR-20261017094937](../adr/20261017094937-mail-mfa-disabled-alerts.md).

The capability requires the existing master, producer, scheduling and
producer-outbox gates, scoped `internal-producer` transport and dedicated apid
credential readers. Historical reader names remain
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. No origin is needed.
Retain readers for all enabled families' pending payloads; startup refuses a
missing live reader. No apid key is delivered to maild or mailctl.

Enabled password, recovery-code and email-confirmation removal commits an
encrypted alert in the same transaction. Both email-token formats are covered;
this does not enable durable link issuance or its page. A final recovery code
can remove MFA. The removal alert replaces any code-use notification for this
operation. Token consumption, factor removal and alert save all roll back if any
write fails, the queue is full, recovery is held or the child gate closes before
commit. Enabled failure never falls back to unnotified removal.

Authentication and CSRF stay in the existing routes. A pending MFA session cannot
remove MFA with a password. Password verification runs before locks and its
password/MFA revisions are checked again under the account lock. Recovery hashes
are matched under that lock; removal clears the authenticator and all its codes
while preserving explicit MFA policy. Confirmation issues no session or step-up
proof. Concurrent removals of the same enrollment produce one alert.

Durable email proofs keep every original binding, cooldown and deadline. Expiry
is checked after saving the alert so a blocked save cannot extend a token's life.
Legacy age envelopes still require host identities, the same authenticated
account, unused hash and original database-checked 24-hour cooldown. Requests
preceding the current enrollment are refused. Legacy rows lack a stored expiry
and saved factor/password versions; these cannot be inferred retroactively.

The encrypted payload contains only event time and method. No password, email
token, TOTP secret, recovery code or recovery HMAC enters intent, email or logs.
Retries preserve event time/method, the frozen V1 body, original 24-hour delivery
deadline and `mfa-disabled:<intent UUID>` admission key. Re-enrollment, later
password changes, billing suspension and later abuse holds do not alter history.
Changed address or account deletion stops pending delivery. Existing suppression,
producer budgets, lease fencing and recovery-generation quarantine apply.

Private inspection returns existing bounded metadata without payload, recipient
or reader key. Admission acknowledgement and exact audit replay require this own
child gate plus the existing reconciliation/operator gates. Acknowledgement only
records an existing receipt; it cannot send mail or reset a deadline. Maild
admission is not destination delivery evidence. Live qualification remains
separate.

Flag rollback restores the previous removal flow for later calls and pauses old
alerts; an operation already selected for an atomic alert fails closed on late
withdrawal. Base maintenance can expire and erase payloads with this child off.
Terminal settlement and reconciliation erase recipient and ciphertext. Hard
account deletion cascades intent while opaque audit survives. Migration Down
refuses remaining alert intent or audit. No forced replay is provided.

## Durable password-change security alerts

`FAAS_INTERNAL_MAIL_PASSWORD_CHANGED_ALERT_OUTBOX_ENABLED=0` independently gates
password-update alerts in apid, maild and private reconciliation. Units and
private examples keep it off. Apply
[`20261017101020256_platform_mail_password_changed_alert_outbox.sql`](../../migrations/20261017101020256_platform_mail_password_changed_alert_outbox.sql)
before enabling the capability. Enabled startup checks the migrated alert
constraint and every live pending reader. See
[ADR-20261017101020](../adr/20261017101020-mail-password-changed-alerts.md).

It requires the existing master, producer, scheduling and producer-outbox gates,
scoped `internal-producer` transport and dedicated apid credential readers under
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. No origin is required.
Retain current/previous readers needed by every enabled family's live pending
payloads. No apid identity is supplied to maild or mailctl.

Enabled dashboard additions/replacements and both durable/legacy reset completion
formats save the password update, revision, encrypted alert and any token use
atomically. A failed alert save, saturated outbox, recovery hold, or late gate
withdrawal leaves the password and token unchanged. The selected enabled path
never falls back to an unalerted update. Reset-link issuance has its own flag;
turning that off does not block alert-coupled redemption of saved credentials.

Dashboard authentication, CSRF, current-password/MFA proof policy, rate limits,
redirects and audit fields remain. Password verification/hash generation happen
outside owner locks; the locked password/MFA revisions fence the checked proof.
Existing session behavior remains: replacement in settings revokes other
sessions, first password addition preserves its existing behavior, and reset
revokes old sessions before the normal new-session/MFA flow. Those post-commit
steps retain their best-effort contracts; the alert records the committed change.

Durable reset links retain their saved bindings after issuance rollback. The
alert's namespace must match the credential. Legacy links share `login_tokens`
and have no saved purpose/address/revision/generation; this change cannot infer
those retrospectively. Enabled legacy reset requires a currently active account
with verified email, an unused hash and its original database deadline. For both
formats, the final expiry CAS runs after password and alert writes, so a blocked
save cannot extend the token's lifetime.

The encrypted event contains only database change time and method. No password,
PHC, reset token/hash or reset URL enters the alert payload, email or logs.
Retries retain the original time/method, frozen V1 body, 24-hour delivery deadline
and `password-changed:<intent UUID>` key. Later credential changes, suspension or
abuse holds do not rewrite that history. Address changes or account deletion
stop delivery. Existing suppression, producer budgets, lease fencing and
recovery-generation quarantine still apply.

Private inspection exposes existing bounded metadata only. Receipt
acknowledgement and exact audit replay require the own alert flag and existing
reconciliation/operator gates; acknowledgement cannot send mail or reset a
deadline. Maild admission is separate from destination delivery evidence. Live
qualification remains separate.

Flag rollback restores previous password flows for later calls and pauses
pending alerts. Base maintenance can still expire and erase them. Terminal
settlement and reconciliation erase recipient and ciphertext; account erasure
cascades intent while opaque audit survives. Migration Down refuses intent or
audit history. No resend override or deadline extension is provided.

## Durable MFA-enabled security alerts

`FAAS_INTERNAL_MAIL_MFA_ENABLED_ALERT_OUTBOX_ENABLED=0` independently gates
internal activation alerts in apid, maild and private reconciliation. Units and
examples keep it off. Apply
[`20261017102802761_platform_mail_mfa_enabled_alert_outbox.sql`](../../migrations/20261017102802761_platform_mail_mfa_enabled_alert_outbox.sql)
before opting in. Enabled startup checks the migrated constraint and all live
pending reader references. See
[ADR-20261017102802](../adr/20261017102802-mail-mfa-enabled-alerts.md).

It requires the existing master, producer, scheduling and producer-outbox gates,
scoped `internal-producer` transport and dedicated apid readers under
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. No origin is required.
Keep current/previous readers needed by every enabled family's pending payloads.
These readers remain separate from host MFA identities. Neither identity family
is supplied to maild or mailctl.

Enabled confirmation verifies the locked authenticator and saves activation,
policy clear, revision and encrypted alert in one transaction. Queue saturation,
recovery hold, failed persistence or late gate withdrawal rolls back all changes;
there is no fallback to confirmation without an alert. TOTP is checked after
owner waits and again after saving intent, so a blocked insert cannot extend
the proof window. Recovery hashes remain unchanged and are not read during
confirmation. No secret, code or QR URL enters the alert payload or logs.

Enabled setup keeps the once-only secret/recovery-code response and emits no
activation event until confirmation. A conditional pending-only write prevents
a setup request started earlier from replacing a confirmed authenticator or its
recovery codes. Unconfirmed setup can still be deliberately replaced. Both
setup and new activation refuse a held recovery generation.

Repeated confirmation verifies the current TOTP before returning success. It
retains activation time/revision, creates no duplicate event and does not
backfill old enrollment after opt-in. It also preserves any later explicit
policy change. An explicit disable followed by new setup and confirmation
creates a new event. Existing authentication, CSRF, TOTP limits, response shapes
and audit aliases stay in place.

Post-commit cookie reissue retains the same SID, clears `mfa_pending` and grants
no step-up stamp or new session row. Its existing non-fatal failure contract
remains: committed enrollment and alert survive a cookie reissue failure.

The encrypted event contains only database activation time. Retries retain that
time, the frozen V1 body, original 24-hour deadline and
`mfa-enabled:<intent UUID>` admission key. Later MFA/password changes, suspension
or abuse holds do not rewrite history. Changed address or account deletion stops
pending delivery. Shared suppression, producer budgets, leases and recovery
quarantine remain in force.

Private inspection exposes existing bounded metadata only. Receipt
acknowledgement and exact audit replay require this own flag plus existing
reconciliation/operator gates. Acknowledgement cannot send mail or reset a
deadline. Admission is separate from destination delivery evidence; live
qualification remains separate.

Flag rollback restores the previous setup/confirmation flows for later calls
and pauses pending alert handoff. Base maintenance still expires and erases
payloads. Terminal settlement and reconciliation erase recipient/ciphertext;
hard account erasure cascades intent while opaque audit survives. Migration Down
refuses activation intent or audit history. No forced resend is provided.

## Durable payment-failure notifications

`FAAS_INTERNAL_MAIL_PAYMENT_FAILED_OUTBOX_ENABLED=0` independently gates this
internal billing capability in apid, maild and private reconciliation. Units and
examples keep it off. Apply
[`20261017105351033_platform_mail_payment_failed_outbox.sql`](../../migrations/20261017105351033_platform_mail_payment_failed_outbox.sql)
before opting in. See
[ADR-20261017105351](../adr/20261017105351-mail-payment-failure-notifications.md).

It requires master, producer, scheduling and producer-outbox gates, scoped
`internal-producer` transport and dedicated apid readers under
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. No origin is required.
Keep readers for all enabled families' unexpired pending payloads. They are not
supplied to maild or mailctl. Enabled startup checks the new schema and reader
references; maild separately checks its lifecycle metadata projection.

After signature verification, customer binding and idempotent invoice upsert,
all three provider webhooks select this path for normalized payment-failed and
subscription-past-due events. A provider event ID is mandatory. Apid saves the
first overdue status, its original dunning timestamp, encrypted warning and
permanent provider event receipt in one transaction. A failed save, pending
limit, recovery hold or late flag withdrawal leaves that transition retryable
without an expiring claim or inline-email fallback. Overlapping failure kinds
share one warning and never reset the original overdue clock.

Receipts have no TTL and must not be pruned: they prevent a repeated failure
from re-entering dunning after recovery. They contain only provider, event ID,
account ID, normalized event type and processing time. Account erasure cascades
them. Distinct provider events still follow the existing billing event policy;
receipts neither reorder those events nor backfill historical legacy events.

The encrypted payload contains only the original database overdue timestamp.
Retries retain the frozen V1 body, absolute seven-day suspension and
twenty-one-day deletion-scheduling deadlines, original 24-hour delivery expiry
and `payment-failed:<intent UUID>` admission key. No invoice, charge amount,
payment method, billing portal credential or raw webhook enters the payload.
Existing suppressions, budgets, leases and recovery quarantine remain in force.

Enabled payment recovery checks the locked current account rather than a stale
webhook snapshot. It restores dunning accounts, clears dunning/deletion stamps,
mirrors a real restoration to the personal organization and atomically cancels
and erases pending warnings. Operator suspensions remain in place. The current
best-effort restored-account notice and meterd's later dunning notices retain
their existing behavior.

Before handoff, apid checks current address, past-due status, deletion stamp and
the exact overdue period. Maild checks the same lifecycle metadata under an
account lock before committing SMTP submission start. This also stops warnings
already admitted to maild if recovery, changed address or account erasure wins
before that commit. A new overdue period cannot revive an older warning. The
maild record becomes `cancelled` with `account_changed` and no SMTP call. The
own gate is checked after submission lock waits; disabling it holds admitted
warnings. Once submission start commits, a later recovery cannot recall SMTP
custody. Submitted/uncertain work is never automatically resent.

Private inspection exposes bounded metadata. Acknowledgement, receipt review
and exact audit replay require this own flag plus existing operator and
reconciliation gates. Acknowledgement sends no mail and extends no deadline.
Terminal outbox settlement erases recipient and ciphertext. Account erasure
cascades intent/receipts while opaque reconciliation audit survives. Migration
Down refuses any used payment notification, receipt or audit history.

Flag rollback pauses pending handoff and maild delivery. Base maintenance can
still expire and erase pending payloads. Later webhooks use the legacy billing
path, including its five-minute replay window; permanent failure deduplication
requires this capability to remain selected. Retain durable receipts and
evaluate provider redelivery before switching to legacy processing. Admission
does not establish SMTP custody or destination delivery; live qualification
remains separate. No activation, deployment or real email is part of this change.

## Durable payment-recovery confirmations

`FAAS_INTERNAL_MAIL_ACCOUNT_RESTORED_OUTBOX_ENABLED=0` independently gates
internal recovery confirmations in apid, maild and private reconciliation. Units
and examples keep it off. Apply
[`20261017112923185_platform_mail_account_restored_outbox.sql`](../../migrations/20261017112923185_platform_mail_account_restored_outbox.sql)
before opting in. See
[ADR-20261017112923](../adr/20261017112923-mail-account-restored-confirmations.md).

It requires the existing master, producer, scheduling and producer-outbox gates,
scoped `internal-producer` transport and dedicated apid readers under
`FAAS_INTERNAL_MAIL_VERIFICATION_IDENTITY_PATH` and
`FAAS_INTERNAL_MAIL_VERIFICATION_PREVIOUS_IDENTITY_PATH`. No origin is required.
Keep readers needed by every enabled family's unexpired pending payloads.
Maild and mailctl receive neither reader. Payment-failure issuance can remain
independently off. Enabled startup checks the schema, lifecycle trigger and
pending reader references; maild checks its metadata projection and trigger.

After existing signature checks, customer binding and invoice persistence,
normalized payment-success events select this path before the legacy replay
claim. A bounded nonempty provider event ID is mandatory. Apid commits a real
dunning restoration, personal-org mirror, pending failure-warning cancellation,
encrypted confirmation and permanent success-event receipt together. A failed
paired write, held recovery, unavailable capacity or late gate withdrawal leaves
that restoration retryable without inline-mail fallback. Cancelled failure
warnings release their pending slots before confirmation capacity is checked.

Duplicate successes and distinct successes on an already-active account create
no extra confirmation. A saved receipt prevents an old success from restoring a
later overdue period. Receipts have no expiry and must not be pruned. They retain
only provider, event ID, account ID and processing time and cascade on erasure.
They do not order distinct events or backfill historical legacy deliveries.

The current account row decides recovery; an earlier active snapshot cannot
miss a concurrent failure. Dunning suspensions and dunning deletions retain
their existing recovery rules. Operator suspensions and voluntary deletion stay
in place. Credit, subscription, plan, quota and lifecycle follow-up keep their
existing ordering. If quota follow-up fails after the paired commit, webhook
retry uses the saved receipt and cannot enqueue another confirmation. Polar PDF
generation remains asynchronous and does not block acknowledgement.

The account's billing-mail revision advances on status, overdue timestamp,
deletion stamp or email changes through all existing database writers, including
meterd and legacy billing. Unrelated password/MFA changes and no-op lifecycle
updates do not advance it. This metadata fence remains active with the flag off
to protect saved confirmations through rollback. It creates no mail work.

The encrypted payload contains only the original database recovery timestamp.
The intent also binds the post-recovery lifecycle revision. Retries preserve
that time, the frozen V1 body, original 24-hour delivery deadline and
`account-restored:<intent UUID>` admission key. Shared suppression, budgets,
leases and recovery quarantine remain in force.

Apid checks current address, active status, absence of overdue/deletion stamps
and exact lifecycle revision before handoff. Maild checks the same metadata
under the account lock before committing SMTP submission start. A later failure,
status/address cycle or erasure therefore cancels stale confirmations even when
the account has become active again, including confirmations admitted to maild.
The maild recipient becomes `cancelled` with `account_changed`, without an SMTP
call. The own gate is checked again after lock waits. Once submission start
commits, later changes cannot recall custody. Submitted/uncertain work is never
automatically resent.

Private acknowledgement, receipt review and exact audit replay require the own
flag and existing operator/reconciliation gates. Acknowledgement cannot send or
extend a deadline. Terminal outbox settlement erases address/ciphertext; erasure
cascades intents/receipts while opaque audit survives. Migration Down refuses
notification, receipt or reconciliation history.

Flag rollback pauses handoff and maild delivery; base maintenance may still
expire and erase pending payloads. Later webhooks use the legacy recovery path
and its replay window. Retain durable receipts and evaluate provider redelivery
before switching. Admission does not establish SMTP custody or destination
delivery. No activation, deployment or real email is part of this change; live
qualification remains separate.

## Durable overdue-account suspension notices

`FAAS_INTERNAL_MAIL_ACCOUNT_SUSPENDED_OUTBOX_ENABLED=0` independently gates
suspension notices in apid, maild, meterd and private reconciliation. Units and
examples keep it off. Apply
[`20261017115753852_platform_mail_account_suspended_outbox.sql`](../../migrations/20261017115753852_platform_mail_account_suspended_outbox.sql)
before opting in. See
[ADR-20261017115753](../adr/20261017115753-mail-account-suspended-notices.md).

Meterd delegates the paired billing/outbox transaction to apid on its existing
loopback operator listener. This adds no listener or public mail API. Enabled
meterd requires the master, producer, scheduling and producer-outbox gates,
`FAAS_APID_INTERNAL_BASE_URL` as a literal loopback HTTP origin with an explicit
port, and `FAAS_INTERNAL_MAIL_SUSPENSION_TOKEN_PATH`. Apid reads that path from
its existing private environment; meterd can receive it in
`/etc/faas/secrets/meterd/billing.env`. The file contains a dedicated service
token of at least 32 printable bytes. Protect it for the two daemon users; do
not reuse a producer or Safe Deploy token. Maild does not receive this token.
The narrow client disables proxies and redirects, bounds bodies and uses a
five-second timeout. Enabled startup probes the authenticated readiness route.
Default-off meterd reads only its exact flag and creates no client or network
request. Late enablement requires configuration and restart; it cannot fall back.

The private routes are readiness, account suspension and exact follow-up
completion under `/v1/internal/billing/`. They are mounted only on the loopback
operator mux when enabled and reject non-loopback peers and other credentials.
Requests carry only account ID, expected overdue timestamp and request UUID.
They cannot supply a recipient, message body, transition time or deadline.

Apid checks the locked current row and database clock against the seven-day
overdue threshold. It atomically saves suspended status, the billing lifecycle
revision, pending failure-warning cancellation, encrypted notice and a permanent
per-account/per-overdue-period receipt. Paired-write failure, held recovery,
capacity exhaustion or late flag withdrawal leaves suspension retryable without
inline-mail fallback. Different ticks cannot create duplicate notices. An old
period cannot suspend a later one; active accounts, operator suspensions and
legacy suspensions receive no backfill.

Only apid receives the existing dedicated current/previous readers under their
historical verification identity names. No origin is needed. Retain every reader
needed by an enabled family's unexpired pending payloads. The sealed payload
contains original database suspension time and original overdue time. The frozen
V1 body preserves those times and the original 21-day deletion deadline. Expiry
is the earlier of suspension plus 24 hours or that deletion deadline. A late
sweep can enforce suspension while its already-expired notice is erased without
sending. Retry/restart preserves `account-suspended:<intent UUID>` and cannot
extend the delivery window.

Apid validates current status, address, overdue anchor, deletion stamp and exact
billing revision before handoff. Maild repeats that check under the account lock
before durable SMTP submission start. Payment recovery, deletion or a status or
address cycle cancels stale notices, including messages already admitted to
maild. The revision fence remains active with issuance off. Maild records
`cancelled` / `account_changed` without an SMTP call. The own gate is rechecked
after lock waits. Later changes cannot recall custody after submission start;
submitted/uncertain mail is never automatically resent.

A five-minute persisted lease protects meterd's existing parking/notification
follow-up. Lost replies and restarts resume only the same current dunning
suspension after lease expiry. Completion requires the exact intent and lease;
exact completion replay is idempotent. Follow-up is at least once across crashes
and keeps the existing idempotent scheduler call, parking reason and best-effort
handling of individual failures. It does not send email inline. The existing
21-day deletion transition and deletion email remain separate.

Shared suppression, budgets, retry leases and recovery quarantine apply. Private
receipt review, acknowledgement and audit replay require the own flag and the
existing operator gates. Account erasure cascades intent and follow-up receipt;
terminal outbox settlement erases address/ciphertext while opaque audit survives.
Down refuses any used suspension intent, command receipt or reconciliation audit.

Flag rollback pauses pending notices and private follow-up access; maintenance
can still expire/erase mail payloads. Future timer work uses its legacy path.
Review pending follow-up that committed before rollback, since already-suspended
accounts do not re-enter the legacy suspension step. Retain receipts; changed
recovery generations require operator review. No activation, deployment,
credential enrollment or real email is part of this change. Live qualification
remains separate.

## Durable non-payment deletion notices

`FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_OUTBOX_ENABLED=0` independently gates
non-payment deletion notices across apid, meterd, maild and private
reconciliation. Units and examples keep it off. Apply
[`20261017125602853_platform_mail_nonpayment_deletion_outbox.sql`](../../migrations/20261017125602853_platform_mail_nonpayment_deletion_outbox.sql)
before opting in; see
[ADR-20261017125602](../adr/20261017125602-mail-nonpayment-deletion-notices.md).

Meterd uses apid's existing loopback operator listener and
`FAAS_APID_INTERNAL_BASE_URL` (plain HTTP, literal loopback IP, explicit port).
The dedicated `FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_TOKEN_PATH` is delivered
through apid's private environment and meterd's existing
`/etc/faas/secrets/meterd/billing.env`. Provision a distinct token file of at
least 32 printable bytes, protected for the two daemon users. Never reuse the
producer, Safe Deploy or enabled suspension-command token. Maild receives no
command token. Default-off meterd reads only the exact own flag; enabled startup
requires the parent mail gates and authenticated readiness. The client disables
proxies/redirects, uses a five-second timeout and bounds bodies.

Private routes are `GET /v1/internal/billing/nonpayment-deletions/readyz`,
`POST /v1/internal/billing/accounts/{id}/nonpayment-deletion` and its `/complete`
suffix. Only the loopback operator mux mounts them. Requests contain expected
overdue time/request UUID or exact intent/lease completion proof; callers cannot
choose recipient, body or deadline. No listener or public mail permission is
added. Late enablement requires configuration/restart and cannot fall back.

Apid checks the locked row and database time against the original 21-day overdue
threshold. Deletion-pending status, the database deletion timestamp, billing
revision, cancellation of pending failure/suspension mail, encrypted notice and
per-period receipt commit together. Failed persistence, capacity exhaustion,
recovery hold or gate withdrawal leaves the transition retryable. Duplicate
ticks cannot create another notice. No historical deletion is backfilled.

A non-payment deletion includes the account's managed PostgreSQL databases,
which apid deletes when the 30-day grace ends
([ADR-20261024150500](../adr/20261024150500-account-deletion-includes-managed-postgres.md)).
The operator kill switch `FAAS_NONPAYMENT_DELETION_MANAGED_POSTGRES=off`, set on
apid and meterd, stops that and restores the guard below.

A deletion guard is not an outage. When the account still owns live managed
PostgreSQL databases and the kill switch is off (or shared app ownership is
unresolved), apid answers
`409` with `{"reason":"managed_postgres_databases","managed_postgres_databases":N}`
(or `shared_app_ownership`) and commits nothing. Meterd logs
`meter: non-payment deletion blocked` with the account and reason, keeps the
account suspended and retries on the next hourly sweep. The legacy direct
store transition reports the same guard. The gauge
`meterd_dunning_deletion_blocked_accounts{reason}` counts the accounts the last
sweep could not advance; a non-zero `managed_postgres_databases` value means
suspended accounts keep provider databases running. Meterd never deletes
customer databases; only apid's grace sweep does.

The frozen V1 body retains the original overdue timestamp and the actual
hard-deletion deadline: saved `deletion_requested_at` plus the canonical account
grace period. Expiry is the earlier of 24 hours after that saved transition or
the hard-deletion deadline. Retries preserve body, expiry and
`nonpayment-deletion:<intent UUID>`. Only apid gets the dedicated current/previous
payload readers; retain all readers needed by enabled families' unexpired
pending payloads. No origin is required.

Apid and maild recheck current status, recipient, overdue/deletion timestamps
and billing revision before handoff/SMTP submission start. Recovery, address or
status cycles, changed deadlines and erasure cancel stale notices before custody.
After submission start commits, mail cannot be recalled or automatically resent.
Shared suppression, budgets, retry leases and recovery quarantine apply.

A saved five-minute lease lets meterd resume its existing lifecycle/deletion
notifications after lost replies and restarts, including deletion-pending rows.
It sends no inline mail or scheduler call on this path. Exact account/intent/lease
completion is idempotent; notification follow-up remains at least once across
crashes and retains best-effort handling of individual failures. Changed billing
revisions or recovery generations stop stale follow-up and require review.

Rollback pauses pending notices and private follow-up. Future timer transitions
use the legacy path; review already-committed incomplete follow-up since those
deletion-pending rows do not repeat the legacy step. Re-enabling can resume
eligible expired leases. Receipts never extend the reaper's saved deadline.
Account erasure cascades intent/receipt; terminal settlement erases address and
ciphertext. Opaque reconciliation audit survives; Down refuses used history.
No activation, deployment, credential enrollment or real email is part of this
change. Live qualification remains separate.

## Durable paid-tier quota warnings

`FAAS_INTERNAL_MAIL_QUOTA_WARNING_OUTBOX_ENABLED=0` independently gates daily
paid-tier quota warnings across apid, meterd, maild and private reconciliation.
Units and examples keep it off. Apply
[`20261017133739854_platform_mail_quota_warning_outbox.sql`](../../migrations/20261017133739854_platform_mail_quota_warning_outbox.sql)
before opting in; see
[ADR-20261017133739](../adr/20261017133739-mail-quota-warnings.md).

The dedicated `FAAS_INTERNAL_MAIL_QUOTA_WARNING_TOKEN_PATH` is supplied only to
apid and meterd, through apid's private environment and meterd's existing
`/etc/faas/secrets/meterd/billing.env`. Provision at least 32 printable bytes,
protected for those daemon users. Keep it distinct from producer, Safe Deploy
and enabled suspension/non-payment deletion credentials. Maild receives no
command credential. Meterd uses `FAAS_APID_INTERNAL_BASE_URL` on the existing
loopback operator listener: plain HTTP, literal loopback IP and explicit port.
The client disables proxies/redirects, bounds bodies and times out in five
seconds. Enabled startup probes authenticated readiness. Disabled setup reads
only the exact own flag; late enablement requires configuration/restart.

Private routes are `GET /v1/internal/billing/quota-warnings/readyz`,
`POST /v1/internal/billing/accounts/{id}/quota-warning` and its `/complete`
suffix. Commands contain expected UTC day/paid plan/request UUID or exact
account/intent/lease completion proof. They cannot select usage, included quota,
recipient or content. No customer route, mail permission or listener is added.

Apid verifies the locked account and database UTC day, reads authoritative
monthly MB-seconds from `usage_minutes`, applies the existing six-decimal GB-h
rounding and canonical paid-plan included quota, then saves the daily stamp,
encrypted notice and business snapshot receipt together. Persistence failure,
capacity exhaustion, recovery hold or gate withdrawal leaves issuance retryable.
Concurrent ticks reuse one notice; today's/future legacy stamps are not
backfilled. Free-tier parking and billing computations keep their existing rules.

Both message alternatives retain the original plan, usage and day. Optional
`FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL` must also be configured in apid when this
family is enabled; it is validated and frozen in the encrypted payload, including
the RFC 8058 header pair. Empty configuration omits those headers. Dedicated
current/previous payload identities remain apid-only; retain reader coverage for
all enabled families' unexpired pending payloads. No dashboard origin is needed.
Retries preserve `quota-warning:<intent UUID>`, body and expiry. Each warning
expires at UTC midnight, including month rollover.

The always-active quota revision trigger tracks plan, address, status, overdue
anchor, deletion request and daily stamp. Both apid and maild recheck the original
plan/day/stamp/revision before handoff/SMTP submission start. Cycles back to the
old value still cancel stale notices. Deleting or erased accounts and old usage
days cannot send. Shared suppression, budgets and recovery quarantine apply.
After submission start, SMTP custody cannot be recalled or automatically resent.

The saved five-minute notification lease allows meterd to recover lost replies
and interrupted follow-up during the original day. Notifications use the saved
plan, usage, quota and observation time. Completion requires exact opaque proof
and is idempotent. Individual notification failures remain best effort; crash
recovery can repeat notification attempts. No scheduler call or inline email is
made on this path, and mail failure does not block paid-upgrade restoration.

Rollback pauses durable issuance, handoff, delivery and private follow-up.
Today's saved stamp still suppresses legacy inline mail; later days use the
legacy path. Review incomplete receipts before rollback and re-enable during
the original day for eligible recovery. Old days, changed revisions and changed
recovery generations cannot be replayed. Account erasure cascades intent and
receipt; terminal settlement erases address/ciphertext. Opaque audit survives,
and Down refuses used history. Deployment, activation, credential enrollment and
live delivery qualification remain separate work.

## Durable subscription-ended confirmations

`FAAS_INTERNAL_MAIL_SUBSCRIPTION_ENDED_OUTBOX_ENABLED=0` independently gates
subscription-ended confirmations in apid, maild and private reconciliation.
Units and examples keep it off. Apply
[`20261017142138994_platform_mail_subscription_ended_outbox.sql`](../../migrations/20261017142138994_platform_mail_subscription_ended_outbox.sql)
before opting in; see
[ADR-20261017142138](../adr/20261017142138-mail-subscription-ended-confirmations.md).

Only existing signed Stripe, Paddle and Polar webhooks select this path. It adds
no listener, customer mail API, SDK method, meterd command or service credential.
Enabled apid requires the existing producer outbox, internal-producer transport,
parent gates and dedicated current/previous payload readers. No origin is needed.
Disabled setup reads only the exact own flag; late enablement requires complete
configuration/restart and cannot fall back to inline mail.

A termination must carry a bounded event ID and actual subscription handle for
the current provider-qualified customer. Stripe subscription-deleted deliveries
use their signed object ID when the invoice-style subscription field is absent.
A different nonempty binding is stale and receives no downgrade. A paid account
with a missing current binding returns a retryable failure. Qualify existing
provider bindings before opting in. Already-Free accounts without a binding
receive no historical backfill.

Apid locks the account and provider row, then commits the exact binding clear,
Free downgrade, legacy customer/subscription cache, personal-org mirror,
streaming opt-in removal, permanent delivery/business receipts and any encrypted
confirmation together. It preserves status and dunning/deletion anchors. Only
an active paid account with no overdue/deletion anchor receives the voluntary
notice. Other qualified providers' bindings remain intact. Failures in paired
writes, mirrors, streaming cleanup, capacity, recovery or final gating leave a
notice-producing transition retryable. Non-mail revocations retain their existing
billing semantics. Free-tier quota and VM lifecycle rules are unchanged.

Receipts permanently deduplicate both provider delivery ID and ended subscription
identity. Replaying a delivery or sending another event for an already-ended
subscription cannot downgrade a later subscription or create another email.
Receipt identity uses hashes rather than raw customer/subscription handles.
Plan-change app notifications commit through transactional `pg_notify` with the
downgrade. Audit remains best effort after the business commit.

The original paid plan and database end time are encrypted in the frozen V1
payload. Retries retain the same content, `subscription-ended:<intent UUID>` and
24-hour expiry. Keep readers covering all enabled families' unexpired pending
payloads. Shared suppression, budgets, lease/retry and recovery controls apply.

Always-active account and qualified-identity triggers advance a separate
subscription-mail revision for plan, address, status, overdue/deletion anchor or
provider-binding changes, including changes while issuance is off. Identity
insert/delete cycles and another provider's binding changes also invalidate
pending mail. Platform writers take the account lock before the provider row;
use the same lock order for operator identity edits. Both lifecycle triggers
must remain enabled. Password, MFA and quota-warning stamps do not alter this
revision.

Apid and maild check current active/Free state, empty subscription cache, address,
anchors and exact subscription revision before handoff/SMTP submission start.
Re-subscription, address/status/plan/binding cycles, deletion or erasure that
commit first cancel stale mail, including admitted messages. After submission
start, SMTP custody cannot be recalled or automatically resent.

Rollback pauses pending durable mail and private reconciliation; future events
use the legacy path. Already-ended accounts do not send replacement inline
confirmations. Re-enable to resume current, unexpired notices. Account erasure
cascades intents and receipts; terminal settlement erases address/ciphertext.
Opaque audit survives and Down refuses used history. Activation, deployment,
credential enrollment and live qualification remain separate work.

## Durable deployment-failure notices

`FAAS_INTERNAL_MAIL_DEPLOYMENT_FAILED_OUTBOX_ENABLED=0` independently gates apid
capture/handoff, maild admission/submission and private reconciliation. Delivered
units and examples keep it off. Apply
[`20261017155842001_platform_mail_deployment_failed_outbox.sql`](../../migrations/20261017155842001_platform_mail_deployment_failed_outbox.sql)
before opting in; see
[ADR-20261017155842](../adr/20261017155842-mail-deployment-failure-notices.md).

Enabled apid needs the existing producer outbox, parent gates, internal-producer
transport and dedicated current/previous payload readers. The optional
`FAAS_CLI_AUTH_URL_BASE` must be an HTTPS origin of at most 256 bytes; the default
is `https://gregale.dev`. Retries retain the saved dashboard origin. Disabled
setup reads only the exact own flag. This adds no public route, credential, SDK
method or meterd command. A selected subscriber never sends inline, including
when its durable service is unavailable.

Passive lifecycle metadata records the database failure time without copying
addresses, errors or logs. Mail capacity/encryption/delivery run later in apid;
they cannot roll back an already committed deployment outcome. The existing
worker polls at most 25 failures per bounded pass. The first enable creates a
durable boundary and excludes old failures. Same-generation restarts retain it,
including failures committed while apid stopped. A pause can resume eligible
failures within their original 24-hour lifetime.

Capture commits encrypted notice, per-failure observation and app cooldown
together. A failed capture leaves its cooldown unclaimed for retry. The hourly
cooldown is shared with the legacy path. Suppressed failures have durable skip
receipts and do not reappear after the hour passes. The recipient is the named
deployer. Legacy inline cooldown claims during a rollback also exclude those
failures after the hour elapses. Recipient selection falls back to the app owner
only when no deployer is recorded. A
deployer outside the app account must still be an active member of its org.

The frozen V1 notice has a stable `deployment-failed:<intent UUID>` key and a
24-hour expiry measured from failure, not capture/retry. It contains a bounded
redacted summary and dashboard link. Oversized fields are omitted whole; build
log excerpts and attachments never enter the queue. Keep payload readers
covering this family's unexpired pending snapshots when rotating identities.
Existing budgets, suppressions and retry controls apply.

Apid and maild check source revision, recipient address/status, app
identity/status, membership when needed and same-scope success revision.
Address/status/ownership cycles, recovered/replaced/deleted deployments or a
later same-scope success cancel stale notices, including admitted mail. Success
in another scope leaves the notice eligible. Keep all three lifecycle triggers
enabled. Readers use the separate scope fence without locking deployment rows.
After durable submission start, SMTP custody cannot be recalled or replayed.

A recovery hold stops capture. After an explicit recovery changes the generation,
the first enabled pass after release advances the discovery boundary to that
generation. Unobserved history before the new boundary is excluded. Investigate
using deployment records and existing recovery evidence; do not rewrite failure
stamps or replay keys to resend mail.

Use existing private outbox status/list/admission and audited reconciliation
commands for saved intents. Reconciliation also requires this exact flag and
never resends mail. Withdrawing the flag pauses durable work. Future legacy
notifications use the inline path, sharing its cooldown. Re-enable explicitly to
resume current, unexpired durable jobs. Account erasure cascades
intents/observations; terminal settlement erases address/ciphertext. Used history
blocks schema rollback. Deployment, activation, credential enrollment and live
qualification remain separate work.
