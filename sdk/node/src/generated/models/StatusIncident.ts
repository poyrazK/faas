/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public operator-authored status incident.
 */
export type StatusIncident = {
  component?: string;
  started_at: string;
  resolved_at: string | null;
  severity: 'degraded' | 'partial_outage' | 'full_outage' | 'maintenance';
  summary: string;
};

