# Hetzner supplier assessment

**Status: pending account evidence; no risk decision recorded.** The public
material below is a research lead, not a completed supplier assessment.

| Field | Current scope |
|---|---|
| Supplier | Hetzner Online GmbH (confirm contracting entity in the account) |
| Tier | Critical (preliminary) |
| Gregale services | Host infrastructure for the documented `fsn-1` control-plane and `fsn-2` compute-node inventory; off-host backup is configured through the `offhostbox` alias, with Hetzner Storage Box named in ADR-056. Confirm the actual rented products and backup endpoint in the provider account. |
| Data and access | Control-plane databases and service state on the host, application workloads, operational metadata, and encrypted backup objects if the configured endpoint is Hetzner. Provider access could affect production availability and integrity. |
| Business owner | Security / Platform — assign a named reviewer in the restricted record |
| Evidence checked | Public sources checked 2026-10-01; no customer-account evidence reviewed |
| Review date | Not reviewed |
| Decision | Pending |

## Scope discrepancy to resolve

The prior workpaper described Gregale's managed PostgreSQL as a Hetzner-managed
service. Repository evidence does not support that description:
[`deploy/managed-postgres.example.json`](../../../deploy/managed-postgres.example.json)
selects the Neon backend and leaves `provisioning_enabled` false;
[`docs/adr/056-off-host-pg-backup.md`](../../adr/056-off-host-pg-backup.md)
explicitly says PostgreSQL runs on the node and excludes Hetzner-managed
PostgreSQL. The current subprocessor register still names “Hetzner managed
single-tenant Postgres.” Reconcile the executed DPA and public register against
the live provider account before approving this assessment or enabling the
managed-PostgreSQL preview. Do not use this workpaper as evidence that Hetzner
operates Gregale's managed-PostgreSQL service.

The checked-in backup runbook uses the generic `offhostbox` alias and does not
prove which provider is configured on a live host. Confirm the account, product,
region, retention policy, and DPA scope before treating Hetzner Storage Box as
the active backup processor.

## Public evidence reviewed

| Evidence | Public fact observed | Assessment limit |
|---|---|---|
| [ISO/IEC 27001:2022 certificate](https://www.hetzner.com/assets/downloads/ISO-Certificate.pdf) | Certificate ZN-2025-35 names Hetzner Online GmbH, covers “all hosting services and the data centers,” lists Nuremberg, Falkenstein, and Tuusula, and is valid 2025-09-27 through 2028-09-26. | Public certificate is a lead. Confirm the rented product, contracting entity, scope, and current validity in the assessment record. |
| [Data-protection guidance](https://docs.hetzner.com/general/company-and-policy/data-protection-at-hetzner/) | A DPA is concluded in the customer account; the customer specifies personal-data types and data-subject groups. Hetzner says customers with a DPA receive its annual TOMs audit report through the portal. | Retrieve Gregale's executed DPA, its completed appendix, and the latest report. None has been reviewed here. |
| [Technical and organizational measures](https://docs.hetzner.com/general/security-and-identify/technical-and-organizational-measures/) | Hetzner distinguishes client-managed dedicated/cloud servers from managed products; Storage Boxes are separately listed among unmanaged products. | Match the correct product-specific table and customer responsibilities to the actual service. |
| [Subcontractor list](https://www.hetzner.com/AV/subunternehmer.pdf) | Hetzner publishes a subcontractor list. | Retrieve the current list and compare it with the DPA, chosen locations, and service account. |

## Evidence to retrieve and review

| Evidence | Source | Status / reviewer note |
|---|---|---|
| Executed DPA covering the exact products, data categories, and regions used | Hetzner customer account | Pending. Verify the account's current agreement and completed processing appendix; the repository register is not proof of execution. |
| Latest TOMs report and product-specific scope | Hetzner customer account; public [TOMs page](https://docs.hetzner.com/general/security-and-identify/technical-and-organizational-measures/) | Pending. Record issuer, period, products, exceptions, retrieval date, reviewer, and restricted evidence ID. |
| Current certificate and covered entity, sites, and scope | Public [certificate](https://www.hetzner.com/assets/downloads/ISO-Certificate.pdf) and account evidence | Public certificate facts recorded above; account/service mapping remains pending. |
| Current subcontractors and data locations | Hetzner account and [subcontractor list](https://www.hetzner.com/AV/subunternehmer.pdf) | Pending. Verify host locations and backup endpoint separately. |
| Incident notice, support access, privileged access, and vulnerability handling | Executed terms, TOMs report, and provider account documentation | Pending. Record contractual commitments and gaps. |
| Backup durability, continuity, deletion, export, and termination path | Actual storage product terms and Gregale restore/exit evidence | Pending. Confirm which `offhostbox` provider and configuration are deployed. |

## Decision record

No acceptance decision is recorded. Resolve the scope discrepancies, complete
the internal assessment, list residual risks and compensating controls, and add
the reviewer, decision date, and opaque evidence references here after the
restricted evidence has been reviewed.
