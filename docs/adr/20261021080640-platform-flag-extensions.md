# ADR 20261021080640 · Platform flags: company targeting, full coverage, scheduled and automatic rollouts, self-serve previews

- **Status:** accepted
- **Date:** 2026-10-21
- **Relates to:** ADR-1700 (platform flags), ADR-377 (customer Flags and its rollout auto-advancer), the #4882 organization model (company resources and personal organizations)
- **Decision:** Extend platform flags in four ways. Each lands as its own pull request, in this order.

## 1. Company targeting

- A rule gains `orgs`: organization IDs, AND-combined with `accounts`, `group`, `plans` and `rollout` like the others. Limit: `PlatformFlagsMaxOrgsPerRule` (1000).
- The evaluated subject gains `OrgID`: the organization the request acts under.
  - apid takes it from request context with no I/O. Sources, in order:
    1. the active-org principal (`authz.ActiveOrgID`, `/v1/orgs/{slug}/…`, `X-Active-Org`);
    2. the project, app, Postgres, object and queue access grants;
    3. an app the handler already loaded (`App.OrgID`);
    4. the API key's organization (`APIKey.OrgID`).
  - A request with none of these has no organization, and org rules never match it. That is a person acting outside a company, and `accounts` already targets a person.
  - Personal organizations are not resolved, because that costs a store call per decision.
- Each catalog entry declares which subject attributes its consumers can supply: `account` always, `plan`, `org`.
  - A write whose rules use an attribute the flag cannot evaluate is refused with `409`, instead of the rule silently never matching. For example:
    - `ai_gateway` refuses org rules, because outboundd does not know the calling app's organization;
    - `live_queries` refuses plan and org rules, because the realtime path knows only the account.
- The inspector (`…/decision?account_id=&org_id=`) and `gregalectl flags allow|deny|revoke --org-id` cover organizations.

## 2. Full feature coverage

Every customer-facing release gate moves onto a flag, following the two kinds of check:

- **Per-request checks** (a handler with an account in scope) decide per account and organization.
- **Boot and worker checks** cannot decide per account. Schedd's workflow dispatch, apid's execution worker and realtime fan-out workers read the flag's kill switch (`GloballyEnabled`) where they already run a runtime-config watcher. Process capabilities stay environment variables: a listener, a receiver, or a daemon allowed to start (gatewayd's dev-bridge forwarders, vmmd's trace receiver, bridged). The flag then only releases the feature per account in apid.

Realtimed has no database pool. Rather than give it one, apid decides the realtime preview flags for each endpoint's account. It sends the decisions with the endpoint configuration it already syncs to realtimed, and resyncs affected endpoints when a realtime flag changes.

**Covered so far:** `operation_schedules`, `operation_children`, `service_map`, `guest_tracing` and `static_egress_ip`. All five keep their environment gate until an operator writes the flag. `guest_tracing` decides by account and plan only, because creating an app has no organization yet. Both operation flags decide by account only, because the schedule firer and the cancellation cascade know nothing else.

**Realtime previews:** `realtime_resume`, `realtime_tickets`, `realtime_access` and `realtime_occupancy`.
- apid gates its own API surfaces for them per request. It sends each endpoint's decisions in the registration's `previews` field, but only once an operator has written one of these flags, so older realtime nodes, which refuse unknown fields, keep accepting registrations.
- A realtime node keeps its environment switch as a capability, so a preview is on only when the node enables it and the decision allows it.
- Upgrade realtime nodes before writing these flags.

**Pending, with the product-area gates:** `FAAS_OPERATION_CONCURRENCY_ACCOUNTS` (ADR-20261018210500) and `FAAS_OPERATION_APPROVAL_INPUTS_ACCOUNTS` (ADR-20261019001500). Both are enforced inside the state layer as well as in apid, so a flag needs a release check injected into the store rather than a handler change.

**Not migrated:**

- enforcement and infrastructure toggles:
  - `FAAS_API_CONTRACT_DIFF_ENABLED` gates promotions and rollbacks in apid *and* imaged, so a per-account decision in apid alone would enforce inconsistently;
  - `FAAS_CUSTOM_METRIC_HISTORY_ENABLED` registers a fleet-wide Prometheus exporter at boot;
- gates bound to native qualification: `FAAS_REHEARSAL_*`, `FAAS_CI_*`, `FAAS_DATA_REPAIR_REHEARSAL_DISPATCH`;
- internal services: `FAAS_INTERNAL_MAIL_*`, `FAAS_PRODUCT_CONTROL_ENABLED`;
- retained realtime messages (`FAAS_REALTIME_RETAINED_PREVIEW_ENABLED`): a storage preview checked by about forty handlers and background workers, many with no account in hand. It stays an environment kill switch;
- fleet infrastructure: `FAAS_REALTIME_CHANNEL_ROUTING_ENABLED`, the private-network fabric, and the telemetry kill switches.

## 3. Scheduled rules, automatic ramps and retire-by reminders

- **Schedules.** A rule gains optional `starts_at` and `ends_at` (RFC 3339, UTC). Outside the window it does not match, and evaluation falls through to the next rule. The inspector reports `rule_not_started` and `rule_ended`.
  - Evaluation reads the clock once per decision, so the snapshot stays lock-free and needs no recompile at the boundary.
- **Automatic ramps.** A rule with a `rollout` can carry a `ramp`:
  - **Fields:**
    - ascending `stages` in basis points (2 to 10 stages);
    - `interval_seconds` (≥ 300);
    - `max_5xx_basis_points`;
    - `min_requests`;
    - `on_breach`: `pause` or `rollback`;
    - `paused` and `paused_reason`, which the worker writes.
  - **Health source.** The rule's `rollout` is the current stage. Health comes from the routes the catalog lists for the flag (`HealthRoutes`), using apid's existing `apid_request_total` series over the last interval (`code="err"` counts 5xx), through apid's PromQL client. The series are not per cohort, so a ramp judges the routes' overall health. A flag without health routes refuses a ramp. With no Prometheus configured, ramps hold and the inspector says so.
  - **The worker.** Every apid replica runs it once a minute, as the customer Flags auto-advancer does. A stage moves only through a write that expects the flag's current version, so exactly one replica advances it; the others see a conflict and stop. That makes an advisory lock unnecessary. The stage began when the flag's current version was written (its revision time), so any operator write restarts the interval.
    - **When it acts:** for every unpaused ramp whose definition version is at least `interval_seconds` old.
    - **Holds:** with too few requests it does nothing.
    - **Breach:** it pauses, and on `rollback` also returns `rollout` to the previous stage.
    - **Otherwise:** it advances one stage.
    - **How it writes:** through the runtime-config store with the expected version, so a conflict with an operator write is a no-op. The row records no actor and a reason naming the stage. The audit event is `platform_flag.ramp_*` with actor `system:platform-flags-ramp`. Each write is applied and notified like an operator write.
    - **Leaving a ramp:** a paused ramp resumes only when an operator clears `paused`.
- **Retire-by reminders.**
  - Every preview flag carries `RetireBy`.
  - apid exports `apid_platform_flag_retire_by_timestamp_seconds{flag}`, and `FaasPlatformFlagPastRetireBy` warns when it has passed.
  - `gregalectl flags list|show` prints the date and flags it as overdue.

## 4. Self-serve previews

- **Operator side.** A definition gains `self_serve`: `enabled`, eligible `plans` (empty means all), and `max_subjects` (≤ 5000).
- **Customer side.**
  - Customers list the previews they may join and opt in or out:
    - `GET /v1/previews`
    - `PUT /v1/previews/{key}`
    - `DELETE /v1/previews/{key}`
    - `gregale previews list|join|leave`
  - An organization owner or admin can do the same for a company under `/v1/orgs/{slug}/previews`, beside the other organization settings. A new org action, `org.manage_previews` (owner and admin), gates it; listing needs `org.view`.
  - A flag decided where the organization is unknown takes only account opt-ins.
- **Storage.** Opt-ins are their own hot runtime-config row per flag, `platform_flag_opt_ins.<key>` (`{accounts, orgs}`).
  - Every consumer receives them through the existing watcher, with receipts, history and rollback, and without a new table or delivery path.
  - apid writes the row on the customer's behalf with the expected version, retrying on conflict. The audit records the customer as the actor.
- **Evaluation order:** kill switch, then rules (first match), then opt-in (eligible plan and opted-in account or organization), then `default`.
  - An operator excludes an account by putting a `value: false` rule first.
  - Turning `self_serve` off stops new opt-ins and ignores existing ones without deleting them.
- **Entitlement.** A preview never grants a plan entitlement. A preview tied to a paid capability lists only entitled plans; a definition that offers it to another plan is refused.
- **Eligibility** is checked against the deciding account's plan, at join time and at every decision, for account and organization opt-ins alike.

## Why

Operators asked to control which accounts and companies use which feature, for every feature, with scheduled and automatic rollouts, and to let customers join previews themselves.

- **Companies.** A company today can only be targeted person by person.
- **Coverage.** Most customer-facing gates are still restart-only environment variables.
- **Rollouts.** A staged rollout needs an operator at every step and nothing reminds anyone to retire a preview.

## Consequences

- **Validation by attribute.** A rule is accepted only when every daemon that evaluates the flag can supply the attributes it uses, so no rule silently never matches.
- **First non-human runtime-config writer.** Its writes carry no actor, an explanatory reason, and a `system:` audit actor.
- **Customer writes.** Customers can write runtime configuration, but only the opt-in row of a self-serve flag, only their own subject, and within `max_subjects`.
- **New limits** in `pkg/api/limits.go`:
  - orgs per rule (1000);
  - ramp stages (2 to 10);
  - minimum ramp interval (300 s);
  - opt-in subjects per flag (5000).

## Rejected alternatives

- **Resolve the personal organization on every decision.** It costs one store call per check and adds nothing over account rules.
- **Store opt-ins in a new table loaded by every watcher.** That needs a second delivery path and new receipts. The per-flag row reuses both.
- **Reuse the customer Flags auto-advancer code.** It is tied to per-environment flag versions and per-request flag evidence, and it never pauses. Its patterns are reused (a window since the last version, conflict as a no-op, a system audit actor).
- **Give realtimed a database pool for flags.** apid already syncs endpoint configuration to it, and the decision can travel with that.
