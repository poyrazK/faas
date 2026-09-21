/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ResourceProfile } from './ResourceProfile.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
import type { ServiceReplicas } from './ServiceReplicas.js';
import type { WorkerScaling } from './WorkerScaling.js';
import type { WorkloadPort } from './WorkloadPort.js';
/**
 * App creation payload: slug, type (app|function), runtime (only for function), RAM MB, max concurrency, idle timeout, and optional manifest.
 */
export type CreateAppRequest = {
  slug: string;
  type?: 'app' | 'function';
  /**
   * Ingress exposure for the new app. Choose internal to make it service-only; that option is available on Pro and Scale.
   */
  visibility?: 'public' | 'internal';
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  ram_mb?: number;
  /**
   * Optional guest vCPU assertion. When supplied with ram_mb, the pair must match the plan shape: Free (128 MB/2), Hobby (256 MB/2), Pro (512 MB/2), or Scale (1024 MB/4). Omit to use the plan default.
   */
  vcpu?: number;
  /**
   * Sustained CPU allowance per instance. Omit for 1000 millicores.
   */
  cpu_millicores?: 250 | 500 | 1000;
  /**
   * Named memory/CPU profile. When set, ram_mb and cpu_millicores are filled from the profile; explicit values must agree with it.
   */
  resource_profile?: ResourceProfile;
  max_concurrency?: number;
  idle_timeout_s?: number;
  /**
   * Per-app request wall-clock timeout in seconds. 0 inherits the plan/type default; the current platform ceiling is 30 seconds.
   */
  request_timeout_s?: number;
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
   * Upper bound on worker or service shutdown draining time in seconds before SIGKILL. 0 uses the mode/plan default.
   */
  stop_grace_period_s?: number;
  /**
   * Signal sent to initiate graceful stop (e.g. SIGTERM, SIGINT, SIGQUIT, SIGHUP, SIGUSR1, SIGUSR2). Omitted defaults to SIGTERM.
   */
  stop_signal?: string;
  /**
   * Maximum consecutive restart attempts. 0 uses the plan default.
   */
  max_retries?: number;
  /**
   * Initial app-level invocation retry default; queue binding and per-invocation policies may override it.
   */
  retry_policy?: RetryPolicyDTO;
  service_replicas?: ServiceReplicas;
  worker_replicas?: WorkerScaling;
  /**
   * App-owned listener declarations. Named TCP listeners are publicly routable at `<slug>--port-<name>.<domain>`; UDP listeners remain guest-only.
   */
  ports?: Array<WorkloadPort>;
  /**
   * Create-time base64-encoded favicon for the gateway edge answer; the decoded payload is capped at 32 KiB.
   */
  favicon?: string | null;
  /**
   * Create-time per-app robots.txt body; empty uses the platform allow-all default.
   */
  robots_txt?: string | null;
  /**
   * Create-time opt-in to waking a parked app for HEAD / instead of receiving the cached edge answer.
   */
  head_wakes?: boolean;
  /**
   * Policy for known monitor/crawler requests: wake the app, serve only a fresh edge cache hit, or suppress the wake.
   */
  crawler_policy?: 'wake' | 'cached' | 'block';
  /**
   * Monitor-facing health path. Empty/omitted uses /healthz.
   */
  health_path?: string;
  /**
   * Allow health probes to wake the app. Pro/Scale only; omitted uses the non-waking edge answer.
   */
  health_path_wakes?: boolean;
  /**
   * Enable best-effort cookie-based routing to the same running instance. Omitted uses false.
   */
  session_affinity?: boolean;
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
   * Desired paused warm-pool size (issue #1056 / ADR-074). Omit for the default of zero; non-zero values require Hobby+ and may not exceed max_concurrency.
   */
  warm_pool_size?: number;
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

