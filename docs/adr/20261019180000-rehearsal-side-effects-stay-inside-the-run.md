# ADR-20261019180000 · Rehearsal side effects stay inside the run

- **Status:** accepted
- **Date:** 2026-10-19
- **Amends:** [ADR-20261017201003](20261017201003-rehearsal-webhook-capture.md), [ADR-20261017201005](20261017201005-rehearsal-network-containment.md)
- **Decision:** Platform-run paths treat an app enrolled in a rehearsal (it has a `scenario_test_action_policies` row) as unable to cause effects outside its run. Account-wide alerts, cross-app event fan-out, workflow outbound steps, log drains, deployment-failure mail and external broker triggers skip or refuse enrolled apps. Contained enrollment refuses apps that already hold real managed PostgreSQL or object-storage bindings.
- **Why:** The network fence covers what a contained guest can send. It does not cover work the platform performs on the guest's behalf. An inventory of every side-effect channel found real effects caused by enrolled apps:
  - Injected faults and candidate failures counted toward account-wide alert rules, which then posted to customer webhooks, Slack, PagerDuty or email.
  - Route-discovery events from candidate traffic fanned out to every other app in the account, including their event-triggered workflows.
  - Workflow outbound steps called real providers with live, customer-sealed credentials once a binding existed.
  - Log drains shipped candidate output to customer destinations.
  - Failed candidate deploys sent deployment-failure mail.
  - A candidate's Kafka, NATS, Redis Streams or SQS-compatible trigger would poll the real broker and commit offsets that production consumers own.

## Decision

The test for "enrolled" is the existence of the app's policy row. Policies are immutable and outlive the test namespace (ADR-20261017201003), so historical rehearsal rows stay excluded after the run.

| Path | Behaviour for an enrolled app | Where |
|---|---|---|
| Account-wide alert aggregates: failed invocations and deployments, `api_up`, new error fingerprints, cold-wake rate, workflow failures/age | Excluded. App-scoped rules on the enrolled app still count it. Spend and daily cost still include it, because rehearsals cost real money | `PgStore` alert queries, `WorkflowAlertSnapshot`, MemStore mirrors |
| `com.gregale.api.route.discovered` events | The route is kept in the app's own inventory and the dashboard notice still fires, but no `event.published` row is written, so nothing fans out | `appendDiscoveredRouteEventTx`, `recordDiscoveredRouteLocked` |
| Workflow outbound steps | Not authorized; the step fails with `outbound_workflow_not_authorized` | `AuthorizeWorkflowOutbound` |
| Log drains | No drain worker starts | `ListEnabledAppLogDrainsWithStandardBinding` |
| Deployment-failure mail | Not a candidate | `DeploymentFailureCandidates` |
| Kafka, NATS, Redis Streams and SQS-compatible triggers | Not listed for the poller. Queue, cron and realtime triggers stay, because they only deliver into the app | `ListEnabledTriggers` |
| Contained enrollment of an app holding a managed PostgreSQL or object-storage binding secret | Refused with `409 Managed bindings cannot be contained`. Use the isolated `postgres`/`objects` fixtures instead | `RegisterScenarioTestMembers` |

Capture-only rehearsals, which have no `network: contained`, keep the ordinary network by design (ADR-20261017201003). Even so, they get every platform-side exclusion above except the managed-binding refusal.

## Consequences

- An operator cannot alert on rehearsal failures through account-wide rules. A rehearsal reports its result through its own receipt.
- Events that a production app publishes are still delivered to an enrolled app's own subscriptions; they only change state inside that contained app. Excluding enrolled apps as recipients would need a replacement of `enqueue_event_fanout`, and is deferred.
- Managed bindings added after enrollment are not refused. A contained member cannot reach them anyway; the network fence still blocks the traffic.
- Builds of the candidate revision are not covered by this decision; see *Builds* below.
- Each exclusion is one primary-key anti-join on `scenario_test_action_policies`. No migration is needed.

## Builds (not covered; follow-up)

Builds of the candidate revision still run with ordinary builder egress.

**What builder VMs get today:**
- TCP 80/443 to any public address the guest resolved through the bridge resolver. SMTP and private ranges are blocked.
- This is the spec's v1 builder policy: "same as tenant".
- There is no per-build or per-app restriction mode. builderd does not even send an app identity to vmmd.

**What a fix needs:**
- The decision point is builderd's VM request, where the build's app is already resolved. Read rehearsal enrollment there and pass a build egress mode to vmmd.
- Enforcement goes in `Manager.Wake` for builder leases.
- Registries use rotating CDN addresses, so registry-only egress needs a name-allowlist resolver mode or the registry proxy the spec plans for v1.1.
- It also needs a platform-owned registry host list per runtime. Dockerfile builds may need to be refused or left unrestricted.

That is a separate decision. Until it lands, rehearsal receipts make no claim about effects caused by build scripts, and recipes should not depend on build-time network side effects.

## Rejected alternatives

- **Excluding every preview or dev-session app.** That changes behaviour for ordinary developer sessions that are not rehearsing.
- **Suppressing only alert delivery, not the aggregates.** A rehearsal app's traffic could then still mask a real outage in `api_up`.
- **Refusing new event subscriptions or bindings for enrolled apps.** The candidate revision's manifest legitimately declares them; what must not happen is an effect outside the run.
