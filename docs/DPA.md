# Data Processing Addendum (DPA)

**Effective date:** {{EFFECTIVE_DATE}}
**Customer:** {{CUSTOMER_NAME}}
**Jurisdiction:** {{JURISDICTION}}

This Data Processing Addendum ("DPA") supplements the master services
agreement between onebox faas ("Processor", the platform operator)
and {{CUSTOMER_NAME}} ("Controller", the customer) and reflects the
parties' agreement with respect to the Processing of Personal Data
by Processor on behalf of Controller in connection with the faas
service.

The DPA is offered under the European Commission's Standard
Contractual Clauses (controller-to-processor, 2021/914) where
applicable, and is consistent with the GDPR Art. 28 minimum content
requirements. Where local law imposes stricter rules, those rules
prevail.

## 1. Subject matter and duration

Processor Processes Personal Data on behalf of Controller for the
purpose of providing the faas platform (deployments, builds, runtime
secrets, request logs, billing). The Processing runs for the term of
the master agreement and any tail period required for lawful record
keeping (tax invoices: 7 years).

## 2. Nature and purpose of processing

- Hosting Controller's application code, build artifacts, and
  customer-uploaded secrets as part of the onebox FaaS platform.
- Routing inbound HTTP requests to the appropriate microVM
  instance.
- Aggregating per-minute usage (RAM-seconds + request count) for
  billing purposes (spec §10).
- Sending transactional email related to the service (account
  changes, billing alerts, deletion grace notice).

## 3. Categories of data subjects

- Controller's end users (visitors to apps Controller deploys on
  faas) — Controller remains the Controller for these subjects'
  data; Processor acts only as a transit / hosting layer.
- Controller's authorized operators (the account holder and any
  team members Controller invites via API key).

## 4. Categories of personal data

- Account data: email address, Stripe customer ID, plan tier.
- App data: app slug, deployment artifacts, runtime env vars
  (sealed at rest with the host X25519 key, spec §11/G2).
- Usage data: per-minute aggregate of (app, instance, request
  count, RAM-seconds). No request bodies or response bodies are
  retained beyond the in-flight transit window.
- Operational logs: request path, status code, host IP, timing —
  retained for 30 days for security incident response (spec §11).

## 5. Processor obligations

Processor shall:

- Process Personal Data only on documented instructions from
  Controller, including with regard to transfers of Personal Data
  to a third country or international organisation.
- Ensure that persons authorised to Process Personal Data have
  committed themselves to confidentiality or are under an
  appropriate statutory obligation of confidentiality.
- Implement the technical and organisational measures specified
  in §8 (Security measures).
- Engage sub-processors only with Controller's prior specific or
  general written authorisation, and notify Controller of any
  intended changes concerning the addition or replacement of other
  sub-processors (see §7).
- Assist Controller by appropriate technical and organisational
  measures, insofar as possible, in the fulfilment of Controller's
  obligation to respond to requests for exercising data subjects'
  rights laid down in Chapter III GDPR.
- Make available to Controller all information necessary to
  demonstrate compliance with Article 28 GDPR.

## 6. Data subject rights (spec §11 / G6)

Controller may at any time exercise the following rights on behalf
of data subjects via the documented faas endpoints:

| Right | Endpoint |
|---|---|
| Right of access (Art. 15) | `GET /v1/account/export` |
| Right to erasure (Art. 17) | `DELETE /v1/account` (30-day grace) |
| Right to restriction (Art. 18) | `POST /v1/account/restore` cancels a pending erasure |
| Right to portability (Art. 20) | `GET /v1/account/export` returns the JSON bundle |
| Right to object (Art. 21) | Contact support@DOMAIN |

The DPA itself is publicly available at
`GET /v1/account/dpa` (no auth required) so prospects can read it
before signing up.

## 7. Sub-processors

The current sub-processor list is maintained at
https://docs.DOMAIN/dpa/subprocessors and includes the categories:

- **Postgres hosting** (database): single-tenant managed Postgres
  with encryption at rest + TLS in transit. Daily encrypted
  snapshots retained 30 days.
- **Stripe** (billing): processes card data under its own PCI-DSS
  attestation; Processor never sees card numbers.
- **Resend / Postmark** (transactional email): receives recipient
  address + subject + body. Email bodies contain no end-user
  personal data — they target the account holder only.

Processor shall notify Controller at least 30 days before adding a
new sub-processor. Controller may object on reasonable
data-protection grounds; the parties shall work in good faith to
resolve the objection before the change takes effect.

## 8. Security measures (spec §11)

Processor implements the technical and organisational measures
required by Art. 32 GDPR, including:

- cgroups v2 isolation: every customer microVM runs in its own
  `memory.max = plan RAM + 8 MB` scope with `unprivileged_userns_clone=0`.
- Jailer-based privilege drop: each microVM is launched as a unique
  uid/gid in the 20000-29999 range inside a chroot, with a default-
  deny seccomp profile.
- Tenant egress filtering: deny 25/465/587; deny RFC1918 + link-
  local + cloud metadata IP ranges.
- No shared host directories with guests: only block devices cross
  the boundary (drive0 read-only base, drive1 per-app layer).
- virtio-rng on every guest so restored snapshots don't reuse RNG
  state.
- Customer secrets sealed at rest with the host X25519 key
  (pkg/secretbox, ADR-020). Plaintext VALUES never touch PG.
- TLS 1.3 in transit; HSTS + Strict CSP on the dashboard.

Full security checklist lives at https://docs.DOMAIN/security.

## 9. Audit rights

Processor shall make available to Controller, on reasonable prior
written notice (not less than 30 days), information necessary to
demonstrate compliance with this DPA and shall allow for and
contribute to audits, including inspections, conducted by
Controller or another auditor mandated by Controller.

Audit scope is limited to the Processing activities carried out for
Controller. Processor may charge a reasonable fee based on the
actual cost of making personnel available for the audit; such fee
shall be agreed in advance in writing.

## 10. Breach notification

Processor shall notify Controller without undue delay, and in any
case within 72 hours, after becoming aware of a Personal Data
breach affecting Controller's data. The notification shall
describe the nature of the breach, categories and approximate
number of data subjects and records concerned, likely
consequences, and measures taken or proposed to address the breach
and mitigate its possible adverse effects.

## 11. Data return on termination

Upon termination of the master agreement for any reason, Controller
may retrieve all Personal Data via `GET /v1/account/export` until
the deletion grace expires. After the grace lapses, Processor shall
delete all Personal Data from its systems, including any copies,
within 30 days, save where retention is required by applicable law
(tax invoices, anti-money-laundering records).

## 12. Governing law ({{JURISDICTION}})

This DPA is governed by the laws of {{JURISDICTION}}. Any dispute
arising out of or in connection with this DPA shall be subject to
the exclusive jurisdiction of the courts of {{JURISDICTION}},
without prejudice to data subjects' rights to bring claims in their
own jurisdiction under Art. 79 GDPR.

---

**Signatures**

Controller: _________________________  Date: ____________

Processor: __________________________  Date: ____________

— onebox faas, {{EFFECTIVE_DATE}}