/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Present on a route health report read while the enforced guard holds the canary: when the hold began (its first blocking decision) and when the hold deadline aborts the canary if the rollout worker still finds it held.
 */
export type RouteHealthHold = {
  since: string;
  aborts_at: string;
};

