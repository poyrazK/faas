# ADR-20261022193512 · Unified incidents

- **Status:** proposed
- **Date:** 2026-10-22
- **Decision:** Add one account-wide incident list, `GET /v1/incidents`, and
  an incident detail, `GET /v1/incidents/{id}`. Both are read-time
  projections over the places Gregale already records trouble: alert rules,
  route production monitors, and the public status page. The one new record
  is the alert episode: a firing alert rule now opens an episode and its
  recovery closes it, so an alert has a start, an end and a history instead
  of only a current state. Dark behind `FAAS_INCIDENTS_ENABLED`.
- **Why:** "Is anything broken, since when, and why?" takes three surfaces
  today. Alert rules show only their current state and the deliveries they
  sent, and a sustained breach sends a delivery every cool-down, so neither
  says when trouble started or ended. Route monitor incidents live under
  each app's routes. Platform incidents are on the status page. A customer
  paged at 03:00 needs one list with the open items first, and for each one
  the evidence the platform already has: what fired, what changed, and what
  the logs said.
- **Consequences:**
  - Sources, each read with its own bounded query and mapped to one shape
    (`id`, `source`, `status` open or resolved, `title`, `app`,
    `opened_at`, `resolved_at`):

    | Source | Store | Open while |
    | --- | --- | --- |
    | `alert` | `alert_episodes` (new) joined to `alert_rules` | the episode has no `resolved_at` |
    | `route_monitor` | `route_monitor_incidents`, metadata columns only | `status = 'open'` |
    | `platform` | public status events of kind `incident` | lifecycle is not `resolved` |

    Ids carry their source (`alert-<uuid>`, `route-<uuid>`,
    `platform-<public id>`), so a detail read goes straight to one store.
  - Alert episodes: `SetAlertRuleState`, which only meterd's evaluator
    calls, records them in the same statement as the state change. A change
    to `firing` opens an episode unless one is open; a change to `ok` closes
    the open one. `degraded` and `unknown` leave it as it is, because the
    rule could not tell. One open episode per rule is a unique index, so
    concurrent evaluators cannot open two. Every branch that sets a rule
    firing (a won fire claim, a breach inside the cool-down, a restored
    degraded read) opens the episode the same way, without new calls in the
    evaluator. Episodes are recorded whether or not the flag is on, so
    history exists when it is turned on.
  - Bounds in `pkg/api/limits.go`: a rule keeps its newest 200 episodes
    (opening a new one deletes the oldest resolved ones beyond that), so a
    flapping rule cannot grow the table without bound. The list covers
    resolved incidents from the last 7 days by default, at most 30, plus
    everything still open, capped at 100 entries with `truncated`. Each
    source reads one row past the cap so no source starves the others.
  - A failed source is listed in `unavailable_sources` and the response
    stays 200, as ADR-751 does for the change timeline.
  - The detail adds, for an app incident, the app's change timeline
    (ADR-751) from an hour before it opened until it resolved (or now),
    capped at 7 days; for an alert episode, the fires it sent and the log
    context the newest fire carried (ADR-20261022172052). The change
    timeline keeps its own flag; while it is off, `changes` is listed in
    `unavailable_sources`.
  - A rule deleted while firing takes its episodes with it, as it already
    takes its deliveries. A rule disabled while firing keeps its episode
    open, matching the rule's own `firing` state, until it is re-enabled
    and evaluates healthy.
  - Platform incidents are public status page events; every account sees
    the same ones.
  - Authorization: `ScopesReadSurface`, account-scoped, every plan.
  - Slices: (1) episodes, the list and detail API, CLI; (2) acknowledging
    and notes, and the dashboard page.
  - Slice 2, notes: `incident_notes` holds an account's notes on an
    incident. An acknowledgement is a note of kind `acknowledged` with no
    body, at most one per account and incident (a unique index), so a
    second acknowledgement returns the first. A row keys the incident by its
    API id and by exactly one foreign key to the source row
    (`alert_episodes.id`, `route_monitor_incidents.id` or
    `status_incidents.public_id`, each `ON DELETE CASCADE`), and a CHECK
    ties the two together; notes go with their incident when a rule is
    deleted, an episode is trimmed or a route incident is pruned. Platform
    incidents are shared, but each account's notes on them are its own.
  - Notes are append-only: a correction is another note, as on the status
    page. Bounds in `pkg/api/limits.go`: a body is at most 2,000
    characters, and an incident holds at most 100 notes per account,
    acknowledgement included; the detail returns them all, oldest first.
    The author is the caller as release approvals record it: the signed-in
    email, or `api_key:<id>`.
  - `POST /v1/incidents/{id}/acknowledge` answers 201 with the new
    acknowledgement or 200 with the existing one; a resolved incident
    answers 409 `incident_resolved`, because there is nothing left to take
    on. `POST /v1/incidents/{id}/notes` answers 201 on open and resolved
    incidents alike; a resolved incident is where follow-up notes go. Both
    need a write scope (`ScopesDeployWriteSurface`, as dashboards). List
    entries carry `acknowledged_at` and `acknowledged_by`, read with one
    query for the page; the detail carries `notes`.
  - The dashboard's `/dashboard/incidents` lists incidents as the API
    does, and `/dashboard/incidents/{id}` shows the evidence with an
    Acknowledge button and a note form. Both forms are CSRF-checked and go
    through the JSON handlers, so validation and bounds are the API's.
- **Rejected alternatives:**
  - *An `incidents` table every producer writes.* Duplicates three stores
    and adds a write to each producer for what is a read; only the alert
    episode does not exist anywhere yet.
  - *Episodes derived from deliveries at read time.* Deliveries are fires,
    not recoveries; without a recorded resolve, two breaches an hour apart
    and one long breach that fired twice look the same.
  - *Episodes from the audit log's `alert.resolved` events.* The audit log
    has no app key and is not bounded per rule (ADR-751 rejected it for the
    same reason).
  - *Acknowledging silences the alert's notifications.* meterd's evaluator
    would read an apid table on every fire, and a rule's cool-down and
    `enabled` already set how often it notifies. An acknowledgement says
    who is on it; it does not change what the rule sends.
  - *Editable or deletable notes.* An incident's record is only useful if
    it is what people wrote at the time.
  - *Grouping all of an app's firing rules into one incident.* Which rules
    belong together is a judgement; slice 2's notes and acknowledgements
    are where people make it, and the list already sorts an app's open
    items together.
