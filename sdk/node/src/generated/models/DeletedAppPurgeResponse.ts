/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Outcome of an owner's purge. The restore window is closed either way.
 */
export type DeletedAppPurgeResponse = {
  account_id: string;
  id: string;
  slug: string;
  /**
   * True once the app is gone and its slug is free for this account.
   */
  purged: boolean;
  /**
   * What still holds the app while purged is false; the grace sweep retries.
   */
  pending_reason?: 'environment_clone' | 'exclusive_work' | 'foreign_key' | 'managed_postgres_binding' | 'multipart_write' | 'object_bucket' | 'retained_resources' | 'retry_pending';
};

