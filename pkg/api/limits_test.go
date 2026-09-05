package api

import (
	"testing"
	"time"
)

// TestPlanLimitsMatchSpec pins every value in the table to the financial-model /
// spec §1 numbers. If the spreadsheet moves, this test must be updated in the
// same PR — that is the point.
func TestPlanLimitsMatchSpec(t *testing.T) {
	want := map[Plan]Limits{
		// Move 1: Free gates async_invoke and queues (spec §4.4 paid-only).
		// EgressAllowlistAllowed/MaxSize default to false/0 (Go zero), so
		// Free/Hobby rows below omit them intentionally — mirrors the
		// MinInstancesAllowed row shape.
		PlanFree: {Plan: PlanFree, DeployedApps: 1, MaxConcurrency: 1, RAMMB: 128, AppLayerMaxMB: 256, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 30, CertExpiryWarningDays: 30, IncludedGBHours: 5, PriceMillicents: 0, RateLimitRPS: 5, RateLimitBurst: 20, EgressMbit: 10, SecretCountMax: 3, SecretValueMaxBytes: 4096, MaxMinInstances: 0,
			// Issue #559: Free = 1 (single-concurrency plan — one VM
			// serves one request at a time; mirrors MaxConcurrency).
			ConcurrencyPerVMBound: 1,
			// Issue #395 / ADR-045: Free gets 8 keys / 4 KB per value.
			EnvVarsMax: 8, EnvValueMaxBytes: 4096,
			// ADR-044: per-plan CPUWeight/CPUQuotaUS/CPUPeriodUS — issue
			// #301 acceptance #1+#2. The 2/4/8/16 ratio is the literal
			// value from the issue; the quota is the spec's literal
			// "100ms/100ms, 200ms/100ms, 500ms/100ms, 1000ms/100ms".
			CPUWeight: 2, CPUQuotaUS: 100_000, CPUPeriodUS: 100_000,
			MaxQueueDepth: 0, MaxDelayedTasksPerApp: 0, MaxSourceBytesPerInvocation: 0, AsyncInvokeAllowed: false,
			// Issue #394: Free is gated out of queues entirely (spec §4.4
			// paid-only), so MaxQueueAttempts is moot — 0 matches the
			// "feature not offered" contract.
			MaxQueueAttempts: 0,
			// ADR-134 PR-B: Free's per-account cap ladder.
			MaxAsyncInvocationsPerAccount:     100,
			MaxAsyncInvocationDeadlineSeconds: 300,
			MaxAsyncResultRetentionSeconds:    86400,
			// Cron (spec §4.4 paid-only): Free has no crons at all. Handler
			// returns 402 ErrPlanCronsNotAllowed before the store is touched.
			CronLimitPerApp: 0, CronLimitPerAccount: 0,
			// Issue #475: Free is gated off the reserved eviction tier.
			// Fail-closed at 0/0 mirrors the cron 0/0 posture above.
			EvictionPriorityReservedAllowed: false, ReservedConcurrencyPerAccount: 0,
			// M-2 / ADR-137+138: Free stays request-only. Per-mode
			// replica caps are zero so ValidatePlan rejects any
			// worker / service / job ExecutionMode. StopGracePeriod
			// caps tighten to 15 s (ADR-138 §Decision 4).
			DefaultStopGracePeriodS: 15, DefaultStartupDeadlineS: 15, DefaultMaxRetries: 3,
			WorkerReplicasMax: 0, ServiceReplicasMax: 0, JobMaxRuntimeS: 0,
			// Issue #477 / ADR-079: Free stays on the no-signup-friction
			// path — public-by-default. Bearer + basic both gated off.
			// The 'open' mode is always available regardless of plan.
			PublicAuthBearerAllowed: false, PublicAuthBasicAllowed: false,
			// IAM-6 / ADR-061 PR-2 (issue #190): Free stays 0/0 by
			// plan policy — the abuse-floor tier cannot host shared
			// orgs. Mirrors CronLimitPerApp posture. Financial model
			// is authoritative; reconciliation follow-up.
			OrgMembersMax: 0, OrgPendingInvitationsMax: 0,
			// ADR-045 (#396): alert rules — Free gated to 402, so the limits
			// surface is 0/0 to fail-closed by default.
			AlertRuleLimitPerApp: 0, AlertRuleLimitPerAccount: 0, AlertPresetCatalogLimitPerAccount: 8,
			// ADR-089 (planned): edge rules — Free gets 5 rules
			// (route|rewrite|redirect|headers|cors) but jwt/ip stay
			// plan-gated to Hobby+. The limits surface reflects only
			// what the create handler will accept (5 rules total).
			EdgeRulesPerApp: 5, EdgeRulesJWTAllowed: false, EdgeRulesIPAllowed: false, EdgeRulesGeoPerApp: 1, EdgeRulesThrottlePerApp: 1, EdgeRulesCachePerApp: 0,
			// issue #975 #4 / Mega-Foundation #979-b — Free is the abuse-floor tier;
			// the abstraction is the upsell. PR-B (#979-c) wires the writer.
			CorsPresetsPerAccount: 0, CorsPresetsPerApp: 0, CorsPresetMaxOrigins: 0, CorsPresetMaxAllowMethods: 0, CorsPresetMaxNameLength: 64,
			// ADR-099 (#879): tenant surfaces — Free is the abuse-floor
			// tier. The `tenant_surfaces` feature is the upsell; Free
			// customers carry the single-tenant case via the legacy
			// `custom_domains` path. Allowed=false means the create
			// handler returns 402 CodeTenantSurfaceQuotaReached before
			// the store is touched.
			TenantSurfacesPerAccount: 0, TenantHostnamesPerSurface: 0, TenantSurfacesAllowed: false,
			DataPlacementHintsPerApp: 0,
			// ADR-076 (#476): outbound webhooks — Free gated to 402
			// (CodePlanWebhooksNotAllowed), same fail-closed shape.
			WebhookPerApp: 0, WebhookPerAccount: 0,
			// ADR-0NN (#757): Free is gated off the Trigger primitive
			// entirely. Handler returns 402 CodePlanTriggersNotAllowed
			// before the store is touched; the 0/0/0/0/0/0/0 tuple
			// here is the defence-in-depth value the store still reads.
			TriggersAllowed: false, TriggerLimitPerApp: 0, TriggerLimitPerAccount: 0, TriggerBatchSizeMax: 0, TriggerBatchWindowMaxSec: 0, TriggerMaxAttemptsMax: 0, TriggerRecordsPerSecondPerApp: 0, TriggerPayloadMaxBytes: 0, MaxESMSourcesPerApp: 0, MaxESMRecordsPerSecond: 0, BrokerEgressMbit: 0, TLSSkipVerifyAllowed: false,
			// ADR-040: Free gets 50/min — covers the 1-concurrency plan's
			// traffic envelope with a 50× burst ceiling.
			RateLimitPerAccountRPM: 50,
			// ADR-104: Free gets 100 — small slice of per-key
			// cardinality, enough to size 1-2 per-key limits.
			ThrottleMaxKeysPerRule: 100,
			// ADR-099 PR-0: Free wake-admission throttle (1/1).
			WakeBurstPerApp:     1,
			WakeBurstPerAccount: 1,
			// Issue #471 / ADR-047 (PR-A): Free is gated out of streaming
			// entirely. The 25 MiB / 300 s caps are the legacy pre-#471
			// defaults — kept here so a Free customer that PATCHes
			// streaming_enabled=false sees the same envelope they'd have
			// seen before the streaming patch landed. MaxResponseBodyBytes
			// (25 MiB) and ResponseWriteTimeoutSeconds (300 s) are the
			// pre-#471 spec §4.1 caps PR-A inherits.
			StreamingEnabled: false, MaxResponseBodyBytes: 26_214_400, ResponseWriteTimeoutSeconds: 300,
			// Issue #676 / ADR-080: Free is the abuse-floor tier — a
			// long-lived WS would pin a wake past the 30 s Free idle
			// timeout. Default off; apid PATCH rejects with 403
			// plan_websocket_not_allowed (mirrors Free's
			// StreamingEnabled=false envelope above).
			WebSocketEnabled: false,
			// ADR-093: per-route metrics surface is a paid-tier
			// feature — Free gated off (the abuse-floor tier's
			// blast radius is small enough that route-level
			// breakdown doesn't justify the per-app cap plumbing).
			RouteMetricsEnabled: false,
			// Issue #461 / ADR-062: Free has no private-registry
			// credential surface (handler returns 403
			// plan_registry_credentials_not_allowed).
			RegistryCredentialMax: 0,
			// Issue #470 / ADR-055: Free is gated off for warm-tier
			// snapshots — doubling the per-app parked footprint
			// doesn't fit the Free pricing tier. The 0/0 defaults
			// are defence-in-depth; the WarmSnapshotAllowed() gate
			// surfaces the 403 to a Free customer PATCHing true.
			WarmSnapshotEnabled: false, WarmSnapshotMinRequestsDefault: 0, WarmSnapshotMinMsDefault: 0,
			// Issue #560: Free is gated off for require_authn
			// — opt-in is a paid-tier feature (Cloud Run's
			// `--no-allow-unauthenticated` shape).
			RequireAuthn: false,
			// ADR-124: Free stays on http1/http2 (universal) but is
			// gated off gRPC entirely — the abuse-floor tier doesn't
			// host the gRPC service-migration use case that prompted
			// ADR-124 (issue #67). apid PATCH rejects with 403
			// plan_app_protocol_grpc_not_allowed on Free.
			AppProtocolGrpcAllowed: false,
			// Issue #695 / ADR-080: Free stays public-by-default.
			// 'open' is the canonical mode for non-token-gated apps;
			// require_authn=true would be meaningless on Free because
			// bearer/basic are both gated off at this tier.
			RequireAuthnDefault: false, PublicAuthModeDefault: "open",
			// Issue #189 / IAM-5: Free = 3 keys (primary deploy + staging + break-glass).
			KeysMax: 3,
			// Issue #667 / ADR-078: tail primitive on with floor timeout.
			TailEnabled: true, TailTimeoutS: 5, TailCapMax: 16, ConcurrentTailsPerInstance: 4,
			// Issue #562: Free has no archive surface.
			LogArchiveEnabled: false, LogArchiveRetentionDaysMax: 0,
			// ADR-096: Free = 1 day retention, 50 fingerprints, 25 request rows.
			AppErrorsRetentionDays: 1, AppErrorsMaxFingerprintsPerApp: 50, AppErrorsMaxRequestRowsPerFingerprint: 25,
			// ADR-120 / issue #975 item #5: consumer keys — Free gated
			// to 0 (the abuse-floor tier cannot host multi-tenant
			// consumer surfaces — mirrors CronLimitPerApp/OrgMembersMax
			// 0/0 posture). Handler returns 402 CodeConsumerKeysNotAllowed.
			ConsumerKeysPerApp: 0, ConsumerKeysPerAccount: 0,
			// ADR-122 / issue #975 item #1: endpoint discovery — Free
			// is gated to 0/0/0 (same fail-closed posture as consumer keys).
			// apid GET / PATCH return 402 CodePlanOpenAPIDocsNotAllowed.
			OpenAPIDocsPerDeployment: 0, OpenAPIDocMaxBytes: 0, OpenAPIDocsPerAccount: 0, OpenAPIImportsPerAccount: 100,
			// ADR-127: Free stays off the debugger surface — the
			// abuse-floor tier carries no per-request telemetry,
			// no retention, no rate budget. Handler returns 402
			// ErrPlanFeatureGated before the store is touched.
			DebugTelemetryEnabled: false, DebugTelemetryRetentionDays: 0, DebugTelemetryRequestsPerMinute: 0, DebugTelemetryDeploymentsPerApp: 0, DebugTelemetrySpansPerTrace: 0,
			// ADR-124: Free keeps cancel + clear-obsolete; reorder
			// stays plan-gated (Free=false).
			QueueControlsAllowed: false, MaxQueuedDeploysPerApp: 2, MaxCancelOpsPerHour: 0, MaxReorderOpsPerHour: 0},
		PlanHobby: {Plan: PlanHobby, DeployedApps: 5, MaxConcurrency: 2, RAMMB: 256, AppLayerMaxMB: 512, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 60, CertExpiryWarningDays: 30, IncludedGBHours: 50, PriceMillicents: 900_000, RateLimitRPS: 20, RateLimitBurst: 100, EgressMbit: 25, SecretCountMax: 25, SecretValueMaxBytes: 8192, MaxMinInstances: 1,
			// Issue #559: Hobby = 5 (smallest paid tier — one Node
			// event loop comfortably handles 5 concurrent requests).
			ConcurrencyPerVMBound: 5,
			// Issue #472 / ADR-058: Hobby gets 4 trusted publishers — covers the
			// typical CI rotation surface (GitHub Actions + GitLab + Jenkins +
			// in-house) without letting one app accumulate an unbounded allowlist.
			TrustedSignerCountMax: 4,
			// Issue #395 / ADR-045: Hobby gets 32 keys / 8 KB per value.
			EnvVarsMax: 32, EnvValueMaxBytes: 8192,
			// ADR-044: see PlanFree. Hobby's tight quota is the
			// load-bearing signal in the cpu-fairness e2e (cmd/e2e/cpu_fairness_test.go).
			CPUWeight: 4, CPUQuotaUS: 200_000, CPUPeriodUS: 200_000,
			MaxQueueDepth: 5, MaxDelayedTasksPerApp: 5, MaxSourceBytesPerInvocation: 64 * 1024, AsyncInvokeAllowed: true,
			// Issue #394: Hobby gets 3 retries before dead-letter — a
			// poisoned row exits within ~15s at the default 5s backoff
			// without thrashing the worker for long.
			MaxQueueAttempts: 3,
			// ADR-134 PR-B: Hobby 1k / 1h / 7d.
			MaxAsyncInvocationsPerAccount:     1000,
			MaxAsyncInvocationDeadlineSeconds: 3600,
			MaxAsyncResultRetentionSeconds:    604800,
			// Issue #462 / ADR-058 / PR-A: Hobby unlocks the warm
			// floor (MinInstancesAllowed) and the max_instances
			// ceiling (MaxInstancesAllowed). Hobby is still
			// gated out of the autoscale_target_rps /
			// autoscale_target_cpu_pct knobs (the cost shape
			// rationale is unchanged). The bill auto-counts
			// (pkg/meter/sampler.go:238-239) so the warm floor
			// has a bounded cost.
			MinInstancesAllowed: true, MaxInstancesAllowed: true,
			// Issue #169 / #172: Hobby is gated on Pro+ for both RPS
			// and CPU (2026-07-28: ADR-037 amendment — Hobby→Pro re-tier
			// on ScaleUpTargetRPSAllowed). CPU-driven scaling is gated
			// on Pro+ for cost reasons.
			ScaleUpTargetRPSAllowed: false, ScaleUpTargetCPUAllowed: false,
			// Cron: Hobby gets 5 per-app and 10 per-account.
			CronLimitPerApp: 5, CronLimitPerAccount: 10,
			// M-2 / ADR-137+138: Hobby's 30 s stop-grace, 30 s
			// startup-deadline, 5-retry caps. Hobby unlocks worker
			// (1) + service (3 replicas) + 5-min job runtime.
			DefaultStopGracePeriodS: 30, DefaultStartupDeadlineS: 30, DefaultMaxRetries: 5,
			WorkerReplicasMax: 1, ServiceReplicasMax: 3, JobMaxRuntimeS: 300,
			// Issue #475: Hobby gets 1 reserved-tier app.
			EvictionPriorityReservedAllowed: true, ReservedConcurrencyPerAccount: 1,
			// Issue #477 / ADR-079: Hobby unlocks bearer (admin
			// endpoints + private webhook receivers) but basic stays
			// gated off — the Hobby customer shape doesn't typically
			// need sealed-credential storage cost.
			PublicAuthBearerAllowed: true, PublicAuthBasicAllowed: false,
			// ADR-045 (#396): Hobby gets 3 per-app and 10 per-account.
			AlertRuleLimitPerApp: 3, AlertRuleLimitPerAccount: 10, AlertPresetCatalogLimitPerAccount: 8,
			// ADR-089 (planned): edge rules — Hobby unlocks 25 rules
			// AND the jwt|ip kinds. The plan-kind gate surface
			// (EdgeRulesJWTAllowed / EdgeRulesIPAllowed) feeds the
			// 402 response in handlers_edge_rules.go for Free.
			EdgeRulesPerApp: 25, EdgeRulesJWTAllowed: true, EdgeRulesIPAllowed: true, EdgeRulesGeoPerApp: 5, EdgeRulesThrottlePerApp: 5, EdgeRulesCachePerApp: 1,
			// issue #975 #4 / Mega-Foundation #979-b — Hobby is the entry paid tier.
			CorsPresetsPerAccount: 10, CorsPresetsPerApp: 5, CorsPresetMaxOrigins: 25, CorsPresetMaxAllowMethods: 8, CorsPresetMaxNameLength: 64,
			// ADR-099 (#879): tenant surfaces — Hobby is the entry
			// paid tier. 1 surface with up to 10 verified hostnames.
			// The "single SaaS customer, handful of end-customer
			// subdomains" use case is the Hobby use case.
			TenantSurfacesPerAccount: 1, TenantHostnamesPerSurface: 10, TenantSurfacesAllowed: true,
			DataPlacementHintsPerApp: 3,
			// IAM-6 / ADR-061 PR-2 (issue #190): Hobby tracks KeysMax
			// (10) one-to-one. Pending invitations = members/2
			// because the default 7d TTL keeps the live set small.
			// Financial model is authoritative — derived value,
			// reconciliation follow-up.
			OrgMembersMax: 10, OrgPendingInvitationsMax: 5,
			// ADR-076 (#476): Hobby gets 3 per-app and 10 per-account
			// — mirrors the alert-rule ratio.
			WebhookPerApp: 3, WebhookPerAccount: 10,
			// ADR-0NN (#757): Hobby unlocks the in-platform queue +
			// sqs_compat kinds. Tight caps (50/30s/3) so a Hobby
			// customer's fan-out can't saturate schedd's per-app
			// WakeRateLimiter bucket.
			TriggersAllowed: true, TriggerLimitPerApp: 2, TriggerLimitPerAccount: 10, TriggerBatchSizeMax: 50, TriggerBatchWindowMaxSec: 30, TriggerMaxAttemptsMax: 3, TriggerRecordsPerSecondPerApp: 100, TriggerPayloadMaxBytes: 1048576, MaxESMSourcesPerApp: 2, MaxESMRecordsPerSecond: 100, BrokerEgressMbit: 10, TLSSkipVerifyAllowed: false,
			// ADR-040: Hobby gets 200/min — ~10× the per-app rps (20),
			// so the per-app limit trips first on a single hot app and
			// the account limit catches the cross-app botnet signature.
			RateLimitPerAccountRPM: 200,
			// ADR-104: Hobby gets 1000 — meaningful per-key
			// cardinality on a small/medium deployment.
			ThrottleMaxKeysPerRule: 1000,
			// ADR-099 PR-0: Hobby wake-admission throttle (5/10).
			WakeBurstPerApp:     5,
			WakeBurstPerAccount: 10,
			// Issue #471 / ADR-047 (PR-A): Hobby unlocks streaming
			// (100 MiB / 900 s) — the first paid tier. PR-A wires
			// the flag + accessor; PR-B activates the Flusher path.
			StreamingEnabled: true, MaxResponseBodyBytes: 104_857_600, ResponseWriteTimeoutSeconds: 900,
			// Issue #676 / ADR-080: Hobby unlocks the raw-bytes
			// Upgrade bridge — many agent / LLM SDKs speak WS over a
			// thin HTTP boundary, and Hobby is the tier where those
			// workloads land first.
			WebSocketEnabled: true,
			// ADR-093: Hobby unlocks per-route observability as
			// the first paid tier. The bounded cap (50 distinct
			// real routes + __route_other__ overflow per app)
			// is what makes this safe to enable across the
			// paid tiers without per-tenant cardinality risk.
			RouteMetricsEnabled: true,
			// Issue #517 / PR-B: Hobby unlocks the `?deployment=`
			// filter for the typical one-staging-deployment workload.
			LogDeploymentFilterMax: 1,
			// Issue #461 / ADR-064: Hobby = 2 — staging + production.
			RegistryCredentialMax: 2,
			// Issue #470 / ADR-055: Hobby is gated off for the
			// same cost-shape reason as Free — the doubled parked
			// footprint doesn't fit the €9/month Hobby tier.
			WarmSnapshotEnabled: false, WarmSnapshotMinRequestsDefault: 0, WarmSnapshotMinMsDefault: 0,
			// Issue #560: Hobby is gated off for the same
			// posture-change shape as Free.
			RequireAuthn: false,
			// ADR-124: Hobby unlocks gRPC framing. Hobby is the
			// smallest paid tier where the gRPC service-migration
			// use case (issue #67) makes sense — Free stays
			// gated to http1/http2 only.
			AppProtocolGrpcAllowed: true,
			// Issue #695 / ADR-080: Hobby unlocks the token gate but
			// bearer is still gated off (see PublicAuthBearerAllowed
			// above) — the default opens the require_authn=true
			// surface but stays on the 'open' mode so customers have
			// a working PATCH escape hatch without us stranding them
			// on a default they can't realise. PublicAuthModeDefault
			// flips to 'bearer' in a follow-up if Hobby is opened up.
			RequireAuthnDefault: true, PublicAuthModeDefault: "open",
			// Issue #554 / ADR-078: Hobby unlocks liveness — the
			// first paid tier gets the Cloud Run-parity primitive.
			// 5s period / 3 consecutive / 60s cooldown / 3 in 300s.
			LivenessPeriodSeconds: 5, LivenessConsecutiveFailures: 3, LivenessCooldownSeconds: 60, LivenessMaxRestarts: 3, LivenessWindowSeconds: 300,
			// Issue #189 / IAM-5: Hobby = 10 keys (2 per app across 5 apps).
			KeysMax: 10,
			// Issue #667 / ADR-078: tail primitive on at 15 s.
			TailEnabled: true, TailTimeoutS: 15, TailCapMax: 16, ConcurrentTailsPerInstance: 16,
			// Issue #562: Hobby unlocks log archive with 7-day retention.
			LogArchiveEnabled: true, LogArchiveRetentionDaysMax: 7,
			// ADR-096: Hobby = 7 days, 200 fingerprints, 100 request rows.
			AppErrorsRetentionDays: 7, AppErrorsMaxFingerprintsPerApp: 200, AppErrorsMaxRequestRowsPerFingerprint: 100,
			// ADR-120 / issue #975 item #5: consumer keys — Hobby gets
			// 100 per app and 250 per account. Same per-app budget as
			// Pro (the Hobby customer shape — small SaaS / hobbyist —
			// is the same one-app-many-keys demand that Pro addresses
			// at higher concurrency). Account ceiling is the abuse
			// floor; Hobby's typical 5-app footprint stays well under.
			ConsumerKeysPerApp: 100, ConsumerKeysPerAccount: 250,
			// ADR-122 / issue #975 item #1: Hobby is the entry paid
			// tier — 1/dep, 100/acct, 128 KiB/doc.
			OpenAPIDocsPerDeployment: 1, OpenAPIDocMaxBytes: 131072, OpenAPIDocsPerAccount: 100, OpenAPIImportsPerAccount: 1000,
			// ADR-127: Hobby = "last week" debugger surface — 3-day
			// retention (matches log-archive retention), 1000
			// req/min ingest, 10 deployments max in the histogram
			// (small Hobby app), 50 spans per trace.
			DebugTelemetryEnabled: true, DebugTelemetryRetentionDays: 3, DebugTelemetryRequestsPerMinute: 1000, DebugTelemetryDeploymentsPerApp: 10, DebugTelemetrySpansPerTrace: 50, PerAppMetricsAllowed: true, AppUsageSummaryAllowed: true, AppErrorsAllowed: true, JobsAllowed: true, WorkflowsAllowed: true, WorkflowMaxPerApp: 3, WorkflowMaxConcurrent: 10, WorkflowStepMaxTimeout: 10 * time.Minute, WorkflowMaxWaitDays: 7,
			// ADR-124 queue controls — Hobby unlocks the gated surface.
			QueueControlsAllowed: true, MaxQueuedDeploysPerApp: 5, MaxCancelOpsPerHour: 120, MaxReorderOpsPerHour: 60},
		// ADR-031: Pro opt-in for per-app egress allowlist with a 16-CIDR cap.
		PlanPro: {Plan: PlanPro, DeployedApps: 25, MaxConcurrency: 5, RAMMB: 512, AppLayerMaxMB: 1024, SourceTarballMaxMB: 250, VCPU: 2, IdleTimeoutS: 300, CertExpiryWarningDays: 30, IncludedGBHours: 250, PriceMillicents: 2_900_000, RateLimitRPS: 100, RateLimitBurst: 500, EgressMbit: 100, SecretCountMax: 50, SecretValueMaxBytes: 16384, MaxMinInstances: 3,
			// Issue #559: Pro = 25 (typical SaaS-tier workload
			// envelope — one Node/Python service handling fan-out).
			ConcurrencyPerVMBound: 25,
			// Issue #472 / ADR-058: Pro gets 8 trusted publishers — 2× Hobby for the
			// larger team rotation surface (multiple repos × multiple CI providers).
			TrustedSignerCountMax: 8,
			// Issue #395 / ADR-045: Pro gets 64 keys / 16 KB per value.
			EnvVarsMax: 64, EnvValueMaxBytes: 16384,
			// Issue #462 / ADR-058: Pro unlocks warm-floor + max-instances
			// ceiling (was min-instances only at the pre-#462 contract).
			MinInstancesAllowed: true, MaxInstancesAllowed: true,
			// ADR-044: see PlanFree.
			CPUWeight: 8, CPUQuotaUS: 500_000, CPUPeriodUS: 500_000,
			MaxQueueDepth: 25, MaxDelayedTasksPerApp: 50, MaxSourceBytesPerInvocation: 256 * 1024, AsyncInvokeAllowed: true,
			// Issue #394: Pro gets 10 retries — 5× Hobby's budget.
			// Tolerates a transient downstream flap while still bounding
			// the "permanently bad payload" worker cost.
			MaxQueueAttempts: 10,
			// ADR-134 PR-B: Pro 10k / 6h / 30d.
			MaxAsyncInvocationsPerAccount:     10000,
			MaxAsyncInvocationDeadlineSeconds: 21600,
			MaxAsyncResultRetentionSeconds:    2592000,
			EgressAllowlistAllowed:            true, EgressAllowlistMaxSize: 16,
			// Issue #477 / ADR-118: Pro unlocks the per-app ingress
			// IP allowlist. Same 16-entry cap as the egress
			// allowlist — symmetric abuse-desk primitives.
			PublicAuthIPAllowlistAllowed: true, PublicAuthIPAllowlistMaxEntries: 16,
			// Issue #169 / #172: Pro unlocks both RPS and CPU targets.
			ScaleUpTargetRPSAllowed: true, ScaleUpTargetCPUAllowed: true,
			// Cron: Pro gets 20 per-app and 50 per-account.
			CronLimitPerApp: 20, CronLimitPerAccount: 50,
			// M-2 / ADR-137+138: Pro doubles Hobby (60 s grace,
			// 60 s deadline, 10 retries). 3 workers, 5 service
			// replicas, 30-min job runtime.
			DefaultStopGracePeriodS: 60, DefaultStartupDeadlineS: 60, DefaultMaxRetries: 10,
			WorkerReplicasMax: 3, ServiceReplicasMax: 5, JobMaxRuntimeS: 1800,
			// Issue #475: Pro gets 2 reserved-tier apps.
			EvictionPriorityReservedAllowed: true, ReservedConcurrencyPerAccount: 2,
			// Issue #477 / ADR-079: Pro unlocks both bearer + basic.
			// Basic is the right shape for Pro's typical webhook-
			// receiver / admin-endpoint use cases.
			PublicAuthBearerAllowed: true, PublicAuthBasicAllowed: true,
			// ADR-045 (#396): Pro gets 10 per-app and 30 per-account.
			AlertRuleLimitPerApp: 10, AlertRuleLimitPerAccount: 30, AlertPresetCatalogLimitPerAccount: 8,
			// ADR-089 (planned): edge rules — Pro unlocks 100 rules
			// AND jwt|ip. Same surface as Hobby; the gate only
			// flips the Free arm of the kind-switch.
			EdgeRulesPerApp: 100, EdgeRulesJWTAllowed: true, EdgeRulesIPAllowed: true, EdgeRulesGeoPerApp: 25, EdgeRulesThrottlePerApp: 25, EdgeRulesCachePerApp: 5,
			// issue #975 #4 / Mega-Foundation #979-b — Pro is the typical SaaS tier.
			CorsPresetsPerAccount: 50, CorsPresetsPerApp: 15, CorsPresetMaxOrigins: 100, CorsPresetMaxAllowMethods: 8, CorsPresetMaxNameLength: 64,
			// ADR-099 (#879): tenant surfaces — Pro gets 5 surfaces
			// with up to 50 verified hostnames each. Each surface
			// still binds to one app (the multi-app variant is the
			// deferred footgun).
			TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 50, TenantSurfacesAllowed: true,
			DataPlacementHintsPerApp: 10,
			// IAM-6 / ADR-061 PR-2 (issue #190): Pro tracks KeysMax
			// (50) one-to-one — every team member can hold a key
			// for their own deploy target. Financial model is
			// authoritative — derived value, reconciliation follow-up.
			OrgMembersMax: 50, OrgPendingInvitationsMax: 25,
			// ADR-076 (#476): Pro gets 10 per-app and 30 per-account
			// — mirrors the alert-rule ratio.
			WebhookPerApp: 10, WebhookPerAccount: 30,
			// ADR-0NN (#757): Pro is the first tier where external
			// broker kinds unlock (Kafka/NATS/Redis-streams). Caps jump
			// to 10/50 + 500/5min/10 attempts so a Pro customer's
			// 1k-msg/s Kafka consumer can be drained with one trigger.
			TriggersAllowed: true, TriggerLimitPerApp: 10, TriggerLimitPerAccount: 50, TriggerBatchSizeMax: 500, TriggerBatchWindowMaxSec: 300, TriggerMaxAttemptsMax: 10, TriggerRecordsPerSecondPerApp: 1000, TriggerPayloadMaxBytes: 6291456, MaxESMSourcesPerApp: 10, MaxESMRecordsPerSecond: 1000, BrokerEgressMbit: 50, TLSSkipVerifyAllowed: true,
			// ADR-040: Pro gets 1000/min — ~10× the per-app rps (100).
			RateLimitPerAccountRPM: 1000,
			// ADR-104: Pro gets 5000 — meaningful per-tenant
			// cardinality on a multi-tenant deployment.
			ThrottleMaxKeysPerRule: 5000,
			// ADR-099 PR-0: Pro wake-admission throttle (20/30).
			WakeBurstPerApp:     20,
			WakeBurstPerAccount: 30,
			// Issue #471 / ADR-047 (PR-A): Pro keeps the same streaming
			// envelope as Hobby. The cap is the same; the per-app
			// streaming path is gatewayd-internal-edged, not per-tier.
			StreamingEnabled: true, MaxResponseBodyBytes: 104_857_600, ResponseWriteTimeoutSeconds: 900,
			// Issue #676 / ADR-080: Pro unlocks the raw-bytes
			// Upgrade bridge for the same reason as Hobby — production
			// workloads at this tier run agent / WS-backed services.
			WebSocketEnabled: true,
			// ADR-093: Pro mirrors Hobby — per-route observability
			// on by default; same bounded cap (50 + overflow).
			RouteMetricsEnabled: true,
			// Issue #517 / PR-B: Pro gets 10 — covers the typical
			// multi-staging fan-out (prod + 3-5 staging + a few
			// preview slots) without monopolising the schedd's
			// per-instance goroutine fan-out.
			LogDeploymentFilterMax: 10,
			// Issue #461 / ADR-064: Pro = 5 — multi-region + CI shapes.
			RegistryCredentialMax: 5,
			// Issue #470 / ADR-055: Pro is the first tier where
			// warm-tier snapshots are on by default — 5 requests /
			// 2000 ms is the sweet spot for the issue's acceptance
			// (p50 halved vs init-tier).
			WarmSnapshotEnabled: true, WarmSnapshotMinRequestsDefault: 5, WarmSnapshotMinMsDefault: 2000,
			// Issue #560: Pro is the first tier where the
			// per-app require_authn opt-in unlocks.
			RequireAuthn: true,
			// Issue #695 / ADR-080: Pro is the first tier where
			// the new-app default flips to authenticated. Both
			// 'bearer' and basic are unlocked here, so the mode
			// can be the secure-by-default 'bearer' shape.
			// Issue #556 PR-A: Pro unlocks traffic-split
			// (issue #556 "Pro+ canary"). Hobby's value-prop
			// stays "near-Free with a floor" — canary
			// rollout adds RAM-billable live deployments
			// the Hobby plan doesn't subsidise.
			RequireAuthnDefault: true, PublicAuthModeDefault: "bearer",
			// Issue #556 PR-A: Pro unlocks traffic splitting.
			TrafficSplit: true,
			// Issue #72 / ADR-125: Pro unlocks traffic mirroring
			// (one shadow deployment per app for canary-shadow
			// comparisons). Hobby/Free stay gated — the mirror
			// path bills a parallel VM and the Hobby plan's
			// value-prop doesn't subsidise it.
			MirrorRuleAllowed: true, MirrorTargetsPerApp: 1,
			// ADR-124: Pro unlocks gRPC framing (matches Hobby —
			// both paid tiers).
			AppProtocolGrpcAllowed: true,
			// Issue #554 / ADR-078: Pro inherits the same liveness
			// defaults as Hobby (5s / 3 / 60s / 3 in 300s). Pro is
			// the unlock point for `GRPCLivenessAllowed()` once v2
			// lands; v1 returns false across all plans.
			LivenessPeriodSeconds: 5, LivenessConsecutiveFailures: 3, LivenessCooldownSeconds: 60, LivenessMaxRestarts: 3, LivenessWindowSeconds: 300,
			// Issue #189 / IAM-5: Pro = 50 keys (2 per app across 25 apps).
			KeysMax: 50,
			// Issue #667 / ADR-078: tail primitive on at 30 s.
			TailEnabled: true, TailTimeoutS: 30, TailCapMax: 16, ConcurrentTailsPerInstance: 64,
			// Issue #562: Pro extends retention to 30 days.
			LogArchiveEnabled: true, LogArchiveRetentionDaysMax: 30,
			// ADR-096: Pro = 30 days, 1000 fingerprints, 500 request rows.
			AppErrorsRetentionDays: 30, AppErrorsMaxFingerprintsPerApp: 1000, AppErrorsMaxRequestRowsPerFingerprint: 500,
			// ADR-120 / issue #975 item #5: consumer keys — Pro gets
			// 100 per app and 2500 per account. 25 apps × 100 = 2500
			// (the per-app ceiling × DeployedApps fits inside the per-
			// account envelope, so neither side trips first on the
			// typical Pro customer).
			ConsumerKeysPerApp: 100, ConsumerKeysPerAccount: 2500,
			// ADR-122 / issue #975 item #1: Pro keeps 1/dep
			// (PK constraint), 1000/acct (10× Hobby).
			OpenAPIDocsPerDeployment: 1, OpenAPIDocMaxBytes: 131072, OpenAPIDocsPerAccount: 1000, OpenAPIImportsPerAccount: 10000,
			// ADR-127: Pro = "this month" debugger surface — 7-day
			// retention, 10000 req/min ingest, 50 deployments in
			// the histogram, 200 spans per trace.
			DebugTelemetryEnabled: true, DebugTelemetryRetentionDays: 7, DebugTelemetryRequestsPerMinute: 10000, DebugTelemetryDeploymentsPerApp: 50, DebugTelemetrySpansPerTrace: 200, PerAppMetricsAllowed: true, AppUsageSummaryAllowed: true, AppErrorsAllowed: true, JobsAllowed: true, WorkflowsAllowed: true, WorkflowMaxPerApp: 10, WorkflowMaxConcurrent: 50, WorkflowStepMaxTimeout: 30 * time.Minute, WorkflowMaxWaitDays: 7,
			// ADR-124: Pro mirrors Hobby for queue controls.
			QueueControlsAllowed: true, MaxQueuedDeploysPerApp: 10, MaxCancelOpsPerHour: 120, MaxReorderOpsPerHour: 60},
		// ADR-031: Scale double-up to 64 CIDR cap (2× Pro, tracks 2×
		// DeployedApps).
		PlanScale: {Plan: PlanScale, DeployedApps: 100, MaxConcurrency: 20, RAMMB: 1024, AppLayerMaxMB: 2048, SourceTarballMaxMB: 250, VCPU: 4, IdleTimeoutS: 600, CertExpiryWarningDays: 30, IncludedGBHours: 1500, PriceMillicents: 9_900_000, RateLimitRPS: 500, RateLimitBurst: 2000, EgressMbit: 250, SecretCountMax: 100, SecretValueMaxBytes: 32768, MaxMinInstances: 10,
			// Issue #559: Scale = 80 (matches Cloud Run's
			// `80 × vCPU` default per the issue body).
			ConcurrencyPerVMBound: 80,
			// Issue #472 / ADR-058: Scale gets 16 trusted publishers — 2× Pro for the
			// enterprise rotation surface (multi-team, multi-cloud, multi-CI).
			TrustedSignerCountMax: 16,
			// Issue #395 / ADR-045: Scale gets 256 keys / 32 KB per value.
			EnvVarsMax: 256, EnvValueMaxBytes: 32768,
			// Issue #462 / ADR-058: Scale unlocks warm-floor +
			// max-instances ceiling (same as Pro).
			MinInstancesAllowed: true, MaxInstancesAllowed: true,
			// ADR-044: see PlanFree. Scale's 1000ms/100ms quota is the
			// upper bound — 10 vCPU worth of compute at the per-instance
			// level, gated by the §1 56 GB hard fence at the slice level.
			CPUWeight: 16, CPUQuotaUS: 1_000_000, CPUPeriodUS: 1_000_000,
			MaxQueueDepth: 100, MaxDelayedTasksPerApp: 1_000_000, MaxSourceBytesPerInvocation: 1024 * 1024, AsyncInvokeAllowed: true,
			// Issue #394: Scale gets 25 retries — 2.5× Pro's budget, but
			// capped so a genuinely-bad payload still terminates within
			// the worker's hourly budget window.
			MaxQueueAttempts: 25,
			// ADR-134 PR-B: Scale 100k / 24h / 90d.
			MaxAsyncInvocationsPerAccount:     100000,
			MaxAsyncInvocationDeadlineSeconds: 86400,
			MaxAsyncResultRetentionSeconds:    7776000,
			EgressAllowlistAllowed:            true, EgressAllowlistMaxSize: 64,
			// ADR-119: Scale unlocks static egress IP (per-app quota=1).
			StaticEgressIPAllowed: true, StaticEgressIPsPerApp: 1,
			// Issue #477 / ADR-118: Scale gets a 64-entry cap, 4× Pro
			// (same ladder as EgressAllowlistMaxSize; SaaS-scale
			// customers with multi-region deployments enumerate more
			// per-region ranges than a Pro-tier app).
			PublicAuthIPAllowlistAllowed: true, PublicAuthIPAllowlistMaxEntries: 64,
			// Issue #169 / #172: Scale unlocks both targets (same rationale as Pro).
			ScaleUpTargetRPSAllowed: true, ScaleUpTargetCPUAllowed: true,
			// Cron: Scale gets 100 per-app and 500 per-account.
			CronLimitPerApp: 100, CronLimitPerAccount: 500,
			// M-2 / ADR-137+138: Scale doubles Pro (120 s grace,
			// 120 s deadline, 20 retries). 10 workers, 20
			// service replicas, 1-h job runtime.
			DefaultStopGracePeriodS: 120, DefaultStartupDeadlineS: 120, DefaultMaxRetries: 20,
			WorkerReplicasMax: 10, ServiceReplicasMax: 20, JobMaxRuntimeS: 3600,
			// Issue #475: Scale gets 4 reserved-tier apps.
			EvictionPriorityReservedAllowed: true, ReservedConcurrencyPerAccount: 4,
			// Issue #477 / ADR-079: Scale unlocks both bearer + basic.
			PublicAuthBearerAllowed: true, PublicAuthBasicAllowed: true,
			// ADR-045 (#396): Scale gets 25 per-app and 100 per-account.
			AlertRuleLimitPerApp: 25, AlertRuleLimitPerAccount: 100, AlertPresetCatalogLimitPerAccount: 8,
			// ADR-089 (planned): edge rules — Scale unlocks 500 rules
			// (5× Pro) AND jwt|ip. The 500 cap is the practical upper
			// bound the LRU + per-host matcher budget tolerates before
			// per-host invalidation becomes load-bearing.
			EdgeRulesPerApp: 500, EdgeRulesJWTAllowed: true, EdgeRulesIPAllowed: true, EdgeRulesGeoPerApp: 100, EdgeRulesThrottlePerApp: 100, EdgeRulesCachePerApp: 20,
			// issue #975 #4 / Mega-Foundation #979-b — Scale is the large-fleet tier.
			CorsPresetsPerAccount: 250, CorsPresetsPerApp: 50, CorsPresetMaxOrigins: 500, CorsPresetMaxAllowMethods: 8, CorsPresetMaxNameLength: 64,
			// ADR-099 (#879): tenant surfaces — Scale gets 25 surfaces
			// with up to 250 verified hostnames each. The 250 cap is
			// bounded by LE's 100-SAN-per-cert limit (per_host_san
			// falls back to per_host above ~100, surfaced via the
			// cert engine, not quota).
			TenantSurfacesPerAccount: 25, TenantHostnamesPerSurface: 250, TenantSurfacesAllowed: true,
			DataPlacementHintsPerApp: 50,
			// IAM-6 / ADR-061 PR-2 (issue #190): Scale tracks KeysMax
			// (200) one-to-one — SaaS-scale multi-team + rotating-CI.
			// Financial model is authoritative — derived value,
			// reconciliation follow-up.
			OrgMembersMax: 200, OrgPendingInvitationsMax: 100,
			// ADR-076 (#476): Scale gets 25 per-app and 100 per-account
			// — mirrors the alert-rule ratio.
			WebhookPerApp: 25, WebhookPerAccount: 100,
			// ADR-0NN (#757): Scale is the upper tier — caps align with
			// the SQL CHECK ceilings (5000 records / 5 min window /
			// 25 attempts) so a Scale customer's SQS-compatible or
			// Kafka consumer can be drained at full throughput.
			TriggersAllowed: true, TriggerLimitPerApp: 50, TriggerLimitPerAccount: 200, TriggerBatchSizeMax: 5000, TriggerBatchWindowMaxSec: 300, TriggerMaxAttemptsMax: 25, TriggerRecordsPerSecondPerApp: 10000, TriggerPayloadMaxBytes: 16777216, MaxESMSourcesPerApp: 50, MaxESMRecordsPerSecond: 10000, BrokerEgressMbit: 200, TLSSkipVerifyAllowed: true,
			// Issue #72 / ADR-125: Scale = 3 mirror targets per
			// app — SaaS-scale multi-region customers run parallel
			// canary shadows against multiple staging stacks (e.g.
			// one per tenant surface). 3 is the upper bound the
			// picker cache refresh path can validate inside the
			// deployment_changed pg_notify fanout window.
			MirrorRuleAllowed: true, MirrorTargetsPerApp: 3,
			// ADR-040: Scale gets 5000/min — ~10× the per-app rps (500).
			// The fleet-summed alert at 100/min/5m (FaasPerAccountRateLimitSpike)
			// triggers well before any single paid customer's bucket fills.
			RateLimitPerAccountRPM: 5000,
			// ADR-104: Scale gets 10000 — full per-tenant
			// cardinality on a multi-tenant SaaS deployment.
			ThrottleMaxKeysPerRule: 10000,
			// ADR-099 PR-0: Scale wake-admission throttle (100/150).
			WakeBurstPerApp:     100,
			WakeBurstPerAccount: 150,
			// Issue #471 / ADR-047 (PR-A): Scale keeps the same envelope
			// as Hobby/Pro. The streaming cap is uniform across paid
			// tiers — the spec's paid-only unlock is the boolean, not
			// the byte/time ceiling.
			StreamingEnabled: true, MaxResponseBodyBytes: 104_857_600, ResponseWriteTimeoutSeconds: 900,
			// Issue #676 / ADR-080: Scale unlocks the raw-bytes
			// Upgrade bridge — production WS-backed services sit at
			// this tier.
			WebSocketEnabled: true,
			// ADR-093: Scale mirrors Hobby/Pro — per-route
			// observability on by default. The 50-cap is per-app;
			// Scale customers can run more apps, not more routes
			// per app, so the cap stays the same across plans.
			RouteMetricsEnabled: true,
			// Issue #517 / PR-B: Scale gets 50 — 5× Pro, tracks the
			// larger app budget (100 vs 25) and multi-region staging
			// fan-out SaaS-scale customers typically run.
			LogDeploymentFilterMax: 50,
			// Issue #461 / ADR-064: Scale = 20 — broad fan-out.
			RegistryCredentialMax: 20,
			// Issue #470 / ADR-055: Scale stays on by default —
			// the per-app parked cost fits inside the 452 GB
			// budget and the wake-p50 win is the largest dollar
			// lever for SaaS workloads.
			WarmSnapshotEnabled: true, WarmSnapshotMinRequestsDefault: 5, WarmSnapshotMinMsDefault: 2000,
			// Issue #560: Scale mirrors Pro — opt-in
			// available, column default still false.
			RequireAuthn: true,
			// Issue #695 / ADR-080: Scale mirrors Pro — secure-by-default
			// at the new-app stamp. Both bearer and basic are unlocked
			// here, so the mode can stay 'bearer'.
			// Issue #556 PR-A: Scale mirrors Pro — canary rollout
			// is part of the production-tier value-prop.
			RequireAuthnDefault: true, PublicAuthModeDefault: "bearer",
			// Issue #556 PR-A: Pro unlocks traffic splitting.
			TrafficSplit: true,
			// ADR-124: Scale mirrors Pro — gRPC framing unlocked.
			AppProtocolGrpcAllowed: true,
			// Issue #554 / ADR-078: Scale mirrors Pro — same
			// 5s / 3 / 60s / 3 in 300s defaults. The
			// per-deployment override column on deployments is the
			// surface a High-traffic Scale customer uses to
			// lengthen the window without a code change.
			LivenessPeriodSeconds: 5, LivenessConsecutiveFailures: 3, LivenessCooldownSeconds: 60, LivenessMaxRestarts: 3, LivenessWindowSeconds: 300,
			// Issue #189 / IAM-5: Scale = 200 keys (2 per app across 100 apps).
			KeysMax: 200,
			// Issue #667 / ADR-078: tail primitive on at 60 s.
			TailEnabled: true, TailTimeoutS: 60, TailCapMax: 16, ConcurrentTailsPerInstance: 256,
			// Issue #562: Scale extends retention to 90 days.
			LogArchiveEnabled: true, LogArchiveRetentionDaysMax: 90,
			// ADR-096: Scale = 90 days, 5000 fingerprints, 1000 request rows.
			AppErrorsRetentionDays: 90, AppErrorsMaxFingerprintsPerApp: 5000, AppErrorsMaxRequestRowsPerFingerprint: 1000,
			// ADR-120 / issue #975 item #5: consumer keys — Scale gets
			// 1000 per app and 25000 per account. 100 apps × 1000 =
			// 100000 if every app maxes out; the 25000-account ceiling
			// is the abuse-floor — the typical Scale customer
			// (multi-tenant SaaS broker) uses 25-30% of the budget per
			// app, well under 100/app and 25000/acct envelopes.
			ConsumerKeysPerApp: 1000, ConsumerKeysPerAccount: 25000,
			// ADR-122 / issue #975 item #1: Scale keeps 1/dep,
			// 10000/acct (10× Pro). Byte cap stays at 128 KiB
			// (global cap is the binding constraint).
			OpenAPIDocsPerDeployment: 1, OpenAPIDocMaxBytes: 131072, OpenAPIDocsPerAccount: 10000, OpenAPIImportsPerAccount: 10000,
			// ADR-127: Scale = "this quarter" debugger surface —
			// 14-day retention, 50000 req/min ingest, 200
			// deployments in the histogram (the deploymentLabelSet
			// discipline is load-bearing at this tier; without the
			// cap, a fleet with thousands of historical deployments
			// would blow up Prometheus cardinality), 1000 spans per
			// trace.
			DebugTelemetryEnabled: true, DebugTelemetryRetentionDays: 14, DebugTelemetryRequestsPerMinute: 50000, DebugTelemetryDeploymentsPerApp: 200, DebugTelemetrySpansPerTrace: 1000, PerAppMetricsAllowed: true, AppUsageSummaryAllowed: true, AppErrorsAllowed: true, JobsAllowed: true, WorkflowsAllowed: true, WorkflowMaxPerApp: 50, WorkflowMaxConcurrent: 200, WorkflowStepMaxTimeout: 2 * time.Hour, WorkflowMaxWaitDays: 7,
			// ADR-124: Scale gets the highest queue depth (25) and
			// the same 60/h reorder budget as Hobby/Pro.
			QueueControlsAllowed: true, MaxQueuedDeploysPerApp: 25, MaxCancelOpsPerHour: 120, MaxReorderOpsPerHour: 60},
	}
	for _, p := range Plans {
		got := MustLimitsFor(p)
		if got != want[p] {
			t.Errorf("limits for %s:\n got  %+v\n want %+v", p, got, want[p])
		}
	}
}

// TestOrgMembersLimits_DerivedFromLadder pins the IAM-6 / ADR-061
// PR-2 contract (issue #190). The handler gates membership creation
// on Plan.OrgMembersMax() and the store reads the same value as a
// defence-in-depth back-stop inside consumeOrgInvitation.
//
// Per-plan values (Free 0/0, Hobby 10/5, Pro 50/25, Scale 200/100)
// are derived from the existing per-plan budget ladder: members
// track KeysMax one-to-one, pending invitations track members/2
// (default 7d invitation TTL keeps the live set small). Free stays
// at 0/0 by plan policy — the abuse-floor tier cannot host shared
// orgs, mirroring the CronLimitPerApp posture.
//
// IMPORTANT: ex44_faas_financial_model.xlsx is the authoritative
// source (CLAUDE.md). If the workbook diverges from these derived
// values, a follow-up PR reconciles — this test must be updated in
// the same PR that changes the workbook. The unknown-plan branch
// keeps failing closed (return 0) so a future contributor cannot
// silently widen a missing-row accessor's behaviour.
func TestOrgMembersLimits_DerivedFromLadder(t *testing.T) {
	want := map[Plan]struct{ members, pending int }{
		PlanFree:  {members: 0, pending: 0}, // abuse-floor: no shared orgs
		PlanHobby: {members: 10, pending: 5},
		PlanPro:   {members: 50, pending: 25},
		PlanScale: {members: 200, pending: 100},
	}
	// Surface unannounced plans: a future contributor who adds a fifth
	// plan row to api.Plans without also filling `want` here must hit a
	// red test, not silently inherit the zero-value {0, 0} Free
	// posture. The map-keyed assertion below already catches missing
	// rows when iterating, but this explicit count check makes the
	// tripwire loud.
	if len(want) != len(Plans) {
		t.Fatalf("derived ladder test out of sync: want %d plans, api.Plans has %d — update both lists together",
			len(want), len(Plans))
	}
	for _, p := range Plans {
		t.Run(string(p), func(t *testing.T) {
			w := want[p]
			if got := p.OrgMembersMax(); got != w.members {
				t.Errorf("Plan(%s).OrgMembersMax() = %d, want %d (derived ladder; reconcile against ex44_faas_financial_model.xlsx)", p, got, w.members)
			}
			if got := p.OrgPendingInvitationsMax(); got != w.pending {
				t.Errorf("Plan(%s).OrgPendingInvitationsMax() = %d, want %d (derived ladder; reconcile against ex44_faas_financial_model.xlsx)", p, got, w.pending)
			}
		})
	}

	// Unknown plan must fail closed (return 0). Mirrors the
	// CronLimitPerApp / AlertRuleLimitPerApp contract — a missing
	// row must NEVER silently inherit a permissive cap.
	if got := Plan("enterprise").OrgMembersMax(); got != 0 {
		t.Errorf("Plan(unknown).OrgMembersMax() = %d, want 0 (fail-closed)", got)
	}
	if got := Plan("enterprise").OrgPendingInvitationsMax(); got != 0 {
		t.Errorf("Plan(unknown).OrgPendingInvitationsMax() = %d, want 0 (fail-closed)", got)
	}
}

// TestOrgAccessorsMatchTable pins that the accessor methods read the
// same value the Limits struct holds. Catches regressions where a
// future contributor edits the struct field but forgets the accessor
// (or vice versa). For PR 1 both fields are 0/0, but the
// relationship must be stable — when PR 2 adds real values, this
// test catches asymmetric drift.
func TestOrgAccessorsMatchTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.OrgMembersMax(), l.OrgMembersMax; got != want {
			t.Errorf("Plan(%s).OrgMembersMax() = %d, table = %d", p, got, want)
		}
		if got, want := p.OrgPendingInvitationsMax(), l.OrgPendingInvitationsMax; got != want {
			t.Errorf("Plan(%s).OrgPendingInvitationsMax() = %d, table = %d", p, got, want)
		}
	}
}

func TestPlansTableCoverage(t *testing.T) {
	if len(Plans) != len(planLimits) {
		t.Fatalf("Plans list (%d) and planLimits table (%d) out of sync", len(Plans), len(planLimits))
	}
	for _, p := range Plans {
		if _, ok := planLimits[p]; !ok {
			t.Errorf("plan %s in Plans but missing from planLimits", p)
		}
	}
}

// TestAdmissionCeilingIs85Percent guards the headroom invariant (§6.2-2): schedd
// admits to 85% of the 56 GB tenant budget.
func TestAdmissionCeilingIs85Percent(t *testing.T) {
	// 0.85 * 56000 = 47600 exactly. Do the check in integers to avoid floats.
	if got := TenantRAMBudgetMB * 85 / 100; got != RAMAdmissionCeilingMB {
		t.Errorf("RAMAdmissionCeilingMB = %d, want 85%% of %d = %d", RAMAdmissionCeilingMB, TenantRAMBudgetMB, got)
	}
	if RAMAdmissionCeilingMB >= TenantSliceMaxMB {
		t.Errorf("admission ceiling %d must sit below the hard slice fence %d", RAMAdmissionCeilingMB, TenantSliceMaxMB)
	}
}

// TestDefaultComputeNodeCeilingMB pins the helper that the synthetic
// default-local row (pkg/state/memstore.go) and the vmmd LoadConfig
// default (cmd/vmmd/config.go) both consume. Today it delegates to
// RAMAdmissionCeilingMB; the test catches any future drift between
// the helper and the constant.
//
// Two assertions cover two regressions:
//   - the value-pinning literal at 47_600 catches a future contributor
//     changing RAMAdmissionCeilingMB without updating the helper, OR
//     changing the helper's body to a non-delegating expression;
//   - the headroom check (helper == 85% of TenantRAMBudgetMB) is the
//     invariant underlying both values, so a regression in either
//     constant alone still surfaces with a targeted message instead
//     of the value-pin's hard-coded number.
//
// PR scale-out readiness #4 callers (memstore seed + vmmd default)
// are pinned in their own test sites so a regression localised to
// the helper surfaces here, not at the production call sites.
func TestDefaultComputeNodeCeilingMB(t *testing.T) {
	const want = 47_600
	if got := DefaultComputeNodeCeilingMB(); got != want {
		t.Errorf("DefaultComputeNodeCeilingMB() = %d, want %d (platform baseline pin)", got, want)
	}
	if got := TenantRAMBudgetMB * 85 / 100; got != DefaultComputeNodeCeilingMB() {
		t.Errorf("DefaultComputeNodeCeilingMB() = %d, want 85%% of %d = %d (headroom invariant)",
			DefaultComputeNodeCeilingMB(), TenantRAMBudgetMB, got)
	}
}

// TestPlansAreMonotonic asserts every quota grows (or holds) from Free→Scale, so
// an upgrade never reduces a customer's allowance.
func TestPlansAreMonotonic(t *testing.T) {
	for i := 1; i < len(Plans); i++ {
		lo := MustLimitsFor(Plans[i-1])
		hi := MustLimitsFor(Plans[i])
		checks := []struct {
			name   string
			lo, hi int
		}{
			{"DeployedApps", lo.DeployedApps, hi.DeployedApps},
			{"MaxConcurrency", lo.MaxConcurrency, hi.MaxConcurrency},
			{"RAMMB", lo.RAMMB, hi.RAMMB},
			{"AppLayerMaxMB", lo.AppLayerMaxMB, hi.AppLayerMaxMB},
			{"IncludedGBHours", lo.IncludedGBHours, hi.IncludedGBHours},
			{"IdleTimeoutS", lo.IdleTimeoutS, hi.IdleTimeoutS},
			{"RateLimitRPS", lo.RateLimitRPS, hi.RateLimitRPS},
			{"EgressMbit", lo.EgressMbit, hi.EgressMbit},
			{"CronLimitPerApp", lo.CronLimitPerApp, hi.CronLimitPerApp},
			{"CronLimitPerAccount", lo.CronLimitPerAccount, hi.CronLimitPerAccount},
			// Issue #475: per-account reserved-tier cap must be
			// monotonic across plans (Free 0 < Hobby 1 < Pro 2 < Scale 4).
			// apid's updateApp path reads this directly.
			{"ReservedConcurrencyPerAccount", lo.ReservedConcurrencyPerAccount, hi.ReservedConcurrencyPerAccount},
			// Issue #395 / ADR-045: env quota must be monotonic like every
			// other gate — Free's 8 < Hobby's 32 < Pro's 64 < Scale's 256,
			// and the per-value byte cap doubles each step.
			{"EnvVarsMax", lo.EnvVarsMax, hi.EnvVarsMax},
			{"EnvValueMaxBytes", lo.EnvValueMaxBytes, hi.EnvValueMaxBytes},
			// Issue #461 / ADR-062: per-app registry credential quota
			// (Free=0 → Hobby=2 → Pro=5 → Scale=20).
			{"RegistryCredentialMax", lo.RegistryCredentialMax, hi.RegistryCredentialMax},
			// Issue #189 / IAM-5: per-account API-key quota
			// (Free=3 → Hobby=10 → Pro=50 → Scale=200).
			{"KeysMax", lo.KeysMax, hi.KeysMax},
			// Issue #559: per-VM concurrency bound must grow with
			// plan (Free=1 → Hobby=5 → Pro=25 → Scale=80). Mirrors
			// MaxConcurrency's monotonicity because a customer's
			// concurrency ceiling should never shrink on upgrade.
			{"ConcurrencyPerVMBound", lo.ConcurrencyPerVMBound, hi.ConcurrencyPerVMBound},
		}
		for _, c := range checks {
			if c.hi < c.lo {
				t.Errorf("%s not monotonic: %s=%d < %s=%d", c.name, Plans[i], c.hi, Plans[i-1], c.lo)
			}
		}
		if hi.PriceMillicents < lo.PriceMillicents {
			t.Errorf("price not monotonic: %s=%d < %s=%d", Plans[i], hi.PriceMillicents, Plans[i-1], lo.PriceMillicents)
		}
	}
}

func TestAdmissionMB(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := l.AdmissionMB(), l.RAMMB+PerVMOverheadMB; got != want {
			t.Errorf("%s AdmissionMB()=%d want %d", p, got, want)
		}
	}
}

func TestIdleTimeoutBounds(t *testing.T) {
	l := MustLimitsFor(PlanPro) // default 300s
	floor, ceiling := l.IdleTimeoutBounds()
	if floor != IdleTimeoutFloorSeconds {
		t.Errorf("floor=%d want %d", floor, IdleTimeoutFloorSeconds)
	}
	if ceiling != 600 {
		t.Errorf("ceiling=%d want 600 (300 * %d)", ceiling, IdleTimeoutMaxMultiple)
	}
}

func TestPlanValidity(t *testing.T) {
	for _, p := range Plans {
		if !p.Valid() {
			t.Errorf("plan %s should be valid", p)
		}
	}
	if Plan("enterprise").Valid() {
		t.Error(`"enterprise" should not be a valid plan`)
	}
	if Plan("").Valid() {
		t.Error("empty plan should not be valid")
	}
	if _, ok := LimitsFor(Plan("nope")); ok {
		t.Error("LimitsFor unknown plan should return ok=false")
	}
}

func TestMustLimitsForPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustLimitsFor should panic on unknown plan")
		}
	}()
	MustLimitsFor(Plan("nope"))
}

// TestPlanMinInstancesAllowed pins the per-plan gate that apid's
// updateApp handler uses for ux_spec §6.5. Free → false (always
// scale to zero); Hobby/Pro/Scale → true (issue #462 / ADR-058
// PR-A tier-up). Unknown plans must default to false (fail-closed:
// a missing plan never silently unlocks a premium feature).
//
// PR-A history (2026-07-31): Hobby was previously false (the
// pre-#462 contract). The Hobby+ tier-up landed because the bill
// auto-counts via pkg/meter/sampler.go:238-239 and Hobby's
// MaxConcurrency is bounded (2) so the worst-case residency cost
// is 2 × RAMMB + 16 MB overhead.
func TestPlanMinInstancesAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.MinInstancesAllowed(); got != c.want {
			t.Errorf("%s.MinInstancesAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestSidecarCapMax pins the global constant (issue #463 / ADR-066
// §Decision 1). The 2-sidecar hard cap is a GLOBAL const, not a
// per-plan matrix field — a future PR may grow this to a per-plan
// matrix if telemetry shows demand, but for PR-A every plan
// inherits the same 2-cap. The companion schema CHECK on
// `deployments.sidecars` (migration 00095) is the second-line
// defence — see migrations/00095_deployments_sidecars_test.go.
func TestSidecarCapMax(t *testing.T) {
	if SidecarCapMax != 2 {
		t.Errorf("SidecarCapMax = %d, want 2 (issue #463 / ADR-066 §Decision 1)", SidecarCapMax)
	}
}

// TestPlanMaxMinInstances pins the per-plan max-floor cap (issue #557
// / ADR-071 §Decision 5). Free 0, Hobby 1, Pro 3, Scale 10. The cap
// is tighter than MaxConcurrency (1/2/5/20) to protect the §6.2-2
// RAM ceiling from a single API call. Unknown plans fail closed
// (return 0) — same contract as TrustedSignerCountMax.
func TestPlanMaxMinInstances(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 1},
		{PlanPro, 3},
		{PlanScale, 10},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.MaxMinInstances(); got != c.want {
			t.Errorf("%s.MaxMinInstances() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanSidecarAllowed pins the per-plan accessor (issue #463 /
// ADR-066 §Decision 1). PR-A's accessor returns true for every
// plan — the load-bearing gate is the GLOBAL `SidecarCapMax`
// constant, not a per-plan matrix. The accessor exists so a future
// per-plan gate (Free = 0, paid = 2) can be wired in one place
// without the apid handler branching on Plan strings. Mirrors
// TestPlanMinInstancesAllowed above.
func TestPlanSidecarAllowed(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		if !p.SidecarAllowed() {
			t.Errorf("%s.SidecarAllowed() = false; PR-A returns true for all plans (global cap is the load-bearing gate)", p)
		}
	}
}

// TestBillableRAMMBWithSidecars pins the sidecar-shape billing
// math (issue #463 / ADR-066 §Decision 6). The billable shutter
// is `plan.RAMMB + Σ(sidecar.ram_mb) + PerVMOverheadMB`: sidecars
// share the per-VM overhead (one netns, one cgroup scope per
// instance), but each sidecar contributes its own RAM. PR-A
// defines the math; PR-B wires the consumer (schedd's admission
// ledger + meterd's sampler).
func TestBillableRAMMBWithSidecars(t *testing.T) {
	cases := []struct {
		name       string
		planRAM    int
		sidecarMBs []int
		want       int
	}{
		// No sidecars: matches BillableRAMMB exactly.
		{"no-sidecars", 256, nil, 256 + PerVMOverheadMB},
		// One init: 256 + 64 + 8 = 328.
		{"one-init-64", 256, []int{64}, 256 + 64 + PerVMOverheadMB},
		// Two sidecars: 256 + 64 + 32 + 8 = 360.
		{"two-sidecars", 256, []int{64, 32}, 256 + 64 + 32 + PerVMOverheadMB},
		// Empty sidecarMBs slice is the no-sidecars shape.
		{"empty-slice", 256, []int{}, 256 + PerVMOverheadMB},
		// Zero in the slice is a "absent / inherit" sentinel — skipped
		// by the helper (the apid handler normalises ram_mb=0 → absent
		// at validation time, but the helper is defensive anyway).
		{"zero-skipped", 256, []int{0, 64}, 256 + 64 + PerVMOverheadMB},
		// Scale shape: 1024 + 64 + 64 + 8 = 1160 (matches ADR-066
		// §Financial-model addendum scenario column).
		{"scale-two-sidecars", 1024, []int{64, 64}, 1024 + 64 + 64 + PerVMOverheadMB},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BillableRAMMBWithSidecars(c.planRAM, c.sidecarMBs)
			if got != c.want {
				t.Errorf("BillableRAMMBWithSidecars(%d, %v) = %d, want %d", c.planRAM, c.sidecarMBs, got, c.want)
			}
		})
	}
}

// TestPlanConcurrencyPerVMBound pins the platform-advertised per-VM
// concurrency bound (issue #559). Free 1, Hobby 5, Pro 25, Scale 80.
// Distinct from MaxConcurrency (the per-app instance cap, free=1 /
// hobby=2 / pro=5 / scale=20) — this is per-VM. Surfaced on GET
// /v1/apps/{slug} as concurrency_per_vm so dashboards + CLI can
// render the bound without reading limits.go. Unknown plans fail
// closed (return 0) — same contract as MaxMinInstances.
func TestPlanConcurrencyPerVMBound(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 1},
		{PlanHobby, 5},
		{PlanPro, 25},
		{PlanScale, 80},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.ConcurrencyPerVMBound(); got != c.want {
			t.Errorf("%s.ConcurrencyPerVMBound() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestConcurrencyPerVMBoundAccessorMatchesTable pins that the
// accessor reads the same value the Limits struct holds. Mirrors
// TestOrgAccessorsMatchTable above — catches regressions where a
// future contributor edits one side but forgets the other.
func TestConcurrencyPerVMBoundAccessorMatchesTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.ConcurrencyPerVMBound(), l.ConcurrencyPerVMBound; got != want {
			t.Errorf("Plan(%s).ConcurrencyPerVMBound() = %d, table = %d", p, got, want)
		}
	}
}

// TestPlanScaleUpTargetRPSAllowed pins the per-plan gate that apid's
// updateApp handler uses for the per-app autoscale_target_rps field
// (issue #172, ADR-037). Free/Hobby → false (Hobby lost the gate
// via the 2026-07-28 Hobby→Pro re-tier — ADR-037 amendment); Pro/Scale
// → true. Unknown plans must default to false (fail-closed: a missing
// plan never silently unlocks a premium feature). Mirrors
// TestPlanMinInstancesAllowed above.
func TestPlanScaleUpTargetRPSAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.ScaleUpTargetRPSAllowed(); got != c.want {
			t.Errorf("%s.ScaleUpTargetRPSAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistAllowed pins the per-plan gate that apid's
// updateApp handler uses for the per-app egress allowlist (ADR-031).
// Free/Hobby → false (no allowlist — abuse-desk hygiene is a Pro+
// concern; the default scale-to-zero tenant never sees this surface);
// Pro/Scale → true. Unknown plans must default to false (fail-closed
// — same contract as MinInstancesAllowed above).
func TestPlanEgressAllowlistAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.EgressAllowlistAllowed(); got != c.want {
			t.Errorf("%s.EgressAllowlistAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanLogArchiveEnabled pins the per-plan gate for the
// log archive + read-back surface (issue #562). Free → false
// (the abuse-floor tier doesn't get the S3 archive); Hobby,
// Pro, Scale → true. Unknown plans default to false
// (fail-closed, same contract as the other plan gates).
func TestPlanLogArchiveEnabled(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.LogArchiveEnabled(); got != c.want {
			t.Errorf("%s.LogArchiveEnabled() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanLogArchiveRetentionDaysMax pins the per-plan
// retention ceiling (issue #562). 0 for Free (no archive);
// 7 / 30 / 90 for Hobby / Pro / Scale (the "last week /
// this month / this quarter" customer expectations per tier).
// Unknown plans default to 0 (fail-closed).
func TestPlanLogArchiveRetentionDaysMax(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 7},
		{PlanPro, 30},
		{PlanScale, 90},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.LogArchiveRetentionDaysMax(); got != c.want {
			t.Errorf("%s.LogArchiveRetentionDaysMax() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistMaxSize pins the per-plan CIDR cap (ADR-031).
// Free/Hobby → 0 (no allowlist slot, the gate above rejects the
// PATCH before this matters); Pro → 16; Scale → 64.
func TestPlanEgressAllowlistMaxSize(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 0},
		{PlanPro, 16},
		{PlanScale, 64},
	}
	for _, c := range cases {
		if got := c.plan.EgressAllowlistMaxSize(); got != c.want {
			t.Errorf("%s.EgressAllowlistMaxSize() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistMonotonic pins the Pro→Scale ordering so a
// future bump that flips the ratio (e.g. Scale 32 < Pro 64) is caught
// here. Mirrors the TestPlansAreMonotonic style — Pro MaxSize must be
// ≤ Scale MaxSize because Scale is the bigger tier.
func TestPlanEgressAllowlistMonotonic(t *testing.T) {
	pro := MustLimitsFor(PlanPro).EgressAllowlistMaxSize
	scale := MustLimitsFor(PlanScale).EgressAllowlistMaxSize
	if scale < pro {
		t.Errorf("Scale EgressAllowlistMaxSize=%d < Pro=%d — Scale must keep the larger CIDR budget", scale, pro)
	}
}

// TestPlanStaticEgressIPAllowed pins the per-plan gate for the
// static outbound IP feature (ADR-119). Free/Hobby/Pro → false
// (the B2B allowlist use case is a Scale-only concern in v1 —
// the per-account variant is a deferred follow-up ADR); Scale
// → true. Unknown plans must default to false (fail-closed —
// same contract as EgressAllowlistAllowed above).
func TestPlanStaticEgressIPAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, false},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.StaticEgressIPAllowed(); got != c.want {
			t.Errorf("%s.StaticEgressIPAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanStaticEgressIPsPerApp pins the per-plan count cap on
// pinned static egress IPs (ADR-119). v1 ships with 1 for Scale
// (the column is a single inet, not a child table — bumping to
// N is a per-plan int change with no schema impact). 0 for
// Free/Hobby/Pro. Unknown plans must default to 0 (fail-closed).
// Mirrors TestPlanEgressAllowlistMaxSize's contract.
func TestPlanStaticEgressIPsPerApp(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 0},
		{PlanPro, 0},
		{PlanScale, 1},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.StaticEgressIPsPerApp(); got != c.want {
			t.Errorf("%s.StaticEgressIPsPerApp() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthIPAllowlistAllowed pins the per-plan gate that
// apid's updateApp handler uses for the per-app ingress IP allowlist
// (ADR-118). Same shape as TestPlanEgressAllowlistAllowed: Free/Hobby
// → false (no allowlist — abuse-desk hygiene is a Pro+ concern); Pro/Scale
// → true. Unknown plans must default to false (fail-closed).
func TestPlanPublicAuthIPAllowlistAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.PublicAuthIPAllowlistAllowed(); got != c.want {
			t.Errorf("%s.PublicAuthIPAllowlistAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthIPAllowlistMaxEntries pins the per-plan CIDR cap
// (ADR-118). Same shape as TestPlanEgressAllowlistMaxSize: Free/Hobby →
// 0; Pro → 16; Scale → 64. Unknown plans default to 0 (fail-closed).
func TestPlanPublicAuthIPAllowlistMaxEntries(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 0},
		{PlanPro, 16},
		{PlanScale, 64},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.PublicAuthIPAllowlistMaxEntries(); got != c.want {
			t.Errorf("%s.PublicAuthIPAllowlistMaxEntries() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthIPAllowlistMonotonic pins the Pro→Scale ordering
// (ADR-118). Pro MaxEntries must be ≤ Scale MaxEntries because Scale
// is the bigger tier.
func TestPlanPublicAuthIPAllowlistMonotonic(t *testing.T) {
	pro := MustLimitsFor(PlanPro).PublicAuthIPAllowlistMaxEntries
	scale := MustLimitsFor(PlanScale).PublicAuthIPAllowlistMaxEntries
	if scale < pro {
		t.Errorf("Scale PublicAuthIPAllowlistMaxEntries=%d < Pro=%d — Scale must keep the larger CIDR budget", scale, pro)
	}
}

// TestPlanCronLimits pins the cron cap per plan (spec §4.4). Free is
// 0/0 (handler returns 402 before the store is touched); Hobby gets
// 5 per-app / 10 per-account; Pro 20/50; Scale 100/500. Unknown plans
// must fail closed (return 0) so a missing row never silently unlocks
// crons — same contract as EgressAllowlistMaxSize above.
// TestPlanLogDeploymentFilterMax pins the per-plan cap on the
// `?deployment=` log-stream filter (issue #517 / PR-B, AC3). Free
// returns 0 so the handler rejects with
// `plan_deployment_filter_not_allowed`; Hobby unlocks the filter for
// the typical one-staging-deployment workload; Pro/Scale get the
// larger caps the multi-deployment fan-out needs. Unknown plans
// must fail closed (return 0) so a missing row never silently
// unlocks a paid feature — same contract as CronLimitPerApp.
func TestPlanLogDeploymentFilterMax(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 1},
		{PlanPro, 10},
		{PlanScale, 50},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.LogDeploymentFilterMax(); got != c.want {
			t.Errorf("%s.LogDeploymentFilterMax() = %d, want %d", c.plan, got, c.want)
		}
	}
}

func TestPlanCronLimits(t *testing.T) {
	cases := []struct {
		plan                    Plan
		wantPerApp, wantPerAcct int
	}{
		{PlanFree, 0, 0},
		{PlanHobby, 5, 10},
		{PlanPro, 20, 50},
		{PlanScale, 100, 500},
		{Plan("unknown"), 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.CronLimitPerApp(); got != c.wantPerApp {
			t.Errorf("%s.CronLimitPerApp() = %d, want %d", c.plan, got, c.wantPerApp)
		}
		if got := c.plan.CronLimitPerAccount(); got != c.wantPerAcct {
			t.Errorf("%s.CronLimitPerAccount() = %d, want %d", c.plan, got, c.wantPerAcct)
		}
	}
}

// TestPlanTriggerLimits pins the per-plan Trigger primitive caps
// (issue #757 / ADR-0NN). Free 0/0 (handler returns 402
// CodePlanTriggersNotAllowed before the store is touched);
// Hobby 2/10 — the entry paid tier gets queue + sqs_compat;
// Pro 10/50 — external broker kinds unlock (Kafka/NATS/Redis);
// Scale 50/200 — SaaS-scale fan-out. Mirrors the TestPlanCronLimits
// shape so the cron ↔ trigger comparison stays visible. Unknown
// plans must fail closed (return 0) — same contract as CronLimitPerApp.
func TestPlanTriggerLimits(t *testing.T) {
	cases := []struct {
		plan                    Plan
		wantPerApp, wantPerAcct int
	}{
		{PlanFree, 0, 0},
		{PlanHobby, 2, 10},
		{PlanPro, 10, 50},
		{PlanScale, 50, 200},
		{Plan("unknown"), 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.TriggerLimitPerApp(); got != c.wantPerApp {
			t.Errorf("%s.TriggerLimitPerApp() = %d, want %d", c.plan, got, c.wantPerApp)
		}
		if got := c.plan.TriggerLimitPerAccount(); got != c.wantPerAcct {
			t.Errorf("%s.TriggerLimitPerAccount() = %d, want %d", c.plan, got, c.wantPerAcct)
		}
	}
}

// TestPlanTriggerAllowed pins the per-plan feature gate for the
// Trigger primitive (issue #757 / ADR-0NN). Free = false (the
// abuse-floor tier doesn't get pull-from-broker primitives); Hobby+
// = true. apid's createTrigger handler reads this via
// Plan.TriggersAllowed() and rejects with 402
// CodePlanTriggersNotAllowed. Unknown plans must fail closed
// (return false) — same contract as EvictionPriorityReservedAllowed.
func TestPlanTriggerAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.TriggersAllowed(); got != c.want {
			t.Errorf("%s.TriggersAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanTriggerBatchAndAttemptCaps pins the per-plan ceilings on
// batch_size_max / batch_window_ms / max_attempts (issue #757 /
// ADR-0NN). Hobby 50/30s/3, Pro 500/300s/10, Scale 5000/300s/25 —
// the Scale ceiling matches the SQL CHECK [1, 5000] upper bound
// (migration 00267). Free 0/0/0 because TriggersAllowed=false there.
// Unknown plans must fail closed (return 0).
func TestPlanTriggerBatchAndAttemptCaps(t *testing.T) {
	cases := []struct {
		plan                            Plan
		wantBatch, wantWindow, wantAtts int
	}{
		{PlanFree, 0, 0, 0},
		{PlanHobby, 50, 30, 3},
		{PlanPro, 500, 300, 10},
		{PlanScale, 5000, 300, 25},
		{Plan("unknown"), 0, 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.TriggerBatchSizeMax(); got != c.wantBatch {
			t.Errorf("%s.TriggerBatchSizeMax() = %d, want %d", c.plan, got, c.wantBatch)
		}
		if got := c.plan.TriggerBatchWindowMaxSec(); got != c.wantWindow {
			t.Errorf("%s.TriggerBatchWindowMaxSec() = %d, want %d", c.plan, got, c.wantWindow)
		}
		if got := c.plan.TriggerMaxAttemptsMax(); got != c.wantAtts {
			t.Errorf("%s.TriggerMaxAttemptsMax() = %d, want %d", c.plan, got, c.wantAtts)
		}
	}
}

// TestPlanTriggerRecordsPerSecond pins the per-plan steady-state
// dispatch-rate ceiling on a single trigger (issue #757 / ADR-0NN).
// Hobby 100, Pro 1000, Scale 10000 — tracks the 10× rule the per-app
// rps tier already follows. Unknown plans must fail closed.
func TestPlanTriggerRecordsPerSecond(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 100},
		{PlanPro, 1000},
		{PlanScale, 10000},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.TriggerRecordsPerSecondPerApp(); got != c.want {
			t.Errorf("%s.TriggerRecordsPerSecondPerApp() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanTriggerAccessorsMatchTable pins that each new accessor
// reads the same value the Limits struct holds. Catches a regression
// where a future contributor edits the struct field but forgets the
// accessor (or vice versa). Mirrors TestPlanKeysMaxAccessorsMatchTable.
func TestPlanTriggerAccessorsMatchTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.TriggersAllowed(), l.TriggersAllowed; got != want {
			t.Errorf("Plan(%s).TriggersAllowed() = %v, table = %v", p, got, want)
		}
		if got, want := p.TriggerLimitPerApp(), l.TriggerLimitPerApp; got != want {
			t.Errorf("Plan(%s).TriggerLimitPerApp() = %d, table = %d", p, got, want)
		}
		if got, want := p.TriggerLimitPerAccount(), l.TriggerLimitPerAccount; got != want {
			t.Errorf("Plan(%s).TriggerLimitPerAccount() = %d, table = %d", p, got, want)
		}
		if got, want := p.TriggerBatchSizeMax(), l.TriggerBatchSizeMax; got != want {
			t.Errorf("Plan(%s).TriggerBatchSizeMax() = %d, table = %d", p, got, want)
		}
		if got, want := p.TriggerBatchWindowMaxSec(), l.TriggerBatchWindowMaxSec; got != want {
			t.Errorf("Plan(%s).TriggerBatchWindowMaxSec() = %d, table = %d", p, got, want)
		}
		if got, want := p.TriggerMaxAttemptsMax(), l.TriggerMaxAttemptsMax; got != want {
			t.Errorf("Plan(%s).TriggerMaxAttemptsMax() = %d, table = %d", p, got, want)
		}
		if got, want := p.TriggerRecordsPerSecondPerApp(), l.TriggerRecordsPerSecondPerApp; got != want {
			t.Errorf("Plan(%s).TriggerRecordsPerSecondPerApp() = %d, table = %d", p, got, want)
		}
		// ADR-118 / issue #757 closure (commit 3 of 11) — the
		// ESM-alias + broker-egress + TLS-skip-verify fields.
		// Pin the accessor↔struct match so a future contributor
		// can't drift them apart.
		if got, want := p.MaxESMSourcesPerApp(), l.MaxESMSourcesPerApp; got != want {
			t.Errorf("Plan(%s).MaxESMSourcesPerApp() = %d, table = %d", p, got, want)
		}
		if got, want := p.MaxESMRecordsPerSecond(), l.MaxESMRecordsPerSecond; got != want {
			t.Errorf("Plan(%s).MaxESMRecordsPerSecond() = %d, table = %d", p, got, want)
		}
		if got, want := p.BrokerEgressMbit(), l.BrokerEgressMbit; got != want {
			t.Errorf("Plan(%s).BrokerEgressMbit() = %d, table = %d", p, got, want)
		}
		if got, want := p.TLSSkipVerifyAllowed(), l.TLSSkipVerifyAllowed; got != want {
			t.Errorf("Plan(%s).TLSSkipVerifyAllowed() = %v, table = %v", p, got, want)
		}
	}
}

// TestPlanMaxESMSourcesPerApp pins the operator-facing alias
// for TriggerLimitPerApp (ADR-118 / issue #757 closure).
// Mirrors TriggerLimitPerApp exactly: 0 / 2 / 10 / 50.
// Unknown plans fail closed (return 0).
func TestPlanMaxESMSourcesPerApp(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 2},
		{PlanPro, 10},
		{PlanScale, 50},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.MaxESMSourcesPerApp(); got != c.want {
			t.Errorf("%s.MaxESMSourcesPerApp() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanMaxESMRecordsPerSecond pins the operator-facing alias
// for TriggerRecordsPerSecondPerApp. Mirrors exactly:
// 0 / 100 / 1000 / 10000. Unknown plans fail closed.
func TestPlanMaxESMRecordsPerSecond(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 100},
		{PlanPro, 1000},
		{PlanScale, 10000},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.MaxESMRecordsPerSecond(); got != c.want {
			t.Errorf("%s.MaxESMRecordsPerSecond() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanBrokerEgressMbit pins the per-app broker-egress cap
// (ADR-118 / commit 8 of 11). Hobby 10 / Pro 50 / Scale 200.
// 0 for Free (gated off via TriggersAllowed=false). The cap is
// enforced via the faas-brokerq.slice cgroup + tc commands.
// Unknown plans fail closed (return 0).
func TestPlanBrokerEgressMbit(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 10},
		{PlanPro, 50},
		{PlanScale, 200},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.BrokerEgressMbit(); got != c.want {
			t.Errorf("%s.BrokerEgressMbit() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanTLSSkipVerifyAllowed pins the per-plan feature gate
// for `tls.skip_verify=true` on KafkaConfig (ADR-118 / commit
// 2 of 11). Hobby=false (a Hobby customer's plaintext-TLS path
// doesn't justify the weakened-verification posture). Pro /
// Scale = true. Free = false (gated off). Unknown plans fail
// closed (return false) — same contract as TriggersAllowed().
func TestPlanTLSSkipVerifyAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.TLSSkipVerifyAllowed(); got != c.want {
			t.Errorf("%s.TLSSkipVerifyAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanKeysMax pins the per-account API-key cap for the plan
// (issue #189 / IAM-5). Free 3, Hobby 10, Pro 50, Scale 200 — see
// pkg/api/limits.go::KeysMax docstring. apid's createKey handler
// reads this value (via Plan.KeysMax()) and rejects with 409
// api_key_limit_exceeded at the cap; rotateKey is quota-neutral
// and is allowed at the cap. Unknown plans must fail closed (return 0)
// so a missing plan row never silently unlocks the auth surface — same
// contract as CronLimitPerAccount above.
func TestPlanKeysMax(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 3},
		{PlanHobby, 10},
		{PlanPro, 50},
		{PlanScale, 200},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.KeysMax(); got != c.want {
			t.Errorf("%s.KeysMax() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanKeysMaxAccessorsMatchTable pins that the accessor reads the
// same value the Limits struct holds. Catches a regression where a
// future contributor edits the struct field but forgets the accessor
// (or vice versa). Mirrors TestOrgAccessorsMatchTable.
func TestPlanKeysMaxAccessorsMatchTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.KeysMax(), l.KeysMax; got != want {
			t.Errorf("Plan(%s).KeysMax() = %d, table = %d", p, got, want)
		}
	}
}

// TestPlanEvictionPriorityReservedAllowed pins the per-plan tier gate
// for the reserved eviction tier (issue #475). Free = false (no
// reserved apps on the abuse-floor tier); Hobby+ = true. apid's
// updateApp handler reads this via Plan.EvictionPriorityReservedAllowed()
// and rejects a `reserved` PATCH on Free with 403
// plan_eviction_priority_reserved_not_allowed. Unknown plans must fail
// closed (return false) so a missing plan row never silently unlocks
// the reserved tier — same contract as WarmSnapshotAllowed above.
func TestPlanEvictionPriorityReservedAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.EvictionPriorityReservedAllowed(); got != c.want {
			t.Errorf("%s.EvictionPriorityReservedAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthBearerAllowed pins the per-plan tier gate for
// public_auth_mode='bearer' (issue #477 / ADR-079). Free = false
// (Free apps stay public-by-default — no-signup friction); Hobby+ =
// true. apid's updateApp handler reads this via
// Plan.PublicAuthBearerAllowed() and rejects a 'bearer' PATCH on Free
// with 402 plan_public_auth_bearer_not_allowed. The 'open' mode is
// always available regardless of plan — only the bearer opt-in is
// gated. Unknown plans must fail closed (return false) so a missing
// plan row never silently unlocks the bearer mode.
func TestPlanPublicAuthBearerAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.PublicAuthBearerAllowed(); got != c.want {
			t.Errorf("%s.PublicAuthBearerAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthBasicAllowed pins the per-plan tier gate for
// public_auth_mode='basic' (issue #477 / ADR-079). Free + Hobby =
// false (basic adds sealed-credential storage cost the lower tiers
// don't need; bearer covers the Hobby admin-endpoint use case);
// Pro+ = true. apid's updateApp handler reads this via
// Plan.PublicAuthBasicAllowed() and rejects a 'basic' PATCH on
// Free/Hobby with 402 plan_public_auth_basic_not_allowed. The
// 'open' mode is always available regardless of plan. Unknown
// plans must fail closed (return false) — same contract as the
// bearer test above.
func TestPlanPublicAuthBasicAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.PublicAuthBasicAllowed(); got != c.want {
			t.Errorf("%s.PublicAuthBasicAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanRequireAuthnDefault pins the per-plan CREATE-TIME default for
// apps.require_authn (issue #695 / ADR-080). Per-plan truth table:
// Free=false, Hobby=true, Pro=true, Scale=true. apid's buildApp path
// reads Plan.RequireAuthnDefault() when the POST body omitted the field
// and stamps the result onto apps.require_authn. The migration 00155
// grandfather marks every pre-flip row with auth_default_flipped_at so
// no customer sees a behaviour change at the migration moment — this
// accessor only affects post-flip CreateApp calls. Unknown plans must
// fail closed (return false) — same contract as RequireAuthnAllowed
// above; the fail-closed path lands on the schema column default.
func TestPlanRequireAuthnDefault(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.RequireAuthnDefault(); got != c.want {
			t.Errorf("%s.RequireAuthnDefault() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanTrafficSplitAllowed pins the per-plan gate for the
// traffic-splitting feature (issue #556 PR-A). Per-plan truth
// table: Free=false (locked), Hobby=false (Hobby's value-prop is
// "near-Free with a floor"; canary rollout adds RAM-billable
// live deployments the Hobby plan doesn't subsidise),
// Pro=true (the "Pro+ canary" issue body), Scale=true. apid's
// createDeployment + updateDeploymentTraffic handlers consult
// this gate so a Free/Hobby account PATCHing or supplying
// traffic_percent on create sees the canonical 403
// plan_traffic_split_not_allowed. Unknown plans must fail closed
// (return false) — same fail-closed contract as the bearer /
// basic gate tests above.
func TestPlanTrafficSplitAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.TrafficSplitAllowed(); got != c.want {
			t.Errorf("%s.TrafficSplitAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanPublicAuthModeDefault pins the per-plan CREATE-TIME default
// for apps.public_auth_mode (issue #695 / ADR-080). Closed enum:
// "open" / "bearer" / "basic". Per-plan truth table: Free="open",
// Hobby="open" (no bearer scope on Hobby), Pro="bearer", Scale="bearer".
// Hobby unlocks the require_authn gate but not the bearer scope —
// defaulting to "bearer" without an unlocked scope would strand the
// customer. apid's buildApp path reads Plan.PublicAuthModeDefault()
// when the POST body omitted the field. Unknown plans must fail closed
// (return "open") — same fail-closed contract as the bearer / basic
// gate tests above.
func TestPlanPublicAuthModeDefault(t *testing.T) {
	cases := []struct {
		plan Plan
		want string
	}{
		{PlanFree, AppPublicAuthModeOpen},
		{PlanHobby, AppPublicAuthModeOpen},
		{PlanPro, AppPublicAuthModeBearer},
		{PlanScale, AppPublicAuthModeBearer},
		{Plan("unknown"), AppPublicAuthModeOpen},
	}
	for _, c := range cases {
		if got := c.plan.PublicAuthModeDefault(); got != c.want {
			t.Errorf("%s.PublicAuthModeDefault() = %q, want %q", c.plan, got, c.want)
		}
	}
}

// TestPlanReservedConcurrencyPerAccount pins the per-account cap on
// apps with eviction_priority='reserved' (issue #475). Free 0; Hobby 1;
// Pro 2; Scale 4. apid's updateApp path enforces this under an
// apps-row FOR UPDATE lock (mirrors CreateCronIfUnderQuota). Unknown
// plans must fail closed (return 0) — same contract as
// CronLimitPerAccount above.
func TestPlanReservedConcurrencyPerAccount(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 1},
		{PlanPro, 2},
		{PlanScale, 4},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.ReservedConcurrencyPerAccount(); got != c.want {
			t.Errorf("%s.ReservedConcurrencyPerAccount() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanEvictionPriorityAccessorsMatchTable pins that the accessors
// read the same values the Limits struct holds. Catches a regression
// where a future contributor edits the struct fields but forgets the
// accessors (or vice versa). Mirrors TestKeysMaxAccessorsMatchTable.
func TestPlanEvictionPriorityAccessorsMatchTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.EvictionPriorityReservedAllowed(), l.EvictionPriorityReservedAllowed; got != want {
			t.Errorf("Plan(%s).EvictionPriorityReservedAllowed() = %v, table = %v", p, got, want)
		}
		if got, want := p.ReservedConcurrencyPerAccount(), l.ReservedConcurrencyPerAccount; got != want {
			t.Errorf("Plan(%s).ReservedConcurrencyPerAccount() = %d, table = %d", p, got, want)
		}
	}
}

// TestPlanRateLimitPerAccount pins the per-account requests/minute cap
// per plan (ADR-040 / issue #292). Free 50/min, Hobby 200/min, Pro
// 1000/min, Scale 5000/min. Unknown plans must fail closed (return 0)
// so a missing row never silently unlocks cross-app botnets — same
// contract as CronLimitPerAccount above.
func TestPlanRateLimitPerAccount(t *testing.T) {
	cases := []struct {
		plan    Plan
		wantRPM int
	}{
		{PlanFree, 50},
		{PlanHobby, 200},
		{PlanPro, 1000},
		{PlanScale, 5000},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.RateLimitPerAccountRPM(); got != c.wantRPM {
			t.Errorf("%s.RateLimitPerAccountRPM() = %d, want %d", c.plan, got, c.wantRPM)
		}
	}
}

// TestPlanStreaming pins the per-plan streaming flags (issue #471 /
// ADR-047 PR-A). Free is gated out (CodePlanStreamingNotAllowed in
// apid's validateUpdateApp); Hobby/Pro/Scale unlock streaming. MaxResponse
// bytes cap is the 100 MiB / 25 MiB pin; ResponseWriteTimeout is the
// 900 s / 300 s pin. Unknown plans must fail closed on all three
// flags so a missing plan never silently unlocks streaming.
func TestPlanStreaming(t *testing.T) {
	enabledCases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range enabledCases {
		if got := c.plan.StreamingEnabled(); got != c.want {
			t.Errorf("%s.StreamingEnabled() = %v, want %v", c.plan, got, c.want)
		}
	}

	allowedCases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range allowedCases {
		if got := c.plan.StreamingResponseAllowed(); got != c.want {
			t.Errorf("%s.StreamingResponseAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}

	bodyCases := []struct {
		plan Plan
		want int64
	}{
		{PlanFree, 26_214_400},
		{PlanHobby, 104_857_600},
		{PlanPro, 104_857_600},
		{PlanScale, 104_857_600},
		// Unknown plans fail closed via the MaxResponseBodyBytesDefault
		// fallback (25 MiB) — the spec §4.1 pre-#471 buffer ceiling
		// is the conservative binding cap. Returns the default, not 0,
		// to guarantee a runaway stream never leaves the cap.
		{Plan("unknown"), MaxResponseBodyBytesDefault},
	}
	for _, c := range bodyCases {
		if got := c.plan.MaxResponseBodyBytes(); got != c.want {
			t.Errorf("%s.MaxResponseBodyBytes() = %d, want %d", c.plan, got, c.want)
		}
	}

	rwCases := []struct {
		plan Plan
		want time.Duration
	}{
		{PlanFree, 300 * time.Second},
		{PlanHobby, 900 * time.Second},
		{PlanPro, 900 * time.Second},
		{PlanScale, 900 * time.Second},
		// Unknown plans fall back to ResponseWriteTimeoutDefault
		// (300 s) — same conservative-fallback shape as the body
		// cap above. The listener ceiling always ends up bound by
		// the spec §4.1 default, never "no timeout".
		{Plan("unknown"), time.Duration(ResponseWriteTimeoutDefault) * time.Second},
	}
	for _, c := range rwCases {
		if got := c.plan.ResponseWriteTimeout(); got != c.want {
			t.Errorf("%s.ResponseWriteTimeout() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanTail pins the per-plan matrix for the waitUntil
// post-response tail primitive (issue #667 / ADR-078). Every plan
// unlocks the primitive (TailEnabled = true), with the per-plan
// TailTimeoutS / TailCapMax / ConcurrentTailsPerInstance values
// pinned verbatim from the issue's "Rules" section:
//
//	Free   5s  / 16 cap / 4 concurrent
//	Hobby 15s  / 16 cap / 16 concurrent
//	Pro   30s  / 16 cap / 64 concurrent
//	Scale 60s  / 16 cap / 256 concurrent
//
// The structural TailCapMax = 16 is a single source of truth — the
// accessor returns the constant regardless of the field value, so
// the cap is enforced even if a future plan row accidentally drops
// it. TailTimeoutSeconds clamps up to TailTimeoutFloorSeconds (5 s)
// for any plan whose row is unset / below the floor; this guarantees
// the reaper's park-watchdog can never be shorter than the per-plan
// timeout. Unknown plans fail closed on the boolean + integer
// accessors (return false / 0) but fall back to the floor on
// TailTimeoutSeconds.
func TestPlanTail(t *testing.T) {
	enabledCases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, true},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		// Unknown plans fail closed (return false) — same contract
		// as StreamingEnabled / WarmSnapshotEnabled / RequireAuthn.
		{Plan("unknown"), false},
	}
	for _, c := range enabledCases {
		if got := c.plan.TailEnabled(); got != c.want {
			t.Errorf("%s.TailEnabled() = %v, want %v", c.plan, got, c.want)
		}
		if got := c.plan.TailAllowed(); got != c.want {
			t.Errorf("%s.TailAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}

	timeoutCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 5},
		{PlanHobby, 15},
		{PlanPro, 30},
		{PlanScale, 60},
		// Unknown plans fall back to the floor — the
		// ParkTailDrainTimeoutSeconds (5 s) watchdog must always
		// be able to drain a tail mid-task.
		{Plan("unknown"), TailTimeoutFloorSeconds},
	}
	for _, c := range timeoutCases {
		if got := c.plan.TailTimeoutSeconds(); got != c.want {
			t.Errorf("%s.TailTimeoutSeconds() = %d, want %d", c.plan, got, c.want)
		}
	}

	// TailCapMax is structural — the accessor returns the constant
	// regardless of the plan row's field. Pin every plan to 16
	// (the issue's single source of truth).
	capMaxCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, TailCapMax},
		{PlanHobby, TailCapMax},
		{PlanPro, TailCapMax},
		{PlanScale, TailCapMax},
		{Plan("unknown"), TailCapMax},
	}
	for _, c := range capMaxCases {
		if got := c.plan.TailCapMax(); got != c.want {
			t.Errorf("%s.TailCapMax() = %d, want %d", c.plan, got, c.want)
		}
	}

	concurrentCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 4},
		{PlanHobby, 16},
		{PlanPro, 64},
		{PlanScale, 256},
		// Unknown plans fail closed (return 0) — same contract
		// as the boolean accessors above.
		{Plan("unknown"), 0},
	}
	for _, c := range concurrentCases {
		if got := c.plan.ConcurrentTailsPerInstance(); got != c.want {
			t.Errorf("%s.ConcurrentTailsPerInstance() = %d, want %d", c.plan, got, c.want)
		}
	}

	// Pin the structural constants themselves so a future refactor
	// cannot silently move them.
	if TailCapMax != 16 {
		t.Errorf("TailCapMax = %d, want 16 (issue #667 single source of truth)", TailCapMax)
	}
	if TailTimeoutFloorSeconds != 5 {
		t.Errorf("TailTimeoutFloorSeconds = %d, want 5 (matches ParkTailDrainTimeoutSeconds)", TailTimeoutFloorSeconds)
	}
	if ParkTailDrainTimeoutSeconds != TailTimeoutFloorSeconds {
		t.Errorf("ParkTailDrainTimeoutSeconds = %d, must equal TailTimeoutFloorSeconds (%d) so the watchdog is never shorter than the shortest per-plan timeout",
			ParkTailDrainTimeoutSeconds, TailTimeoutFloorSeconds)
	}
}

// TestPlanTailTimeoutClamp pins the clamp-up behaviour on
// TailTimeoutSeconds (issue #667 / ADR-078 §"Why the host ships
// entropy" parallel): a buggy planLimits entry that drops below
// the floor is clamped up by Plan.TailTimeoutSeconds() so the
// reaper's 5 s park-watchdog always has at least a chance to drain
// the tail before force-park. The accessor is the only entry point
// used by schedd / apid / runner, so the clamp is the load-bearing
// invariant — a regression here would let a runaway tail hold a
// wake open past the watchdog ceiling.
func TestPlanTailTimeoutClamp(t *testing.T) {
	// Confirm the floor is non-zero (otherwise the clamp is a no-op
	// and the watchdog contract breaks).
	if TailTimeoutFloorSeconds <= 0 {
		t.Fatalf("TailTimeoutFloorSeconds = %d, must be > 0 so the watchdog always has drain headroom", TailTimeoutFloorSeconds)
	}

	// All four known plans must return >= the floor (the per-plan
	// values 5/15/30/60 are all strictly >= the 5 s floor, but the
	// clamp guards against future regressions).
	for _, p := range Plans {
		if got := p.TailTimeoutSeconds(); got < TailTimeoutFloorSeconds {
			t.Errorf("%s.TailTimeoutSeconds() = %d, must be >= TailTimeoutFloorSeconds (%d)",
				p, got, TailTimeoutFloorSeconds)
		}
	}
}

// TestOCIPullTimeoutSeconds pins the per-pull HTTP timeout (ADR-021) —
// pkg/oci.RegistryClient consults this when no WithTimeout override is
// passed. The number is a platform constant: every plan shares the same
// ceiling so the cold-boot latency contract stays predictable. 60s is
// well above the largest manifest + image-config GET and a generous
// safety margin over the fail-fast PullImageConfig path.
func TestOCIPullTimeoutSeconds(t *testing.T) {
	if OCIPullTimeoutSeconds != 60 {
		t.Errorf("OCIPullTimeoutSeconds = %d, want 60", OCIPullTimeoutSeconds)
	}
	if OCIPullTimeoutSeconds < 10 {
		t.Errorf("OCIPullTimeoutSeconds = %d must be >= 10s so a slow registry cannot starve the cold-boot latency budget", OCIPullTimeoutSeconds)
	}
}

// TestPlanRequireAuthnAllowed pins the per-plan gate that apid's
// updateApp handler uses for the per-app require_authn field
// (issue #560). Free/Hobby → false (Cloud Run's
// `--no-allow-unauthenticated` is a paid-tier feature); Pro/Scale →
// true. Unknown plans must default to false (fail-closed: a missing
// plan never silently unlocks a premium feature). Mirrors
// TestPlanScaleUpTargetRPSAllowed shape — same boolean accessor,
// same plan row count.
func TestPlanRequireAuthnAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.RequireAuthnAllowed(); got != c.want {
			t.Errorf("%s.RequireAuthnAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestRequireAuthnAccessorMatchesTable pins that the accessor reads
// the same value the Limits struct holds. Mirrors
// TestOrgAccessorsMatchTable above — catches regressions where a
// future contributor edits the struct field but forgets the
// accessor (or vice versa).
func TestRequireAuthnAccessorMatchesTable(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := p.RequireAuthnAllowed(), l.RequireAuthn; got != want {
			t.Errorf("Plan(%s).RequireAuthnAllowed() = %v, table = %v", p, got, want)
		}
	}
}

// TestPlanLiveness pins the per-plan liveness probe defaults
// (issue #554 / ADR-078). Free stays off (the §13 M7 free-stop
// path already handles abuse-floor work; LivenessAllowed() mirrors
// the MinInstancesAllowed/RequireAuthnAllowed false-on-Free
// contract). Hobby first unlocks the primitive at 5 s period / 3
// consecutive / 60 s cooldown / 3 in 300 s — every paid tier
// inherits the same defaults. Unknown plans fail closed (every
// accessor returns 0/false) — same contract as
// TestPlanRequireAuthnAllowed above.
func TestPlanLiveness(t *testing.T) {
	allowedCases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, true},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range allowedCases {
		if got := c.plan.LivenessAllowed(); got != c.want {
			t.Errorf("%s.LivenessAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}

	periodCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 5},
		{PlanPro, 5},
		{PlanScale, 5},
		{Plan("unknown"), 0},
	}
	for _, c := range periodCases {
		if got := c.plan.LivenessPeriodSeconds(); got != c.want {
			t.Errorf("%s.LivenessPeriodSeconds() = %d, want %d", c.plan, got, c.want)
		}
	}

	consCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 3},
		{PlanPro, 3},
		{PlanScale, 3},
		{Plan("unknown"), 0},
	}
	for _, c := range consCases {
		if got := c.plan.LivenessConsecutiveFailures(); got != c.want {
			t.Errorf("%s.LivenessConsecutiveFailures() = %d, want %d", c.plan, got, c.want)
		}
	}

	cooldownCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 60},
		{PlanPro, 60},
		{PlanScale, 60},
		{Plan("unknown"), 0},
	}
	for _, c := range cooldownCases {
		if got := c.plan.LivenessCooldownSeconds(); got != c.want {
			t.Errorf("%s.LivenessCooldownSeconds() = %d, want %d", c.plan, got, c.want)
		}
	}

	maxCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 3},
		{PlanPro, 3},
		{PlanScale, 3},
		{Plan("unknown"), 0},
	}
	for _, c := range maxCases {
		if got := c.plan.LivenessMaxRestarts(); got != c.want {
			t.Errorf("%s.LivenessMaxRestarts() = %d, want %d", c.plan, got, c.want)
		}
	}

	windowCases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 300},
		{PlanPro, 300},
		{PlanScale, 300},
		{Plan("unknown"), 0},
	}
	for _, c := range windowCases {
		if got := c.plan.LivenessWindowSeconds(); got != c.want {
			t.Errorf("%s.LivenessWindowSeconds() = %d, want %d", c.plan, got, c.want)
		}
	}

	// GRPCLivenessAllowed is hard-wired to false across the board
	// in v1 (issue #554 / ADR-078 §"gRPC liveness"); the accessor
	// exists so v2 can flip it without a DTO/SDK change.
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale, Plan("unknown")} {
		if p.GRPCLivenessAllowed() {
			t.Errorf("%s.GRPCLivenessAllowed() = true, want false (v1 is HTTP-only; v2 PR will flip this without a DTO change)", p)
		}
	}
}

// TestLivenessConfigConstants pins the §13 mirror constants —
// every consumer (cmd/vmmd poll goroutine, pkg/sched liveness
// window, Dashboard help text) reads these. The clamp floors and
// ceilings are the second-line defence: a customer that PATCHes
// 0s period or 9999 consecutive in `OverrideLivenessProbe` is
// rejected by Limits.X validation in the apid handler (TODO:
// follow-up; v1 still surfaces the constants to the OpenAPI
// schema's min/max).
func TestLivenessConfigConstants(t *testing.T) {
	if DefaultLivenessPeriodSeconds != 5 {
		t.Errorf("DefaultLivenessPeriodSeconds = %d, want 5", DefaultLivenessPeriodSeconds)
	}
	if DefaultLivenessConsecutiveFailures != 3 {
		t.Errorf("DefaultLivenessConsecutiveFailures = %d, want 3", DefaultLivenessConsecutiveFailures)
	}
	if DefaultLivenessCooldownSeconds != 60 {
		t.Errorf("DefaultLivenessCooldownSeconds = %d, want 60", DefaultLivenessCooldownSeconds)
	}
	if DefaultLivenessMaxRestarts != 3 {
		t.Errorf("DefaultLivenessMaxRestarts = %d, want 3", DefaultLivenessMaxRestarts)
	}
	if DefaultLivenessWindowSeconds != 300 {
		t.Errorf("DefaultLivenessWindowSeconds = %d, want 300", DefaultLivenessWindowSeconds)
	}
	if MinLivenessPeriodSeconds != 1 {
		t.Errorf("MinLivenessPeriodSeconds = %d, want 1", MinLivenessPeriodSeconds)
	}
	if MaxLivenessPeriodSeconds != 60 {
		t.Errorf("MaxLivenessPeriodSeconds = %d, want 60", MaxLivenessPeriodSeconds)
	}
}

// TestPlanLivenessAccessorsMatchTable pins that the accessors read
// the same value the Limits struct holds (mirrors
// TestOrgAccessorsMatchTable above). Catches a regression where a
// future contributor edits the struct field but forgets the accessor
// (or vice versa).
func TestPlanLivenessAccessorsMatchTable(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		l := MustLimitsFor(p)
		if got, want := p.LivenessPeriodSeconds(), l.LivenessPeriodSeconds; got != want {
			t.Errorf("Plan(%s).LivenessPeriodSeconds() = %d, table = %d", p, got, want)
		}
		if got, want := p.LivenessConsecutiveFailures(), l.LivenessConsecutiveFailures; got != want {
			t.Errorf("Plan(%s).LivenessConsecutiveFailures() = %d, table = %d", p, got, want)
		}
		if got, want := p.LivenessCooldownSeconds(), l.LivenessCooldownSeconds; got != want {
			t.Errorf("Plan(%s).LivenessCooldownSeconds() = %d, table = %d", p, got, want)
		}
		if got, want := p.LivenessMaxRestarts(), l.LivenessMaxRestarts; got != want {
			t.Errorf("Plan(%s).LivenessMaxRestarts() = %d, table = %d", p, got, want)
		}
		if got, want := p.LivenessWindowSeconds(), l.LivenessWindowSeconds; got != want {
			t.Errorf("Plan(%s).LivenessWindowSeconds() = %d, table = %d", p, got, want)
		}
	}
}

// TestCharacterizationDeadlines pins the ADR-051 observation window.
// The guest (characterize_linux.go::waitForBind) and the host
// (pkg/fcvm/manager.go::characterizationWait) both read from this
// single source so a future bump moves both sides together. The
// invariants: guest >= host (the guest's full observation window
// must cover the host's dial+read window or the host gives up
// first and reports a false timeout), both > 0 (a zero deadline
// would make waitForBind return instantly without observing the
// bind), and both < readyTimeout (the legacy vmmd waitReady
// default of 30s — characterization is the faster gate).
func TestCharacterizationDeadlines(t *testing.T) {
	if CharacterizationDeadline <= 0 {
		t.Errorf("CharacterizationDeadline = %s, want > 0", CharacterizationDeadline)
	}
	if CharacterizationHostDeadline <= 0 {
		t.Errorf("CharacterizationHostDeadline = %s, want > 0", CharacterizationHostDeadline)
	}
	if CharacterizationDeadline < CharacterizationHostDeadline {
		t.Errorf("guest deadline %s < host deadline %s (host would time out before guest has a chance to ship)",
			CharacterizationDeadline, CharacterizationHostDeadline)
	}
	const readyTimeout = 30 * time.Second
	if CharacterizationDeadline >= readyTimeout {
		t.Errorf("CharacterizationDeadline = %s must be < readyTimeout (%s) so characterization gates boot faster than the legacy :8080 accept path",
			CharacterizationDeadline, readyTimeout)
	}
}

// TestLogRingBufferBytes pins the ADR-051 Slice A PR-B ring buffer
// capacity. The characterize probe reads this buffer's Tail() into
// the report's LogTail field (after truncateLog's wire-side clamp
// at VsockCharacterizationMaxBody = 32 KiB). Three invariants:
//
//   - non-zero: a zero-sized buffer would silently drop every boot
//     log byte, regressing the LogTail field back to the pre-PR-B
//     empty-string sentinel.
//   - >= 32 KiB: the buffer must be at least as large as the
//     wire-body cap so a customer's boot log that fills the buffer
//     can still surface the wire's full 32 KiB without truncation
//     inside the buffer itself.
//   - <= 1 MiB: a single-megabyte ring buffer per guest is the
//     largest reasonable allocation; anything larger would silently
//     bloat the per-guest memory budget (every Supervisor carries
//     one of these, even on boxes where the characterize probe is
//     disabled).
func TestLogRingBufferBytes(t *testing.T) {
	if LogRingBufferBytes <= 0 {
		t.Fatalf("LogRingBufferBytes = %d, want > 0", LogRingBufferBytes)
	}
	const wireBodyCap = 32 * 1024 // VsockCharacterizationMaxBody
	if LogRingBufferBytes < wireBodyCap {
		t.Errorf("LogRingBufferBytes = %d, want >= %d (VsockCharacterizationMaxBody) so the buffer holds the full wire body without internal truncation",
			LogRingBufferBytes, wireBodyCap)
	}
	const saneUpperBound = 1024 * 1024 // 1 MiB
	if LogRingBufferBytes > saneUpperBound {
		t.Errorf("LogRingBufferBytes = %d, want <= %d (1 MiB sanity upper bound; per-guest ring buffer must not silently bloat memory)",
			LogRingBufferBytes, saneUpperBound)
	}
}

// TestPlanWebSocketEnabled pins the per-plan gate for the raw-bytes
// Upgrade bridge (issue #676 / ADR-080). Free stays off (abuse-floor
// tier — a long-lived WS would pin a wake past wake_idle_timeout);
// Hobby/Pro/Scale opt in. The fail-closed contract means an unknown
// plan reads as false (the same shape as MinInstancesAllowed /
// EgressAllowlistAllowed).
//
// Both WebSocketEnabled (the create-time default) and
// WebSocketResponseAllowed (the PATCH-time gate) read the same
// per-plan Limits.WebSocketEnabled bit — same shape as the
// StreamingEnabled pair in 00080_apps_streaming_enabled / limits.go
// (the create-time default and the PATCH-time gate both read
// l.StreamingEnabled so a Free app cannot opt in even when an admin
// backfills the column). Mirrors TestPlanSidecarAllowed above.
func TestPlanWebSocketEnabled(t *testing.T) {
	for _, tc := range []struct {
		plan   Plan
		want   bool
		reason string
	}{
		{PlanFree, false, "Free is the abuse-floor tier; long-lived WS would pin a wake past wake_idle_timeout"},
		{PlanHobby, true, "Hobby is the first paid tier; LLM/agent SDKs speak WS over HTTP"},
		{PlanPro, true, "Pro is the first tier where production workloads sit"},
		{PlanScale, true, "Scale is the tier where production WS-backed services run"},
	} {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := tc.plan.WebSocketEnabled(); got != tc.want {
				t.Errorf("%s.WebSocketEnabled() = %v, want %v (%s)",
					tc.plan, got, tc.want, tc.reason)
			}
			if got := tc.plan.WebSocketResponseAllowed(); got != tc.want {
				t.Errorf("%s.WebSocketResponseAllowed() = %v, want %v (PATCH gate mirrors the default)",
					tc.plan, got, tc.want)
			}
		})
	}
}

// TestPlanWebSocketEnabled_UnknownFailsClosed pins the fail-closed
// contract for an unknown plan (same shape as the per-plan gate
// accessors above). A typo in apid's buildApp (e.g. Plan("freee"))
// must not silently enable the raw-bytes bridge; the worst case is
// the customer accidentally gets an open Upgrade on a plan that
// doesn't allow it, which would let a long-lived WS run unmetered.
func TestPlanWebSocketEnabled_UnknownFailsClosed(t *testing.T) {
	const unknown = Plan("nonexistent")
	if got := unknown.WebSocketEnabled(); got {
		t.Errorf("Plan(nonexistent).WebSocketEnabled() = true, want false (fail-closed)")
	}
	if got := unknown.WebSocketResponseAllowed(); got {
		t.Errorf("Plan(nonexistent).WebSocketResponseAllowed() = true, want false (fail-closed)")
	}
}

// TestPlanPerAppMetricsAllowed pins the per-plan gate that apid's
// per-app observability handlers consult
// (cmd/apid/handlers_metrics.go +
// cmd/apid/handlers_wake_timeline.go). The handler returns 402
// ErrPlanPerAppMetricsNotAllowed when this returns false. Free must
// fail closed (the surface is paid-only); Hobby/Pro/Scale must
// return true. Mirrors the gating posture of
// TestPlanMinInstancesAllowed above and TestPlanStreaming.
func TestPlanPerAppMetricsAllowed(t *testing.T) {
	for _, tc := range []struct {
		plan   Plan
		want   bool
		reason string
	}{
		{PlanFree, false, "Free is the abuse-floor tier; the per-app dashboard is paid-only"},
		{PlanHobby, true, "Hobby is the first paid tier; 'see what you pay for' is the upsell"},
		{PlanPro, true, "Pro is the first tier where production observability matters"},
		{PlanScale, true, "Scale is the tier where enterprise observability matters"},
	} {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := tc.plan.PerAppMetricsAllowed(); got != tc.want {
				t.Errorf("%s.PerAppMetricsAllowed() = %v, want %v (%s)",
					tc.plan, got, tc.want, tc.reason)
			}
		})
	}
}

// TestPlanAppUsageSummaryAllowed pins the per-plan gate that
// apid's billing-usage handler consults
// (cmd/apid/handlers_usage.go). The handler returns 402
// ErrPlanAppUsageSummaryNotAllowed when this returns false. Free
// must fail closed (billing-transparency is paid-only); Hobby/Pro/
// Scale must return true.
func TestPlanAppUsageSummaryAllowed(t *testing.T) {
	for _, tc := range []struct {
		plan   Plan
		want   bool
		reason string
	}{
		{PlanFree, false, "Free is the abuse-floor tier; billing-usage read is paid-only"},
		{PlanHobby, true, "Hobby is the first paid tier; billing transparency is part of the contract"},
		{PlanPro, true, "Pro is the first tier with production-grade billing visibility"},
		{PlanScale, true, "Scale is the tier where enterprise billing visibility matters"},
	} {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := tc.plan.AppUsageSummaryAllowed(); got != tc.want {
				t.Errorf("%s.AppUsageSummaryAllowed() = %v, want %v (%s)",
					tc.plan, got, tc.want, tc.reason)
			}
		})
	}
}

// TestPlanAppErrorsAllowed pins the per-plan gate that apid's
// error-fingerprint handler consults
// (cmd/apid/handlers_app_errors.go). The handler returns 402
// ErrPlanAppErrorsNotAllowed when this returns false. Free must
// fail closed (grouped errors are paid-only); Hobby/Pro/Scale must
// return true.
func TestPlanAppErrorsAllowed(t *testing.T) {
	for _, tc := range []struct {
		plan   Plan
		want   bool
		reason string
	}{
		{PlanFree, false, "Free is the abuse-floor tier; error grouping is paid-only"},
		{PlanHobby, true, "Hobby is the first paid tier; 'see what failed' is the upsell"},
		{PlanPro, true, "Pro is the first tier with production-grade error visibility"},
		{PlanScale, true, "Scale is the tier where enterprise error visibility matters"},
	} {
		t.Run(string(tc.plan), func(t *testing.T) {
			if got := tc.plan.AppErrorsAllowed(); got != tc.want {
				t.Errorf("%s.AppErrorsAllowed() = %v, want %v (%s)",
					tc.plan, got, tc.want, tc.reason)
			}
		})
	}
}

// TestPlanPerAppObservability_UnknownFailsClosed pins the
// fail-closed contract for the three per-app observability
// accessors above. A typo in apid's buildApp (e.g. Plan("freee"))
// must not silently enable the per-app dashboard on a plan that
// doesn't allow it — the worst case is a Free customer reading
// Hobby+ telemetry unmetered. Same shape as
// TestPlanWebSocketEnabled_UnknownFailsClosed.
func TestPlanPerAppObservability_UnknownFailsClosed(t *testing.T) {
	const unknown = Plan("nonexistent")
	if got := unknown.PerAppMetricsAllowed(); got {
		t.Errorf("Plan(nonexistent).PerAppMetricsAllowed() = true, want false (fail-closed)")
	}
	if got := unknown.AppUsageSummaryAllowed(); got {
		t.Errorf("Plan(nonexistent).AppUsageSummaryAllowed() = true, want false (fail-closed)")
	}
	if got := unknown.AppErrorsAllowed(); got {
		t.Errorf("Plan(nonexistent).AppErrorsAllowed() = true, want false (fail-closed)")
	}
}

// TestHAConstantsStable pins the high-availability timing /
// retry constants introduced by Tier A8 (ADR-083) and Tier A9
// (ADR-084). These are NOT plan-level limits (no per-plan
// derivation); they are the cluster-wide operating parameters
// the writeGate and standby warmup use. Review finding #8 of
// PR #761: rename any of these and the test fails before the
// call-site drift in cmd/gatewayd-internal/proxy.go reaches
// runtime.
//
// The expected values are the design decisions captured in the
// ADRs / noble-swimming-balloon.md plan. A change requires the
// matching ADR update in the same PR.
func TestHAConstantsStable(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		// Tier A8 (ADR-083) — active-passive HA topology.
		{"HAFailoverProbeTimeoutMS", HAFailoverProbeTimeoutMS, 500},
		{"HADNSRecordStaleSeconds", HADNSRecordStaleSeconds, 30},
		{"HAStandbyWarmupIntervalMS", HAStandbyWarmupIntervalMS, 500},

		// Tier A9 (ADR-084) — standby write-redirect.
		{"StandbyWriteRedirectTimeoutMS", StandbyWriteRedirectTimeoutMS, 5000},
		{"StandbyWriteRetryAfterSeconds", StandbyWriteRetryAfterSeconds, 5},
		{"StandbyWriteLeaderURLCacheTTLSeconds", StandbyWriteLeaderURLCacheTTLSeconds, 5},
		{"StandbyWriteNoLeaderRetryAfterSeconds", StandbyWriteNoLeaderRetryAfterSeconds, 60},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (ADR-083/084 design value)",
				c.name, c.got, c.want)
		}
	}
}

// TestEdgeRulesGeoPerApp_PerPlanMatrix pins the per-plan matrix for the
// new EdgeRulesGeoPerApp quota (ADR-091 D22). Free customers get 1
// geo rule (the "block everything except DE" abuse-block use case),
// Hobby=5, Pro=25, Scale=100. This is the FAIL-CLOSED shape that the
// apid handler dispatches on ErrPlanEdgeRuleKindQuotaReached.
//
// Distinct from EdgeRulesPerApp (the general per-app cap), which is
// 5/25/100/500 — geo is a high-touch abuse primitive so the per-kind
// cap is intentionally tighter than the general cap on Free.
func TestEdgeRulesGeoPerApp_PerPlanMatrix(t *testing.T) {
	want := map[Plan]int{
		PlanFree:  1,
		PlanHobby: 5,
		PlanPro:   25,
		PlanScale: 100,
	}
	for plan, expected := range want {
		l, ok := LimitsFor(plan)
		if !ok {
			t.Fatalf("LimitsFor(%s) returned !ok", plan)
		}
		if l.EdgeRulesGeoPerApp != expected {
			t.Errorf("%s: EdgeRulesGeoPerApp = %d, want %d", plan, l.EdgeRulesGeoPerApp, expected)
		}
	}
	// Sanity: at every plan tier, EdgeRulesGeoPerApp ≤ EdgeRulesPerApp
	// (the per-kind cap must never exceed the general cap). A
	// regression that swaps the two values breaks the fail-closed
	// quota gate — Free customers could create more geo rules than
	// total rules, which is impossible to enforce.
	for _, plan := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		l, _ := LimitsFor(plan)
		if l.EdgeRulesGeoPerApp > l.EdgeRulesPerApp {
			t.Errorf("%s: per-kind cap %d > general cap %d (per-kind must ≤ general)",
				plan, l.EdgeRulesGeoPerApp, l.EdgeRulesPerApp)
		}
	}
}

// TestEdgeRulesGeoPerApp_MonotonicLadder pins that the per-plan
// geo-cap ladder is non-decreasing. A regression where Pro < Hobby
// or Scale < Pro breaks the upgrade story (a Pro customer
// downgrading-from-Scale would gain geo capacity, which is an
// invariant the billing model depends on).
func TestEdgeRulesGeoPerApp_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.EdgeRulesGeoPerApp < prevL.EdgeRulesGeoPerApp {
			t.Errorf("%s.EdgeRulesGeoPerApp (%d) < %s.EdgeRulesGeoPerApp (%d)",
				ladder[i], currL.EdgeRulesGeoPerApp, ladder[i-1], prevL.EdgeRulesGeoPerApp)
		}
	}
}

// TestEdgeRulesThrottlePerApp_PerPlanMatrix pins the per-plan matrix
// for EdgeRulesThrottlePerApp (ADR-091 D20.5 amendment, issue #881).
// Free 1, Hobby 5, Pro 25, Scale 100 — mirrors EdgeRulesGeoPerApp
// so the upgrade curve is predictable.
func TestEdgeRulesThrottlePerApp_PerPlanMatrix(t *testing.T) {
	want := map[Plan]int{
		PlanFree:  1,
		PlanHobby: 5,
		PlanPro:   25,
		PlanScale: 100,
	}
	for plan, expected := range want {
		l, ok := LimitsFor(plan)
		if !ok {
			t.Errorf("plan %s: missing from LimitsFor", plan)
			continue
		}
		if l.EdgeRulesThrottlePerApp != expected {
			t.Errorf("%s: EdgeRulesThrottlePerApp = %d, want %d", plan, l.EdgeRulesThrottlePerApp, expected)
		}
		// Sanity: throttle cap is bounded by total EdgeRulesPerApp.
		if l.EdgeRulesThrottlePerApp > l.EdgeRulesPerApp {
			t.Errorf("%s: EdgeRulesThrottlePerApp (%d) > EdgeRulesPerApp (%d); per-kind cap cannot exceed the per-app total",
				plan, l.EdgeRulesThrottlePerApp, l.EdgeRulesPerApp)
		}
	}
}

// TestEdgeRulesThrottlePerApp_MonotonicLadder pins that the per-plan
// throttle-cap ladder is non-decreasing. Same load-bearing reasoning
// as the geo ladder — the upgrade story and the billing model both
// depend on Free ≤ Hobby ≤ Pro ≤ Scale.
func TestEdgeRulesThrottlePerApp_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.EdgeRulesThrottlePerApp < prevL.EdgeRulesThrottlePerApp {
			t.Errorf("%s.EdgeRulesThrottlePerApp (%d) < %s.EdgeRulesThrottlePerApp (%d)",
				ladder[i], currL.EdgeRulesThrottlePerApp, ladder[i-1], prevL.EdgeRulesThrottlePerApp)
		}
	}
}

// TestEdgeRulesCachePerApp_PerPlanMatrix pins the per-plan matrix
// for EdgeRulesCachePerApp (ADR-122 §Decision). Free 0, Hobby 1,
// Pro 5, Scale 20. Free=0 mirrors the abuse-floor stance used by
// tenant_surfaces / alert_rules / cors_presets on Free: the
// wake-elision guarantee is the upsell, not a baseline amenity.
//
// Sanity: cache cap is bounded by total EdgeRulesPerApp. A
// regression that pushed cache above the per-app total would let
// a customer pin the gateway's route cardinality to a single
// kind, breaking the per-app shape.
func TestEdgeRulesCachePerApp_PerPlanMatrix(t *testing.T) {
	want := map[Plan]int{
		PlanFree:  0,
		PlanHobby: 1,
		PlanPro:   5,
		PlanScale: 20,
	}
	for plan, expected := range want {
		l, ok := LimitsFor(plan)
		if !ok {
			t.Errorf("plan %s: missing from LimitsFor", plan)
			continue
		}
		if l.EdgeRulesCachePerApp != expected {
			t.Errorf("%s: EdgeRulesCachePerApp = %d, want %d", plan, l.EdgeRulesCachePerApp, expected)
		}
		if l.EdgeRulesCachePerApp > l.EdgeRulesPerApp {
			t.Errorf("%s: EdgeRulesCachePerApp (%d) > EdgeRulesPerApp (%d); per-kind cap cannot exceed the per-app total",
				plan, l.EdgeRulesCachePerApp, l.EdgeRulesPerApp)
		}
	}
}

// TestEdgeRulesCachePerApp_MonotonicLadder pins that the per-plan
// cache-cap ladder is non-decreasing. Same load-bearing reasoning
// as the throttle / geo ladders — the upgrade story and the
// billing model both depend on Free ≤ Hobby ≤ Pro ≤ Scale.
func TestEdgeRulesCachePerApp_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.EdgeRulesCachePerApp < prevL.EdgeRulesCachePerApp {
			t.Errorf("%s.EdgeRulesCachePerApp (%d) < %s.EdgeRulesCachePerApp (%d)",
				ladder[i], currL.EdgeRulesCachePerApp, ladder[i-1], prevL.EdgeRulesCachePerApp)
		}
	}
}

// TestEdgeRulesCachePerApp_FreeZeroIsClosed pins that Free=0 is
// not a transient gap — the per-kind quota branch in
// pkg/state/pgstore.go and memstore.go skips the check when the
// limit is 0, so a Free customer can otherwise create unlimited
// cache rules if the field ever defaults away. This test makes
// the "Free cannot cache" promise grep-able in the limits table
// rather than inferred from the gate's off-by-zero behaviour.
func TestEdgeRulesCachePerApp_FreeZeroIsClosed(t *testing.T) {
	l, ok := LimitsFor(PlanFree)
	if !ok {
		t.Fatal("PlanFree missing from LimitsFor")
	}
	if l.EdgeRulesCachePerApp != 0 {
		t.Errorf("PlanFree.EdgeRulesCachePerApp = %d, want 0 (Free customers stay on cold wake every time — the wake-elision guarantee is the upsell, not a baseline amenity)", l.EdgeRulesCachePerApp)
	}
}

// TestDataPlacementHintsPerApp_PerPlanMatrix pins the per-plan
// matrix for the ADR-098 §D5 data-placement cap. A regression that
// swaps the per-plan values breaks the customer quota dashboard
// ("you have N/M data upstreams").
func TestDataPlacementHintsPerApp_PerPlanMatrix(t *testing.T) {
	want := map[Plan]int{
		PlanFree:  0,
		PlanHobby: 3,
		PlanPro:   10,
		PlanScale: 50,
	}
	for plan, expected := range want {
		l, ok := LimitsFor(plan)
		if !ok {
			t.Fatalf("LimitsFor(%s) returned !ok", plan)
		}
		if l.DataPlacementHintsPerApp != expected {
			t.Errorf("%s: DataPlacementHintsPerApp = %d, want %d",
				plan, l.DataPlacementHintsPerApp, expected)
		}
	}
	// Sanity: the global UpstreamProbeMaxConcurrent and
	// UpstreamFitMinDeltaMs constants live on the api package (not
	// per-plan). A regression that moves them onto the Limits
	// struct would break the meterd boot-time read path
	// (cmd/meterd/main.go) which dereferences them as api.X, not
	// planLimits[plan].X.
	if UpstreamProbeMaxConcurrent <= 0 {
		t.Errorf("UpstreamProbeMaxConcurrent must be positive, got %d", UpstreamProbeMaxConcurrent)
	}
	if UpstreamFitMinDeltaMs <= 0 {
		t.Errorf("UpstreamFitMinDeltaMs must be positive, got %d", UpstreamFitMinDeltaMs)
	}
}

// TestDataPlacementHintsPerApp_MonotonicLadder pins that the
// per-plan data-placement-cap ladder is non-decreasing. Mirrors the
// EdgeRulesGeoPerApp ladder — a regression where Pro < Hobby or
// Scale < Pro breaks the upgrade story.
func TestDataPlacementHintsPerApp_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.DataPlacementHintsPerApp < prevL.DataPlacementHintsPerApp {
			t.Errorf("%s.DataPlacementHintsPerApp (%d) < %s.DataPlacementHintsPerApp (%d)",
				ladder[i], currL.DataPlacementHintsPerApp,
				ladder[i-1], prevL.DataPlacementHintsPerApp)
		}
	}
}

// TestPlanCorsPresetLimits pins the per-plan CORS preset cap ladder
// (issue #975 item #4 / Mega-Foundation #979-b, slot 00294). The
// progression matches the documented per-plan posture:
//
//	Free  = 0/0/0/0/64      (abuse-floor — abstraction is the upsell)
//	Hobby = 10/5/25/8/64    (entry paid)
//	Pro   = 50/15/100/8/64  (typical SaaS)
//	Scale = 250/50/500/8/64 (large fleet)
//
// Field order is per-account, per-app, max-origins, max-methods,
// max-name-length. Unknown plans fail closed (return 0 for caps,
// 0 for name length) so a missing plan row never silently unlocks
// the abstraction. apid-Validate's CreateCorsPreset handler (PR-B,
// slot 00295) trips the per-plan caps at insert time.
func TestPlanCorsPresetLimits(t *testing.T) {
	cases := []struct {
		plan           Plan
		wantPerAcct    int
		wantPerApp     int
		wantMaxOrigins int
		wantMaxMethods int
		wantMaxName    int
	}{
		{PlanFree, 0, 0, 0, 0, 64},
		{PlanHobby, 10, 5, 25, 8, 64},
		{PlanPro, 50, 15, 100, 8, 64},
		{PlanScale, 250, 50, 500, 8, 64},
		{Plan("unknown"), 0, 0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.CorsPresetsPerAccount(); got != c.wantPerAcct {
			t.Errorf("%s.CorsPresetsPerAccount() = %d, want %d", c.plan, got, c.wantPerAcct)
		}
		if got := c.plan.CorsPresetsPerApp(); got != c.wantPerApp {
			t.Errorf("%s.CorsPresetsPerApp() = %d, want %d", c.plan, got, c.wantPerApp)
		}
		if got := c.plan.CorsPresetMaxOrigins(); got != c.wantMaxOrigins {
			t.Errorf("%s.CorsPresetMaxOrigins() = %d, want %d", c.plan, got, c.wantMaxOrigins)
		}
		if got := c.plan.CorsPresetMaxAllowMethods(); got != c.wantMaxMethods {
			t.Errorf("%s.CorsPresetMaxAllowMethods() = %d, want %d", c.plan, got, c.wantMaxMethods)
		}
		if got := c.plan.CorsPresetMaxNameLength(); got != c.wantMaxName {
			t.Errorf("%s.CorsPresetMaxNameLength() = %d, want %d", c.plan, got, c.wantMaxName)
		}
	}
}

// TestPlanCorsPresetLimits_MonotonicLadder pins that the per-plan
// CORS preset cap ladder is non-decreasing across the upgrade
// curve. A regression where Pro < Hobby or Scale < Pro breaks the
// upgrade story; a customer who outgrows Hobby should land on a
// higher Pro number, not a lower one.
func TestPlanCorsPresetLimits_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.CorsPresetsPerAccount < prevL.CorsPresetsPerAccount {
			t.Errorf("%s.CorsPresetsPerAccount (%d) < %s.CorsPresetsPerAccount (%d)",
				ladder[i], currL.CorsPresetsPerAccount,
				ladder[i-1], prevL.CorsPresetsPerAccount)
		}
		if currL.CorsPresetsPerApp < prevL.CorsPresetsPerApp {
			t.Errorf("%s.CorsPresetsPerApp (%d) < %s.CorsPresetsPerApp (%d)",
				ladder[i], currL.CorsPresetsPerApp,
				ladder[i-1], prevL.CorsPresetsPerApp)
		}
		if currL.CorsPresetMaxOrigins < prevL.CorsPresetMaxOrigins {
			t.Errorf("%s.CorsPresetMaxOrigins (%d) < %s.CorsPresetMaxOrigins (%d)",
				ladder[i], currL.CorsPresetMaxOrigins,
				ladder[i-1], prevL.CorsPresetMaxOrigins)
		}
	}
}

// TestPlanConsumerKeysLimits pins the per-plan consumer key cap
// ladder (ADR-120 / issue #975 item #5). The progression matches
// the documented per-plan posture:
//
//	Free  = 100/app 250/acct    (abuse-floor ceiling — every
//	                            plan gets the per-app floor;
//	                            the per-account cap is the
//	                            abuse-desk ceiling)
//	Hobby = 100/app 250/acct    (entry paid — same numbers)
//	Pro   = 100/app 2500/acct   (typical SaaS — per-account
//	                            step-up, 25× Hobby)
//	Scale = 1000/app 25000/acct (large fleet — per-app step-up,
//	                            10× Pro on the per-app floor,
//	                            10× Pro on the per-account cap)
//
// Field order is per-app, per-account. Unknown plans fail closed
// (return 0 for both caps) so a missing plan row never silently
// unlocks the primitive. apid-Validate's CreateConsumerKey
// handler (PR #5-B) trips the per-plan caps at insert time.
func TestPlanConsumerKeysLimits(t *testing.T) {
	cases := []struct {
		plan        Plan
		wantPerApp  int
		wantPerAcct int
	}{
		{PlanFree, 0, 0},
		{PlanHobby, 100, 250},
		{PlanPro, 100, 2500},
		{PlanScale, 1000, 25000},
		{Plan("unknown"), 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.ConsumerKeysPerApp(); got != c.wantPerApp {
			t.Errorf("%s.ConsumerKeysPerApp() = %d, want %d", c.plan, got, c.wantPerApp)
		}
		if got := c.plan.ConsumerKeysPerAccount(); got != c.wantPerAcct {
			t.Errorf("%s.ConsumerKeysPerAccount() = %d, want %d", c.plan, got, c.wantPerAcct)
		}
	}
}

// TestPlanConsumerKeysLimits_MonotonicLadder pins that the per-plan
// consumer key cap ladder is non-decreasing across the upgrade
// curve. A regression where Pro < Hobby or Scale < Pro breaks the
// upgrade story; a customer who outgrows Hobby should land on a
// higher Pro number, not a lower one.
func TestPlanConsumerKeysLimits_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.ConsumerKeysPerApp < prevL.ConsumerKeysPerApp {
			t.Errorf("%s.ConsumerKeysPerApp (%d) < %s.ConsumerKeysPerApp (%d)",
				ladder[i], currL.ConsumerKeysPerApp,
				ladder[i-1], prevL.ConsumerKeysPerApp)
		}
		if currL.ConsumerKeysPerAccount < prevL.ConsumerKeysPerAccount {
			t.Errorf("%s.ConsumerKeysPerAccount (%d) < %s.ConsumerKeysPerAccount (%d)",
				ladder[i], currL.ConsumerKeysPerAccount,
				ladder[i-1], prevL.ConsumerKeysPerAccount)
		}
	}
}

// TestPlanOpenAPIDocLimits pins the per-plan OpenAPI doc cap
// ladder (ADR-122 / issue #975 item #1, migrations/00330). The
// progression matches the documented per-plan posture:
//
//	Free  = 0/dep 0/acct 0 bytes (gated off — apid returns 402
//	                            CodePlanOpenAPIDocsNotAllowed)
//	Hobby = 1/dep 100/acct 131072 bytes (entry paid — 128 KiB is
//	                                     the global cap)
//	Pro   = 1/dep 1000/acct 131072 bytes (10× Hobby on per-account)
//	Scale = 1/dep 10000/acct 131072 bytes (10× Pro on per-account)
//
// The per-deployment cap is 1 across all paid plans because the
// schema's PRIMARY KEY is deployment_id. The per-account cap
// scales 10× across the upgrade ladder — the openapi docs are
// larger + rarer than consumer keys (which scale 25×), so the
// 10× number keeps the dollar/GB-h economics balanced. Unknown
// plans fail closed (return 0 on all three caps) so a missing
// plan row never silently unlocks the primitive.
func TestPlanOpenAPIDocLimits(t *testing.T) {
	cases := []struct {
		plan        Plan
		wantPerDep  int
		wantPerAcct int
		wantBytes   int
	}{
		{PlanFree, 0, 0, 0},
		{PlanHobby, 1, 100, 131072},
		{PlanPro, 1, 1000, 131072},
		{PlanScale, 1, 10000, 131072},
		{Plan("unknown"), 0, 0, 0},
	}
	for _, c := range cases {
		if got := c.plan.OpenAPIDocsPerDeployment(); got != c.wantPerDep {
			t.Errorf("%s.OpenAPIDocsPerDeployment() = %d, want %d", c.plan, got, c.wantPerDep)
		}
		if got := c.plan.OpenAPIDocsPerAccount(); got != c.wantPerAcct {
			t.Errorf("%s.OpenAPIDocsPerAccount() = %d, want %d", c.plan, got, c.wantPerAcct)
		}
		if got := c.plan.OpenAPIDocMaxBytes(); got != c.wantBytes {
			t.Errorf("%s.OpenAPIDocMaxBytes() = %d, want %d", c.plan, got, c.wantBytes)
		}
	}
}

// TestPlanOpenAPIDocLimits_MonotonicLadder pins that the per-plan
// OpenAPI doc cap ladder is non-decreasing across the upgrade
// curve. A regression where Pro < Hobby or Scale < Pro breaks the
// upgrade story; a customer who outgrows Hobby should land on a
// higher Pro number, not a lower one.
func TestPlanOpenAPIDocLimits_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.OpenAPIDocsPerDeployment < prevL.OpenAPIDocsPerDeployment {
			t.Errorf("%s.OpenAPIDocsPerDeployment (%d) < %s.OpenAPIDocsPerDeployment (%d)",
				ladder[i], currL.OpenAPIDocsPerDeployment,
				ladder[i-1], prevL.OpenAPIDocsPerDeployment)
		}
		if currL.OpenAPIDocsPerAccount < prevL.OpenAPIDocsPerAccount {
			t.Errorf("%s.OpenAPIDocsPerAccount (%d) < %s.OpenAPIDocsPerAccount (%d)",
				ladder[i], currL.OpenAPIDocsPerAccount,
				ladder[i-1], prevL.OpenAPIDocsPerAccount)
		}
		// OpenAPIDocMaxBytes is the same across paid plans — the
		// global cap is the binding constraint. The Free=0 vs
		// paid=131072 step-up is the only monotonic check needed.
		if currL.OpenAPIDocMaxBytes < prevL.OpenAPIDocMaxBytes {
			t.Errorf("%s.OpenAPIDocMaxBytes (%d) < %s.OpenAPIDocMaxBytes (%d)",
				ladder[i], currL.OpenAPIDocMaxBytes,
				ladder[i-1], prevL.OpenAPIDocMaxBytes)
		}
	}
}

// TestPlanOpenAPIImportsPerAccount pins the per-plan
// OpenAPIImportsPerAccount ladder (ADR-126 / issue #975
// item #2). Per-plan: Free 100, Hobby 1000, Pro 10000, Scale
// 10000. The apid POST /v1/apps/{slug}/openapi handler
// enforces via Store.CountOpenAPIImportsByAccount; 403 when
// the cap is reached. The Free 100 ladder step is the
// "every plan can import (limits are abuse-surface, not
// tier)" decision — Free is non-zero so even the cheapest
// tier can import.
func TestPlanOpenAPIImportsPerAccount(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 100},
		{PlanHobby, 1000},
		{PlanPro, 10000},
		{PlanScale, 10000},
		{Plan("unknown"), 0},
	}
	for _, c := range cases {
		if got := c.plan.OpenAPIImportsPerAccount(); got != c.want {
			t.Errorf("%s.OpenAPIImportsPerAccount() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanOpenAPIImportsPerAccount_MonotonicLadder pins that
// the per-plan OpenAPIImportsPerAccount ladder is non-decreasing
// across the upgrade curve (mirrors
// TestPlanOpenAPIDocLimits_MonotonicLadder for the import
// surface).
func TestPlanOpenAPIImportsPerAccount_MonotonicLadder(t *testing.T) {
	ladder := []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}
	for i := 1; i < len(ladder); i++ {
		prevL, _ := LimitsFor(ladder[i-1])
		currL, _ := LimitsFor(ladder[i])
		if currL.OpenAPIImportsPerAccount < prevL.OpenAPIImportsPerAccount {
			t.Errorf("%s.OpenAPIImportsPerAccount (%d) < %s.OpenAPIImportsPerAccount (%d)",
				ladder[i], currL.OpenAPIImportsPerAccount,
				ladder[i-1], prevL.OpenAPIImportsPerAccount)
		}
	}
}

// TestPlanOpenAPIDocConstants verifies the cross-package constant
// state.OpenAPIDocMaxBytes matches the per-plan Hobby/Pro/Scale
// value. A drift between the constant and the per-plan value
// would mean the guest-init probe truncates at one cap and the
// apid PATCH validates against another — the customer sees a
// doc captured at a size the apid refuses to serve.
func TestPlanOpenAPIDocConstants(t *testing.T) {
	hobbyCap := PlanHobby.OpenAPIDocMaxBytes()
	proCap := PlanPro.OpenAPIDocMaxBytes()
	scaleCap := PlanScale.OpenAPIDocMaxBytes()
	if got, want := hobbyCap, 131072; got != want {
		t.Errorf("Hobby.OpenAPIDocMaxBytes = %d, want %d", got, want)
	}
	if got, want := proCap, 131072; got != want {
		t.Errorf("Pro.OpenAPIDocMaxBytes = %d, want %d", got, want)
	}
	if got, want := scaleCap, 131072; got != want {
		t.Errorf("Scale.OpenAPIDocMaxBytes = %d, want %d", got, want)
	}
}

// TestFullRootfsAllowedPlans_PlanMembership (M-3 commit 9 / ADR-141
// §Decision 2). PlanFree must be absent; Hobby/Pro/Scale must be
// present. Closed-set posture mirrors PlanMeetsMinimumPlan.
func TestFullRootfsAllowedPlans_PlanMembership(t *testing.T) {
	for _, p := range FullRootfsAllowedPlans {
		if p == PlanFree {
			t.Errorf("FullRootfsAllowedPlans must NOT include PlanFree (no auto-dispatch on Free)")
		}
	}
	if !PlanMeetsFullRootfs(PlanHobby) {
		t.Errorf("PlanMeetsFullRootfs(PlanHobby) = false; want true")
	}
	if !PlanMeetsFullRootfs(PlanPro) {
		t.Errorf("PlanMeetsFullRootfs(PlanPro) = false; want true")
	}
	if !PlanMeetsFullRootfs(PlanScale) {
		t.Errorf("PlanMeetsFullRootfs(PlanScale) = false; want true")
	}
	if PlanMeetsFullRootfs(PlanFree) {
		t.Errorf("PlanMeetsFullRootfs(PlanFree) = true; want false (Free has no auto-dispatch)")
	}
	if PlanMeetsFullRootfs(Plan("unknown")) {
		t.Errorf("PlanMeetsFullRootfs(unknown) = true; want false (closed-set)")
	}
}

// TestUserUIDOverrideMax_PerPlan (M-3 commit 9 / ADR-142 §Decision 4).
// Hobby 16 / Pro 64 / Scale 256. Free is 0 (Free cannot auto-dispatch).
// Monotonic ladder: Hobby ≤ Pro ≤ Scale.
func TestUserUIDOverrideMax_PerPlan(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 16},
		{PlanPro, 64},
		{PlanScale, 256},
	}
	for _, tc := range cases {
		if got := UserUIDOverrideMax[tc.plan]; got != tc.want {
			t.Errorf("UserUIDOverrideMax[%s] = %d; want %d", tc.plan, got, tc.want)
		}
	}
	// Monotonic ladder.
	if !(UserUIDOverrideMax[PlanHobby] <= UserUIDOverrideMax[PlanPro] &&
		UserUIDOverrideMax[PlanPro] <= UserUIDOverrideMax[PlanScale]) {
		t.Errorf("UserUIDOverrideMax is not monotonic Hobby ≤ Pro ≤ Scale")
	}
}

// TestMaxFullRootfsLayerBytes_PerPlan (M-3 commit 9 / ADR-141
// §Decision 5). Hobby 256 MB / Pro 1 GB / Scale 4 GB. Closed set:
// unknown plan → zero value.
func TestMaxFullRootfsLayerBytes_PerPlan(t *testing.T) {
	cases := []struct {
		plan Plan
		want int64
	}{
		{PlanHobby, 256 << 20},
		{PlanPro, 1 << 30},
		{PlanScale, 4 << 30},
	}
	for _, tc := range cases {
		if got := MaxFullRootfsLayerBytes[tc.plan]; got != tc.want {
			t.Errorf("MaxFullRootfsLayerBytes[%s] = %d; want %d", tc.plan, got, tc.want)
		}
	}
	// Closed-set: unknown plan returns the zero value (no panic).
	_ = MaxFullRootfsLayerBytes[Plan("unknown")]
}

// TestFullRootfsAllowAutoDefault_PerPlan (M-3 commit 9 / ADR-141
// §Decision 2). Free → false (no auto-dispatch). Paid plans → true.
func TestFullRootfsAllowAutoDefault_PerPlan(t *testing.T) {
	if FullRootfsAllowAutoDefault[PlanFree] {
		t.Errorf("FullRootfsAllowAutoDefault[PlanFree] = true; want false")
	}
	for _, p := range []Plan{PlanHobby, PlanPro, PlanScale} {
		if !FullRootfsAllowAutoDefault[p] {
			t.Errorf("FullRootfsAllowAutoDefault[%s] = false; want true", p)
		}
	}
}
