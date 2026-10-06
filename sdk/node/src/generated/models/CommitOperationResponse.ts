/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationEffectRecord } from './OperationEffectRecord.js';
/**
 * Retained Commit acceptance and execution facts after managed Operation cleanup.
 */
export type CommitOperationResponse = {
  id: string;
  receipt_id: string;
  source_id: string;
  event_id: string;
  state: 'accepted' | 'pending' | 'running' | 'completed' | 'cancelled' | 'failed' | 'expired' | 'unknown';
  /**
   * Business result of a completed managed Commit operation; any JSON value.
   */
  result?: any;
  effects?: Array<OperationEffectRecord>;
  accepted_at: string;
  completed_at?: string;
};

