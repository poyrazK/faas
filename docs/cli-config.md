# CLI configuration

`gregale config` stores non-secret preferences for the local CLI. It does not
store API tokens; login credentials remain in the OS keychain (or the existing
restricted fallback file).

```bash
gregale config list
gregale config get api-base
gregale config set api-base https://api.gregale.dev
gregale config set json true
```

The file is written atomically at `$XDG_CONFIG_HOME/gregale/config.json` (or
the platform user-config directory) with mode `0600`. Supported settings are:

| Key | Values | Default |
|---|---|---|
| `api-base` | an `http://` or `https://` URL (without embedded credentials) | `https://api.gregale.dev` |
| `json` | `true`, `false`, `on`, `off`, `yes`, `no`, `1`, `0` | `false` |

Environment variables take precedence over the file: `FAAS_API` overrides
`api-base`, and `FAAS_JSON` overrides `json`. Use `gregale config list --json`
to see the effective value and its source without exposing credentials.

## Command output and forwarded arguments

Put the global `--json` (or `-j`) flag before a command's `--` separator.
Arguments after the separator belong to the app command, including flags such
as `--help` and `--json`:

```bash
gregale --json app demo exec --detach -- python --help
```

`gregale invocations wait <id> --json` writes the invocation status to stdout
and API errors as a single Problem JSON object to stderr. A wait timeout exits
with `124`, writes the last known status to stdout when available, and writes
an `invocation_wait_timeout` Problem to stderr. Ctrl-C exits with `130`.
Stopping the CLI wait leaves the invocation running; inspect it later with
`gregale invocations get <id>`.

## Connection profiles

Use a named connection to keep API endpoints and login credentials separate:

```bash
gregale profile add staging https://staging.example.com
gregale --profile staging login
gregale profile use staging
gregale context
gregale --profile default apps
gregale profile list --json
gregale profile use default
gregale profile remove staging
```

`profile add <name> <api-url>` creates an inactive connection. Names contain
1–64 lowercase letters, digits, underscores, or hyphens. `default` is reserved:
it uses your existing API setting, keychain entry, and fallback token file.
Upgrading does not move or replace existing credentials.

`profile use <name>` saves the active connection. The prefix option
`gregale --profile <name> <command>` selects a connection for that command only.
Put it **before the command**: some commands have their own `--profile` option
for resource or test settings, and those options retain their meaning.
Arguments after `--` are forwarded unchanged.

`FAAS_API` and `FAAS_TOKEN` still override the selected connection's endpoint
and credential. JSON preferences remain shared across connections.
`gregale config set api-base <url>` updates the selected connection; changing
its endpoint does not clear its stored credential, so log in again when changing
to a different server. `gregale context` reports the effective profile and API
URL, including where each came from, even outside a linked checkout.

Named connections use separate OS keychain accounts. On headless hosts,
tokens fall back to restricted files under
`$XDG_CONFIG_HOME/gregale/profiles/<name>/token`. Managed login session
metadata lives beside the token as `session.json`, so logging in or out of one
connection preserves the other connections' revocation information. The default
connection retains its existing `gregale/session.json` path. Tokens are never
stored in `config.json` or printed by `profile list` or `context`.
Completion caches are isolated by connection, endpoint, and credential.
`FAAS_COMPLETION_CACHE_PATH` remains an explicit shared cache override.
Regenerate installed shell completion scripts after upgrading to support the
connection prefix option and configured names for `--profile`, `profile use`,
and `profile remove`.

`profile remove <name>` removes an inactive connection and clears its locally
stored credentials, session metadata, and completion caches. Switch away from a
connection before removing it. Removal does not revoke a server-side session; use
`gregale --profile <name> logout` first when revocation is needed.

### Check a connection

```bash
gregale profile check
gregale --profile staging profile check --timeout 5s --json
```

`profile check` makes one read-only `GET /v1/account` request using the selected
endpoint and credential. It reports the profile, setting sources, endpoint
reachability, authentication result, and account ID, email, plan, and status.
It explains `FAAS_API` and `FAAS_TOKEN` overrides without printing credentials
or raw server error details. It does not change saved settings or credentials.

The default timeout is `10s`; `--timeout` accepts a positive Go duration.
Diagnostics distinguish missing, rejected, expired, and revoked credentials,
permission failures, DNS/TLS/connection errors, and unexpected API responses.
The API must report an expiry or revocation code for that distinction to be
available. Ctrl-C cancels the request.

With `--json`, completed checks write one report to stdout, including failures.
`endpoint_reachable` is `null` when reachability could not be determined.
Argument and configuration errors use the normal stderr error output.

| Exit code | Meaning |
|---|---|
| `0` | Connection and authentication succeeded |
| `1` | Invalid arguments/configuration, API error, or unexpected response |
| `2` | Missing/invalid credential or account access denied |
| `3` | DNS, TLS, or network connection failure |
| `124` | Timeout |
| `130` | Interrupted |

## Shared command error diagnostics

`login`, `apps`, and `deploy` use the shared CLI error renderer. Authentication
hints name the selected profile and explain when `FAAS_TOKEN` overrides its
stored credential. An explicitly supplied login token gets a token replacement
hint. Permission, rate-limit, and server errors receive recovery hints when the
API has not supplied one. Existing server codes and specific hints are retained.

Network diagnostics distinguish DNS, TLS, timeout, and cancellation problems
while retaining the stable `transport_error` code. Before retrying a failed or
interrupted deploy, inspect its status: the server may have accepted the request.

With `--json`, shared command errors write one Problem object to stderr; local
workspace hints are included in its `hint` field. Known credentials, bearer
credentials, and Gregale API key strings are redacted, including nested error
fields. This protects recognized credentials, not arbitrary secrets in user
messages. Successful command output remains on stdout.

Shared command errors use these exit codes. JSON error objects on stderr
include `category` and `exit_code` alongside the server's unchanged `code`:

| Exit code | JSON category | Meaning |
|---|---|---|
| `0` | — | Success |
| `1` | `invalid_request` | Invalid arguments, local validation/filesystem failures, or other API rejection |
| `2` | `authentication` | Missing credential or API 401; log in again |
| `3` | `temporary_failure` | Network/transport failure, timeout, API 408/429, or API 5xx |
| `4` | `not_found` | API 404/410; resource missing or gone |
| `5` | `conflict` | API 409/412; inspect current state before retrying |
| `6` | `permission_denied` | API 403; check access or plan requirements |
| `130` | `cancelled` | Cancelled request, interrupted deployment, or declined app/deployment cleanup confirmation |

These mappings apply to shared errors, including deployment and destructive
commands. Missing an explicit confirmation flag is an invalid request (`1`);
declining a displayed destructive confirmation is cancellation (`130`).
The additional codes replace earlier generic `1` results for these HTTP statuses
and `3` for request cancellation, so scripts matching exact numbers should update.
A temporary failure does not guarantee that retrying a mutation is safe: inspect
deployment status and reuse the original idempotency key before retrying.

Commands with dedicated wait or diagnostic contracts, such as `profile check`
and `invocations wait`, retain their documented exit codes. Direct command
failures outside shared error handling may also retain dedicated contracts.

## Non-interactive automation

Put the global `--non-interactive` option **before the command**. It disables
interactive prompts and browser launches even when stdin is a terminal.
Combine it with `--json` to keep supported command results on stdout and
Problem errors on stderr:

```bash
printf '%s' "$CI_GREGALE_TOKEN" | gregale --non-interactive --json login --token-stdin
gregale --non-interactive --profile staging --json deploy --image registry.example/app@sha256:HASH --name my-api
gregale --non-interactive --json apps --yes my-api
gregale --non-interactive --json account delete --yes
gregale --non-interactive --json triggers delete TRIGGER_ID --yes
```

`--non-interactive` does not imply JSON or approval. It must be in the prefix;
command-local options and arguments after `--` retain their meanings.
`--non-interactive=false` restores interactive behavior for that invocation.
Mode state is scoped to each command invocation.

Login requires `--token` or `--token-stdin`; it fails before opening a browser
or starting device-code authentication when neither is supplied. Explicit
stdin options remain available: the caller must supply their documented input
and close the stream. Interactive secret input fails with guidance to supply
an explicit secret or stdin option. `signup` accepts `--email-only` or
`--password-stdin`; `start` requires an interactive session.

Commands that normally ask for confirmation require explicit approval:

| Operation | Automation confirmation |
|---|---|
| App deletion | `--yes`, `--quiet`, or `-q` |
| Account deletion | `--yes`, `--quiet`, or `-q` |
| Trigger / edge-rule deletion | `--yes` or `--quiet` |
| Organization deletion | `-q` |
| Project deploy / environment changes | `--yes` |
| Deployment history cleanup | `--force` (`--dry-run` needs no approval) |
| Subscription cancellation / plan downgrade | `--yes` |

Missing required input or approval exits `1` with an error; mode selection does
not supply implicit consent. Existing commands that do not prompt retain their
confirmation contracts. Deploy preflight and shared progress messages move to
stderr in this mode. Human result output remains human unless `--json` is also
selected; commands with dedicated output formats retain those formats.
Optional browser opening is suppressed and the command's existing URL/error
handling is used. Regenerate installed shell completions to recognize the flag.

## Preview deletion and cleanup

```bash
gregale --non-interactive --json apps --dry-run my-api
gregale --non-interactive --json deploys clear DEPLOYMENT_ID --dry-run
gregale --non-interactive --json deploys clear-obsolete --app my-api --older-than 168h --dry-run
```

These previews use GET requests only, never delete resources or submit cleanup,
and never prompt. `--dry-run` takes precedence over `--yes`, `--quiet`, or
`--force`. Human and JSON previews contain the same selected resource IDs,
statuses, expected effect, recovery guidance, and snapshot limitations.

App deletion lists the app and its visible deployment history. Deletion parks
the app and stops it serving; restoration is available before the server's
app-deletion grace deadline (normally seven days) through `apps restore`.
Deployment cleanup hides history while retaining audit records. The CLI has no
unhide command for cleared deployments; clearing is not a traffic rollback.
Live deployments remain protected by server policy.

Bulk cleanup previews are **candidates**, with `exact: false`: they filter
visible deployments by terminal status and creation time. The server may retain
some candidates under its retention rules, and concurrent changes can affect
selection. Plan eligibility is checked by the applying endpoint, not the local
preview. Pagination failures and invalid candidate timestamps fail the preview
rather than return an incomplete inventory. A preview is not an approval token
and does not freeze the resources for a later operation.

After review, rerun app deletion with `--yes` (or `--quiet`/`-q`), or cleanup
with `--force`. Interactive confirmation displays a preview before asking for
approval; JSON mutation calls require an explicit confirmation flag. Already
confirmed scripted calls keep their existing mutation request/output contract.

## List pagination

Deployment and invocation lists use the same pagination controls:

```sh
gregale deployments --app my-api --limit 50 --json
gregale deployments --app my-api --cursor 'CURSOR' --limit 50 --json
gregale invocations list --limit 50 --json
gregale invocations list --cursor 'CURSOR' --all --limit 100 --json
```

`--limit` sets the page size, defaulting to 50. Deployments accept 1–200;
invocations accept 1–100. `--cursor` passes the opaque continuation value from
the previous response unchanged. The existing `--before` flag remains an alias;
if both aliases are supplied, the last one takes precedence.

`--all` starts at the supplied cursor and follows every remaining page, using
the selected page size. Deployment lists previously ignored `--limit` and
`--before` with `--all`; these options are now honored. Account-wide and
app-scoped deployment lists use the same behavior.

Paged JSON retains `next_before` and adds the equivalent `next_cursor`. Both
fields are absent at the end. Deployment rows remain under `items`; invocation
rows remain under `invocations`. Invocation `--all --json` returns one envelope
with the combined rows. Deployment `--all --json` retains its existing NDJSON
format (one deployment per line; an empty list emits no lines).

All-page traversal buffers rows before printing. An API failure, cancellation,
repeated cursor, or more than 1,000 pages fails without partial list output.
Large inventories should be fetched page by page. Pagination reflects live API
results and does not guarantee a snapshot while resources are changing.

### Apps and builds

`gregale apps` (or `gregale apps ls`) returns the complete account app list in
one request; its API does not offer cursors. JSON retains its NDJSON format.
App pagination requires an API contract change before CLI cursor flags can be
supported.

Build listing uses the same guarded pagination as deployments and invocations:

```sh
gregale build list --app my-api --status succeeded --limit 50 --json
gregale build list --app my-api --cursor 'CURSOR' --all --limit 200 --json
```

Build page sizes range from 1–200, with a default of 50. `--before` remains an
alias for `--cursor`, with the last alias supplied taking precedence. Cursors
are opaque, and app/status filters are retained on every page. `--all` now
honors the selected page size and starting cursor instead of ignoring them.

Paged JSON retains `items` and `next_before`, adding `next_cursor`. Completed
traversals omit continuation fields. `--all --json` retains the existing plain
JSON array, including `[]` for no matches. Traversal errors emit no partial list;
repeated cursors and the 1,000-page ceiling stop the walk.

### Webhook deliveries and queue inspection

These read-only commands support `--limit` (1–100, default 50), `--cursor`,
and `--all`:

```sh
gregale webhooks deliveries WEBHOOK_ID --app my-api --status failed --all --json
gregale queue peek my-worker --limit 100 --cursor 'CURSOR' --json
gregale queue dead-letter my-worker --all --json
```

Webhook `--page-size` and `--page-token` remain aliases for `--limit` and
`--cursor`. Queue `--before` remains an alias for `--cursor`. The last supplied
alias takes precedence. `--all` honors the starting cursor and page size;
webhook app, subscription, and status filters are retained on every page.

JSON keeps the existing envelopes: webhook `deliveries` / `next_token`, and
queue `app_slug` / `messages` / `next_before`. Both add the equivalent
`next_cursor`, omitted when there are no further pages. `--all --json` returns
one combined envelope rather than changing to an array or NDJSON. Empty row
collections are arrays. Human output shows continuation hints even for empty
pages that carry a cursor.

The shared repeated-cursor guard and 1,000-page ceiling apply. Fetch errors
produce no partial list output. Queue inspection does not acquire leases or
acknowledge messages; lists reflect current state rather than a fixed snapshot.

### Job lists and run history

Job endpoints use offsets rather than opaque cursors:

```sh
gregale jobs list --limit 100 --offset 50 --all --json
gregale jobs runs my-job --limit 100 --offset 50 --all --json
```

Both commands accept `--limit` (1–200, default 50), `--offset` (nonnegative,
default 0), and `--all`. Traversal follows the API's `next_offset` until `-1`,
retaining the page size and job name. Repeated, backward, or invalid continuation
offsets stop an all-page walk, as does the shared 1,000-page ceiling. Fetch
failures emit no partial list.

Job-list JSON retains `jobs`, `limit`, `offset`, `next_offset`, and `total`.
For `--all`, rows are combined, `limit` is the per-page size, `offset` is the
starting offset, and `next_offset` is `-1`. `total` comes from the latest page;
it can change during traversal. Run-history JSON retains its NDJSON format,
with one run per line and no synthetic pagination record. Human output shows
continuation offsets for single pages.

Offset pagination reads current API state. Concurrent inserts or deletions can
shift page boundaries, so a complete traversal does not guarantee a snapshot.

### Event deliveries

`gregale events deliveries <app> --all` traverses invocation deliveries and
pre-invocation fanout failures independently. `--before` and `--fanout-before`
select the starting cursor for each stream; `--limit` sets each stream's page
size (1–200, default 20). App, event source/ID, and state filters are retained on
every request.

JSON retains `app_slug`, `deliveries`, `fanout_failures`, `next_before`, and
`next_fanout_before`. An all-page result combines both collections and omits
continuation fields. Each collection retains its own API order; there is no
combined chronological ordering. Empty collections are arrays. Human output
shows each continuation even if that page contains no corresponding rows.

A repeated cursor in either active stream, fetch failure, or the 1,000-request
ceiling fails without partial list output. Once a stream ends, its cursor is
frozen and later rows for it are ignored while the other continues. The API
always reads both streams, so preventing those extra server reads requires an
API change. Traversal reflects live state and does not guarantee a snapshot.

### Event fanout history

`gregale events fanout-history <app> --event-source SOURCE --event-id ID`
supports `--cursor`, `--limit` (1–200, default 20), and guarded `--all` traversal.
`--before` remains a cursor alias; the last supplied alias takes precedence.
App, event identity, and optional `--subscription-id` are retained on every page.

JSON retains the existing envelope, including `coverage`, `summaries`, event
identity, `history`, and `next_before`, and adds the equivalent `next_cursor`.
`--all` combines history rows while retaining coverage and summaries from the
latest page. Summaries describe the full filtered history on each API response;
they are not appended or added together across pages. These live values may
change during traversal. Completed walks omit both continuation fields.

Repeated cursors, fetch failures, and the 1,000-page ceiling fail without partial
list output. Human output shows continuation hints even for empty history pages.

### Schedule occurrence history

`gregale jobs occurrences <name>` and `gregale crons occurrences <id>` support
`--cursor`, `--limit` (1–200, default 50), and guarded `--all` traversal.
`--before` remains an alias for `--cursor`; the last supplied alias wins.
Each request retains the selected job or cron and page size.

JSON keeps `occurrences`, `limit`, `before`, and `next_before`, adding the
same continuation value under `next_cursor`. For `--all`, occurrence rows
are combined, `limit` remains the per-page size, `before` retains the starting
cursor echoed by the first response, and continuation fields are omitted.
Empty collections are arrays. Human continuation hints use `--cursor`.

Repeated cursors, API failures, and the 1,000-page ceiling fail without partial
list output. Pagination reflects live API state rather than a fixed snapshot.

### Project environment history and release sets

`projects environments history <project> <environment>` and
`projects environments release-sets <project> <environment>` accept `--cursor`
and `--all`. `--before` remains an alias; the last alias supplied wins.
`--limit` controls each page (1..100). History preserves `--from` and `--status`
filters across every page and includes them in its continuation command.

JSON preserves `items` and `next_before` and adds `next_cursor` when more pages
exist. Empty results use `items: []`. `--all` buffers results and stops on API
errors, repeated cursors, or the 1,000-page guard without emitting a partial
list. Traversal follows live pages rather than a fixed snapshot.

### Organization activity and delayed tasks

`orgs activity --org <slug>` and `delayed-task list --app <slug>` accept
`--cursor` and guarded `--all` traversal. `--before` remains an alias; the
last alias supplied wins. `--limit` controls each page (1..100 for activity,
1..200 for delayed tasks). Activity preserves `--kind-prefix`, `--actor-type`,
and `--app-id` on every request.

JSON keeps `items` or `tasks` and `next_before`, adds `next_cursor` for
continuation, and emits empty arrays for empty results. Human output includes
a quoted continuation command with the scope, filters, and page size, even
when the current page is empty. `--all` follows empty pages with continuation
and stops without partial output on API errors, repeated cursors, or the
1,000-page guard. Pages reflect live data rather than a fixed snapshot.
