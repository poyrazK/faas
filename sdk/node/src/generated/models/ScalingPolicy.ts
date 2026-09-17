/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScalingTarget } from './ScalingTarget.js';
/**
 * Per-app autoscaling configuration (issue #462 / ADR-058). Mirrors the on-disk jsonb column `apps.scaling_policy`. Empty values map to the engine default (the apid gate is load-bearing for the floor / ceiling, not the encoder). PR-A persists the DTO; PR-C wires the engine; PR-D carves out the worker-class branch.
 */
export type ScalingPolicy = {
  /**
   * Per-app cold-wake floor. 0 = scale to zero (default). Hobby+ unlocked at PR-A (was Pro/Scale pre-#462). Free → 403 plan_min_instances_not_allowed.
   */
  min_instances?: number;
  /**
   * Per-app ceiling on live instances. Must be in [min_instances, plan.MaxConcurrency]. Hobby+ unlocked at PR-A. Free → 403 plan_max_instances_not_allowed. 0 = use plan max_concurrency.
   */
  max_instances?: number;
  /**
   * Per-instance signal the engine watches for the scale-up trigger. Closed metric set: rps | concurrent_requests | queue_depth | p99_latency_ms. queue_depth is a per-worker backlog budget and is valid for job/worker apps. Empty/null = engine falls back to the legacy autoscale_target_rps / autoscale_target_cpu_pct columns. Worker-class apps reject concurrent_requests with 422 scaling_target_incompatible_with_workload_class (PR-D carve-out).
   */
  target?: (null | ScalingTarget);
  /**
   * Minimum seconds between two scale-out events. Floor 1 (no 0 traps); ceiling 3600 (1 h). Out-of-range → 422 invalid_cooldown.
   */
  scale_out_cooldown_s?: number;
  /**
   * Minimum seconds between two scale-in events. Floor 5 (matches the reaper's 5 s idle window); ceiling 86400 (1 day). Out-of-range → 422 invalid_cooldown.
   */
  scale_in_cooldown_s?: number;
};

