/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorEvidenceWindow } from './RouteMonitorEvidenceWindow.js';
/**
 * One violated route and selected signal captured at incident opening or escalation. Customer-only opening violations are scoped to the affected request-time identity; escalation evidence is aggregate and contains no customer identity.
 */
export type RouteMonitorEvidence = {
  customer_group_by?: 'tenant' | 'consumer';
  /**
   * Affected request-time identity UUID; included only on explicitly opted-in reads.
   */
  customer_id?: string;
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  path: string;
  signal: 'errors' | 'latency';
  windows: Array<RouteMonitorEvidenceWindow>;
};

