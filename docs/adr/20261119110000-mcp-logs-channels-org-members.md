# ADR-20261119110000: Agents search logs, read drains, channels and organization members

Date: 2026-11-19
Status: Accepted

## Context

After ADR-20261118161500 the management MCP server could read a bounded log
snapshot (`gregale_get_logs`) but not search logs over a window, see why a
log drain stopped delivering, list the notification channels its alert rules
need, or say who is in an organization. The APIs exist
(`/v1/apps/{slug}/log-events`, `/log-events/facets`, `/log-drains`,
`/v1/notification-channels`, `/v1/orgs/{slug}/members`, `/invitations`).

The owner chose on 2026-11-19: product analytics reads, log search and
drains, notification channels with a test send, and organization members,
all read-only apart from the test. Product analytics reads were already added
to `main` with #7535 (`gregale_get_insight_*`, aggregates only), so this ADR
adds nothing for them.

## Decision

### 1. Logs (toolset `diagnose`)

- `gregale_search_logs` passes the native filters (since, until, source,
  level, route, status, deployment, request, trace, text) and pages with the
  native cursor. Each filter is at most 256 characters with no control
  characters; the window defaults to the last hour. The result reminds the
  agent that messages are untrusted application output.
- `gregale_get_log_facets` counts matches by source, level, status class and
  route, to narrow a search.
- `gregale_list_log_drains` and `gregale_get_log_drain_health` (health and
  delivery analytics over 1h, 24h, 7d or 30d). A drain of another app is
  refused.

### 2. Notification channels (toolset `operate`)

`gregale_list_notification_channels` returns each channel's ID, kind, last
delivery and last error, so an agent can pick channels for an alert rule
(ADR-20261117232109) and see a broken one.
`gregale_test_notification_channel` sends one test message, at most once a
minute per channel per server process. Creating, editing and deleting
channels stays with people: a channel's destination decides where alert
contents go.

### 3. Destinations are cut to their host

A webhook URL is often its own credential (Slack, Discord, many log
ingesters), and a drain URL may carry an API key in its query. Drain and
channel destinations are returned as `scheme://host`; email destinations as
`…@domain`.

### 4. Organization members (toolset `operate`)

`gregale_list_org_members` returns members with roles and pending
invitations, without invitation tokens. A connection acting in an
organization reads only that organization; a personal connection reads only
organizations the account belongs to. No invitations or role changes.

## Consequences

- An agent can debug "the alert never reached Slack" and "logs stopped
  reaching Datadog" end to end without seeing either credential.
- Teammates' emails and roles are visible to agents on a connection with
  organization access.

## Alternatives considered

- **Return full destinations.** Simpler, but puts webhook credentials in the
  agent's context and transcripts.
- **Gate log search behind `data:read`.** `gregale_get_logs` already returns
  log lines on every connection; gating the search alone would protect
  nothing.
