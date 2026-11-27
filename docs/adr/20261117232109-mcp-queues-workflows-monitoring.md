# ADR-20261117232109: Agents read queues, recover workflows and manage monitoring

Date: 2026-11-17
Status: Accepted
Amends: ADR-20261117215858 (renames its `postgres:query` scope to `data:read`),
ADR-20261104140058 (human approval for risky agent actions)

## Context

After ADR-20261117215858 the next gaps in the management MCP server were
asynchronous work and monitoring. An agent debugging "orders stopped
shipping" could not see a queue backlog, the dead-letter queue or a failed
workflow step, and could not replay or retry once it found the cause. It
could not see which alerts fired, what an SLO's error budget was, or record
what it did on an incident. The APIs exist (`/v1/apps/{slug}/dlq`,
`/v1/workflows/runs/{id}/...`, alerts, SLOs, synthetics, `/v1/incidents`);
MCP has none of them.

The owner chose, on 2026-11-17: reads plus reviewed DLQ replay (no purge);
reads plus plan/apply workflow recovery; reads plus incident acknowledge and
notes plus plan/apply for creating and editing synthetics, SLOs and alert
rules (deletes stay with people).

## Decision

### 1. One consent for customer data: `data:read`

Dead-letter payloads and headers, and workflow run inputs and outputs, are
customer data like database rows. ADR-20261117215858's `postgres:query`
(unreleased) becomes `data:read`, "Read my app data": SQL rows, message
bodies and workflow payloads. Without it the tools return everything else
(IDs, sources, error kinds, retry counts, timestamps, step names, errors) and
replace each body with `{"redacted": "data:read consent required", "bytes": N}`.

### 2. Queues and dead letters

Reads (toolset `diagnose`):
- `gregale_get_queue_state(app)`: depth, in-flight, oldest pending age, plan
  cap and backlog by source.
- `gregale_list_dead_letters(app, limit, before)`: the unified app DLQ, newest
  first, bodies per §1.

Writes (toolset `operate`): `gregale_plan_dead_letter_replay(app, event_ids |
all + limit, reason)` reviews replaying up to 50 named events or the oldest
`limit` (at most 200, apid's replay limit) and lists what it would replay; `gregale_apply_operation`
replays them. Each call carries an idempotency key derived from the plan and
the event, so a retried apply replays nothing twice. No purge tool.

### 3. Workflow runs

Reads (`diagnose`): `gregale_list_workflow_runs(app, status, workflow,
limit, offset)`, `gregale_get_workflow_run(app, run_id)` (run, steps and
diagnostics, checked to belong to the app; payloads per §1) and
`gregale_list_workflow_attention(app)`.

Writes (`operate`): `gregale_plan_workflow_recovery(app, action, ...)` with
action `retry_step` (run and step), `resume` (pins the run's resume count, so
a concurrent resume makes apply fail instead of resuming twice), `cancel`
(one run) or `cancel_queued` (up to 50 runs that have not started). The plan
shows the run's status and failing step. Applied with `gregale_apply_operation`.

### 4. Monitoring and incidents

Reads (`diagnose`): `gregale_list_alerts(app)`, `gregale_get_slo_status(app,
window)` (SLOs with burn and the app's request SLO), `gregale_list_synthetics(app)`
(with recent results), `gregale_list_incidents(app, open_only)`,
`gregale_get_incident(id)` (its app must be in consent).

Writes (`operate`, write consent):
- `gregale_acknowledge_incident(id)` and `gregale_add_incident_note(id,
  body)` act at once: they change no app behavior, and the incident timeline
  records the agent as the actor.
- `gregale_plan_monitoring_change(app, change)` reviews one of: create an SLO;
  create a synthetic check; enable or disable a synthetic check; create an
  alert rule; change an alert rule's name, threshold, comparison, window,
  channels, cooldown or enabled state. Applied with `gregale_apply_operation`.

Alert rules made by agents notify only: the tool accepts existing
notification channel IDs and never a webhook URL, webhook secret or
`action` (such as rollback), so an agent cannot send alert data to a new
destination or make an alert act on production. Deletes of alerts, SLOs and
synthetics stay with people (the CLI and dashboard).

### 5. Approval

New production writes in `pkg/agentapproval`:
- `POST /v1/apps/{}/dlq/{}/replay`, `POST /v1/apps/{}/dlq:replay_all` and
  `POST /v1/apps/{}/queues/dead_letter/{}/replay`: a replay runs the app's
  code on old input (`TargetApp`).
- `POST /v1/apps/{}/workflows/runs:cancel-queued` (`TargetApp`).
- `POST /v1/workflows/runs/{}/cancel`, `/resume` and `/steps/{}/retry`. The
  route names only the run, so these ask on every environment (`TargetNone`).

Incident notes and acknowledgements and monitoring configuration are not
gated.

## Consequences

- Agent keys replaying dead letters or recovering workflows with the CLI on
  production now ask a person first.
- Connections granted `data:read` see message and workflow payloads as well as
  database rows; the connect page says so.
- Workflow recovery on preview apps asks too, until approval can resolve a run
  to its app.

## Alternatives considered

- **Separate `queues:read-payloads` consent.** One more checkbox for the same
  kind of exposure; one data consent is easier to understand and to deny.
- **Direct replay and retry tools.** Faster, but a replay re-runs side effects
  (charges, emails); the owner chose review.
- **Let agents add alert webhooks.** Lets an agent exfiltrate alert payloads to
  any URL; existing channels cover notification.
