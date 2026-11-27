# ADR-20261118161500: Agents manage event subscriptions, API consumers and notify-only budgets, and read the audit trail

Date: 2026-11-18
Status: Accepted
Amends: ADR-20261104140058 (human approval for risky agent actions)

## Context

After ADR-20261118140000 the next gaps in the management MCP server were
event subscriptions, the audit trail, spend and API consumers. An agent
debugging "webhooks stopped" cannot see a subscription's backlog or replay
the events it missed; one asked "who changed this yesterday?" cannot read the
audit log; one asked "are we on track this month?" cannot read the forecast
or set a budget alert; one onboarding a customer of the app cannot register
that API consumer. The APIs exist (`/v1/apps/{slug}/event-subscriptions`,
`/v1/apps/{slug}/event-recoveries`, `/v1/audit-log`, `/v1/audit-events`,
`/v1/billing/forecast`, `/v1/billing/budgets`, `/v1/apps/{slug}/consumers`).

The owner chose all four areas on 2026-11-18, with plan/apply for writes.

## Decision

### 1. Event subscriptions

Reads (toolset `diagnose`): `gregale_list_event_subscriptions`,
`gregale_get_event_subscription_health` (consumer and delivery health over
5m to 24h), `gregale_list_event_deliveries` (metadata only, no payloads) and
`gregale_list_event_recoveries`.

`gregale_plan_event_change` (toolset `operate`) reviews `pause`, `resume`
(optional rate), `recover` and `cancel_recovery`. A recovery body is the
native request, decoded strictly; the agent cannot set `parent_job_id` or
`request_id`. The plan runs the native preview and shows the matched count;
a recovery matching nothing is refused. Apply sets `request_id` to the plan's
operation key, so a retried apply starts one job. Subscriptions and recovery
jobs of another app are refused.

### 2. Audit trail

`gregale_list_audit_events` (toolset `diagnose`) reads the account audit log
(`/v1/audit-log`, cursor-paged) on a connection consented to every app. A
connection limited to some apps must name one, and reads that app's events
(`/v1/audit-events?app_id=`) only. The agent's own trail stays
`gregale_agent_activity`.

### 3. Spend and budgets

These tools need "Read usage and billing" (`usage:read`) and are not listed
without it. `gregale_get_spend_forecast` and the account-scope budgets need a
connection consented to every app; a limited connection sees only budgets
scoped to its apps.

`gregale_plan_budget_change` reviews creating or updating a budget that only
notifies: mode `monitored`, action `notify`. An agent cannot create a budget
that rejects traffic, suspends work or stops previews, and cannot update an
existing enforcing budget. Updates carry the budget's revision. Budgets are
account objects, not app operations, so they have their own in-memory plans
and `gregale_apply_budget_change`, like flag plans. Budget routes are billing
routes: apid asks a person before every apply.

### 4. API consumers

Reads (toolset `operate`): `gregale_list_api_consumers` and
`gregale_get_api_consumer` (usage, spend cap, plan assignments; key values are
never returned). `gregale_plan_api_consumer_change` reviews `create`,
`revoke`, `set_spend_cap` and `assign_plan`. Issuing keys stays with people:
a key route mints a credential.

### 5. Approval

New entries in `pkg/agentapproval`:
- production (`TargetApp`): `POST /v1/apps/{}/event-subscriptions/{}/delivery-control/pause`
  (stops processing) and `POST /v1/apps/{}/event-recoveries` (re-runs app
  code on old events);
- billing: `PUT /v1/apps/{}/consumers/{}/spend-cap`.

Consumer revocation (`DELETE`), plan assignment and budgets were already
gated. Resuming a subscription, cancelling a recovery and creating a consumer
are not.

## Consequences

- Agent keys pausing production subscriptions, starting production event
  recoveries or setting consumer spend caps with the CLI now ask a person.
- Agents can add spend alerts but never spend enforcement.

## Alternatives considered

- **Let agents create enforcing budgets.** A wrong `reject_traffic` budget
  takes production down at the limit; notification covers the agent's job.
- **Filter the account audit log by app client-side.** Audit rows do not all
  carry an app; filtering would silently drop or leak rows. The per-app read
  uses the server's filter.
- **Issue consumer keys from MCP.** The key value would pass through the
  agent's context; people issue keys.
