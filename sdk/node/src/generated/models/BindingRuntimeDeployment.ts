/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingRuntimeInstanceCounts } from './BindingRuntimeInstanceCounts.js';
/**
 * A live deployment or one retaining resident app instances. Serving counts running rows; resident includes serving, starting, warm, snapshotting, draining and migrating rows. Parked and terminal rows are excluded.
 */
export type BindingRuntimeDeployment = {
  deployment_id: string;
  scope: string;
  deployment_status: string;
  /**
   * Stale residents take precedence, then unknown residents, then starting instances. Current does not imply connectivity or readiness.
   */
  status: 'current' | 'stale' | 'unknown' | 'updating' | 'inactive';
  serving: BindingRuntimeInstanceCounts;
  resident: BindingRuntimeInstanceCounts;
  /**
   * Waking or cold-booting instances, also included in resident counts.
   */
  starting: number;
};

