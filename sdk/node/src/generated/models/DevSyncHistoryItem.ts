/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevSyncPhase } from './DevSyncPhase.js';
/**
 * One redacted developer sync receipt.
 */
export type DevSyncHistoryItem = {
  deployment_id: string;
  status: 'live' | 'failed';
  edit_to_live_ms: number;
  slo_target_ms: number;
  within_slo: boolean;
  phases: Array<DevSyncPhase>;
  created_at: string;
};

