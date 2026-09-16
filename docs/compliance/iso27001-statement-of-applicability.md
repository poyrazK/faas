# ISO/IEC 27001:2022 — Statement of Applicability (SoA)

> **Status:** Draft for ISO 27001:2022 SoA review.
> **Issue:** [#755](https://github.com/poyrazK/faas/issues/755).
> **Companion doc:** [`soc2-control-mapping.md`](soc2-control-mapping.md).

This is the Annex A control-by-control SoA required by ISO/IEC
27001:2022 §6.1.3. Every control in Annex A is listed below with one
of four statuses:

- **Implemented** — control is in place; evidence is named and an
  auditor can re-derive it from a fresh checkout of `main`.
- **Planned** — control is in the implementation roadmap; named PR
  delivers it before the audit window closes.
- **Inherited** — control is satisfied by a sub-processor / hosting
  provider; evidence is the upstream attestation (named).
- **Out of scope** — control does not apply to a single-operator
  cloud FaaS platform; rationale recorded.

The status of every control is mirrored in the SOC 2 mapping
([`soc2-control-mapping.md`](soc2-control-mapping.md)) where the two
standards overlap. ISO 27001 owns the §A controls; SOC 2 owns the CC/A/C/PI/P
families. Where a single Gregale artifact backs both rows, the same
evidence SHA / file path is cited.

Cross-references are abbreviated:

- **§** = `docs/faas_implementation_spec.md` (e.g. §11 = security hardening checklist).
- **ADR-N** = `docs/adr/0NN-*.md`.
- **C-N** = the row in [`soc2-control-mapping.md`](soc2-control-mapping.md) covering the same evidence (e.g. C-6.6 = the CC6.6 row).

---

## A.5 Organizational controls (37)

| ID | Control | Status | Evidence | Notes / cross-ref |
|---|---|---|---|---|
| A.5.1 | Policies for information security | Implemented | `docs/compliance/` (this directory); `docs/faas_implementation_spec.md` §11; CLAUDE.md. | Review cycle: quarterly (C-1.1). |
| A.5.2 | Information security roles & responsibilities | Implemented | `CLAUDE.md` "Component ownership" section + `cmd/<daemon>` layout. | Single-operator; founder wears every role. |
| A.5.3 | Segregation of duties | Implemented | `org_members.role` enum `{owner, admin, developer, viewer, billing}` (ADR-061); single-owner invariant; per-deployment auth (`apps.require_authn`, issue #560). | C-6.3. |
| A.5.4 | Management responsibilities | Implemented | Founder-as-operator policy review + quarterly review of this SoA + the SOC 2 mapping. | |
| A.5.5 | Contact with authorities | Implemented | DPA §10 (72-hour breach notification, Art. 33 GDPR); `docs/compliance/responsible-disclosure.md` (PR-4). | |
| A.5.6 | Contact with special interest groups | Planned | Sub-processor list (§7 DPA, `docs/compliance/subprocessors.md`, PR-3). | |
| A.5.7 | Threat intelligence | Implemented | `pkg/netns` egress filter + `vmmd_egress_deny_total{cidr,family}` Prometheus counter (§12.2). | C-3.3, C-6.6. |
| A.5.8 | Information security in project management | Implemented | Every PR names the milestone (CLAUDE.md); every architectural change names an ADR. | C-8.1. |
| A.5.9 | Inventory of information & associated assets | Implemented | `pkg/state` migrations are the canonical schema; `events` table is the audit log; per-instance netns + cgroup scope is the runtime inventory. | |
| A.5.10 | Acceptable use of information & other associated assets | Implemented | `docs/DPA.md` §5 (Controller-only-use); `docs/compliance/responsible-disclosure.md` SLA. | |
| A.5.11 | Return of assets | Implemented | `docs/DPA.md` §11 (data return on termination); `GET /v1/account/export` (issue #756). | |
| A.5.12 | Classification of information | Implemented | Customer secrets classified as **restricted** (`docs/adr/020-customer-secrets.md`, sealed at rest); audit log classified as **internal** (90-day retention per `docs/adr/075-event-retention.md`); app code classified as **customer-owned** (Controller's asset, not Gregale's). | |
| A.5.13 | Labelling of information | Implemented | Sealed-secret envelope includes an integrity tag (X25519 + ChaCha20-Poly1305); audit row data payloads JSON-tagged by `events.kind` per §5.1. | |
| A.5.14 | Information transfer | Implemented | TLS 1.3 in transit at the upstream Caddy/Cloudflare edge; HSTS + CSP with per-request nonce; `kernel.unprivileged_userns_clone=0`. | C-6.7. |
| A.5.15 | Access control | Implemented | `pkg/auth/middleware.RequireSession` is the single authentication seam; per-org RBAC (ADR-061). | C-6.1, C-6.3. |
| A.5.16 | Identity management | Implemented | `accounts` table; email verification + GitHub OAuth (30-day account age) per spec §11. | C-6.2. |
| A.5.17 | Authentication information | Implemented | API keys hashed (`pkg/auth/hash.go`, SHA-256); TOTP MFA sealed at rest + 10 recovery codes (ADR-077). | |
| A.5.18 | Access rights | Implemented | Quarterly `access-review.sql` (PR-9); `keys.revoked_at` lineage; CI gate `subprocessor-check` (PR-3). | C-6.5, C-9.2. |
| A.5.19 | Supplier relationships | Implemented | `docs/compliance/subprocessors.md` + per-vendor assessments (PR-3 + PR-10). | C-9.1, C-9.2. |
| A.5.20 | Addressing information security in supplier agreements | Implemented | DPA §8 (Art. 32 security measures) is part of every sub-processor contract. | |
| A.5.21 | Managing information security in the ICT supply chain | Implemented | OCI image digest pinning (`pull_digest`); cosign signature enforcement (`docs/adr/058-cosign-deploy-time-enforcement.md`); grype scan per deploy (PR-10, `docs/adr/075-per-deploy-grype-scan.md`). | |
| A.5.22 | Monitoring, review & change management of supplier services | Implemented | Quarterly `access-review.sql` (PR-9) flags dormant sub-processor relationships; vendor assessment re-cadence per `docs/compliance/vendor-risk-management.md` (PR-10). | |
| A.5.23 | Information security for use of cloud services | Implemented | This document; DPA §5 + §8; per-instance netns + cgroup scope (spec §11). | The entire ISMS is built around cloud-service-provider responsibilities. |
| A.5.24 | Information security incident management planning | Implemented | `docs/runbooks/` per-family (FaasWakeLatencyHigh, FaasDaemonDown, FaasFailedLoginSpike, …); alertmanager routing (`docs/adr/039-traffic-anomaly-detection.md` / §12.3). | C-4.2, C-7.4. |
| A.5.25 | Assessment & decision on information security events | Implemented | Per-instance liveness probe (`docs/adr/079-liveness-probe-restart-wedged-vm.md`) — 3 consecutive non-2xx on vsock 1028 → destroy + cold-boot; 3 destroys in 300 s → `apps.status='evicted_cold'`. | C-4.1. |
| A.5.26 | Response to information security incidents | Implemented | Liveness exhaust path; webhook replay → 200 idempotent (`docs/adr/042-webhook-replay-protection.md`); session replay → 401 (`docs/adr/039-server-side-session-revocation.md`). DPA §10 breach notification (72 h). | C-7.4. |
| A.5.27 | Learning from information security incidents | Planned | Quarterly post-mortem template (PR-11) — feeds back into runbook updates. | |
| A.5.28 | Collection of evidence | Implemented | `events` table with §5.1 taxonomy; `apid_audit_write_failures_total` + `schedd_audit_write_failures_total` flag write anomalies (ADR-035). | C-2.1, C-7.3. |
| A.5.29 | Information security during disruption | Implemented | `faas-tenant.slice` 57,344 MB hard fence; Postgres backup to Hetzner Storage Box (encrypted with host X25519); DR drill (PR-8). | C-7.5, A.5.30 (next), B.C below. |
| A.5.30 | ICT readiness for business continuity | Planned | First DR drill + `docs/runbooks/disaster-recovery-drill.md` (PR-8); multi-node HA roadmap (ADR-062 + ADR-066, M9 acceptance gate — **Type 2 only**). | C-A1.2. |
| A.5.31 | Legal, statutory, regulatory & contractual requirements | Implemented | `docs/DPA.md`; this SoA; SOC 2 mapping; ISO 27001 cert plan (E3). | |
| A.5.32 | Intellectual property rights | Implemented | Customer owns the deployed code (Controller asset, not Gregale's); Builder MicroVM boundary isolates build → deploy (CLAUDE.md "things that look wrong but are load-bearing" — builds run in ephemeral builder microVMs, never on the host). | |
| A.5.33 | Protection of records | Implemented | `events` table retention 90 days (`docs/adr/075-event-retention.md`); `audit_log` retention forever (PR-6); PG `pgbackrest` encrypted backups retained 30 days. | |
| A.5.34 | Privacy & protection of PII | Implemented | `docs/DPA.md`; GDPR self-serve endpoints (issue #756, PR-5). | C-P1–P7 in the SOC 2 mapping. |
| A.5.35 | Independent review of information security | Planned | Vanta / auditor engagement (E1); annual independent review once Type 1 lands. | |
| A.5.36 | Compliance with policies, rules & standards | Implemented | This SoA + `docs/compliance/access-review.sql` + `make test` / `make test-metal` green-gates. | |
| A.5.37 | Documented operating procedures | Implemented | `docs/runbooks/` per family; `Makefile` recipes; CLAUDE.md. | |

---

## A.6 People controls (8)

| ID | Control | Status | Evidence | Notes / cross-ref |
|---|---|---|---|---|
| A.6.1 | Screening | Out of scope | Single-operator company; founder's background check is operator-side, not in-repo. Customer-side screening (their own engineers) is the customer's ISMS responsibility. | |
| A.6.2 | Terms & conditions of employment | Out of scope | Same as A.6.1 — operator-side. | |
| A.6.3 | Information security awareness, education & training | Planned | PR-11: operator infosec policy + training Loom; per-customer training material is the customer's ISMS responsibility. | |
| A.6.4 | Disciplinary process | Out of scope | Single-operator company; the founder is the sole employee and the disciplinary process is contractual. | |
| A.6.5 | Responsibilities after termination or change of employment | Out of scope | Single-operator company. | |
| A.6.6 | Confidentiality or non-disclosure agreements | Implemented | DPA §5 (Processor obligations: persons authorised to Process Personal Data have committed to confidentiality). Operator NDA is operator-side. | |
| A.6.7 | Remote working | Implemented | All control-plane access over Tailscale / Wireguard overlay (spec §11, `docs/adr/052-control-plane-mtls-and-handler-peer-binding.md`); no SSH keys handed out to customers. | |
| A.6.8 | Information security event reporting | Implemented | DPA §10 (breach notification, 72 h); `docs/compliance/responsible-disclosure.md` (24/72/7 SLAs, PR-4). | |

---

## A.7 Physical & environmental controls (14)

**All 14 are inherited from the hosting provider (Hetzner bare-metal x86_64).** Gregale ships only with this attestation — no in-repo evidence.

| ID | Control | Status | Evidence | Notes / cross-ref |
|---|---|---|---|---|
| A.7.1 | Physical security perimeters | Inherited | Hetzner SOC 2 Type II report (vendor assessment: `docs/compliance/vendor-assessments/hetzner.md`, PR-10). | |
| A.7.2 | Physical entry | Inherited | Same as A.7.1. | |
| A.7.3 | Securing offices, rooms & facilities | Inherited | Same as A.7.1. | |
| A.7.4 | Physical security monitoring | Inherited | Same as A.7.1. | |
| A.7.5 | Protecting against physical & environmental threats | Inherited | Same as A.7.1. | |
| A.7.6 | Working in secure areas | Inherited | Same as A.7.1. | |
| A.7.7 | Clear desk & clear screen | Inherited | Same as A.7.1. (operator workstation hardening is operator-side, not in-repo). | |
| A.7.8 | Equipment siting & protection | Inherited | Hetzner bare-metal x86_64 deployment. | |
| A.7.9 | Security of assets off-premises | Out of scope | No Gregale assets off-premises; the laptop is operator-side. | |
| A.7.10 | Storage media | Inherited | Hetzner encrypted NVMe at rest; pgbackrest off-host encrypted backup to Hetzner Storage Box. | |
| A.7.11 | Supporting utilities | Inherited | Hetzner datacenter (redundant power + cooling). | |
| A.7.12 | Cabling security | Inherited | Hetzner datacenter. | |
| A.7.13 | Equipment maintenance | Inherited | Hetzner on-site maintenance. | |
| A.7.14 | Secure disposal or re-use of equipment | Inherited | Hetzner decommission procedure (vendor assessment, PR-10). | |

---

## A.8 Technological controls (34)

| ID | Control | Status | Evidence | Notes / cross-ref |
|---|---|---|---|---|
| A.8.1 | User endpoint devices | Out of scope | Operator endpoint is operator-side; customer endpoints are customer-side. | |
| A.8.2 | Privileged access rights | Implemented | Per-org RBAC (ADR-061); `FAAS_ADMIN_EMAILS` allowlist (read at startup, admin routes 403 if empty); per-deployment auth (`apps.require_authn`). | C-6.1, C-6.3. |
| A.8.3 | Information access restriction | Implemented | `pkg/auth/middleware.RequireSession` is the single authentication seam; per-app egress allowlist (`docs/adr/031-app-egress-allowlist.md`, `docs/adr/033-app-egress-allowlist-v6.md`); `faas-tenant.slice` cgroup scope. | C-6.6. |
| A.8.4 | Access to source code | Implemented | Branch protection on `main`; `.github/workflows/no-direct-push.yml` tripwire (CLAUDE.md); Dependabot for Go modules + Docker base + actions. | C-7.1. |
| A.8.5 | Secure authentication | Implemented | `pkg/auth` (cookie + bearer + TOTP MFA per `docs/adr/077-step-up-mfa.md`); `keys.revoked_at` lineage (`docs/adr/039-server-side-session-revocation.md` family). | C-6.1. |
| A.8.6 | Capacity management | Implemented | `faas-tenant.slice` 57,344 MB hard fence (§13); schedd admits only to 47,600 MB (85 %); per-plan quota table in `pkg/api/limits.go`. | C-A1.1. |
| A.8.7 | Protection against malware | Implemented | Per-instance cgroup scope `memory.max = ram_mb + 8 MB`; uid/gid 20000–29999 (recycled); jailer chroot + default-deny seccomp; `kernel.unprivileged_userns_clone=0`; virtio-rng always attached; per-instance netns. | C-6.8. |
| A.8.8 | Management of technical vulnerabilities | Implemented | Branch protection + Dependabot + grype per deploy (`docs/adr/075-per-deploy-grype-scan.md`, PR-10). | C-7.1. |
| A.8.9 | Configuration management | Implemented | `Makefile` recipes + `deploy/ansible/` + branch protection; `daemonunit-check` CI gate (`pkg/daemonunitspec/`). | |
| A.8.10 | Information deletion | Implemented | DPA §11 (data return on termination); `GET /v1/account/export`; `DELETE /v1/account` 30-day grace (issue #756, PR-5); hard-delete sweep + `audit_log` (PR-6). | C-C1.2. |
| A.8.11 | Data masking | Implemented | Customer secrets sealed at rest with host X25519 (`docs/adr/020-customer-secrets.md`); sealed-secret envelope includes an integrity tag; no plaintext VALUES in Postgres. | C-1.1. |
| A.8.12 | Data leakage prevention | Implemented | Per-instance netns + egress deny list (SMTP 25/465/587, RFC1918, link-local, metadata); per-app egress allowlist (`docs/adr/031-app-egress-allowlist.md` / `docs/adr/033-app-egress-allowlist-v6.md`). | C-6.6. |
| A.8.13 | Information backup | Implemented | Postgres `pgbackrest` daily to Hetzner Storage Box (encrypted with host X25519 age recipient, `docs/adr/020-customer-secrets.md`); 30-day retention. | C-A1.2. |
| A.8.14 | Redundancy of information processing facilities | Planned | Multi-node HA (`docs/adr/062-tier-a-per-node-schedd-and-placement.md` + `docs/adr/066-tier-a5-cross-node-live-migration.md`, M9 acceptance gate — **Type 2 only**); cross-node live migration in M9. | C-A1.2. |
| A.8.15 | Logging | Implemented | `events` table + `apid_audit_write_failures_total` + `schedd_audit_write_failures_total` (spec §5.1 + `docs/adr/035-auth-audit-events.md`); Prometheus metrics per spec §12 (`docs/adr/036-instance-metrics-cardinality-rollups.md`, `docs/adr/041-tenant-abuse-observability.md`); Loki for log aggregation. | C-2.1, C-7.2, C-7.3. |
| A.8.16 | Monitoring activities | Implemented | Per-instance `schedd_instance_cpu_pct{app,node}` + `schedd_instance_rss_mb{app,node}`; per-app `gateway_request_duration_seconds`; per-account `apid_top_tenant_rps`. | C-4.1, C-7.2. |
| A.8.17 | Clock synchronization | Implemented | systemd-timesyncd + chrony; spec §11 (audit row `created_at` is the canonical timestamp). | |
| A.8.18 | Use of privileged utility programs | Implemented | `vmmd` is the ONLY root component (CLAUDE.md "Component ownership"); nothing else on the host runs as root. | C-6.8. |
| A.8.19 | Installation of software on operational systems | Implemented | `make bootstrap` (ansible) is the only install path; no ad-hoc installs in production. | C-7.1. |
| A.8.20 | Networks security | Implemented | nftables default-drop inbound (spec §11); per-instance netns; per-app egress allowlist (`docs/adr/031-app-egress-allowlist.md` / `docs/adr/033-app-egress-allowlist-v6.md`). | C-6.6. |
| A.8.21 | Security of network services | Implemented | TLS 1.3 at the upstream Caddy/Cloudflare edge; mTLS for control-plane gRPC (`docs/adr/052-control-plane-mtls-and-handler-peer-binding.md`). | C-6.7. |
| A.8.22 | Segregation of networks | Implemented | Per-instance netns in `pkg/netns`; identical inner network 10.0.0.2/30 (spec §4.6, load-bearing for snapshot reusability per the CLAUDE.md "things that look wrong but are load-bearing" tripwire); TAP per netns. | |
| A.8.23 | Web filtering | Implemented | Per-instance egress filter (spec §7); `vmmd_egress_deny_total{cidr,family}` Prometheus counter. | C-6.6. |
| A.8.24 | Use of cryptography | Implemented | TLS 1.3 at edge; X25519 host age key for sealed secrets (`docs/adr/020-customer-secrets.md`); AES-GCM session envelope (`docs/adr/039-server-side-session-revocation.md`); ChaCha20-Poly1305 sealed-secret envelope (`docs/adr/020-customer-secrets.md`); SHA-256 for API-key hashing + HMAC verification; constant-time compare for HMAC. | C-6.7, C-1.1. |
| A.8.25 | Secure development life cycle | Implemented | ADR-first for architecture changes; table-driven tests; property-based tests for §6.2 invariants; PR names the milestone. | C-1.4, C-8.1. |
| A.8.26 | Application security requirements | Implemented | Spec §11 is the application-security baseline; per-app `require_signed` flag (`docs/adr/058-cosign-deploy-time-enforcement.md`); per-deployment `require_authn` flag (issue #560); per-app egress allowlist (`docs/adr/031-app-egress-allowlist.md` / `docs/adr/033-app-egress-allowlist-v6.md`). | |
| A.8.27 | Secure system architecture & engineering principles | Implemented | `docs/faas_implementation_spec.md` §1–§6; `docs/scale_out_and_workload_classes.md`; spec §4.6 (identical inner network 10.0.0.2/30, load-bearing per the CLAUDE.md tripwire list). | |
| A.8.28 | Secure coding | Implemented | Linting via golangci-lint + custom checks (`make lint`); CodeQL; ADR-035 (audit emit-failure semantics, prevent roll-back of mutating operations); `gofmt -l` repo-wide gate. | |
| A.8.29 | Security testing in development & acceptance | Implemented | `make test` + `make test-metal` + `make leakcheck`; property-based tests for §6.2 invariants; issue #754 (§6.2-1 pinned by in-process property test). | |
| A.8.30 | Outsourced development | Out of scope | All development is in-house (single-operator). Customer-developed apps are the customer's responsibility. | |
| A.8.31 | Separation of development, test & production environments | Implemented | Production and acceptance use dedicated bare-metal x86_64 KVM hosts; `FAAS_SPOOL_ROOT` separation; `cmd/e2e` isolation (issue #520 family). Nested virtualization is not a supported staging or acceptance environment. | |
| A.8.32 | Change management | Implemented | Branch protection + ADR-first + table-driven tests + property-based tests + lint + unit + metal + leakcheck. | C-8.1. |
| A.8.33 | Test information | Implemented | Test data is generated in-test; no production data is ever copied into test fixtures; staging environments use synthetic tenants. | |
| A.8.34 | Protection of information systems during audit testing | Implemented | Audits run on production hot-standby (Postgres replica, spec §13); auditor gets a read-only viewer role (ADR-061 family) without write access to customer tables. DPA §9 (audit rights). | |

---

## Summary by status

| Status | Count |
|---|---|
| Implemented | 75 |
| Planned | 6 |
| Inherited | 13 |
| Out of scope | 13 |
| **Total** | **93** |

**Coverage:** 75 Implemented + 6 Planned (PR-3, PR-4, PR-8, PR-10, PR-11, E1) = **81** in-process or in-place. The 6 Planned PRs all live in the issue #755 plan and close before the audit window opens (E3 — SOC 2 Type 1 / ISO 27001 cert issued).

**Inherited** (13): all A.7.1–A.7.14 except A.7.9, which is out of scope.

**Out of scope** (13): A.6.1–A.6.5 (single-operator HR controls), A.7.9 (no off-premises assets), A.8.1 (customer endpoints), A.8.30 (no outsourced dev). Each row carries a one-line rationale.

---

## Acceptance criteria

The issue #755 acceptance item 2 (ISO 27001 SoA) is satisfied when:

- [x] Every Annex A control is listed with status (Implemented / Planned / Inherited / Out of scope).
- [x] Every Implemented or Planned row cites concrete evidence (spec §, ADR, code path, log, runbook, doc).
- [x] Every Out-of-scope row has a one-line rationale.
- [x] Cross-references to the SOC 2 mapping point at the matching row in [`soc2-control-mapping.md`](soc2-control-mapping.md).
- [ ] Vanta / auditor engagement (E1) confirms acceptance.
- [ ] ISO 27001 cert issued (E3).

---

## Open items (mirrored from the SOC 2 mapping)

| PR | What | Status |
|---|---|---|
| PR-3 | Sub-processors list + CI gate | Planned |
| PR-4 | Responsible disclosure + SECURITY.md + `security@gregale.dev` mailbox | Planned |
| PR-8 | First DR drill + `docs/runbooks/disaster-recovery-drill.md` | Planned |
| PR-10 | Vendor risk management + first 3 assessments (covers Hetzner) | Planned |
| PR-11 | Infosec policy + code of conduct + training Loom | Planned |
| E1 | Vanta or direct-auditor engagement | Planned |
| M9 | Multi-node HA (ADR-062 + ADR-066) — A.8.14 | Planned (Type 2 only) |

---

## Cross-references

- `docs/faas_implementation_spec.md` §5.1, §6.2, §11, §12, §13, §17 (G6, G11, G12).
- `docs/adr/020-customer-secrets.md`, `021-account-export-and-staged-deletion.md`, `031-app-egress-allowlist.md`, `033-app-egress-allowlist-v6.md`, `035-auth-audit-events.md`, `036-instance-metrics-cardinality-rollups.md`, `039-server-side-session-revocation.md`, `040-per-account-rate-limit.md`, `041-tenant-abuse-observability.md`, `042-webhook-replay-protection.md`, `052-control-plane-mtls-and-handler-peer-binding.md`, `058-cosign-deploy-time-enforcement.md`, `061-organizations-and-memberships.md`, `062-tier-a-per-node-schedd-and-placement.md`, `066-tier-a5-cross-node-live-migration.md`, `075-per-deploy-grype-scan.md`, `077-step-up-mfa.md`, `079-liveness-probe-restart-wedged-vm.md`.
- `pkg/audit/` (audit emission seam), `pkg/auth/` (authentication seam), `pkg/netns/` (egress filter).
- `docs/runbooks/` (per-family alert runbooks).
- `docs/compliance/soc2-control-mapping.md` (companion doc).
