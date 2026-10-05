/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthInvestigationSide } from './RouteHealthInvestigationSide.js';
import type { RouteHealthLatencyDiagnostics } from './RouteHealthLatencyDiagnostics.js';
/**
 * Saved bounded request examples and optional one-sided dependency diagnostics for an opening window.
 */
export type RouteMonitorEvidenceWindow = {
  start: string;
  end: string;
  requests: RouteHealthInvestigationSide;
  /**
   * Candidate describes the monitored deployment. Stable is empty; dependency groups are one-sided with no delta. Percentiles are separate retained populations and are not additive or proof of cause.
   */
  diagnostics?: RouteHealthLatencyDiagnostics;
};

