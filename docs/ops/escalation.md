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

| Severity | Receiver | Delivery | Repeat |
|---|---|---|---|
| `page` | `faas-page` | Pushover at priority 2 (emergency: bypasses quiet hours, retries every minute for an hour until acknowledged) and email to `am_email_to_page` | every hour while firing |
| `warn` | `faas-warn` | email to `am_email_to_warn` only | every 4 hours |
| `info` | none | Prometheus and Grafana only | n/a |

The Pushover token, the Pushover user key and the SMTP password are files
under `/etc/alertmanager/secrets/`. They are never in the rendered config.

Two consequences matter during an incident:

- **There is one Pushover target.** Alertmanager has no secondary rotation.
  Moving to a second responder is a human step (below), not an automatic
  one. To page a group, point `am_pushover_user_key_file` at a Pushover
  *delivery group* key instead of a single user key.
- **If Alertmanager or Prometheus is down, nothing pages.** The
  `FaasAlertmanagerDeliveryDegraded` and `FaasPrometheusAlertingPathDegraded`
  runbooks cover the alerting path itself. The six-hourly synthetic canary
  (`FaasSyntheticCanaryFailed`) runs from GitHub Actions but still delivers
  through this Alertmanager. There is no outside dead-man's switch yet, so a
  silent pager is not proof of a healthy platform.

## Tiers

| Tier | Who | Paged when | Acknowledge | Engage |
|---|---|---|---|---|
| 0 | Primary on-call: the holder of the Pushover target | Any `page` alert, or a customer or security report | 5 min | 15 min (first command, first log line, or first status update) |
| 1 | Secondary on-call | Tier 0 has not acknowledged in 15 min, or hands off (asleep, in another incident, workstation lost) | 5 min after the call | 15 min |
| 2 | Platform owner | Both tiers silent for 30 min, **or** any SEV1 (fleet-wide outage, data loss, security incident) | immediately | immediately |

Tier 1 and Tier 2 are reached by phone call or direct message from whoever
noticed the gap: the Tier 0 responder, a teammate, or the reporter. Keep
their contact details in the private on-call roster, not in this repository.

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
