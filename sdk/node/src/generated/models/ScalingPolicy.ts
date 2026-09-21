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
   * Single-signal form, superseded by `targets` (ADR-194) and still accepted: it is read as a one-element list. Closed metric set: rps | cpu | concurrent_requests | queue_depth. queue_depth is a per-worker backlog budget and is valid for job/worker apps. Empty/null = engine falls back to the legacy autoscale_target_rps / autoscale_target_cpu_pct columns. Worker-class apps reject concurrent_requests with 422 scaling_target_incompatible_with_workload_class (PR-D carve-out). Mutually exclusive with `targets` — setting both is 422.
   */
  target?: (null | ScalingTarget);
  /**
   * Multi-signal autoscaling (ADR-194). Each entry states how much load ONE instance should carry on that metric; the platform evaluates every entry independently and provisions for the largest resulting instance count. The combination rule, the windowing and the cooldowns are platform policy and are not configurable per metric — declaring the signals is the whole surface. Each metric may appear at most once, every value must be > 0, and cpu is a percentage capped at 100. Mutually exclusive with `target`.
   */
  targets?: Array<ScalingTarget>;
  /**
   * Minimum seconds between two scale-out events. Floor 1 (no 0 traps); ceiling 3600 (1 h). Out-of-range → 422 invalid_cooldown.
   */
  scale_out_cooldown_s?: number;
  /**
   * Minimum seconds between two scale-in events. Floor 5 (matches the reaper's 5 s idle window); ceiling 86400 (1 day). Out-of-range → 422 invalid_cooldown.
   */
  scale_in_cooldown_s?: number;
  /**
   * Behavior when the app concurrency boundary is saturated. queue waits up to max_queue_wait_ms; drop returns 429 immediately. Empty uses queue.
   */
  concurrency_overflow?: 'queue' | 'drop';
  /**
   * Maximum admission wait in milliseconds. 0 uses the plan default; capped at 120000.
   */
  max_queue_wait_ms?: number;
  /**
   * Per-app cold-wake waiter cap. 0 uses the plan default; positive values are capped at 8x the plan default.
   */
  wake_max_queue_depth?: number;
  /**
   * Per-app cold-wake wait budget in seconds. 0 uses the plan default; capped at 60 seconds.
   */
  wake_max_queue_wait_seconds?: number;
};

