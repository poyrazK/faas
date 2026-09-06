/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublicAuthBlock } from './PublicAuthBlock.js';
import type { ResourceProfile } from './ResourceProfile.js';
import type { ScalingPolicy } from './ScalingPolicy.js';
import type { ServiceReplicas } from './ServiceReplicas.js';
/**
 * Partial update — every field is optional; omitted fields are unchanged.
 */
export type UpdateAppRequest = {
  ram_mb?: number | null;
  /**
   * Sustained CPU allowance per instance. Omit for no change.
   */
  cpu_millicores?: 250 | 500 | 1000;
  /**
   * Named memory/CPU profile. Omit for no change; explicit ram_mb or cpu_millicores must agree with the selected profile.
   */
  resource_profile?: (ResourceProfile | null);
  idle_timeout_s?: number | null;
  max_concurrency?: number | null;
  /**
   * Lifecycle contract for the app. Omit for no change; service/worker/job are plan-gated.
   */
  execution_mode?: 'request' | 'service' | 'worker' | 'job';
  /**
   * Restart behavior for the workload. Omit for no change.
   */
  restart_policy?: 'no' | 'on-failure' | 'always' | 'unless-stopped';
  /**
   * Upper bound on time-to-ready in seconds. Omit for no change; 0 uses the plan default.
   */
  startup_deadline_s?: number | null;
  /**
   * Maximum consecutive restart attempts. Omit for no change; 0 uses the plan default.
   */
  max_retries?: number | null;
  /**
   * Full replacement of the service replica policy. Omit for no change.
   */
  service_replicas?: ServiceReplicas;
  min_instances?: number | null;
  /**
   * v4 or v6 CIDR allowlist; empty array clears to chain-default-accept.
   */
  egress_allowlist?: Array<string>;
  /**
   * Per-instance RPS target for the reactive scale-up trigger. 0 = disable. Hobby/Pro/Scale only. Values < 0 are 422 invalid_autoscale_target_rps.
   */
  autoscale_target_rps?: number | null;
  /**
   * Per-instance CPU% target (1..100, 0 = disable) for the reactive scale-up trigger. Pro/Scale only. Values outside [1, 100] (other than 0) are 422 invalid_autoscale_target_cpu_pct.
   */
  autoscale_target_cpu_pct?: number | null;
  /**
   * Per-app streaming flag (issue #471). Omitted → no change. Free PATCHing true is 403 plan_streaming_not_allowed.
   */
  streaming_enabled?: boolean | null;
  /**
   * Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Omitted → no change. Free PATCHing true is 403 plan_websocket_not_allowed.
   */
  websocket_enabled?: boolean | null;
  /**
   * Per-app per-route observability flag (ADR-093). Omitted → no change. Free PATCHing true is 403 plan_route_metrics_not_allowed.
   */
  route_metrics_enabled?: boolean | null;
  /**
   * Coarse per-app maintenance toggle (ADR-091 amendment). Omitted → no change. PATCH true pins the app for maintenance (every request 503 + Retry-After); PATCH false restores normal handling. Free-tier allowed; no plan gate. The apps_maintenance_mode_notify trigger (migration 00237) fires pg_notify on flip.
   */
  maintenance_mode?: boolean | null;
  /**
   * Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Omit for no change; set explicitly to opt in (http2/grpc) or reset to 'http1'. Free customers PATCHing 'grpc' are rejected with 403 plan_app_protocol_grpc_not_allowed.
   */
  app_protocol?: 'http1' | 'http2' | 'grpc';
  /**
   * Per-app scaling policy. Omitted → no change. Non-null → atomic full-overwrite of the jsonb column.
   */
  scaling_policy?: (null | ScalingPolicy);
  /**
   * DEPRECATED on this surface. The customer PATCH /v1/apps/{slug} endpoint silently drops require_signed; the operator endpoint PATCH /v1/apps/{slug}/security is the only path that flips the flag (issue #472 / ADR-054). The field is parsed for backwards compatibility but never persisted from this endpoint.
   */
  require_signed?: boolean | null;
  /**
   * Per-app two-tier snapshot flag (issue #470 / ADR-055). Omitted → no change. PATCH-true on Free/Hobby is rejected with 403 plan_warm_snapshot_not_allowed.
   */
  warm_snapshot_enabled?: boolean | null;
  /**
   * Per-app request-count threshold for warm-tier capture (issue #470 / ADR-055). Range [1, 100]. Omitted → no change.
   */
  warm_snapshot_min_requests?: number | null;
  /**
   * Per-app time-since-first-ready threshold for warm-tier capture, milliseconds (issue #470 / ADR-055). Range [100, 60000]. Omitted → no change.
   */
  warm_snapshot_min_ms?: number | null;
  /**
   * Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction (every best_effort candidate is drained before any reserved is parked). Plan-gated upstream: Free PATCH 'reserved' returns 402 plan_eviction_priority_reserved_not_allowed. Per-account cap (Hobby 1, Pro 2, Scale 4): 422 plan_eviction_priority_reserved_quota when exhausted. Omitted → no change.
   */
  eviction_priority?: 'best_effort' | 'reserved';
  /**
   * Per-deployment token-gate flag (issue #560). Omitted → no change. PATCH-true on Free/Hobby is rejected with 403 plan_require_authn_not_allowed.
   */
  require_authn?: boolean | null;
  /**
   * Per-app public-URL auth configuration (issue #477 / ADR-077). Omitted → no change. When present, mode is the closed enum {open, bearer, basic}; basic_user + basic_pass are required when mode='basic' and the apid seal step encrypts them under the APP_BASIC_AUTH secretbox namespace before persistence.
   */
  public_auth?: (null | PublicAuthBlock);
  /**
   * Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Wire form is the human-readable compute_nodes.name; apid resolves to UUID server-side. Tri-state: omitted → no change; empty string → clear (back to A9 fallback); non-empty → resolve name → UUID via Store.ComputeNodeByName and persist the UUID. 404 on unknown name; 422 on inactive node.
   */
  overflow_node?: string | null;
  cors_default_enabled?: boolean | null;
  cors_default_origins?: Array<string>;
};

