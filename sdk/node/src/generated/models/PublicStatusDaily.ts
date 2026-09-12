/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC day's status, uptime, and telemetry coverage observation.
 */
export type PublicStatusDaily = {
  date: string;
  status: 'operational' | 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage' | 'unknown';
  uptime_pct: number | null;
  coverage_pct: number;
};

