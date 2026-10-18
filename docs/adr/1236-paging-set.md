# ADR-1236: Paging set — page only on symptoms that need a human now

- **Status:** accepted (owner decision, 2026-10-13: all four case-by-case rows as proposed)
- **Date:** 2026-10-13
- **Decision:** Keep 24 of the 96 page alerts, demote the other 72 to warn, and add three pages: two near-expiry certificate pages and compile errors on access-gating edge rules. The result is 27 page alerts, each with a promtool fixture that fires it.
- **Why:** One operator receives every page. At 96, most pages are causes, trends or backlogs that wait safely for business hours, so a real outage page arrives among routine ones and paging stops meaning "act now".

## Rule

A page needs a human within minutes, at any hour. It must meet one of:

1. **Customers are failing now** — availability burn, a serving daemon down or
   not ready, a node unable to serve.
2. **Capacity will run out within hours** — host memory or disk, tenant RAM,
   lv-fc, Postgres connections.
3. **Data is being lost now and will not be retried** — financial usage events,
   realtime callbacks.
4. **The paging path itself is broken** — Alertmanager delivery, Prometheus
   dropping alerts. The external heartbeat covers a dead Alertmanager.

Everything else is warn and is reviewed once a day.

## Keep at page (23)

| Alert | Why it pages |
|---|---|
| `FaasApiAvailabilityBurnRateHigh` | The SLO burn alert: customers are failing faster than the monthly budget allows. |
| `FaasDaemonDown` | A platform daemon is not answering. |
| `FaasDaemonNotReady` | A daemon is up but refusing work. |
| `FaasRestartLoop` | A daemon is crash-looping, which `FaasDaemonDown` can miss between restarts. |
| `FaasPostgresDown` | Every daemon depends on it. |
| `FaasPostgresProbeStale` | Postgres is running but hung. |
| `FaasPostgresConnectionsCritical` | Above 90% of `max_connections`; the 2026-09-12 outage mode (SQLSTATE 53300). |
| `FaasDBPoolStarved` | A daemon's callers are giving up waiting for connections. |
| `FaasHighResidentRamPct` | Invariant 2: wake admission is about to refuse tenants. |
| `FaasLvFcUsageHighPage` | Snapshot and drive storage is about to fill; wakes fail. |
| `FaasHostDiskSpaceCritical` | Under 5% free. |
| `FaasHostDiskFillingFast` | Predicted full within 4 hours. |
| `FaasHostMemoryCritical` | Under 5% available; the OOM killer is next. |
| `FaasVMMDGuestReceiverDown` | The node is not traffic-ready until the receiver recovers. |
| `FaasFleetSigningKIDDiverged` | Internal-service JWTs fail across nodes. |
| `FaasComputeNodeFlapping` | Each failover parks every instance on the node (hunt #9: ~22 per node). |
| `FaasAlertmanagerNotificationsFailed` | Pages are not being delivered. |
| `FaasPrometheusNotificationsDropped` | Alerts are dropped before Alertmanager. |
| `FaasConsumerUsageOutboxAppendFailed` | A financial usage event was not journaled; it is lost. |
| `FaasConsumerUsageOutboxNearCapacity` | The gateway leaves rotation at 90% of its outbox. |
| `FaasRealtimeCallbackOutboxFull` | Customer callbacks are rejected and live WebSockets closed. |
| `FaasRealtimeCallbackOutboxAdmissionFailed` | Same loss, caused by local storage. |
| `FaasSafeReleaseEmergencyRecoveryFailed` | The automatic canary recovery failed; customer traffic needs a human. |

## Owner's call (4), decided as proposed

| Alert | For page | For warn | Decision |
|---|---|---|---|
| `FaasApiAvailabilityLow` | Fires within 10 minutes of a sharp outage. | Duplicates the burn-rate page and is noisy at low traffic. | warn |
| `PgBackupStale` | Backups silently failing for days is the classic unrecoverable loss. | Nothing is lost in the next few hours; a daily review catches it. | warn; the warn route is reviewed daily |
| `FaasOutboundAdmissionUnavailable` | outboundd fails closed, so that integration is fully down for customers. | One integration, not the platform. | page |
| `FaasFleetSealDomainUnverified` | A node is serving without proved access to customer secrets. | It appears at node join, when an operator is present. | warn |

## Demote to warn (69)

### A Covered by a page that stays (6)

A kept page fires for the same incident, or this one precedes an automatic restart that a kept page catches if it fails.

`FaasDaemonLoopStalled`, `FaasHostExporterDown`, `FaasHostOOMKill`, `FaasSafeReleaseServingCanaryAtRisk`, `FaasScheddInstanceStatsPartialErrors`, `FaasStuckActivating`

### B Automation already acted (6)

The platform contained the problem itself (recycled, blocked, held, aborted); a human reviews it, but not at 3 a.m.

`FaasAccountAbuseHold`, `FaasBuilderSliceOOMKill`, `FaasImageAbuseBlocked`, `FaasInactiveAccountHasLiveInstance`, `FaasSafeReleaseEmergencyAbort`, `FaasTenantEgressAbuse`

### C Long lead time (11)

Days of warning before impact. A page at 14 days out is noise; the proposal adds a separate page close to expiry where that matters.

`FaasBridgeRollbackStuck`, `FaasComputeNodeTrustedKeyOverflow`, `FaasInternalMTLSExpiryMetricsStale`, `FaasInternalMTLSLeafExpiryCritical`, `FaasInternalMTLSRenewalFailed`, `FaasInternalMTLSRenewalNeverCompleted`, `FaasLogArchiveSpoolNearCap`, `FaasNewCve`, `FaasSnapshotFleetAvgHighPage`, `FaasTLSCertExpiryPage`, `FaasTLSCertExpiryPageByHost`

### D Durable backlog (27)

The work is persisted and retried; nothing is lost while it waits for business hours.

`BillingReconcileFailures`, `BillingReconciliationUnavailable`, `FaasAppDeletionDeadlineMissing`, `FaasAuditRetentionLoopStalled`, `FaasComputeHeartbeatRetentionStalled`, `FaasDebugRegressionStalled`, `FaasDomainDoctorStalled`, `FaasDomainVerificationStalled`, `FaasGithubRecoveryQueueStalled`, `FaasGithubWebhookDeliveryDead`, `FaasGithubdPathFilterDegraded`, `FaasJobImageMaterializationClaimFailed`, `FaasJobInstanceCleanupFailed`, `FaasManagedPostgresUsageCollectionStale`, `FaasManagedRealtimeDrainClaimFailed`, `FaasMeterdFleetEgressStreamsMissing`, `FaasOperatorActionTraceCompletenessFirstTickStalled`, `FaasOperatorActionTraceCompletenessLowPage`, `FaasPublicStatusEvaluationStalled`, `FaasRealtimeCallbackUnpersisted`, `FaasRequestTelemetryDefaultReceivingRows`, `FaasRequestTelemetryPartitionCoverageMissing`, `FaasRequestTelemetryPartitionReconcileStalled`, `FaasSnapshotDriftAuditStale`, `FaasSnapshotDriftAuditUnavailable`, `FaasStaleDeploymentBacklog`, `FaasUsageDailyRollupFailed`

### E Diagnostic, not a symptom (14)

A cause or trend signal. The symptom pages (availability burn, capacity, daemon health) catch the customer impact.

`BillingDrift`, `FaasColdBootRatioHigh`, `FaasCustomDomainTLSAskErrors`, `FaasDebugRegressionDetected`, `FaasErrorRateDrop`, `FaasErrorRateSpike`, `FaasPrometheusNotificationQueueBacklog`, `FaasPrometheusRuleEvaluationFailed`, `FaasTrafficAnomaly`, `FaasTrafficDrop`, `FaasTrafficSpike`, `FaasUpstreamClassifierFailure`, `FaasUpstreamProbeOutcomeDegraded`, `FaasUpstreamRttDegraded`

### F Not applicable on bare metal / dead (5)

Pages for components that are not deployed in production (Loki, GCP Ops Agent) or that cannot fire today.

`FaasBuildQueueDepthHigh`, `FaasGCPOpsAgentMissing`, `FaasLokiBackendDown`, `FaasLokiPromtailDown`, `FaasLokiShippingDropped`

## The five ADR-1235 alerts the spec marks page

| Alert | Proposal |
|---|---|
| `FaasEdgeRuleCompileError` | Page for `kind=~"jwt\|ip\|waf"`, warn for the rest. A dropped rule is skipped with only a log warning, so a broken authentication rule leaves its route unprotected. |
| `FaasCanaryStuckStep` | warn. Traffic holds at the current split; nothing breaks while it waits. |
| `FaasSafedeployAuditEmitFailing` | warn. Rollouts continue; only the audit trail is incomplete. |
| `FaasWriteRedirectLeaderUnreachable` / `LoopPrevented` / `MTLSFailure` | warn now. They need a standby control plane, which production does not run yet; revisit with multi-host control plane. |
| `FaasScheddLoopWorkPanicked` | warn. A handler bug; the durable table retries the work. |

## Add (2)

| Alert | Rule |
|---|---|
| `FaasTLSCertExpiryImminent` | Public edge certificate expires within 72 hours (`gateway_tls_cert_expiry_seconds < 3 * 86400`). The 14-day alerts become warn. |
| `FaasInternalMTLSLeafExpiryImminent` | An internal mTLS leaf expires within 72 hours. |

## Consequences

- 27 page alerts: the 23 kept above, `FaasOutboundAdmissionUnavailable`,
  `FaasTLSCertExpiryImminent`, `FaasInternalMTLSLeafExpiryImminent` and
  `FaasEdgeRuleAuthCompileError` (`kind=~"jwt|ip|waf"`).
  `FaasEdgeRuleCompileError` keeps the other kinds at warn.
- Every page alert has a promtool fixture that fires it.
  `pkg/promqlrules/testdata/page_alerts_without_fixture.txt` is empty, and
  `TestPageAlertsHaveFiringFixture` keeps it that way.
- Demotion was a severity label change; the fixtures that asserted
  `severity: page` for a demoted alert now assert warn, and the runbooks
  that called those alerts pages say warn and name the page that covers
  the incident, where one does.
- Warn alerts are only safe to demote to if someone reads them. This ADR
  assumes a daily review of the warn route; without one, `PgBackupStale` and
  the backlog alerts should go back to page. To support that review, the
  warn route groups by family and repeats a still-firing family once a day
  instead of every four hours, optional `am_*_warn_webhook_url_file`
  channels keep warns out of the page chat, and the fleet dashboard opens
  with a firing-pages count and a firing-warn table.
