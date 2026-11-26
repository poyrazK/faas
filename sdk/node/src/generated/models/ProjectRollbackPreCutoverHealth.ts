/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable outcome of configured HTTP probes of the prepared revision before cutover.
 */
export type ProjectRollbackPreCutoverHealth = {
  /**
   * One of pending, awaiting_probe, passed, or failed.
   */
  status: string;
  attempt_id?: string;
  checked_at?: string;
};

