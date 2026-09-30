/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppManifest } from './AppManifest.js';
import type { EnvironmentPolicy } from './EnvironmentPolicy.js';
import type { EnvironmentQueueBinding } from './EnvironmentQueueBinding.js';
import type { EnvironmentRouteContract } from './EnvironmentRouteContract.js';
import type { EnvironmentServiceBinding } from './EnvironmentServiceBinding.js';
import type { EnvironmentWorkloadSource } from './EnvironmentWorkloadSource.js';
/**
 * Logical workload intent; unsupported adapters are reported as blocking reasons.
 */
export type EnvironmentWorkload = {
  app?: string;
  source?: EnvironmentWorkloadSource;
  runtime?: AppManifest;
  variables?: Record<string, string>;
  secret_refs?: Record<string, string>;
  routes?: EnvironmentRouteContract;
  policies?: Array<EnvironmentPolicy>;
  queue_bindings?: Record<string, EnvironmentQueueBinding>;
  service_bindings?: Record<string, EnvironmentServiceBinding>;
};

