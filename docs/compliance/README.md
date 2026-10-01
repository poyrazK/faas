# Compliance

This directory holds the evidence, policies, and procedures that back Gregale's
security and privacy attestations (SOC 2 Type 1, ISO 27001, GDPR DPA).

## Index

| Doc | Purpose | Owner | Status |
|---|---|---|---|
| [soc2-control-mapping.md](soc2-control-mapping.md) | Maps every SOC 2 Trust Services Criteria (TSC) control to the artifact in the codebase that satisfies it. | Platform | Draft |
| [iso27001-statement-of-applicability.md](iso27001-statement-of-applicability.md) | ISO/IEC 27001:2022 Annex A — Applicable / Not-applicable per control + rationale. | Platform | Draft |
| [csa-ccm-v4.1-readiness.md](csa-ccm-v4.1-readiness.md) | Gregale-authored, high-level evidence and gap index for CSA CCM / CAIQ v4.1. | Platform + Security | Alignment draft |
| [csa-ccm-v4.1-evidence-ledger-template.csv](csa-ccm-v4.1-evidence-ledger-template.csv) | Empty Gregale-authored schema for a restricted control-level evidence ledger; populate only after confirming licensing. | Security | Template; licensing review required |
| [iso27017-2026-readiness.md](iso27017-2026-readiness.md) | Gregale-authored cloud-service evidence and gap index for the ISO/IEC 27017:2026 review. | Platform + Security | Alignment draft; authorized clause review outstanding |
| [subprocessors.md](subprocessors.md) | Public sub-processor list with category, data, region, DPA reference. | Platform + Legal | Draft |
| [subprocessors.json](subprocessors.json) | Source-of-truth JSON for the sub-processor list; `subprocessor-check` CI gate renders `subprocessors.md` from this. | Platform | Draft |
| [subprocessor-archive.json](subprocessor-archive.json) | Removed sub-processors with effective date + removal reason. | Platform | Draft |
| [responsible-disclosure.md](responsible-disclosure.md) | Public security disclosure policy + 24/72/7-day SLAs + PGP. | Security | Draft |
| [../../SECURITY.md](../../SECURITY.md) | Repo-root mirror of `responsible-disclosure.md` (GitHub convention). | Security | Draft |
| [../../.well-known/security.txt](../../.well-known/security.txt) | RFC 9116 security.txt for tool-driven discovery. | Security | Draft |
| [vendor-risk-management.md](vendor-risk-management.md) | Gregale-authored supplier tiers, evidence criteria, review cadence, and decision process. | Security | Policy draft; assessments open |
| [vendor-assessments/README.md](vendor-assessments/README.md) | Public index for critical-supplier workpapers and pre-onboarding candidates; confidential reports and decisions stay in restricted storage. | Security | Evidence collection required |
| [vendor-assessments/managed-postgres-supplier-approval.example.json](vendor-assessments/managed-postgres-supplier-approval.example.json) | Operator decision record shape enforced by the managed-PostgreSQL provisioning gate. | Platform + Security | Template; completed copies stay in restricted storage |
| [access-review.sql](access-review.sql) | Read-only quarterly inventory of active org memberships, API keys, sessions, pending invitations, and access audit events. | Security | First review outstanding |
| [access-review.md](access-review.md) | Procedure for running the database inventory, reviewing operator access, handling findings, and retaining restricted evidence. | Security | First review outstanding |
| [access-review-record-template.md](access-review-record-template.md) | Sign-off record for a completed quarterly review; production copies belong in restricted evidence storage. | Security | Template |

## Companion documents outside `docs/compliance/`

- `docs/DPA.md` — the in-repo Data Processing Addendum template. Production binding lives at `/etc/faas/dpa.md` (rendered from a template, single source of truth).
- `docs/faas_implementation_spec.md` §5.1 (audit event taxonomy), §11 (security hardening checklist), §12 (observability SLOs), §17 (known gaps register — GDPR G6 lives there).
- `docs/adr/020-customer-secrets.md` (sealed secrets), `021-account-export-and-staged-deletion.md` (GDPR endpoints), `042-webhook-replay-protection.md` (replay dedupe), `058-cosign-deploy-time-enforcement.md` (code signing), `077-step-up-mfa.md`, `079-liveness-probe-restart-wedged-vm.md`.

## Tracking

The issue that drives this directory is
[#755 — Compliance attestations: SOC 2 Type 1 + ISO 27001 + GDPR DPA](https://github.com/poyrazK/faas/issues/755).
A full implementation plan lives at `$CLAUDE_JOB_DIR/tmp/755-plan/plan.md` and
as a comment on that issue.

## Conventions

- Every page that touches customer data must link back to one of the GDPR / DPA
  artifacts here or in `docs/DPA.md`.
- Every page that names a control (SOC 2 / ISO 27001) must cite the
  cross-reference it relies on (spec §, ADR, code path) so the auditor can
  re-derive the evidence from a fresh checkout.
- All sub-processor changes go through `subprocessor-check` (see PR-3 plan in
  the issue). The 30-day notice is enforced by the CI gate, not by humans.
