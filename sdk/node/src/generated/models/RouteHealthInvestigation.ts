/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthFinding } from './RouteHealthFinding.js';
import type { RouteHealthInvestigationSelection } from './RouteHealthInvestigationSelection.js';
import type { RouteHealthInvestigationWindow } from './RouteHealthInvestigationWindow.js';
import type { RouteHealthReport } from './RouteHealthReport.js';
/**
 * Shareable read-only investigation. The full aggregate report preserves rollout health context; finding is the selected aggregate or customer-specific route. Status and reason describe the selected 5xx or watched-code signal, independently of rollout decisions. Examples reference retained metadata only. Missing stable comparisons return explicit unavailable evidence and zero rows. Coverage stays observed_only.
 */
export type RouteHealthInvestigation = {
  version: number;
  selection: RouteHealthInvestigationSelection;
  report: RouteHealthReport;
  finding: RouteHealthFinding;
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
  coverage: 'observed_only';
  evidence_status: 'observed' | 'unavailable';
  examples_limit: number;
  windows: Array<RouteHealthInvestigationWindow>;
};

