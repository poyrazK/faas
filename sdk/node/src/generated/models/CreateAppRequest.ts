/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceReplicas } from './ServiceReplicas.js';
/**
 * App creation payload: slug, type (app|function), runtime (only for function), RAM MB, max concurrency, idle timeout, and optional manifest.
 */
export type CreateAppRequest = {
  slug: string;
  type?: 'app' | 'function';
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  ram_mb?: number;
  /**
   * Sustained CPU allowance per instance. Omit for 1000 millicores.
   */
  cpu_millicores?: 250 | 500 | 1000;
  max_concurrency?: number;
  idle_timeout_s?: number;
  /**
   * Lifecycle contract for the app. Default is request; service/worker/job are plan-gated.
   */
  execution_mode?: 'request' | 'service' | 'worker' | 'job';
  /**
   * Restart behavior for the workload. Omitted uses the execution-mode default.
   */
  restart_policy?: 'no' | 'on-failure' | 'always' | 'unless-stopped';
  /**
   * Upper bound on time-to-ready in seconds. 0 uses the plan default.
   */
  startup_deadline_s?: number;
  /**
   * Maximum consecutive restart attempts. 0 uses the plan default.
   */
  max_retries?: number;
  service_replicas?: ServiceReplicas;
  /**
   * Per-app streaming flag. Omitted at create-time → apid applies the plan default (issue #471).
   */
  streaming_enabled?: boolean;
  /**
   * Per-app raw-bytes Upgrade bridge flag (issue #676 / ADR-080). Omitted → apid applies the plan default; PATCH-true on Free is rejected by apid with 403 plan_websocket_not_allowed.
   */
  websocket_enabled?: boolean;
  /**
   * Per-app per-route observability flag (ADR-093). Omitted → apid applies the plan default (Free = false; Hobby/Pro/Scale = true). PATCH-true on Free is rejected by apid with 403 plan_route_metrics_not_allowed.
   */
  route_metrics_enabled?: boolean;
  /**
   * Coarse per-app maintenance toggle (ADR-091 amendment). Omitted → apid applies the default (false). Free-tier allowed; no plan gate. Flipping this on at create time pins the app for maintenance from the first request.
   */
  maintenance_mode?: boolean;
  /**
   * Per-app wire-protocol selector (ADR-124). Closed set {http1, http2, grpc}. Omit to use the per-plan default ('http1'); set explicitly to opt in to http2 or grpc. Free customers POSTing 'grpc' are rejected with 403 plan_app_protocol_grpc_not_allowed.
   */
  app_protocol?: 'http1' | 'http2' | 'grpc';
  /**
   * Per-app two-tier snapshot flag (issue #470 / ADR-055). Omitted at create-time → apid applies the plan default. Free/Hobby PATCH-true is rejected.
   */
  warm_snapshot_enabled?: boolean;
  /**
   * Optional create-time override for the warm-tier request-count threshold (issue #470 / ADR-055). Range [1, 100]. Omitted → apid applies the plan default.
   */
  warm_snapshot_min_requests?: number;
  /**
   * Optional create-time override for the warm-tier time-since-first-ready threshold, milliseconds (issue #470 / ADR-055). Range [100, 60000]. Omitted → apid applies the plan default.
   */
  warm_snapshot_min_ms?: number;
  /**
   * Per-app eviction tier (issue #475). 'best_effort' (default) keeps the pre-#475 LRU-by-last_request_at reaper behaviour; 'reserved' protects the app from cross-account RAM-pressure eviction. Omitted at create-time → apid applies the schema default 'best_effort'.
   */
  eviction_priority?: 'best_effort' | 'reserved';
  /**
   * Per-app preferred spill target for cross-node pressure rebalance (Tier A10 / ADR-088). Wire form is compute_nodes.name (resolved server-side). Omitted → no preference; empty string at create-time is rejected with 422 invalid_overflow_node because the column starts NULL and there is no 'clear' path at create-time.
   */
  overflow_node?: string;
  /**
   * Per-deployment token-gate flag (issue #560). Omitted at create-time → apid applies the plan default (false). Pro/Scale only.
   */
  require_authn?: boolean;
};

