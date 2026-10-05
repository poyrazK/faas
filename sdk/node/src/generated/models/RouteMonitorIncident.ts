/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorEvidence } from './RouteMonitorEvidence.js';
import type { RouteMonitorReport } from './RouteMonitorReport.js';
/**
 * Saved opening evidence, optional comparable recovery report and explicit incident lifecycle. Opening evidence stays fixed even when telemetry expires.
 */
export type RouteMonitorIncident = {
  version: number;
  id: string;
  app_id: string;
  deployment_id: string;
  revision: number;
  status: 'open' | 'recovered' | 'superseded';
  opened_at: string;
  closed_at?: string;
  opening_report: RouteMonitorReport;
  recovery_report?: RouteMonitorReport;
  evidence: Array<RouteMonitorEvidence>;
  evidence_truncated: boolean;
};

