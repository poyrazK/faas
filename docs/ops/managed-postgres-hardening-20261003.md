# Managed PostgreSQL hardening audit — 2026-10-03

Audited the merged implementation at `9d4324cfc4148ec2a402ef23e7295c0aa7cebee2`
(PR #4127), concentrating on provider contracts, credential lifecycle, restore
retries, usage recovery, and cutover staging. This is a focused audit, not a
claim that all provider or VM failure modes are qualified.

## Reproduced and fixed

| Bug | Effect | Regression |
| --- | --- | --- |
| Neon v2 storage/history byte-months interpreted as byte-hours | Quantities and their cost/cap inputs were 744 times too small | `TestUsageNormalizesByteMonthStorage` covers all four storage/history metrics and overflow |
| Normal project pagination cursor rejected | A valid single-project consumption response failed collection | `TestUsageRequiresCompleteConsumptionCoverage/normal_project_cursor` |
| Response time boundaries ignored; duplicate or malformed quantities accepted | Partial/empty data advanced coverage; repeated data inflated consumption | `TestUsageRequiresCompleteConsumptionCoverage` rejects missing windows, duplicate periods/timeframes/meters, absent/null values, negatives, unknown meters, and overflow |
| Retention checked before finding an existing restore | Retrying a completed restore failed after its timestamp aged out | `TestRestoreCompletedRetrySurvivesRetentionExpiry` also checks conflicting intents and new expired requests |

The HTTP tests failed against the merged adapter before their fixes. Existing
fixtures modeled missing time metadata and an empty cursor, concealing these
contract mismatches. The storage fixture also asserted the incorrect unit.
See [ADR-492](../adr/492-managed-postgres-consumption-contract.md) for provider
sources and the existing-ledger reconciliation requirement.

## Implemented capability boundary

The gated Neon adapter supports PostgreSQL 14–18, development/burstable/production
compute classes, provider suspension, single-zone placement, root-project
creation/deletion, PITR to a new branch, metadata health, pooled runtime
credentials, direct migration credentials, durable binding/rotation/revocation,
and normalized compute/storage/history/network accounting. Restore descendants
share the source project's aggregate usage; recording it once prevents double
counting. The CLI/API expose database and binding operations, usage/health reads,
and cutover prepare/status/verify/cancel. The dashboard exposes reads.

## Remaining work, in priority order

| Priority | Gap and evidence | Required next work |
| --- | --- | --- |
| P1 | Live credential/provider qualification remains pending. `sqlCredentialRoles.Ensure` creates SQL passwords; `credentialMaterial` recovers them through the Neon API. Local role tests install a fixture password, so they do not establish this provider contract. | Run version 3 qualification on disposable resources, including stable password recovery on retry, real restricted runtime/migration login, rotation, inherited-login isolation, and revocation. This is an unverified contract, not a reproduced password bug. |
| P1 | Usage recovery is hourly and bounded by provider retention. `usageGranularity` selects granularity from window size; an hourly backlog older than Neon's 168-hour history cannot be replayed by `UsageCollector`. | Add an explicit operator reconciliation workflow for retained exports/invoices and gap diagnosis. Preserve nonoverlapping ledger windows and fail-closed admission. Daily totals cannot simply replace already-recorded hourly windows. |
| P1 | Corrections only automatically refresh the latest completed policy window (`collectDatabase`); older provider revisions require manual reconciliation. | Add a bounded rolling correction horizon and provider-lag policy, with tests proving stable monthly totals and coverage under revisions. |
| P1 | Provider polling has no organization-wide request budget or fair backfill scheduler. One sweep can request up to 24 windows per database across every page of ready databases. | Add provider-scoped pacing and fair recovery scheduling, accounting for Neon's consumption rate limit and shared credentials. Preserve existing coverage when requests are deferred. |
| P1 | Customer restore cutover activation is absent. Staging, SQL verification, and admission fences exist, but SQL-session drain evidence, atomic binding publication, snapshot invalidation, workload verification, and rollback activation are unfinished. | Complete the activation state machine only after durable writer-drain proof and supported native x86_64 KVM restart/power-loss acceptance. Keep activation disabled meanwhile. |
| P2 | Provider usage is a COGS guardrail, not a complete invoice reconciliation model. `consumptionMetrics` excludes `extra_branches_month`; branch-count charges and provider plan allowances are not represented in the canonical ledger. | Add a separate provider reconciliation model before describing account cost ceilings as complete provider spend or enabling commercial customer metering. |
| P2 | `Provider.Update` returns unsupported; there is no durable resize/version/retention-change workflow. Neon capabilities omit read-only credentials and portable HA. | Add capability-backed state machines and qualification before exposing these operations. Read-only roles are a useful next credential feature after existing roles pass live qualification. |
| P2 | PITR validation uses the configured retention window, not the provider's earliest recoverable timestamp or branch lineage. | Expose provider recoverability metadata and reject impossible new restore intents before reservation. Existing durable retries must stay idempotent. |

Neon's [consumption reference](https://api-docs.neon.tech/reference/getconsumptionhistoryperprojectv2)
documents history availability and granularity limits. Enabling collection after
the provider account's eligible-plan upgrade can also leave earlier creation
windows unavailable. Neither missing history nor missing timeframes establishes
zero consumption.

Always-on compute is excluded by current plan entitlements. That is a product
boundary requiring pricing and entitlement work, not a provider-adapter bug.
The native VM acceptance and cutover gaps are detailed in the existing
[integration evidence](evidence/20261003-managed-postgres-main-integration/README.md).

## Validation

Go 1.25.13: the full managed PostgreSQL suite passed under `-race` with 249
passing cases against PostgreSQL 16.15 using TLS and SCRAM. The two opt-in tests
for live Neon and installed JavaScript starter copies were skipped. Native SQL
credential verification and PostgreSQL-backed catalog, lease, cutover,
revocation, and usage tests ran. The API, billing, and dashboard packages also
passed under `-race`; managed PostgreSQL `go vet` and golangci-lint passed.
The before/after regressions were rerun using a Go overlay of the merged source,
without changing the working implementation.

No VM lifecycle code changed in this hardening patch. Existing native KVM
acceptance requirements for cutover activation remain pending.
