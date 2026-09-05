/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app scalar patch. Pointer-aware: nil = "don't touch";
 * explicit zero / explicit value = "set to this". Matches
 * [UpdateAppRequest] semantics but exposes only the fields
 * the engine computes against (no ScalingPolicy /
 * PublicAuth / OverflowNode).
 *
 */
export type DiffAppConfigPatch = {
  ram_mb?: number;
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
};

