/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The resource, scaling, rate, and timeout envelope currently applied to an app. Values are resolved from the app configuration and current plan; they describe enforcement rather than guest hardware alone.
 */
export type AppEffectiveLimits = {
  /**
   * Memory limit configured for this app instance, in MB.
   */
  memory_limit_mb: number;
  /**
   * Largest memory limit the current plan permits for an app instance, in MB.
   */
  plan_memory_max_mb: number;
  /**
   * Number of processors visible inside the guest. This is distinct from the sustained CPU cgroup limit.
   */
  guest_vcpus: number;
  /**
   * Sustained per-instance CPU allowance derived from cpu.max, expressed in millicores.
   */
  cpu_limit_millicores: number;
  /**
   * Relative cgroup CPU scheduling weight applied when the host is contended.
   */
  cpu_weight: number;
  /**
   * Effective per-app live-instance ceiling after applying the scaling policy.
   */
  max_instances: number;
  /**
   * Maximum in-flight requests accepted by one instance. Handler-level concurrency remains the application's responsibility.
   */
  concurrency_per_instance: number;
  /**
   * Per-app edge token-bucket refill rate, in requests per second.
   */
  app_request_rate_rps: number;
  /**
   * Per-app edge token-bucket burst capacity.
   */
  app_request_burst: number;
  /**
   * Account-wide edge token-bucket refill rate across all apps, in requests per minute.
   */
  account_request_rate_rpm: number;
  /**
   * Default end-to-end request budget before a route override, in milliseconds.
   */
  request_budget_ms: number;
  /**
   * Maximum end-to-end request budget allowed through route overrides, in milliseconds.
   */
  request_budget_max_ms: number;
  /**
   * Maximum response write window for the plan, in seconds.
   */
  response_write_timeout_s: number;
};

