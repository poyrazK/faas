# Neon supplier assessment

**Status: pre-onboarding; evidence and risk decision pending.** The checked-in
managed-PostgreSQL example names Neon as its backend, but leaves
`provisioning_enabled` false. This workpaper does not assert that Neon is
currently processing Gregale customer data or that the supplier is approved.
The runtime gate also requires a restricted supplier decision and an installed
subprocessor-register snapshot. Neon has no active entry in the current
register, so this backend remains blocked even if its technical qualification
succeeds.

| Field | Current scope |
|---|---|
| Supplier | Neon (confirm contracting entity in the account and DPA) |
| Tier | Critical (preliminary) |
| Gregale service | Candidate backend for Gregale's gated managed-PostgreSQL preview |
| Configured example region | `eu-central-1`, provider region ID `aws-eu-central-1`; verify the actual account's project region and related processing locations |
| Data and access | If enabled, managed database contents and service metadata for databases provisioned by Gregale; exact customer data categories and access path remain to be documented |
| Business owner | Security / Platform — assign a named reviewer in the restricted record |
| Evidence checked | Public sources checked 2026-10-01; no Neon account evidence reviewed |
| Review date | Not reviewed |
| Decision | Pending; do not enable customer provisioning |

## Public evidence reviewed

| Evidence | Public fact observed | Assessment limit |
|---|---|---|
| [Neon Security & Compliance](https://neon.com/security) | Neon says it undergoes annual SOC 2 Type II and ISO/IEC 27001:2022 and ISO/IEC 27701:2019 audits, offers a DPA, hosts on AWS and Azure, and uses TLS 1.2+ and AES-256 for transport and stored data. It identifies the SOC 3 summary as public. | These are provider statements. Obtain current reports/certificates and verify product, legal entity, region, exceptions, and covered period. |
| [Neon Trust Center](https://trust.neon.com/) | The center lists SOC 2, SOC 3, ISO 27001, ISO 27701, DPA, subprocessor, and other materials. It says the SOC 2 report is available to paid customers only. | No report, certificate, DPA, or subprocessor file was retrieved or assessed for Gregale. Confirm account eligibility and request the controlled documents. |

## Evidence to retrieve and review

| Evidence | Source | Status / reviewer note |
|---|---|---|
| Executed DPA and transfer terms for the actual legal entity | Neon account / Trust Center | Pending. Confirm data categories, data subjects, breach notice, deletion/return, subprocessors, and transfer mechanism. |
| Current SOC 2 Type II report and bridge letter | Neon Trust Center | Pending. Confirm access entitlement, covered period, service boundary, subservice organizations, exceptions, and complementary user-entity controls. |
| Current ISO/IEC 27001:2022 and 27701:2019 certificates and scopes | Neon Trust Center | Pending. Record certificate identifiers, covered entity/services, sites, expiry, and exceptions. |
| Product-region and processing-location evidence | Neon organization/project settings, DPA, and subprocessor list | Pending. Confirm what `aws-eu-central-1` means for the actual account, and identify support, telemetry, backups, and subprocessors that may process data elsewhere. |
| Security and operations evidence | SOC 2 report, DPA, service terms, and account settings | Pending. Review privileged support access, logging, incident handling, vulnerability management, encryption/key responsibilities, continuity, recovery, retention, export, and deletion. |
| Tenant and integration controls | Gregale configuration and Neon account | Pending. Confirm project isolation, API-key privileges/rotation, connection TLS, IP restrictions where available, database deletion, and tested restore/exit procedures. |

## Enablement gate

Before customer provisioning is enabled:

1. Complete this assessment and record a named reviewer, decision, residual
   risks, and restricted evidence IDs.
2. Reconcile the inaccurate Hetzner managed-PostgreSQL entry with the actual
   provider plan. Update the executed DPA and public subprocessor notice through
   the documented change process; record the date the notice is actually
   published. Do not invent a publication date in the repository.
3. Satisfy the DPA's 30-day notice period before Neon begins processing customer
   data. Keep the existing provisioning and qualification gates closed until
   the notice period and provider qualification are complete. Record the
   verified publication and effective dates in the active register entry, then
   create the restricted supplier approval bound to the configured backend
   fingerprint.
4. Validate the actual configured region, retention, backups, restore path, and
   customer-data deletion against the reviewed contract and reports.

## Decision record

No acceptance decision is recorded. Keep customer provisioning disabled until
the evidence review, contract and notice work, and provider qualification are
complete. Add the reviewer, decision date, conditions, and opaque evidence
references here after review.
