/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthInvestigationSide } from './RouteHealthInvestigationSide.js';
import type { RouteHealthLatencyDiagnostics } from './RouteHealthLatencyDiagnostics.js';
/**
 * Independently bounded candidate and stable diagnostic rows within one exact health observation window.
 */
export type RouteHealthInvestigationWindow = {
  start: string;
  end: string;
  candidate: RouteHealthInvestigationSide;
  stable: RouteHealthInvestigationSide;
  diagnostics?: RouteHealthLatencyDiagnostics;
};

