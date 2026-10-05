/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucketObjectLockConfiguration } from './ObjectBucketObjectLockConfiguration.js';
/**
 * Owned durable intent and native observation with permanent enablement history and reconciliation progress.
 */
export type ObjectBucketObjectLock = {
  bucket_id: string;
  state: 'waiting' | 'applying' | 'ready';
  revision: number;
  /**
   * Permanent protection latch; enablement and version accounting cannot be disabled.
   */
  enabled_required: boolean;
  observed_known: boolean;
  observed_configuration?: ObjectBucketObjectLockConfiguration;
  desired_configuration?: ObjectBucketObjectLockConfiguration;
  last_error_code?: 'versioning_pending' | 'untracked_writes' | 'unsettled_writes' | 'multipart_active' | 'capacity_active' | 'provider_failed' | 'provider_mismatch' | 'provider_unsupported';
  updated_at: string;
};

