# Vendor risk management

**Status: policy draft; supplier assessments remain open.** This Gregale-authored
procedure sets the minimum review for vendors that process customer data,
provide production infrastructure, or can affect the integrity or availability
of Gregale. It does not reproduce a third-party questionnaire or claim that a
vendor is certified or approved.

## Scope and initial tiers

Use [`subprocessors.json`](subprocessors.json) as the starting data-processing
inventory, then reconcile it against current production configuration,
contracts, source-control integrations, and operations. The current inventory
contains several services from the same supplier; assess risk by supplier and
service scope, keeping distinct data, regions, and contract terms visible.

| Initial tier | Gregale supplier / service scope | Why this tier applies | Review state |
|---|---|---|---|
| Critical | Hetzner: host infrastructure and possible off-host backup storage | Failure or compromise could affect production availability, integrity, or recovery. The actual products and storage endpoint remain to be verified. | Evidence and scope reconciliation required; no decision recorded. |
| Critical | GitHub: source repository, Checks API, installation-token exchange, and OAuth identity | Compromise could affect source integrity, software delivery, or account identity. | Evidence collection required; no decision recorded. |
| Critical (candidate) | Neon: backend named in the managed-PostgreSQL example | If enabled, the service would process customer database contents and availability-critical state. | Pre-onboarding only; example keeps provisioning disabled. Assess and complete the DPA notice process before customer provisioning. |
| Important | Stripe, Paddle, and Polar billing integrations | Process customer contact, subscription, and invoice or usage data; billing has a bounded provider-specific fallback path. | Collect evidence for each enabled provider and contract scope. |
| Important | Resend and Postmark transactional email | Process recipient addresses and authentication or service messages. | Collect evidence for each enabled transport. |
| Important | Google OAuth and GitHub OAuth | Process identity-provider claims used to authenticate account holders. | Collect evidence for each enabled login integration. |
| General | Vendors with no production access and no sensitive or personal data | Limited operational impact and data exposure. | None classified yet; add vendors only after inventory reconciliation. |

These are preliminary inherent-risk tiers based on Gregale's documented service
use. They are not a finding that any provider failed or passed an assessment.
Revisit the tier when data, access, regions, architecture, or business reliance
changes.

## Known inventory reconciliation

The repository's managed-PostgreSQL example selects Neon (`aws-eu-central-1`)
but sets `provisioning_enabled` to `false`. The supplier register still calls
the service “Hetzner managed single-tenant Postgres,” while ADR-056 says
PostgreSQL runs on Gregale's node and excludes Hetzner-managed PostgreSQL. The
Hetzner workpaper records the mismatch; the Neon workpaper tracks pre-onboarding
evidence and the enablement gate. Reconcile the actual deployed provider,
executed DPA, and public subprocessor notice before enabling customer
provisioning. The repository example alone does not establish which provider is
currently processing production data.

The backup runbook names a generic `offhostbox` endpoint. ADR-056 names Hetzner
Storage Box, but the checked-in runbook does not establish the live endpoint.
Verify its provider account, product, region, and contract before treating
Hetzner as the active backup processor.

## Assessment and review cadence

Complete an initial assessment before onboarding a new vendor or expanding its
data or production access. Existing critical suppliers must have an assessment
reviewed at least annually; important suppliers at least every two years;
general suppliers at least every three years. Reassess earlier after a material
incident, ownership change, new subprocessor, data-region change, material
service change, or assurance evidence that expires or narrows in scope.

At minimum, record and review:

- service scope, business owner, data categories, data subjects, regions, and
  access granted to the supplier;
- executed contract and DPA status, processing instructions, breach notice,
  deletion and return terms, subprocessor changes, and transfer safeguards;
- current independent assurance and its period, service boundary, exceptions,
  and auditor or certifier; a public certificate alone may not cover the
  service Gregale uses;
- identity and privileged access, encryption, vulnerability handling,
  incident response, continuity and recovery, and customer-data isolation;
- service dependencies, outage impact, export or recovery path, termination
  steps, and any compensating controls.

For gated provider reports, store the report in the restricted evidence store
and record its issuer, title, service scope, covered period, exceptions,
retrieval date, and evidence identifier. Do not publish confidential reports,
account screenshots, or customer-specific contract details in this repository.
The `dpa_signed` value in the public subprocessor register is an inventory
assertion, not the executed agreement itself; verify it against a restricted
contract record and the actual product, data, and region scope before relying on
it in an assessment.

## Decision and remediation

The service owner prepares the assessment. Security reviews critical suppliers;
Legal or the privacy owner reviews DPA terms, data transfers, and subprocessors.
The accountable operator records one decision: **accepted**, **accepted with
conditions**, **rejected**, or **pending**. A pending worksheet is not an
approval.

Each finding needs an owner, due date, evidence reference, and action. Confirmed
critical exposure is contained promptly. Other material findings need a dated
remediation or a written exception naming the approver, compensating controls,
residual risk, and expiry (no more than 12 months). Re-review expired exceptions
before continued reliance.

Keep only an assessment index and non-sensitive decision summary in Git. Store
reports and signed contracts in the restricted evidence store. Update the
public subprocessor register through its established notice and
`subprocessor-check` workflow when processing scope changes.

For managed PostgreSQL, record the accepted supplier decision in the restricted
[`supplier approval template`](vendor-assessments/managed-postgres-supplier-approval.example.json).
The runtime requires that decision to match the configured backend and the
active public register entry; provider qualification does not replace supplier
or contract review.

## Current evidence workpapers

- [`vendor-assessments/README.md`](vendor-assessments/README.md) indexes the
  first critical-supplier workpapers.
- [`vendor-assessments/hetzner.md`](vendor-assessments/hetzner.md) identifies
  the account-gated evidence needed for Gregale's hosting and storage scope.
- [`vendor-assessments/github.md`](vendor-assessments/github.md) identifies
  organization-level reports and settings evidence needed for source control,
  checks, and OAuth.
- [`vendor-assessments/neon.md`](vendor-assessments/neon.md) is a pre-onboarding
  assessment for the configured managed-PostgreSQL backend; customer
  provisioning remains disabled pending evidence and notice.
- [`vendor-assessments/managed-postgres-supplier-approval.example.json`](vendor-assessments/managed-postgres-supplier-approval.example.json)
  shows the operator decision shape; completed copies and evidence references
  stay in restricted storage.

The public [Hetzner data-protection guidance](https://docs.hetzner.com/general/company-and-policy/data-protection-at-hetzner/)
describes its DPA, subcontractor list, and access to customer-specific audit
materials. The current [technical and organizational measures](https://docs.hetzner.com/general/security-and-identify/technical-and-organizational-measures/)
must be checked against the actual products Gregale uses; Hetzner distinguishes
dedicated servers from managed products. GitHub says organization owners can
access compliance reports, including SOC reports, CAIQ, and ISO certification,
through organization settings; retrieve the current scoped report from the
[GitHub compliance reports page](https://docs.github.com/en/enterprise-cloud@latest/organizations/keeping-your-organization-secure/managing-security-settings-for-your-organization/accessing-compliance-reports-for-your-organization).
These public entry points do not replace review of current, account-specific
evidence.

Neon lists its SOC 2 Type II, ISO/IEC 27001:2022, ISO/IEC 27701:2019, and DPA
materials on its [security page](https://neon.com/security) and [Trust Center](https://trust.neon.com/).
The Trust Center states that SOC 2 reports are limited to paid customers;
verify Gregale's report access and inspect the actual documents before making
an assessment decision.
