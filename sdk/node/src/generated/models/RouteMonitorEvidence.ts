/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorEvidenceWindow } from './RouteMonitorEvidenceWindow.js';
/**
 * One violated route and selected signal captured in the opening evaluation. At most three route/signal entries are saved in configuration order, errors before latency.
 */
export type RouteMonitorEvidence = {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  path: string;
  signal: 'errors' | 'latency';
  windows: Array<RouteMonitorEvidenceWindow>;
};

