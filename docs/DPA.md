# Data Processing Addendum (DPA)

<!--
  Maintainer notes (not shown to customers): docs/ops/legal-documents-operator-notes.md.
-->

**Parties:** Gregale ("Processor") and the customer that accepted the
Gregale Terms of Service ("Controller").
**Effective:** from the date the Controller accepted the Terms of
Service, for as long as they apply.

This Data Processing Addendum ("DPA") supplements the Gregale Terms of
Service (the "master agreement", https://gregale.dev/terms) between
Gregale ("Processor", the platform operator) and the customer that
accepted them ("Controller"). It forms part of the master agreement
and needs no separate signature. It reflects the
parties' agreement with respect to the Processing of Personal Data
by Processor on behalf of Controller in connection with the Gregale FaaS
service.

The DPA is offered under the European Commission's Standard
Contractual Clauses (controller-to-processor, 2021/914) where
applicable, and is consistent with the GDPR Art. 28 minimum content
requirements. Where local law imposes stricter rules, those rules
prevail.

## 1. Subject matter and duration

Processor Processes Personal Data on behalf of Controller for the
purpose of providing the Gregale FaaS platform (deployments, builds, runtime
secrets, request logs, billing). The Processing runs for the term of
the master agreement and any tail period required for lawful record
keeping (tax invoices: 7 years).

## 2. Nature and purpose of processing

- Hosting Controller's application code, build artifacts, and
  customer-uploaded secrets as part of the Gregale FaaS platform.
- Routing inbound HTTP requests to the appropriate microVM
  instance.
- Aggregating per-minute usage (RAM-seconds + request count) for
  billing purposes.
- Sending transactional email related to the service (account
  changes, billing alerts, deletion grace notice).
- Only on Controller's instruction (Pro and Scale): restoring a copy
  of a running instance's memory into an isolated, non-serving debug
  instance (a production fork), and capturing such a copy when an
  instance answers a request with a server error or when Controller's
  application asks for one from its own code (a crash snapshot, off by
  default per app).

## 3. Categories of data subjects

- Controller's end users (visitors to apps Controller deploys on
  faas) — Controller remains the Controller for these subjects'
  data; Processor acts only as a transit / hosting layer.
- Controller's authorized operators (the account holder and any
  team members Controller invites via API key).

## 4. Categories of personal data

- Account data: email address, billing-provider customer ID, plan tier.
- App data: app slug, deployment artifacts, runtime env vars
  (sealed at rest with the host X25519 key).
- Usage data: per-minute aggregate of (app, instance, request
  count, RAM-seconds). No request bodies or response bodies are
  retained beyond the in-flight transit window.
- Operational logs: request path, status code, host IP, timing —
  retained for 30 days for security incident response.
- Application logs: what Controller's applications write to standard
  output and standard error, which can contain any personal data an
  application chooses to log. Held in memory while an instance runs,
  then archived in object storage. Controller can read archived logs for
  its plan's log retention (Free 1 day, Hobby 7, Pro 30, Scale 90). The
  archive deletes them 90 days after they are written, and with the
  account (section 11); the storage provider keeps a deleted object
  recoverable for 7 more days before purging it.
- Job output: the last 1 MiB of what each attempt of a job task Controller
  runs writes to standard output and standard error, which can contain any
  personal data the job chooses to write. Stored in the platform database
  with the run's history. Deleted when the run finished longer ago than the
  plan's log retention (Free 1 day, Hobby 7, Pro 30, Scale 90), and with the
  account (section 11); the run's status, exit codes and timings are kept
  until the account is deleted.
- Request traces: spans Controller's applications export when tracing is
  on — operation and dependency names, durations, status and failure
  class. Exception messages and stack traces are not retained. Spans are
  kept with the request record and follow the same retention as
  customer request analytics below.
- Crash snapshots and production forks (only when Controller turns
  them on or requests one): a copy of one instance's memory and
  writable disk layer at one moment, which can contain any personal
  data the application held in memory, including end users' data and
  credentials. A crash snapshot records the triggering status code
  and request path, or the label and path Controller's application
  supplied when it asked for the snapshot. Crash snapshots are encrypted at rest with a key
  unique to each snapshot and deleted with that key after 7 days;
  they are decrypted only while Controller has a fork of them open.
  A fork cannot reach the network, never receives production traffic,
  stays on the node it was restored on, and is destroyed at its TTL
  (at most 4 hours); its writable disk copy is deleted with it.
- Customer request analytics: bounded aggregates over a normalized
  User-Agent family, hostname-only referrer, ISO country code, route,
  and status. The analytics path stores no source IP, raw User-Agent,
  full referrer URL, cookies, tracking script, or request/response body;
  retention follows the plan's DebugTelemetryRetentionDays setting.

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

## 6. Data subject rights

Controller may at any time exercise the following rights on behalf
of data subjects via the documented Gregale endpoints:

| Right | Endpoint |
|---|---|
| Right of access (Art. 15) | `GET /v1/account/export` |
| Right to erasure (Art. 17) | `DELETE /v1/account` (30-day grace) |
| Right to restriction (Art. 18) | `POST /v1/account/restore` cancels a pending erasure |
| Right to portability (Art. 20) | `GET /v1/account/export` returns the JSON bundle |
| Right to object (Art. 21) | Contact gregale@gregale.dev |

The DPA itself is publicly available at
`GET /v1/account/dpa` (no auth required) so prospects can read it
before signing up.

## 7. Sub-processors

The current sub-processor list is published at
https://gregale.dev/docs/dpa/subprocessors for advance notification
of additions and replacements (30-day notice, see paragraph below);
the sub-processor set that applies when the Controller accepts this
DPA is the list enumerated in this section at that time, and the
notification URL is the channel by which Processor signals
upcoming changes.

- **OVHcloud** (infrastructure hosting, US): dedicated bare-metal servers
  that run Gregale's control plane, compute nodes, managed Postgres and
  storage for the public release. Gregale administers the servers;
  customer secrets are sealed with host keys before storage.
- **Google Cloud** (infrastructure hosting, US, us-east1): Compute Engine
  and Cloud Storage host the current production fleet during the public
  beta, and a standby and test environments afterwards.
- **Cloudflare** (DNS, reverse proxy, CDN, DDoS and firewall protection):
  every request to Gregale and to customer apps passes through Cloudflare,
  which sees request metadata (IP address, headers, URL) and carries
  request and response bodies in transit.
- **Polar** (billing): merchant of record. Polar receives customer email,
  plan and subscription metadata, and monthly metered usage; it handles
  payment data and merchant-of-record tax obligations under its own
  controls.
- **Resend** (transactional email): receives the recipient address,
  subject and body of account emails, which go to the account holder
  only, and reports bounces and complaints back to Gregale.
- **Google** (OAuth identity provider): account holders who choose
  Google login exchange the OAuth subject (`sub`) and a verified
  email address — and the optional profile name and avatar URL —
  at `POST /v1/auth/google/callback`. No contact list, no Drive,
  no repo list. Scopes are `openid email profile`.
- **GitHub** (OAuth identity provider): account holders who choose
  GitHub login exchange the GitHub user-id, the primary verified
  email address, and the login handle — and the optional name and
  avatar URL — at `POST /v1/auth/github/callback`. No repo list,
  no contact list, no org list. Scopes are `read:user user:email`.
- **GitHub App** (deploys from GitHub): a single Processor service is the
  only outbound caller to `api.github.com`. It fetches the source of
  repositories a customer connects to build their deployments, posts
  Checks results keyed by commit SHA and exchanges installation tokens.
  Inbound webhooks at `POST /webhooks/github` are HMAC-SHA-256 verified
  before any payload is consumed.
- **Vercel** (website hosting): serves the gregale.dev website,
  documentation and web front end, and sees website visitors' request
  metadata. Account data and applications are not stored there.
- **Amazon Web Services** (test environments): runs test and development
  environments with test accounts and synthetic data only; production
  customer data is not copied there.

Processor shall notify Controller at least 30 days before adding a
new sub-processor. Controller may object on reasonable
data-protection grounds; the parties shall work in good faith to
resolve the objection before the change takes effect.

## 8. Security measures

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
  so plaintext values are never stored in the database.
- Crash snapshots encrypted on the capturing host before they are
  stored (age, a fresh X25519 key per snapshot sealed to the fleet
  key), so no unencrypted copy is ever written to storage; a decrypted
  copy exists only on the host running a fork of the snapshot, for that
  fork, and is deleted with it.
- Production forks run with all outbound network access blocked and
  are never routed production traffic; opening one requires a key
  with secret-read scope and MFA, and every create, access and delete
  is written to the audit log.
- TLS 1.3 in transit; HSTS + Strict CSP on the dashboard.

Full security checklist lives at https://gregale.dev/docs/security.

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

## 12. Governing law

This DPA is governed by the same law, and disputes arising out of or
in connection with it go to the same courts, as the master agreement,
without prejudice to data subjects' rights to bring claims in their
own jurisdiction under Art. 79 GDPR.

---

This DPA is accepted together with the Terms of Service. A Controller
that needs a countersigned copy can request one at gregale@gregale.dev.
