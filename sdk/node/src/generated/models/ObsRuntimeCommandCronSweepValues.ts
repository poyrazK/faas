/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit scan-in-progress bit, last actual scan start and last scan that completed listing and owner lookups successfully, including empty scans. Start and success are absent before observation. Running requires an observed start. An idle scan does not imply success or disabled cron intent; no new progress deadline is inferred.
 */
export type ObsRuntimeCommandCronSweepValues = {
  running: boolean;
  last_started_at?: string;
  last_success_at?: string;
};

