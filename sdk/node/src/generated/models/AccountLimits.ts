/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TriggerKind } from './TriggerKind.js';
/**
 * Plan-driven quota, resource caps, and trigger capabilities returned by GET /v1/account.
 */
export type AccountLimits = {
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  ram_mb: number;
  /**
   * Plan-derived guest vCPU topology. Free/Hobby/Pro use 2; Scale uses 4. This is informational on account reads.
   */
  vcpu: number;
  max_concurrency: number;
  deployed_apps: number;
  /**
   * Maximum live `gregale dev` environments for this plan.
   */
  developer_apps: number;
  included_gb_hours: number;
  app_layer_max_mb: number;
  /**
   * Maximum writable ephemeral app-disk capacity per app, in MB. This is the same physical drive1 cap historically named app_layer_max_mb.
   */
  ephemeral_disk_max_mb: number;
  /**
   * Whether the plan permits external event triggers.
   */
  triggers_allowed: boolean;
  /**
   * External trigger kinds this plan may create. Cron schedules use the dedicated crons API and are not included.
   */
  trigger_kinds: Array<TriggerKind>;
  trigger_limit_per_app: number;
  trigger_limit_per_account: number;
  trigger_batch_size_max: number;
  /**
   * Maximum batching window in milliseconds.
   */
  trigger_batch_window_max_ms: number;
  trigger_max_attempts_max: number;
  trigger_payload_max_bytes: number;
  /**
   * Whether Kafka tls.skip_verify=true is permitted.
   */
  trigger_tls_skip_verify_allowed: boolean;
};

