/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRouteRegression } from './ProfileRouteRegression.js';
/**
 * One retained route CPU/request comparison from a periodic monitor.
 */
export type ProfilePeriodicObservation = {
  id: string;
  status: 'baseline_pinned' | 'baseline_expired' | 'regressed' | 'no_regression_detected' | 'inconclusive';
  reason: string;
  checked_at: string;
  baseline: ProfileQuery;
  candidate: ProfileQuery;
  route_check?: ProfileRouteRegression;
  incident_id?: string;
  transition?: 'profile.route_regressed' | 'profile.route_recovered';
  investigation_id?: string;
  comparison_url?: string;
};

