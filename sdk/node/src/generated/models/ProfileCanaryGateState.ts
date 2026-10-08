/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCanaryGatePolicy } from './ProfileCanaryGatePolicy.js';
import type { ProfileGateRouteStreak } from './ProfileGateRouteStreak.js';
import type { ProfileQuery } from './ProfileQuery.js';
export type ProfileCanaryGateState = {
  policy: ProfileCanaryGatePolicy;
  status: 'collecting' | 'passed' | 'regressed' | 'timed_out' | 'inconclusive';
  reason: string;
  deadline: string;
  windows: number;
  last_window_end?: string;
  streaks: Array<ProfileGateRouteStreak>;
  /**
   * Separate next capture selection. Retained code evidence always refers to the signal's baseline and candidate queries.
   */
  next_candidate?: ProfileQuery;
};

