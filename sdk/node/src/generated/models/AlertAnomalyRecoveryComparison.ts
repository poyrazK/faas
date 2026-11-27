/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertObservedIncident } from './AlertObservedIncident.js';
/**
 * Replay outcomes with two consecutive healthy checks required for recovery.
 */
export type AlertAnomalyRecoveryComparison = {
  required_healthy_checks: 2;
  /**
   * Incidents for the selected multiplier with recovery confirmation, newest first.
   */
  incidents: Array<AlertObservedIncident>;
  /**
   * Incidents for the candidate with recovery confirmation, newest first. Omitted when the candidate has no incidents or is absent.
   */
  candidate_incidents?: Array<AlertObservedIncident>;
};

