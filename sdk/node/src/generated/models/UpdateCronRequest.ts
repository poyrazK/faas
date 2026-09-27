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
  /**
   * Replace the retry allowance for an existing deployment-command cron.
   */
  retry_max?: number | null;
  /**
   * Replace the base retry delay for an existing deployment-command cron; the delay doubles after each failed attempt.
   */
  retry_backoff_seconds?: number | null;
};

