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

## Follow-up after PR #4129

Audited `c113d1c51` after the first hardening patch merged. New regressions
reproduced late corrections missed beyond the latest hour, accepted windows
that cannot partition UTC billing months, and configuration seconds overflowing
into valid-looking collection or staleness durations. The follow-up replays the
last three completed windows after forward recovery, sharing the existing
24-window per-database budget. It rejects unsupported window sizes and duration
overflow. The PostgreSQL-backed replay test also reproduced a checkpoint
timezone bug: struct comparison rejected the same instant represented with
`time.Local`, stopping collection after its first sweep. Window identity now
compares instants. PostgreSQL-backed tests verify restart-safe replacement,
downward and zero revisions, month totals, and rejection without committing
coverage; fixed-offset tests cover timezone-independent resumption.
See [ADR-493](../adr/493-managed-postgres-usage-correction-replay.md).

## Follow-up: provider throttling and cancellation

A TLS HTTP regression reproduced 25 consumption requests after the first 429:
the adapter discarded `Retry-After` and continued the sweep. Rate-limit
responses now establish a concurrency-safe cooldown shared by that provider
instance. Consumption exhaustion does not block lifecycle or credential
operations; general API exhaustion also defers consumption. Missing or invalid
guidance uses a one-minute fallback. Requests resume at the deadline, and
out-of-order responses cannot shorten it. There is no internal mutation retry.

Separate regressions reproduced response-body cancellation being returned as
provider unavailability and an already canceled request reaching transport.
The adapter preserves cancellation and checks it before applying a cooldown.
Tests cover expiry, both retry header formats, overflow, concurrent callers,
resource-lock isolation, and rate-limited provisioning recovery.
See [ADR-500](../adr/500-managed-postgres-provider-rate-limit-cooldowns.md).

## Remaining work, in priority order

| Priority | Gap and evidence | Required next work |
| --- | --- | --- |
| P1 | Live credential/provider qualification remains pending. `sqlCredentialRoles.Ensure` creates SQL passwords; `credentialMaterial` recovers them through the Neon API. Local role tests install a fixture password, so they do not establish this provider contract. | Run version 3 qualification on disposable resources, including stable password recovery on retry, real restricted runtime/migration login, rotation, inherited-login isolation, and revocation. This is an unverified contract, not a reproduced password bug. |
| P1 | Usage recovery is hourly and bounded by provider retention. `usageGranularity` selects granularity from window size; an hourly backlog older than Neon's 168-hour history cannot be replayed by `UsageCollector`. | Add an explicit operator reconciliation workflow for retained exports/invoices and gap diagnosis. Preserve nonoverlapping ledger windows and fail-closed admission. Daily totals cannot simply replace already-recorded hourly windows. |
| P1 | Automatic correction replay now covers the last three completed policy windows. Revisions outside that bounded horizon and final provider settlement remain unreconciled. | Add explicit export/invoice reconciliation and a provider-lag policy before treating fresh coverage as final spend. |
| P1 | Reactive provider-instance cooldowns now suppress requests after a 429. Polling still has no account-wide request budget or fair backfill scheduler; fixed database order and up to 24 requests per database can favor earlier databases when the quota repeatedly exhausts. | Add shared provider-account pacing and fair recovery scheduling across backends/processes, including fleet-wide priority for missing windows over correction replay. Preserve existing coverage when requests are deferred. |
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
