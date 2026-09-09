# SOC 2 control mapping

> **Status:** Draft for SOC 2 Type 1 readiness review.
> **Issue:** [#755](https://github.com/poyrazK/faas/issues/755).
> **Audit window target:** the calendar quarter in which the last open
> gap (see [§ Open items](#open-items)) closes.

This document maps every SOC 2 Trust Services Criteria (TSC) control
that the platform engages with to the **artifact in the codebase** that
satisfies it — code, schema, log, dashboard, runbook, or external
service. The auditor can re-derive the evidence from a fresh checkout
of `main` at the SHA named in the row's "evidence SHA" column.

Gregale is a single-control-plane-node FaaS on bare-metal x86_64
(`docs/faas_implementation_spec.md` §1, §11). Multi-node HA is the M9
acceptance gate (ADR-025, ADR-062, ADR-066); until that ships,
availability-related controls (A1.x) are documented as **point-in-time
single-node** with explicit gap notes.

Conventions used in the table:

- **Evidence type** — what shape the artifact takes. `code`, `schema`,
  `audit-log`, `metric`, `runbook`, `external service`, `policy`.
- **Frequency** — how often the control operates. `continuous`,
  `per-request`, `per-deploy`, `per-day`, `per-quarter`, `per-event`.
- **Verification** — the concrete command or panel an operator runs to
  prove the control works during an audit window.

---

## Common Criteria (CC series)

### CC1 — Control environment

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC1.1 | The entity demonstrates a commitment to integrity and ethical values. | `docs/faas_implementation_spec.md` §11 (security hardening checklist) + `docs/compliance/` (this directory). Code-of-conduct lives outside the repo; see `docs/compliance/vendor-risk-management.md` § Onboarding for the operator commitment. | policy | per-quarter | `git log --oneline -- docs/compliance/` shows quarterly review cadence. |
| CC1.2 | The board of directors exercises oversight responsibility. | One-operator company; founder-operator wears every role. The §13 RAM budget ledger (`faas-tenant.slice` 57,344 MB hard fence) is the load-bearing governance mechanism. | policy | continuous | `systemctl status faas-tenant.slice` + `systemctl show faas-tenant.slice -p MemoryMax`. |
| CC1.3 | Management establishes structures, reporting lines, and authorities. | Repository structure (`cmd/{apid,gatewayd,gatewayd-public,gatewayd-internal,schedd,vmmd,builderd,imaged,meterd,gregale}`) + CLAUDE.md "Component ownership" section — every component has a single owner. | policy + code | continuous | `ls cmd/` returns the canonical daemon list; CLAUDE.md present at repo root. |
| CC1.4 | The entity demonstrates a commitment to competence. | `pkg/api/limits.go` is the single source of truth for every limit; new limits must add a field here (spec §15 conventions). Code review gates every change. | code | per-PR | `git diff --stat main..HEAD \| grep -v limits.go` to find limits-inlined; should be empty. |
| CC1.5 | The entity holds individuals accountable for their responsibilities. | PR review required on `main`; `.github/workflows/no-direct-push.yml` tripwire; ADRs required for architecture changes (CLAUDE.md "Workflow"). | code + audit-log | per-PR | `gh api repos/poyrazK/faas/branches/main/protection` shows required-review enforcement. |

### CC2 — Information and communication

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC2.1 | The entity generates and uses relevant, quality information. | `events` table with §5.1 taxonomy (30+ kind families); `pkg/audit.Auditor.Emit` is the single emit seam (failed writes count + log + return, never roll back — see ADR-035). | audit-log | per-event | `SELECT count(*) FROM events WHERE created_at > now() - interval '24 hours'` ≈ expected throughput. |
| CC2.2 | The entity internally communicates information necessary to support the functioning of internal control. | Prometheus + Alertmanager + self-hosted Grafana OSS (`docs/adr/031-self-hosted-grafana.md`); Loki for log aggregation. Alertmanager routes by `family` label per §12.3 (`docs/adr/039-traffic-anomaly-detection.md`). | metric + runbook | continuous | `kubectl get pods` analog: `systemctl status node_exporter prometheus alertmanager grafana loki`. |
| CC2.3 | The entity communicates with external parties. | `docs/DPA.md` (Data Processing Addendum); `docs/compliance/subprocessors.md` (public sub-processor list — PR-3 of issue #755); `docs/compliance/responsible-disclosure.md` (PR-4). | policy | per-change | DPA + sub-processor list published at customer-facing URLs. |

### CC3 — Risk assessment

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC3.1 | The entity specifies objectives with sufficient clarity to enable the identification of risks. | Spec §1 (inherited constraints), §13 (RAM budget ledger), §14 (delivery milestones, each gate = passing acceptance tests). Each milestone's acceptance criteria are executable tests. | policy + code | per-milestone | `make test` + `make test-metal` green. |
| CC3.2 | The entity identifies risks to the achievement of its objectives. | `docs/faas_implementation_spec.md` §17 (known gaps register — G1–G16); `docs/scale_out_and_workload_classes.md` (single-node blast-radius analysis). | policy | per-quarter | Section §17 review on every ADR. |
| CC3.3 | The entity considers the potential for fraud. | Rate limits per app + per account (`pkg/api/limits.go`: `RateLimitRPS` 5/20/100/500 + `RateLimitPerAccountRPM` 300/1200/6000/30000 by plan); `pkg/auth/middleware.AuthLimit` shared bucket (10/min/IP failed-auth, `docs/adr/040-per-account-rate-limit.md` family). Crypto-mining heuristic (spec §11): sustained CPU 100 % for > 15 min on Free/Hobby → auto-park + review queue. | code | continuous | `apid_top_tenant_rps{account_id!="other"}` panel; `FaasFailedLoginSpike` alert. |
| CC3.4 | The entity identifies and assesses changes that could significantly impact the system. | Every architectural change requires an ADR (`docs/adr/`); every PR names the milestone (CLAUDE.md). Firecracker upgrade = documented routine (ADR-005), not incident. | policy + audit-log | per-PR | `ls docs/adr/ | wc -l` increases monotonically with merges. |

### CC4 — Monitoring activities

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC4.1 | The entity performs ongoing evaluations of internal control. | Per-instance liveness probe (`docs/adr/079-liveness-probe-restart-wedged-vm.md`): 3 consecutive non-2xx on vsock 1028 → `Engine.DestroyForLivenessFailure` → snapshot marked stale → cold-boot fallback per ADR-005. 3 destroys in 300 s → `apps.status='evicted_cold'` + audit kind `instances.parked_liveness_exhausted`. | code + audit-log | continuous | `SELECT count(*) FROM events WHERE kind = 'instances.parked_liveness_exhausted' AND created_at > now() - interval '7 days'` should be near zero. |
| CC4.2 | The entity evaluates and communicates internal control deficiencies. | Alertmanager routes per-family; each runbook at `docs/runbooks/` (`FaasWakeLatencyHigh.md`, `FaasDaemonDown.md`, etc.) names the response. | runbook | continuous | `gh api repos/poyrazK/faas/contents/docs/runbooks` returns the catalogue. |

### CC5 — Control activities

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC5.1 | The entity selects and develops control activities. | Control mapping (this document) + `docs/compliance/access-review.sql` (PR-9). | policy | per-quarter | `docs/compliance/access-review.sql` run quarterly; output archived with date. |
| CC5.2 | The entity selects and develops general control activities over technology. | Spec §11 hardening checklist is the technology-control baseline; applied via `make bootstrap` (ansible). | code + runbook | continuous | `make bootstrap` idempotent on fresh Ubuntu 24.04 (M0 acceptance). |
| CC5.3 | The entity deploys policies and procedures. | This directory + `docs/compliance/vendor-risk-management.md` + operator onboarding checklist (operator-side, not in repo). | policy | per-quarter | Quarterly review checklist signed by operator. |

### CC6 — Logical and physical access (logical portion)

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC6.1 | The entity implements logical access security software, infrastructure, and architectures. | `pkg/auth/middleware.RequireSession` is the single authentication seam (cookie + bearer branches); `pkg/auth/hash.go` SHA-256 hashes; `pkg/auth/totp.go` TOTP MFA (ADR-077, sealed at rest + 10 recovery codes). | code | per-request | Unit test `pkg/auth/middleware/middleware_test.go::TestRequireSession_*` green. |
| CC6.2 | New users are registered and authorized before access is granted. | Signup requires email verification + one of (card, GitHub account ≥ 30 days) per spec §11 abuse control. The signup path is observable in the `events` table (`auth.login` / `auth.login_failed` per §5.1 + ADR-035). | code + audit-log | per-event | `SELECT count(*) FROM events WHERE kind = 'auth.login' AND created_at > now() - interval '24 hours'` ≈ expected throughput; `auth.login_failed` spikes via the `FaasFailedLoginSpike` alert (CC4.2). |
| CC6.3 | The entity authorizes, modifies, or removes access to data and software based on roles, responsibilities, or system design. | `org_members.role` enum `{owner, admin, developer, viewer, billing}` (ADR-061); exactly one owner per non-personal org; ownership transfer is the only way to vacate the role. Per-deployment auth (`apps.require_authn`, issue #560). Audit trail per ADR-061 + ADR-035 (event kind names tracked in those ADRs; see also the §5.1 family table for the canonical kind registry). | code + schema + audit-log | per-event | `events.kind` filter for the org-membership family in the `GET /v1/audit-events` dashboard (kind_prefix panel). |
| CC6.4 | The entity restricts physical access to protected information assets. | **Inherited from hosting provider.** Gregale runs on Hetzner bare-metal x86_64 (founding doc R3); Hetzner SOC 2 report covers physical access. | external service | per-year | Hetzner SOC 2 Type II report under NDA (vendor-assessment file: `docs/compliance/vendor-assessments/hetzner.md`, PR-10). |
| CC6.5 | The entity discontinues logical and physical protection only when no longer required. | Key rotation: `keys.revoked_at` (retained for audit lineage, exempt from `KeysMax` quota). `sessions.revoked_at` (G12 / `docs/adr/039-server-side-session-revocation.md` — `auth.session.revoke` + `auth.sessions.revoke_all` + `auth.session.stolen` audit kinds per ADR-039/035). `POST /v1/auth/sessions/revoke_all` for emergency. `pkg/api/limits.go::DefaultAPIKeyGraceWindowDays = 7` lets CI rotate atomically. | code + audit-log | per-event | Quarterly `access-review.sql` (PR-9) flags keys older than 1y or unused >90d. |
| CC6.6 | The entity implements logical access controls to protect against threats from sources outside its system boundaries. | Per-instance netns (`pkg/netns`); egress deny list (spec §7 / `docs/adr/034-scoped-api-keys.md` — SMTP 25/465/587 blocked, RFC1918 + link-local + metadata denied); per-app egress allowlist (`docs/adr/031-app-egress-allowlist.md` / `docs/adr/033-app-egress-allowlist-v6.md`, Pro ≤16 / Scale ≤64 entries, Free/Hobby gated); nftables default-drop inbound (spec §11). | code | per-packet | `vmmd_egress_deny_total{cidr, family}` Prometheus counter (spec §12.2). |
| CC6.7 | The entity restricts the transmission, movement, and removal of information to authorized users. | TLS 1.3 at edge; Certmagic wildcard cert automation (`docs/adr/024-certmagic-cutover.md`). HSTS, X-Frame-Options=DENY, nosniff, Referrer-Policy, Permissions-Policy, CSP with per-request 128-bit nonce (`pkg/httpsec`, issue #249). mTLS for control-plane gRPC (`docs/adr/052-control-plane-mtls-and-handler-peer-binding.md`, Gate A). | code | per-request | `curl -I https://{slug}.gregale.dev 2>&1 \| grep -i 'strict-transport'`. |
| CC6.8 | The entity implements controls to prevent or detect and act upon the introduction of unauthorized or malicious software. | Per-instance cgroup scope `memory.max = ram_mb + 8 MB` (spec §4.4 line 180); unique uid/gid 20000–29999 per instance (recycled); jailer chroot + default-deny seccomp profile (spec §11 / line 669-670); `kernel.unprivileged_userns_clone=0`; virtio-rng always attached. Builds run inside ephemeral builder microVMs (CLAUDE.md "things that look wrong but are load-bearing" — VM boundary IS the resource cap). OCI image digest pinning (`pull_digest`); cosign signature enforcement (`docs/adr/058-cosign-deploy-time-enforcement.md` — issue #472, `apps.require_signed` + per-publisher trust list at `/etc/faas/secrets/trusted-publishers/<signer>.pem`, mode 0444 per file). | code + schema | per-deploy | `imaged deploys.verify_signature_total` counter (spec §12); audit row emitted at signature-verified path (see ADR-058 for the canonical kind name once landed). |

### CC7 — System operations

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC7.1 | The entity uses detection and monitoring procedures to identify changes to configurations that result in introduction of new vulnerabilities. | Branch protection on `main` (Settings → Branches → "Require a pull request before merging"); `.github/workflows/no-direct-push.yml` tripwire (CLAUDE.md). Dependabot for Go modules + Docker base images + actions. | code + external service | continuous | `gh api repos/poyrazK/faas/branches/main/protection`. |
| CC7.2 | The entity monitors system components for anomalies. | Prometheus (per-instance `schedd_instance_cpu_pct{app,node}`, `schedd_instance_rss_mb{app,node}`, `schedd_instance_inflight_requests{app,node}` per spec §12 / `docs/adr/036-instance-metrics-cardinality-rollups.md`); per-app `gateway_request_duration_seconds{app,class}` (`docs/adr/041-tenant-abuse-observability.md`); egress deny counters (spec §12.2); per-app top-N RPS gauge (`apid_top_tenant_rps{account_id}` per §12.4 / `docs/adr/041-tenant-abuse-observability.md`). | metric | continuous | `kubectl get pods` analog + Grafana dashboard `top-tenants.json`. |
| CC7.3 | The entity evaluates security events to determine whether they could or have resulted in a failure to meet objectives. | `events.kind` taxonomy (§5.1) tags every auditable action; `apid_audit_write_failures_total` + `schedd_audit_write_failures_total` (per-daemon) flag audit-write anomalies. `webhook.replay_rejected` (G11 / `docs/adr/042-webhook-replay-protection.md`) flags webhook HMAC-verified replays. `auth.session.stolen` (G12 / `docs/adr/039-server-side-session-revocation.md`) flags revoked-cookie replay. | metric + audit-log | continuous | `FaasApidAuditWriteFailures` alert runbook. |
| CC7.4 | The entity responds to identified security incidents. | Liveness probe failure → destroy + cold-boot (`docs/adr/079-liveness-probe-restart-wedged-vm.md`); webhook replay → 200 (idempotent, `docs/adr/042-webhook-replay-protection.md`); session replay → 401 `CodeSessionExpired` (`docs/adr/039-server-side-session-revocation.md`). Operator escalation: `docs/runbooks/` per-family (e.g. `FaasDaemonDown.md`, `FaasFailedLoginSpike.md`). | code + runbook | per-event | Each runbook has a documented response procedure + verification command. |
| CC7.5 | The entity identifies, develops, and implements activities to recover from identified security incidents. | Postgres backup to Hetzner Storage Box via rclone (encrypted with host X25519 age recipient, `docs/adr/020-customer-secrets.md`). Backup verify via `pgbackrest check`. **Quarterly DR drill** — `docs/runbooks/disaster-recovery-drill.md` (PR-8 of issue #755). | runbook | per-quarter | Dated DR drill artifact references this control. |

### CC8 — Change management

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC8.1 | The entity authorizes, designs, develops or acquires, configures, documents, tests, approves, and implements changes to infrastructure, data, software, and procedures. | Branch protection (CC7.1) + ADR-first workflow (CLAUDE.md) + table-driven tests + property-based tests for §6.2 invariants. Every PR names the milestone; architecture changes name an ADR. | policy + code | per-PR | `gh pr list --state merged --limit 100 --json title,body` shows ADR-first cadence. |

### CC9 — Risk mitigation

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| CC9.1 | The entity identifies, selects, and develops risk mitigation activities. | `docs/compliance/vendor-risk-management.md` (PR-10); per-vendor assessments in `docs/compliance/vendor-assessments/` (PR-10). | policy | per-year | Each critical-tier vendor has a dated assessment file. |
| CC9.2 | The entity assesses and manages risks associated with vendors and business partners. | `docs/compliance/subprocessors.md` + `subprocessors.json` (PR-3); 30-day notice enforced by `subprocessor-check` CI gate. DPA per vendor (`docs/DPA.md` §7). | policy + code | per-change | CI gate `subprocessor-check` green. |

---

## Availability (A series)

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| A1.1 | The entity maintains, monitors, and evaluates current processing capacity. | `resident_ram_pct_of_target` gauge (spec §12, alert at 80 % warn / 92 % page); `vmmd_egress_deny_total`; `lv_fc_used_pct`; `schedd_instance_cpu_pct`. Spec §13 RAM budget ledger (`faas-tenant.slice` 57,344 MB hard fence; schedd admits only to 47,600 MB = 85 %). | metric | continuous | `FaasHighResidentRam` + `FaasCpuStarvation` runbooks. |
| A1.2 | The entity authorizes, designs, develops or acquires, implements, operates, approves, maintains, and monitors environmental protections, software, data backup processes, and recovery infrastructure to meet its objectives. | Postgres `pgbackrest` daily backup to Hetzner Storage Box (encrypted at rest with host X25519). DR drill quarterly (`docs/runbooks/disaster-recovery-drill.md`, PR-8). Multi-node HA roadmap: `docs/adr/062-tier-a-per-node-schedd-and-placement.md` (per-node schedd) + `docs/adr/066-tier-a5-cross-node-live-migration.md` (cross-node live migration) — M9 acceptance gate ships two-node active-active. **GAP (pre-M9):** single-node blast radius. | runbook | per-quarter | Dated DR drill artifact; `docs/scale_out_and_workload_classes.md` Phase 2 plan. |
| A1.3 | The entity tests recovery plan procedures. | `docs/runbooks/disaster-recovery-drill.md` (PR-8). Acceptance: PG + one app back serving on a clean VM < 30 min, documented as executed (spec §14 M8). | runbook | per-quarter | Quarterly drill artifact references this control. |

---

## Confidentiality (C series)

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| C1.1 | The entity identifies and maintains confidential information to meet its objectives. | Customer secrets sealed with X25519 host age key (`docs/adr/020-customer-secrets.md`), injected into `/etc/faas/secrets.env` on every wake, never in snapshots of other deployments. Plaintext VALUES never touch Postgres. TLS 1.3 in transit; HSTS at edge. | code | continuous | `pkg/secretbox` unit tests; DPA §8 cross-reference. |
| C1.2 | The entity disposes of, retains, and protects confidential information to meet its objectives. | GDPR self-serve endpoints (issue #756, Phase B of #755 plan): `GET /v1/account/export`, `DELETE /v1/account` (30-day staged deletion, `docs/adr/021-account-export-and-staged-deletion.md`), `POST /v1/account/cancel-deletion`. Hard-delete sweep + `audit_log` table (PR-6). Audit rows: `account.deletion_scheduled` / `account.deletion_restored` (spec §5.1) + the post-grace hard-delete row (see ADR-021). | code + schema | per-event | `SELECT count(*) FROM events WHERE kind IN ('account.deletion_scheduled','account.deletion_restored')` panel. |

---

## Processing Integrity (PI series)

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| PI1.1 | The entity obtains and uses relevant, quality data to meet its objectives. | `usage API == invoice` invariant (spec §10); `Provider.PushUsageRecord` extension is the only billing-write seam. `events.kind = 'credit.consumed'` per drained credit (FIFO). Idempotent on `(subscription_item, hour)` (spec §10). Stripe usage records idempotent on the same key. | code + audit-log | per-day | `BillingDrift.md` runbook; meterd reconciliation alert. |

---

## Privacy (P series)

Gregale is a Processor (B2B platform). Controller obligations remain with the
customer. The Privacy criteria are mapped to `docs/DPA.md` and the issue #755
plan rather than as separate in-repo controls.

| TSC ID | Control | Gregale evidence | Evidence type | Frequency | Verification |
|---|---|---|---|---|---|
| P1.1 | The entity provides notice to data subjects about its privacy practices. | `docs/DPA.md` (Art. 13 notice); customer-facing `/dpa` URL. | policy | per-customer | DPA signed per customer (PR-7: `POST /v1/account/dpa/sign` audit row). |
| P2.1 | The entity communicates choices available regarding the collection, use, retention, and disclosure of personal information. | DPA §6 (data subject rights endpoint table); `GET /v1/account/export`; `DELETE /v1/account`. | policy + code | per-event | #756 acceptance criteria. |
| P3.1 | The entity collects personal information consistent with its objectives. | DPA §2 (nature + purpose); §3 (categories of data subjects); §4 (categories of personal data). | policy | per-DPA | DPA signed. |
| P3.2 | The entity explicitly agrees with the data subject on the use of personal information. | DPA Article 28 contract; `dpa.signed{dpa_sha256, signed_at}` audit row. | audit-log | per-customer | Audit table query. |
| P4.1 | The entity limits the use of personal information to the purposes identified in the entity's objectives. | DPA §5 (Processor obligations — only on documented instructions); `events` table scope per §5.1. | policy + audit-log | continuous | DPA + audit log. |
| P5.1 | The entity retains personal information consistent with its objectives. | DPA §1 (tax-invoice retention 7 years); DPA §11 (data return on termination); hard-delete sweep (PR-6). | policy | per-event | DR drill + audit_log. |
| P5.2 | The entity protects personal information against unauthorized access, use, or disclosure. | DPA §8 (Art. 32 security measures); per-instance netns; sealed secrets; cosign signature enforcement. | policy + code | continuous | See CC6.6, CC6.7, CC6.8 rows above. |
| P6.1 | The entity provides data subjects with the ability to access their personal information for review and update. | `GET /v1/account/export` (right of access, Art. 15); `PATCH /v1/account` (right to rectification, Art. 16). | code | per-request | #756 acceptance criteria. |
| P6.2 | The entity provides data subjects with the ability to request the deletion of their personal information. | `DELETE /v1/account` (right to erasure, Art. 17, 30-day grace); `POST /v1/account/cancel-deletion` (right to restriction, Art. 18). | code | per-request | #756 acceptance criteria. |
| P6.3 | The entity provides data subjects with the ability to obtain their personal information in a portable format. | `GET /v1/account/export` returns NDJSON bundle (right to portability, Art. 20) — streamable, idempotent, rate-limited. | code | per-request | #756 acceptance criteria. |
| P6.4 | The entity provides data subjects with the ability to communicate choices regarding the collection, use, retention, and disclosure of their personal information. | DPA §6 (right to object, Art. 21) — contact `support@gregale.dev`; right to lodge a complaint (Art. 77). | policy | per-customer | DPA signed. |
| P6.5 | The entity implements a process to address data subject complaints. | DPA §6 (right to object + complaint); `support@gregale.dev` SLA per `docs/compliance/responsible-disclosure.md`. | policy | per-complaint | SLA in `responsible-disclosure.md`. |
| P6.6 | The entity provides data subjects with notification of breaches and incidents. | DPA §10 (72-hour breach notification, Art. 33). | policy | per-incident | Breach notification template (operator-side). |
| P6.7 | The entity provides data subjects with the ability to participate in the design and implementation of privacy controls. | DPA §9 (audit rights — Controller may audit on 30-day notice). | policy | per-customer | DPA signed. |

---

## Open items

These are the gap items that block the SOC 2 Type 1 as-of date. Each is
tracked as a PR in the [issue #755 implementation plan](https://github.com/poyrazK/faas/issues/755#issuecomment-5225012070).

| Item | PR | Blocks Type 1? |
|---|---|---|
| `docs/compliance/iso27001-statement-of-applicability.md` | PR-2 | Yes |
| `docs/compliance/subprocessors.md` + `subprocessors.json` + `subprocessor-check` CI gate | PR-3 | Yes |
| `docs/compliance/responsible-disclosure.md` + `SECURITY.md` + `security@gregale.dev` mailbox | PR-4 | Yes |
| GDPR self-serve endpoints (`GET /v1/account/export` + `DELETE /v1/account`) | PR-5 (issue #756) | Yes (P6.1–P6.3) |
| `audit_log` table + hard-delete sweep | PR-6 | Yes (P5.1) |
| DPA publication path + `POST /v1/account/dpa/sign` | PR-7 | Yes (P1.1, P3.2) |
| First DR drill + `docs/runbooks/disaster-recovery-drill.md` | PR-8 | Yes (A1.2, A1.3, CC7.5) |
| `docs/compliance/access-review.sql` + `access-review.md` | PR-9 | Yes (CC5.1) |
| `docs/compliance/vendor-risk-management.md` + first 3 vendor assessments | PR-10 | Yes (CC9.1, CC9.2) |
| Infosec policy + code of conduct + training Loom | PR-11 | Yes (CC1.1) |
| Vanta or direct-auditor engagement + Type 1 report issued | E1–E3 | Yes (the audit itself) |
| Multi-node HA (ADR-062 + ADR-066, M9 acceptance gate) | not in this issue's scope | **Type 2 only** |

---

## Cross-references

- `docs/faas_implementation_spec.md` §5.1 (audit event taxonomy), §11 (security hardening checklist), §12 (observability SLOs), §13 (RAM budget ledger), §17 (known gaps register).
- `docs/DPA.md` (Data Processing Addendum template).
- `docs/adr/020-customer-secrets.md`, `021-account-export-and-staged-deletion.md`, `035-auth-audit-events.md`, `039-server-side-session-revocation.md`, `040-per-account-rate-limit.md`, `042-webhook-replay-protection.md`, `058-cosign-deploy-time-enforcement.md` (issue #472), `061-organizations-and-memberships.md`, `077-step-up-mfa.md`, `079-liveness-probe-restart-wedged-vm.md`.
- `pkg/audit/` (audit emission seam), `pkg/auth/` (authentication seam).
- `docs/runbooks/` (per-family alert runbooks).
- `docs/compliance/` sibling documents (subprocessors, vendor risk management, responsible disclosure, access review, etc.).
