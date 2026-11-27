# Escalation matrix

Who gets paged, how, and what to do when nobody answers. This describes the
paging path the repository actually deploys. If you change
`deploy/ansible/roles/alertmanager/templates/alertmanager.yml.j2` or the
status-page commands, update this page in the same pull request.

Severity levels, war-room roles and update cadence are in
[`docs/runbooks/incident-response.md`](../runbooks/incident-response.md).
Scenario playbooks are listed there too.

## How a page reaches a person

Alertmanager routes by the `severity` label on each rule in
`deploy/ansible/roles/prometheus/files/*.rules.yml`:

| Alert | Receiver | Delivery | Repeat |
|---|---|---|---|
| `severity=page` | `faas-page` | Pushover at priority 2 (emergency: bypasses quiet hours, retries every minute for an hour until acknowledged), email to `am_email_to_page`, and the page chat webhook (Discord or Slack) when configured | every hour while firing |
| `FaasPrometheusNotificationsDropped`, `FaasAlertmanagerNotificationsFailed` at page severity | `faas-delivery-page` plus `faas-page` | Optional PagerDuty incident assigned to the primary on-call schedule, escalating to the secondary after five minutes without action | one open PagerDuty incident per fleet while either condition is firing |
| `severity=warn` | `faas-warn` | email to `am_email_to_warn` and the warn chat webhook (falls back to the page webhook when no warn-only one is set). A review queue, not an interrupt (ADR-1236): grouped by `family`, new alerts batched for an hour | once a day (`am_warn_repeat_interval`, default 24h) |
| `severity=info` | none | Prometheus and Grafana only | n/a |
| `FaasWatchdog` (always firing) | `faas-heartbeat` | posts to the external monitor URL in `am_heartbeat_url_file` | every `am_heartbeat_interval` (default 5m) |

Receiver credentials live in files under `/etc/alertmanager/secrets/`. Use
`gregalectl obs notification-health` to see the current deployment inventory
and transport observations for the fleet.

What this means during an incident:

- Acknowledge the delivery incident in PagerDuty to stop its escalation.
  Other pages use the manual on-call tiers below.
- Pushover supports an individual key or a delivery group key in
  `am_pushover_user_key_file`.
- **A silent warn is not a resolved warn.** A still-firing warn family is
  repeated once a day. Check Alertmanager or Grafana for its current state.
- **If Alertmanager or Prometheus is down, nothing pages from inside.** The
  dead-man's switch covers that: the external monitor alerts when the
  `FaasWatchdog` heartbeat stops ([FaasWatchdog.md](../runbooks/FaasWatchdog.md)).
  It only works if `am_heartbeat_url_file` is provisioned on the control
  plane and the external monitor notifies the on-call. Check both at every
  paging drill. `FaasAlertmanagerDeliveryDegraded` and
  `FaasPrometheusAlertingPathDegraded` cover partial failures of the
  alerting path.

### Enable delivery escalation for a fleet

1. Create a dedicated PagerDuty service with an Events API v2 integration
   for the fleet. Assign it the Gregale Platform on-call policy. Put the
   primary and secondary on-call schedules at levels one and two. Set the
   primary escalation delay to five minutes. Check that both schedules have
   coverage and that the service sends high-urgency notifications. [PagerDuty escalation policies](https://support.pagerduty.com/main/docs/escalation-policies)
2. Provision the integration key through the host secret procedure at
   `/etc/alertmanager/secrets/delivery-pagerduty-key`, owned by
   `alertmanager:alertmanager` with mode `0400`. Set
   `am_delivery_pagerduty_key_file` to that path and set `am_fleet_name` to
   the fleet ID in the fleet's Ansible vars. Each fleet gets its own PagerDuty
   service and key. Roll out the release containing the receiver health API
   and the Alertmanager role with the fleet's normal deployment procedure.
3. Confirm the expected `faas-delivery-page/pagerduty` group appears in
   `gregalectl obs notification-health`. Run the approved
   [monitoring qualification exercise](notification-delivery.md#rollout-and-operational-qualification).
   Have the primary confirm the labeled PagerDuty incident and its recovery.
   Use a separate labeled incident on the PagerDuty service to leave the
   first notification unacknowledged for five minutes. Verify the secondary
   receives the escalation, acknowledge it, and close the test. Record the
   PagerDuty incident IDs and the trigger, receipt, acknowledgment, and
   escalation times in the drill record.

## Tiers

| Tier | Who | Paged when | Acknowledge | Engage |
|---|---|---|---|---|
| 0 | Primary on-call | Any `page` alert, or a customer or security report | 5 min | 15 min (first command, first log line, or first status update) |
| 1 | Secondary on-call | PagerDuty escalates delivery incidents after 5 min if the primary has taken no action. For other pages, call when Tier 0 has not acknowledged in 15 min or hands off. | 5 min after receiving the escalation | 15 min |
| 2 | Platform owner | Both tiers silent for 30 min, **or** any SEV1 (fleet-wide outage, data loss, security incident) | immediately | immediately |

Manual escalations reach Tier 1 and Tier 2 by phone call or direct message from whoever
noticed the gap: the Tier 0 responder, a teammate, or the reporter. Keep
their contact details in the private on-call roster, not in this repository.

**Don't wait out the clock on a SEV1.** If a SEV1 is in progress and Tier 0
has not acknowledged within 10 minutes, call Tier 1 immediately rather than
waiting the full 15. The timeouts are a floor for responsiveness, not a
procedure to finish before acting.

The platform owner can pull in anyone, approve customer-facing wording,
engage legal for a security incident, and open provider escalations (GCP
support, Cloudflare, GitHub, the domain registrar, Polar).

### Provider escalations

Open these as soon as the provider is a credible cause or a party to a
security event, and paste the case ID into the incident record.

| Provider | What depends on it | Runbook |
|---|---|---|
| Google Cloud (project in [gcp-public-beta-hardening.md](../runbooks/gcp-public-beta-hardening.md)) | control plane, compute nodes, disks, backups in GCS | [cloud-provider-loss.md](../runbooks/cloud-provider-loss.md) |
| Cloudflare | DNS, proxy, WAF, Workers (`get.gregale.dev`, public timeout worker) | [third-party-outage.md](../runbooks/third-party-outage.md) |
| GitHub | source, CI, release signing identity, deploys, deploy freeze | [third-party-outage.md](../runbooks/third-party-outage.md), [release-pipeline-compromise.md](../runbooks/release-pipeline-compromise.md) |
| Domain registrar | the `gregale.dev` delegation | [dns-registrar-compromise.md](../runbooks/dns-registrar-compromise.md) |
| Polar | payments and billing webhooks | [PolarBillingWebhooks.md](../runbooks/PolarBillingWebhooks.md) |
| Email (Resend, Postmark) | sign-in mail, notifications, alert email | [third-party-outage.md](../runbooks/third-party-outage.md) |

## Customer communication

| Situation | Channel | Who approves |
|---|---|---|
| SEV1 or SEV2 with customer impact | Public status: `gregalectl status incident create` (see [StatusPageDegraded.md](../runbooks/StatusPageDegraded.md)) | Tier 0 may post the first factual update. The platform owner approves anything beyond impact, start time, mitigation and next update time. |
| One affected customer | Direct reply on the customer's support thread | Tier 0 |
| Security incident or personal-data breach | As set out in [breach-notification.md](../runbooks/breach-notification.md) | Platform owner, with legal |

The public status page reads `/v1/status` from the same API origin. If the
API or the edge in front of it is down, the status page is down with it.
Until an off-platform status channel exists, post on the company's social
accounts and reply to support threads directly. There is no bulk customer
email tool. Do not improvise one from a production database export.

## Post-mortems

- Customer-facing SEV1: post-mortem within 5 business days.
- SEV2 lasting more than 30 minutes: within 10 business days.
- SEV3: reviewed at the next weekly operations review.
- Any incident that exposed a missing control gets a post-mortem regardless
  of severity.

Use [`docs/postmortems/TEMPLATE.md`](../postmortems/TEMPLATE.md) and list the
result in [`docs/postmortems/INDEX.md`](../postmortems/INDEX.md).

## On-call handoff

Before going off rotation, the outgoing responder fills in
[`handoff-template.md`](handoff-template.md) and the incoming responder
acknowledges it. During an open incident, the incident commander handoff in
incident-response.md applies as well.

## Keep this page true

Review this page at every drill (see [`docs/drills/README.md`](../drills/README.md)):
send a test page, confirm it reaches the current Tier 0 device, and confirm
the Tier 1 and Tier 2 contacts in the private roster are current.
