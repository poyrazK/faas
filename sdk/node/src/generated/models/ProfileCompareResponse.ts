/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionComparison } from './ProfileAttributionComparison.js';
import type { ProfileFunctionDelta } from './ProfileFunctionDelta.js';
import type { ProfileResponse } from './ProfileResponse.js';
import type { ProfileRouteAdjustment } from './ProfileRouteAdjustment.js';
import type { ProfileStackDelta } from './ProfileStackDelta.js';
/**
 * Deployment CPU rate differences with a compatibility decision.
 */
export type ProfileCompareResponse = {
  readonly attribution?: ProfileAttributionComparison;
  route_adjustment?: ProfileRouteAdjustment;
  baseline: ProfileResponse;
  candidate: ProfileResponse;
  functions: Array<ProfileFunctionDelta>;
  comparable: boolean;
  reason?: string;
  flamegraph?: ProfileStackDelta;
  /**
   * Explains unavailable differential data while preserving a valid function comparison.
   */
  flamegraph_reason?: string;
};

