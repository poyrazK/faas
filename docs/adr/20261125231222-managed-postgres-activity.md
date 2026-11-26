# ADR-20261125231222: Why a managed PostgreSQL database is awake

Status: Accepted

Extends ADR-155 (managed PostgreSQL), ADR-20261121120000 (customer suspend
timeout) and the idle-tail telemetry of PR #8039.

## Context

A scale-to-zero database bills compute while it is awake, and something
outside the database decides that: a cron, a request, a worker, a prewarm, a
direct connection. Gregale shows none of it. `gregale postgres usage` gives
one account-wide CU-hour total, and nothing ties a database's compute to the
thing that woke it.

On 2026-11-26 the owner's account had used 118.5 of its 200 CU-hours by the
25th. Finding out why took control-plane SQL: two test databases woke 96
times a day each because two apps ran a cron every 15 minutes, and each wake
held the compute for the old five-minute suspend timeout. A customer has no
way to find that, and would see only the bill.

Everything needed to answer it already exists:

- Neon's operation log holds each compute's `start_compute` and
  `suspend_compute`, to the second. The idle-tail reader already pages
  through it for every scale-to-zero database that used compute in a window.
- `events` holds what woke an app: `cron.fired` (app, cron path) and
  `wake.admitted` (app, request id, empty for a wake no request asked for).
- Bindings say which apps can reach a database, and
  `managed_postgres_wake_with_app` when the platform started one for a wake.

## Decision

Gregale records every start of a database's compute with its likely cause,
and shows it to customers, their agents, and to them unprompted when one
cause dominates (owner, 2026-11-26).

### Record

- `managed_postgres_compute_starts` has one row per compute start: the
  database, the account, `started_at`, `suspended_at` (null while it runs),
  the provider's operation id (unique per database, so a re-read is
  idempotent), and the cause: `kind`, the app and a detail (the cron path).
  apid is the only writer. Rows go with the database and are pruned after
  `api.ManagedPostgresActivityRetention` (30 days).
- The usage sweep's idle-tail read already fetches the window's operations;
  it now also returns the window's starts and suspends, in the same requests,
  so recording adds no Neon API calls. It returns them even where the idle
  tail cannot be priced (a config apply in the window). A provider that has
  no operation log records nothing; the API says so.
- The cause is decided when the start is recorded, from the first match in
  this order, over the database's bindings live at the start:
  1. `app_wake`: the platform started it for an app's wake
     (ADR-20261125105038), at most 30 seconds before.
  2. `cron`: a bound app's `cron.fired` within 2 minutes before. Detail: the
     cron path.
  3. `request`: a bound app's `wake.admitted` with a request id within 1
     minute before.
  4. `app_start`: a bound app's `wake.admitted` without one (a prewarm, a
     deploy, a restart) within 1 minute before.
  5. `running_app`: a bound app had a live instance started before it (a
     worker, an app with a warm floor, a request served from a warm
     instance).
  6. `external`: none of the above. A connection that did not come through
     an app Gregale knows of: `psql`, a client outside Gregale, a branch tool.
  The cause is a best guess from timing, and the surfaces say "likely cause".

### Show

- **API.** `GET /v1/postgres/databases/{id}/activity?since=` (and the org
  route) returns a summary over the period (starts, awake seconds), the
  starts newest first (bounded page), per-cause totals, the period covered,
  and `hints`. CU-seconds stay on the usage API, which already carries them
  per database; the idle-tail share stays a fleet metric. The usage API gains `databases`: each database's CU-seconds,
  starts and awake seconds for the month.
- **Hints** are computed by apid, so the CLI, the API and the MCP tool say
  the same thing. Each cause holding at least a fifth of the period's awake
  time names itself and its lever (several causes can each get a hint): a cron names its schedule and suggests running it less
  often or a 1 minute suspend timeout; a running app suggests that a pool
  keeping connections open holds the compute; `external` names direct
  connections.
- **CLI.** `gregale postgres activity DATABASE [--since 24h|7d]` prints the
  summary, the starts table (started, awake, likely cause) and the hints.
  `gregale postgres usage` adds the per-database table.
- **MCP.** A read-only `gregale_get_postgres_activity` tool returns the same
  payload, so an agent can answer "why is my database bill high" itself.

### Tell

A weekly sweep mails the account owner, once per database per week, when a
scale-to-zero database averaged at least
`api.ManagedPostgresAwakeNoticeHoursPerDay` (6) awake hours a day over the
last complete UTC week (Monday to Monday) and one cause other than `request`
holds at least `api.ManagedPostgresAwakeNoticeCauseSharePercent` (50) of its
starts. Requests are the app's own traffic, with nothing to change, so they are
never noticed. The email names the database, the hours, the cause and its
lever, and links the activity command. Notices are recorded
(`managed_postgres_awake_notices`) and marked mailed in the transaction that
queues the email (template `managed_postgres_awake`, gated by
`FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_AWAKE_OUTBOX_ENABLED` on apid and maild),
so a replica never mails twice. The usage sweep runs the pass at most hourly,
and records a week only `api.ManagedPostgresAwakeNoticeSettle` (12 h) after it
ends, so the lagging idle-tail reads have recorded its last starts; a start
with no suspend and no later start counts no time in a notice.

## Consequences

- Coverage follows the idle-tail queue: a window the sweep skips (another
  window of the same database replaced it before a pass reached it) leaves a
  gap, and environment-branch databases, held computes and a restore's second
  compute are not read. The API reports the covered period; gaps are visible,
  not filled with guesses.
- A start whose suspend was never read (its window skipped) ends at the
  database's next recorded start; only the latest open start counts to now.
  Wake-with-app claims are kept per claim (`managed_postgres_app_wake_starts`)
  so a start read later still matches its claim.
- The cause can be wrong when two things happen within the matching window (a
  cron and a request in the same minute); the first rule wins. It is
  diagnosis, never billing: CU-hours stay the usage ledger's.
- Nothing new is billed and no plan changes. The table is operational data
  (not cloned into environments).
- Rollout: the migration, then apid. An older apid ignores the table. The
  email adds `20261126120000100_platform_mail_managed_postgres_awake.sql`
  (widens the outbox template CHECKs; marked contract, so a full release
  applies it); deploy maild with apid, then turn the gate on for both.
