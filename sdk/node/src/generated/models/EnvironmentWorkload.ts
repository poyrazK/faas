/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppManifest } from './AppManifest.js';
import type { EnvironmentJobSchedule } from './EnvironmentJobSchedule.js';
import type { EnvironmentJobSmoke } from './EnvironmentJobSmoke.js';
import type { EnvironmentPolicy } from './EnvironmentPolicy.js';
import type { EnvironmentQueueBinding } from './EnvironmentQueueBinding.js';
import type { EnvironmentQueueSmoke } from './EnvironmentQueueSmoke.js';
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
  /**
   * Reviewed synthetic JSON input for each enabled push worker queue binding. Qualification sends it directly to the private candidate VM and never enqueues a customer message.
   */
  queue_smoke?: Record<string, EnvironmentQueueSmoke>;
  job_smoke?: EnvironmentJobSmoke;
  schedule?: EnvironmentJobSchedule;
  /**
   * Explicit recovery of retained queues, keyed by a declared binding name and pinned to its original scoped binding UUID. Requires reviewed adoption before reconciliation can resume retained work; adoption itself preserves the retirement hold.
   */
  queue_recoveries?: Record<string, string>;
  service_bindings?: Record<string, EnvironmentServiceBinding>;
};

