/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublicStatusDaily } from './PublicStatusDaily.js';
/**
 * Current and 30-day status summary for one public capability.
 */
export type PublicStatusComponent = {
  id: 'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability';
  name: string;
  status: 'operational' | 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage' | 'unknown';
  uptime_30d_pct: number | null;
  coverage_30d_pct: number;
  daily: Array<PublicStatusDaily>;
};

