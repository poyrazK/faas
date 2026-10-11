/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationEffectRecord } from './OperationEffectRecord.js';
/**
 * Public receipt. Accepted request contents, claim tokens, and renewal credentials are never returned.
 */
export type ExclusiveOperationRecord = {
  id: string;
  app_id?: string;
  job_id?: string;
  platform_tenant_id?: string;
  sequence: number;
  state: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';
  policy_revision: number;
  /**
   * Monotonically increasing fencing generation for this coordination key.
   */
  generation: number;
  lease_expires_at?: string;
  attempt_deadline?: string;
  /**
   * Platform-committed invocation result; arbitrary JSON value.
   */
  result?: any;
  effects?: Array<OperationEffectRecord>;
  last_error?: string;
  created_at: string;
  completed_at?: string;
};

