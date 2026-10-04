/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectLifecycleExpiration } from './ObjectLifecycleExpiration.js';
import type { ObjectLifecycleFilter } from './ObjectLifecycleFilter.js';
import type { ObjectLifecycleNoncurrentExpiration } from './ObjectLifecycleNoncurrentExpiration.js';
/**
 * At least one action is required. Omitted IDs receive deterministic IDs. Tag filters cannot be combined with multipart abort or expired delete marker actions.
 */
export type ObjectLifecycleRule = {
  /**
   * Unique rule ID; generated when omitted or empty.
   */
  id?: string;
  status: 'Enabled' | 'Disabled';
  filter?: ObjectLifecycleFilter;
  expiration?: ObjectLifecycleExpiration;
  noncurrent_version_expiration?: ObjectLifecycleNoncurrentExpiration;
  abort_incomplete_multipart_days?: number;
};

