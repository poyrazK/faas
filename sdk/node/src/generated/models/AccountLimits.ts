/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Plan-driven quota and resource caps: max RAM per app, concurrent wakes, total deployed apps, included GB-hours, and writable ephemeral app-disk capacity.
 */
export type AccountLimits = {
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  ram_mb: number;
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
};

