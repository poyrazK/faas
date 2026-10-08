/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorDeploymentBaseline } from './RouteMonitorDeploymentBaseline.js';
import type { RouteMonitorEvidence } from './RouteMonitorEvidence.js';
import type { RouteMonitorIncidentEscalation } from './RouteMonitorIncidentEscalation.js';
import type { RouteMonitorIncidentTimelineEntry } from './RouteMonitorIncidentTimelineEntry.js';
import type { RouteMonitorReport } from './RouteMonitorReport.js';
/**
 * Saved opening evidence, the previous healthy deployment when known, bounded route-impact timeline, transition-linked escalation evidence, optional comparable recovery report and explicit incident lifecycle. Opening evidence stays fixed even when telemetry expires. Timeline entries contain aggregate customer counts only and preserve the opening baseline plus the newest evaluations.
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
  /**
   * Saved last-known healthy deployment. Absent for legacy incidents or when no different prior healthy deployment is available.
   */
  baseline?: RouteMonitorDeploymentBaseline;
  opening_report: RouteMonitorReport;
  recovery_report?: RouteMonitorReport;
  evidence: Array<RouteMonitorEvidence>;
  evidence_truncated: boolean;
  /**
   * Opening baseline and up to 59 most recent confirmed evaluations. Route indexes refer to opening_report.routes.
   */
  timeline?: Array<RouteMonitorIncidentTimelineEntry>;
  /**
   * True when older evaluations were dropped to preserve the opening baseline and bounded incident size.
   */
  timeline_truncated?: boolean;
  /**
   * Newest newly violated route/signal transitions with bounded evidence captured at each transition.
   */
  escalations?: Array<RouteMonitorIncidentEscalation>;
  /**
   * True when older escalation records were dropped to preserve the newest transition details and incident size.
   */
  escalations_truncated?: boolean;
};

