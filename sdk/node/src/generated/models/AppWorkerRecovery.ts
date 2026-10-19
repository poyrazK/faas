/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppWorkerPoolObservation } from './AppWorkerPoolObservation.js';
/**
 * Read-only app-wide worker retry status and last scheduler pool observations. Environment selection filters pools; retry status remains app-wide. Missing evidence never establishes health.
 */
export type AppWorkerRecovery = {
  available: boolean;
  /**
   * Whether the app and account permit automatic recovery.
   */
  eligible: boolean;
  scope: 'app';
  /**
   * Last app-wide recovery outcome; unknown and not_observed indicate missing evidence.
   */
  status: string;
  reason?: string;
  failures: number;
  next_attempt_at?: string;
  lease_until?: string;
  updated_at?: string;
  pools: Array<AppWorkerPoolObservation>;
};

