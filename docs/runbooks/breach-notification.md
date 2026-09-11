# Breach notification

This runbook covers a suspected personal-data breach affecting Gregale or a
customer deployment. It applies to an operator who has enough evidence to
start response, even when the scope is still unknown.

The DPA requires notifying the Controller without undue delay and within 72
hours of becoming aware of a breach. That is the outer deadline for the
Controller notification, not a reason to wait for a complete investigation.

## 1. Discover and preserve

1. Open an incident with a UTC timestamp, incident commander, reporter, and
   the first known affected component or account.
2. Preserve the original alert, request IDs, audit IDs, deployment SHA, and
   relevant logs. Record queries and time ranges in the incident; do not copy
   secrets, tokens, full request bodies, or customer payloads into tickets.
3. Page the primary and secondary on-call using
   [`docs/ops/escalation.md`](../ops/escalation.md). A suspected customer-data
   exposure is at least SEV1 until triage downgrades it.

## 2. Triage and classify

Within the first 30 minutes, answer four questions:

- Which accounts, apps, regions, and data classes may be affected?
- Is access ongoing, or has the path been contained?
- Is there evidence of exfiltration, alteration, or only attempted access?
- Which processors or infrastructure providers need to be engaged?

Use this severity vocabulary for the incident record:

| Severity | Trigger | Default response |
| --- | --- | --- |
| SEV1 | Confirmed or likely access to customer personal data, cross-tenant access, credential/secret exposure, or an active exploit | Incident commander, engineering manager, legal/DPA owner, immediate containment, Controller notice clock starts |
| SEV2 | Security control failure with a credible but unconfirmed data path, or a contained exposure limited to one tenant | Engineering manager and security owner, contain within the same shift, validate scope before downgrade |
| SEV3 | Attempted or theoretical issue with no evidence of customer-data access | Fix through the normal change process, document why the incident is not a breach |

Do not downgrade a SEV1 solely because logs are incomplete. Treat missing
telemetry as uncertainty and keep the higher severity until the evidence is
recovered or the access path is disproved.

## 3. Contain and eradicate

Choose the least destructive action that stops further access, then record the
operator, timestamp, and verification:

- revoke or rotate exposed API keys, session keys, OAuth credentials, webhook
  secrets, or host-age identities;
- disable the affected route, tenant surface, deployment, or compute node;
- block the abusive source at the edge and preserve the rule or request ID;
- quarantine affected images or deployments and stop new builds from the
  affected artifact;
- take a forensic snapshot before deleting volatile evidence when it is safe;
- verify containment with a new request, audit row, or metric query.

Never test containment by using a customer's secret or by modifying customer
data. If a provider is involved, open its security escalation at the same
time and preserve the provider case ID.

## 4. Notify within 72 hours

The incident commander and DPA/legal owner prepare the Controller notice as
soon as the affected data class and likely impact are known. The notice must
state, to the extent known:

- when Gregale became aware of the breach and when it occurred;
- the nature of the breach and the affected systems;
- categories and approximate number of data subjects and records;
- likely consequences and the containment/mitigation steps taken;
- a current contact and the next update time.

Send the notice through the contractual Controller contact and record the
delivery timestamp. If facts are incomplete, send an initial notice with the
known facts and a correction schedule; do not wait for final root cause.

## 5. Decide on data-subject notification

The DPA/legal owner decides whether the breach is likely to result in a high
risk to individuals. Notify data subjects without undue delay when that
threshold is met, unless a documented GDPR exception applies (for example,
effective encryption makes the data unintelligible or an equally effective
measure has removed the high risk).

The customer Controller owns notices to its data subjects unless the contract
or law requires Gregale to assist. Gregale supplies the affected data
categories, timeline, mitigations, and approved customer-facing wording.

## 6. Recover and close

1. Remove temporary containment only after the fix is deployed and verified.
2. Add regression coverage for the failed seam and rotate any credential that
   could have been exposed.
3. Publish a customer-facing advisory when the security owner and Controller
   agree that disclosure is safe. Use
   [`docs/security-advisories/TEMPLATE.md`](../security-advisories/TEMPLATE.md).
4. Complete a post-incident review within five business days for SEV1 and ten
   business days for SEV2. Include the timeline, scope evidence, notification
   decisions, missed signals, and owned follow-up issues.
5. Close the incident only when the Controller notice, evidence retention,
   fix verification, and follow-up owners are recorded.

## Required records

Keep the incident ID, UTC timestamps, notification deliveries, provider case
IDs, relevant audit/request IDs, containment changes, affected-data estimate,
and final advisory decision. Store sensitive evidence in the restricted
incident workspace; link to it from the incident rather than copying it into
GitHub or ordinary support tickets.
