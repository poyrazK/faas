# Incident response runbook

Use this runbook for any operator alert, customer report, or security signal that may affect Gregale control-plane availability, data integrity, or customer workloads. Record all timestamps in UTC and open a post-mortem when the incident is resolved.

## First 15 minutes

| Time | Incident commander (IC) | Operations lead | Scribe / communications |
|---|---|---|---|
| 0–2 min | Acknowledge the page, name an IC, and open the incident record. | Confirm the alert and check the affected service, region, and recent deploys. | Record detection time, reporter, first symptom, and incident link. |
| 2–5 min | Choose SEV1/SEV2/SEV3 using the matrix below; assign an ops lead and scribe. | Bound the blast radius using status, metrics, logs, and customer reports. | Draft the first internal update with known impact and unknowns. |
| 5–10 min | Freeze unrelated changes and choose the safest mitigation. | Roll back or contain only when the criteria below are met; preserve evidence first. | Post the approved status-page update for customer-facing impact. |
| 10–15 min | Confirm the next decision, owner, and deadline; escalate if progress is blocked. | Execute the mitigation and report measured effect. | Start the update cadence and record commands, links, and decisions. |

If no responder has acknowledged a page after five minutes, use the escalation matrix in [`docs/ops/escalation.md`](../ops/escalation.md). A SEV1 with no working IC at 15 minutes is an escalation failure: page the secondary on-call and engineering manager immediately.

## Severity decision matrix

| Severity | Trigger | Required response |
|---|---|---|
| **SEV1** | Public API or control plane is unavailable; a broad customer workload is failing; confirmed data loss/corruption; or a security incident with active exposure. | IC, ops lead, and scribe immediately. Status page within 15 minutes. Update at least every 30 minutes. Engineering manager and security/legal escalation as applicable. |
| **SEV2** | Material degradation for multiple customers, a critical subsystem is impaired, or recovery is uncertain, but the platform remains usable. | IC and ops lead. Status page when customers need to act. Update at least hourly and at every material change. |
| **SEV3** | A single customer, non-critical feature, or internal operator path is affected with a safe workaround and no evidence of wider impact. | Assign an owner, record the workaround, and update at milestones. Review in the next weekly operations cycle if it is not resolved during the shift. |

When severity is uncertain, start at the higher level and downgrade only after evidence narrows the impact. A security event follows [`docs/runbooks/breach-notification.md`](breach-notification.md) in parallel with this runbook.

## War-room protocol

1. The IC owns the incident state and makes the final operational call. Every action has one named owner and a target time.
2. Keep the war room focused on observations, hypotheses, actions, and results. Move design debates to a follow-up thread.
3. Prefer reversible containment: stop a rollout, drain an unhealthy node, disable a feature flag, or route around a failed dependency. Do not delete evidence or mutate customer data while diagnosing.
4. The scribe keeps a UTC timeline with links to dashboards, logs, deploys, commands, and customer communications. Do not paste credentials, tokens, or customer payloads into the record.
5. On handoff, the outgoing IC states severity, impact, current hypothesis, actions in progress, next decision time, and unresolved risks. The incoming IC acknowledges ownership in the incident record.

## Rollback and containment criteria

Roll back when all of the following are true:

- a recent deploy or configuration change is a credible cause;
- the previous version is known to be compatible with the current schema and queued work;
- rollback reduces customer impact faster than a forward fix; and
- the IC has named an owner and a verification probe.

Do not roll back a schema or data migration solely to restore availability. Freeze writes or isolate the affected path, consult the migration owner, and use the recovery procedure instead. For a suspected security event, preserve evidence and follow the breach runbook before rotating or deleting anything unless active containment requires it.

After every mitigation, verify the customer-facing probe, error rate, queue depth, and affected dependency. Keep the incident open until the IC records a stable observation window and an explicit recovery decision.

## Communications and closure

- **SEV1:** status-page update within 15 minutes, then at least every 30 minutes and on every material change.
- **SEV2:** status-page update when customer action or prolonged degradation warrants it, then at least hourly while impact continues.
- **SEV3:** communicate directly to the affected customer or operator and record the workaround and follow-up owner.
- Never speculate about root cause in a customer update. State impact, start time, current mitigation, and next update time.

At closure, record the end time, measured impact, mitigation, residual risk, and follow-up tickets. Create a post-mortem for a customer-facing SEV1, a SEV2 lasting more than 30 minutes, or any incident that exposed a systemic control gap. Use [`docs/postmortems/TEMPLATE.md`](../postmortems/TEMPLATE.md) and add the completed artifact to [`docs/postmortems/INDEX.md`](../postmortems/INDEX.md).
