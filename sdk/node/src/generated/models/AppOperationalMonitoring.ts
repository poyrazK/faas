/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOperationalIncident } from './AppOperationalIncident.js';
/**
 * Recent default-scope route-monitor evaluation with observed-only coverage. Unknown or unavailable health is independent of deployment smoke verification.
 */
export type AppOperationalMonitoring = {
  available: boolean;
  status: string;
  reason: string;
  coverage: string;
  deployment_id?: string;
  checked_at?: string;
  window_start?: string;
  window_end?: string;
  incidents_available: boolean;
  incident?: AppOperationalIncident;
};

