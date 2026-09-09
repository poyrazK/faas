# Responsible disclosure policy

> **Issue:** [#755](https://github.com/poyrazK/faas/issues/755).
> **Owner:** Security (single-operator company; founder wears the role).
> **Mirror:** [`/SECURITY.md`](../../SECURITY.md) — repo-root GitHub convention.

Gregale welcomes coordinated vulnerability disclosure. This page
sets out the channels, the SLAs, the scope, and the safe-harbour
language that backs the policy.

## 1. Contact

| Channel | Address |
|---|---|
| Email (preferred) | `security@gregale.dev` |
| PGP-encrypted email | `security@gregale.dev` — see §2 for the current fingerprint |
| Web form | <https://docs.gregale.dev/security/report> (operator-side, post-launch) |
| GitHub Security Advisories | <https://github.com/poyrazK/faas/security/advisories/new> |

Email is preferred for sensitive reports. GitHub Security Advisories
are appropriate for non-sensitive coordination; the platform will
acknowledge via the same channel.

## 2. PGP fingerprint

The current fingerprint is rotated every 24 months; the latest
fingerprint is published at <https://docs.gregale.dev/security/pgp.txt>
and mirrored here:

```
F1A0 9B3E 2C77 8D4F  6E12 5C9A 7B8D 4F3E 1A2C 9D5B
```

The full public key block lives at
<https://docs.gregale.dev/security/pgp.asc>. Reporters who wish to
encrypt their initial submission should fetch the key directly from
the docs site (not from this repo) — repo mirrors are public and
key-rotation may lag by a day.

## 3. SLAs (Service Level Agreements)

| Phase | SLA | Definition |
|---|---|---|
| Initial acknowledgement | **24 hours** | Operator sends an acknowledgement email confirming receipt + assigning an internal tracking id. |
| Triage + scope assessment | **72 hours** | Operator confirms whether the report is in-scope, out-of-scope, or a duplicate, and assigns a severity (Critical / High / Medium / Low). |
| Status update | **every 7 days** | Operator sends a status update while the report is open — even if the update is "still investigating." |
| Fix timeline (Critical) | **30 days** | Operator commits to deploying a fix or mitigation within 30 days for Critical-severity issues. |
| Fix timeline (High) | **60 days** | Operator commits to deploying a fix or mitigation within 60 days for High-severity issues. |
| Fix timeline (Medium / Low) | **90 days** | Operator commits to deploying a fix or mitigation within 90 days. |
| Public disclosure | **coordinated** | Operator coordinates public disclosure with the reporter; default is 90 days after the fix ships or after the reporter's chosen embargo date. |

These SLAs are aspirational — single-operator company, the founder
is on call. If a Critical takes 35 days instead of 30, the operator
will notify the reporter and explain. The SLAs are NOT a contract.

## 4. Scope

**In scope:**

- The Gregale platform (`poyrazK/faas` repo), running on a customer
  deployment. Includes the `apid`, `gatewayd-public`,
  `gatewayd-internal`, `schedd`, `vmmd`, `builderd`, `imaged`,
  `meterd`, `gregale` daemons, plus the `pkg/{api,state,fcvm,netns,
  oci,rootfs,meter,stripex,wire}` shared libraries.
- Customer-facing dashboard and app endpoints at `*.gregale.dev`.
- The Stripe / Paddle / Resend / Postmark / GitHub / Google OAuth
  integrations listed in
  [`subprocessors.md`](subprocessors.md), to the extent that Gregale
  code can affect them.

**Out of scope:**

- Denial-of-service attacks, volumetric or otherwise.
- Rate-limit testing that intentionally exhausts shared resources
  (CPU, RAM, network bandwidth).
- Social engineering of the operator or Gregale staff.
- Physical attacks against the Hetzner datacenter (see vendor
  assessment file `docs/compliance/vendor-assessments/hetzner.md`
  for physical controls).
- Reports against a deployment running a known-vulnerable Firecracker
  version past the 90-day patch window — these are operator-side
  hygiene, not Gregale code.
- Third-party plugins / customer code deployed on Gregale apps.
- End-user input validation in customer-deployed apps (the customer
  is the Controller; the customer's ISMS owns that surface).

**Excluded by design:**

- Issues that depend on a customer already having lost their
  authentication credentials (e.g. a leaked cookie used in an
  `auth.session.stolen` flow — see `events.kind` in spec §5.1).
- Issues that depend on a customer self-hosting a Gregale deployment
  with `unprivileged_userns_clone=1` or other spec §11 violations.

## 5. Safe harbour

Gregale will not pursue legal action against a reporter who:

- Made a good-faith effort to avoid privacy violations, data
  destruction, or service disruption during testing.
- Confined the report to the in-scope surface above.
- Did not exploit a vulnerability beyond the minimum necessary to
  demonstrate it.
- Reported the vulnerability promptly via the channels in §1 and
  refrained from public disclosure until coordinated with the
  operator.

The safe harbour covers activity conducted under the laws of the
reporter's jurisdiction. The safe harbour does not cover activity
that would itself be criminal under those laws (e.g. unauthorised
access to customer data, even when demonstrating a vulnerability).

## 6. Recognition

Gregale does not currently run a paid bug bounty programme. Reporters
who submit a valid, in-scope report are credited in the public
advisory after the fix ships (unless the reporter asks to remain
anonymous).

## 7. What to include in a report

Reports are triaged faster when they include:

1. **A clear summary** of the vulnerability and its impact.
2. **Steps to reproduce**, including any required setup (account,
   app, deployment, build, request shape).
3. **The affected component** — daemon name, package, route, or
   migration number.
4. **The affected deployment** — production / staging / Lima, plus
   the SHA of the running build.
5. **The reporter's preferred disclosure timeline**, with a default
   of 90 days from the operator's fix ship date.

## 8. Operator commitments

- All reports land in an encrypted internal tracker (operator-side,
  not in this repo) keyed by the `security@gregale.dev` mailbox.
- The tracker row carries: `received_at`, `acknowledged_at`,
  `severity`, `affected_component`, `reporter_handle`,
  `pgp_fingerprint_of_reporter` (if PGP used), `internal_status`,
  `fix_shipped_at`, `public_advisory_url`, `closed_at`.
- The tracker is reviewed at every quarterly `access-review.sql` run
  (PR-9) — any open reports older than 90 days are flagged for
  escalation.
- Inbound reports emit an audit row in the `events` table per
  spec §5.1 + ADR-035. The kind name lives in the ADR-035 family
  (canonical registry of all customer-facing audit kinds); the
  audit row carries no payload beyond `report_id` and a redacted
  `reporter_handle`.

## 9. Cross-references

- DPA §10 (breach notification SLA, 72 hours) — same 72-hour
  window applies when a vulnerability is exploited against customer
  data.
- DPA §5 (Processor obligations — confidentiality).
- [`soc2-control-mapping.md`](soc2-control-mapping.md) CC2.3 /
  CC4.1 / CC7.4 / CC7.5 / CC7.6.
- [`iso27001-statement-of-applicability.md`](iso27001-statement-of-applicability.md) A.5.24 / A.5.25 / A.5.26 / A.5.28 / A.6.6 / A.6.8.
- [`../../SECURITY.md`](../../SECURITY.md) — repo-root mirror
  (GitHub convention).
- [`subprocessors.md`](subprocessors.md) §3 for the in-scope
  third-party surface.
