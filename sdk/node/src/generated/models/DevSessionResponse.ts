/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResponse } from './AppResponse.js';
import type { DevBucketResponse } from './DevBucketResponse.js';
import type { DevPostgresResponse } from './DevPostgresResponse.js';
import type { DevWatch } from './DevWatch.js';
/**
 * Stable remote developer workspace and its renewable lease.
 */
export type DevSessionResponse = {
  app: AppResponse;
  expires_at: string;
  postgres?: DevPostgresResponse;
  /**
   * Active automatic bucket bindings, including resources omitted from the latest upsert. Included only for principals with storage:manage or admin. Revoked bindings are excluded.
   */
  buckets?: Array<DevBucketResponse>;
  watch?: DevWatch;
  /**
   * The stored Go command; omitted when none.
   */
  go_command?: string;
};

