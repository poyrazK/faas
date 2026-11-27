/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertAnomalyDeploymentContext } from './AlertAnomalyDeploymentContext.js';
import type { AlertAnomalyRecoveryComparison } from './AlertAnomalyRecoveryComparison.js';
import type { AlertObservedIncident } from './AlertObservedIncident.js';
/**
 * Anomaly incidents reconstructed from a selected two-hour interval.
 */
export type AlertAnomalyIncidentReplay = {
  cooldown_minutes: number;
  /**
   * Incidents for the selected multiplier, newest first. An incident is open at the end of the replay when ended_at is absent.
   */
  incidents: Array<AlertObservedIncident>;
  /**
   * Incidents for the candidate, newest first. Omitted when the candidate has no incidents or is absent.
   */
  candidate_incidents?: Array<AlertObservedIncident>;
  recovery_confirmation?: AlertAnomalyRecoveryComparison;
  deployment_context?: AlertAnomalyDeploymentContext;
};

