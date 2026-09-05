/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppConfiguredResources } from './AppConfiguredResources.js';
import type { AppEffectiveLimits } from './AppEffectiveLimits.js';
import type { AppManifest } from './AppManifest.js';
import type { ParkedDeploymentRef } from './ParkedDeploymentRef.js';
import type { PublicAuthStatus } from './PublicAuthStatus.js';
import type { ScalingPolicy } from './ScalingPolicy.js';
/**
 * An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169 / #172).
 */
export type AppResponse = {
  id: string;
  slug: string;
  type: 'app' | 'function';
  /**
   * Runtime for `type: function` apps. Omit for `type: app` (the default).
   */
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  ram_mb: number;
  cpu_millicores: 250 | 500 | 1000;
  configured_resources: AppConfiguredResources;
  max_concurrency: number;
  concurrency_per_vm: number;
  effective_limits: AppEffectiveLimits;
  idle_timeout_s?: number | null;
  min_instances: number;
  status: string;
  url: string;
  manifest: AppManifest;
  /**
   * Per-app outbound CIDR allowlist (ADR-031 + ADR-032). Each entry is a CIDR string — v4 (`1.2.3.0/24`) or v6 (`2001:db8::/32`). v4-mapped v6 form (`::ffff:1.2.3.0/120`) is silently canonicalised to its v4 form at write time. Empty array means no allowlist rule; the per-netns chain's default-accept policy applies.
   */
  egress_allowlist?: Array<string>;
  /**
   * Per-instance RPS target for the reactive scale-up trigger. 0 = disabled. Hobby/Pro/Scale only. When measured per-instance RPS exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037.
   */
  autoscale_target_rps: number;
  /**
   * Per-instance CPU% target (1..100) for the reactive scale-up trigger. 0 = disabled. Pro/Scale only. When measured per-instance CPU% exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037.
   */
  autoscale_target_cpu_pct: number;
  /**
   * Per-app streaming flag (issue #471). Free customers always see this as false; Hobby/Pro/Scale can PATCH it. PR-B activates the streamed response path; PR-A only persists the flag.
   */
  streaming_enabled?: boolean;
  /**
   * Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Default-on for Hobby/Pro/Scale; Free customers always see this as false. PATCH-true on Free is rejected by apid with 403 plan_websocket_not_allowed.
   */
  websocket_enabled?: boolean;
  /**
   * Per-app per-route observability flag (ADR-093). When true, gatewayd-internal emits gateway_request_duration_seconds{app,route,class} and serves the bounded reader at GET /v1/apps/{slug}/routes. Default-on for Hobby/Pro/Scale; Free customers always see this as false. PATCH-true on Free is rejected by apid with 403 plan_route_metrics_not_allowed.
   */
  route_metrics_enabled?: boolean;
  /**
   * Coarse per-app maintenance toggle (ADR-091 amendment). When true the gatewayd-internal hot-path short-circuits every request to this app with 503 + Retry-After (default 60 s) BEFORE auth, BEFORE wake, BEFORE any kind=maintenance edge rule. Free-tier allowed. Surfaced in the GET /v1/apps/{slug} response so dashboards can show 'maintenance on / off' alongside the streaming/WS pills.
   */
  maintenance_mode?: boolean;
  /**
   * Per-app scaling policy (issue #462 / ADR-058). null = legacy row, project the empty-policy shape from min_instances / max_concurrency. Non-null = customer-authored policy persisted to the jsonb column `apps.scaling_policy`.
   */
  scaling_policy?: (null | ScalingPolicy);
  /**
   * RFC 3339 timestamp of the most recent scale-out event schedd admitted for this app, or null if the app has never scaled out.
   */
  last_scale_out_at?: string | null;
  /**
   * RFC 3339 timestamp of the most recent scale-in event schedd reaped for this app, or null if the app has never scaled in.
   */
  last_scale_in_at?: string | null;
  /**
   * Per-app cosign signature-enforcement flag (issue #472 / ADR-054). When true, OCI image deploys must carry a valid signature from a publisher in the per-app trusted_signers list. Default false.
   */
  require_signed?: boolean;
  /**
   * Per-app two-tier snapshot flag (issue #470 / ADR-055). True on Pro/Scale by default; Free/Hobby always false.
   */
  warm_snapshot_enabled?: boolean;
  /**
   * Effective per-app request-count threshold for warm-tier capture on this app (issue #470 / ADR-055). Range [1, 100].
   */
  warm_snapshot_min_requests?: number;
  /**
   * Per-app time-since-first-ready threshold for warm-tier capture, milliseconds (issue #470 / ADR-055). Range [100, 60000].
   */
  warm_snapshot_min_ms?: number;
  /**
   * Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction.
   */
  eviction_priority?: 'best_effort' | 'reserved';
  /**
   * Per-deployment token-gate flag (issue #560). When true, gatewayd-internal demands `Authorization: Bearer <token>` on every request; cross-account tokens receive 403 insufficient_scope. Pro/Scale only — Free/Hobby PATCH-true is rejected with 403 plan_require_authn_not_allowed.
   */
  require_authn?: boolean;
  /**
   * Most-recently parked deployment for this app, or null if never parked (issue #554 / ADR-079 follow-up). The reference surfaces the closed-set parking reason + timestamp on GET /v1/apps/{slug} so operators can answer 'why is my app evicted_cold?' without grepping the audit log.
   */
  parked_deployment?: (ParkedDeploymentRef | null);
  /**
   * Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Resolved UUID from the customer's named compute_nodes.name preference (null when unset). Consulted by Engine.RebalancePressuredApps before the A9 fallback; falls through to A9 when the target is inactive or full.
   */
  overflow_node?: string | null;
  cors_default_enabled?: boolean | null;
  cors_default_origins?: Array<string>;
  public_auth?: PublicAuthStatus;
  auth_default_flipped_at?: string | null;
  /**
   * Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Default 'http1' (universal). Setting 'grpc' is plan-gated to Hobby+/Pro/Scale; Free customers see this as 'http1'.
   */
  app_protocol?: 'http1' | 'http2' | 'grpc';
};

