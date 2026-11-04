/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingRuntimeDeployment } from './BindingRuntimeDeployment.js';
/**
 * App-wide timestamp-based configuration freshness, independent of canary verification. This is not guest acknowledgement, readiness or proof of credential use. Scope filtering selects deployments; task guests, jobs and mirrors are excluded.
 */
export type BindingRuntimeFreshness = {
  /**
   * Currently instance_started_at.
   */
  source: string;
  observed_at: string;
  /**
   * Latest app-wide environment or secret change. Without a stamp, resident freshness is unknown.
   */
  config_changed_at?: string;
  deployments: Array<BindingRuntimeDeployment>;
};

