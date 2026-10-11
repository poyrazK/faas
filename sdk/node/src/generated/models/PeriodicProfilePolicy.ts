/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in monitoring of live completed deployments and explicit routes. Interval must be a whole number of minutes and at least window_seconds. Each route pins its first evidence-qualified window on this deployment and policy revision. Both regression and recovery need this many consecutive supported, distinct-window results; missing evidence interrupts confirmation. Expired baselines start a new context without recovering the old incident.
 */
export type PeriodicProfilePolicy = {
  interval_seconds: number;
  confirmations: number;
};

