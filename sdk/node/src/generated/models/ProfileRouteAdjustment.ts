/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRouteWeight } from './ProfileRouteWeight.js';
/**
 * Common request mix uses the mean of stable/canary route shares. Requires matching route sets, no unattributed CPU, sufficient collection coverage and at least 20 requests per route on each side. This does not alter canary outcomes.
 */
export type ProfileRouteAdjustment = {
  available: boolean;
  reason: string;
  baseline_cpu_seconds_per_request?: number;
  candidate_cpu_seconds_per_request?: number;
  delta_cpu_seconds_per_request?: number;
  weights: Array<ProfileRouteWeight>;
};

