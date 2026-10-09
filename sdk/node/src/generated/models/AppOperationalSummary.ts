/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOperationalMonitoring } from './AppOperationalMonitoring.js';
import type { AppOperationalRecommendation } from './AppOperationalRecommendation.js';
import type { AppOperationalRecovery } from './AppOperationalRecovery.js';
/**
 * Read-only current production route health, open incident metadata and pending recovery work, shared by inspect and the app dashboard.
 */
export type AppOperationalSummary = {
  version: 1;
  app_id: string;
  checked_at: string;
  monitoring: AppOperationalMonitoring;
  recovery: AppOperationalRecovery;
  recommendations: Array<AppOperationalRecommendation>;
};

