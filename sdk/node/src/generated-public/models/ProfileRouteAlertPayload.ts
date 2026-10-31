/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCallPathFrame } from './ProfileCallPathFrame.js';
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRouteRegression } from './ProfileRouteRegression.js';
/**
 * Advisory app webhook evidence. Application frames are aggregate hotspots and do not establish route-level code causality. Paths are relative to Gregale; raw profile samples are excluded.
 */
export type ProfileRouteAlertPayload = {
  /**
   * Dashboard link with the exact route, baseline and candidate windows and selected route call path.
   */
  comparison_url?: string;
  version: 1;
  app_id: string;
  deployment_id: string;
  baseline_deployment_id: string;
  policy_revision: number;
  incident_id: string;
  status: 'regressed' | 'recovered';
  source: 'deployment' | 'canary' | 'periodic';
  checked_at: string;
  baseline: ProfileQuery;
  candidate: ProfileQuery;
  route_check: ProfileRouteRegression;
  application_frames?: Array<ProfileCallPathFrame>;
  evidence_path: string;
  investigation_path?: string;
};

