/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial cron update.
 */
export type UpdateCronRequest = {
  schedule?: string | null;
  path?: string | null;
  enabled?: boolean | null;
  /**
   * IANA timezone; an empty value resets to UTC.
   */
  timezone?: string | null;
  /**
   * Enable or disable overlap skipping for scheduled fires.
   */
  skip_if_running?: boolean | null;
};

