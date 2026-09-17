/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevSyncPhase } from './DevSyncPhase.js';
/**
 * Redacted edit-to-live receipt written by the developer CLI.
 */
export type RecordDevSyncRequest = {
  workspace_id: string;
  deployment_id: string;
  status: 'live' | 'failed';
  edit_to_live_ms: number;
  slo_target_ms: number;
  within_slo: boolean;
  phases: Array<DevSyncPhase>;
};

