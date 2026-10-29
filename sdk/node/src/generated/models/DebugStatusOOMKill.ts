/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One out-of-memory kill of an app instance. A deployment that was not yet live fails; a live release keeps serving on a new instance.
 */
export type DebugStatusOOMKill = {
  at: string;
  deployment_id: string;
  peak_mb: number;
  plan_mb: number;
  deployment_failed: boolean;
};

