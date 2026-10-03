# GitHub supplier assessment

**Status: pending evidence; no risk decision recorded.** This workpaper does
not claim that the supplier passed or failed review.

| Field | Current scope |
|---|---|
| Supplier | GitHub |
| Tier | Critical (preliminary) |
| Gregale services | Source repository, Checks API and installation-token exchange, and optional GitHub OAuth, as listed in `subprocessors.json` |
| Data and access | Source code and change history, SHA-only commit metadata for checks, installation-token exchange payloads, and OAuth identity claims |
| Business owner | Security / Platform — assign a named reviewer in the restricted record |
| Review date | Not reviewed |
| Decision | Pending |

## Evidence to retrieve and review

| Evidence | Source | Status / reviewer note |
|---|---|---|
| Current SOC 2 report and any relevant independent assessment | GitHub organization settings → Security → Compliance; see the [official access instructions](https://docs.github.com/en/enterprise-cloud@latest/organizations/keeping-your-organization-secure/managing-security-settings-for-your-organization/accessing-compliance-reports-for-your-organization) | Confirm our organization plan provides access; record report period, product scope, exceptions, and evidence ID. |
| Current CAIQ / STAR / ISO evidence relevant to the services used | GitHub organization settings → Security → Compliance | Review scope and validity; do not infer service coverage from a report title or public marketing page. |
| Repository and organization administrators, team membership, and branch/ruleset protections | GitHub organization and repository settings | Export a dated, least-privilege inventory; compare with approved operator access and review records. |
| GitHub App permissions and installation scope for the Checks API integration | Gregale GitHub App and organization installation settings | Confirm only required repositories and permissions are granted; review token rotation and revocation paths. |
| OAuth application configuration, requested scopes, and account-removal behavior | GitHub OAuth application settings and Gregale authentication flow | Confirm scopes, callback URLs, account data collected, and how access is removed. |
| Incident notification, data retention/deletion, and subcontractor terms | Executed agreement and current GitHub trust/compliance materials | Record applicable commitments and gaps in restricted evidence. |

## Decision record

No acceptance decision is recorded. Complete the internal assessment, list
residual risks and compensating controls, and add the reviewer, decision date,
and opaque evidence references here after the restricted evidence has been
reviewed.
