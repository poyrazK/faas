/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Plan-driven quota and resource caps: max RAM per app, concurrent wakes, total deployed apps, included GB-hours, and max app-layer bytes per build.
 */
export type AccountLimits = {
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  ram_mb: number;
  max_concurrency: number;
  deployed_apps: number;
  included_gb_hours: number;
  app_layer_max_mb: number;
};

