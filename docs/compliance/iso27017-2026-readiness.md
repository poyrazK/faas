# ISO/IEC 27017:2026 readiness

**Status: alignment draft.** This page is a Gregale-authored index of cloud-service
responsibilities, evidence, and open questions. ISO lists the second edition,
ISO/IEC 27017:2026, as published in July 2026 at the [official standard
page](https://www.iso.org/standard/27017).

This is not a clause-level crosswalk or a conformity assessment. No ISO clauses
or control text have been mapped or reproduced here. The work areas below are
Gregale's own operational groupings, reused from its [CSA CCM 4.1 readiness
index](csa-ccm-v4.1-readiness.md); they are not ISO headings. A control-level
review requires authorized access to the published edition and review of its
applicable use and reproduction terms.

## Current evidence and open work

| Gregale work area | Current evidence | State and next evidence needed |
|---|---|---|
| Governance and assurance | [SOC 2 mapping](soc2-control-mapping.md); [ISO 27001 SoA](iso27001-statement-of-applicability.md) | Both mappings are drafts. Independent review and attestation remain outstanding. |
| Identity and access | [Access review procedure](access-review.md), [inventory query](access-review.sql), and [sign-off template](access-review-record-template.md) | Complete and retain the first dated, signed quarterly review. |
| Platform infrastructure and isolation | `docs/faas_implementation_spec.md`; `docs/adr/052-control-plane-mtls-and-handler-peer-binding.md`; `deploy/ansible/` | The control plane remains single-node; cross-node high availability is future work. |
| Data protection and customer rights | `docs/DPA.md`; `docs/adr/021-account-export-and-staged-deletion.md`; `subprocessors.md` and `subprocessors.json` | DPA and subprocessor artifacts are drafts; document the responsibility split and review evidence. |
| Continuity and recovery | `docs/drills/2026-09-09-060104-restore-drill.md`; `docs/runbooks/PostgresBackup.md` | Define customer-facing RPO/RTO targets and retain recurring off-host restore evidence; the recorded basebackup RPO was 176 minutes. |
| Facilities and suppliers | [Supplier risk procedure](vendor-risk-management.md); [Hetzner workpaper](vendor-assessments/hetzner.md); [Neon pre-onboarding workpaper](vendor-assessments/neon.md) | Resolve the Hetzner / Neon database-scope mismatch, verify the live hosting and backup products, review current provider assurance, and record signed decisions. Physical controls remain inherited and unverified. |
| Development and supply chain | `docs/adr/038-build-attestation.md`; `docs/adr/058-cosign-deploy-time-enforcement.md`; `docs/adr/075-per-deploy-grype-scan.md` | Application SBOM generation remains best-effort; review GitHub evidence and record the supplier decision. |
| Monitoring and incident response | `docs/adr/035-auth-audit-events.md`; `docs/runbooks/incident-response.md`; `responsible-disclosure.md` | Record a recurring incident-response exercise and review the evidence-retention schedule. |
| Workforce and endpoints | `iso27001-statement-of-applicability.md`; `README.md` | Review operator endpoint evidence and the boundary with customer-managed endpoints; training remains planned. |
| Customer portability and shared responsibility | `docs/DPA.md`; `docs/event-driven.md`; `api/openapi.yaml`; `api/asyncapi.yaml` | Publish a consolidated shared-responsibility guide and complete an end-to-end portability assessment. |

## Review sequence

1. Confirm the service boundary and which Gregale, hosting-provider, and customer
   responsibilities are in scope.
2. Have a reviewer with authorized access assess the published edition and record
   permitted clause references without copying protected text.
3. Link each reviewed responsibility to current evidence, owner, evidence date,
   and a concrete gap or review cadence. Reuse the CSA CCM and SOC 2 evidence
   where it actually supports the assessed responsibility.
4. Update this draft only after the review; do not represent it as certification
   or independent assurance.
