/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardOperationTarget } from './ApplicationStandardOperationTarget.js';
/**
 * Saved operation progress. Persisted targets still require consumer observation; this response never advances it.
 */
export type ApplicationStandardOperation = {
  id: string;
  org_id: string;
  plan_id: string;
  assignment_id: string;
  approval_hash: string;
  approved_by: string;
  batch_size: number;
  state: 'queued' | 'running' | 'waiting' | 'paused' | 'blocked' | 'failed' | 'completed';
  error_code?: string;
  targets: Array<ApplicationStandardOperationTarget>;
  created_at: string;
  updated_at: string;
};

