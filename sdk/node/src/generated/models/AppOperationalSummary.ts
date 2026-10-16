/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthResponse } from './AppHealthResponse.js';
import type { AppOperationalMonitoring } from './AppOperationalMonitoring.js';
import type { AppOperationalRecommendation } from './AppOperationalRecommendation.js';
import type { AppOperationalRecovery } from './AppOperationalRecovery.js';
/**
 * Read-only current production route health, open incident metadata and pending recovery work, shared by inspect and the app dashboard.
 */
export type AppOperationalSummary = {
  scope?: string;
  project_id?: string;
  environment_id?: string;
  health?: AppHealthResponse;
  version: 1;
  app_id: string;
  checked_at: string;
  monitoring: AppOperationalMonitoring;
  recovery: AppOperationalRecovery;
  recommendations: Array<AppOperationalRecommendation>;
};

