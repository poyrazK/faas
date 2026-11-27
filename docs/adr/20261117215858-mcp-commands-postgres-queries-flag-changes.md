# ADR-20261117215858: Agents run commands, query PostgreSQL and change feature flags

Date: 2026-11-17
Status: Accepted
Amends: ADR-20261104140058 (human approval for risky agent actions),
ADR-20261103155008 (management MCP agent workflows), ADR-20261029085359
(agent keys)
Related: ADR-1037 (app tasks), ADR 20261114150000 (flag usability),
ADR-20261117070620 (app exec debug tools)

## Context

The hosted management MCP server has about 170 tools, but three things
agents ask for in almost every debugging session still send them to the CLI:

- **Running a command in the app** (a migration, a one-off script, `env`,
  a health probe from inside). `gregale app exec` exists; MCP has nothing.
- **Looking at data.** Agents can plan a database and list them, but cannot
  see its tables or run a `SELECT`, so they guess at schemas or ask the
  person to paste rows.
- **Changing feature flags.** Agents read experiment results but cannot
  create a flag, change its rollout, kill it or roll the configuration back.

The owner chose, on 2026-11-17: commands as plan → approval → apply in a fresh
task VM; schema plus read-only SQL under a new consent; full plan/apply for
flags with the kill switch as an unapproved emergency action.

## Decision

### 0. Approval errors leave a plan retryable

`gregale_apply_operation` recorded every 4xx as a final refusal, including
apid's `428 approval_required`, so after a person approved, retrying the
same plan returned the stored refusal (restart had this bug). The gate
answers before the route runs, so nothing was written: an approval error is
now returned without recording a failure, and the approved retry runs.

### 1. Run a command

Tools (toolset `operate`, write connections with `deploy:write`):

- `gregale_plan_run_command(app, command | shell, timeout_seconds,
  max_output_bytes, reason)` reviews one non-interactive command. `command`
  is an argv array; `shell` is one string run by `/bin/sh -c`. Timeout
  defaults to 120 s (at most 900 s), output to 64 KiB (at most 256 KiB).
  The plan names the live deployment the command will run on.
- `gregale_apply_operation(plan_id)` admits it through
  `POST /v1/apps/{slug}/tasks` with the plan ID as idempotency key, so a
  retried apply never starts a second command.
- `gregale_get_command_run(app, task_id, wait_seconds)` returns status, exit
  code and the stdout/stderr tails, waiting up to 45 s for a terminal state.
- `gregale_cancel_command_run(app, task_id)`. Cancelling is a `DELETE`, so
  on a production app it asks a person like every agent delete.

The command runs in a fresh single-use task VM from the live deployment
(ADR-1037), never in a serving instance. No TTY, no stdin. Only the default
scope: tasks run against the app's live deployment.

Output tails pass through `secretscan` before reaching the agent; a line with
a credential-like value is replaced by `[redacted: <rule>]`. This is a guard
against `env` dumps, not a boundary: the command itself can do anything the
deployment can.

`POST /v1/apps/{}/tasks` becomes a production write in `pkg/agentapproval`
(`TargetApp`). An agent key or hosted sign-in asks a person before it runs a
command on a production app, from MCP, the CLI or the raw API alike. Previews
and developer environments do not ask.

### 2. Query PostgreSQL (read-only)

New API, `POST /v1/postgres/databases/{id}/query`:

```json
{"sql": "select id, email from users order by id desc limit 20", "max_rows": 100}
```

returns `{columns: [{name, type}], rows: [[...]], row_count, truncated,
duration_ms}`. Values are their PostgreSQL text form (`null` stays null).

Execution, all enforced in apid and the provider, not the MCP bridge:

- A new optional provider capability, `managedpostgres.ReadOnlyQuerier`.
  The Neon provider issues the database's **agent query login**: a
  `read_only` credential with the fixed identity `gregale-agent-query`,
  so it carries exactly the privilege contract read-only bindings already
  verify (`verifyReadOnlyRole`: SELECT on the public schema, no CREATE, no
  write grants, no security-definer functions). Issuing it again for each
  query recovers the same login and reruns that verification; only apid
  holds its password, and only for the query.
- One statement, sent with the extended protocol (which refuses multiple
  statements), inside `BEGIN READ ONLY` with `statement_timeout` 5 s,
  `lock_timeout` 1 s and `idle_in_transaction_session_timeout` 10 s, then
  `ROLLBACK`.
- At most 1,000 rows (default 100) and 1 MiB of values; more sets
  `truncated`. One query per database at a time; a second gets 409.
- Backends without the capability (the Firecracker backend today) answer
  501 `postgres_query_unsupported`.
- A query on a suspended database wakes its compute and is billed as usage;
  the tool description says so.
- Audit `postgres.query_executed` with the database ID, the SHA-256 and
  length of the SQL, row count and outcome. The SQL text and rows are not
  logged.

Scope: new `data:read` (with `admin`). It is separate from
`postgres:read`, which reads database metadata only: row data is a
different kind of access. Added to the key scope vocabulary (migration
`api_keys_scopes_vocab_v5_chk`), the org agent policy capabilities, and the
connect page as "Query my databases (read-only)". Amended by ADR-20261117232109:
the scope is `data:read`, "Read my app data", and also covers message and
workflow payloads. App-bound deploy tokens
cannot carry it.

Tools (toolset `diagnose`, listed only for connections with
`data:read`). Like `gregale_list_databases`, they name an app, and the
database (ID or name) must be bound to it in the selected scope, so app
consent bounds which databases an agent reads:

- `gregale_get_postgres_schema(app, database, schema)` lists tables, views,
  columns, primary keys and indexes of one schema (default `public`), by
  running fixed catalog queries through the same endpoint.
- `gregale_query_postgres(app, database, sql, max_rows)`.

In an organization the route needs the members who may bind an app to the
database (`org.bind_postgres`): they could read its rows through that app.

Queries are reads, so they are not approval-gated. Schema-changing work
stays with migrations run as commands (§1), which are.

### 3. Change feature flags

Tools (toolset `release`, beside the flag experiment tools; writes need
`deploy:write`):

- `gregale_list_flags(project, environment)`: configuration, version and the
  lifecycle report.
- `gregale_plan_flag_change(project, environment, changes[], reason)`.
  `changes` is an ordered list of edits: `create`, `set` (default,
  description, owner, expires), `enable`, `disable`, `kill`, `revive`,
  `set_percent`, `add_rule`, `remove_rule`, `delete`. The plan applies them
  to the current configuration, pins its version, and shows each edit's
  summary plus the changed flags before and after.
- `gregale_plan_flag_rollback(project, environment, version, reason)`.
- Both apply with `gregale_apply_flag_change`. A configuration that
  changed since the plan answers `plan_stale`; the agent plans again. Flag
  plans live in the connection's memory: after a bridge restart the agent
  plans again. A retried apply whose first publish landed recognizes its own
  version (the next version holding exactly the planned configuration)
  instead of reporting it stale.
- `gregale_kill_flag(project, environment, key, reason)`: no plan. It calls
  the new `POST …/flags/{key}/kill`, which sets `killed` on the latest
  configuration (re-reading on a version race) and is idempotent.

The edit primitives move from `cmd/gregale` to `pkg/flags/edit.go`, so the CLI
and MCP apply the same rules (a disabled boolean flag serves `false`, a
progression rule is not overwritten by a percentage, and so on).

Approval: `PUT …/flags`, `POST …/flags/rollback` and
`POST …/flags/{}/rollout/promote` become production writes (`TargetNone`,
as project environments record no production flag). `POST …/flags/{}/kill`
is deliberately not gated: turning a feature off is the emergency action an
agent should be able to take alone, and it only makes the app serve the
fallback its own code chose. Reviving a killed flag is a `PUT` and asks.

## Consequences

- Agent keys that ran `gregale app exec` or changed flags with the CLI on
  production now ask a person first. `--approvals none` keys keep the old
  behavior unless their organization's floor says otherwise.
- `data:read` is a new consent. Existing connections do not have it;
  the person reconnects or mints a key to grant it.
- The query endpoint is the first API that returns customer row data to an
  API key. Its scope, audit and per-database concurrency limit are the
  controls; the read-only login is the boundary.
- Generated SDKs gain `queryManagedPostgresDatabase` and `killProjectFlag`.

## Alternatives considered

- **Run commands without a plan.** Faster, but commands are the broadest
  write an agent can make; the owner chose review.
- **Query through an app task (`psql` in the task VM).** Needs `psql` in
  every image and the app's own read-write credential; the read-only login
  is a stronger boundary.
- **Owner login with `SET ROLE`.** The owner can terminate sessions and
  read auth schemas; a `READ ONLY` transaction does not stop either.
- **Gate the kill route too.** Safer on paper, but a person approving a kill
  during an incident adds minutes to the outage the kill is meant to end.
- **Reuse `postgres:read` for queries.** Existing metadata-only keys would
  silently gain row access.
