/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScalingPolicy } from './ScalingPolicy.js';
/**
 * Per-app scalar patch. Pointer-aware: nil = "don't touch";
 * explicit zero / explicit value = "set to this". Matches
 * [UpdateAppRequest] semantics but exposes only the fields
 * the engine computes against (no PublicAuth /
 * OverflowNode).
 *
 */
export type DiffAppConfigPatch = {
  ram_mb?: number;
  vcpu?: number;
  cpu_millicores?: 250 | 500 | 1000;
  idle_timeout_s?: number;
  max_concurrency?: number;
  min_instances?: number;
  egress_allowlist?: Array<string>;
  autoscale_target_rps?: number;
  autoscale_target_cpu_pct?: number;
  streaming_enabled?: boolean;
  websocket_enabled?: boolean;
  require_signed?: boolean;
  warm_snapshot_enabled?: boolean;
  require_authn?: boolean;
  eviction_priority?: 'normal' | 'batch' | 'latency';
  /**
   * Per-app wire-protocol selector (ADR-124). Same closed set + plan gate as UpdateAppRequest.app_protocol. Pointer-aware: omitted → no change; non-null → set to this value.
   */
  app_protocol?: 'http1' | 'http2' | 'grpc';
  /**
   * Lifecycle mode. Omitted → no change; worker runs as a long-lived non-HTTP workload.
   */
  execution_mode?: 'request' | 'service' | 'worker' | 'job';
  /**
   * Supervisor restart policy. Omitted → no change.
   */
  restart_policy?: 'no' | 'on-failure' | 'always' | 'unless-stopped';
  /**
   * Maximum startup deadline in seconds; zero inherits the plan default.
   */
  startup_deadline_s?: number | null;
  /**
   * Maximum lifecycle restart attempts; zero inherits the plan default.
   */
  max_retries?: number | null;
  /**
   * Per-app request wall-clock timeout in seconds; zero inherits the plan/type default.
   */
  request_timeout_s?: number | null;
  /**
   * Replica policy for service mode.
   */
  service_replicas?: any | null;
  /**
   * Per-app scaling policy. Omitted → no change. Non-null → atomic full-overwrite of the app scaling policy.
   */
  scaling_policy?: (null | ScalingPolicy);
};

